package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// Permisos de miembros, roles y usuarios (catálogo C7).
const (
	PermUsersRead         = "users.read"
	PermUsersManage       = "users.manage"
	PermRolesRead         = "roles.read"
	PermRolesAssign       = "roles.assign"
	PermPlatformUsersRead = "platform.users.read"
)

// Códigos de error de campo propios.
const (
	// CodeInvitationInvalid: token de invitación desconocido, usado o caducado
	// (misma respuesta en todos los casos).
	CodeInvitationInvalid = "INVITATION_INVALID"
	CodeUnknownRole       = "UNKNOWN_ROLE"
	CodeNotAMember        = "NOT_A_MEMBER"
)

// Estados válidos del filtro de miembros.
var memberStatuses = []string{domain.MembershipActive, domain.MembershipInvited, domain.MembershipRevoked}

// AssignmentInput es RoleAssignment del contrato (role_key es informativo y
// se ignora en la entrada).
type AssignmentInput struct {
	RoleID  string  `json:"role_id"`
	RoleKey *string `json:"role_key,omitempty"`
	Scope   string  `json:"scope"`
}

// tenantPrincipal devuelve el principal y el tenant de un token de ISP de
// persona con perm (defensa en profundidad tras el guard).
func tenantPrincipal(ctx context.Context, perm string) (*authz.Principal, uuid.UUID, error) {
	p := authz.FromContext(ctx)
	tid, err := authz.TenantOf(ctx)
	if err != nil || p.Type == authz.TypeKiosk {
		return nil, uuid.Nil, apperr.Forbidden(problem.CodeTokenScopeInvalid, "")
	}
	if !p.Has(perm) {
		return nil, uuid.Nil, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	return p, tid, nil
}

// clearSessionCache olvida el estado de sesiones y membresías cacheado
// (un cambio de membresía corta el acceso sin esperar al TTL).
func (s *Service) clearSessionCache() {
	s.mu.Lock()
	s.cache = map[string]cachedStatus{}
	s.mu.Unlock()
}

func assignmentsData(as []domain.Assignment) []map[string]any {
	out := make([]map[string]any, 0, len(as))
	for _, a := range as {
		out = append(out, map[string]any{"role_id": a.RoleID.String(), "scope": a.Scope()})
	}
	return out
}

func assignmentScopes(as []domain.Assignment) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.RoleKey+"@"+a.Scope())
	}
	return out
}

// memberEvent construye horus.auth.membership.<typ> (C4, Membership).
func memberEvent(actor Actor, tenant uuid.UUID, typ, reason string, now time.Time) MemberEvent {
	return func(m domain.Member, previous []domain.Assignment) OutboxEvent {
		tid := tenant
		var why any
		if reason != "" {
			why = reason
		}
		return OutboxEvent{
			ID: uuid.Must(uuid.NewV7()), Type: "horus.auth.membership." + typ, TenantID: &tid, AggregateType: "membership",
			AggregateID: m.MembershipID, AggregateVersion: m.Version, Actor: actor, OccurredAt: now,
			Data: map[string]any{
				"id": m.MembershipID.String(), "user_id": m.UserID.String(), "version": m.Version, "status": m.Status,
				"assignments": assignmentsData(m.Assignments), "previous_assignments": assignmentsData(previous), "reason": why,
			},
		}
	}
}

