package api

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ServiceIntegrationWebhooks → IntegrationWebhooks.
const ServiceIntegrationWebhooks = "alerts.IntegrationWebhooks"

// IntegrationWebhook es el canal webhook hacia un sistema integrado que crea
// el enlace de conexión (docs/integrations/argosisp.md §1.4 y §3.4): URL y
// secreto los da el receptor (Argos). El canal se crea o actualiza APAGADO.
type IntegrationWebhook struct {
	// ChannelID: canal creado por un canje anterior (nil = crear uno nuevo).
	ChannelID *uuid.UUID
	Name      string
	URL       string
	Secret    string
	// ActorID es el usuario a cuyo nombre se hace (auditoría).
	ActorID uuid.UUID
}

// ErrIntegrationWebhookInvalid: URL o secreto no válidos para un canal webhook.
var ErrIntegrationWebhookInvalid = errors.New("alerts: invalid integration webhook")

// IntegrationWebhooks crea o actualiza el canal webhook de una integración.
type IntegrationWebhooks interface {
	UpsertIntegrationWebhook(ctx context.Context, tenant uuid.UUID, w IntegrationWebhook) (uuid.UUID, error)
}
