package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// Gestión de cuentas (docs/api.md §2.1–§2.2 y §5; docs/security.md §4.5):
// corrección de correo o nombre, baja lógica y enlaces de restablecimiento de
// contraseña.
//
// Decisiones:
//   - La cuenta es global. Un administrador de ISP (users.manage) solo edita o
//     restablece cuentas que pertenecen únicamente a su ISP: sin membresías no
//     revocadas en otros tenants ni roles de plataforma (409 USER_SHARED si
//     no; lo hace la plataforma). Así un ISP no toma el control de una cuenta
//     que también entra en otro.
//   - Un enlace de restablecimiento da acceso a la cuenta: quien lo emite en un
//     ISP debe tener todos los permisos de los roles del miembro (misma regla
//     anti-escalada que reenviar una invitación).
//   - Emitir un enlace no revoca sesiones; canjearlo revoca todas (reason
//     password_reset) y quita el bloqueo por intentos. El 2FA no se toca.
//   - "Olvidé mi contraseña" responde igual exista o no la cuenta; el correo se
//     envía en segundo plano para que el tiempo de respuesta no delate nada, y
//     sin SMTP no se emite nada (la respuesta lo dice con email_delivery=false).
//   - Baja lógica: deleted_at libera el correo; sesiones y membresías se revocan
//     con sus eventos; queda la auditoría. Nadie se da de baja a sí mismo y la
//     instalación conserva un platform_admin activo.

// Códigos de error propios de cuentas.
const (
	// CodeUserShared: la cuenta pertenece a otros ISP o a la plataforma.
	CodeUserShared = "USER_SHARED"
	// CodeCannotDeleteSelf: nadie elimina su propia cuenta.
	CodeCannotDeleteSelf = "CANNOT_DELETE_SELF"
)

// forgotPerAccountPerMinute es el límite por cuenta de /auth/password/forgot
// (docs/api.md §1.10: 5/min por cuenta además del límite por IP del gateway).
const forgotPerAccountPerMinute = 5

func errUserShared() error {
	return apperr.Conflict(CodeUserShared,
		"La cuenta también pertenece a otro ISP o tiene roles de plataforma: solo puede gestionarla un administrador de la plataforma.")
}

func errEmailTaken() error {
	return apperr.Conflict(problem.CodeAlreadyExists, "Ya existe una cuenta con ese correo.")
}

// validEmail normaliza y valida un correo (422 si no vale).
func validEmail(raw string) (string, *problem.FieldError) {
	email := strings.TrimSpace(raw)
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 {
		fe := apperr.Field("email", "INVALID_EMAIL", "Email no válido.")
		return "", &fe
	}
	return email, nil
}

// userEvent construye horus.auth.user.<typ> (catálogo C4).
func userEvent(actor Actor, typ string, now time.Time) func(u domain.User, changed []string) OutboxEvent {
	return func(u domain.User, changed []string) OutboxEvent {
		if changed == nil {
			changed = []string{}
		}
		return OutboxEvent{ID: uuid.Must(uuid.NewV7()), Type: "horus.auth.user." + typ, AggregateType: "user", AggregateID: u.ID,
			AggregateVersion: u.Version, Actor: actor, OccurredAt: now,
			Data: map[string]any{"id": u.ID.String(), "email": u.Email, "display_name": u.DisplayName, "status": u.Status,
				"version": u.Version, "changed": changed}}
	}
}

// identityPatch interpreta {display_name?, email?} (MemberPatch /
// PlatformUserPatch sin status).
func identityPatch(body map[string]json.RawMessage, allowed []string) (name, email *string, errs []problem.FieldError) {
	for k, raw := range body {
		if !slices.Contains(allowed, k) {
			errs = append(errs, apperr.Field(k, "UNKNOWN_FIELD", "Campo no editable."))
			continue
		}
		switch k {
		case "display_name":
			var v string
			if json.Unmarshal(raw, &v) != nil || strings.TrimSpace(v) == "" || len([]rune(v)) > 120 {
				errs = append(errs, apperr.Field(k, "INVALID_VALUE", "Nombre de 1 a 120 caracteres."))
				continue
			}
			v = strings.TrimSpace(v)
			name = &v
		case "email":
			var v string
			if json.Unmarshal(raw, &v) != nil {
				errs = append(errs, apperr.Field(k, "INVALID_EMAIL", "Email no válido."))
				continue
			}
			e, fe := validEmail(v)
			if fe != nil {
				errs = append(errs, *fe)
				continue
			}
			email = &e
		}
	}
	slices.SortFunc(errs, func(a, b problem.FieldError) int { return strings.Compare(a.Field, b.Field) })
	return name, email, errs
}

