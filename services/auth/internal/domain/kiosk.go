package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Parámetros del kiosco (permissions.yaml kiosk; api.md §2.12).
const (
	KioskCodeLength    = 8
	KioskCodeAlphabet  = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // A-HJ-NP-Z2-9
	KioskCodeTTL       = 10 * time.Minute
	KioskCodeMaxFails  = 10
	KioskDefaultExpiry = 180 * 24 * time.Hour
	KioskIdleTTL       = 14 * 24 * time.Hour
	KioskCookie        = "__Secure-hf_kiosk"
	KioskCookiePath    = "/api/v1/kiosk"
)

// Estados del kiosco.
const (
	KioskPending = "pending_enrollment"
	KioskActive  = "active"
	KioskRevoked = "revoked"
	KioskExpired = "expired"
)

// Kiosk es una pantalla NOC registrada como dispositivo (ADR-0026).
type Kiosk struct {
	ID                     uuid.UUID
	TenantID               uuid.UUID
	Name                   string
	Status                 string
	PlaylistID             *uuid.UUID
	DashboardIDs           []uuid.UUID
	AllowedCIDRs           []netip.Prefix
	ShowPersonalData       bool
	ShowPersonalDataReason *string
	CriticalFindingBanner  bool
	CredentialHash         []byte
	CredentialFamily       *uuid.UUID
	ExpiresAt              time.Time
	LastSeenAt             *time.Time
	LastIP                 *netip.Addr
	CreatedAt              time.Time
	UpdatedAt              time.Time
	Version                int
}

// EffectiveStatus aplica la caducidad (absoluta e inactividad de 14 días).
func (k *Kiosk) EffectiveStatus(now time.Time) string {
	if k.Status == KioskActive || k.Status == KioskPending {
		if !now.Before(k.ExpiresAt) {
			return KioskExpired
		}
		if k.Status == KioskActive && k.LastSeenAt != nil && now.Sub(*k.LastSeenAt) > KioskIdleTTL {
			return KioskExpired
		}
	}
	return k.Status
}

// AllowsIP aplica allowed_cidrs (vacío = cualquier IP).
func (k *Kiosk) AllowsIP(ip string) bool {
	if len(k.AllowedCIDRs) == 0 {
		return true
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range k.AllowedCIDRs {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// NewKioskCode genera un código de enrolamiento de 8 caracteres.
func NewKioskCode() string {
	b := make([]byte, KioskCodeLength)
	_, _ = rand.Read(b)
	var sb strings.Builder
	for _, x := range b {
		sb.WriteByte(KioskCodeAlphabet[int(x)%len(KioskCodeAlphabet)])
	}
	return sb.String()
}

// ValidKioskCode comprueba el formato del código.
func ValidKioskCode(c string) bool {
	if len(c) != KioskCodeLength {
		return false
	}
	for _, r := range c {
		if !strings.ContainsRune(KioskCodeAlphabet, r) {
			return false
		}
	}
	return true
}

// NewKioskCredential genera una credencial opaca de 256 bits (base64url).
func NewKioskCredential() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashSecret devuelve el SHA-256 de un secreto (solo el hash va a la base).
func HashSecret(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}
