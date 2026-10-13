package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/mask"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/provider"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/provider/fake"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/tools"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

// memStore es un Store en memoria (por tenant).
type memStore struct {
	mu       sync.Mutex
	settings map[uuid.UUID]domain.Settings
	audit    []domain.AuditEntry
	tokens   []domain.MCPToken
}

func newMem() *memStore { return &memStore{settings: map[uuid.UUID]domain.Settings{}} }

func (m *memStore) GetSettings(_ context.Context, t uuid.UUID) (*domain.Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.settings[t]
	if !ok {
		return nil, ErrNotFound
	}
	return &s, nil
}

func (m *memStore) PutSettings(_ context.Context, s *domain.Settings) (*domain.Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *s
	cp.Stored, cp.APIKeySet = true, s.Sealed != nil
	cp.Version = m.settings[s.TenantID].Version + 1
	m.settings[s.TenantID] = cp
	return &cp, nil
}

func (m *memStore) RecordAudit(_ context.Context, e *domain.AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audit = append(m.audit, *e)
	return nil
}

func (m *memStore) CountRequests(_ context.Context, t uuid.UUID, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, e := range m.audit {
		if e.TenantID == t && !e.CreatedAt.Before(since) && (e.Feature == domain.FeatureFinding || e.Feature == domain.FeatureRouter) &&
			e.Outcome != domain.OutcomeLimited && e.Outcome != domain.OutcomeDenied {
			n++
		}
	}
	return n, nil
}

func (m *memStore) ListAudit(_ context.Context, t uuid.UUID, limit int, _ *AuditCursor) ([]domain.AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.AuditEntry
	for i := len(m.audit) - 1; i >= 0 && len(out) < limit; i-- {
		if m.audit[i].TenantID == t {
			out = append(out, m.audit[i])
		}
	}
	return out, nil
}

func (m *memStore) CreateMCPToken(_ context.Context, t *domain.MCPToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens = append(m.tokens, *t)
	return nil
}

