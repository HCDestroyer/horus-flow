package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	alertsapi "github.com/hcdestroyer/horus-flow/services/alerts/api"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

// IntegrationWebhooks implementa alertsapi.IntegrationWebhooks: el enlace de
// conexión con ArgosISP (docs/integrations/argosisp.md §1.4) deja preparado
// el canal webhook hacia Argos (§3.4) con su URL y su secreto, APAGADO hasta
// que un administrador lo active. Mismas reglas que un canal webhook creado a
// mano (https, guardia de egreso al enviar, secreto cifrado con la KEK).
type IntegrationWebhooks struct{ Svc *Service }

var _ alertsapi.IntegrationWebhooks = IntegrationWebhooks{}

// integrationSubscription son los eventos del contrato §3.4 (las mismas
// categorías que la consola propone para un webhook nuevo), sin agrupar.
func integrationSubscription() Subscription {
	zero, info := 0, "info"
	return Subscription{EventTypes: []string{}, Categories: []string{CategoryInfrastructure, CategoryRouters, CategorySecurity,
		CategorySecurityUpdates}, MinSeverity: &info, SiteIDs: []uuid.UUID{}, ThrottleMinutes: &zero}
}

// UpsertIntegrationWebhook implementa alertsapi.IntegrationWebhooks.
func (h IntegrationWebhooks) UpsertIntegrationWebhook(ctx context.Context, tenant uuid.UUID, w alertsapi.IntegrationWebhook) (uuid.UUID, error) {
	s := h.Svc
	t := pgdb.TenantID(tenant)
	var f fieldErrs
	raw, _ := json.Marshal(WebhookConfig{URL: w.URL})
	cfg := normalizeWebhook(raw, &f, s.webhookAllowPrivate)
	if n := len(w.Secret); n < 16 || n > 256 {
		f.add("webhook_secret", "INVALID_VALUE", "Secreto de 16 a 256 caracteres.")
	}
	if f.err() != nil || cfg == nil {
		return uuid.Nil, alertsapi.ErrIntegrationWebhookInvalid
	}
	name := w.Name
	if l := len([]rune(name)); l == 0 || l > 80 {
		name = "ArgosISP"
	}
	now := s.ts()
	var c *Channel
	created := false
	if w.ChannelID != nil {
		cur, err := s.st.getChannel(ctx, t, *w.ChannelID)
		switch {
		case err == nil && cur.Kind == KindWebhook:
			c = cur
		case err != nil && !errors.Is(err, errNotFound):
			return uuid.Nil, err
		}
	}
	if c == nil {
		c = &Channel{ID: uuid.Must(uuid.NewV7()), TenantID: tenant, Name: name, Kind: KindWebhook, CreatedAt: now, UpdatedAt: now, Version: 1,
			Subscription: integrationSubscription()}
		created = true
	}
	c.Enabled, c.Config, c.Status = false, cfg, statusUnverified
	plain, _ := json.Marshal(Credentials{WebhookSecret: w.Secret})
	ct, dek, err := s.sealer.Seal(plain, aad(c))
	if err != nil {
		return uuid.Nil, fmt.Errorf("alerts: seal: %w", err)
	}
	kek := s.sealer.KEKID()
	c.SecretCiphertext, c.DEKWrapped, c.KEKID = ct, dek, &kek
	if created {
		if err := s.st.insertChannel(ctx, t, c); err != nil {
			return uuid.Nil, err
		}
		// insertChannel no guarda los secretos: se completan con la actualización.
		if err := s.st.updateChannel(ctx, t, c, 0); err != nil {
			return uuid.Nil, err
		}
	} else if err := s.st.updateChannel(ctx, t, c, 0); err != nil {
		return uuid.Nil, err
	}
	action := "alerts.channel.credentials.updated"
	if created {
		action = "alerts.channel.created"
	}
	if rec, ok := s.audit(); ok {
		_ = rec.Record(ctx, authapi.AuditEntry{TenantID: tenant, OccurredAt: now, ActorType: "user", ActorID: w.ActorID.String(),
			Action: action, ResourceType: "notification_channel", ResourceID: c.ID.String(), Scope: "tenant", Outcome: "success",
			Changes: map[string]any{"kind": KindWebhook, "via": "connection_link", "enabled": false, "fields": []string{"config.url", "webhook_secret"}}})
	}
	return c.ID, nil
}