// resolveAssignments valida las asignaciones pedidas contra los roles
// visibles en el tenant y aplica la regla anti-escalada (security.md §2,
// amenaza E): nadie concede, a otros ni a sí mismo, un permiso que no tiene
// en ese alcance.
func (s *Service) resolveAssignments(ctx context.Context, p *authz.Principal, tenant uuid.UUID, items []AssignmentInput, field string) ([]domain.Assignment, error) {
	roles, err := s.store.TenantRoles(ctx, tenant)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]domain.TenantRoleRecord, len(roles))
	for _, r := range roles {
		byID[r.ID] = r
	}
	var errs []problem.FieldError
	seen := map[string]bool{}
	out := make([]domain.Assignment, 0, len(items))
	for i, it := range items {
		at := fmt.Sprintf("%s[%d]", field, i)
		id, err := uuid.Parse(it.RoleID)
		if err != nil {
			errs = append(errs, apperr.Field(at+".role_id", "INVALID_UUID", "UUID no válido."))
			continue
		}
		role, ok := byID[id]
		if !ok {
			errs = append(errs, apperr.Field(at+".role_id", CodeUnknownRole, "El rol no existe en este ISP."))
			continue
		}
		typ, sid, ok := domain.ParseScope(it.Scope)
		if !ok {
			errs = append(errs, apperr.Field(at+".scope", "INVALID_FORMAT", "tenant, site:<uuid> o router_group:<uuid>."))
			continue
		}
		a := domain.Assignment{RoleID: role.ID, RoleKey: role.Key, ScopeType: typ, ScopeID: sid, RequiresMFA: role.RequiresMFA, Permissions: role.Permissions}
		if k := a.RoleID.String() + "|" + a.Scope(); !seen[k] {
			seen[k] = true
			out = append(out, a)
		}
	}
	if len(errs) > 0 {
		return nil, apperr.Validation(errs...)
	}
	if err := checkGrantable(p, out, "conceder"); err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(x, y domain.Assignment) int {
		if c := strings.Compare(x.RoleKey, y.RoleKey); c != 0 {
			return c
		}
		return strings.Compare(x.Scope(), y.Scope())
	})
	return out, nil
}

// checkGrantable aplica la regla anti-escalada a unas asignaciones: p debe
// tener cada permiso de cada rol en su alcance.
func checkGrantable(p *authz.Principal, as []domain.Assignment, verb string) error {
	for _, a := range as {
		scope := ""
		if a.ScopeType != "tenant" {
			scope = a.PermScope()
		}
		for _, perm := range a.Permissions {
			if !p.HasScope(perm, scope) {
				return apperr.Forbidden(problem.CodePermissionDenied,
					fmt.Sprintf("No puede %s el rol %s: incluye %s, que usted no tiene en ese alcance.", verb, a.RoleKey, perm))
			}
		}
	}
	return nil
}

func (s *Service) recordTenant(ctx context.Context, p *authz.Principal, tenant uuid.UUID, action, resourceType, resourceID, ip string, changes map[string]any) {
	_ = s.Record(ctx, api.AuditEntry{
		TenantID: tenant, ActorType: p.Type, ActorID: p.Subject, ViaPlatform: p.ViaPlatform, Action: action,
		ResourceType: resourceType, ResourceID: resourceID, Scope: "tenant", Outcome: "success", IP: ip, Changes: changes,
	})
}

// MemberPage es una página de miembros.
type MemberPage struct {
	Data []domain.Member
	Page pagination.Page
}

