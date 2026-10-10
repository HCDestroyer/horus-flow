package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	collectorapi "github.com/hcdestroyer/horus-flow/services/collector/api"
)

type memStates map[uuid.UUID]*collectorapi.FlowExporter

func (m memStates) Get(_ context.Context, id uuid.UUID) (*collectorapi.FlowExporter, error) {
	return m[id], nil
}

var (
	tenantA = uuid.MustParse("0192e000-0000-7000-8000-000000000001")
	tenantB = uuid.MustParse("0192e000-0000-7000-8000-000000000002")
	routerA = uuid.MustParse("0192e333-0000-7000-8000-000000000033")
	routerB = uuid.MustParse("0192e333-0000-7000-8000-000000000034")
)

func setup(t *testing.T) (http.Handler, *authz.Signer) {
	t.Helper()
	key, err := authz.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := authz.NewSigner(key, "horus-auth", time.Minute, nil)
	ks := authz.KeySet{}
	ks.Add(signer.PublicKey())
	inv, err := flowinv.New(flowinv.Data{Exporters: []flowinv.Exporter{
		{TenantID: tenantA, RouterID: routerA, SiteID: uuid.New(), TunnelIP: netip.MustParseAddr("10.255.0.1")},
		{TenantID: tenantB, RouterID: routerB, SiteID: uuid.New(), TunnelIP: netip.MustParseAddr("10.255.0.2")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	fps := 152.0
	states := memStates{routerA: {TenantID: tenantA, RouterID: routerA, Version: 3, State: collectorapi.StateExporting,
		StateSince: now, LastFlowAt: &now, FlowsPerSecond: &fps, DroppedRecordsQuota1h: "0", SequenceGaps: 80}}
	mux := httpx.NewMux(nil, slog.New(slog.DiscardHandler))
	New(authz.NewGuard(authz.NewVerifier(ks, "horus-auth", nil)), flowinv.NewStore(inv), states).Mount(mux.ForService("ingester"))
	return mux.Handler(), signer
}

func token(t *testing.T, s *authz.Signer, tenant uuid.UUID, perms map[string][]string) string {
	t.Helper()
	tok, _, err := s.Sign(authz.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: uuid.NewString()},
		Typ: authz.TypeUser, Scope: authz.ScopeTenant, TID: tenant.String(), Perms: perms})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func do(h http.Handler, path, tok string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestFlowExportersAPI(t *testing.T) {
	h, s := setup(t)
	read := map[string][]string{PermFlowsRead: {"*"}}
	w := do(h, "/api/v1/flow-exporters", token(t, s, tenantA, read))
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Data) != 1 {
		t.Fatalf("list body %s", w.Body)
	}
	if body.Data[0]["state"] != "exporting" || body.Data[0]["sequence_gaps"] != nil || body.Data[0]["flows_per_second"] != 152.0 {
		t.Fatalf("exporter %v", body.Data[0])
	}
	// Router sin estado del collector ⇒ pending_configuration.
	w = do(h, "/api/v1/flow-exporters/"+routerB.String(), token(t, s, tenantB, read))
	if w.Code != http.StatusOK || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("get = %d", w.Code)
	}
	var one map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &one)
	if one["state"] != "pending_configuration" {
		t.Fatalf("pending: %v", one)
	}
	// Router de otro ISP ⇒ 404; sin permiso ⇒ 403; sin token ⇒ 401.
	if w := do(h, "/api/v1/flow-exporters/"+routerB.String(), token(t, s, tenantA, read)); w.Code != http.StatusNotFound {
		t.Fatalf("other tenant = %d", w.Code)
	}
	if w := do(h, "/api/v1/flow-exporters", token(t, s, tenantA, nil)); w.Code != http.StatusForbidden {
		t.Fatalf("no perm = %d", w.Code)
	}
	if w := do(h, "/api/v1/flow-exporters", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d", w.Code)
	}
}

func kioskToken(t *testing.T, s *authz.Signer, tenant uuid.UUID) string {
	t.Helper()
	kiosk := uuid.NewString()
	tok, _, err := s.Sign(authz.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "kiosk:" + kiosk},
		Typ: authz.TypeKiosk, Scope: authz.ScopeKiosk, TID: tenant.String(), KioskID: kiosk})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// El widget de exportadores del kiosco usa GET /flow-exporters con el JWT de
// kiosco (sin permisos; lista blanca de permissions.yaml §kiosk).
func TestFlowExportersKiosk(t *testing.T) {
	h, s := setup(t)
	w := do(h, "/api/v1/flow-exporters", kioskToken(t, s, tenantA))
	if w.Code != http.StatusOK {
		t.Fatalf("kiosk list = %d %s", w.Code, w.Body)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Data) != 1 || body.Data[0]["router_id"] != routerA.String() {
		t.Fatalf("kiosk list body %s", w.Body)
	}
	// El detalle no está en la lista blanca del kiosco.
	if w := do(h, "/api/v1/flow-exporters/"+routerA.String(), kioskToken(t, s, tenantA)); w.Code != http.StatusForbidden {
		t.Fatalf("kiosk get = %d", w.Code)
	}
	// Un token de usuario sin flows.read sigue sin ver nada.
	noRead := map[string][]string{"customers.read": {"*"}}
	if w := do(h, "/api/v1/flow-exporters", token(t, s, tenantA, noRead)); w.Code != http.StatusForbidden {
		t.Fatalf("user without flows.read = %d", w.Code)
	}
}