func (m *memStore) ListMCPTokens(_ context.Context, tenant uuid.UUID) ([]domain.MCPToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.MCPToken
	for _, t := range m.tokens {
		if t.TenantID == tenant {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *memStore) RevokeMCPToken(_ context.Context, tenant, id uuid.UUID, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.tokens {
		if m.tokens[i].TenantID == tenant && m.tokens[i].ID == id {
			m.tokens[i].RevokedAt = &at
			return nil
		}
	}
	return ErrNotFound
}

func (m *memStore) MCPTokenByHash(_ context.Context, h []byte) (*domain.MCPToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tokens {
		if string(t.Hash) == string(h) {
			cp := t
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (m *memStore) TouchMCPToken(_ context.Context, id uuid.UUID, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.tokens {
		if m.tokens[i].ID == id {
			m.tokens[i].LastUsedAt = &at
		}
	}
	return nil
}

// apiReader simula la API de Horus.
type apiReader struct {
	resp map[string]string
	errs map[string]error
}

func (r *apiReader) Get(_ context.Context, path string, _ url.Values) (any, error) {
	if err, ok := r.errs[path]; ok {
		return nil, err
	}
	body, ok := r.resp[path]
	if !ok {
		return nil, &tools.HTTPError{Status: 404}
	}
	var v any
	_ = json.Unmarshal([]byte(body), &v)
	return v, nil
}

var (
	tenantA   = uuid.MustParse("0195a000-0000-7000-8000-00000000000a")
	tenantB   = uuid.MustParse("0195a000-0000-7000-8000-00000000000b")
	userA     = uuid.MustParse("0195a000-0000-7000-8000-0000000000a1")
	findingID = "0195a000-0000-7000-8000-000000000f01"
)

type harness struct {
	svc      *Service
	store    *memStore
	prov     *fake.Provider
	reader   *apiReader
	built    []ProviderConfig
	now      time.Time
	readTok  []string
	delegate *fakeDelegator
}

type fakeDelegator struct {
	deny bool
	reqs []authapi.DelegationRequest
}

func (d *fakeDelegator) DelegatedToken(_ context.Context, req authapi.DelegationRequest) (string, error) {
	d.reqs = append(d.reqs, req)
	if d.deny {
		return "", authapi.ErrDelegationDenied
	}
	return "delegated-jwt", nil
}

func newHarness(t *testing.T, d Defaults) *harness {
	t.Helper()
	reg, err := tools.NewRegistry(tools.Horus()...)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{store: newMem(), now: time.Date(2026, 10, 13, 15, 0, 0, 0, time.UTC), delegate: &fakeDelegator{},
		prov: &fake.Provider{Turns: []provider.Turn{
			{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{{ID: "1", Name: "get_finding",
				Input: json.RawMessage(`{"finding_id":"` + findingID + `"}`)}}, Usage: provider.Usage{InputTokens: 900, OutputTokens: 60}},
			{Text: fullAnswer("El Cliente 1 (100.70.x.x) contactó 100.70.1.25"), StopReason: provider.StopEnd,
				Usage: provider.Usage{InputTokens: 1200, OutputTokens: 300}},
		}},
		reader: &apiReader{resp: map[string]string{
			"/api/v1/findings/" + findingID:               `{"id":"x","kind":"c2_contact","customer":{"address":"100.70.1.25","alias":"Juan"}}`,
			"/api/v1/findings/" + findingID + "/evidence": `{"data":[]}`,
		}},
	}
	h.svc = New(Options{
		Store: h.store, Sealer: envelope.Ephemeral(), Registry: reg, Defaults: d, Delegation: h.delegate,
		Now: func() time.Time { return h.now },
		Providers: func(c ProviderConfig) (provider.Provider, error) {
			h.built = append(h.built, c)
			return h.prov, nil
		},
		Readers: func(p *authz.Principal) tools.Reader {
			h.readTok = append(h.readTok, p.Raw)
			return h.reader
		},
	})
	return h
}

// fullAnswer envuelve un texto en las cinco secciones obligatorias.
func fullAnswer(body string) string {
	return "## Qué está pasando\n" + body + "\n## Posibles causas\n-\n## Qué hacer\n-\n## Para deshacer\n-\n## Fuentes\n- get_finding [F1]"
}

func ctxWith(tenant uuid.UUID, perms ...string) context.Context {
	p := &authz.Principal{Type: authz.TypeUser, UserID: userA, Subject: userA.String(), Scope: authz.ScopeTenant, TenantID: tenant,
		Perms: map[string][]string{}, Raw: "user-jwt"}
	for _, perm := range perms {
		p.Perms[perm] = []string{"*"}
	}
	return authz.WithPrincipal(context.Background(), p)
}

func str(s string) *string { return &s }

func code(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Code
	}
	return ""
}

func enable(t *testing.T, h *harness, upd domain.Update) {
	t.Helper()
	if _, err := h.svc.UpdateSettings(ctxWith(tenantA, PermSettingsManage, PermSettingsRead), upd); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsDefaultsAndValidation(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Defaults{})
	v, err := h.svc.Settings(ctxWith(tenantA, PermSettingsRead))
	if err != nil {
		t.Fatal(err)
	}
	if v.Source != "platform" || v.Enabled || v.Provider != "anthropic" || v.Model != "claude-opus-5-5" || v.APIKeySource != "none" {
		t.Fatalf("defaults = %+v", v)
	}
	if _, err := h.svc.Settings(ctxWith(tenantA)); code(err) != "PERMISSION_DENIED" {
		t.Fatalf("sin settings.read: %v", err)
	}
	ctx := ctxWith(tenantA, PermSettingsManage)
	cases := map[string]struct {
		upd   domain.Update
		field string
	}{
		"proveedor":       {domain.Update{Provider: "gemini", DailyRequestLimit: 10}, "provider"},
		"límite":          {domain.Update{Provider: "anthropic", DailyRequestLimit: 0}, "daily_request_limit"},
		"esfuerzo":        {domain.Update{Provider: "anthropic", DailyRequestLimit: 5, Effort: "max"}, "effort"},
		"sin base_url":    {domain.Update{Provider: "openai_compatible", DailyRequestLimit: 5}, "base_url"},
		"base_url mala":   {domain.Update{Provider: "openai_compatible", DailyRequestLimit: 5, BaseURL: str("ftp://x")}, "base_url"},
		"credenciales":    {domain.Update{Provider: "openai_compatible", DailyRequestLimit: 5, BaseURL: str("http://u:p@10.0.0.5")}, "base_url"},
		"base anthropic":  {domain.Update{Provider: "anthropic", DailyRequestLimit: 5, BaseURL: str("http://10.0.0.5")}, "base_url"},
		"clave corta":     {domain.Update{Provider: "anthropic", DailyRequestLimit: 5, APIKey: str("abc")}, "api_key"},
		"activar sin key": {domain.Update{Provider: "anthropic", DailyRequestLimit: 5, Enabled: true}, "api_key"},
		"modelo":          {domain.Update{Provider: "anthropic", DailyRequestLimit: 5, Model: "bad model;"}, "model"},
	}
	for name, c := range cases {
		_, err := h.svc.UpdateSettings(ctx, c.upd)
		e, ok := apperr.As(err)
		if !ok || e.Code != "VALIDATION_FAILED" || len(e.Fields) == 0 || !hasField(e, c.field) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func hasField(e *apperr.Error, f string) bool {
	for _, x := range e.Fields {
		if x.Field == f {
			return true
		}
	}
	return false
}

func TestAPIKeyWriteOnlyEncryptedAndNeverReturned(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Defaults{})
	secret := "sk-ant-super-secret-key-123"
	ctx := ctxWith(tenantA, PermSettingsManage, PermSettingsRead)
	v, err := h.svc.UpdateSettings(ctx, domain.Update{Enabled: true, Provider: "anthropic", DailyRequestLimit: 20, APIKey: str(secret)})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), secret) || !v.APIKeySet || v.APIKeySource != "tenant" || v.Source != "tenant" {
		t.Fatalf("vista = %s", b)
	}
	stored := h.store.settings[tenantA]
	if stored.Sealed == nil || strings.Contains(string(stored.Sealed.Ciphertext), secret) {
		t.Fatal("la clave debe guardarse cifrada")
	}
	// La clave está ligada al tenant (AAD): copiada a otro tenant no se descifra.
	h.store.settings[tenantB] = domain.Settings{TenantID: tenantB, Enabled: true, Provider: "anthropic", Model: "m", Effort: "low",
		DailyRequestLimit: 5, Sealed: stored.Sealed, Stored: true}
	if _, err := h.svc.TestConnection(ctxWith(tenantB, PermSettingsManage)); code(err) != CodeKeyStoreUnavailable {
		t.Fatalf("clave copiada a otro tenant: %v", err)
	}
	// Guardar sin api_key conserva la clave; la prueba de conexión la usa.
	if _, err := h.svc.UpdateSettings(ctx, domain.Update{Enabled: true, Provider: "anthropic", DailyRequestLimit: 30}); err != nil {
		t.Fatal(err)
	}
	res, err := h.svc.TestConnection(ctx)
	if err != nil || !res.OK || h.built[len(h.built)-1].APIKey != secret {
		t.Fatalf("test = %+v %v", res, err)
	}
	// Cambiar de proveedor sin clave nueva descarta la de Claude.
	v, err = h.svc.UpdateSettings(ctx, domain.Update{Provider: "openai_compatible", BaseURL: str("http://10.0.0.5:11434/"), DailyRequestLimit: 30})
	if err != nil || v.APIKeySet || *v.BaseURL != "http://10.0.0.5:11434" || v.Model != "llama3.1" {
		t.Fatalf("cambio de proveedor = %+v %v", v, err)
	}
	// clear_api_key.
	if _, err := h.svc.UpdateSettings(ctx, domain.Update{Provider: "openai_compatible", BaseURL: str("http://10.0.0.5"), DailyRequestLimit: 3,
		APIKey: str("local-key-1234")}); err != nil {
		t.Fatal(err)
	}
	v, err = h.svc.UpdateSettings(ctx, domain.Update{Provider: "openai_compatible", BaseURL: str("http://10.0.0.5"), DailyRequestLimit: 3,
		ClearAPIKey: true})
	if err != nil || v.APIKeySet {
		t.Fatalf("clear = %+v %v", v, err)
	}
}

func TestPlatformKeyUsedWhenTenantHasNone(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Defaults{Enabled: true, APIKey: "sk-platform-key", Effort: "low"})
	v, err := h.svc.Settings(ctxWith(tenantA, PermSettingsRead))
	if err != nil || v.APIKeySource != "platform" || !v.PlatformDefault.APIKeySet {
		t.Fatalf("vista = %+v %v", v, err)
	}
	if strings.Contains(mustJSON(t, v), "sk-platform-key") {
		t.Fatal("la clave de plataforma no se devuelve")
	}
	if _, err := h.svc.Analyze(ctxWith(tenantA, PermFindingsRead), AnalysisRequest{Feature: "finding", ContextID: findingID}); err != nil {
		t.Fatal(err)
	}
	if h.built[0].APIKey != "sk-platform-key" || h.built[0].Effort != "low" {
		t.Fatalf("config = %+v", h.built[0])
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAnalyzeSuccessMaskedAndAudited(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Defaults{})
	enable(t, h, domain.Update{Enabled: true, Provider: "anthropic", DailyRequestLimit: 5, APIKey: str("sk-ant-0123456789")})
	res, err := h.svc.Analyze(ctxWith(tenantA, PermFindingsRead), AnalysisRequest{Feature: "finding", ContextID: findingID})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Markdown, "100.70.1.25") || !strings.Contains(res.Markdown, "Cliente 1 (100.70.x.x)") {
		t.Fatalf("markdown = %q", res.Markdown)
	}
	// get_finding se consultó dos veces: precargado (anclaje) y a petición del modelo.
	if res.Disclaimer != Disclaimer || res.Usage.InputTokens != 2100 || len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "get_finding" ||
		res.ToolCalls[0].Count != 2 || res.Grounding.Status != "verified" || res.Grounding.Repaired {
		t.Fatalf("res = %+v", res)
	}
	// El primer mensaje lleva los datos primarios enmascarados y el catálogo de comandos.
	if u := h.prov.Requests[0].User; !strings.Contains(u, "Datos ya consultados") || !strings.Contains(u, `"tool":"get_finding"`) ||
		strings.Contains(u, "Juan") || strings.Contains(u, "100.70.1.25") || !strings.Contains(u, "/ip firewall address-list add") {
		t.Fatalf("user = %s", u)
	}
	// Lo que llega al proveedor (resultado de la herramienta) está enmascarado.
	sent := mustJSON(t, h.prov.Results)
	if strings.Contains(sent, "100.70.1.25") || strings.Contains(sent, "Juan") {
		t.Fatalf("datos personales enviados al proveedor: %s", sent)
	}
	// El sistema es estable (sin fechas) y las herramientas van en orden.
	req := h.prov.Requests[0]
	if strings.Contains(req.System, "2026") || len(req.Tools) != 8 || req.Tools[0].Name != "get_customer_profile" {
		t.Fatalf("petición = %+v", req.Tools)
	}
	// Las herramientas leen como el usuario.
	for _, tok := range h.readTok {
		if tok != "user-jwt" {
			t.Fatalf("lector con token %q", tok)
		}
	}
	a := h.store.audit[len(h.store.audit)-1]
	if a.Outcome != "success" || a.Feature != "finding" || a.ContextID != findingID || a.UserID == nil || *a.UserID != userA ||
		a.InputTokens != 2100 || a.OutputTokens != 360 || a.Provider != "anthropic" || len(a.ToolCalls) != 1 {
		t.Fatalf("auditoría = %+v", a)
	}
	if strings.Contains(mustJSON(t, a), "100.70") {
		t.Fatal("la auditoría no guarda datos de clientes")
	}
}

func TestAnalyzeGuards(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Defaults{})
	ctx := ctxWith(tenantA, PermFindingsRead)
	if _, err := h.svc.Analyze(ctx, AnalysisRequest{Feature: "finding", ContextID: findingID}); code(err) != CodeDisabled {
		t.Fatalf("desactivado: %v", err)
	}
	if _, err := h.svc.Analyze(ctx, AnalysisRequest{Feature: "shell", ContextID: findingID}); code(err) != "VALIDATION_FAILED" {
		t.Fatalf("feature: %v", err)
	}
	if _, err := h.svc.Analyze(ctx, AnalysisRequest{Feature: "router", ContextID: "x"}); code(err) != "VALIDATION_FAILED" {
		t.Fatalf("context_id: %v", err)
	}
	if _, err := h.svc.Analyze(ctx, AnalysisRequest{Feature: "router", ContextID: findingID}); code(err) != "PERMISSION_DENIED" {
		t.Fatalf("router sin devices.read: %v", err)
	}
	enable(t, h, domain.Update{Enabled: true, Provider: "anthropic", DailyRequestLimit: 5, APIKey: str("sk-ant-0123456789")})
	other := uuid.Must(uuid.NewV7()).String()
	if _, err := h.svc.Analyze(ctx, AnalysisRequest{Feature: "finding", ContextID: other}); code(err) != CodeContextNotFound {
		t.Fatalf("contexto invisible: %v", err)
	}
	if h.prov.Calls() != 0 {
		t.Fatal("no se llama al proveedor si el contexto no es visible")
	}
	// La API no sirve el contexto (módulo degradado, 401, cuerpo ilegible): código
	// propio con el estado, nunca un 500 sin pista.
	h.reader.errs = map[string]error{"/api/v1/findings/" + findingID: &tools.HTTPError{Status: 503, Code: "SERVICE_UNAVAILABLE"}}
	_, err := h.svc.Analyze(ctx, AnalysisRequest{Feature: "finding", ContextID: findingID})
	if e, ok := apperr.As(err); !ok || e.Code != CodeContextUnavailable || e.Status() != 502 || !strings.Contains(e.Detail, "503 SERVICE_UNAVAILABLE") {
		t.Fatalf("probe 503: %v", err)
	}
	h.reader.errs = map[string]error{"/api/v1/findings/" + findingID: errors.New("loopback: decode")}
	_, err = h.svc.Analyze(ctx, AnalysisRequest{Feature: "finding", ContextID: findingID})
	if e, ok := apperr.As(err); !ok || e.Code != CodeContextUnavailable || e.Status() != 503 {
		t.Fatalf("probe decode: %v", err)
	}
	h.reader.errs = nil
}

func TestDailyLimit(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Defaults{})
	enable(t, h, domain.Update{Enabled: true, Provider: "anthropic", DailyRequestLimit: 2, APIKey: str("sk-ant-0123456789")})
	ctx := ctxWith(tenantA, PermFindingsRead)
	for i := 0; i < 2; i++ {
		h.prov.Turns = []provider.Turn{{Text: "ok", StopReason: provider.StopEnd}}
		if _, err := h.svc.Analyze(ctx, AnalysisRequest{Feature: "finding", ContextID: findingID}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := h.svc.Analyze(ctx, AnalysisRequest{Feature: "finding", ContextID: findingID})
	e, ok := apperr.As(err)
	if !ok || e.Code != CodeLimitReached || e.RetryAfter != 9*3600+1 {
		t.Fatalf("límite: %+v", err)
	}
	if last := h.store.audit[len(h.store.audit)-1]; last.Outcome != "limited" {
		t.Fatalf("auditoría del rechazo = %+v", last)
	}
	// Otro tenant no comparte el contador; al día siguiente se reinicia.
	st, _ := h.svc.Status(ctxWith(tenantA))
	if st.Remaining != 0 {
		t.Fatalf("restantes = %d", st.Remaining)
	}
	h.now = h.now.Add(10 * time.Hour)
	st, _ = h.svc.Status(ctxWith(tenantA))
	if st.Remaining != 2 || !st.Enabled {
		t.Fatalf("día nuevo = %+v", st)
	}
}

func TestAnalyzeRefusalAndProviderErrors(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Defaults{})
	enable(t, h, domain.Update{Enabled: true, Provider: "anthropic", DailyRequestLimit: 50, APIKey: str("sk-ant-0123456789")})
	ctx := ctxWith(tenantA, PermFindingsRead)
	h.prov.Turns = []provider.Turn{{StopReason: provider.StopRefusal, RefusalCategory: "cyber"}}
	if _, err := h.svc.Analyze(ctx, AnalysisRequest{Feature: "finding", ContextID: findingID}); code(err) != CodeRefused {
		t.Fatalf("rechazo: %v", err)
	}
	if a := h.store.audit[len(h.store.audit)-1]; a.Outcome != "refused" {
		t.Fatalf("auditoría = %+v", a)
	}
	h.prov.Turns = []provider.Turn{{Text: "x"}}
	h.prov.Err, h.prov.ErrAt = provider.ErrRateLimited, h.prov.Calls()
	_, err := h.svc.Analyze(ctx, AnalysisRequest{Feature: "finding", ContextID: findingID})
	if code(err) != CodeProviderUnavailable || !strings.Contains(err.Error(), "") {
		t.Fatalf("proveedor: %v", err)
	}
	if a := h.store.audit[len(h.store.audit)-1]; a.Outcome != "error" || a.ErrorCode != "PROVIDER_RATE_LIMITED" {
		t.Fatalf("auditoría = %+v", a)
	}
	// La lista de auditoría es por tenant.
	page, err := h.svc.Audit(ctxWith(tenantB, PermSettingsRead), 10, nil)
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("otro tenant ve auditoría: %+v %v", page, err)
	}
	page, err = h.svc.Audit(ctxWith(tenantA, PermSettingsRead), 1, nil)
	if err != nil || len(page.Data) != 1 || page.Next == nil {
		t.Fatalf("página = %+v %v", page, err)
	}
}