// ListMembers es GET /members (keyset por created_at, id; cursor ligado al
// tenant y a los filtros).
func (s *Service) ListMembers(ctx context.Context, codec *pagination.Codec, req pagination.Request, q, status string) (*MemberPage, error) {
	_, tenant, err := tenantPrincipal(ctx, PermUsersRead)
	if err != nil {
		return nil, err
	}
	q = strings.TrimSpace(q)
	if len(q) > 128 {
		return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "q admite hasta 128 caracteres.")
	}
	if status != "" && !slices.Contains(memberStatuses, status) {
		return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "status: active, invited o revoked.")
	}
	f := MemberFilter{Q: q, Status: status, Limit: req.Limit + 1}
	fk := pagination.FilterKey(f.Q, f.Status)
	if req.Cursor != "" {
		cur, err := codec.Decode(req.Cursor, "created_at", fk, tenant.String())
		if err != nil || len(cur.Keys) != 2 {
			return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		at, err1 := time.Parse(time.RFC3339Nano, cur.Keys[0])
		id, err2 := uuid.Parse(cur.Keys[1])
		if err1 != nil || err2 != nil {
			return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		f.AfterCreated, f.AfterID = &at, id
	}
	rows, err := s.store.ListMembers(ctx, tenant, f)
	if err != nil {
		return nil, err
	}
	page := pagination.Page{Limit: req.Limit}
	if len(rows) > req.Limit {
		rows = rows[:req.Limit]
		last := rows[len(rows)-1]
		next := codec.Encode(pagination.Cursor{Keys: []string{last.CreatedAt.UTC().Format(time.RFC3339Nano), last.MembershipID.String()},
			Sort: "created_at", Filter: fk, Tenant: tenant.String()})
		page.NextCursor, page.HasMore = &next, true
	}
	if req.IncludeTotal {
		f.AfterCreated = nil
		n, err := s.store.CountMembers(ctx, tenant, f)
		if err != nil {
			return nil, err
		}
		page.Total = &n
	}
	if rows == nil {
		rows = []domain.Member{}
	}
	return &MemberPage{Data: rows, Page: page}, nil
}

// Resultados de POST /members (InvitationIssued.outcome).
const (
	OutcomeAdded   = "added"
	OutcomeInvited = "invited"
)

// InvitationIssued es la respuesta de POST /members y de
// POST /members/{user_id}/invitation. URL lleva el token en claro: solo se
// devuelve a quien invita (nunca se registra).
type InvitationIssued struct {
	Outcome   string
	UserID    uuid.UUID
	URL       *string
	ExpiresAt *time.Time
	EmailSent bool
}

// invitationURL es <HORUS_PUBLIC_BASE_URL>/invitation#token=<token>: el token
// va en el fragmento, que el navegador no envía a ningún servidor.
func (s *Service) invitationURL(token string) string {
	return s.o.PublicBaseURL + "/invitation#token=" + token
}

// InviteMember es POST /members: invita un email con roles iniciales. Si el
// email es de una cuenta activa la membresía queda activa (outcome added);
// si no, el usuario queda pendiente con la membresía invitada y una
// invitación de un uso (72 h) cuyo enlace se devuelve a quien invita y, si
// hay SMTP, se envía por correo (outcome invited). Si ya era miembro no se
// cambian sus roles (PUT /members/{user_id}/role-assignments); si seguía
// invitado, el enlace anterior deja de valer.
func (s *Service) InviteMember(ctx context.Context, email string, items []AssignmentInput, requestIP string) (*InvitationIssued, error) {
	p, tenant, err := tenantPrincipal(ctx, PermUsersManage)
	if err != nil {
		return nil, err
	}
	email = strings.TrimSpace(email)
	var errs []problem.FieldError
	addr, perr := mail.ParseAddress(email)
	if perr != nil || addr.Address != email || len(email) > 254 {
		errs = append(errs, apperr.Field("email", "INVALID_EMAIL", "Email no válido."))
	}
	if len(items) == 0 {
		errs = append(errs, apperr.Field("role_assignments", "REQUIRED", "Al menos una asignación (rol, alcance)."))
	}
	if len(items) > 50 {
		errs = append(errs, apperr.Field("role_assignments", "TOO_MANY", "Hasta 50 asignaciones."))
	}
	if len(errs) > 0 {
		return nil, apperr.Validation(errs...)
	}
	as, err := s.resolveAssignments(ctx, p, tenant, items, "role_assignments")
	if err != nil {
		return nil, err
	}
	now := s.now()
	in, token, err := s.newInvitation(tenant, email, as, p.UserID, memberEvent(s.actor(p), tenant, "granted", "", now), now)
	if err != nil {
		return nil, err
	}
	res, err := s.store.InviteMember(ctx, in)
	if err != nil {
		if errors.Is(err, ErrUnknownRole) {
			return nil, apperr.Validation(apperr.Field("role_assignments", CodeUnknownRole, "El rol no existe en este ISP."))
		}
		return nil, err
	}
	out := s.invitationIssued(ctx, in, res, token)
	s.recordTenant(ctx, p, tenant, "members.invited", "membership", res.MembershipID.String(), requestIP, map[string]any{
		"user_id": res.UserID.String(), "status": res.Status, "already_member": res.AlreadyMember,
		"user_created": res.UserCreated, "role_assignments": assignmentScopes(as),
		"link_issued": res.TokenStored, "email_sent": out.EmailSent,
	})
	return out, nil
}

// newInvitation prepara la invitación de email a tenant con un token nuevo
// de un uso (72 h): al repositorio solo llega su hash. El token en claro se
// devuelve para el enlace (nunca se registra).
func (s *Service) newInvitation(tenant uuid.UUID, email string, as []domain.Assignment, grantedBy uuid.UUID, ev MemberEvent,
	now time.Time,
) (NewInvitation, string, error) {
	token, err := domain.NewInvitationToken()
	if err != nil {
		return NewInvitation{}, "", err
	}
	return NewInvitation{
		TenantID: tenant, Email: email, Assignments: as, GrantedBy: grantedBy, TokenHash: domain.HashSecret(token),
		ExpiresAt: now.Add(domain.InvitationTTL), Now: now, Event: ev,
	}, token, nil
}

// invitationIssued construye InvitationIssued a partir de lo que hizo el
// repositorio con in y, si guardó el token, entrega el enlace por correo
// (tras el commit).
func (s *Service) invitationIssued(ctx context.Context, in NewInvitation, res *InviteResult, token string) *InvitationIssued {
	out := &InvitationIssued{Outcome: OutcomeAdded, UserID: res.UserID}
	if res.Status == domain.MembershipInvited {
		out.Outcome = OutcomeInvited
	}
	if res.TokenStored {
		u, exp := s.invitationURL(token), in.ExpiresAt
		out.URL, out.ExpiresAt = &u, &exp
		out.EmailSent = s.deliverInvitation(ctx, InvitationDelivery{Email: in.Email, TenantID: in.TenantID, Token: token, URL: u, ExpiresAt: exp},
			res.MembershipID)
	}
	return out
}

// ReissueInvitation es POST /members/{user_id}/invitation: enlace nuevo para
// un miembro invitado (los anteriores sin usar dejan de valer). 404 si no es
// miembro de este ISP; 409 si no está invitado. Anti-escalada: quien lo
// renueva debe tener todos los permisos de los roles del invitado (el enlace
// da acceso a la cuenta pendiente).
func (s *Service) ReissueInvitation(ctx context.Context, userID uuid.UUID, requestIP string) (*InvitationIssued, error) {
	p, tenant, err := tenantPrincipal(ctx, PermUsersManage)
	if err != nil {
		return nil, err
	}
	cur, err := s.store.Member(ctx, tenant, userID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && cur.Status == domain.MembershipRevoked) {
		return nil, apperr.NotFound(problem.CodeNotFound)
	}
	if err != nil {
		return nil, err
	}
	if cur.Status != domain.MembershipInvited {
		return nil, errNotInvited()
	}
	if err := checkGrantable(p, cur.Assignments, "reenviar la invitación con"); err != nil {
		return nil, err
	}
	token, err := domain.NewInvitationToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	exp := now.Add(domain.InvitationTTL)
	res, err := s.store.ReissueInvitation(ctx, ReissuedInvitation{TenantID: tenant, UserID: userID, TokenHash: domain.HashSecret(token),
		ExpiresAt: exp, CreatedBy: p.UserID, Now: now})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil, apperr.NotFound(problem.CodeNotFound)
	case errors.Is(err, ErrNotInvited):
		return nil, errNotInvited()
	case err != nil:
		return nil, err
	}
	u := s.invitationURL(token)
	out := &InvitationIssued{Outcome: OutcomeInvited, UserID: userID, URL: &u, ExpiresAt: &exp}
	out.EmailSent = s.deliverInvitation(ctx, InvitationDelivery{Email: res.Email, TenantID: tenant, Token: token, URL: u, ExpiresAt: exp}, res.MembershipID)
	s.recordTenant(ctx, p, tenant, "members.invitation_reissued", "membership", res.MembershipID.String(), requestIP, map[string]any{
		"user_id": userID.String(), "replaced": res.Replaced, "email_sent": out.EmailSent,
	})
	return out, nil
}

