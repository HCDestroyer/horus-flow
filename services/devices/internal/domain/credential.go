package domain

import (
	"time"

	"github.com/google/uuid"
)

// Tipos de credencial (devices.credential.kind).
const (
	CredentialSNMPv3      = "snmp_v3"
	CredentialRouterOSAPI = "routeros_api"
	// CredentialIPsecPSK es la clave precompartida IKEv2 del túnel IPsec del
	// router (docs/tunnels.md §4.3).
	CredentialIPsecPSK = "ipsec_psk"
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
	// TLSPending es la huella observada a la espera de confirmación
	// (primer contacto o distinta de la fijada); nunca se acepta sola.
	TLSPending     *string
	TLSPendingAt   *time.Time
	TLSConfirmedAt *time.Time
	TLSConfirmedBy *string
	LastUsedAt     *time.Time
	LastResult     *string
	UpdatedAt      time.Time
	Version        int
}
