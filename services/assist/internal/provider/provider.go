// Package provider define la interfaz común de los proveedores de IA del
// asistente (docs/assist.md §Proveedores): Anthropic (Claude) y servidores
// compatibles con la API de chat de OpenAI (Ollama, vLLM, LM Studio).
//
// Cada adaptador guarda el historial en su formato nativo (así se devuelven
// sin tocar los bloques que el proveedor exige reenviar, p. ej. el
// razonamiento de Claude) y expone turnos neutrales al bucle del agente.
package provider

import (
	"context"
	"encoding/json"
	"errors"
)

// ToolSpec es una herramienta que el modelo puede pedir.
type ToolSpec struct {
	Name        string
	Description string
	Schema      map[string]any
}

// Request inicia una conversación.
type Request struct {
	// System es el prompt de sistema (estable: se cachea).
	System string
	Tools  []ToolSpec
	// User es el primer mensaje del usuario.
	User string
	// MaxTokens de salida por turno.
	MaxTokens int64
}

// ToolCall es una llamada a herramienta pedida por el modelo.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResult es el resultado de una llamada (Content es texto/JSON).
type ToolResult struct {
	CallID  string
	Content string
	IsError bool
}

// StopReason neutral de un turno.
type StopReason string

// Motivos de parada.
const (
	StopEnd       StopReason = "end"
	StopToolUse   StopReason = "tool_use"
	StopRefusal   StopReason = "refusal"
	StopMaxTokens StopReason = "max_tokens"
)

// Usage son los tokens de un turno.
type Usage struct {
	InputTokens  int64
	OutputTokens int64
}

// Add suma u2.
func (u *Usage) Add(u2 Usage) {
	u.InputTokens += u2.InputTokens
	u.OutputTokens += u2.OutputTokens
}

// Turn es la respuesta del modelo a un turno.
type Turn struct {
	Text       string
	ToolCalls  []ToolCall
	StopReason StopReason
	Usage      Usage
	// RefusalCategory es la categoría de un rechazo (informativa).
	RefusalCategory string
	// Model es el modelo que respondió (puede diferir del pedido si hubo
	// respaldo del lado del proveedor).
	Model string
}

// Conversation es una conversación en curso.
type Conversation interface {
	// Next envía el historial y devuelve la respuesta del modelo.
	Next(ctx context.Context) (Turn, error)
	// AddToolResults añade los resultados de TODAS las llamadas del último
	// turno (en un único mensaje).
	AddToolResults(results []ToolResult)
	// AddUserMessage añade un mensaje de usuario (turno de corrección tras la
	// verificación de la respuesta).
	AddUserMessage(text string)
}

// Provider crea conversaciones con un modelo concreto.
type Provider interface {
	Name() string
	Model() string
	Start(req Request) Conversation
	// Ping comprueba conectividad y credenciales con una petición mínima.
	Ping(ctx context.Context) error
}

// Errores neutrales de los adaptadores (se auditan por código).
var (
	ErrUnauthorized = errors.New("provider: unauthorized")
	ErrRateLimited  = errors.New("provider: rate limited")
	ErrUnavailable  = errors.New("provider: unavailable")
	ErrBadRequest   = errors.New("provider: bad request")
	ErrNotFound     = errors.New("provider: model not found")
)

// Code devuelve el código de error auditable de err.
func Code(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrUnauthorized):
		return "PROVIDER_UNAUTHORIZED"
	case errors.Is(err, ErrRateLimited):
		return "PROVIDER_RATE_LIMITED"
	case errors.Is(err, ErrNotFound):
		return "PROVIDER_MODEL_NOT_FOUND"
	case errors.Is(err, ErrBadRequest):
		return "PROVIDER_BAD_REQUEST"
	case errors.Is(err, context.DeadlineExceeded):
		return "PROVIDER_TIMEOUT"
	default:
		return "PROVIDER_UNAVAILABLE"
	}
}