func errNotInvited() error {
	return apperr.Conflict(problem.CodeConflict, "El miembro no está pendiente de aceptar una invitación.")
}

// deliverInvitation envía el enlace por el canal configurado (síncrono, con
// plazo) y dice si se entregó. Un fallo nunca falla la petición: se registra
// sin la dirección, el token ni el enlace.
func (s *Service) deliverInvitation(ctx context.Context, d InvitationDelivery, membershipID uuid.UUID) bool {
	if s.o.Invitations == nil {
		s.o.Logger.InfoContext(ctx, "invitation link issued without email delivery", slog.String("membership_id", membershipID.String()))
		return false
	}
	if t, err := s.store.Tenant(ctx, d.TenantID); err == nil {
		d.TenantName = t.Name
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.o.InvitationSendTimeout)
	defer cancel()
	if err := s.o.Invitations.SendInvitation(sctx, d); err != nil {
		msg := err.Error()
		for _, secret := range []string{d.URL, d.Token, d.Email} {
			if secret != "" {
				msg = strings.ReplaceAll(msg, secret, "***")
			}
		}
		s.o.Logger.WarnContext(ctx, "invitation email not sent", slog.String("membership_id", membershipID.String()), slog.String("error", msg))
		return false
	}
	return true
}

// RemoveMember es DELETE /members/{user_id}: revoca la membresía (no borra
// al usuario de la plataforma) y emite horus.auth.membership.revoked para
// que el gateway corte el acceso a ese tenant.
func (s *Service) RemoveMember(ctx context.Context, userID uuid.UUID, requestIP string) error {
	p, tenant, err := tenantPrincipal(ctx, PermUsersManage)
	if err != nil {
		return err
	}
	now := s.now()
	ok, err := s.store.RevokeMember(ctx, tenant, userID, now, memberEvent(s.actor(p), tenant, "revoked", "removed_by_tenant_admin", now))
	if err != nil {
		return err
	}
	if !ok {
		return apperr.NotFound(problem.CodeNotFound)
	}
	s.clearSessionCache()
	s.recordTenant(ctx, p, tenant, "members.removed", "user", userID.String(), requestIP, map[string]any{"user_id": userID.String()})
	return nil
}

