// Package anthropic es el adaptador del asistente para la API de Anthropic
// (Claude) con el SDK oficial de Go (docs/assist.md §Proveedores).
//
//   - Prompt de sistema estable con cache_control: las herramientas y el
//     sistema forman el prefijo cacheado (no lleva fechas ni datos variables).
//   - En claude-opus-5-5 el razonamiento no se puede desactivar: no se envía
//     `thinking` (adaptativo por defecto) y la profundidad se controla con
//     output_config.effort (low | medium | high).
//   - tool_choice auto (forzar herramienta devuelve 400 en este modelo); sin
//     prefill del asistente.
//   - Respaldo del lado del servidor ante rechazos de los clasificadores
//     (`fallbacks: "default"`, beta server-side-fallback-2026-07-01).
//   - El historial se reenvía tal cual (resp.ToParam()), con los bloques de
//     razonamiento intactos.
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/hcdestroyer/horus-flow/services/assist/internal/provider"
)

// DefaultModel es el modelo por defecto.
const DefaultModel = "claude-opus-5-5"

// Options del adaptador.
type Options struct {
	APIKey string
	Model  string
	// Effort: low | medium | high (vacío = el del modelo).
	Effort string
	// BaseURL sustituye la de la API (solo tests).
	BaseURL string
	// HTTPClient opcional (tests, proxy).
	HTTPClient *http.Client
	// Fallbacks activa el respaldo del lado del servidor ante rechazos (por defecto sí).
	DisableFallbacks bool
	// Timeout por petición HTTP (0 = el del contexto).
	Timeout time.Duration
}

// Provider habla con la API de Anthropic.
type Provider struct {
	client anthropic.Client
	o      Options
}

// New crea el adaptador.
func New(o Options) *Provider {
	if o.Model == "" {
		o.Model = DefaultModel
	}
	opts := []option.RequestOption{option.WithAPIKey(o.APIKey), option.WithMaxRetries(2)}
	if o.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(o.BaseURL))
	}
	if o.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(o.HTTPClient))
	}
	if o.Timeout > 0 {
		opts = append(opts, option.WithRequestTimeout(o.Timeout))
	}
	return &Provider{client: anthropic.NewClient(opts...), o: o}
}

// Name implementa provider.Provider.
func (p *Provider) Name() string { return "anthropic" }

// Model implementa provider.Provider.
func (p *Provider) Model() string { return p.o.Model }

// Start implementa provider.Provider.
func (p *Provider) Start(req provider.Request) provider.Conversation {
	tools := make([]anthropic.BetaToolUnionParam, 0, len(req.Tools))
	for _, t := range req.Tools {
		props, _ := t.Schema["properties"].(map[string]any)
		schema := anthropic.BetaToolInputSchemaParam{Properties: props}
		if r, ok := t.Schema["required"].([]string); ok {
			schema.Required = r
		}
		tools = append(tools, anthropic.BetaToolUnionParam{OfTool: &anthropic.BetaToolParam{
			Name: t.Name, Description: anthropic.String(t.Description), InputSchema: schema,
		}})
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 16000
	}
	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(p.o.Model),
		MaxTokens: maxTokens,
		System: []anthropic.BetaTextBlockParam{{
			Text:         req.System,
			CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
		}},
		Tools:      tools,
		ToolChoice: anthropic.BetaToolChoiceUnionParam{OfAuto: &anthropic.BetaToolChoiceAutoParam{}},
		Messages:   []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(req.User))},
	}
	if p.o.Effort != "" {
		params.OutputConfig = anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffort(p.o.Effort)}
	}
	if !p.o.DisableFallbacks {
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
		params.Betas = append(params.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
	}
	return &conversation{p: p, params: params}
}

type conversation struct {
	p      *Provider
	params anthropic.BetaMessageNewParams
}

