package app

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// Códigos del autologin (contrato docs/integrations/argosisp.md §1.5).
const (
	CodeSSOTicketInvalid      = "SSO_TICKET_INVALID"
	CodeSSOLinkInvalid        = "SSO_LINK_INVALID"
	CodeSSOLinkExists         = "SSO_LINK_EXISTS"
	CodeSSOLinkNotFound       = "SSO_LINK_NOT_FOUND"
	CodeSSOAccountUnavailable = "SSO_ACCOUNT_UNAVAILABLE"
	CodeSSONotMember          = "SSO_NOT_A_MEMBER"
	ssoPurgeEvery             = 10 * time.Minute
	ssoTicketRetention        = 24 * time.Hour
)

// ErrSSOLinkTaken: el usuario externo ya está vinculado en ese conector.
var ErrSSOLinkTaken = errors.New("auth: sso link already exists")

// SSOStore es el repositorio del autologin.
type SSOStore interface {
	CreateSSOTicket(ctx context.Context, t *domain.SSOTicket) error
	// RedeemSSOTicket marca usado (un uso, CAS) el ticket vigente con ese
	// hash y lo devuelve; domain.ErrNotFound si no existe, caducó o ya se usó.
	RedeemSSOTicket(ctx context.Context, hash []byte, now time.Time, ip string) (*domain.SSOTicket, error)
	SSOTicketByID(ctx context.Context, id uuid.UUID) (*domain.SSOTicket, error)
	// ConsumeSSOLinkToken consume (CAS) el link_token del ticket.
	ConsumeSSOLinkToken(ctx context.Context, tenant, ticketID uuid.UUID, now time.Time) (bool, error)
	PurgeSSOTickets(ctx context.Context, before time.Time) (int, error)
	SSOLinkByExternal(ctx context.Context, tenant, connector uuid.UUID, externalUserID string) (*domain.SSOLink, error)
	// CreateSSOLink guarda la vinculación (ErrSSOLinkTaken si ya existe).
	CreateSSOLink(ctx context.Context, l *domain.SSOLink) error
	TouchSSOLink(ctx context.Context, tenant, id uuid.UUID, now time.Time) error
	ListSSOLinksByUser(ctx context.Context, userID uuid.UUID) ([]domain.SSOLink, error)
	ListSSOLinks(ctx context.Context, tenant uuid.UUID) ([]domain.SSOLink, error)
	// DeleteSSOLink borra una vinculación del tenant (uuid.Nil = de cualquier
	// tenant) y, si userID no es nil, solo si es de ese usuario.
	DeleteSSOLink(ctx context.Context, tenant, id uuid.UUID, userID *uuid.UUID) (*domain.SSOLink, error)
	DeleteSSOLinks(ctx context.Context, tenant uuid.UUID) (int, error)
}

// SSO implementa el autologin desde otros sistemas (ArgosISP): emisión de
// tickets (api.SSOTickets), canje con sesión normal o vinculación, y gestión
// de vinculaciones. El destino aplica SIEMPRE el ACL del usuario de Horus:
// el ticket no da permisos.
type SSO struct {
	svc        *Service
	store      SSOStore
	connectors func() (api.SSOConnectors, bool)

	mu        sync.Mutex
	lastPurge time.Time
}

var _ api.SSOTickets = (*SSO)(nil)

// NewSSO crea el servicio. connectors resuelve el dueño del conector (devices)
// en el proceso; sin él, ningún ticket se canjea.
func NewSSO(svc *Service, store SSOStore, connectors func() (api.SSOConnectors, bool)) *SSO {
	if connectors == nil {
		connectors = func() (api.SSOConnectors, bool) { return nil, false }
	}
	return &SSO{svc: svc, store: store, connectors: connectors}
}

func (s *SSO) now() time.Time { return s.svc.now().Truncate(time.Millisecond) }

var errSSOTicketInvalid = &apperr.Error{Kind: apperr.KindGone, Code: CodeSSOTicketInvalid,
	Detail: "El enlace de acceso no es válido, caducó o ya se usó. Vuelva a abrirlo desde el otro sistema."}

var errSSOLinkInvalid = &apperr.Error{Kind: apperr.KindGone, Code: CodeSSOLinkInvalid,
	Detail: "La vinculación caducó o ya se usó. Vuelva a abrir el enlace desde el otro sistema."}

var errSSOAccountUnavailable = &apperr.Error{Kind: apperr.KindForbidden, Code: CodeSSOAccountUnavailable,
	Detail: "La cuenta vinculada no puede entrar a este ISP. Inicie sesión normalmente o pida ayuda a un administrador."}

