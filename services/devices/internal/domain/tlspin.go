package domain

import (
	"encoding/hex"
	"errors"
	"strings"
)

// ErrTLSPendingChanged: la huella pendiente ya no es la que se confirma (otra
// observación la sustituyó o ya se confirmó).
var ErrTLSPendingChanged = errors.New("devices: pending TLS fingerprint changed")

// Estados de la huella TLS del router (RouterTlsFingerprintStatus del contrato).
const (
	// TLSPinNotCaptured: aún no se ha leído el certificado del router.
	TLSPinNotCaptured = "not_captured"
	// TLSPinPending: huella observada en el alta (TOFU) a la espera de que un
	// administrador la confirme; hasta entonces Horus no lee datos del router.
	TLSPinPending = "pending_confirmation"
	// TLSPinConfirmed: huella fijada; las lecturas en segundo plano la exigen.
	TLSPinConfirmed = "confirmed"
	// TLSPinChanged: el router presenta una huella distinta de la fijada; las
	// lecturas se detienen hasta que un administrador vuelva a fijarla.
	TLSPinChanged = "changed"
)

// Códigos de error de la confirmación de huella.
const (
	CodeTLSFingerprintUnconfirmed = "ROUTER_TLS_FINGERPRINT_UNCONFIRMED"
	CodeTLSFingerprintMismatch    = "ROUTER_TLS_FINGERPRINT_MISMATCH"
)

// TLSPinStatus deriva el estado de la huella de una credencial de la API.
func TLSPinStatus(c *Credential) string {
	switch {
	case c == nil:
		return TLSPinNotCaptured
	case c.TLSFingerprintSHA256 != nil && *c.TLSFingerprintSHA256 != "":
		if c.TLSPending != nil && !strings.EqualFold(*c.TLSPending, *c.TLSFingerprintSHA256) {
			return TLSPinChanged
		}
		return TLSPinConfirmed
	case c.TLSPending != nil:
		return TLSPinPending
	}
	return TLSPinNotCaptured
}

// NormalizeFingerprint acepta un SHA-256 en hexadecimal (64 dígitos, con o
// sin «:», mayúsculas o minúsculas) y lo devuelve como AA:BB:… en mayúsculas.
func NormalizeFingerprint(raw string) (string, bool) {
	clean := strings.ReplaceAll(strings.TrimSpace(raw), ":", "")
	b, err := hex.DecodeString(clean)
	if err != nil || len(b) != 32 {
		return "", false
	}
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = strings.ToUpper(hex.EncodeToString([]byte{x}))
	}
	return strings.Join(parts, ":"), true
}