// Next implementa provider.Conversation.
func (c *conversation) Next(ctx context.Context) (provider.Turn, error) {
	resp, err := c.p.client.Beta.Messages.New(ctx, c.params)
	if err != nil {
		return provider.Turn{}, mapError(err)
	}
	turn := provider.Turn{
		Model: string(resp.Model),
		Usage: provider.Usage{
			InputTokens:  resp.Usage.InputTokens + resp.Usage.CacheCreationInputTokens + resp.Usage.CacheReadInputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		},
	}
	// El rechazo se comprueba antes de leer el contenido.
	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		turn.StopReason = provider.StopRefusal
		turn.RefusalCategory = string(resp.StopDetails.Category)
		return turn, nil
	}
	// El turno del asistente se añade completo (razonamiento incluido) antes de
	// procesar las herramientas.
	c.params.Messages = append(c.params.Messages, resp.ToParam())
	var text strings.Builder
	for _, block := range resp.Content {
		switch v := block.AsAny().(type) {
		case anthropic.BetaTextBlock:
			text.WriteString(v.Text)
		case anthropic.BetaToolUseBlock:
			turn.ToolCalls = append(turn.ToolCalls, provider.ToolCall{ID: v.ID, Name: v.Name, Input: []byte(v.JSON.Input.Raw())})
		}
	}
	turn.Text = text.String()
	switch resp.StopReason {
	case anthropic.BetaStopReasonToolUse:
		turn.StopReason = provider.StopToolUse
	case anthropic.BetaStopReasonMaxTokens:
		turn.StopReason = provider.StopMaxTokens
	default:
		turn.StopReason = provider.StopEnd
	}
	return turn, nil
}

// AddToolResults implementa provider.Conversation: todos los tool_result en un
// único mensaje de usuario.
func (c *conversation) AddToolResults(results []provider.ToolResult) {
	blocks := make([]anthropic.BetaContentBlockParamUnion, 0, len(results))
	for _, r := range results {
		blocks = append(blocks, anthropic.NewBetaToolResultBlock(r.CallID, r.Content, r.IsError))
	}
	c.params.Messages = append(c.params.Messages, anthropic.NewBetaUserMessage(blocks...))
}

// AddUserMessage implementa provider.Conversation.
func (c *conversation) AddUserMessage(text string) {
	c.params.Messages = append(c.params.Messages, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(text)))
}

// Ping implementa provider.Provider: una petición mínima (sin herramientas).
func (p *Provider) Ping(ctx context.Context) error {
	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(p.o.Model),
		MaxTokens: 1024,
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("Responde solo: ok"))},
	}
	if p.o.Effort != "" {
		params.OutputConfig = anthropic.BetaOutputConfigParam{Effort: "low"}
	}
	if _, err := p.client.Beta.Messages.New(ctx, params); err != nil {
		return mapError(err)
	}
	return nil
}

// mapError traduce los errores del SDK (que ya reintenta 429 y 5xx dos veces).
func mapError(err error) error {
	var apierr *anthropic.Error
	if errors.As(err, &apierr) {
		switch {
		case apierr.StatusCode == http.StatusUnauthorized || apierr.StatusCode == http.StatusForbidden:
			return fmt.Errorf("%w: status %d", provider.ErrUnauthorized, apierr.StatusCode)
		case apierr.StatusCode == http.StatusNotFound:
			return fmt.Errorf("%w: status %d", provider.ErrNotFound, apierr.StatusCode)
		case apierr.StatusCode == http.StatusTooManyRequests:
			return fmt.Errorf("%w: status %d", provider.ErrRateLimited, apierr.StatusCode)
		case apierr.StatusCode >= 500:
			return fmt.Errorf("%w: status %d", provider.ErrUnavailable, apierr.StatusCode)
		default:
			return fmt.Errorf("%w: status %d", provider.ErrBadRequest, apierr.StatusCode)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	return fmt.Errorf("%w: %v", provider.ErrUnavailable, err) //nolint:errorlint // se oculta el error de transporte
}
