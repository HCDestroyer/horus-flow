package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// Roles propios del ISP (POST /roles, PATCH y DELETE /roles/{role_id}).
//
// Los permisos de un rol se resuelven al emitir cada token de tenant
// (POST /auth/token y su refresco): un cambio de permisos de un rol llega a
// sus miembros en su siguiente token, como mucho tras la vida del access token
// (HORUS_AUTH_ACCESS_TTL, 10 min por defecto). La caché de sesiones del
// gateway se vacía igualmente al cambiar un rol.
//
// No se emiten eventos horus.auth.role.*: no están en el catálogo de eventos
// (packages/events/catalog/auth.yaml); los cambios quedan en la auditoría del
// tenant (roles.created/updated/deleted).

// PermRolesManage es el permiso de los roles propios.
const PermRolesManage = "roles.manage"

// Códigos de error de roles (ErrorCode del contrato).
const (
	CodeRoleNotFound      = "ROLE_NOT_FOUND"
	CodeRoleReadOnly      = "ROLE_READ_ONLY"
	CodeRoleInUse         = "ROLE_IN_USE"
	CodeUnknownPermission = "UNKNOWN_PERMISSION"
)

// RoleInput es RoleCreate del contrato.
type RoleInput struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description *string  `json:"description"`
	Permissions []string `json:"permissions"`
}

func errRoleNotFound() error { return apperr.NotFound(CodeRoleNotFound) }

func errRoleReadOnly() error {
	return apperr.New(apperr.KindInvalid, CodeRoleReadOnly, "Los roles de sistema no se editan ni se borran.")
}

func validRoleName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	n := len([]rune(name))
	return name, n >= 1 && n <= 120
}

// normDescription recorta una descripción ("" = null).
func normDescription(d *string) (*string, bool) {
	if d == nil {
		return nil, true
	}
	v := strings.TrimSpace(*d)
	if len([]rune(v)) > 500 {
		return nil, false
	}
	if v == "" {
		return nil, true
	}
	return &v, true
}

// rolePermissions valida una lista de permisos para un rol propio: claves de
// permisos de tenant del catálogo, sin duplicados, al menos una. Devuelve la
// lista ordenada y si alguno exige 2FA (requires_mfa del rol, como en las
// plantillas de sistema).
func (s *Service) rolePermissions(field string, perms []string) ([]string, bool, []problem.FieldError) {
	var errs []problem.FieldError
	if len(perms) == 0 {
		return nil, false, []problem.FieldError{apperr.Field(field, "REQUIRED", "Al menos un permiso.")}
	}
	if len(perms) > 200 {
		return nil, false, []problem.FieldError{apperr.Field(field, "TOO_MANY", "Demasiados permisos.")}
	}
	seen := map[string]bool{}
	mfa := false
	out := make([]string, 0, len(perms))
	for i, k := range perms {
		at := fmt.Sprintf("%s[%d]", field, i)
		p, ok := s.o.Catalog.Permission(k)
		switch {
		case !ok || p.Family != "tenant":
			errs = append(errs, apperr.Field(at, CodeUnknownPermission, "No es un permiso de ISP del catálogo (GET /permissions)."))
			continue
		case seen[k]:
			errs = append(errs, apperr.Field(at, "DUPLICATE", "Permiso repetido."))
			continue
		}
		seen[k] = true
		mfa = mfa || p.RequiresMFA
		out = append(out, k)
	}
	if len(errs) > 0 {
		return nil, false, errs
	}
	slices.Sort(out)
	return out, mfa, nil
}

// checkRolePermissions aplica la regla anti-escalada (security.md §2, amenaza
// E) a un rol propio: quien lo crea o lo amplía debe tener cada permiso nuevo
// en todo el tenant (`*`), porque el rol puede asignarse con alcance tenant.
func checkRolePermissions(p *authz.Principal, perms []string) error {
	for _, perm := range perms {
		if !p.TenantWide(perm) {
			return apperr.Forbidden(problem.CodePermissionDenied,
				fmt.Sprintf("No puede dar a un rol el permiso %s: usted no lo tiene en todo el ISP.", perm))
		}
	}
	return nil
}

