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

// fakeArgos es una ArgosISP de prueba sin el resolvedor por lotes (A1
// pendiente): solo GET /ip/:ip, /clientes/:id y /servicios/:id.
type fakeArgos struct {
	mu      sync.Mutex
	service int
	name    string
	estado  string
}

func (f *fakeArgos) set(service int, name, estado string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.service, f.name, f.estado = service, name, estado
}

func (f *fakeArgos) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer atk_prueba_1234" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/v1/ip/10.10.0.41":
		_, _ = fmt.Fprintf(w, `{"servicio_id":%d,"cliente_id":%d,"prefijo":"10.10.0.41/32"}`, f.service, f.service+1000)
	case strings.HasPrefix(r.URL.Path, "/api/v1/clientes/"):
		_, _ = fmt.Fprintf(w, `{"id":%d,"nombre":%q,"tipo":"empresa"}`, f.service+1000, f.name)
	case strings.HasPrefix(r.URL.Path, "/api/v1/servicios/"):
		_, _ = fmt.Fprintf(w, `{"id":%d,"estado":%q,"user_ppp":"u%d"}`, f.service, f.estado, f.service)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// H6 de extremo a extremo: conector con respaldo GET /ip/:ip, alias de
// ArgosISP sin pisar el manual, vigencias al cambiar de dueño por webhook
// firmado (con deduplicación), degradación a stale con ArgosISP caído.
func TestArgosConnectorIdentity(t *testing.T) {
	t.Parallel()
	a := startAppEnv(t, "HORUS_INTEGRATIONS_ALLOW_PRIVATE=true", "HORUS_PUBLIC_BASE_URL=https://horus.test", "HORUS_INTEGRATIONS_SWEEP_EVERY=1h")
	pt := a.superadmin().platformToken()
	A := a.newISP(pt, "argos-a")
	params := a.fixtureISP(A, "10.10.0.0/24")
	cust := params["customer_id"]
	fa := &fakeArgos{}
	fa.set(812, "Ferretería López", "activo")
	srv := httptest.NewServer(fa)
	defer srv.Close()

	conn := a.must(a.do(req{Method: "PUT", Path: "/api/v1/integrations/argosisp", Token: A.token,
		Body: map[string]any{"base_url": srv.URL + "/api/v1", "enabled": true, "token": "atk_prueba_1234"}}), 200)
	if conn.Body["has_token"] != true || strings.Contains(string(conn.Raw), "atk_prueba") || conn.Body["has_webhook_secret"] != true {
		t.Fatalf("conector = %s", conn.Raw)
	}
	test := a.must(a.post(A.token, "/api/v1/integrations/argosisp/connection-test", nil), 200)
	if test.Body["ok"] != true || test.Body["resolver"] != "fallback" {
		t.Fatalf("prueba = %s", test.Raw)
	}

	// A demanda: identidad vigente y alias de ArgosISP.
	id := a.must(a.post(A.token, "/api/v1/customers/"+cust+"/external-identity/refresh", nil), 200)
	cur, _ := id.Body["current"].(map[string]any)
	if cur["display_name"] != "Ferretería López" || cur["service_id"] != float64(812) || cur["stale"] != false {
		t.Fatalf("identidad = %s", id.Raw)
	}
	c := a.must(a.do(req{Method: "GET", Path: "/api/v1/customers/" + cust, Token: A.token}), 200)
	if c.Body["alias"] != "Ferretería López" || c.Body["alias_source"] != "argosisp" {
		t.Fatalf("alias = %s", c.Raw)
	}
	st := a.must(a.do(req{Method: "GET", Path: "/api/v1/integrations/argosisp", Token: A.token}), 200)
	if st.Body["resolver"] != "fallback" || st.Body["last_sync_status"] != "degraded" || st.Body["identities"] != float64(1) {
		t.Fatalf("estado del conector = %s", st.Raw)
	}

	// Webhook firmado: la IP pasa al servicio 901 → se cierra la vigencia anterior.
	sec := a.must(a.post(A.token, "/api/v1/integrations/argosisp/webhook-secret", nil), 200)
	hookURL, secret := sec.str("webhook_url"), sec.str("webhook_secret")
	path := strings.TrimPrefix(hookURL, "https://horus.test")
	fa.set(901, "Ana Pérez", "activo")
	body, _ := json.Marshal(map[string]any{"id": "evt-1", "evento": "sesion.iniciada", "ocurrido_en": time.Now().UTC().Format(time.RFC3339),
		"payload": map[string]any{"servicio_id": 901, "ipv4": "10.10.0.41"}})
	send := func(sig http.Header) int {
		r, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, a.srv.URL+path, bytes.NewReader(body))
		for k, v := range sig {
			r.Header[k] = v
		}
		r.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	h := http.Header{}
	webhooksig.Sign(h, []byte(secret), "evt-1", time.Now(), body)
	if s := send(h); s != http.StatusAccepted {
		t.Fatalf("webhook = %d", s)
	}
	if s := send(h); s != http.StatusAccepted {
		t.Fatalf("duplicado = %d", s)
	}
	bad := http.Header{}
	webhooksig.Sign(bad, []byte("otro-secreto-cualquiera"), "evt-2", time.Now(), body)
	if s := send(bad); s != http.StatusUnauthorized {
		t.Fatalf("firma inválida = %d", s)
	}
	var hist []any
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r := a.must(a.do(req{Method: "GET", Path: "/api/v1/customers/" + cust + "/external-identity", Token: A.token}), 200)
		hist, _ = r.Body["history"].([]any)
		if len(hist) == 2 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if len(hist) != 2 {
		t.Fatalf("vigencias tras el webhook = %v", hist)
	}
	newest, oldest := hist[0].(map[string]any), hist[1].(map[string]any)
	if newest["external_service_id"] != float64(901) || newest["valid_to"] != nil || oldest["external_service_id"] != float64(812) || oldest["valid_to"] == nil {
		t.Fatalf("vigencias = %v", hist)
	}
	var inbox int
	if err := a.admin.QueryRow(context.Background(), `SELECT count(*) FROM devices.argosisp_webhook_inbox WHERE webhook_id = 'evt-1'`).Scan(&inbox); err != nil || inbox != 1 {
		t.Fatalf("deduplicación: %d %v", inbox, err)
	}

	// Un alias puesto a mano nunca lo pisa ArgosISP.
	c = a.must(a.do(req{Method: "GET", Path: "/api/v1/customers/" + cust, Token: A.token}), 200)
	ver := fmt.Sprintf(`"%v"`, c.Body["version"])
	a.must(a.do(req{Method: "PATCH", Path: "/api/v1/customers/" + cust, Token: A.token, Body: map[string]any{"alias": "Puesto a mano"},
		Header: map[string]string{"If-Match": ver, "Content-Type": "application/merge-patch+json"}}), 200)
	a.must(a.post(A.token, "/api/v1/customers/"+cust+"/external-identity/refresh", nil), 200)
	c = a.must(a.do(req{Method: "GET", Path: "/api/v1/customers/" + cust, Token: A.token}), 200)
	if c.Body["alias"] != "Puesto a mano" || c.Body["alias_source"] != "manual" {
		t.Fatalf("alias manual pisado: %s", c.Raw)
	}

	// ArgosISP caído: se conserva la última identidad marcada stale (sin error).
	srv.Close()
	id = a.must(a.post(A.token, "/api/v1/customers/"+cust+"/external-identity/refresh", nil), 200)
	cur, _ = id.Body["current"].(map[string]any)
	if cur["stale"] != true || cur["display_name"] != "Ana Pérez" || id.Body["last_check_status"] != "error" {
		t.Fatalf("degradación = %s", id.Raw)
	}
	st = a.must(a.do(req{Method: "GET", Path: "/api/v1/integrations/argosisp", Token: A.token}), 200)
	if st.Body["last_sync_status"] != "failing" || st.Body["stale_identities"] != float64(1) {
		t.Fatalf("estado tras la caída = %s", st.Raw)
	}
	// La consulta 360 muestra la identidad (stale) y dice qué bloques no están disponibles en este proceso.
	look := a.must(a.post(A.token, "/api/v1/integrations/ip-lookup", map[string]any{"address": "10.10.0.41"}), 200)
	ext, _ := look.Body["external"].(map[string]any)
	if ext["stale"] != true || ext["service_id"] != float64(901) || look.Body["consumption"] != nil {
		t.Fatalf("ip-lookup = %s", look.Raw)
	}
	// La IP fue del servicio 812 antes del cambio: el instante anterior al cambio resuelve al dueño anterior.
	at := oldest["valid_from"].(string)
	past := a.must(a.post(A.token, "/api/v1/integrations/ip-lookup", map[string]any{"address": "10.10.0.41", "at": at, "include": []string{}}), 200)
	if pe, _ := past.Body["external"].(map[string]any); pe["service_id"] != float64(812) {
		t.Fatalf("ip-lookup en el pasado = %s", past.Raw)
	}
	// Purga con el cliente.
	if _, err := a.admin.Exec(context.Background(), `DELETE FROM devices.customer WHERE id = $1`, cust); err != nil {
		t.Fatal(err)
	}
	var left int
	_ = a.admin.QueryRow(context.Background(), `SELECT count(*) FROM devices.customer_external_identity WHERE customer_id = $1`, cust).Scan(&left)
	if left != 0 {
		t.Fatalf("la identidad debe purgarse con el cliente: %d", left)
	}
}
