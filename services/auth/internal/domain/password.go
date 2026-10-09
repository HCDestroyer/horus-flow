// Package domain contiene las reglas puras del módulo auth: hash de
// contraseñas Argon2id (PHC), política de contraseñas, TOTP (RFC 6238),
// retardo progresivo de login, cifrado de secretos (envelope encryption) y el
// catálogo de permisos C7 (docs/security.md §4–§6).
package domain

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2Params son los parámetros de Argon2id (security.md S2).
type Argon2Params struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

// DefaultArgon2 son los parámetros de producción: m=64 MiB, t=3, p=1.
var DefaultArgon2 = Argon2Params{Memory: 64 * 1024, Time: 3, Threads: 1, SaltLen: 16, KeyLen: 32}

// ErrInvalidHash indica un hash PHC mal formado.
var ErrInvalidHash = errors.New("auth: invalid password hash")

// HashPassword devuelve el hash PHC `$argon2id$v=19$m=…,t=…,p=…$sal$hash`.
func HashPassword(password string, p Argon2Params) (string, error) {
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword compara en tiempo constante. needsRehash es true si el hash
// usa parámetros distintos de want (se re-hashea en el siguiente login).
func VerifyPassword(password, phc string, want Argon2Params) (ok, needsRehash bool, err error) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, false, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, false, ErrInvalidHash
	}
	var p Argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return false, false, ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, false, ErrInvalidHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return false, false, ErrInvalidHash
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(key))) //nolint:gosec // longitud acotada
	ok = subtle.ConstantTimeCompare(got, key) == 1
	needsRehash = p.Memory != want.Memory || p.Time != want.Time || p.Threads != want.Threads ||
		uint32(len(salt)) != want.SaltLen || uint32(len(key)) != want.KeyLen //nolint:gosec // longitudes acotadas
	return ok, needsRehash, nil
}

// Política de contraseñas (NIST SP 800-63B, security.md §4.2).
const (
	PasswordMinLen = 12
	PasswordMaxLen = 128
)

// commonPasswords es una lista mínima embebida de contraseñas filtradas
// frecuentes (una por línea, en minúsculas).
//
//go:embed common_passwords.txt
var commonPasswordsTxt string

var commonPasswords = func() map[string]struct{} {
	m := map[string]struct{}{}
	for _, l := range strings.Split(commonPasswordsTxt, "\n") {
		if l = strings.TrimSpace(strings.ToLower(l)); l != "" {
			m[l] = struct{}{}
		}
	}
	return m
}()

// Códigos de error de campo de la política.
const (
	PwTooShort  = "PASSWORD_TOO_SHORT"
	PwTooLong   = "PASSWORD_TOO_LONG"
	PwCommon    = "PASSWORD_TOO_COMMON"
	PwPersonal  = "PASSWORD_CONTAINS_PERSONAL_DATA"
	PwSameAsOld = "PASSWORD_UNCHANGED"
)

// ValidateNewPassword aplica la política: 12–128 caracteres, sin reglas de
// composición, rechazo de contraseñas filtradas y del propio email/nombre.
// Devuelve el código de error o "".
func ValidateNewPassword(password, email, displayName string) string {
	n := utf8.RuneCountInString(password)
	switch {
	case n < PasswordMinLen:
		return PwTooShort
	case n > PasswordMaxLen:
		return PwTooLong
	}
	lower := strings.ToLower(password)
	if _, ok := commonPasswords[lower]; ok {
		return PwCommon
	}
	local, _, _ := strings.Cut(strings.ToLower(email), "@")
	if len(local) >= 4 && strings.Contains(lower, local) {
		return PwPersonal
	}
	if dn := strings.ToLower(strings.ReplaceAll(displayName, " ", "")); len(dn) >= 4 && strings.Contains(strings.ReplaceAll(lower, " ", ""), dn) {
		return PwPersonal
	}
	return ""
}

// Retardo progresivo por cuenta (security.md §3.3): a partir del 5.º fallo
// consecutivo, 1, 2, 4… s hasta 15 min. Mientras dura, el login responde
// igual que con credenciales inválidas (no revela si la cuenta existe).
const (
	LockThreshold = 5
	LockMax       = 15 * time.Minute
)

// LockDelay devuelve el bloqueo tras failed fallos consecutivos.
func LockDelay(failed int) time.Duration {
	if failed < LockThreshold {
		return 0
	}
	exp := failed - LockThreshold
	if exp > 20 {
		return LockMax
	}
	d := time.Second << exp
	if d > LockMax {
		return LockMax
	}
	return d
}