// CreateRole es POST /roles.
func (s *Service) CreateRole(ctx context.Context, in RoleInput, requestIP string) (*domain.TenantRoleRecord, error) {
	p, tenant, err := tenantPrincipal(ctx, PermRolesManage)
	if err != nil {
		return nil, err
	}
	var errs []problem.FieldError
	key := strings.TrimSpace(in.Key)
	if !domain.RoleKeyRe.MatchString(key) {
		errs = append(errs, apperr.Field("key", "INVALID_FORMAT", "Minúsculas, dígitos y guion bajo; empieza por letra (2–63)."))
	}
	name, ok := validRoleName(in.Name)
	if !ok {
		errs = append(errs, apperr.Field("name", "INVALID_VALUE", "Nombre de 1 a 120 caracteres."))
	}
	desc, ok := normDescription(in.Description)
	if !ok {
		errs = append(errs, apperr.Field("description", "INVALID_VALUE", "Hasta 500 caracteres."))
	}
	perms, mfa, perrs := s.rolePermissions("permissions", in.Permissions)
	errs = append(errs, perrs...)
	if len(errs) > 0 {
		return nil, apperr.Validation(errs...)
	}
	if _, sys := s.o.Catalog.TenantRole(key); sys {
		return nil, errRoleKeyTaken()
	}
	if _, sys := s.o.Catalog.PlatformRole(key); sys {
		return nil, errRoleKeyTaken()
	}
	if err := checkRolePermissions(p, perms); err != nil {
		return nil, err
	}
	tid := tenant
	r := &domain.TenantRoleRecord{ID: uuid.Must(uuid.NewV7()), TenantID: &tid, Key: key, Name: name, Description: desc,
		RequiresMFA: mfa, Permissions: perms, Version: 1}
	if err := s.store.CreateRole(ctx, tenant, r); err != nil {
		if errors.Is(err, ErrRoleKeyTaken) {
			return nil, errRoleKeyTaken()
		}
		return nil, err
	}
	s.recordTenant(ctx, p, tenant, "roles.created", "role", r.ID.String(), requestIP, map[string]any{
		"key": r.Key, "name": r.Name, "permissions": r.Permissions, "requires_mfa": r.RequiresMFA,
	})
	return r, nil
}

func errRoleKeyTaken() error {
	return apperr.Conflict(problem.CodeAlreadyExists, "Ya existe un rol con esa clave en este ISP (o es la de un rol de sistema).")
}

// customRole devuelve el rol propio id del tenant: 404 ROLE_NOT_FOUND si no
// es visible en el tenant, 422 ROLE_READ_ONLY si es una plantilla.
func (s *Service) customRole(ctx context.Context, tenant, id uuid.UUID) (*domain.TenantRoleRecord, error) {
	r, err := s.store.TenantRole(ctx, tenant, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, errRoleNotFound()
	}
	if err != nil {
		return nil, err
	}
	if r.IsSystem || r.TenantID == nil || *r.TenantID != tenant {
		return nil, errRoleReadOnly()
	}
	return r, nil
}

