package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// Gestión de usuarios por la plataforma (PATCH /platform/users/{user_id} y
// PUT /platform/users/{user_id}/platform-roles; platform.users.manage con
// re-autenticación reciente, que comprueban el gateway y el handler).
//
// Decisiones:
//   - Bloquearse a sí mismo es un 422 de campo (status: CANNOT_DISABLE_SELF):
//     es una petición mal formada para quien la hace, no un conflicto de estado.
//   - Nadie cambia sus propios roles de plataforma (403 PERMISSION_DENIED,
//     docs/api.md §2.8: "nadie se asigna a sí mismo").
//   - Nunca queda la instalación sin un platform_admin activo (409
//     LAST_PLATFORM_ADMIN), comprobado en la base bajo un cerrojo común.
//   - Bloquear revoca al momento todas las sesiones de la persona (evento
//     horus.auth.session.revoked con reason user_disabled; el gateway deja de
//     aceptar sus tokens en la siguiente petición y cierra sus WebSocket).
//   - Quitar roles de plataforma revoca sus sesiones (reason roles_changed): un
//     token de plataforma lleva sus permisos hasta caducar. Añadir roles no
//     revoca: el siguiente token de plataforma ya los trae.
//   - Los roles de plataforma exigen 2FA: quien los recibe debe activar TOTP
//     (POST /me/totp/enroll y /confirm) antes de obtener un token de plataforma
//     (403 MFA_ENROLLMENT_REQUIRED hasta entonces). La cuenta queda con
//     mfa_enforced.
//   - No se emiten horus.auth.user.disabled/enabled: no están en el catálogo de
//     eventos; queda la auditoría de plataforma.

// PermPlatformUsersManage es el permiso de gestión de usuarios.
const PermPlatformUsersManage = "platform.users.manage"

// Códigos de error de usuarios de plataforma.
const (
	CodeLastPlatformAdmin = "LAST_PLATFORM_ADMIN"
	CodeCannotDisableSelf = "CANNOT_DISABLE_SELF"
)

// platformCaller devuelve el principal de un token de plataforma de
// persona con perm (defensa en profundidad tras el guard).
func platformCaller(ctx context.Context, perm string) (*authz.Principal, error) {
	p := authz.FromContext(ctx)
	if p == nil || p.Type != authz.TypeUser || p.Scope != authz.ScopePlatform {
		return nil, apperr.Forbidden(problem.CodeTokenScopeInvalid, "")
	}
	if !p.Has(perm) {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	return p, nil
}

func errLastPlatformAdmin() error {
	return apperr.Conflict(CodeLastPlatformAdmin, "La instalación debe conservar al menos un platform_admin activo.")
}

func (s *Service) platformUser(ctx context.Context, id uuid.UUID) (*domain.PlatformUser, error) {
	u, err := s.store.PlatformUser(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, apperr.NotFound(problem.CodeNotFound)
	}
	return u, err
}

func (s *Service) recordPlatform(ctx context.Context, p *authz.Principal, action, resourceID, ip string, changes map[string]any) {
	_ = s.Record(ctx, api.AuditEntry{
		TenantID: uuid.Nil, ActorType: p.Type, ActorID: p.Subject, Action: action, ResourceType: "user",
		ResourceID: resourceID, Scope: "platform", Outcome: "success", IP: ip, Changes: changes,
	})
}

// SetUserStatus es PATCH /platform/users/{user_id} {status: active|disabled}.
// Idempotente: pedir el estado que ya tiene devuelve el usuario sin cambios.
func (s *Service) SetUserStatus(ctx context.Context, id uuid.UUID, body map[string]json.RawMessage, requestIP string) (*domain.PlatformUser, error) {
	p, err := platformCaller(ctx, PermPlatformUsersManage)
	if err != nil {
		return nil, err
	}
	u, err := s.platformUser(ctx, id)
	if err != nil {
		return nil, err
	}
	var errs []problem.FieldError
	var status string
	for k, raw := range body {
		if k != "status" {
			errs = append(errs, apperr.Field(k, "UNKNOWN_FIELD", "Campo no editable."))
			continue
		}
		if json.Unmarshal(raw, &status) != nil || (status != domain.UserActive && status != domain.UserDisabled) {
			errs = append(errs, apperr.Field("status", "INVALID_VALUE", "active o disabled."))
		}
	}
	if _, ok := body["status"]; !ok {
		errs = append(errs, apperr.Field("status", "REQUIRED", "active o disabled."))
	}
	if len(errs) > 0 {
		return nil, apperr.Validation(errs...)
	}
	disable := status == domain.UserDisabled
	if disable && id == p.UserID {
		return nil, apperr.Validation(apperr.Field("status", CodeCannotDisableSelf, "No puede bloquear su propia cuenta."))
	}
	if (disable && u.Status == domain.UserDisabled) || (!disable && u.Status != domain.UserDisabled) {
		return u, nil
	}
	res, err := s.store.SetUserStatus(ctx, UserStatusChange{UserID: id, Disable: disable, Actor: s.actor(p), Now: s.now()})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil, apperr.NotFound(problem.CodeNotFound)
	case errors.Is(err, ErrLastPlatformAdmin):
		return nil, errLastPlatformAdmin()
	case err != nil:
		return nil, err
	}
	s.clearSessionCache()
	s.recordPlatform(ctx, p, "platform.user.status_changed", id.String(), requestIP, map[string]any{
		"previous_status": res.Previous, "status": res.Status, "revoked_sessions": len(res.Revoked),
	})
	return s.platformUser(ctx, id)
}

