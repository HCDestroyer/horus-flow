//go:build integration

package tenancy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

// I1-28 por la API: el router (fixtures REST de 7.12 detrás de un servidor
// TLS que falla ante cualquier escritura) se lee con el usuario de solo
// lectura generado por el script de alta; la propuesta no aplica nada y el
// lote confirmado crea solo lo elegido y publica client_prefix.created.
func TestPrefixImportE2E(t *testing.T) {
	t.Parallel()
	var writes, gets atomic.Int32
	var user atomic.Value
	router := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		gets.Add(1)
		u, p, _ := r.BasicAuth()
		user.Store(u + ":" + p)
		name := strings.ReplaceAll(strings.TrimPrefix(r.URL.Path, "/rest/"), "/", "_") + ".json"
		b, err := os.ReadFile(filepath.Join("..", "..", "services", "devices", "internal", "adapters", "routeros", "testdata", "7.12", name))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write(b)
	}))
	defer router.Close()
	a := startAppEnv(t, "HORUS_DEVICES_ROUTEROS_BASE_URL="+router.URL)
	pt := a.superadmin().platformToken()
	A := a.newISP(pt, "isp-a")
	ctx := context.Background()

	site := a.must(a.post(A.token, "/api/v1/sites", map[string]any{"name": "Nodo"}), 201).str("id")
	rt := a.must(a.post(A.token, "/api/v1/routers", map[string]any{"site_id": site, "name": "rt-1", "routeros_version": "7.12"}), 201).str("id")
	preview := "/api/v1/routers/" + rt + "/prefix-import-preview"
	// Sin túnel todavía → 502 ROUTER_UNREACHABLE sin tocar el router.
	if r := a.post(A.token, preview, nil); r.Status != 502 || r.code() != "ROUTER_UNREACHABLE" {
		t.Fatalf("sin túnel: %d %s", r.Status, r.Raw)
	}
	// El script de alta asigna el túnel y genera la credencial de solo lectura.
	script := string(a.must(a.do(req{Method: "POST", Path: "/api/v1/routers/" + rt + "/provisioning-script", Token: A.token,
		Header: map[string]string{"Idempotency-Key": uuid.NewString()}}), 201).Raw)
	a.must(a.post(A.token, "/api/v1/sites/"+site+"/client-prefixes", map[string]any{"prefix": "10.20.0.0/24", "role": "customers"}), 201)

	pv := a.must(a.post(A.token, preview, nil), 200)
	items := pv.Body["items"].([]any)
	if pv.str("routeros_version") != "7.12" || len(pv.str("tls_fingerprint_sha256")) != 95 || len(items) < 5 {
		t.Fatalf("propuesta = %s", pv.Raw)
	}
	byPrefix := map[string]map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		byPrefix[m["prefix"].(string)] = m
	}
	if byPrefix["10.20.0.0/24"]["diff"] != "exists" || byPrefix["100.64.0.0/20"]["suggested_role"] != "customers" ||
		byPrefix["2001:db8:1000::/40"]["suggested_ipv6_client_len"].(float64) != 56 || byPrefix["2001:db8:1000::/40"]["ipv6_pool_usage"] != "dhcpv6_pd" {
		t.Fatalf("items = %v", byPrefix)
	}
	if writes.Load() != 0 {
		t.Fatal("escritura hacia el router")
	}
	creds, _ := user.Load().(string)
	if !strings.HasPrefix(creds, "horus:") || !strings.Contains(script, `password="`+strings.TrimPrefix(creds, "horus:")+`"`) {
		t.Fatal("no se usó el usuario de solo lectura del script")
	}
	if a.auditCount("devices.router.tls_fingerprint_pinned") != 1 {
		t.Fatal("primer contacto TLS no auditado")
	}
	// Nada aplicado: el nodo sigue con 1 prefijo.
	list := a.must(a.do(req{Method: "GET", Path: "/api/v1/sites/" + site + "/client-prefixes", Token: A.token}), 200)
	if len(list.Body["data"].([]any)) != 1 {
		t.Fatal("la propuesta aplicó cambios")
	}
	// Huella fijada: otra lectura funciona; una huella "aceptada" distinta de la real → 409.
	a.must(a.post(A.token, preview, nil), 200)
	if r := a.post(A.token, preview, map[string]any{"accept_new_tls_fingerprint": strings.Repeat("AB:", 31) + "AB"}); r.Status != 409 ||
		r.code() != "ROUTER_TLS_FINGERPRINT_CHANGED" {
		t.Fatalf("huella distinta: %d %s", r.Status, r.Raw)
	}

	// Lote: solo lo elegido, todo o nada.
	batch := "/api/v1/sites/" + site + "/client-prefixes/batch"
	idem := map[string]string{"Idempotency-Key": uuid.NewString()}
	if r := a.do(req{Method: "POST", Path: batch, Token: A.token, Header: idem, Body: map[string]any{"items": []any{
		map[string]any{"prefix": "100.64.0.0/20", "role": "customers", "source": "routeros_api"},
		map[string]any{"prefix": "10.20.0.0/25", "role": "customers", "source": "routeros_api"}, // solapa con el existente
	}}}); r.Status != 409 || r.code() != "CLIENT_PREFIX_OVERLAP" {
		t.Fatalf("lote con solape: %d %s", r.Status, r.Raw)
	}
	created := a.must(a.do(req{Method: "POST", Path: batch, Token: A.token, Header: map[string]string{"Idempotency-Key": uuid.NewString()},
		Body: map[string]any{"items": []any{
			map[string]any{"prefix": "100.64.0.0/20", "role": "customers", "assignment_mode": "dynamic", "source": "routeros_api"},
			map[string]any{"prefix": "2001:db8:1000::/40", "role": "customers", "ipv6_client_len": 56, "source": "routeros_api"},
		}}}), 201)
	data := created.Body["data"].([]any)
	if len(data) != 2 || data[0].(map[string]any)["source"] != "routeros_api" || data[1].(map[string]any)["ipv6_client_len"].(float64) != 56 {
		t.Fatalf("lote = %s", created.Raw)
	}
	if r := a.do(req{Method: "POST", Path: batch, Token: A.token, Header: map[string]string{"Idempotency-Key": uuid.NewString()},
		Body: map[string]any{"items": []any{map[string]any{"prefix": "10.0.0.1/8", "role": "x"}}}}); r.Status != 422 {
		t.Fatalf("lote inválido: %d %s", r.Status, r.Raw)
	}
	var evs int
	_ = a.admin.QueryRow(ctx, `SELECT count(*) FROM devices.outbox WHERE subject LIKE 'horus.devices.client_prefix.created.%'`).Scan(&evs)
	if evs != 3 { // 1 manual + 2 importados
		t.Fatalf("eventos client_prefix.created = %d", evs)
	}
}
