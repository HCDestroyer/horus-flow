package domain

import (
	"time"

	"github.com/google/uuid"
)

// Tipos de credencial (devices.credential.kind).
const (
	CredentialSNMPv3      = "snmp_v3"
	CredentialRouterOSAPI = "routeros_api"
)

// Credential es una credencial de router cifrada (envelope encryption).
type Credential struct {
	ID                   uuid.UUID
	RouterID             uuid.UUID
	Kind                 string
	Username             string
	Ciphertext           []byte
	DEKWrapped           []byte
	KEKID                string
	TLSFingerprintSHA256 *string
	LastUsedAt           *time.Time
	LastResult           *string
	UpdatedAt            time.Time
	Version              int
}
