package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// APITokenPrefix es el prefijo de los API tokens de integración.
const APITokenPrefix = "hf_pat_"

// Límites de los API tokens (docs/security.md §5.4).
const (
	APITokenDefaultTTL = 365 * 24 * time.Hour
	// APITokenMaxTTL admite un día de holgura sobre el año por zonas horarias del cliente.
	APITokenMaxTTL = 366 * 24 * time.Hour
	APITokenMinTTL = time.Hour
	apiTokenBytes  = 32
	apiTokenChars  = 43 // ceil(256 / log2(62))
	base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// APIToken es un token de integración ligado a un ISP. Solo se guarda su
// SHA-256; Permissions son claves de permiso (los alcances salen, en cada
// canje, de las asignaciones actuales del creador).
type APIToken struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	UserID      uuid.UUID // creador: el token actúa con sus permisos actuales ∩ Permissions
	Name        string
	Prefix      string // primeros caracteres, para reconocerlo en la lista
	Hash        []byte
	Permissions []string
	Template    *string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	LastUsedAt  *time.Time
	LastUsedIP  *string
	RevokedAt   *time.Time
	RevokedBy   *uuid.UUID
}

// Active indica si el token sirve en now.
func (t *APIToken) Active(now time.Time) bool { return t.RevokedAt == nil && now.Before(t.ExpiresAt) }

// GenerateAPIToken devuelve un token nuevo `hf_pat_<43 caracteres base62>`
// (32 bytes aleatorios).
func GenerateAPIToken() (string, error) {
	b := make([]byte, apiTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	n := new(big.Int).SetBytes(b)
	base := big.NewInt(62)
	mod := new(big.Int)
	out := make([]byte, apiTokenChars)
	for i := apiTokenChars - 1; i >= 0; i-- {
		n.DivMod(n, base, mod)
		out[i] = base62Alphabet[mod.Int64()]
	}
	return APITokenPrefix + string(out), nil
}

// LooksLikeAPIToken comprueba la forma (prefijo, longitud y alfabeto) sin
// tocar la base.
func LooksLikeAPIToken(raw string) bool {
	rest, ok := strings.CutPrefix(raw, APITokenPrefix)
	if !ok || len(rest) != apiTokenChars {
		return false
	}
	for i := 0; i < len(rest); i++ {
		if !strings.ContainsRune(base62Alphabet, rune(rest[i])) {
			return false
		}
	}
	return true
}

// HashAPIToken es el SHA-256 del token (lo único que se guarda).
func HashAPIToken(raw string) []byte {
	s := sha256.Sum256([]byte(raw))
	return s[:]
}

// APITokenTemplate es una plantilla de permisos para una integración.
type APITokenTemplate struct {
	Key         string
	Name        string
	Description string
	Permissions []string
}

// APITokenTemplates son las plantillas disponibles. "argosisp" es el código
// de conexión para ArgosISP (docs/integrations/argosisp.md §1.1): consulta
// 360 por IP (customers.read), consumo (traffic.customer.read), hallazgos y
// estado de seguridad (security.findings.read), salud de la red (snmp.read)
// y, desde el contrato 1.2.0, tickets de autologin hacia Horus (sso.issue,
// §1.5).
var APITokenTemplates = []APITokenTemplate{
	{Key: "argosisp", Name: "ArgosISP", Description: "Código de conexión para ArgosISP: clientes, consumo, seguridad, salud de la red y autologin hacia Horus.",
		Permissions: []string{"customers.read", "security.findings.read", "snmp.read", "sso.issue", "traffic.customer.read"}},
}

// APITokenTemplateByKey devuelve una plantilla por clave.
func APITokenTemplateByKey(key string) (APITokenTemplate, bool) {
	for _, t := range APITokenTemplates {
		if t.Key == key {
			return t, true
		}
	}
	return APITokenTemplate{}, false
}

// IntersectPermissions devuelve los permisos efectivos de un canje: los del
// token que el creador conserva hoy, con los alcances actuales del creador.
func IntersectPermissions(token []string, effective map[string][]string) map[string][]string {
	out := map[string][]string{}
	for _, p := range token {
		if scopes, ok := effective[p]; ok && len(scopes) > 0 {
			out[p] = slices.Clone(scopes)
		}
	}
	return out
}