// ReplaceAssignments es PUT /members/{user_id}/role-assignments (If-Match
// sobre la versión de la membresía). body es el array RoleAssignment[]; un
// miembro inexistente responde 404 antes de validar el cuerpo.
func (s *Service) ReplaceAssignments(ctx context.Context, userID uuid.UUID, ifMatch int, body json.RawMessage, requestIP string) (*domain.Member, error) {
	p, tenant, err := tenantPrincipal(ctx, PermRolesAssign)
	if err != nil {
		return nil, err
	}
	cur, err := s.store.Member(ctx, tenant, userID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && cur.Status == domain.MembershipRevoked) {
		return nil, apperr.NotFound(problem.CodeNotFound)
	}
	if err != nil {
		return nil, err
	}
	var items []AssignmentInput
	if json.Unmarshal(body, &items) != nil || items == nil {
		return nil, apperr.Validation(apperr.Field("body", "INVALID_TYPE", "Se espera un array de asignaciones {role_id, scope}."))
	}
	if len(items) > 50 {
		return nil, apperr.Validation(apperr.Field("body", "TOO_MANY", "Hasta 50 asignaciones."))
	}
	as, err := s.resolveAssignments(ctx, p, tenant, items, "")
	if err != nil {
		return nil, err
	}
	now := s.now()
	var previous []domain.Assignment
	inner := memberEvent(s.actor(p), tenant, "updated", "", now)
	m, err := s.store.ReplaceAssignments(ctx, tenant, userID, ifMatch, as, now, func(m domain.Member, prev []domain.Assignment) OutboxEvent {
		previous = prev
		return inner(m, prev)
	})
	var verr *MemberVersionError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil, apperr.NotFound(problem.CodeNotFound)
	case errors.As(err, &verr):
		return nil, apperr.PreconditionFailed(verr.Current)
	case errors.Is(err, ErrUnknownRole):
		return nil, apperr.Validation(apperr.Field("", CodeUnknownRole, "El rol no existe en este ISP."))
	case err != nil:
		return nil, err
	}
	s.clearSessionCache()
	s.recordTenant(ctx, p, tenant, "members.role_assignments.replaced", "membership", m.MembershipID.String(), requestIP, map[string]any{
		"user_id": userID.String(), "previous": assignmentScopes(previous), "assignments": assignmentScopes(m.Assignments),
	})
	return m, nil
}

// ListRoles es GET /roles: plantillas de sistema + roles propios del tenant.
func (s *Service) ListRoles(ctx context.Context) ([]domain.TenantRoleRecord, error) {
	_, tenant, err := tenantPrincipal(ctx, PermRolesRead)
	if err != nil {
		return nil, err
	}
	roles, err := s.store.TenantRoles(ctx, tenant)
	if err != nil {
		return nil, err
	}
	if roles == nil {
		roles = []domain.TenantRoleRecord{}
	}
	return roles, nil
}

// AcceptInvitationInput es el cuerpo de POST /auth/invitations/accept.
type AcceptInvitationInput struct {
	Token       string  `json:"token"`
	DisplayName *string `json:"display_name"`
	Password    *string `json:"password"`
}

func errInvitationInvalid() error {
	return apperr.Validation(apperr.Field("token", CodeInvitationInvalid, "La invitación no es válida, ya se usó o caducó."))
}

