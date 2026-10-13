// Package domain contiene las reglas puras del asistente de IA (docs/assist.md):
// configuración del proveedor por ISP y su validación, features (puntos de
// entrada) y resultados auditables. Sin E/S.
package domain

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Proveedores.
const (
	// ProviderAnthropic usa la API de Anthropic (Claude).
	ProviderAnthropic = "anthropic"
	// ProviderOpenAICompatible usa un servidor con la API de chat de OpenAI
	// (Ollama, vLLM, LM Studio): un modelo local, sin que los datos salgan de la red del ISP.
	ProviderOpenAICompatible = "openai_compatible"
)

// Providers son los proveedores admitidos.
var Providers = []string{ProviderAnthropic, ProviderOpenAICompatible}

// Esfuerzos admitidos (output_config.effort en Anthropic; ignorado por los compatibles con OpenAI).
var Efforts = []string{"low", "medium", "high"}

// DefaultModels es el modelo por defecto de cada proveedor.
var DefaultModels = map[string]string{
	ProviderAnthropic:        "claude-opus-5-5",
	ProviderOpenAICompatible: "llama3.1",
}

// Features: puntos de entrada del asistente (genérico: feature + id de contexto).
const (
	FeatureFinding = "finding"
	FeatureRouter  = "router"
	FeatureMCP     = "mcp"
	FeatureTest    = "test"
)

// AnalysisFeatures son las features que admite POST /assist/analyses.
var AnalysisFeatures = []string{FeatureFinding, FeatureRouter}

// Resultados auditados.
const (
	OutcomeSuccess = "success"
	OutcomeRefused = "refused"
	OutcomeError   = "error"
	OutcomeLimited = "limited"
	OutcomeDenied  = "denied"
)

// Límites de validación.
const (
	MinDailyLimit = 1
	MaxDailyLimit = 10000
	MinAPIKeyLen  = 8
	MaxAPIKeyLen  = 512
)

// Settings es la configuración efectiva del asistente de un ISP.
type Settings struct {
	TenantID          uuid.UUID
	Enabled           bool
	Provider          string
	Model             string
	BaseURL           string
	Effort            string
	DailyRequestLimit int
	// APIKeySet indica que el ISP guardó su propia clave (cifrada).
	APIKeySet bool
	// Sealed es la clave cifrada (solo la usa la aplicación para descifrarla).
	Sealed *SealedKey
	// Stored indica que la configuración es del ISP (si no, es la de plataforma).
	Stored    bool
	Version   int
	UpdatedAt time.Time
	UpdatedBy *uuid.UUID
}

// SealedKey es una clave de API con envelope encryption.
type SealedKey struct {
	Ciphertext []byte
	DEKWrapped []byte
	KEKID      string
}

// Update es el cuerpo de PUT /assist/settings.
type Update struct {
	Enabled           bool    `json:"enabled"`
	Provider          string  `json:"provider"`
	Model             string  `json:"model"`
	BaseURL           *string `json:"base_url"`
	Effort            string  `json:"effort"`
	DailyRequestLimit int     `json:"daily_request_limit"`
	// APIKey (write-only): nil = sin cambios; "" no se admite (usar ClearAPIKey).
	APIKey      *string `json:"api_key"`
	ClearAPIKey bool    `json:"clear_api_key"`
}

// FieldError es un error de validación de un campo.
type FieldError struct {
	Field, Code, Message string
}

// ValidationError agrupa errores de campo.
type ValidationError struct{ Fields []FieldError }

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		parts[i] = f.Field + ": " + f.Code
	}
	return "assist: invalid settings (" + strings.Join(parts, ", ") + ")"
}

var modelRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,99}$`)

// Normalize recorta espacios y aplica valores por defecto (modelo y esfuerzo).
func (u *Update) Normalize() {
	u.Provider = strings.TrimSpace(u.Provider)
	u.Model = strings.TrimSpace(u.Model)
	u.Effort = strings.TrimSpace(u.Effort)
	if u.Model == "" {
		u.Model = DefaultModels[u.Provider]
	}
	if u.Effort == "" {
		u.Effort = "medium"
	}
	if u.BaseURL != nil {
		v := strings.TrimRight(strings.TrimSpace(*u.BaseURL), "/")
		u.BaseURL = &v
	}
	if u.APIKey != nil {
		v := strings.TrimSpace(*u.APIKey)
		u.APIKey = &v
	}
}

// Validate comprueba la forma de la configuración. keyAvailable indica si,
// tras aplicar el cambio, habrá una clave usable (del ISP o de plataforma) para
// el proveedor anthropic.
func (u *Update) Validate(keyAvailable bool) error {
	var fe []FieldError
	add := func(field, code, msg string) { fe = append(fe, FieldError{field, code, msg}) }
	if !slices.Contains(Providers, u.Provider) {
		add("provider", "INVALID_VALUE", "Proveedor no admitido (anthropic u openai_compatible).")
	}
	if !modelRe.MatchString(u.Model) {
		add("model", "INVALID_VALUE", "Identificador de modelo no válido.")
	}
	if !slices.Contains(Efforts, u.Effort) {
		add("effort", "INVALID_VALUE", "Esfuerzo no admitido (low, medium o high).")
	}
	if u.DailyRequestLimit < MinDailyLimit || u.DailyRequestLimit > MaxDailyLimit {
		add("daily_request_limit", "OUT_OF_RANGE", "Entre 1 y 10 000 consultas por día.")
	}
	base := ""
	if u.BaseURL != nil {
		base = *u.BaseURL
	}
	switch u.Provider {
	case ProviderOpenAICompatible:
		if base == "" {
			add("base_url", "REQUIRED", "Indica la URL del servidor (p. ej. http://10.0.0.5:11434).")
		} else if err := ValidateBaseURL(base); err != nil {
			add("base_url", "INVALID_VALUE", err.Error())
		}
	case ProviderAnthropic:
		if base != "" {
			add("base_url", "NOT_APPLICABLE", "Solo para servidores compatibles con OpenAI.")
		}
	}
	if u.APIKey != nil {
		if n := len(*u.APIKey); n < MinAPIKeyLen || n > MaxAPIKeyLen {
			add("api_key", "INVALID_VALUE", "Entre 8 y 512 caracteres.")
		}
		if u.ClearAPIKey {
			add("clear_api_key", "CONFLICT", "No envíes api_key y clear_api_key a la vez.")
		}
	}
	if u.Enabled && u.Provider == ProviderAnthropic && !keyAvailable {
		add("api_key", "REQUIRED", "Para activar Claude hace falta una clave de API (del ISP o de plataforma).")
	}
	if len(fe) > 0 {
		return &ValidationError{Fields: fe}
	}
	return nil
}

// MsgError es un error con un mensaje para el usuario (español, frase completa).
type MsgError string

func (e MsgError) Error() string { return string(e) }

// ValidateBaseURL comprueba una URL de servidor compatible con OpenAI: http o
// https, con host, sin credenciales, consulta ni fragmento.
func ValidateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return MsgError("URL no válida")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return MsgError("Solo http o https")
	}
	if u.User != nil {
		return MsgError("Sin usuario ni contraseña en la URL: usa el campo de clave de API")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return MsgError("Sin parámetros ni fragmento")
	}
	return nil
}

// StartOfDay es el inicio del día UTC de t (ventana del límite diario).
func StartOfDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// ToolUse cuenta las llamadas a una herramienta durante un análisis (auditoría).
type ToolUse struct {
	Name   string `json:"name"`
	Count  int    `json:"count"`
	Errors int    `json:"errors,omitempty"`
}

// AuditEntry es una fila de assist.audit.
type AuditEntry struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	UserID       *uuid.UUID
	Feature      string
	ContextID    string
	Provider     string
	Model        string
	InputTokens  int64
	OutputTokens int64
	ToolCalls    []ToolUse
	DurationMS   int
	Outcome      string
	ErrorCode    string
	CreatedAt    time.Time
}

// MCPToken es un token del servidor MCP (sin el valor: solo su hash).
type MCPToken struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	UserID     uuid.UUID
	Name       string
	Hash       []byte
	Prefix     string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// Active indica si el token sirve en now.
func (t *MCPToken) Active(now time.Time) bool {
	return t.RevokedAt == nil && now.Before(t.ExpiresAt)
}