func TestTestConnectionFailureAudited(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Defaults{})
	enable(t, h, domain.Update{Provider: "openai_compatible", BaseURL: str("http://10.0.0.5:11434"), DailyRequestLimit: 5})
	h.prov.PingErr = errors.Join(provider.ErrUnauthorized)
	res, err := h.svc.TestConnection(ctxWith(tenantA, PermSettingsManage))
	if err != nil || res.OK || res.ErrorCode != "PROVIDER_UNAUTHORIZED" || res.Message == "" {
		t.Fatalf("res = %+v %v", res, err)
	}
	a := h.store.audit[len(h.store.audit)-1]
	if a.Feature != "test" || a.Outcome != "error" {
		t.Fatalf("auditoría = %+v", a)
	}
	// La prueba no cuenta para el límite diario.
	if n, _ := h.store.CountRequests(context.Background(), tenantA, time.Time{}); n != 0 {
		t.Fatalf("cuenta = %d", n)
	}
}

func TestMCPTokenLifecycle(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Defaults{})
	ctx := ctxWith(tenantA, PermMCPTokens)
	if _, err := h.svc.CreateMCPToken(ctxWith(tenantA), "x"); code(err) != "PERMISSION_DENIED" {
		t.Fatalf("sin permiso: %v", err)
	}
	issued, err := h.svc.CreateMCPToken(ctx, " Claude Desktop ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(issued.Token, TokenPrefix) || issued.Name != "Claude Desktop" || !issued.Active {
		t.Fatalf("issued = %+v", issued)
	}
	if strings.Contains(string(h.store.tokens[0].Hash), issued.Token) || len(h.store.tokens[0].Hash) != 32 {
		t.Fatal("solo se guarda el hash")
	}
	list, _ := h.svc.ListMCPTokens(ctx)
	if len(list) != 1 || strings.Contains(mustJSON(t, list), issued.Token) {
		t.Fatalf("lista = %+v", list)
	}
	sess, err := h.svc.AuthenticateMCP(context.Background(), issued.Token)
	if err != nil || sess.TenantID != tenantA || sess.UserID != userA {
		t.Fatalf("auth = %+v %v", sess, err)
	}
	if _, err := h.svc.AuthenticateMCP(context.Background(), "hmcp_otro"); !errors.Is(err, ErrMCPUnauthenticated) {
		t.Fatalf("token desconocido: %v", err)
	}
	// Llamada MCP con el asistente desactivado → denegada y auditada.
	m := mask.New()
	if _, err := h.svc.MCPCall(context.Background(), sess, m, "get_finding", json.RawMessage(`{"finding_id":"`+findingID+`"}`)); err == nil {
		t.Fatal("MCP con el asistente desactivado")
	}
	enable(t, h, domain.Update{Enabled: true, Provider: "anthropic", DailyRequestLimit: 5, APIKey: str("sk-ant-0123456789")})
	out, err := h.svc.MCPCall(context.Background(), sess, m, "get_finding", json.RawMessage(`{"finding_id":"`+findingID+`"}`))
	if err != nil || strings.Contains(out, "100.70.1.25") || !strings.Contains(out, "Cliente 1") {
		t.Fatalf("out = %s err = %v", out, err)
	}
	d := h.delegate.reqs[len(h.delegate.reqs)-1]
	if d.UserID != userA || d.TenantID != tenantA || len(d.Permissions) == 0 || h.readTok[len(h.readTok)-1] != "delegated-jwt" {
		t.Fatalf("delegación = %+v tok = %v", d, h.readTok)
	}
	a := h.store.audit[len(h.store.audit)-1]
	if a.Feature != "mcp" || a.ContextID != "get_finding" || a.Outcome != "success" {
		t.Fatalf("auditoría = %+v", a)
	}
	// Si auth ya no delega (membresía revocada), el token deja de servir.
	h.delegate.deny = true
	if _, err := h.svc.MCPCall(context.Background(), sess, m, "list_open_findings", nil); err == nil {
		t.Fatal("delegación denegada aceptada")
	}
	h.delegate.deny = false
	if err := h.svc.RevokeMCPToken(ctx, issued.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.AuthenticateMCP(context.Background(), issued.Token); !errors.Is(err, ErrMCPUnauthenticated) {
		t.Fatalf("token revocado: %v", err)
	}
	if err := h.svc.RevokeMCPToken(ctxWith(tenantB, PermMCPTokens), issued.ID); code(err) != CodeTokenNotFound {
		t.Fatalf("revocar desde otro tenant: %v", err)
	}
	// Caducidad.
	issued2, _ := h.svc.CreateMCPToken(ctx, "otro")
	h.now = h.now.Add(91 * 24 * time.Hour)
	if _, err := h.svc.AuthenticateMCP(context.Background(), issued2.Token); !errors.Is(err, ErrMCPUnauthenticated) {
		t.Fatalf("token caducado: %v", err)
	}
}

