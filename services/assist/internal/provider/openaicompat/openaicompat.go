// Package openaicompat es el adaptador del asistente para servidores con la API
// de chat de OpenAI (POST {base_url}/v1/chat/completions con function calling):
// Ollama, vLLM, LM Studio… Pensado para un modelo local en la red del ISP, de
// modo que ningún dato sale de ella (docs/assist.md §Proveedores). No es para
// Claude: Claude usa el adaptador anthropic.
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
	"time"

	"github.com/hcdestroyer/horus-flow/services/assist/internal/provider"
)

// DefaultModel es el modelo por defecto (cualquiera con soporte de herramientas).
const DefaultModel = "llama3.1"

// maxResponse acota el cuerpo de una respuesta (protege la memoria).
const maxResponse = 4 << 20

// Options del adaptador.
type Options struct {
	BaseURL string
	// APIKey opcional (Authorization: Bearer).
	APIKey string
	Model  string
	// HTTPClient opcional (tests). Si es nil se crea uno con Dialer protegido.
	HTTPClient *http.Client
	// Timeout por petición HTTP (por defecto 120 s: un modelo local es lento).
	Timeout time.Duration
	// Deny son direcciones a las que nunca se conecta (las de la instalación).
	Deny []netip.Prefix
	// AllowLoopback permite 127.0.0.0/8 y ::1.
	AllowLoopback bool
}

// Provider habla con el servidor compatible.
type Provider struct {
	o      Options
	client *http.Client
}

// New crea el adaptador.
func New(o Options) *Provider {
	if o.Model == "" {
		o.Model = DefaultModel
	}
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	if o.Timeout <= 0 {
		o.Timeout = 120 * time.Second
	}
	c := o.HTTPClient
	if c == nil {
		d := &net.Dialer{Timeout: 10 * time.Second, Control: dialGuard(o.Deny, o.AllowLoopback)}
		c = &http.Client{
			Timeout:       o.Timeout,
			Transport:     &http.Transport{DialContext: d.DialContext, Proxy: nil, MaxIdleConns: 4, IdleConnTimeout: 60 * time.Second},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return &Provider{o: o, client: c}
}

// ErrDenied indica un destino prohibido (enlace local, metadatos de la nube,
// direcciones de la propia instalación o loopback sin permiso).
var ErrDenied = errors.New("openaicompat: destination not allowed")

// dialGuard impide conectar a direcciones de enlace local (incluida la de
// metadatos 169.254.169.254), multicast, sin especificar, loopback (salvo
// permiso) y las de la propia instalación. Las privadas se permiten: el
// servidor del modelo suele estar en la red del ISP.
func dialGuard(deny []netip.Prefix, allowLoopback bool) func(string, string, syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrDenied, address)
		}
		a := ap.Addr().Unmap()
		if a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsMulticast() || a.IsUnspecified() ||
			a.IsInterfaceLocalMulticast() || (a.IsLoopback() && !allowLoopback) {
			return fmt.Errorf("%w: %s", ErrDenied, a)
		}
		for _, d := range deny {
			if d.Contains(a) && (!a.IsLoopback() || !allowLoopback) {
				return fmt.Errorf("%w: %s", ErrDenied, a)
			}
		}
		return nil
	}
}

// Name implementa provider.Provider.
func (p *Provider) Name() string { return "openai_compatible" }

// Model implementa provider.Provider.
func (p *Provider) Model() string { return p.o.Model }

