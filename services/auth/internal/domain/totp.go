package domain

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 usa HMAC-SHA1 (compatibilidad con apps de autenticación)
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP (RFC 6238): HMAC-SHA1, 6 dígitos, paso 30 s, ventana ±1 paso; se
// guarda el último paso usado para impedir reutilizar un código.
const (
	TOTPDigits     = 6
	TOTPPeriod     = 30
	TOTPSecretSize = 20
	TOTPWindow     = 1
	RecoveryCodes  = 10
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret genera un secreto de 20 B.
func NewTOTPSecret() ([]byte, error) {
	s := make([]byte, TOTPSecretSize)
	if _, err := rand.Read(s); err != nil {
		return nil, fmt.Errorf("auth: totp secret: %w", err)
	}
	return s, nil
}

// EncodeTOTPSecret codifica el secreto en base32 (lo que teclea el usuario).
func EncodeTOTPSecret(secret []byte) string { return b32.EncodeToString(secret) }

// TOTPStep devuelve el paso de t.
func TOTPStep(t time.Time) int64 { return t.Unix() / TOTPPeriod }

// TOTPCode calcula el código de un paso (RFC 4226 §5.3).
func TOTPCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // paso positivo
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1_000_000)
}

// VerifyTOTP comprueba code en la ventana ±1 paso alrededor de now y solo si
// el paso es posterior a lastStep. Devuelve el paso aceptado.
func VerifyTOTP(secret []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != TOTPDigits {
		return 0, false
	}
	cur := TOTPStep(now)
	for d := int64(-TOTPWindow); d <= TOTPWindow; d++ {
		step := cur + d
		if step <= lastStep {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(TOTPCode(secret, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// OTPAuthURI devuelve la URI `otpauth://` para el QR.
func OTPAuthURI(issuer, account string, secret []byte) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", EncodeTOTPSecret(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(TOTPDigits))
	q.Set("period", fmt.Sprint(TOTPPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// NewRecoveryCodes genera códigos de recuperación de 10 caracteres base32.
func NewRecoveryCodes() ([]string, error) {
	out := make([]string, RecoveryCodes)
	for i := range out {
		b := make([]byte, 7)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("auth: recovery code: %w", err)
		}
		out[i] = b32.EncodeToString(b)[:10]
	}
	return out, nil
}

// HashRecoveryCode es el hash con el que se guarda un código (alta entropía:
// SHA-256 basta, security.md §4.3).
func HashRecoveryCode(userID, code string) []byte {
	sum := sha256.Sum256([]byte(userID + ":" + strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))))
	return sum[:]
}