// InvitationInfo es la respuesta de POST /auth/invitations/inspect.
type InvitationInfo struct {
	Email         string
	TenantName    string
	NeedsPassword bool
	ExpiresAt     time.Time
}

// usableInvitation resuelve un token de invitación vigente (sin tenant: se
// busca por hash con auth_platform). Desconocido, usado, caducado, de una
// membresía que ya no está invitada o de una cuenta que no puede aceptarla:
// la misma respuesta 422.
func (s *Service) usableInvitation(ctx context.Context, token string, now time.Time) (*domain.Invitation, error) {
	if len(token) < 32 || len(token) > 256 {
		return nil, errInvitationInvalid()
	}
	inv, err := s.store.InvitationByHash(ctx, domain.HashSecret(token))
	if errors.Is(err, domain.ErrNotFound) {
		return nil, errInvitationInvalid()
	}
	if err != nil {
		return nil, err
	}
	if !inv.Usable(now) || (inv.UserStatus != domain.UserPending && inv.UserStatus != domain.UserActive) {
		return nil, errInvitationInvalid()
	}
	return inv, nil
}

// InspectInvitation es POST /auth/invitations/inspect (público): los datos
// de la página de aceptación sin consumir la invitación.
func (s *Service) InspectInvitation(ctx context.Context, token string) (*InvitationInfo, error) {
	inv, err := s.usableInvitation(ctx, token, s.now())
	if err != nil {
		return nil, err
	}
	return &InvitationInfo{Email: inv.UserEmail, TenantName: inv.TenantName, NeedsPassword: inv.UserStatus == domain.UserPending,
		ExpiresAt: inv.ExpiresAt}, nil
}

// AcceptInvitation canjea una invitación (un uso): activa la membresía y,
// si el usuario estaba pendiente, lo activa con la contraseña elegida. Un
// usuario que ya existía conserva su contraseña y su nombre (el token no
// sirve para cambiarlos).
func (s *Service) AcceptInvitation(ctx context.Context, in AcceptInvitationInput, li LoginInput) error {
	now := s.now()
	inv, err := s.usableInvitation(ctx, in.Token, now)
	if err != nil {
		return err
	}
	acc := AcceptedInvitation{Invitation: *inv, Now: now}
	if inv.UserStatus == domain.UserPending {
		name := inv.UserDisplayName
		var errs []problem.FieldError
		if in.DisplayName != nil {
			name = strings.TrimSpace(*in.DisplayName)
			if name == "" || len([]rune(name)) > 120 {
				errs = append(errs, apperr.Field("display_name", "INVALID_VALUE", "Nombre de 1 a 120 caracteres."))
			}
		}
		switch {
		case in.Password == nil || *in.Password == "":
			errs = append(errs, apperr.Field("password", "REQUIRED", "Elija una contraseña para activar la cuenta."))
		default:
			if code := domain.ValidateNewPassword(*in.Password, inv.UserEmail, name); code != "" {
				errs = append(errs, apperr.Field("password", code, "La contraseña no cumple la política (12–128 caracteres, no común, sin datos personales)."))
			}
		}
		if len(errs) > 0 {
			return apperr.Validation(errs...)
		}
		h, err := domain.HashPassword(*in.Password, s.o.Argon)
		if err != nil {
			return err
		}
		acc.PasswordHash, acc.DisplayName = h, name
	}
	actor := Actor{Type: "user", ID: inv.UserID.String()}
	acc.Event = memberEvent(actor, inv.TenantID, "updated", "invitation_accepted", now)
	if err := s.store.AcceptInvitation(ctx, acc); err != nil {
		if errors.Is(err, ErrInvitationUsed) {
			return errInvitationInvalid()
		}
		return err
	}
	s.clearSessionCache()
	_ = s.Record(ctx, api.AuditEntry{
		TenantID: inv.TenantID, ActorType: "user", ActorID: inv.UserID.String(), Action: "auth.invitation.accepted",
		ResourceType: "membership", ResourceID: inv.MembershipID.String(), Scope: "tenant", Outcome: "success",
		IP: li.IP, UserAgent: li.UserAgent, Changes: map[string]any{"user_activated": acc.PasswordHash != ""},
	})
	return nil
}

