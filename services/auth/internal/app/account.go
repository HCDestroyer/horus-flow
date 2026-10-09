package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// MeView es la respuesta de GET /me.
type MeView struct {
	User                *domain.User
	MFAEnabled          bool
	PlatformRoles       []string
	PlatformPermissions []string
	Memberships         []domain.Membership
}

// Me devuelve el usuario, sus roles de plataforma y sus membresías con los
// permisos efectivos por ISP.
func (s *Service) Me(ctx context.Context, p *authz.Principal) (*MeView, error) {
	_, u, err := s.sessionFor(ctx, p, "", s.now())
	if err != nil {
		return nil, err
	}
	roles, err := s.store.PlatformRoles(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	perms, _ := s.o.Catalog.PlatformPermissions(roles)
	ms, err := s.store.Memberships(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	t, err := s.confirmedTOTP(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	if roles == nil {
		roles = []string{}
	}
	if perms == nil {
		perms = []string{}
	}
	return &MeView{User: u, MFAEnabled: t != nil, PlatformRoles: roles, PlatformPermissions: perms, Memberships: ms}, nil
}

// ChangePassword cambia la contraseña propia y revoca las demás sesiones.
func (s *Service) ChangePassword(ctx context.Context, p *authz.Principal, current, next string, in LoginInput) error {
	now := s.now()
	sess, u, err := s.sessionFor(ctx, p, "", now)
	if err != nil {
		return err
	}
	ok, _, _ := domain.VerifyPassword(current, u.PasswordHash, s.o.Argon)
	if u.PasswordHash == "" || !ok {
		return apperr.Validation(apperr.Field("current_password", problem.CodeInvalidCredentials, "La contraseña actual no es correcta."))
	}
	if code := domain.ValidateNewPassword(next, u.Email, u.DisplayName); code != "" {
		return apperr.Validation(apperr.Field("new_password", code, "La contraseña no cumple la política (12–128 caracteres, no común, sin datos personales)."))
	}
	if next == current {
		return apperr.Validation(apperr.Field("new_password", domain.PwSameAsOld, "La contraseña nueva debe ser distinta."))
	}
	h, err := domain.HashPassword(next, s.o.Argon)
	if err != nil {
		return err
	}
	if err := s.store.ChangePassword(ctx, u.ID, h, now, sess.ID); err != nil {
		return err
	}
	s.mu.Lock()
	s.cache = map[string]cachedStatus{}
	s.mu.Unlock()
	s.auditLogin(ctx, in, u.ID.String(), "auth.password.changed", "success", map[string]any{"sid": sess.ID.String()})
	return nil
}

// TOTPEnrollment es el secreto que se muestra una sola vez.
type TOTPEnrollment struct {
	URI    string
	Secret string
}

// EnrollTOTP genera un secreto TOTP pendiente de confirmar.
func (s *Service) EnrollTOTP(ctx context.Context, p *authz.Principal) (*TOTPEnrollment, error) {
	_, u, err := s.sessionFor(ctx, p, "", s.now())
	if err != nil {
		return nil, err
	}
	if t, err := s.confirmedTOTP(ctx, u.ID); err != nil {
		return nil, err
	} else if t != nil {
		return nil, apperr.Conflict(problem.CodeAlreadyExists, "El TOTP ya está activo.")
	}
	secret, err := domain.NewTOTPSecret()
	if err != nil {
		return nil, err
	}
	ct, dek, err := s.o.Sealer.Seal(secret, []byte("totp:"+u.ID.String()))
	if err != nil {
		return nil, err
	}
	if err := s.store.PutPendingTOTP(ctx, &domain.TOTP{UserID: u.ID, Ciphertext: ct, DEKWrapped: dek, KEKID: s.o.Sealer.KEKID()}); err != nil {
		return nil, err
	}
	return &TOTPEnrollment{URI: domain.OTPAuthURI(s.o.TOTPIssuer, u.Email, secret), Secret: domain.EncodeTOTPSecret(secret)}, nil
}

// ConfirmTOTP activa el TOTP con un código válido, marca la sesión actual
// como verificada con 2FA y devuelve los 10 códigos de recuperación.
func (s *Service) ConfirmTOTP(ctx context.Context, p *authz.Principal, code string, in LoginInput) ([]string, error) {
	now := s.now()
	sess, u, err := s.sessionFor(ctx, p, "", now)
	if err != nil {
		return nil, err
	}
	t, err := s.store.TOTP(ctx, u.ID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && t.ConfirmedAt != nil) {
		return nil, apperr.Validation(apperr.Field("code", problem.CodeConflict, "No hay un alta de TOTP pendiente."))
	}
	if err != nil {
		return nil, err
	}
	secret, err := s.o.Sealer.Open(t.Ciphertext, t.DEKWrapped, []byte("totp:"+u.ID.String()))
	if err != nil {
		return nil, fmt.Errorf("totp secret: %w", err)
	}
	step, ok := domain.VerifyTOTP(secret, code, now, t.LastUsedStep)
	if !ok {
		return nil, apperr.Validation(apperr.Field("code", "INVALID_CODE", "El código no es válido."))
	}
	codes, err := domain.NewRecoveryCodes()
	if err != nil {
		return nil, err
	}
	hashes := make([][]byte, len(codes))
	for i, c := range codes {
		hashes[i] = domain.HashRecoveryCode(u.ID.String(), c)
	}
	if err := s.store.ConfirmTOTP(ctx, u.ID, step, hashes, now, sess.ID); err != nil {
		return nil, err
	}
	s.auditLogin(ctx, in, u.ID.String(), "auth.mfa.enabled", "success", map[string]any{"method": "totp"})
	return codes, nil
}

// Catalog devuelve el catálogo de permisos C7.
func (s *Service) Catalog() *domain.Catalog { return s.o.Catalog }

// Bootstrap sincroniza el catálogo y crea el superadministrador semilla.
func (s *Service) Bootstrap(ctx context.Context, seedEmail, seedPassword, seedName string) error {
	if err := s.store.SyncCatalog(ctx, s.o.Catalog); err != nil {
		return fmt.Errorf("sync catalog: %w", err)
	}
	if seedEmail == "" {
		return nil
	}
	if code := domain.ValidateNewPassword(seedPassword, "", ""); code == domain.PwTooShort || code == domain.PwTooLong {
		return fmt.Errorf("seed admin password: %s", strings.ToLower(code))
	}
	h, err := domain.HashPassword(seedPassword, s.o.Argon)
	if err != nil {
		return err
	}
	u := &domain.User{
		ID: uuid.Must(uuid.NewV7()), Email: strings.TrimSpace(seedEmail), DisplayName: seedName, PasswordHash: h,
		Status: domain.UserActive, IsPlatformAdmin: true, MFAEnforced: true, MustChangePassword: true,
	}
	created, err := s.store.EnsureSeedAdmin(ctx, u)
	if err != nil {
		return fmt.Errorf("seed admin: %w", err)
	}
	if created {
		s.o.Logger.InfoContext(ctx, "seed superadmin created; password change and TOTP required at first login",
			slog.String("user_id", u.ID.String()))
		_ = s.Record(ctx, api.AuditEntry{Action: "platform.superadmin.seeded", ResourceType: "user", ResourceID: u.ID.String()})
	}
	return nil
}