// UpdateMember es PATCH /members/{user_id}: nombre o correo de un miembro que
// solo pertenece a este ISP.
func (s *Service) UpdateMember(ctx context.Context, userID uuid.UUID, body map[string]json.RawMessage, requestIP string) (*domain.Member, error) {
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
	name, email, errs := identityPatch(body, []string{"display_name", "email"})
	if len(errs) > 0 {
		return nil, apperr.Validation(errs...)
	}
	if name == nil && email == nil {
		return nil, apperr.Validation(apperr.Field("body", "REQUIRED", "display_name o email."))
	}
	shared, err := s.store.UserSharedOutside(ctx, userID, tenant)
	if err != nil {
		return nil, err
	}
	if shared {
		return nil, errUserShared()
	}
	now := s.now()
	u, err := s.store.UpdateUserIdentity(ctx, UserIdentityChange{UserID: userID, Email: email, DisplayName: name, Now: now,
		Event: userEvent(s.actor(p), "updated", now)})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil, apperr.NotFound(problem.CodeNotFound)
	case errors.Is(err, ErrEmailTaken):
		return nil, errEmailTaken()
	case err != nil:
		return nil, err
	}
	s.clearSessionCache()
	s.recordTenant(ctx, p, tenant, "members.updated", "user", userID.String(), requestIP, map[string]any{
		"fields": identityFields(name, email), "email": u.Email, "display_name": u.DisplayName,
	})
	return s.store.Member(ctx, tenant, userID)
}

func identityFields(name, email *string) []string {
	out := []string{}
	if name != nil {
		out = append(out, "display_name")
	}
	if email != nil {
		out = append(out, "email")
	}
	return out
}

// UpdatePlatformUser es PATCH /platform/users/{user_id}: status (bloqueo o
// reactivación, con sus reglas) y, además o en su lugar, email y
// display_name. Todo o nada: se valida el cuerpo entero antes de aplicar.
func (s *Service) UpdatePlatformUser(ctx context.Context, id uuid.UUID, body map[string]json.RawMessage, requestIP string) (*domain.PlatformUser, error) {
	p, err := platformCaller(ctx, PermPlatformUsersManage)
	if err != nil {
		return nil, err
	}
	if _, err := s.platformUser(ctx, id); err != nil {
		return nil, err
	}
	identity := map[string]json.RawMessage{}
	status := map[string]json.RawMessage{}
	for k, raw := range body {
		if k == "status" {
			status[k] = raw
		} else {
			identity[k] = raw
		}
	}
	name, email, errs := identityPatch(identity, []string{"display_name", "email"})
	if len(errs) > 0 {
		return nil, apperr.Validation(errs...)
	}
	if len(status) == 0 && name == nil && email == nil {
		return nil, apperr.Validation(apperr.Field("body", "REQUIRED", "status, email o display_name."))
	}
	if len(status) > 0 {
		if _, err := s.SetUserStatus(ctx, id, status, requestIP); err != nil {
			return nil, err
		}
	}
	if name != nil || email != nil {
		now := s.now()
		u, err := s.store.UpdateUserIdentity(ctx, UserIdentityChange{UserID: id, Email: email, DisplayName: name, Now: now,
			Event: userEvent(s.actor(p), "updated", now)})
		switch {
		case errors.Is(err, domain.ErrNotFound):
			return nil, apperr.NotFound(problem.CodeNotFound)
		case errors.Is(err, ErrEmailTaken):
			return nil, errEmailTaken()
		case err != nil:
			return nil, err
		}
		s.clearSessionCache()
		s.recordPlatform(ctx, p, "platform.user.updated", id.String(), requestIP, map[string]any{
			"fields": identityFields(name, email), "email": u.Email, "display_name": u.DisplayName,
		})
	}
	return s.platformUser(ctx, id)
}

