package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Enlace de conexión con ArgosISP (contrato docs/integrations/argosisp.md §1.4).
const (
	// ConnectionLinkTTL es la vida del código (un solo uso).
	ConnectionLinkTTL = 15 * time.Minute
	connectionCodeLen = 32
	// ConnectionCodeChars es la longitud del código en base64url.
	ConnectionCodeChars = 43
	// ConnectPath es la página del enlace (`https://<A>/conectar#<código>`).
	ConnectPath = "/conectar"
)

// ConnectionLink es un enlace de conexión generado en Horus. Solo se guarda
// el SHA-256 del código; tras el canje, la respuesta cifrada sirve para
// contestar igual a la misma petición repetida (idempotencia) hasta que caduca.
type ConnectionLink struct {
	ID                 uuid.UUID
	TenantID           uuid.UUID
	CodeHash           []byte
	CreatedBy          uuid.UUID
	CreatedAt          time.Time
	ExpiresAt          time.Time
	UsedAt             *time.Time
	RequestHash        []byte
	ResponseCiphertext []byte
	ResponseDEK        []byte
}

// GenerateConnectionCode devuelve un código nuevo (32 bytes aleatorios en
// base64url, 43 caracteres).
func GenerateConnectionCode() (string, error) {
	b := make([]byte, connectionCodeLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// LooksLikeConnectionCode comprueba la forma del código sin tocar la base.
func LooksLikeConnectionCode(code string) bool {
	if len(code) != ConnectionCodeChars {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(code)
	return err == nil && len(b) == connectionCodeLen
}

// HashConnectionCode es el SHA-256 del código (lo único que se guarda).
func HashConnectionCode(code string) []byte {
	s := sha256.Sum256([]byte("argos_connection_link:" + code))
	return s[:]
}

// ParsedLink es un enlace de conexión generado en ArgosISP.
type ParsedLink struct {
	Code string
	// APIOrigin es el origen de la API de ArgosISP (sin ruta): el del
	// enlace o, si el panel y la API están en orígenes distintos, el del
	// parámetro `api` (contrato 1.5.0).
	APIOrigin string
}

// ParseConnectionLink valida un enlace `https://<panel>/conectar[?api=<origen>]#<código>`
// (http solo con allowHTTP, laboratorios). Rechaza credenciales en la URL,
// rutas distintas de /conectar y orígenes con ruta, consulta o fragmento.
func ParseConnectionLink(raw string, allowHTTP bool) (ParsedLink, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 4096 {
		return ParsedLink{}, false
	}
	u, err := url.Parse(raw)
	if err != nil || !schemeOK(u.Scheme, allowHTTP) || u.Host == "" || u.User != nil {
		return ParsedLink{}, false
	}
	if strings.TrimRight(u.Path, "/") != ConnectPath || !LooksLikeConnectionCode(u.Fragment) {
		return ParsedLink{}, false
	}
	q := u.Query()
	for k := range q {
		if k != "api" {
			return ParsedLink{}, false
		}
	}
	origin := u.Scheme + "://" + u.Host
	if api := q.Get("api"); api != "" {
		o, ok := ParseOrigin(api, allowHTTP)
		if !ok {
			return ParsedLink{}, false
		}
		origin = o
	}
	return ParsedLink{Code: u.Fragment, APIOrigin: origin}, true
}

func schemeOK(scheme string, allowHTTP bool) bool {
	return scheme == "https" || (allowHTTP && scheme == "http")
}

// ParseOrigin valida un origen `https://host[:puerto]` (admite una barra o
// `/api/v1` al final, que se quitan). http solo con allowHTTP.
func ParseOrigin(raw string, allowHTTP bool) (string, bool) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 2048 {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || !schemeOK(u.Scheme, allowHTTP) || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	p := strings.TrimRight(u.Path, "/")
	if p != "" && p != "/api/v1" {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

// ValidSSOEntryURL comprueba la URL de autologin que devuelve ArgosISP
// (`https://<panel>/sso/entrar#<ticket>`): la abre el navegador del usuario,
// así que solo se acepta https (http en laboratorio), sin credenciales, con
// esa ruta y el ticket en el fragmento (nunca en la consulta).
func ValidSSOEntryURL(raw string, allowHTTP bool) bool {
	if len(raw) > 4096 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || !schemeOK(u.Scheme, allowHTTP) || u.Host == "" || u.User != nil || u.RawQuery != "" {
		return false
	}
	return strings.TrimRight(u.Path, "/") == "/sso/entrar" && len(u.Fragment) >= 16 && len(u.Fragment) <= 512
}
