//go:build integration

package tenancy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/webhooksig"
)

// fakeArgosBatch es una ArgosISP 1.2.0 con el resolvedor por lotes (A1) y la
// empresa del servicio (multiempresa).
type fakeArgosBatch struct {
	mu      sync.Mutex
	company any // {id, nombre, nit} o nil
}

func (f *fakeArgosBatch) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer atk_prueba_1234" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/api/v1/integraciones/horus/resolver" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var in struct {
		IPs []string `json:"ips"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	out := []map[string]any{}
	for _, ip := range in.IPs {
		if ip != "10.10.0.41" {
			out = append(out, map[string]any{"ip": ip, "error": "no_encontrada"})
			continue
		}
		out = append(out, map[string]any{"ip": ip, "servicio_id": 812, "cliente_id": 455,
			"cliente":  map[string]any{"nombre": "Ferretería López", "codigo": "C-000455", "tipo": "empresa"},
			"servicio": map[string]any{"user_ppp": "flopez", "plan": "Fibra 100", "estado": "activo", "router_id": 3},
			"empresa":  f.company})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"resultados": out})
}

// H7–H9 (contrato 1.2.0) de extremo a extremo: empresa del servicio guardada
// y devuelta en la identidad y la consulta 360; webhook con empresa_id que la
// refresca; consulta 360 por external_service_id con un hf_pat_; estado de
// seguridad por lotes con el estado de detection; un token de otro ISP no ve
// el servicio.
func TestArgosMultiCompanyServiceLookupAndSecurityStates(t *testing.T) {
	t.Parallel()
	a := startAppEnv(t, "HORUS_INTEGRATIONS_ALLOW_PRIVATE=true", "HORUS_PUBLIC_BASE_URL=https://horus.test", "HORUS_INTEGRATIONS_SWEEP_EVERY=1h")
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "multi-a"), a.newISP(pt, "multi-b")
	params := a.fixtureISP(A, "10.10.0.0/24")
	a.fixtureISP(B, "10.10.0.0/24")
	cust := params["customer_id"]
	fa := &fakeArgosBatch{company: map[string]any{"id": 2, "nombre": "Fibra Sur", "nit": "1234567K"}}
	srv := httptest.NewServer(fa)
	defer srv.Close()
	a.must(a.do(req{Method: "PUT", Path: "/api/v1/integrations/argosisp", Token: A.token,
		Body: map[string]any{"base_url": srv.URL + "/api/v1", "enabled": true, "token": "atk_prueba_1234"}}), 200)

	// H7: la empresa del resolutor queda en la identidad vigente (y su NIT en el periodo).
	id := a.must(a.post(A.token, "/api/v1/customers/"+cust+"/external-identity/refresh", nil), 200)
	cur, _ := id.Body["current"].(map[string]any)
	hist, _ := id.Body["history"].([]any)
	if cur["company_id"] != float64(2) || cur["company_name"] != "Fibra Sur" || len(hist) != 1 ||
		hist[0].(map[string]any)["company_nit"] != "1234567K" {
		t.Fatalf("identidad con empresa = %s", id.Raw)
	}

	// Código de conexión ArgosISP (plantilla sin cambios: security.findings.read ya está).
	tokA := a.must(a.post(A.token, "/api/v1/api-tokens", map[string]any{"name": "ArgosISP", "template": "argosisp"}), 201).str("token")
	tokB := a.must(a.post(B.token, "/api/v1/api-tokens", map[string]any{"name": "ArgosISP", "template": "argosisp"}), 201).str("token")

	// H8: consulta 360 por servicio, sin IP; devuelve la IP y la empresa.
	look := a.must(a.post(tokA, "/api/v1/integrations/ip-lookup", map[string]any{"external_service_id": 812, "include": []string{"security"}}), 200)
	c, _ := look.Body["customer"].(map[string]any)
	ext, _ := look.Body["external"].(map[string]any)
	if c["id"] != cust || c["address"] != "10.10.0.41" || ext["company_id"] != float64(2) || ext["company_name"] != "Fibra Sur" {
		t.Fatalf("ip-lookup por servicio = %s", look.Raw)
	}
	if r := a.post(tokA, "/api/v1/integrations/ip-lookup", map[string]any{"external_service_id": 812, "address": "10.10.0.41"}); r.Status != 422 {
		t.Fatalf("con ambos → 422: %d %s", r.Status, r.Raw)
	}
	if r := a.post(tokA, "/api/v1/integrations/ip-lookup", map[string]any{"external_service_id": 999}); r.Status != 404 {
		t.Fatalf("servicio sin identidad → 404: %d %s", r.Status, r.Raw)
	}
	// Un token de otro ISP no ve el servicio de A (B no tiene conector ni identidades).
	if r := a.post(tokB, "/api/v1/integrations/ip-lookup", map[string]any{"external_service_id": 812}); r.Status != 404 {
		t.Fatalf("servicio de A con el token de B = %d %s", r.Status, r.Raw)
	}

	// H9: estado de seguridad por lotes, desde detection.customer_security.
	since := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	if _, err := a.admin.Exec(context.Background(), `INSERT INTO detection.customer_security (tenant_id, customer_id, site_id, state,
		open_findings, top_kind, max_severity, confidence, since, updated_at) VALUES ($1, $2, $3, 'infected', 2, 'outbound_scanning',
		'high', 0.9, $4, now())`, A.id, cust, params["site_id"], since); err != nil {
		t.Fatal(err)
	}
	st := a.must(a.post(tokA, "/api/v1/integrations/security-states", map[string]any{"items": []any{
		map[string]any{"external_service_id": 812}, map[string]any{"address": "10.10.0.41"},
		map[string]any{"external_service_id": 999}, map[string]any{"address": "10.10.0.99"}}}), 200)
	items, _ := st.Body["items"].([]any)
	if len(items) != 4 {
		t.Fatalf("security-states = %s", st.Raw)
	}
	bySvc, byIP := items[0].(map[string]any), items[1].(map[string]any)
	if bySvc["state"] != "infected" || bySvc["open_findings"] != float64(2) || bySvc["max_severity"] != "high" ||
		bySvc["address"] != "10.10.0.41" || bySvc["since"] != since.Format("2006-01-02T15:04:05.000Z") {
		t.Fatalf("por servicio = %v", bySvc)
	}
	if byIP["state"] != "infected" || byIP["external_service_id"] != float64(812) {
		t.Fatalf("por IP = %v", byIP)
	}
	for _, i := range []int{2, 3} {
		if items[i].(map[string]any)["error"] != "not_found" {
			t.Fatalf("elemento %d = %v", i, items[i])
		}
	}
	if st.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("respuesta con datos personales: no-store")
	}
	// Con el token de B: la misma IP es el cliente de B (sin estado: limpio) y el servicio de A no existe.
	stB := a.must(a.post(tokB, "/api/v1/integrations/security-states", map[string]any{"items": []any{
		map[string]any{"address": "10.10.0.41"}}}), 200)
	if strings.Contains(string(stB.Raw), "infected") || strings.Contains(string(stB.Raw), `"external_service_id":812`) {
		t.Fatalf("el token de B ve el estado de A: %s", stB.Raw)
	}
	if r := a.post(tokB, "/api/v1/integrations/security-states", map[string]any{"items": []any{
		map[string]any{"external_service_id": 812}}}); r.Status != 422 || r.code() != "VALIDATION_FAILED" {
		t.Fatalf("B sin conector: external_service_id → 422: %d %s", r.Status, r.Raw)
	}
	// Límites del lote y permiso.
	big := make([]any, 501)
	for i := range big {
		big[i] = map[string]any{"address": fmt.Sprintf("10.10.1.%d", i%250)}
	}
	if r := a.post(tokA, "/api/v1/integrations/security-states", map[string]any{"items": big}); r.Status != 422 {
		t.Fatalf("más de 500 → 422: %d", r.Status)
	}
	other := a.must(a.post(A.token, "/api/v1/api-tokens", map[string]any{"name": "solo clientes", "permissions": []string{"customers.read"}}), 201)
	if r := a.post(other.str("token"), "/api/v1/integrations/security-states", map[string]any{"items": []any{
		map[string]any{"address": "10.10.0.41"}}}); r.Status != 403 {
		t.Fatalf("sin security.findings.read → 403: %d %s", r.Status, r.Raw)
	}
	var n int
	if err := a.admin.QueryRow(context.Background(), `SELECT count(*) FROM auth.audit_log WHERE action = 'integrations.security_states'
		AND tenant_id = $1 AND changes->>'via' = 'api_token'`, A.id).Scan(&n); err != nil || n < 1 {
		t.Fatalf("auditoría del estado por lotes: %d %v", n, err)
	}

	// H7: webhook de un servicio con empresa_id → la identidad toma la empresa nueva (el resolutor
	// la confirma con su nombre en la re-resolución).
	fa.mu.Lock()
	fa.company = map[string]any{"id": 3, "nombre": "Fibra Norte", "nit": "7654321"}
	fa.mu.Unlock()
	sec := a.must(a.post(A.token, "/api/v1/integrations/argosisp/webhook-secret", nil), 200)
	path := strings.TrimPrefix(sec.str("webhook_url"), "https://horus.test")
	body, _ := json.Marshal(map[string]any{"id": "evt-emp-1", "evento": "servicio.estado_cambiado", "ocurrido_en": time.Now().UTC().Format(time.RFC3339),
		"payload": map[string]any{"servicio_id": 812, "estado": "activo", "empresa_id": 3}})
	h := http.Header{}
	webhooksig.Sign(h, []byte(sec.str("webhook_secret")), "evt-emp-1", time.Now(), body)
	rq, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, a.srv.URL+path, bytes.NewReader(body))
	for k, v := range h {
		rq.Header[k] = v
	}
	rq.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(rq)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("webhook = %d", res.StatusCode)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r := a.must(a.do(req{Method: "GET", Path: "/api/v1/customers/" + cust + "/external-identity", Token: A.token}), 200)
		cur, _ = r.Body["current"].(map[string]any)
		if cur["company_id"] == float64(3) && cur["company_name"] == "Fibra Norte" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if cur["company_id"] != float64(3) || cur["company_name"] != "Fibra Norte" {
		t.Fatalf("empresa tras el webhook = %v", cur)
	}
}