// DeleteUser es DELETE /platform/users/{user_id}: baja lógica.
func (s *Service) DeleteUser(ctx context.Context, id uuid.UUID, requestIP string) error {
	p, err := platformCaller(ctx, PermPlatformUsersManage)
	if err != nil {
		return err
	}
	if id == p.UserID {
		return apperr.Validation(apperr.Field("user_id", CodeCannotDeleteSelf, "No puede eliminar su propia cuenta."))
	}
	if _, err := s.platformUser(ctx, id); err != nil {
		return err
	}
	now := s.now()
	actor := s.actor(p)
	res, err := s.store.DeleteUser(ctx, UserDelete{UserID: id, Actor: actor, Now: now, Event: func(u domain.User, tenants []uuid.UUID) OutboxEvent {
		ev := userEvent(actor, "deleted", now)(u, nil)
		ids := make([]string, 0, len(tenants))
		for _, t := range tenants {
			ids = append(ids, t.String())
		}
		ev.Data["tenants"] = ids
		return ev
	}})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return apperr.NotFound(problem.CodeNotFound)
	case errors.Is(err, ErrLastPlatformAdmin):
		return errLastPlatformAdmin()
	case err != nil:
		return err
	}
	s.clearSessionCache()
	tenants := make([]string, 0, len(res.Tenants))
	for _, t := range res.Tenants {
		tenants = append(tenants, t.String())
	}
	s.recordPlatform(ctx, p, "platform.user.deleted", id.String(), requestIP, map[string]any{
		"email": res.Email, "revoked_sessions": len(res.Revoked), "revoked_memberships": tenants, "platform_roles": orEmptyList(res.PlatformRoles),
	})
	return nil
}

// --- Restablecimiento de contraseña ------------------------------------------------------------

// PasswordResetIssued es PasswordResetIssued del contrato.
type PasswordResetIssued struct {
	UserID    uuid.UUID
	URL       string
	ExpiresAt time.Time
	EmailSent bool
}

// PasswordResetInfo es la respuesta de /auth/password/reset/inspect.
type PasswordResetInfo struct {
	Email       string
	DisplayName string
	ExpiresAt   time.Time
}

func (s *Service) resetURL(token string) string {
	return s.o.PublicBaseURL + "/reset-password#token=" + token
}

// issuePasswordReset crea el enlace para u (los anteriores sin usar dejan de
// valer) y lo entrega por correo si hay canal; el enlace se devuelve para
// mostrarlo una vez. createdBy/tenant describen quién lo emitió.
func (s *Service) issuePasswordReset(ctx context.Context, u *domain.User, createdBy uuid.UUID, tenant *uuid.UUID, sync bool) (*PasswordResetIssued, error) {
	token, err := domain.NewInvitationToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	exp := now.Add(domain.PasswordResetTTL)
	if err := s.store.CreatePasswordReset(ctx, NewPasswordReset{ID: uuid.Must(uuid.NewV7()), UserID: u.ID, TokenHash: domain.HashSecret(token),
		ExpiresAt: exp, CreatedBy: createdBy, TenantID: tenant, Now: now}); err != nil {
		return nil, err
	}
	out := &PasswordResetIssued{UserID: u.ID, URL: s.resetURL(token), ExpiresAt: exp}
	d := PasswordResetDelivery{Email: u.Email, DisplayName: u.DisplayName, Token: token, URL: out.URL, ExpiresAt: exp, RequestedByAdmin: createdBy != uuid.Nil}
	if sync {
		out.EmailSent = s.deliverPasswordReset(ctx, d, u.ID)
	} else if s.o.PasswordResets != nil {
		go s.deliverPasswordReset(context.WithoutCancel(ctx), d, u.ID)
	}
	return out, nil
}