// UpdateRole es PATCH /roles/{role_id} (JSON Merge Patch de RolePatch con
// If-Match). ifMatch nil = falta la cabecera (428 tras comprobar que el rol
// existe y es editable).
func (s *Service) UpdateRole(ctx context.Context, id uuid.UUID, ifMatch *int, patch map[string]json.RawMessage, requestIP string) (*domain.TenantRoleRecord, error) {
	p, tenant, err := tenantPrincipal(ctx, PermRolesManage)
	if err != nil {
		return nil, err
	}
	cur, err := s.customRole(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	if ifMatch == nil {
		return nil, apperr.PreconditionRequired("Falta la cabecera If-Match con el ETag del rol.")
	}
	next := *cur
	var errs []problem.FieldError
	var changed []string
	if len(patch) == 0 {
		errs = append(errs, apperr.Field("", "REQUIRED", "Indique name, description o permissions."))
	}
	for k, raw := range patch {
		switch k {
		case "name":
			var v string
			name, ok := "", false
			if json.Unmarshal(raw, &v) == nil {
				name, ok = validRoleName(v)
			}
			if !ok {
				errs = append(errs, apperr.Field(k, "INVALID_VALUE", "Nombre de 1 a 120 caracteres."))
				continue
			}
			next.Name = name
		case "description":
			var v *string
			if json.Unmarshal(raw, &v) != nil {
				errs = append(errs, apperr.Field(k, "INVALID_VALUE", "Texto (hasta 500) o null."))
				continue
			}
			d, ok := normDescription(v)
			if !ok {
				errs = append(errs, apperr.Field(k, "INVALID_VALUE", "Hasta 500 caracteres."))
				continue
			}
			next.Description = d
		case "permissions":
			var v []string
			if json.Unmarshal(raw, &v) != nil {
				errs = append(errs, apperr.Field(k, "INVALID_TYPE", "Array de claves de permiso."))
				continue
			}
			perms, mfa, perrs := s.rolePermissions(k, v)
			if len(perrs) > 0 {
				errs = append(errs, perrs...)
				continue
			}
			next.Permissions, next.RequiresMFA = perms, mfa
		default:
			errs = append(errs, apperr.Field(k, "UNKNOWN_FIELD", "Campo no editable."))
			continue
		}
		changed = append(changed, k)
	}
	if len(errs) > 0 {
		slices.SortFunc(errs, func(a, b problem.FieldError) int { return strings.Compare(a.Field, b.Field) })
		return nil, apperr.Validation(errs...)
	}
	if *ifMatch != cur.Version {
		return nil, apperr.PreconditionFailed(cur)
	}
	added, removed := diffPerms(cur.Permissions, next.Permissions)
	if err := checkRolePermissions(p, added); err != nil {
		return nil, err
	}
	slices.Sort(changed)
	err = s.store.UpdateRole(ctx, tenant, &next, *ifMatch, s.now())
	var verr *RoleVersionError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil, errRoleNotFound()
	case errors.As(err, &verr):
		return nil, apperr.PreconditionFailed(verr.Current)
	case err != nil:
		return nil, err
	}
	if len(added)+len(removed) > 0 {
		s.clearSessionCache()
	}
	s.recordTenant(ctx, p, tenant, "roles.updated", "role", id.String(), requestIP, map[string]any{
		"key": next.Key, "changed_fields": changed, "added_permissions": orEmptyList(added), "removed_permissions": orEmptyList(removed),
		"version": next.Version,
	})
	return &next, nil
}

// DeleteRole es DELETE /roles/{role_id} (If-Match): solo roles propios sin
// asignaciones (409 ROLE_IN_USE).
func (s *Service) DeleteRole(ctx context.Context, id uuid.UUID, ifMatch *int, requestIP string) error {
	p, tenant, err := tenantPrincipal(ctx, PermRolesManage)
	if err != nil {
		return err
	}
	cur, err := s.customRole(ctx, tenant, id)
	if err != nil {
		return err
	}
	if ifMatch == nil {
		return apperr.PreconditionRequired("Falta la cabecera If-Match con el ETag del rol.")
	}
	if *ifMatch != cur.Version {
		return apperr.PreconditionFailed(cur)
	}
	if cur.AssignmentsCount > 0 {
		return errRoleInUse()
	}
	err = s.store.DeleteRole(ctx, tenant, id, *ifMatch)
	var verr *RoleVersionError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return errRoleNotFound()
	case errors.As(err, &verr):
		return apperr.PreconditionFailed(verr.Current)
	case errors.Is(err, ErrRoleInUse):
		return errRoleInUse()
	case err != nil:
		return err
	}
	s.recordTenant(ctx, p, tenant, "roles.deleted", "role", id.String(), requestIP, map[string]any{
		"key": cur.Key, "permissions": cur.Permissions,
	})
	return nil
}

func errRoleInUse() error {
	return apperr.Conflict(CodeRoleInUse, "El rol está asignado; quite antes sus asignaciones (PUT /members/{user_id}/role-assignments).")
}

// diffPerms devuelve los permisos añadidos y quitados de before a after.
func diffPerms(before, after []string) (added, removed []string) {
	for _, p := range after {
		if !slices.Contains(before, p) {
			added = append(added, p)
		}
	}
	for _, p := range before {
		if !slices.Contains(after, p) {
			removed = append(removed, p)
		}
	}
	return added, removed
}

func orEmptyList(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