// platformRoleKeys son los roles de plataforma admitidos (enum del contrato).
func (s *Service) platformRoleKeys() []string {
	out := make([]string, 0, len(s.o.Catalog.PlatformRoles))
	for _, r := range s.o.Catalog.PlatformRoles {
		out = append(out, r.Key)
	}
	return out
}

// ReplacePlatformRoles es PUT /platform/users/{user_id}/platform-roles.
func (s *Service) ReplacePlatformRoles(ctx context.Context, id uuid.UUID, roles []string, present bool, requestIP string) (*domain.PlatformUser, error) {
	p, err := platformCaller(ctx, PermPlatformUsersManage)
	if err != nil {
		return nil, err
	}
	u, err := s.platformUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, apperr.Validation(apperr.Field("platform_roles", "REQUIRED", "Array de roles de plataforma ([] los quita)."))
	}
	valid := s.platformRoleKeys()
	var errs []problem.FieldError
	out := make([]string, 0, len(roles))
	for i, r := range roles {
		at := "platform_roles[" + strconv.Itoa(i) + "]"
		switch {
		case !slices.Contains(valid, r):
			errs = append(errs, apperr.Field(at, "INVALID_VALUE", "platform_admin, platform_operator o platform_auditor."))
		case slices.Contains(out, r):
			errs = append(errs, apperr.Field(at, "DUPLICATE", "Rol repetido."))
		default:
			out = append(out, r)
		}
	}
	if len(errs) > 0 {
		return nil, apperr.Validation(errs...)
	}
	slices.Sort(out)
	if id == p.UserID {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "Nadie cambia sus propios roles de plataforma.")
	}
	if slices.Equal(out, u.PlatformRoles) {
		return u, nil
	}
	res, err := s.store.ReplacePlatformRoles(ctx, PlatformRolesChange{UserID: id, Roles: out, GrantedBy: p.UserID, Actor: s.actor(p), Now: s.now()})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil, apperr.NotFound(problem.CodeNotFound)
	case errors.Is(err, ErrLastPlatformAdmin):
		return nil, errLastPlatformAdmin()
	case err != nil:
		return nil, err
	}
	s.clearSessionCache()
	s.recordPlatform(ctx, p, "platform.user.roles_replaced", id.String(), requestIP, map[string]any{
		"previous": orEmptyList(res.Previous), "platform_roles": out, "revoked_sessions": len(res.Revoked),
	})
	return s.platformUser(ctx, id)
}