// deliverPasswordReset envía el enlace (con plazo) y dice si se entregó. Un
// fallo no falla la petición y se registra sin dirección, token ni enlace.
func (s *Service) deliverPasswordReset(ctx context.Context, d PasswordResetDelivery, userID uuid.UUID) bool {
	if s.o.PasswordResets == nil {
		return false
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.o.InvitationSendTimeout)
	defer cancel()
	if err := s.o.PasswordResets.SendPasswordReset(sctx, d); err != nil {
		msg := err.Error()
		for _, secret := range []string{d.URL, d.Token, d.Email} {
			if secret != "" {
				msg = strings.ReplaceAll(msg, secret, "***")
			}
		}
		s.o.Logger.WarnContext(ctx, "password reset email not sent", "user_id", userID.String(), "error", msg)
		return false
	}
	return true
}

// ForgotPassword es POST /auth/password/forgot. Devuelve si la instalación
// envía correo; la respuesta no depende de que exista la cuenta.
func (s *Service) ForgotPassword(ctx context.Context, rawEmail string, li LoginInput) (bool, error) {
	email, fe := validEmail(rawEmail)
	if fe != nil {
		return false, apperr.Validation(*fe)
	}
	if s.o.PasswordResets == nil {
		return false, nil
	}
	now := s.now()
	if !s.forgotAllowed(strings.ToLower(email), now) {
		return true, apperr.New(apperr.KindTooManyRequests, problem.CodeRateLimited, "Demasiadas solicitudes para esa cuenta; espere un minuto.")
	}
	u, err := s.store.UserByEmail(ctx, email)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return true, err
	}
	found := err == nil && u.Status == domain.UserActive && u.PasswordHash != ""
	if found {
		if _, err := s.issuePasswordReset(ctx, u, uuid.Nil, nil, false); err != nil {
			return true, err
		}
	}
	s.auditLogin(ctx, li, "", "auth.password.forgot", "success", map[string]any{"account_found": found})
	return true, nil
}

