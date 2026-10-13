package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/chread"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/app"
)

// emptyRows no devuelve filas (ClickHouse sin muestras).
type emptyRows struct{ chread.Rows }

func (emptyRows) Next() bool   { return false }
func (emptyRows) Close() error { return nil }
func (emptyRows) Err() error   { return nil }

type emptyQ struct{ err error }

func (q emptyQ) Query(context.Context, uuid.UUID, string, ...any) (chread.Rows, context.CancelFunc, error) {
	if q.err != nil {
		return nil, nil, q.err
	}
	return emptyRows{}, func() {}, nil
}

type routerEnv struct {
	t            *testing.T
	h            http.Handler
	signer       *authz.Signer
	tenant       uuid.UUID
	site, router uuid.UUID
	q            *emptyQ
}

func newRouterEnv(t *testing.T) *routerEnv {
	t.Helper()
	e := &routerEnv{t: t, tenant: uuid.New(), site: uuid.New(), router: uuid.New(), q: &emptyQ{}}
	inv, err := flowinv.New(flowinv.Data{Exporters: []flowinv.Exporter{{TenantID: e.tenant, RouterID: e.router, SiteID: e.site,
		Name: "rt-centro", TunnelIP: netip.MustParseAddr("10.255.0.1")}}})
	if err != nil {
		t.Fatal(err)
	}
	key, _ := authz.GenerateKey()
	e.signer = authz.NewSigner(key, "horus-auth", time.Hour, nil)
	ks := authz.KeySet{}
	ks.Add(e.signer.PublicKey())
	mux := httpx.NewMux(nil, slog.New(slog.DiscardHandler))
	New(authz.NewGuard(authz.NewVerifier(ks, "horus-auth", nil)), &app.Service{Q: e.q, Inv: flowinv.NewStore(inv)}).Mount(mux.ForService("analytics"))
	e.h = mux.Handler()
	return e
}

func (e *routerEnv) token(tenant uuid.UUID, perms map[string][]string, kiosk bool) string {
	e.t.Helper()
	c := authz.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: uuid.NewString()},
		Typ: authz.TypeUser, Scope: authz.ScopeTenant, TID: tenant.String(), Perms: perms}
	if kiosk {
		c.Typ, c.Scope, c.KioskID = authz.TypeKiosk, authz.ScopeKiosk, uuid.NewString()
	}
	tok, _, err := e.signer.Sign(c)
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

func (e *routerEnv) get(path, tok string) (int, map[string]any) {
	e.t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	body, _ := io.ReadAll(w.Result().Body)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	return w.Code, out
}

// Series de routers: snmp.read (kiosco permitido, sin permisos en el token),
// 404 para un router de otro ISP o fuera del alcance por nodo, 422 con
// interface_id inválido o más de 8, meta.source snmp, 503 sin ClickHouse.
func TestRouterSeriesRoutes(t *testing.T) {
	e := newRouterEnv(t)
	read := map[string][]string{PermSNMPRead: {"*"}}
	base := "/api/v1/analytics/routers/" + e.router.String()

	code, body := e.get(base+"/metrics?range=1h&metrics=cpu_percent,availability_ratio", e.token(e.tenant, read, false))
	if code != 200 {
		t.Fatalf("metrics = %d %v", code, body)
	}
	meta := body["meta"].(map[string]any)
	if meta["source"] != "snmp" || meta["step"] != 60.0 || meta["coverage"] != 0.0 || meta["partial"] != true {
		t.Fatalf("meta %v", meta)
	}
	series := body["data"].(map[string]any)["series"].([]any)
	if len(series) != 2 || series[1].(map[string]any)["unit"] != "ratio" {
		t.Fatalf("series %v", series)
	}
	if pts := series[0].(map[string]any)["points"].([]any); len(pts) < 55 || pts[0].([]any)[1] != nil {
		t.Fatalf("points %v", pts)
	}

	// Kiosco: permitido sin permisos en el token.
	if code, body = e.get(base+"/metrics", e.token(e.tenant, nil, true)); code != 200 {
		t.Fatalf("kiosk = %d %v", code, body)
	}
	// Usuario sin snmp.read: 403.
	if code, _ = e.get(base+"/metrics", e.token(e.tenant, map[string][]string{"traffic.read": {"*"}}, false)); code != 403 {
		t.Fatalf("without snmp.read = %d", code)
	}
	// Otro ISP: 404 (sin distinguir de inexistente).
	if code, _ = e.get(base+"/metrics", e.token(uuid.New(), read, false)); code != 404 {
		t.Fatalf("other tenant = %d", code)
	}
	// Alcance por nodo: su nodo sí, otro nodo 404.
	if code, _ = e.get(base+"/metrics", e.token(e.tenant, map[string][]string{PermSNMPRead: {"site:" + e.site.String()}}, false)); code != 200 {
		t.Fatalf("site scope = %d", code)
	}
	if code, _ = e.get(base+"/metrics", e.token(e.tenant, map[string][]string{PermSNMPRead: {"site:" + uuid.NewString()}}, false)); code != 404 {
		t.Fatalf("foreign site scope = %d", code)
	}
	// Métrica desconocida y step inválido: 422.
	if code, _ = e.get(base+"/metrics?metrics=rx_bps", e.token(e.tenant, read, false)); code != 422 {
		t.Fatalf("bad metric = %d", code)
	}
	if code, _ = e.get(base+"/metrics?step=5", e.token(e.tenant, read, false)); code != 422 {
		t.Fatalf("bad step = %d", code)
	}

	// Interfaces.
	ids := make([]string, 0, app.MaxRouterInterfaces+1)
	for range app.MaxRouterInterfaces + 1 {
		ids = append(ids, uuid.NewString())
	}
	code, body = e.get(base+"/interfaces/metrics?interface_id="+strings.Join(ids[:2], ","), e.token(e.tenant, read, false))
	if code != 200 || len(body["data"].(map[string]any)["series"].([]any)) != 4 {
		t.Fatalf("interfaces = %d %v", code, body)
	}
	if code, _ = e.get(base+"/interfaces/metrics?interface_id="+strings.Join(ids, ","), e.token(e.tenant, read, false)); code != 422 {
		t.Fatalf("9 interfaces = %d", code)
	}
	if code, _ = e.get(base+"/interfaces/metrics?interface_id=ether1", e.token(e.tenant, read, false)); code != 422 {
		t.Fatalf("bad interface_id = %d", code)
	}
	if code, body = e.get(base+"/interfaces/metrics", e.token(e.tenant, nil, true)); code != 200 || len(body["data"].(map[string]any)["series"].([]any)) != 0 {
		t.Fatalf("kiosk interfaces = %d %v", code, body)
	}

	// ClickHouse caído: 503.
	e.q.err = chread.ErrUnavailable
	if code, _ = e.get(base+"/metrics", e.token(e.tenant, read, false)); code != 503 {
		t.Fatalf("unavailable = %d", code)
	}
}
