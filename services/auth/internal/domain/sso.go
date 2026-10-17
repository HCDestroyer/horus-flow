package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"

	"github.com/google/uuid"
)

// Autologin entre sistemas (contrato docs/integrations/argosisp.md §1.5).
const (
	// SSOTicketTTL es la vida de un ticket (un solo uso).
	SSOTicketTTL = 60 * time.Second
	// SSOLinkTokenTTL es la vida del link_token de la primera vinculación.
	SSOLinkTokenTTL = 10 * time.Minute
	// SSOSystemArgos es el único sistema integrado.
	SSOSystemArgos = "argosisp"
	// AMRSSO marca una sesión abierta por un ticket de autologin (sin
	// contraseña en Horus; el 2FA de Horus se exige igual).
	AMRSSO       = "sso"
	ssoTicketLen = 32
)

// SSOTargetTypes son los destinos del contrato.
var SSOTargetTypes = []string{"cliente", "servicio", "router", "hallazgo"}

// ValidSSOTargetType indica si t es un destino del contrato.
func ValidSSOTargetType(t string) bool {
	for _, x := range SSOTargetTypes {
		if x == t {
			return true
		}
	}
	return false
}

// SSOTicket es un ticket de autologin. Solo se guarda el SHA-256 del valor.
type SSOTicket struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	System         string
	ConnectorID    uuid.UUID
	APITokenID     *uuid.UUID
	Hash           []byte
	ExternalUserID string
	ExternalEmail  *string
	ExternalName   *string
	TargetType     string
	TargetID       string
	CreatedAt      time.Time
	ExpiresAt      time.Time
	RedeemedAt     *time.Time
	LinkConsumedAt *time.Time
}

// SSOLink vincula un usuario de otro sistema (por conector) con un usuario de
// Horus. Correo y nombre externos son informativos: nunca deciden la
// vinculación.
type SSOLink struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	TenantName     string
	System         string
	ConnectorID    uuid.UUID
	ExternalUserID string
	ExternalEmail  *string
	ExternalName   *string
	UserID         uuid.UUID
	UserEmail      string
	CreatedAt      time.Time
	LastUsedAt     *time.Time
}

// GenerateSSOTicket devuelve un ticket nuevo (32 bytes aleatorios en
// base64url, 43 caracteres).
func GenerateSSOTicket() (string, error) {
	b := make([]byte, ssoTicketLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// LooksLikeSSOTicket comprueba la forma sin tocar la base.
func LooksLikeSSOTicket(raw string) bool {
	if len(raw) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	return err == nil && len(b) == ssoTicketLen
}

// HashSSOTicket es el SHA-256 del ticket (lo único que se guarda).
func HashSSOTicket(raw string) []byte {
	s := sha256.Sum256([]byte("sso_ticket:" + raw))
	return s[:]
}
