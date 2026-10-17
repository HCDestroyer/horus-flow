package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

var _ api.IntegrationTokens = (*APITokens)(nil)

// IssueIntegrationToken implementa api.IntegrationTokens: el enlace de
// conexión (docs/integrations/argosisp.md §1.4) emite un token con la
// plantilla a nombre de quien generó o canjeó el enlace, solo si sigue siendo
// miembro activo del ISP y tiene hoy TODOS los permisos de la plantilla.
func (s *APITokens) IssueIntegrationToken(ctx context.Context, req api.IntegrationTokenRequest) (api.IntegrationToken, error) {
	tpl, ok := domain.APITokenTemplateByKey(req.Template)
	if !ok {
		return api.IntegrationToken{}, fmt.Errorf("auth: unknown api token template %q", req.Template)
	}
	u, err := s.store.UserByID(ctx, req.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		return api.IntegrationToken{}, api.ErrIntegrationTokenNotGranted
	}
	if err != nil {
		return api.IntegrationToken{}, err
	}
	if u.Status != domain.UserActive {
		return api.IntegrationToken{}, api.ErrIntegrationTokenNotGranted
	}
	ms, err := s.store.Memberships(ctx, req.UserID)
	if err != nil {
		return api.IntegrationToken{}, err
	}
	var eff map[string][]string
	for i := range ms {
		if m := &ms[i]; m.Tenant.ID == req.TenantID && m.Status == domain.MembershipActive && m.Tenant.Status == domain.TenantActive {
			eff = m.EffectivePermissions()
		}
	}
	for _, p := range tpl.Permissions {
		if len(eff[p]) == 0 {
			return api.IntegrationToken{}, api.ErrIntegrationTokenNotGranted
		}
	}
	name := strings.TrimSpace(req.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 80 {
		name = tpl.Name
	}
	raw, err := domain.GenerateAPIToken()
	if err != nil {
		return api.IntegrationToken{}, err
	}
	now := s.now().UTC()
	key := tpl.Key
	t := domain.APIToken{ID: uuid.Must(uuid.NewV7()), TenantID: req.TenantID, UserID: req.UserID, Name: name,
		Prefix: raw[:len(domain.APITokenPrefix)+4], Hash: domain.HashAPIToken(raw), Permissions: slices.Clone(tpl.Permissions),
		Template: &key, CreatedAt: now, ExpiresAt: now.Add(domain.APITokenDefaultTTL)}
	if err := s.store.CreateAPIToken(ctx, &t); err != nil {
		return api.IntegrationToken{}, err
	}
	if s.audit != nil {
		if err := s.audit.Record(ctx, api.AuditEntry{TenantID: req.TenantID, OccurredAt: now, ActorType: "user", ActorID: req.UserID.String(),
			Action: "auth.api_token.created", ResourceType: "api_token", ResourceID: t.ID.String(), Scope: "tenant", Outcome: "success",
			IP: req.IP, Changes: map[string]any{"name": name, "prefix": t.Prefix, "permissions": t.Permissions, "template": key,
				"expires_at": t.ExpiresAt.Format(time.RFC3339), "via": req.Via}}); err != nil {
			s.logger.ErrorContext(ctx, "api token audit failed", slog.Any("error", err))
		}
	}
	return api.IntegrationToken{ID: t.ID, Raw: raw, ExpiresAt: t.ExpiresAt}, nil
}

// RevokeIntegrationToken implementa api.IntegrationTokens (sin error si ya
// estaba revocado o no existe en el ISP).
func (s *APITokens) RevokeIntegrationToken(ctx context.Context, tenant, id uuid.UUID, reason string) error {
	t, err := s.store.RevokeAPIToken(ctx, tenant, id, uuid.Nil, s.now().UTC())
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	s.forget(t.ID)
	if s.audit != nil {
		if err := s.audit.Record(ctx, api.AuditEntry{TenantID: tenant, OccurredAt: s.now().UTC(), ActorType: "system",
			ActorID: "system:integrations", Action: "auth.api_token.revoked", ResourceType: "api_token", ResourceID: t.ID.String(),
			Scope: "tenant", Outcome: "success", Changes: map[string]any{"name": t.Name, "prefix": t.Prefix, "reason": reason}}); err != nil {
			s.logger.ErrorContext(ctx, "api token audit failed", slog.Any("error", err))
		}
	}
	return nil
}

var _ api.UserProfiles = (*Service)(nil)

// UserProfile implementa api.UserProfiles.
func (s *Service) UserProfile(ctx context.Context, id uuid.UUID) (*api.UserProfile, error) {
	u, err := s.store.UserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &api.UserProfile{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName}, nil
}