// IssueSSOTicket implementa api.SSOTickets.
func (s *SSO) IssueSSOTicket(ctx context.Context, req api.SSOTicketRequest) (api.SSOTicketIssued, error) {
	now := s.now()
	if req.TenantID == uuid.Nil || req.ConnectorID == uuid.Nil || !domain.ValidSSOTargetType(req.TargetType) ||
		req.ExternalUserID == "" || req.TargetID == "" {
		return api.SSOTicketIssued{}, apperr.New(apperr.KindInvalid, problem.CodeValidationFailed, "Ticket incompleto.")
	}
	raw, err := domain.GenerateSSOTicket()
	if err != nil {
		return api.SSOTicketIssued{}, err
	}
	t := &domain.SSOTicket{ID: uuid.Must(uuid.NewV7()), TenantID: req.TenantID, System: domain.SSOSystemArgos, ConnectorID: req.ConnectorID,
		Hash: domain.HashSSOTicket(raw), ExternalUserID: req.ExternalUserID, ExternalEmail: req.ExternalEmail, ExternalName: req.ExternalName,
		TargetType: req.TargetType, TargetID: req.TargetID, CreatedAt: now, ExpiresAt: now.Add(domain.SSOTicketTTL)}
	if req.APITokenID != uuid.Nil {
		id := req.APITokenID
		t.APITokenID = &id
	}
	if err := s.store.CreateSSOTicket(ctx, t); err != nil {
		return api.SSOTicketIssued{}, err
	}
	s.purge(ctx, now)
	p := authz.FromContext(ctx)
	e := api.AuditEntry{TenantID: req.TenantID, ActorType: "service", ActorID: "svc:auth", Action: "auth.sso.ticket_issued",
		ResourceType: "sso_ticket", ResourceID: t.ID.String(), Scope: "tenant", Outcome: "success",
		Changes: map[string]any{"system": t.System, "connector_id": t.ConnectorID.String(), "external_user_id": t.ExternalUserID,
			"target_type": t.TargetType, "target_id": t.TargetID, "expires_at": t.ExpiresAt.Format(time.RFC3339)}}
	if p != nil {
		e.ActorType, e.ActorID = p.Type, p.Subject
	}
	_ = s.svc.Record(ctx, e)
	return api.SSOTicketIssued{Ticket: raw, ExpiresAt: t.ExpiresAt}, nil
}

// purge borra, como mucho cada 10 min, los tickets de más de un día.
func (s *SSO) purge(ctx context.Context, now time.Time) {
	s.mu.Lock()
	due := now.Sub(s.lastPurge) >= ssoPurgeEvery
	if due {
		s.lastPurge = now
	}
	s.mu.Unlock()
	if !due {
		return
	}
	if _, err := s.store.PurgeSSOTickets(context.WithoutCancel(ctx), now.Add(-ssoTicketRetention)); err != nil {
		s.svc.o.Logger.WarnContext(ctx, "sso ticket purge failed", slog.Any("error", err))
	}
}

// SSOTarget es el destino pedido por el origen (la consola lo resuelve con el
// ACL del usuario).
type SSOTarget struct {
	TenantID   uuid.UUID
	TenantSlug string
	Type       string
	ExternalID string
}

// SSORedeemResult es el resultado del canje.
type SSORedeemResult struct {
	Status        string // authenticated | mfa_required | link_required
	Tokens        *Tokens
	MFAToken      string
	LinkToken     string
	LinkExpiresAt time.Time
	ExternalUser  SSOExternalUser
	Target        SSOTarget
}

// SSOExternalUser es el usuario del sistema de origen.
type SSOExternalUser struct {
	ID    string
	Email *string
	Name  *string
}

func (s *SSO) audit(ctx context.Context, in LoginInput, tenant uuid.UUID, actorType, actorID, action, outcome string, changes map[string]any) {
	if actorID == "" {
		actorType, actorID = "user", "anonymous"
	}
	_ = s.svc.Record(ctx, api.AuditEntry{TenantID: tenant, ActorType: actorType, ActorID: actorID, Action: action,
		ResourceType: "sso_ticket", Scope: "tenant", Outcome: outcome, IP: in.IP, UserAgent: in.UserAgent, Changes: changes})
}

func (s *SSO) connectorActive(ctx context.Context, tenant, connector uuid.UUID) bool {
	c, ok := s.connectors()
	if !ok {
		return false
	}
	active, err := c.SSOConnectorActive(ctx, tenant, connector)
	if err != nil {
		s.svc.o.Logger.WarnContext(ctx, "sso connector check failed", slog.Any("error", err))
		return false
	}
	return active
}

