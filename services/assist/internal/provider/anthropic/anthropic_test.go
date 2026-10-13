package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hcdestroyer/horus-flow/services/assist/internal/agent"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/provider"
)

// Servidor de prueba con la forma de la API de mensajes (no se llama a la API real).
type fakeAPI struct {
	mu      sync.Mutex
	bodies  []map[string]any
	headers []http.Header
	replies []string
	status  int
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(b, &body)
	f.mu.Lock()
	f.bodies = append(f.bodies, body)
	f.headers = append(f.headers, r.Header.Clone())
	i := len(f.bodies) - 1
	status := f.status
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if status != 0 {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
		return
	}
	_, _ = io.WriteString(w, f.replies[min(i, len(f.replies)-1)])
}

func newTest(t *testing.T, f *fakeAPI, effort string) *Provider {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(ts.Close)
	return New(Options{APIKey: "sk-ant-test", Model: "claude-opus-5-5", Effort: effort, BaseURL: ts.URL, HTTPClient: ts.Client()})
}

const toolUseReply = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"tool_use",
 "content":[{"type":"thinking","thinking":"","signature":"sig1"},{"type":"text","text":"Reviso los datos."},
  {"type":"tool_use","id":"tu_1","name":"get_finding","input":{"finding_id":"f1"}},
  {"type":"tool_use","id":"tu_2","name":"get_router","input":{"router_id":"r1"}}],
 "usage":{"input_tokens":1000,"output_tokens":200,"cache_creation_input_tokens":3000,"cache_read_input_tokens":0}}`

const finalReply = `{"id":"msg_2","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"end_turn",
 "content":[{"type":"text","text":"## Qué está pasando\nSeñales compatibles con un bot."}],
 "usage":{"input_tokens":50,"output_tokens":400,"cache_creation_input_tokens":0,"cache_read_input_tokens":3000}}`

func TestRequestShapeAndToolLoop(t *testing.T) {
	t.Parallel()
	f := &fakeAPI{replies: []string{toolUseReply, finalReply}}
	p := newTest(t, f, "high")
	conv := p.Start(provider.Request{System: "sistema estable", User: "analiza", MaxTokens: 8000, Tools: []provider.ToolSpec{{
		Name: "get_finding", Description: "detalle", Schema: map[string]any{"type": "object", "required": []string{"finding_id"},
			"properties": map[string]any{"finding_id": map[string]any{"type": "string"}}},
	}}})
	res, err := agent.Run(context.Background(), conv, func(_ context.Context, c provider.ToolCall) (string, error) {
		if c.Name == "get_router" {
			return "", errors.New("sin permiso")
		}
		var in map[string]string
		if err := json.Unmarshal(c.Input, &in); err != nil || in["finding_id"] != "f1" {
			t.Errorf("input = %s", c.Input)
		}
		return `{"kind":"c2_contact"}`, nil
	}, agent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Markdown, "Señales compatibles") || res.Usage.InputTokens != 7050 || res.Usage.OutputTokens != 600 {
		t.Fatalf("res = %+v", res)
	}
	first := f.bodies[0]
	if first["model"] != "claude-opus-5-5" || first["max_tokens"].(float64) != 8000 {
		t.Fatalf("cuerpo = %v", first)
	}
	if _, ok := first["thinking"]; ok {
		t.Error("no se envía thinking (en este modelo no se puede desactivar)")
	}
	if first["fallbacks"] != "default" {
		t.Errorf("fallbacks = %v", first["fallbacks"])
	}
	if !strings.Contains(f.headers[0].Get("Anthropic-Beta"), "server-side-fallback-2026-07-01") {
		t.Errorf("beta = %q", f.headers[0].Get("Anthropic-Beta"))
	}
	if oc, _ := first["output_config"].(map[string]any); oc["effort"] != "high" {
		t.Errorf("output_config = %v", first["output_config"])
	}
	if tc, _ := first["tool_choice"].(map[string]any); tc["type"] != "auto" {
		t.Errorf("tool_choice = %v", first["tool_choice"])
	}
	sys := first["system"].([]any)[0].(map[string]any)
	if sys["text"] != "sistema estable" || sys["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Errorf("system = %v", sys)
	}
	// Segunda petición: el turno del asistente se reenvía completo (razonamiento incluido) y los dos
	// tool_result van en UN mensaje de usuario, el fallido con is_error.
	msgs := f.bodies[1]["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("mensajes = %d", len(msgs))
	}
	asst := msgs[1].(map[string]any)["content"].([]any)
	if asst[0].(map[string]any)["type"] != "thinking" || asst[0].(map[string]any)["signature"] != "sig1" {
		t.Errorf("razonamiento no reenviado: %v", asst[0])
	}
	results := msgs[2].(map[string]any)["content"].([]any)
	if len(results) != 2 {
		t.Fatalf("tool_result = %v", results)
	}
	r2 := results[1].(map[string]any)
	if r2["tool_use_id"] != "tu_2" || r2["is_error"] != true {
		t.Errorf("tool_result con error = %v", r2)
	}
	if f.headers[0].Get("X-Api-Key") != "sk-ant-test" {
		t.Error("falta x-api-key")
	}
}

func TestRefusalChecked(t *testing.T) {
	t.Parallel()
	f := &fakeAPI{replies: []string{`{"id":"m","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"refusal",
		"stop_details":{"type":"refusal","category":"cyber","explanation":"x"},"content":[],"usage":{"input_tokens":10,"output_tokens":0}}`}}
	turn, err := newTest(t, f, "").Start(provider.Request{User: "x"}).Next(context.Background())
	if err != nil || turn.StopReason != provider.StopRefusal || turn.RefusalCategory != "cyber" {
		t.Fatalf("turn = %+v err = %v", turn, err)
	}
	if _, ok := f.bodies[0]["output_config"]; ok {
		t.Error("sin esfuerzo configurado no se envía output_config")
	}
}

func TestErrorMapping(t *testing.T) {
	t.Parallel()
	f := &fakeAPI{status: http.StatusUnauthorized}
	err := newTest(t, f, "").Ping(context.Background())
	if !errors.Is(err, provider.ErrUnauthorized) || provider.Code(err) != "PROVIDER_UNAUTHORIZED" {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "sk-ant-test") {
		t.Fatal("la clave aparece en el error")
	}
}