// forgotAllowed aplica el límite por cuenta de forgot (ventana deslizante de
// un minuto, en memoria: suficiente por instancia junto al límite por IP).
func (s *Service) forgotAllowed(key string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forgot == nil {
		s.forgot = map[string][]time.Time{}
	}
	cut := now.Add(-time.Minute)
	kept := s.forgot[key][:0]
	for _, t := range s.forgot[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= forgotPerAccountPerMinute {
		s.forgot[key] = kept
		return false
	}
	s.forgot[key] = append(kept, now)
	if len(s.forgot) > 10000 { // sin crecimiento sin límite
		s.forgot = map[string][]time.Time{key: s.forgot[key]}
	}
	return true
}

func errResetInvalid(code string) error {
	msg := map[string]string{
		"USED":    "Este enlace ya se usó. Pida uno nuevo.",
		"EXPIRED": "Este enlace caducó (dura 30 minutos). Pida uno nuevo.",
	}[code]
	if msg == "" {
		code, msg = "INVALID_TOKEN", "El enlace no es válido."
	}
	return apperr.Validation(apperr.Field("token", code, msg))
}

// usableReset busca el enlace y comprueba que sirve.
func (s *Service) usableReset(ctx context.Context, token string, now time.Time) (*domain.PasswordReset, error) {
	if len(token) < 32 {
		return nil, errResetInvalid("INVALID_TOKEN")
	}
	r, err := s.store.PasswordResetByHash(ctx, domain.HashSecret(token))
	if errors.Is(err, domain.ErrNotFound) {
		return nil, errResetInvalid("INVALID_TOKEN")
	}
	if err != nil {
		return nil, err
	}
	if code := r.Problem(now); code != "" {
		return nil, errResetInvalid(code)
	}
	return r, nil
}

// InspectPasswordReset es POST /auth/password/reset/inspect.
func (s *Service) InspectPasswordReset(ctx context.Context, token string) (*PasswordResetInfo, error) {
	r, err := s.usableReset(ctx, token, s.now())
	if err != nil {
		return nil, err
	}
	return &PasswordResetInfo{Email: r.UserEmail, DisplayName: r.UserDisplayName, ExpiresAt: r.ExpiresAt}, nil
}

// ResetPassword es POST /auth/password/reset: contraseña nueva con un enlace
// (un uso); revoca todas las sesiones de la cuenta.
func (s *Service) ResetPassword(ctx context.Context, token, next string, li LoginInput) error {
	now := s.now()
	r, err := s.usableReset(ctx, token, now)
	if err != nil {
		return err
	}
	if code := domain.ValidateNewPassword(next, r.UserEmail, r.UserDisplayName); code != "" {
		return apperr.Validation(apperr.Field("new_password", code, "La contraseña no cumple la política (12–128 caracteres, no común, sin datos personales)."))
	}
	h, err := domain.HashPassword(next, s.o.Argon)
	if err != nil {
		return err
	}
	res, err := s.store.ConsumePasswordReset(ctx, ConsumedPasswordReset{Reset: *r, PasswordHash: h, Now: now})
	if errors.Is(err, ErrResetUsed) {
		return errResetInvalid("USED")
	}
	if err != nil {
		return err
	}
	s.clearSessionCache()
	s.auditLogin(ctx, li, r.UserID.String(), "auth.password.reset", "success", map[string]any{"revoked_sessions": len(res.Revoked)})
	return nil
}

// ResetMemberPassword es POST /members/{user_id}/password-reset.
func (s *Service) ResetMemberPassword(ctx context.Context, userID uuid.UUID, requestIP string) (*PasswordResetIssued, error) {
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
	if cur.Status == domain.MembershipInvited {
		return nil, apperr.Conflict(problem.CodeConflict, "El miembro aún no aceptó su invitación: reenvíe la invitación en lugar de restablecer la contraseña.")
	}
	if err := checkGrantable(p, cur.Assignments, "restablecer la contraseña de un miembro con"); err != nil {
		return nil, err
	}
	shared, err := s.store.UserSharedOutside(ctx, userID, tenant)
	if err != nil {
		return nil, err
	}
	if shared {
		return nil, errUserShared()
	}
	u, err := s.resettableUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out, err := s.issuePasswordReset(ctx, u, p.UserID, &tenant, true)
	if err != nil {
		return nil, err
	}
	s.recordTenant(ctx, p, tenant, "members.password_reset_issued", "user", userID.String(), requestIP, map[string]any{
		"user_id": userID.String(), "email_sent": out.EmailSent, "expires_at": out.ExpiresAt.UTC(),
	})
	return out, nil
}

// ResetUserPassword es POST /platform/users/{user_id}/password-reset.
func (s *Service) ResetUserPassword(ctx context.Context, userID uuid.UUID, requestIP string) (*PasswordResetIssued, error) {
	p, err := platformCaller(ctx, PermPlatformUsersManage)
	if err != nil {
		return nil, err
	}
	u, err := s.resettableUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out, err := s.issuePasswordReset(ctx, u, p.UserID, nil, true)
	if err != nil {
		return nil, err
	}
	s.recordPlatform(ctx, p, "platform.user.password_reset_issued", userID.String(), requestIP, map[string]any{
		"email_sent": out.EmailSent, "expires_at": out.ExpiresAt.UTC(),
	})
	return out, nil
}

// resettableUser devuelve la cuenta si puede recibir un enlace: activa y con
// contraseña (pendiente → reenviar invitación; bloqueada → reactivar antes).
func (s *Service) resettableUser(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	u, err := s.store.UserByID(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, apperr.NotFound(problem.CodeNotFound)
	}
	if err != nil {
		return nil, err
	}
	switch {
	case u.Status == domain.UserPending || u.PasswordHash == "":
		return nil, apperr.Conflict(problem.CodeConflict, "La cuenta aún no se activó: reenvíe la invitación en lugar de restablecer la contraseña.")
	case u.Status == domain.UserDisabled:
		return nil, apperr.Conflict(problem.CodeConflict, "La cuenta está bloqueada: reactívela antes de restablecer la contraseña.")
	}
	return u, nil
}

// requireRecentAuth aplica x-reauth a una parte de PATCH /me (el correo).
func requireRecentAuth(p *authz.Principal, now time.Time) error {
	if p == nil || p.AuthAge(now) > 5*time.Minute {
		return apperr.Forbidden(problem.CodeReauthRequired, "Cambiar el correo exige volver a autenticarse.")
	}
	return nil
}
