package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/hcdestroyer/horus-flow/services/assist/internal/agent"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/provider"
)

type server struct {
	mu       sync.Mutex
	requests []map[string]any
	auth     []string
	replies  []string
	status   int
}

func (s *server) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(b, &req); err != nil {
			t.Errorf("cuerpo no JSON: %v", err)
		}
		s.mu.Lock()
		s.requests = append(s.requests, req)
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		i := len(s.requests) - 1
		status := s.status
		s.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, s.replies[min(i, len(s.replies)-1)])
	})
}

func newProvider(t *testing.T, s *server, key string) *Provider {
	t.Helper()
	ts := httptest.NewServer(s.handler(t))
	t.Cleanup(ts.Close)
	// Cliente de test sin la protección de destino (el servidor de prueba escucha en loopback).
	return New(Options{BaseURL: ts.URL + "/", APIKey: key, Model: "llama3.1", HTTPClient: ts.Client()})
}

func TestToolLoopAgainstServer(t *testing.T) {
	t.Parallel()
	s := &server{replies: []string{
		`{"model":"llama3.1","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[
			{"id":"call_a","type":"function","function":{"name":"get_router","arguments":"{\"router_id\":\"r1\"}"}},
			{"type":"function","function":{"name":"get_exporter_status","arguments":""}}]}}],
		 "usage":{"prompt_tokens":120,"completion_tokens":30}}`,
		`{"model":"llama3.1","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"## Qué está pasando\nEl exportador está silencioso."}}],
		 "usage":{"prompt_tokens":400,"completion_tokens":80}}`,
	}}
	p := newProvider(t, s, "sk-local")
	conv := p.Start(provider.Request{System: "sistema", User: "diagnostica", MaxTokens: 2000,
		Tools: []provider.ToolSpec{{Name: "get_router", Description: "d", Schema: map[string]any{"type": "object"}}}})
	var mu sync.Mutex
	var calls []provider.ToolCall
	res, err := agent.Run(context.Background(), conv, func(_ context.Context, c provider.ToolCall) (string, error) {
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
		if c.Name == "get_exporter_status" {
			return "", errors.New("sin permiso")
		}
		return `{"state":"silent"}`, nil
	}, agent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Markdown, "silencioso") || res.Usage.InputTokens != 520 || res.Usage.OutputTokens != 110 {
		t.Fatalf("res = %+v", res)
	}
	sort.Slice(calls, func(i, j int) bool { return calls[i].Name > calls[j].Name }) // get_router, get_exporter_status
	if len(calls) != 2 || string(calls[0].Input) != `{"router_id":"r1"}` || string(calls[1].Input) != "{}" || calls[1].ID == "" {
		t.Fatalf("calls = %+v", calls)
	}
	if s.auth[0] != "Bearer sk-local" {
		t.Fatalf("auth = %q", s.auth[0])
	}
	first := s.requests[0]
	if first["model"] != "llama3.1" || first["stream"] != false || first["max_tokens"].(float64) != 2000 {
		t.Fatalf("petición = %v", first)
	}
	tools := first["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if tools[0].(map[string]any)["type"] != "function" || fn["name"] != "get_router" {
		t.Fatalf("tools = %v", tools)
	}
	// Segunda petición: system, user, assistant (con tool_calls) y un mensaje `tool` por llamada.
	msgs := s.requests[1]["messages"].([]any)
	if len(msgs) != 5 {
		t.Fatalf("mensajes = %d: %v", len(msgs), msgs)
	}
	roles := []string{"system", "user", "assistant", "tool", "tool"}
	for i, m := range msgs {
		if m.(map[string]any)["role"] != roles[i] {
			t.Fatalf("rol %d = %v", i, m)
		}
	}
	if msgs[3].(map[string]any)["tool_call_id"] != "call_a" || !strings.HasPrefix(msgs[4].(map[string]any)["content"].(string), "ERROR: ") {
		t.Fatalf("resultados = %v %v", msgs[3], msgs[4])
	}
	if msgs[4].(map[string]any)["tool_call_id"] != calls[1].ID {
		t.Fatalf("id generado no coincide: %v vs %s", msgs[4], calls[1].ID)
	}
}

func TestErrorsMapped(t *testing.T) {
	t.Parallel()
	cases := map[int]error{401: provider.ErrUnauthorized, 404: provider.ErrNotFound, 429: provider.ErrRateLimited,
		500: provider.ErrUnavailable, 400: provider.ErrBadRequest}
	for status, want := range cases {
		s := &server{status: status}
		p := newProvider(t, s, "")
		if err := p.Ping(context.Background()); !errors.Is(err, want) {
			t.Errorf("%d → %v, want %v", status, err, want)
		}
		s.mu.Lock()
		got := s.auth[0]
		s.mu.Unlock()
		if got != "" {
			t.Errorf("sin clave no se envía Authorization: %q", got)
		}
	}
}

func TestContentFilterIsRefusal(t *testing.T) {
	t.Parallel()
	s := &server{replies: []string{`{"choices":[{"finish_reason":"content_filter","message":{"role":"assistant","content":""}}]}`}}
	turn, err := newProvider(t, s, "").Start(provider.Request{User: "x"}).Next(context.Background())
	if err != nil || turn.StopReason != provider.StopRefusal {
		t.Fatalf("turn = %+v err = %v", turn, err)
	}
}

func TestDialGuard(t *testing.T) {
	t.Parallel()
	deny := []netip.Prefix{netip.MustParsePrefix("172.18.0.5/32")}
	g := dialGuard(deny, false)
	for _, addr := range []string{"169.254.169.254:80", "127.0.0.1:11434", "[::1]:11434", "172.18.0.5:8080", "0.0.0.0:80", "224.0.0.1:80"} {
		if err := g("tcp", addr, nil); !errors.Is(err, ErrDenied) {
			t.Errorf("%s permitido", addr)
		}
	}
	for _, addr := range []string{"10.0.0.5:11434", "192.168.1.20:8000", "203.0.113.7:443"} {
		if err := g("tcp", addr, nil); err != nil {
			t.Errorf("%s denegado: %v", addr, err)
		}
	}
	if err := dialGuard(deny, true)("tcp", "127.0.0.1:11434", nil); err != nil {
		t.Errorf("loopback con permiso: %v", err)
	}
}

func TestGuardedClientRefusesLoopback(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()
	p := New(Options{BaseURL: ts.URL})
	if err := p.Ping(context.Background()); !errors.Is(err, provider.ErrUnavailable) || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("err = %v", err)
	}
}
