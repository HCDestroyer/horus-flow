package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/hcdestroyer/horus-flow/services/assist/internal/mask"
)

// fakeReader responde por ruta (sin query) con JSON o error.
type fakeReader struct {
	mu    sync.Mutex
	resp  map[string]string
	errs  map[string]error
	calls []string
}

func (f *fakeReader) Get(_ context.Context, path string, q url.Values) (any, error) {
	f.mu.Lock()
	f.calls = append(f.calls, path+"?"+q.Encode())
	f.mu.Unlock()
	if err, ok := f.errs[path]; ok {
		return nil, err
	}
	body, ok := f.resp[path]
	if !ok {
		return nil, &HTTPError{Status: 404, Code: "NOT_FOUND"}
	}
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return nil, err
	}
	return v, nil
}

const findingID = "0195a000-0000-7000-8000-000000000001"
const routerID = "0195a000-0000-7000-8000-0000000000aa"

func registry(t *testing.T) *Registry {
	t.Helper()
	r, err := NewRegistry(Horus()...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRegistrySpecsSortedAndPermissions(t *testing.T) {
	t.Parallel()
	r := registry(t)
	specs := r.Specs()
	if len(specs) != 7 {
		t.Fatalf("specs = %d", len(specs))
	}
	for i := 1; i < len(specs); i++ {
		if specs[i-1].Name >= specs[i].Name {
			t.Fatalf("orden no estable: %s, %s", specs[i-1].Name, specs[i].Name)
		}
	}
	perms := strings.Join(r.Permissions(), ",")
	for _, p := range []string{"security.findings.read", "devices.read", "flows.read", "customers.read", "traffic.read"} {
		if !strings.Contains(perms, p) {
			t.Errorf("falta permiso %s en %s", p, perms)
		}
	}
	if strings.Contains(perms, "manage") || strings.Contains(perms, "write") {
		t.Errorf("las herramientas son de solo lectura: %s", perms)
	}
}

func TestNewRegistryRejectsDuplicatesAndBadSchemas(t *testing.T) {
	t.Parallel()
	tool := Tool{Name: "dup", Schema: object(nil)}
	if _, err := NewRegistry(tool, tool); err == nil {
		t.Fatal("nombre repetido aceptado")
	}
	if _, err := NewRegistry(Tool{Name: "bad", Schema: map[string]any{"type": "string"}}); err == nil {
		t.Fatal("esquema no objeto aceptado")
	}
	if _, err := NewRegistry(Tool{Name: "Bad Name", Schema: object(nil)}); err == nil {
		t.Fatal("nombre inválido aceptado")
	}
}

func TestCallValidatesArguments(t *testing.T) {
	t.Parallel()
	r := registry(t)
	env := Env{Reader: &fakeReader{}}
	cases := map[string]struct{ tool, args, want string }{
		"desconocida":  {"drop_table", `{}`, "unknown tool"},
		"no objeto":    {"get_finding", `[1]`, "objeto JSON"},
		"falta":        {"get_finding", `{}`, "Falta el argumento finding_id"},
		"extra":        {"get_finding", `{"finding_id":"` + findingID + `","sql":"x"}`, "desconocido: sql"},
		"uuid":         {"get_finding", `{"finding_id":"1 OR 1=1"}`, "formato no válido"},
		"tipo":         {"get_finding", `{"finding_id":42}`, "debe ser un texto"},
		"enum":         {"get_traffic_top", `{"dimension":"passwords"}`, "valor no admitido"},
		"entero":       {"list_open_findings", `{"limit":2.5}`, "debe ser un entero"},
		"máximo":       {"list_open_findings", `{"limit":500}`, "máximo 50"},
		"booleano":     {"get_finding", `{"finding_id":"` + findingID + `","include_evidence":"yes"}`, "true o false"},
		"uno de los 2": {"reputation_lookup", `{}`, "asn o prefix"},
		"prefix":       {"reputation_lookup", `{"prefix":"no-ip"}`, "no válido"},
	}
	for name, c := range cases {
		_, err := r.Call(context.Background(), env, c.tool, json.RawMessage(c.args))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestGetFindingMasksAndAggregatesEvidence(t *testing.T) {
	t.Parallel()
	fr := &fakeReader{resp: map[string]string{
		"/api/v1/findings/" + findingID: `{"id":"` + findingID + `","tenant_id":"t","kind":"c2_contact","severity":"high",
			"customer":{"customer_id":"c1","address":"100.70.1.25","alias":"Juan"},
			"summary":{"text":"Contacto con C2"},
			"recommended_actions":[{"routeros":{"rendered_commands":["add address=100.70.1.25"]}}]}`,
		"/api/v1/findings/" + findingID + "/evidence": `{"data":[
			{"ts":"2026-10-13T10:00:00.000Z","remote_ip":"203.0.113.9","remote_port":443,"protocol":6,"direction":"upload","bytes":"100","packets":"2","remote_asn":64500},
			{"ts":"2026-10-13T10:05:00.000Z","remote_ip":"203.0.113.9","remote_port":443,"protocol":6,"direction":"upload","bytes":"300","packets":"4","remote_asn":64500},
			{"ts":"2026-10-13T10:01:00.000Z","remote_ip":"100.70.9.9","remote_port":23,"protocol":6,"direction":"internal","bytes":"10","packets":"1"}
		],"page":{}}`,
	}}
	out, err := registry(t).Call(context.Background(), Env{Reader: fr, Masker: mask.New()}, "get_finding",
		json.RawMessage(`{"finding_id":"`+findingID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"100.70.1.25", "100.70.9.9", "Juan", "rendered_commands", `"tenant_id"`} {
		if strings.Contains(out, leak) {
			t.Errorf("fuga de %q: %s", leak, out)
		}
	}
	for _, want := range []string{"Cliente 1 (100.70.x.x)", "Cliente 2 (100.70.x.x)", `"bytes":400`, `"flows":2`, "203.0.113.9", `"flows_sampled":3`} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q: %s", want, out)
		}
	}
}

func TestGetFindingWithoutEvidencePermission(t *testing.T) {
	t.Parallel()
	fr := &fakeReader{
		resp: map[string]string{"/api/v1/findings/" + findingID: `{"id":"x","kind":"scanning"}`},
		errs: map[string]error{"/api/v1/findings/" + findingID + "/evidence": &HTTPError{Status: 403, Code: "PERMISSION_DENIED"}},
	}
	out, err := registry(t).Call(context.Background(), Env{Reader: fr}, "get_finding", json.RawMessage(`{"finding_id":"`+findingID+`"}`))
	if err != nil || !strings.Contains(out, "sin permiso") {
		t.Fatalf("out = %s, err = %v", out, err)
	}
}

func TestPublicErrors(t *testing.T) {
	t.Parallel()
	r := registry(t)
	cases := map[error]string{
		&HTTPError{Status: 403}:                  "no tiene permiso",
		&HTTPError{Status: 404}:                  "No encontrado",
		&HTTPError{Status: 503}:                  "no está disponible",
		&HTTPError{Status: 418}:                  "418",
		context.DeadlineExceeded:                 "tardó demasiado",
		errors.New("pq: secret internal detail"): "Error interno",
	}
	for e, want := range cases {
		fr := &fakeReader{errs: map[string]error{"/api/v1/routers/" + routerID: e}}
		_, err := r.Call(context.Background(), Env{Reader: fr}, "get_router", json.RawMessage(`{"router_id":"`+routerID+`"}`))
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "secret") {
			t.Errorf("%v → %v, want %q", e, err, want)
		}
	}
}

func TestGetRouterCombinesPeerAndExporter(t *testing.T) {
	t.Parallel()
	fr := &fakeReader{
		resp: map[string]string{
			"/api/v1/routers/" + routerID:        `{"id":"r","name":"core-1","onboarding_state":"tunnel_up","tunnel_address":"10.255.0.7"}`,
			"/api/v1/wireguard/peers":            `{"data":[{"status":"active","handshake_state":"stale","address":"10.255.0.7/32","public_key":"k="}]}`,
			"/api/v1/flow-exporters/" + routerID: `{"state":"silent","exporter_ip":"10.255.0.7"}`,
		},
	}
	out, err := registry(t).Call(context.Background(), Env{Reader: fr}, "get_router", json.RawMessage(`{"router_id":"`+routerID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"core-1", "tunnel_up", "stale", "silent", "10.255.0.7/32"} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q: %s", want, out)
		}
	}
	if strings.Contains(out, "k=") {
		t.Errorf("clave pública filtrada: %s", out)
	}
	if !strings.Contains(strings.Join(fr.calls, " "), "router_id="+routerID) {
		t.Errorf("peers sin filtro de router: %v", fr.calls)
	}
}

func TestTrafficTopCustomersPseudonymized(t *testing.T) {
	t.Parallel()
	fr := &fakeReader{resp: map[string]string{"/api/v1/analytics/traffic/top": `{"data":{"dimension":"customers","rows":[
		{"key":"c1","label":"Juan Pérez","down_bytes":"10","up_bytes":"5","share":0.5,"customer":{"customer_id":"c1","address":"100.70.1.25","alias":"Juan Pérez"}}
	],"others":{},"totals":{}},"meta":{"range":"24h"}}`}}
	out, err := registry(t).Call(context.Background(), Env{Reader: fr}, "get_traffic_top", json.RawMessage(`{"dimension":"customers"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Juan") || strings.Contains(out, "100.70.1.25") || !strings.Contains(out, "Cliente 1 (100.70.x.x)") {
		t.Errorf("out = %s", out)
	}
	if !strings.Contains(strings.Join(fr.calls, " "), "range=24h") {
		t.Errorf("range por defecto: %v", fr.calls)
	}
}

func TestReputationLookup(t *testing.T) {
	t.Parallel()
	fr := &fakeReader{resp: map[string]string{
		"/api/v1/reputation/allowlist": `{"data":[{"prefix":"203.0.113.0/24","asn":null,"reason":"monitoreo propio"},{"prefix":null,"asn":64501,"reason":"cdn"}]}`,
		"/api/v1/findings": `{"data":[{"id":"f1","kind":"c2_contact","primary_target":{"type":"remote_ip","value":"203.0.113.9"},
			"customer":{"address":"100.70.1.25"}},{"id":"f2","primary_target":{"type":"remote_asn","value":"AS64999"}}]}`,
		"/api/v1/reputation/sources": `{"data":[{"name":"Feodo","category":"c2","entries":10,"age_seconds":60,"url":"https://x"}]}`,
	}}
	out, err := registry(t).Call(context.Background(), Env{Reader: fr}, "reputation_lookup", json.RawMessage(`{"prefix":"203.0.113.9"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"in_isp_allowlist":true`, "monitoreo propio", `"id":"f1"`, "Feodo"} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q: %s", want, out)
		}
	}
	if strings.Contains(out, `"id":"f2"`) || strings.Contains(out, "100.70.1.25") || strings.Contains(out, "https://x") {
		t.Errorf("out = %s", out)
	}
	out, err = registry(t).Call(context.Background(), Env{Reader: fr}, "reputation_lookup", json.RawMessage(`{"asn":64999}`))
	if err != nil || !strings.Contains(out, `"id":"f2"`) || !strings.Contains(out, `"in_isp_allowlist":false`) {
		t.Errorf("asn: %s %v", out, err)
	}
}

func TestResultTruncated(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", MaxResultBytes*2)
	r, err := NewRegistry(Tool{Name: "big", Schema: object(nil), Run: func(context.Context, Env, map[string]any) (any, error) {
		return map[string]any{"v": big}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Call(context.Background(), Env{}, "big", nil)
	if err != nil || len(out) > MaxResultBytes+64 || !strings.HasSuffix(out, "[recortado]") {
		t.Fatalf("len = %d err = %v", len(out), err)
	}
}