// activeMember indica si u es miembro activo del tenant activo (no basta el
// acceso de soporte de plataforma).
func (s *SSO) activeMember(ctx context.Context, u *domain.User, tenant uuid.UUID) (*domain.Membership, error) {
	if u.Status != domain.UserActive {
		return nil, nil
	}
	ms, err := s.svc.store.Memberships(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	for i := range ms {
		m := &ms[i]
		if m.Tenant.ID == tenant && m.Status == domain.MembershipActive && m.Tenant.Status == domain.TenantActive {
			return m, nil
		}
	}
	return nil, nil
}

// Redeem canjea un ticket (un uso, 60 s, ligado al conector). Con
// vinculación abre una sesión normal del usuario local (con su 2FA); sin
// ella pide iniciar sesión para vincular. Nunca crea usuarios ni vincula por
// correo.
func (s *SSO) Redeem(ctx context.Context, raw string, in LoginInput) (*SSORedeemResult, error) {
	now := s.now()
	if !domain.LooksLikeSSOTicket(raw) {
		return nil, errSSOTicketInvalid
	}
	t, err := s.store.RedeemSSOTicket(ctx, domain.HashSSOTicket(raw), now, in.IP)
	if errors.Is(err, domain.ErrNotFound) {
		s.audit(ctx, in, uuid.Nil, "", "", "auth.sso.redeem_failed", "denied", map[string]any{"reason": "ticket_invalid"})
		return nil, errSSOTicketInvalid
	}
	if err != nil {
		return nil, err
	}
	base := map[string]any{"ticket_id": t.ID.String(), "system": t.System, "connector_id": t.ConnectorID.String(),
		"external_user_id": t.ExternalUserID, "target_type": t.TargetType, "target_id": t.TargetID}
	with := func(k string, v any) map[string]any {
		out := make(map[string]any, len(base)+1)
		for a, b := range base {
			out[a] = b
		}
		out[k] = v
		return out
	}
	if !s.connectorActive(ctx, t.TenantID, t.ConnectorID) {
		s.audit(ctx, in, t.TenantID, "", "", "auth.sso.redeemed", "denied", with("reason", "connector_inactive"))
		return nil, errSSOTicketInvalid
	}
	tenant, err := s.svc.store.Tenant(ctx, t.TenantID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, errSSOTicketInvalid
	}
	if err != nil {
		return nil, err
	}
	res := &SSORedeemResult{ExternalUser: SSOExternalUser{ID: t.ExternalUserID, Email: t.ExternalEmail, Name: t.ExternalName},
		Target: SSOTarget{TenantID: tenant.ID, TenantSlug: tenant.Slug, Type: t.TargetType, ExternalID: t.TargetID}}
	if tenant.Status != domain.TenantActive {
		s.audit(ctx, in, t.TenantID, "", "", "auth.sso.redeemed", "denied", with("reason", "tenant_"+tenant.Status))
		return nil, errSSOAccountUnavailable
	}
	link, err := s.store.SSOLinkByExternal(ctx, t.TenantID, t.ConnectorID, t.ExternalUserID)
	if errors.Is(err, domain.ErrNotFound) {
		exp := now.Add(domain.SSOLinkTokenTTL)
		res.Status, res.LinkToken, res.LinkExpiresAt = "link_required", s.linkToken(t.ID, exp), exp
		s.audit(ctx, in, t.TenantID, "", "", "auth.sso.redeemed", "success", with("result", "link_required"))
		return res, nil
	}
	if err != nil {
		return nil, err
	}
	u, err := s.svc.store.UserByID(ctx, link.UserID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	var m *domain.Membership
	if u != nil {
		if m, err = s.activeMember(ctx, u, t.TenantID); err != nil {
			return nil, err
		}
	}
	if m == nil {
		s.audit(ctx, in, t.TenantID, "user", link.UserID.String(), "auth.sso.redeemed", "denied", with("reason", "account_unavailable"))
		return nil, errSSOAccountUnavailable
	}
	totp, err := s.svc.confirmedTOTP(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	sess := &domain.Session{ID: uuid.Must(uuid.NewV7()), UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(s.svc.o.SessionMax),
		AMR: []string{domain.AMRSSO}, IP: in.IP, UserAgent: in.UserAgent}
	if totp != nil {
		// La política de 2FA de Horus se aplica igual: la sesión queda pendiente
		// hasta POST /auth/mfa/verify.
		if err := s.svc.store.CreateSession(ctx, sess, nil); err != nil {
			return nil, err
		}
		res.Status, res.MFAToken = "mfa_required", s.svc.mfaToken(sess.ID, now.Add(MFATokenTTL))
	} else {
		rt, rawRT, err := s.svc.newRefresh(sess, now)
		if err != nil {
			return nil, err
		}
		if err := s.svc.store.CreateSession(ctx, sess, rt); err != nil {
			return nil, err
		}
		tok, err := s.svc.sessionToken(u, sess, now)
		if err != nil {
			return nil, err
		}
		tok.Refresh, tok.RefreshExpiresAt = rawRT, rt.ExpiresAt
		res.Status, res.Tokens = "authenticated", tok
	}
	if err := s.store.TouchSSOLink(ctx, t.TenantID, link.ID, now); err != nil {
		s.svc.o.Logger.WarnContext(ctx, "sso link touch failed", slog.Any("error", err))
	}
	done := with("result", res.Status)
	done["sid"] = sess.ID.String()
	s.audit(ctx, in, t.TenantID, "user", u.ID.String(), "auth.sso.redeemed", "success", done)
	in.Username = u.Email
	s.svc.auditLogin(ctx, in, u.ID.String(), "auth.login.sso", "success", map[string]any{"sid": sess.ID.String(), "system": t.System,
		"tenant_id": t.TenantID.String(), "mfa_pending": totp != nil})
	return res, nil
}

// linkToken firma (MAC de auth) el id del ticket y su caducidad: el valor no
// se guarda; el uso único lo da link_consumed_at del ticket.
func (s *SSO) linkToken(ticketID uuid.UUID, exp time.Time) string {
	payload := ticketID.String() + "|" + strconv.FormatInt(exp.Unix(), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(s.svc.o.Sealer.MAC("sso_link_token", []byte(payload)))
}

func (s *SSO) parseLinkToken(tok string, now time.Time) (uuid.UUID, bool) {
	body, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return uuid.Nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return uuid.Nil, false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || subtle.ConstantTimeCompare(got, s.svc.o.Sealer.MAC("sso_link_token", payload)) != 1 {
		return uuid.Nil, false
	}
	idStr, expStr, ok := strings.Cut(string(payload), "|")
	if !ok {
		return uuid.Nil, false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || now.Unix() > exp {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(idStr)
	return id, err == nil
}

// Link vincula la cuenta externa del ticket canjeado con el usuario de la
// sesión. Exige una autenticación (login o re-autenticación) posterior al
// canje: una sesión ya abierta en el navegador no basta, así un enlace ajeno
// no vincula una cuenta sin que su dueño escriba su contraseña.
func (s *SSO) Link(ctx context.Context, p *authz.Principal, linkToken string, in LoginInput) (*domain.SSOLink, *SSOTarget, error) {
	now := s.now()
	if p == nil || p.Type != authz.TypeUser || p.UserID == uuid.Nil || p.ViaPlatform {
		return nil, nil, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	id, ok := s.parseLinkToken(linkToken, now)
	if !ok {
		return nil, nil, errSSOLinkInvalid
	}
	t, err := s.store.SSOTicketByID(ctx, id)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && (t.RedeemedAt == nil || t.LinkConsumedAt != nil)) {
		return nil, nil, errSSOLinkInvalid
	}
	if err != nil {
		return nil, nil, err
	}
	// auth_time tiene resolución de segundos: se exige un segundo posterior al
	// del canje (una persona no inicia sesión en el mismo segundo).
	if p.AuthTime.IsZero() || p.AuthTime.Unix() <= t.RedeemedAt.Unix() {
		return nil, nil, apperr.Forbidden(problem.CodeReauthRequired,
			"Confirme su contraseña (y su código 2FA) para vincular la cuenta del otro sistema.")
	}
	u, err := s.svc.store.UserByID(ctx, p.UserID)
	if err != nil {
		return nil, nil, err
	}
	m, err := s.activeMember(ctx, u, t.TenantID)
	if err != nil {
		return nil, nil, err
	}
	if m == nil {
		s.audit(ctx, in, t.TenantID, "user", u.ID.String(), "auth.sso.link_failed", "denied", map[string]any{"reason": "not_a_member",
			"ticket_id": t.ID.String()})
		return nil, nil, apperr.Forbidden(CodeSSONotMember, "Su usuario no es miembro activo del ISP de esta integración.")
	}
	if !s.connectorActive(ctx, t.TenantID, t.ConnectorID) {
		return nil, nil, errSSOLinkInvalid
	}
	consumed, err := s.store.ConsumeSSOLinkToken(ctx, t.TenantID, t.ID, now)
	if err != nil {
		return nil, nil, err
	}
	if !consumed {
		return nil, nil, errSSOLinkInvalid
	}
	l := &domain.SSOLink{ID: uuid.Must(uuid.NewV7()), TenantID: t.TenantID, TenantName: m.Tenant.Name, System: t.System, ConnectorID: t.ConnectorID,
		ExternalUserID: t.ExternalUserID, ExternalEmail: t.ExternalEmail, ExternalName: t.ExternalName, UserID: u.ID, UserEmail: u.Email,
		CreatedAt: now, LastUsedAt: &now}
	if err := s.store.CreateSSOLink(ctx, l); err != nil {
		if errors.Is(err, ErrSSOLinkTaken) {
			return nil, nil, apperr.Conflict(CodeSSOLinkExists, "Esa cuenta del otro sistema ya está vinculada a un usuario de Horus.")
		}
		return nil, nil, err
	}
	s.audit(ctx, in, t.TenantID, "user", u.ID.String(), "auth.sso.linked", "success", map[string]any{"link_id": l.ID.String(),
		"system": l.System, "connector_id": l.ConnectorID.String(), "external_user_id": l.ExternalUserID, "ticket_id": t.ID.String()})
	return l, &SSOTarget{TenantID: t.TenantID, TenantSlug: m.Tenant.Slug, Type: t.TargetType, ExternalID: t.TargetID}, nil
}

// MyLinks es GET /me/sso-links.
func (s *SSO) MyLinks(ctx context.Context, p *authz.Principal) ([]domain.SSOLink, error) {
	if p == nil || p.Type != authz.TypeUser || p.UserID == uuid.Nil {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	return s.store.ListSSOLinksByUser(ctx, p.UserID)
}

// DeleteMyLink es DELETE /me/sso-links/{id}.
func (s *SSO) DeleteMyLink(ctx context.Context, p *authz.Principal, id uuid.UUID, in LoginInput) error {
	if p == nil || p.Type != authz.TypeUser || p.UserID == uuid.Nil {
		return apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	uid := p.UserID
	l, err := s.store.DeleteSSOLink(ctx, uuid.Nil, id, &uid)
	if errors.Is(err, domain.ErrNotFound) {
		return apperr.NotFound(CodeSSOLinkNotFound)
	}
	if err != nil {
		return err
	}
	s.audit(ctx, in, l.TenantID, "user", uid.String(), "auth.sso.unlinked", "success", map[string]any{"link_id": l.ID.String(),
		"system": l.System, "external_user_id": l.ExternalUserID, "by": "user"})
	return nil
}

func ssoAdminScope(ctx context.Context, perm string) (*authz.Principal, uuid.UUID, error) {
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

// TenantLinks es GET /integrations/argosisp/sso/links.
func (s *SSO) TenantLinks(ctx context.Context) ([]domain.SSOLink, error) {
	_, tid, err := ssoAdminScope(ctx, PermUsersRead)
	if err != nil {
		return nil, err
	}
	return s.store.ListSSOLinks(ctx, tid)
}

func (s *SSO) adminAudit(ctx context.Context, p *authz.Principal, tenant uuid.UUID, action, resID string, changes map[string]any) {
	_ = s.svc.Record(ctx, api.AuditEntry{TenantID: tenant, ActorType: p.Type, ActorID: p.Subject, ViaPlatform: p.ViaPlatform,
		Action: action, ResourceType: "sso_link", ResourceID: resID, Scope: "tenant", Outcome: "success", Changes: changes})
}

// RevokeLink es DELETE /integrations/argosisp/sso/links/{id}.
func (s *SSO) RevokeLink(ctx context.Context, id uuid.UUID) error {
	p, tid, err := ssoAdminScope(ctx, PermUsersManage)
	if err != nil {
		return err
	}
	l, err := s.store.DeleteSSOLink(ctx, tid, id, nil)
	if errors.Is(err, domain.ErrNotFound) {
		return apperr.NotFound(CodeSSOLinkNotFound)
	}
	if err != nil {
		return err
	}
	s.adminAudit(ctx, p, tid, "auth.sso.unlinked", l.ID.String(), map[string]any{"system": l.System, "user_id": l.UserID.String(),
		"external_user_id": l.ExternalUserID, "by": "admin"})
	return nil
}

// RevokeAllLinks es DELETE /integrations/argosisp/sso/links.
func (s *SSO) RevokeAllLinks(ctx context.Context) (int, error) {
	p, tid, err := ssoAdminScope(ctx, PermUsersManage)
	if err != nil {
		return 0, err
	}
	n, err := s.store.DeleteSSOLinks(ctx, tid)
	if err != nil {
		return 0, err
	}
	s.adminAudit(ctx, p, tid, "auth.sso.links_revoked", tid.String(), map[string]any{"revoked": n})
	return n, nil
}