var localeRe = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$`)

// UpdateMe es PATCH /me (JSON Merge Patch de MePatch).
func (s *Service) UpdateMe(ctx context.Context, p *authz.Principal, patch map[string]json.RawMessage, requestIP string) (*MeView, error) {
	_, u, err := s.sessionFor(ctx, p, "", s.now())
	if err != nil {
		return nil, err
	}
	var errs []problem.FieldError
	var changed []string
	nullable := func(raw json.RawMessage) (*string, bool) {
		var v *string
		if json.Unmarshal(raw, &v) != nil {
			return nil, false
		}
		return v, true
	}
	for k, raw := range patch {
		switch k {
		case "display_name":
			var v string
			if json.Unmarshal(raw, &v) != nil || strings.TrimSpace(v) == "" || len([]rune(v)) > 120 {
				errs = append(errs, apperr.Field(k, "INVALID_VALUE", "Nombre de 1 a 120 caracteres."))
				continue
			}
			u.DisplayName = strings.TrimSpace(v)
		case "locale":
			v, ok := nullable(raw)
			if !ok || (v != nil && !localeRe.MatchString(*v)) {
				errs = append(errs, apperr.Field(k, "INVALID_FORMAT", "Etiqueta de idioma BCP 47 (p. ej. es-MX) o null."))
				continue
			}
			u.Locale = v
		case "timezone":
			v, ok := nullable(raw)
			if !ok || (v != nil && !validTimezone(*v)) {
				errs = append(errs, apperr.Field(k, "INVALID_TIMEZONE", "Zona horaria IANA o null."))
				continue
			}
			u.Timezone = v
		case "default_tenant_id":
			var v *uuid.UUID
			if json.Unmarshal(raw, &v) != nil {
				errs = append(errs, apperr.Field(k, "INVALID_UUID", "UUID o null."))
				continue
			}
			if v != nil {
				ms, err := s.store.Memberships(ctx, u.ID)
				if err != nil {
					return nil, err
				}
				if !slices.ContainsFunc(ms, func(m domain.Membership) bool { return m.Tenant.ID == *v }) {
					errs = append(errs, apperr.Field(k, CodeNotAMember, "No es miembro de ese ISP."))
					continue
				}
			}
			u.DefaultTenantID = v
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
	if len(changed) > 0 {
		if err := s.store.UpdateProfile(ctx, u, s.now()); err != nil {
			return nil, err
		}
		slices.Sort(changed)
		_ = s.Record(ctx, api.AuditEntry{ActorType: "user", ActorID: u.ID.String(), Action: "auth.profile.updated",
			ResourceType: "user", ResourceID: u.ID.String(), IP: requestIP, Changes: map[string]any{"fields": changed}})
	}
	return s.Me(ctx, p)
}

// PlatformUserPage es una página de usuarios de la plataforma.
type PlatformUserPage struct {
	Data []domain.PlatformUser
	Page pagination.Page
}

// ListPlatformUsers es GET /platform/users (keyset por created_at, id).
func (s *Service) ListPlatformUsers(ctx context.Context, codec *pagination.Codec, req pagination.Request, q string) (*PlatformUserPage, error) {
	q = strings.TrimSpace(q)
	if len(q) > 128 {
		return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "q admite hasta 128 caracteres.")
	}
	f := UserFilter{Q: q, Limit: req.Limit + 1}
	fk := pagination.FilterKey(f.Q)
	if req.Cursor != "" {
		cur, err := codec.Decode(req.Cursor, "users.created_at", fk, "")
		if err != nil || len(cur.Keys) != 2 {
			return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		at, err1 := time.Parse(time.RFC3339Nano, cur.Keys[0])
		id, err2 := uuid.Parse(cur.Keys[1])
		if err1 != nil || err2 != nil {
			return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		f.AfterCreated, f.AfterID = &at, id
	}
	rows, err := s.store.ListUsers(ctx, f)
	if err != nil {
		return nil, err
	}
	page := pagination.Page{Limit: req.Limit}
	if len(rows) > req.Limit {
		rows = rows[:req.Limit]
		last := rows[len(rows)-1]
		next := codec.Encode(pagination.Cursor{Keys: []string{last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID.String()},
			Sort: "users.created_at", Filter: fk})
		page.NextCursor, page.HasMore = &next, true
	}
	if rows == nil {
		rows = []domain.PlatformUser{}
	}
	return &PlatformUserPage{Data: rows, Page: page}, nil
}