type message struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type toolDef struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type chatRequest struct {
	Model     string    `json:"model"`
	Messages  []message `json:"messages"`
	Tools     []toolDef `json:"tools,omitempty"`
	MaxTokens int64     `json:"max_tokens,omitempty"`
	Stream    bool      `json:"stream"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

func str(s string) *string { return &s }

// Start implementa provider.Provider.
func (p *Provider) Start(req provider.Request) provider.Conversation {
	tools := make([]toolDef, 0, len(req.Tools))
	for _, t := range req.Tools {
		var d toolDef
		d.Type = "function"
		d.Function.Name, d.Function.Description, d.Function.Parameters = t.Name, t.Description, t.Schema
		tools = append(tools, d)
	}
	return &conversation{p: p, req: chatRequest{
		Model: p.o.Model, Tools: tools, MaxTokens: req.MaxTokens,
		Messages: []message{{Role: "system", Content: str(req.System)}, {Role: "user", Content: str(req.User)}},
	}}
}

type conversation struct {
	p   *Provider
	req chatRequest
	// seq numera las llamadas sin id (algunos servidores no lo envían).
	seq int
}

// Next implementa provider.Conversation.
func (c *conversation) Next(ctx context.Context) (provider.Turn, error) {
	var resp chatResponse
	if err := c.p.post(ctx, c.req, &resp); err != nil {
		return provider.Turn{}, err
	}
	if len(resp.Choices) == 0 {
		return provider.Turn{}, fmt.Errorf("%w: no choices", provider.ErrUnavailable)
	}
	ch := resp.Choices[0]
	msg := ch.Message
	turn := provider.Turn{
		Model: resp.Model,
		Usage: provider.Usage{InputTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.CompletionTokens},
	}
	if msg.Content != nil {
		turn.Text = *msg.Content
	}
	for i := range msg.ToolCalls {
		tc := &msg.ToolCalls[i]
		if tc.ID == "" {
			c.seq++
			tc.ID = fmt.Sprintf("call_%d", c.seq)
		}
		if tc.Type == "" {
			tc.Type = "function"
		}
		args := strings.TrimSpace(tc.Function.Arguments)
		if args == "" {
			args = "{}"
		}
		turn.ToolCalls = append(turn.ToolCalls, provider.ToolCall{ID: tc.ID, Name: tc.Function.Name, Input: json.RawMessage(args)})
	}
	msg.Role = "assistant"
	c.req.Messages = append(c.req.Messages, msg)
	switch {
	case len(turn.ToolCalls) > 0:
		turn.StopReason = provider.StopToolUse
	case ch.FinishReason == "length":
		turn.StopReason = provider.StopMaxTokens
	case ch.FinishReason == "content_filter":
		turn.StopReason = provider.StopRefusal
		turn.RefusalCategory = "content_filter"
	default:
		turn.StopReason = provider.StopEnd
	}
	return turn, nil
}

// AddToolResults implementa provider.Conversation (un mensaje `tool` por
// llamada, todos juntos tras el turno del asistente, como exige la API).
func (c *conversation) AddToolResults(results []provider.ToolResult) {
	for _, r := range results {
		content := r.Content
		if r.IsError {
			content = "ERROR: " + content
		}
		c.req.Messages = append(c.req.Messages, message{Role: "tool", ToolCallID: r.CallID, Content: str(content)})
	}
}

// Ping implementa provider.Provider: una petición mínima sin herramientas.
func (p *Provider) Ping(ctx context.Context) error {
	var resp chatResponse
	return p.post(ctx, chatRequest{Model: p.o.Model, MaxTokens: 16,
		Messages: []message{{Role: "user", Content: str("Responde solo: ok")}}}, &resp)
}

func (p *Provider) post(ctx context.Context, body chatRequest, out *chatResponse) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("%w: %v", provider.ErrBadRequest, err) //nolint:errorlint // detalle interno
	}
	ctx, cancel := context.WithTimeout(ctx, p.o.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.o.BaseURL+"/v1/chat/completions", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("%w: %v", provider.ErrBadRequest, err) //nolint:errorlint // detalle interno
	}
	req.Header.Set("Content-Type", "application/json")
	if p.o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.o.APIKey)
	}
	res, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("%w: %v", provider.ErrUnavailable, err) //nolint:errorlint // se oculta el transporte
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponse))
	if err != nil {
		return fmt.Errorf("%w: read: %v", provider.ErrUnavailable, err) //nolint:errorlint // detalle interno
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: status %d", provider.ErrUnauthorized, res.StatusCode)
	case res.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: status %d", provider.ErrNotFound, res.StatusCode)
	case res.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: status %d", provider.ErrRateLimited, res.StatusCode)
	case res.StatusCode >= 500:
		return fmt.Errorf("%w: status %d", provider.ErrUnavailable, res.StatusCode)
	case res.StatusCode >= 300:
		return fmt.Errorf("%w: status %d", provider.ErrBadRequest, res.StatusCode)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%w: invalid JSON", provider.ErrUnavailable)
	}
	return nil
}
