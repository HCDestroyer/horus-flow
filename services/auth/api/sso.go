package api

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Nombres en module.Services para el enlace de conexión y el autologin con
// ArgosISP (docs/integrations/argosisp.md §1.4 y §1.5).
const (
	// ServiceSSOTickets → SSOTickets (auth).
	ServiceSSOTickets = "auth.SSOTickets"
	// ServiceIntegrationTokens → IntegrationTokens (auth).
	ServiceIntegrationTokens = "auth.IntegrationTokens"
	// ServiceUserProfiles → UserProfiles (auth).
	ServiceUserProfiles = "auth.UserProfiles"
	// ServiceSSOConnectors → SSOConnectors. Lo implementa el dueño del
	// conector (devices) y auth lo consulta al canjear: así auth no depende
	// de devices.
	ServiceSSOConnectors = "auth.SSOConnectors"
)

// SSOTicketRequest es un ticket a emitir. El llamante (devices) ya validó los
// campos y que el conector está activo; auth guarda solo el hash.
type SSOTicketRequest struct {
	TenantID       uuid.UUID
	ConnectorID    uuid.UUID
	APITokenID     uuid.UUID
	ExternalUserID string
	ExternalEmail  *string
	ExternalName   *string
	TargetType     string // cliente | servicio | router | hallazgo
	TargetID       string
}

// SSOTicketIssued es el ticket emitido (el valor solo viaja en la respuesta).
type SSOTicketIssued struct {
	Ticket    string
	ExpiresAt time.Time
}

// SSOTickets emite tickets de autologin (un uso, 60 s). El canje lo hace auth
// en POST /integrations/argosisp/sso/redeem.
type SSOTickets interface {
	IssueSSOTicket(ctx context.Context, req SSOTicketRequest) (SSOTicketIssued, error)
}

// SSOConnectors dice si el conector que emitió un ticket sigue activo en su
// ISP. Sin implementación en el proceso, auth rechaza el canje (fail-closed).
type SSOConnectors interface {
	SSOConnectorActive(ctx context.Context, tenant, connector uuid.UUID) (bool, error)
}

// IntegrationTokenRequest pide un API token hf_pat_ para un sistema integrado
// a nombre de UserID (enlace de conexión, §1.4).
type IntegrationTokenRequest struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
	Template string // p. ej. "argosisp"
	Name     string
	// Via describe el origen para la auditoría (p. ej. "connection_link").
	Via string
	IP  string
}

// IntegrationToken es el token emitido (Raw solo viaja en la respuesta).
type IntegrationToken struct {
	ID        uuid.UUID
	Raw       string
	ExpiresAt time.Time
}

// ErrIntegrationTokenNotGranted: el usuario ya no es miembro activo del ISP
// o no tiene todos los permisos de la plantilla (un token nunca lleva más
// que su creador).
var ErrIntegrationTokenNotGranted = errors.New("auth: integration token not granted")

// IntegrationTokens emite y revoca API tokens de integración desde otros
// módulos (sin principal de persona: el canje del enlace es público).
type IntegrationTokens interface {
	IssueIntegrationToken(ctx context.Context, req IntegrationTokenRequest) (IntegrationToken, error)
	// RevokeIntegrationToken revoca el token (ya revocado o inexistente = sin error).
	RevokeIntegrationToken(ctx context.Context, tenant, id uuid.UUID, reason string) error
}

// UserProfile son los datos de un usuario que se comparten con un sistema
// integrado al pedirle un ticket ("Ver en ArgosISP").
type UserProfile struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
}

// UserProfiles lee el perfil de un usuario.
type UserProfiles interface {
	UserProfile(ctx context.Context, id uuid.UUID) (*UserProfile, error)
}
