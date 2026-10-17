package argos

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeArgos imita la API de ArgosISP: con batch=false el resolvedor por lotes
// no existe (404) y solo están GET /ip/:ip, /clientes/:id y /servicios/:id.
type fakeArgos struct {
	mu    sync.Mutex
	batch bool
	token string
	paths []string
}

func (f *fakeArgos) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/integraciones/horus/resolver":
		if !f.batch {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var in struct {
			IPs []string `json:"ips"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		var out []map[string]any
		for _, ip := range in.IPs {
			switch ip {
			case "100.64.1.20":
				out = append(out, map[string]any{"ip": ip, "servicio_id": 812, "cliente_id": 455, "prefijo": "100.64.1.20/32",
					"cliente":  map[string]any{"nombre": "Ferretería López", "codigo": "C-000455", "tipo": "empresa"},
					"servicio": map[string]any{"user_ppp": "flopez", "plan": "Fibra 100", "estado": "activo", "router_id": 3},
					"empresa":  map[string]any{"id": 2, "nombre": "Fibra Sur", "nit": "1234567K"},
					"desde":    "2026-10-17T08:12:00Z", "campo_nuevo": true})
			case "100.64.1.21": // ArgosISP < 1.2.0: sin el campo empresa
				out = append(out, map[string]any{"ip": ip, "servicio_id": 813, "cliente_id": 456})
			case "100.64.1.22": // servicio sin empresa
				out = append(out, map[string]any{"ip": ip, "servicio_id": 814, "cliente_id": 457, "empresa": nil})
			default:
				out = append(out, map[string]any{"ip": ip, "error": "no_encontrada"})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"resultados": out})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/ip/100.64.1.20":
		_, _ = w.Write([]byte(`{"servicio_id":812,"cliente_id":455,"prefijo":"100.64.1.20/32"}`))
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/ip/"):
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"title":"no encontrado"}`))
	case r.URL.Path == "/api/v1/clientes/455":
		_, _ = w.Write([]byte(`{"id":455,"nombre":"Ferretería López","tipo":"empresa","nit_cui":null}`))
	case r.URL.Path == "/api/v1/servicios/812":
		_, _ = w.Write([]byte(`{"id":812,"cliente_id":455,"plan_id":4,"estado":"suspendido","user_ppp":"flopez"}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func client(srv *httptest.Server, token string) *Client {
	return &Client{BaseURL: srv.URL + "/api/v1", Token: token, HTTP: srv.Client()}
}

func TestResolveBatch(t *testing.T) {
	t.Parallel()
	f := &fakeArgos{batch: true, token: "atk_ok"}
	srv := httptest.NewServer(f)
	defer srv.Close()
	res, mode, err := client(srv, "atk_ok").Resolve(context.Background(), []string{"100.64.1.20", "2800:abcd:1:2::1"})
	if err != nil || mode != ModeBatch {
		t.Fatalf("mode=%s err=%v", mode, err)
	}
	r := res[0].Resolution
	if !r.Found || *r.ExternalServiceID != 812 || *r.ExternalCustomerID != 455 || *r.DisplayName != "Ferretería López" ||
		*r.Code != "C-000455" || *r.ServiceState != "activo" || *r.Plan != "Fibra 100" || r.Since == nil {
		t.Fatalf("resolución = %+v", r)
	}
	if !r.CompanyKnown || *r.CompanyID != 2 || *r.CompanyName != "Fibra Sur" || *r.CompanyNIT != "1234567K" {
		t.Fatalf("empresa (H7) = %+v", r)
	}
	if res[1].Resolution.Found {
		t.Fatal("no_encontrada")
	}
	if len(f.paths) != 1 {
		t.Fatalf("una sola petición por lote: %v", f.paths)
	}
}

// H7: sin el campo empresa (ArgosISP < 1.2.0) la empresa es desconocida (se
// conserva la guardada); con empresa null, el servicio no tiene empresa.
func TestResolveBatchCompanyVariants(t *testing.T) {
	t.Parallel()
	f := &fakeArgos{batch: true, token: "atk_ok"}
	srv := httptest.NewServer(f)
	defer srv.Close()
	res, _, err := client(srv, "atk_ok").Resolve(context.Background(), []string{"100.64.1.21", "100.64.1.22"})
	if err != nil {
		t.Fatal(err)
	}
	if r := res[0].Resolution; !r.Found || r.CompanyKnown || r.CompanyID != nil {
		t.Fatalf("sin campo empresa = %+v", r)
	}
	if r := res[1].Resolution; !r.Found || !r.CompanyKnown || r.CompanyID != nil || r.CompanyName != nil {
		t.Fatalf("empresa null = %+v", r)
	}
}

// A1 aún no existe en ArgosISP: 404 → GET /ip/:ip + /clientes/:id + /servicios/:id.
func TestResolveFallbackOn404(t *testing.T) {
	t.Parallel()
	f := &fakeArgos{token: "atk_ok"}
	srv := httptest.NewServer(f)
	defer srv.Close()
	res, mode, err := client(srv, "atk_ok").Resolve(context.Background(), []string{"100.64.1.20", "100.64.1.99"})
	if err != nil || mode != ModeFallback {
		t.Fatalf("mode=%s err=%v", mode, err)
	}
	r := res[0].Resolution
	if !r.Found || *r.ExternalServiceID != 812 || *r.DisplayName != "Ferretería López" || r.Code != nil || *r.ServiceState != "suspendido" {
		t.Fatalf("resolución = %+v", r)
	}
	if r.CompanyKnown || r.CompanyID != nil {
		t.Fatalf("el respaldo no trae empresa: queda null = %+v", r)
	}
	if res[1].Resolution.Found {
		t.Fatal("la segunda IP no existe")
	}
	want := []string{"POST /api/v1/integraciones/horus/resolver", "GET /api/v1/ip/100.64.1.20", "GET /api/v1/clientes/455",
		"GET /api/v1/servicios/812", "GET /api/v1/ip/100.64.1.99"}
	if strings.Join(f.paths, "|") != strings.Join(want, "|") {
		t.Fatalf("peticiones = %v", f.paths)
	}
}

func TestResolveErrors(t *testing.T) {
	t.Parallel()
	f := &fakeArgos{batch: true, token: "atk_ok"}
	srv := httptest.NewServer(f)
	if _, _, err := client(srv, "atk_mal").Resolve(context.Background(), []string{"100.64.1.20"}); !errors.Is(err, ErrAuth) {
		t.Fatalf("401: %v", err)
	}
	srv.Close()
	_, _, err := client(srv, "atk_ok").Resolve(context.Background(), []string{"100.64.1.20"})
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "100.64.1.20") {
		t.Fatalf("caído: %v", err)
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer down.Close()
	if _, _, err := client(down, "x").Resolve(context.Background(), []string{"100.64.1.20"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("502: %v", err)
	}
}
