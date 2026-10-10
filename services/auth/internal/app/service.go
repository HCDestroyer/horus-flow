package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"slices"
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

// Códigos de error propios (enum abierto de ErrorCode).
const (
	CodePasswordChangeRequired = "PASSWORD_CHANGE_REQUIRED"
)

// MFATokenTTL es la vida del mfa_token del login con 2FA.
const MFATokenTTL = 5 * time.Minute

// maxMFAFailures invalida la sesión pendiente tras tantos códigos erróneos.
const maxMFAFailures = 5

// Options configura el servicio.
type Options struct {
	Signer      *authz.Signer
	Sealer      *domain.Sealer
	Catalog     *domain.Catalog
	Argon       domain.Argon2Params
	RefreshIdle time.Duration
	SessionMax  time.Duration
	TOTPIssuer  string
	Now         func() time.Time
	Logger      *slog.Logger
}

// Service implementa los casos de uso de auth.
type Service struct {
	store     Store
	o         Options
	dummyHash string

	mu          sync.Mutex
	cache       map[string]cachedStatus
}

type cachedStatus struct {
	st  SessionStatus
	exp time.Time
}

// sessionCacheTTL acota la caché en proceso de CheckSession; la revocación
// local la invalida al momento.
const sessionCacheTTL = 2 * time.Second

// NewService crea el servicio.
func NewService(store Store, o Options) (*Service, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.TOTPIssuer == "" {
		o.TOTPIssuer = "Horus Flow"
	}
	dummy, err := domain.HashPassword("horus-dummy-password-for-timing", o.Argon)
	if err != nil {
		return nil, err
	}
	return &Service{store: store, o: o, dummyHash: dummy, cache: map[string]cachedStatus{}}, nil
}

func (s *Service) now() time.Time { return s.o.Now().UTC() }

// Tokens es un access token y, si aplica, el refresh de la cookie.
type Tokens struct {
	Access           string
	ExpiresAt        time.Time
	Scope            string
	TenantID         *uuid.UUID
	Refresh          string
	RefreshExpiresAt time.Time
}

// LoginInput son las credenciales y el contexto de red.
type LoginInput struct {
	Username, Password string
	IP, UserAgent      string
}

// LoginResult es un par de tokens o un desafío de 2FA.
type LoginResult struct {
	Tokens   *Tokens
	MFAToken string
}

var errInvalidCredentials = &apperr.Error{
	Kind: apperr.KindUnauthorized, Code: problem.CodeInvalidCredentials,
	Detail: "Usuario o contraseña incorrectos, o acceso bloqueado temporalmente.",
}

func errUnauth(code string) *apperr.Error {
	return &apperr.Error{Kind: apperr.KindUnauthorized, Code: code}
}

