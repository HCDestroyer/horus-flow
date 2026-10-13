package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/app"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/tools"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

// store mínimo para el servidor MCP.
type store struct {
	app.Store
	mu      sync.Mutex
	enabled bool
	tokens  []domain.MCPToken
	audit   []domain.AuditEntry
}

func (s *store) GetSettings(_ context.Context, t uuid.UUID) (*domain.Settings, error) {
	return &domain.Settings{TenantID: t, Enabled: s.enabled, Provider: "anthropic", Model: "m", Effort: "medium", DailyRequestLimit: 5,
		Stored: true}, nil
}

func (s *store) CreateMCPToken(_ context.Context, t *domain.MCPToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = append(s.tokens, *t)
	return nil
}

func (s *store) MCPTokenByHash(_ context.Context, h []byte) (*domain.MCPToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tokens {
		if bytes.Equal(t.Hash, h) {
			cp := t
			return &cp, nil
		}
	}
	return nil, app.ErrNotFound
}

func (s *store) TouchMCPToken(context.Context, uuid.UUID, time.Time) error { return nil }

func (s *store) RecordAudit(_ context.Context, e *domain.AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audit = append(s.audit, *e)
	return nil
}

type delegator struct{}

func (delegator) DelegatedToken(context.Context, authapi.DelegationRequest) (string, error) {
	return "delegated", nil
}

type reader struct{}

func (reader) Get(_ context.Context, path string, _ url.Values) (any, error) {
	if strings.HasPrefix(path, "/api/v1/flow-exporters") {
		return map[string]any{"data": []any{map[string]any{"state": "silent", "router_id": "r1", "tenant_id": "t"}}}, nil
	}
	return nil, &tools.HTTPError{Status: 404}
}

func setup(t *testing.T, enabled bool) (*httptest.Server, string, *store) {
	t.Helper()
	reg, err := tools.NewRegistry(tools.Horus()...)
	if err != nil {
		t.Fatal(err)
	}
	st := &store{enabled: enabled}
	svc := app.New(app.Options{Store: st, Registry: reg, Delegation: delegator{},
		Readers: func(*authz.Principal) tools.Reader { return reader{} }})
	tenant, user := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	ctx := authz.WithPrincipal(context.Background(), &authz.Principal{Type: authz.TypeUser, UserID: user, Subject: user.String(),
		Scope: authz.ScopeTenant, TenantID: tenant, Perms: map[string][]string{app.PermMCPTokens: {"*"}}})
	issued, err := svc.CreateMCPToken(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(svc, nil))
	t.Cleanup(ts.Close)
	return ts, issued.Token, st
}

func rpc(t *testing.T, url, token, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return res.StatusCode, out
}

const initMsg = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},
	"clientInfo":{"name":"test","version":"1"}}}`

func TestMCPRequiresToken(t *testing.T) {
	t.Parallel()
	ts, _, _ := setup(t, true)
	if code, _ := rpc(t, ts.URL, "", initMsg); code != http.StatusUnauthorized {
		t.Fatalf("sin token = %d", code)
	}
	if code, _ := rpc(t, ts.URL, "hmcp_falso", initMsg); code != http.StatusUnauthorized {
		t.Fatalf("token falso = %d", code)
	}
	if code, _ := rpc(t, ts.URL, "eyJhbGciOi.jwt.horus", initMsg); code != http.StatusUnauthorized {
		t.Fatalf("un JWT no sirve como token MCP = %d", code)
	}
}

func TestMCPListAndCallTools(t *testing.T) {
	t.Parallel()
	ts, tok, st := setup(t, true)
	code, out := rpc(t, ts.URL, tok, initMsg)
	if code != http.StatusOK {
		t.Fatalf("initialize = %d %v", code, out)
	}
	info := out["result"].(map[string]any)["serverInfo"].(map[string]any)
	if info["name"] != "horus-flow" {
		t.Fatalf("serverInfo = %v", info)
	}
	code, out = rpc(t, ts.URL, tok, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if code != http.StatusOK {
		t.Fatalf("tools/list = %d %v", code, out)
	}
	list := out["result"].(map[string]any)["tools"].([]any)
	if len(list) != 8 {
		t.Fatalf("herramientas = %d", len(list))
	}
	first := list[0].(map[string]any)
	if first["annotations"].(map[string]any)["readOnlyHint"] != true {
		t.Fatalf("anotaciones = %v", first)
	}
	code, out = rpc(t, ts.URL, tok, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_exporter_status","arguments":{}}}`)
	if code != http.StatusOK {
		t.Fatalf("tools/call = %d %v", code, out)
	}
	res := out["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if res["isError"] == true || !strings.Contains(text, "silent") || strings.Contains(text, "tenant_id") {
		t.Fatalf("resultado = %v", res)
	}
	// Sobre anclado: hechos, faltantes y contenido estructurado.
	sc, _ := res["structuredContent"].(map[string]any)
	if sc["tool"] != "get_exporter_status" || len(sc["facts"].([]any)) == 0 || sc["missing"] == nil || !strings.Contains(text, `"facts"`) {
		t.Fatalf("sobre = %v", sc)
	}
	// Prompts y recursos de referencia.
	code, out = rpc(t, ts.URL, tok, `{"jsonrpc":"2.0","id":5,"method":"prompts/list","params":{}}`)
	if code != http.StatusOK || len(out["result"].(map[string]any)["prompts"].([]any)) != 2 {
		t.Fatalf("prompts/list = %d %v", code, out)
	}
	code, out = rpc(t, ts.URL, tok, `{"jsonrpc":"2.0","id":6,"method":"resources/read","params":{"uri":"horus://routeros"}}`)
	if code != http.StatusOK {
		t.Fatalf("resources/read = %d %v", code, out)
	}
	if rt := out["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)["text"].(string); !strings.Contains(rt, "/interface wireguard peers print detail") {
		t.Fatalf("recurso = %s", rt)
	}
	code, out = rpc(t, ts.URL, tok, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_router","arguments":{"router_id":"nope"}}}`)
	if code != http.StatusOK || out["result"].(map[string]any)["isError"] != true {
		t.Fatalf("argumento inválido = %d %v", code, out)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.audit) != 2 || st.audit[0].Feature != "mcp" || st.audit[0].ContextID != "get_exporter_status" || st.audit[1].Outcome != "error" {
		t.Fatalf("auditoría = %+v", st.audit)
	}
}

func TestMCPDisabledTenant(t *testing.T) {
	t.Parallel()
	ts, tok, _ := setup(t, false)
	_, out := rpc(t, ts.URL, tok, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_open_findings","arguments":{}}}`)
	res := out["result"].(map[string]any)
	if res["isError"] != true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), "no está activado") {
		t.Fatalf("resultado = %v", out)
	}
}