func TestAnalyzeRepairsUngroundedAnswer(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Defaults{})
	// Primera respuesta: una IP y un ASN que no están en ningún dato, y un comando fuera del catálogo.
	h.prov.Turns = []provider.Turn{
		{Text: "## Qué está pasando\nContactó 203.0.113.9 (AS64496).\n```\n/system reboot\n```", StopReason: provider.StopEnd,
			Usage: provider.Usage{InputTokens: 100, OutputTokens: 10}},
		{Text: fullAnswer("Contactó un destino sin dato [F1]."), StopReason: provider.StopEnd, Usage: provider.Usage{InputTokens: 200, OutputTokens: 20}},
	}
	enable(t, h, domain.Update{Enabled: true, Provider: "anthropic", DailyRequestLimit: 5, APIKey: str("sk-ant-0123456789")})
	res, err := h.svc.Analyze(ctxWith(tenantA, PermFindingsRead), AnalysisRequest{Feature: "finding", ContextID: findingID})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.prov.UserMessages) != 1 || !strings.Contains(h.prov.UserMessages[0], "203.0.113.9") ||
		!strings.Contains(h.prov.UserMessages[0], "/system reboot") || !strings.Contains(h.prov.UserMessages[0], "Faltan las secciones") {
		t.Fatalf("corrección = %q", h.prov.UserMessages)
	}
	if res.Grounding.Status != "verified" || !res.Grounding.Repaired || strings.Contains(res.Markdown, "⚠") || res.Usage.InputTokens != 300 {
		t.Fatalf("res = %+v md = %q", res.Grounding, res.Markdown)
	}
}

func TestAnalyzeWarnsWhenRepairFails(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Defaults{})
	bad := fullAnswer("Contactó 203.0.113.9 con 9,8 GB.")
	h.prov.Turns = []provider.Turn{{Text: bad, StopReason: provider.StopEnd, Usage: provider.Usage{InputTokens: 100, OutputTokens: 10}}}
	enable(t, h, domain.Update{Enabled: true, Provider: "anthropic", DailyRequestLimit: 5, APIKey: str("sk-ant-0123456789")})
	res, err := h.svc.Analyze(ctxWith(tenantA, PermFindingsRead), AnalysisRequest{Feature: "finding", ContextID: findingID})
	if err != nil {
		t.Fatal(err)
	}
	if res.Grounding.Status != "partial" || !res.Grounding.Repaired || len(res.Grounding.Unsupported) != 2 ||
		!strings.Contains(res.Markdown, "⚠ Verificación: datos que no aparecen en Horus: 203.0.113.9, 9,8 GB") {
		t.Fatalf("res = %+v md = %q", res.Grounding, res.Markdown)
	}
}
