// Package problem escribe errores HTTP RFC 9457 (`application/problem+json`)
// con el formato del contrato v0 (packages/schemas/openapi/v0/common.yaml
// `Problem`, docs/api.md §1.4). El campo `code` es el contrato; `title` y
// `detail` son texto humano en español y pueden cambiar.
//
// Lo usan los módulos y el gateway para producir el mismo formato.
package problem

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

// ContentType es el tipo MIME de los errores.
const ContentType = "application/problem+json"

// typeBase es el prefijo de `type` (URI del catálogo de errores).
const typeBase = "https://docs.horus-flow.local/errors/"

// Códigos transversales del contrato (ErrorCode, enum abierto).
const (
	CodeValidationFailed      = "VALIDATION_FAILED"
	CodeUnauthenticated       = "UNAUTHENTICATED"
	CodeTokenExpired          = "TOKEN_EXPIRED"
	CodeSessionRevoked        = "SESSION_REVOKED"
	CodePermissionDenied      = "PERMISSION_DENIED"
	CodeOriginNotAllowed      = "ORIGIN_NOT_ALLOWED"
	CodeMFARequired           = "MFA_REQUIRED"
	CodeMFAEnrollmentRequired = "MFA_ENROLLMENT_REQUIRED"
	CodeReauthRequired        = "REAUTH_REQUIRED"
	CodeNotFound              = "NOT_FOUND"
	CodeAlreadyExists         = "ALREADY_EXISTS"
	CodeConflict              = "CONFLICT"
	CodePreconditionFailed    = "PRECONDITION_FAILED"
	CodePreconditionRequired  = "PRECONDITION_REQUIRED"
	CodeRateLimited           = "RATE_LIMITED"
	CodeInternal              = "INTERNAL"
	CodeServiceUnavailable    = "SERVICE_UNAVAILABLE"
	CodeInvalidCredentials    = "INVALID_CREDENTIALS"
	CodeInvalidCursor         = "INVALID_CURSOR"
	CodeInvalidFilter         = "INVALID_FILTER"
	CodeInvalidSortField      = "INVALID_SORT_FIELD"
	CodeTenantNotFound        = "TENANT_NOT_FOUND"
	CodeTenantMismatch        = "TENANT_MISMATCH"
	CodeTenantSuspended       = "TENANT_SUSPENDED"
	CodeTokenScopeInvalid     = "TOKEN_SCOPE_INVALID"
	CodeKioskForbidden        = "KIOSK_FORBIDDEN"
	CodeMethodNotAllowed      = "METHOD_NOT_ALLOWED"
	CodePayloadTooLarge       = "PAYLOAD_TOO_LARGE"
	CodeUnsupportedMediaType  = "UNSUPPORTED_MEDIA_TYPE"
)

// FieldError es un error de validación de un campo (`errors[]`).
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Problem es el cuerpo RFC 9457.
type Problem struct {
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	Status    int            `json:"status"`
	Detail    string         `json:"detail,omitempty"`
	Instance  string         `json:"instance,omitempty"`
	Code      string         `json:"code"`
	TraceID   string         `json:"trace_id,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
	Errors    []FieldError   `json:"errors,omitempty"`
	Current   map[string]any `json:"current,omitempty"`
}

// Option ajusta un Problem antes de escribirlo.
type Option func(*Problem)

// WithDetail fija `detail`.
func WithDetail(d string) Option { return func(p *Problem) { p.Detail = d } }

// WithErrors añade errores de campo.
func WithErrors(errs ...FieldError) Option {
	return func(p *Problem) { p.Errors = append(p.Errors, errs...) }
}

// WithCurrent añade la representación actual del recurso (412).
func WithCurrent(v any) Option {
	return func(p *Problem) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		m := map[string]any{}
		if json.Unmarshal(b, &m) == nil {
			p.Current = m
		}
	}
}

// New construye un Problem.
func New(status int, code, title string, opts ...Option) *Problem {
	p := &Problem{
		Type:   typeBase + strings.ReplaceAll(strings.ToLower(code), "_", "-"),
		Title:  title,
		Status: status,
		Code:   code,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Write escribe p como respuesta, completando instance y request_id desde r.
func (p *Problem) Write(w http.ResponseWriter, r *http.Request) {
	if r != nil {
		if p.Instance == "" {
			p.Instance = r.URL.Path
		}
		if p.RequestID == "" {
			p.RequestID = observability.RequestIDFrom(r.Context())
		}
		if p.TraceID == "" {
			p.TraceID, _ = observability.TraceFrom(r.Context())
		}
	}
	h := w.Header()
	h.Set("Content-Type", ContentType)
	h.Set("Cache-Control", "no-store")
	if p.Status == http.StatusUnauthorized && h.Get("WWW-Authenticate") == "" {
		h.Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	}
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p) // el cliente puede haber cerrado
}

// Write es atajo de New(...).Write.
func Write(w http.ResponseWriter, r *http.Request, status int, code, title string, opts ...Option) {
	New(status, code, title, opts...).Write(w, r)
}

// Títulos por defecto de los códigos transversales.
var defaultTitles = map[string]string{
	CodeValidationFailed:      "La solicitud contiene campos inválidos",
	CodeUnauthenticated:       "Se requiere autenticación",
	CodeTokenExpired:          "El token ha caducado",
	CodeSessionRevoked:        "La sesión fue revocada",
	CodePermissionDenied:      "No tiene permiso para esta operación",
	CodeOriginNotAllowed:      "Origen no permitido",
	CodeMFARequired:           "Se requiere el segundo factor",
	CodeMFAEnrollmentRequired: "Debe activar el segundo factor (TOTP)",
	CodeReauthRequired:        "Se requiere re-autenticación reciente",
	CodeNotFound:              "No encontrado",
	CodeAlreadyExists:         "Ya existe",
	CodeConflict:              "Conflicto de estado",
	CodePreconditionFailed:    "La versión del recurso no coincide",
	CodePreconditionRequired:  "Falta una cabecera de precondición obligatoria",
	CodeRateLimited:           "Demasiadas solicitudes",
	CodeInternal:              "Error interno",
	CodeServiceUnavailable:    "Servicio no disponible",
	CodeInvalidCredentials:    "Credenciales inválidas",
	CodeInvalidCursor:         "Cursor inválido",
	CodeInvalidFilter:         "Filtro no permitido",
	CodeInvalidSortField:      "Orden no permitido",
	CodeTenantNotFound:        "ISP no encontrado",
	CodeTenantMismatch:        "El ISP del cuerpo no coincide con el del token",
	CodeTenantSuspended:       "El ISP está suspendido",
	CodeTokenScopeInvalid:     "El ámbito del token no es válido para esta ruta",
	CodeKioskForbidden:        "Operación no permitida para un kiosco",
	CodeMethodNotAllowed:      "Método no permitido",
	CodePayloadTooLarge:       "Cuerpo demasiado grande",
	CodeUnsupportedMediaType:  "Tipo de contenido no soportado",
}

// Title devuelve el título por defecto de code (o code si no tiene).
func Title(code string) string {
	if t, ok := defaultTitles[code]; ok {
		return t
	}
	return code
}

// Std escribe un problema con el título por defecto de code.
func Std(w http.ResponseWriter, r *http.Request, status int, code string, opts ...Option) {
	Write(w, r, status, code, Title(code), opts...)
}