// Login autentica con usuario (email) y contraseña. Todos los fallos
// responden igual (no revelan si el usuario existe ni si está bloqueado).
func (s *Service) Login(ctx context.Context, in LoginInput) (*LoginResult, error) {
	now := s.now()
	email := strings.TrimSpace(in.Username)
	if email == "" || in.Password == "" || len(in.Password) > 1024 {
		_, _, _ = domain.VerifyPassword(in.Password, s.dummyHash, s.o.Argon)
		return nil, errInvalidCredentials
	}
	u, err := s.store.UserByEmail(ctx, email)
	if errors.Is(err, domain.ErrNotFound) {
		_, _, _ = domain.VerifyPassword(in.Password, s.dummyHash, s.o.Argon)
		s.auditLogin(ctx, in, "", "auth.login.failed", "failure", map[string]any{"reason": "invalid_credentials"})
		return nil, errInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if u.LockedUntil != nil && now.Before(*u.LockedUntil) {
		_, _, _ = domain.VerifyPassword(in.Password, s.dummyHash, s.o.Argon)
		s.auditLogin(ctx, in, u.ID.String(), "auth.login.locked", "denied", map[string]any{
			"failed_logins": u.FailedLogins, "locked_until": u.LockedUntil.Format(time.RFC3339),
		})
		return nil, errInvalidCredentials
	}
	ok, rehash := false, false
	if u.PasswordHash != "" {
		ok, rehash, _ = domain.VerifyPassword(in.Password, u.PasswordHash, s.o.Argon)
	} else {
		_, _, _ = domain.VerifyPassword(in.Password, s.dummyHash, s.o.Argon)
	}
	if !ok {
		failed := u.FailedLogins + 1
		var until *time.Time
		if d := domain.LockDelay(failed); d > 0 {
			t := now.Add(d)
			until = &t
		}
		if err := s.store.SetLoginFailure(ctx, u.ID, failed, until); err != nil {
			return nil, err
		}
		s.auditLogin(ctx, in, u.ID.String(), "auth.login.failed", "failure", map[string]any{"reason": "invalid_credentials", "failed_logins": failed})
		if until != nil {
			s.o.Logger.WarnContext(ctx, "login progressive lock", slog.String("user_id", u.ID.String()),
				slog.Int("failed_logins", failed), slog.Time("locked_until", *until))
			s.auditLogin(ctx, in, u.ID.String(), "auth.login.locked", "denied", map[string]any{
				"failed_logins": failed, "locked_until": until.Format(time.RFC3339),
			})
		}
		return nil, errInvalidCredentials
	}
	if u.Status != domain.UserActive {
		s.auditLogin(ctx, in, u.ID.String(), "auth.login.failed", "failure", map[string]any{"reason": "user_" + u.Status})
		return nil, errInvalidCredentials
	}
	newHash := ""
	if rehash {
		if h, err := domain.HashPassword(in.Password, s.o.Argon); err == nil {
			newHash = h
		}
	}
	if err := s.store.SetLoginSuccess(ctx, u.ID, now, newHash); err != nil {
		return nil, err
	}
	totp, err := s.confirmedTOTP(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	sess := &domain.Session{
		ID: uuid.Must(uuid.NewV7()), UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(s.o.SessionMax),
		AMR: []string{domain.AMRPassword}, IP: in.IP, UserAgent: in.UserAgent,
	}
	if totp != nil {
		if err := s.store.CreateSession(ctx, sess, nil); err != nil {
			return nil, err
		}
		s.auditLogin(ctx, in, u.ID.String(), "auth.login.mfa_challenge", "success", nil)
		return &LoginResult{MFAToken: s.mfaToken(sess.ID, now.Add(MFATokenTTL))}, nil
	}
	rt, raw, err := s.newRefresh(sess, now)
	if err != nil {
		return nil, err
	}
	if err := s.store.CreateSession(ctx, sess, rt); err != nil {
		return nil, err
	}
	tok, err := s.sessionToken(u, sess, now)
	if err != nil {
		return nil, err
	}
	tok.Refresh, tok.RefreshExpiresAt = raw, rt.ExpiresAt
	s.auditLogin(ctx, in, u.ID.String(), "auth.login.succeeded", "success", map[string]any{"sid": sess.ID.String()})
	return &LoginResult{Tokens: tok}, nil
}

func (s *Service) confirmedTOTP(ctx context.Context, userID uuid.UUID) (*domain.TOTP, error) {
	t, err := s.store.TOTP(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if t.ConfirmedAt == nil {
		return nil, nil
	}
	return t, nil
}

func (s *Service) mfaToken(sid uuid.UUID, exp time.Time) string {
	payload := sid.String() + "|" + strconv.FormatInt(exp.Unix(), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(s.o.Sealer.MAC("mfa_token", []byte(payload)))
}

func (s *Service) parseMFAToken(tok string, now time.Time) (uuid.UUID, bool) {
	body, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return uuid.Nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return uuid.Nil, false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || subtle.ConstantTimeCompare(got, s.o.Sealer.MAC("mfa_token", payload)) != 1 {
		return uuid.Nil, false
	}
	sidStr, expStr, ok := strings.Cut(string(payload), "|")
	if !ok {
		return uuid.Nil, false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || now.Unix() > exp {
		return uuid.Nil, false
	}
	sid, err := uuid.Parse(sidStr)
	return sid, err == nil
}

// VerifyMFA completa el login con 2FA (TOTP o código de recuperación). El
// mfa_token es de un solo uso: la sesión pendiente solo se verifica una vez.
func (s *Service) VerifyMFA(ctx context.Context, mfaToken, code string, in LoginInput) (*Tokens, error) {
	now := s.now()
	sid, ok := s.parseMFAToken(mfaToken, now)
	if !ok {
		return nil, errInvalidCredentials
	}
	sess, err := s.store.Session(ctx, sid)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, errInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !sess.Active(now) || sess.MFAVerifiedAt != nil {
		return nil, errInvalidCredentials
	}
	u, err := s.store.UserByID(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	in.Username = u.Email
	if !s.checkSecondFactor(ctx, u.ID, code, now) {
		// El contador vive en la sesión pendiente (D23: un reinicio no
		// regala intentos).
		n, err := s.store.MFAFailure(ctx, sid)
		if err != nil {
			return nil, err
		}
		s.auditLogin(ctx, in, u.ID.String(), "auth.mfa.failed", "failure", map[string]any{"attempt": n})
		if n >= maxMFAFailures {
			_ = s.revoke(ctx, sess, "mfa_failures", now)
		}
		return nil, errInvalidCredentials
	}
	rt, raw, err := s.newRefresh(sess, now)
	if err != nil {
		return nil, err
	}
	amr := []string{domain.AMRPassword, domain.AMROTP}
	ok, err = s.store.SetSessionMFA(ctx, sid, now, amr, rt)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errInvalidCredentials
	}
	sess.MFAVerifiedAt, sess.AMR = &now, amr
	tok, err := s.sessionToken(u, sess, now)
	if err != nil {
		return nil, err
	}
	tok.Refresh, tok.RefreshExpiresAt = raw, rt.ExpiresAt
	s.auditLogin(ctx, in, u.ID.String(), "auth.login.succeeded", "success", map[string]any{"sid": sid.String(), "mfa": true})
	return tok, nil
}

// checkSecondFactor valida un código TOTP (6 dígitos) o de recuperación.
func (s *Service) checkSecondFactor(ctx context.Context, userID uuid.UUID, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	t, err := s.confirmedTOTP(ctx, userID)
	if err != nil || t == nil {
		return false
	}
	if len(code) == domain.TOTPDigits {
		secret, err := s.o.Sealer.Open(t.Ciphertext, t.DEKWrapped, []byte("totp:"+userID.String()))
		if err != nil {
			s.o.Logger.ErrorContext(ctx, "totp secret unreadable", slog.Any("error", err))
			return false
		}
		step, ok := domain.VerifyTOTP(secret, code, now, t.LastUsedStep)
		if !ok {
			return false
		}
		advanced, err := s.store.AdvanceTOTPStep(ctx, userID, step)
		return err == nil && advanced
	}
	used, err := s.store.UseRecoveryCode(ctx, userID, domain.HashRecoveryCode(userID.String(), code), now)
	return err == nil && used
}

func hashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

func (s *Service) newRefresh(sess *domain.Session, now time.Time) (*domain.RefreshToken, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, "", fmt.Errorf("refresh token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	exp := now.Add(s.o.RefreshIdle)
	if exp.After(sess.ExpiresAt) {
		exp = sess.ExpiresAt
	}
	return &domain.RefreshToken{ID: uuid.Must(uuid.NewV7()), SessionID: sess.ID, Hash: hashToken(raw), IssuedAt: now, ExpiresAt: exp}, raw, nil
}

func (s *Service) sign(c authz.Claims) (*Tokens, error) {
	tok, exp, err := s.o.Signer.Sign(c)
	if err != nil {
		return nil, err
	}
	t := &Tokens{Access: tok, ExpiresAt: exp, Scope: c.Scope}
	if c.TID != "" {
		id := uuid.MustParse(c.TID)
		t.TenantID = &id
	}
	return t, nil
}

func (s *Service) baseClaims(u *domain.User, sess *domain.Session, scope string, authTime time.Time) authz.Claims {
	c := authz.Claims{Typ: authz.TypeUser, Scope: scope, SID: sess.ID.String(), AMR: sess.AMR, AuthTime: authTime.Unix()}
	c.Subject = u.ID.String()
	return c
}

func (s *Service) sessionToken(u *domain.User, sess *domain.Session, _ time.Time) (*Tokens, error) {
	return s.sign(s.baseClaims(u, sess, authz.ScopeSession, sess.AuthTime()))
}

// Refresh rota el refresh de la cookie y devuelve un token de sesión. Un
// refresh ya usado revoca la sesión entera (detección de reutilización).
func (s *Service) Refresh(ctx context.Context, raw string, in LoginInput) (*Tokens, error) {
	now := s.now()
	if raw == "" {
		return nil, errUnauth(problem.CodeUnauthenticated)
	}
	rt, err := s.store.RefreshByHash(ctx, hashToken(raw))
	if errors.Is(err, domain.ErrNotFound) {
		return nil, errUnauth(problem.CodeUnauthenticated)
	}
	if err != nil {
		return nil, err
	}
	sess, err := s.store.Session(ctx, rt.SessionID)
	if err != nil {
		return nil, err
	}
	if rt.UsedAt != nil {
		return nil, s.reuseDetected(ctx, sess, in, now)
	}
	if !now.Before(rt.ExpiresAt) || !sess.Active(now) {
		return nil, errUnauth(problem.CodeSessionRevoked)
	}
	u, err := s.store.UserByID(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	if u.Status != domain.UserActive {
		return nil, errUnauth(problem.CodeSessionRevoked)
	}
	next, nextRaw, err := s.newRefresh(sess, now)
	if err != nil {
		return nil, err
	}
	rotated, err := s.store.RotateRefresh(ctx, rt.ID, next, now)
	if err != nil {
		return nil, err
	}
	if !rotated {
		return nil, s.reuseDetected(ctx, sess, in, now)
	}
	tok, err := s.sessionToken(u, sess, now)
	if err != nil {
		return nil, err
	}
	tok.Refresh, tok.RefreshExpiresAt = nextRaw, next.ExpiresAt
	return tok, nil
}

func (s *Service) reuseDetected(ctx context.Context, sess *domain.Session, in LoginInput, now time.Time) error {
	s.o.Logger.WarnContext(ctx, "refresh token reuse: session revoked", slog.String("sid", sess.ID.String()),
		slog.String("user_id", sess.UserID.String()))
	if err := s.revoke(ctx, sess, "refresh_token_reuse", now); err != nil {
		return err
	}
	s.auditLogin(ctx, in, sess.UserID.String(), "auth.refresh_token.reused", "denied", map[string]any{"sid": sess.ID.String()})
	return errUnauth(problem.CodeSessionRevoked)
}

func (s *Service) revoke(ctx context.Context, sess *domain.Session, reason string, now time.Time) error {
	if sess.RevokedAt != nil {
		return nil
	}
	ev := &OutboxEvent{
		ID: uuid.Must(uuid.NewV7()), Type: "horus.auth.session.revoked", AggregateType: "session",
		AggregateID: sess.ID, AggregateVersion: 1, OccurredAt: now,
		Actor: Actor{Type: "system", ID: "system:auth"},
		Data:  map[string]any{"session_id": sess.ID.String(), "user_id": sess.UserID.String(), "reason": reason},
	}
	if err := s.store.RevokeSession(ctx, sess.ID, reason, now, ev); err != nil {
		return err
	}
	s.invalidate(sess.ID)
	return nil
}

// Logout revoca la sesión del refresh presentado (siempre éxito).
func (s *Service) Logout(ctx context.Context, raw string, in LoginInput) error {
	if raw == "" {
		return nil
	}
	rt, err := s.store.RefreshByHash(ctx, hashToken(raw))
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	sess, err := s.store.Session(ctx, rt.SessionID)
	if err != nil {
		return err
	}
	if err := s.revoke(ctx, sess, "logout", s.now()); err != nil {
		return err
	}
	s.auditLogin(ctx, in, sess.UserID.String(), "auth.logout", "success", map[string]any{"sid": sess.ID.String()})
	return nil
}

// TokenRequest pide un token de tenant o de plataforma.
type TokenRequest struct {
	TenantID uuid.UUID // uuid.Nil + Platform = plataforma
	Platform bool
	// AuthTime fuerza auth_time (re-autenticación); cero = el de la sesión.
	AuthTime time.Time
	IP       string
}

// sessionFor resuelve la sesión del principal (bearer) o del refresh.
func (s *Service) sessionFor(ctx context.Context, p *authz.Principal, refreshRaw string, now time.Time) (*domain.Session, *domain.User, error) {
	var sid uuid.UUID
	switch {
	case p != nil && p.Type == authz.TypeUser && p.SessionID != uuid.Nil:
		sid = p.SessionID
	case refreshRaw != "":
		rt, err := s.store.RefreshByHash(ctx, hashToken(refreshRaw))
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil, errUnauth(problem.CodeUnauthenticated)
		}
		if err != nil {
			return nil, nil, err
		}
		if rt.UsedAt != nil || !now.Before(rt.ExpiresAt) {
			return nil, nil, errUnauth(problem.CodeSessionRevoked)
		}
		sid = rt.SessionID
	default:
		return nil, nil, errUnauth(problem.CodeUnauthenticated)
	}
	sess, err := s.store.Session(ctx, sid)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil, errUnauth(problem.CodeSessionRevoked)
	}
	if err != nil {
		return nil, nil, err
	}
	if !sess.Active(now) {
		return nil, nil, errUnauth(problem.CodeSessionRevoked)
	}
	u, err := s.store.UserByID(ctx, sess.UserID)
	if err != nil {
		return nil, nil, err
	}
	if u.Status != domain.UserActive {
		return nil, nil, errUnauth(problem.CodeSessionRevoked)
	}
	return sess, u, nil
}

// requireMFA aplica D14: un rol que exige 2FA necesita TOTP activo
// (MFA_ENROLLMENT_REQUIRED) y una sesión verificada con él (MFA_REQUIRED).
func (s *Service) requireMFA(ctx context.Context, u *domain.User, sess *domain.Session) error {
	t, err := s.confirmedTOTP(ctx, u.ID)
	if err != nil {
		return err
	}
	if t == nil {
		return apperr.Forbidden(problem.CodeMFAEnrollmentRequired, "Active TOTP en POST /me/totp/enroll y /me/totp/confirm.")
	}
	if sess.MFAVerifiedAt == nil {
		return apperr.Forbidden(problem.CodeMFARequired, "Inicie sesión de nuevo con el segundo factor.")
	}
	return nil
}

// IssueToken emite un token de tenant (`tid`) o de plataforma para la sesión
// del llamante (POST /auth/token).
func (s *Service) IssueToken(ctx context.Context, p *authz.Principal, refreshRaw string, req TokenRequest) (*Tokens, error) {
	now := s.now()
	sess, u, err := s.sessionFor(ctx, p, refreshRaw, now)
	if err != nil {
		return nil, err
	}
	if u.MustChangePassword {
		return nil, apperr.Forbidden(CodePasswordChangeRequired, "Debe cambiar la contraseña (POST /me/password) antes de continuar.")
	}
	authTime := sess.AuthTime()
	if !req.AuthTime.IsZero() {
		authTime = req.AuthTime
	}
	roles, err := s.store.PlatformRoles(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	if req.Platform {
		if len(roles) == 0 {
			return nil, apperr.Forbidden(problem.CodePermissionDenied, "El usuario no tiene roles de plataforma.")
		}
		perms, needMFA := s.o.Catalog.PlatformPermissions(roles)
		if needMFA || u.MFAEnforced {
			if err := s.requireMFA(ctx, u, sess); err != nil {
				return nil, err
			}
		}
		c := s.baseClaims(u, sess, authz.ScopePlatform, authTime)
		c.Perms = allScopes(perms)
		return s.sign(c)
	}
	memberships, err := s.store.Memberships(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	var m *domain.Membership
	for i := range memberships {
		if memberships[i].Tenant.ID == req.TenantID {
			m = &memberships[i]
		}
	}
	via := false
	if m == nil {
		// Superadministrador sin membresía: entra en el ISP como plataforma
		// (via_platform), auditado en el tenant y en la plataforma.
		if !slices.Contains(roles, domain.RolePlatformAdmin) {
			return nil, apperr.NotFound(problem.CodeTenantNotFound)
		}
		t, err := s.store.Tenant(ctx, req.TenantID)
		if errors.Is(err, domain.ErrNotFound) {
			return nil, apperr.NotFound(problem.CodeTenantNotFound)
		}
		if err != nil {
			return nil, err
		}
		admin, _ := s.o.Catalog.TenantRole(domain.RoleTenantAdmin)
		m = &domain.Membership{Tenant: *t, Status: domain.MembershipActive, Assignments: []domain.Assignment{{
			RoleID: admin.ID, RoleKey: admin.Key, ScopeType: "tenant", RequiresMFA: true, Permissions: admin.Permissions,
		}}}
		via = true
	}
	if m.Tenant.Status != domain.TenantActive {
		return nil, apperr.Forbidden(problem.CodeTenantSuspended, "")
	}
	if m.RequiresMFA() || u.MFAEnforced || via {
		if err := s.requireMFA(ctx, u, sess); err != nil {
			return nil, err
		}
	}
	c := s.baseClaims(u, sess, authz.ScopeTenant, authTime)
	c.TID = m.Tenant.ID.String()
	c.ViaPlatform = via
	c.Perms = m.EffectivePermissions()
	tok, err := s.sign(c)
	if err != nil {
		return nil, err
	}
	if via {
		for _, tenant := range []uuid.UUID{m.Tenant.ID, uuid.Nil} {
			_ = s.Record(ctx, api.AuditEntry{
				TenantID: tenant, ActorType: "user", ActorID: u.ID.String(), ViaPlatform: true,
				Action: "platform.support_access.token_issued", ResourceType: "tenant", ResourceID: m.Tenant.ID.String(),
				Outcome: "success", IP: req.IP, Changes: map[string]any{"sid": sess.ID.String()},
			})
		}
	}
	return tok, nil
}

func allScopes(perms []string) map[string][]string {
	out := make(map[string][]string, len(perms))
	for _, p := range perms {
		out[p] = []string{authz.ScopeAll}
	}
	return out
}

// Reauth verifica de nuevo la contraseña (y TOTP si está activo) y devuelve
// un token del mismo ámbito con `auth_time` reciente.
func (s *Service) Reauth(ctx context.Context, p *authz.Principal, password, code string, in LoginInput) (*Tokens, error) {
	now := s.now()
	sess, u, err := s.sessionFor(ctx, p, "", now)
	if err != nil {
		return nil, err
	}
	ok, _, _ := domain.VerifyPassword(password, u.PasswordHash, s.o.Argon)
	if u.PasswordHash == "" || !ok {
		s.auditLogin(ctx, in, u.ID.String(), "auth.reauth.failed", "failure", nil)
		return nil, errInvalidCredentials
	}
	if t, err := s.confirmedTOTP(ctx, u.ID); err != nil {
		return nil, err
	} else if t != nil && !s.checkSecondFactor(ctx, u.ID, code, now) {
		s.auditLogin(ctx, in, u.ID.String(), "auth.reauth.failed", "failure", map[string]any{"reason": "mfa"})
		return nil, errInvalidCredentials
	}
	switch p.Scope {
	case authz.ScopeTenant:
		return s.IssueToken(ctx, p, "", TokenRequest{TenantID: p.TenantID, AuthTime: now, IP: in.IP})
	case authz.ScopePlatform:
		return s.IssueToken(ctx, p, "", TokenRequest{Platform: true, AuthTime: now, IP: in.IP})
	default:
		return s.sign(s.baseClaims(u, sess, authz.ScopeSession, now))
	}
}

// CheckSession implementa api.SessionChecker (revocación para el gateway).
func (s *Service) CheckSession(ctx context.Context, req api.CheckSessionRequest) (api.CheckSessionResponse, error) {
	key := req.SID.String() + "|" + req.TenantID.String()
	now := s.now()
	s.mu.Lock()
	c, ok := s.cache[key]
	s.mu.Unlock()
	var st SessionStatus
	if ok && now.Before(c.exp) {
		st = c.st
	} else {
		var err error
		st, err = s.store.SessionStatus(ctx, req.SID, req.UserID, req.TenantID)
		if err != nil {
			return api.CheckSessionResponse{}, err
		}
		s.mu.Lock()
		if len(s.cache) > 10000 {
			s.cache = map[string]cachedStatus{}
		}
		s.cache[key] = cachedStatus{st: st, exp: now.Add(sessionCacheTTL)}
		s.mu.Unlock()
	}
	resp := api.CheckSessionResponse{
		Active:           st.SessionActive && st.UserActive,
		MembershipActive: st.MembershipActive,
		TenantActive:     st.TenantActive,
		RevokedReason:    st.RevokedReason,
	}
	if req.ViaPlatform && req.TenantID != uuid.Nil {
		// El acceso de plataforma no tiene membresía: basta con el rol.
		roles, err := s.store.PlatformRoles(ctx, req.UserID)
		if err != nil {
			return api.CheckSessionResponse{}, err
		}
		resp.MembershipActive = slices.Contains(roles, domain.RolePlatformAdmin)
	}
	return resp, nil
}

func (s *Service) invalidate(sid uuid.UUID) {
	prefix := sid.String() + "|"
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.cache {
		if strings.HasPrefix(k, prefix) {
			delete(s.cache, k)
		}
	}
}

// Record implementa api.AuditRecorder.
func (s *Service) Record(ctx context.Context, e api.AuditEntry) error {
	if e.OccurredAt.IsZero() {
		e.OccurredAt = s.now()
	}
	if e.Outcome == "" {
		e.Outcome = "success"
	}
	if e.ActorType == "" {
		e.ActorType = "system"
	}
	if e.ActorID == "" {
		e.ActorID = "system:auth"
	}
	if err := s.store.InsertAudit(ctx, e); err != nil {
		s.o.Logger.ErrorContext(ctx, "audit write failed", slog.String("action", e.Action), slog.Any("error", err))
		return err
	}
	return nil
}

func (s *Service) auditLogin(ctx context.Context, in LoginInput, userID, action, outcome string, changes map[string]any) {
	actor := userID
	if actor == "" {
		actor = "anonymous"
	}
	_ = s.Record(ctx, api.AuditEntry{
		ActorType: "user", ActorID: actor, Action: action, ResourceType: "session", Outcome: outcome,
		IP: in.IP, UserAgent: in.UserAgent, Changes: changes,
	})
}
