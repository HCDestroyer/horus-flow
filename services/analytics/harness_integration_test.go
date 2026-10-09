//go:build integration

package analytics_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/chtest"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/services/analytics"
	"github.com/hcdestroyer/horus-flow/services/ingester"
)

const analyticsPassword = "analytics-test-password-0123"

// row es una fila mínima de flows_raw para los tests.
type row struct {
	ts                time.Time
	site, realm       uuid.UUID
	status, direction string
	client, remote    netip.Addr
	bytes, packets    uint64
	service, category uuid.UUID
	asn               uint32
	remotePort        uint16
	proto             uint8
}

type env struct {
	t        *testing.T
	srv      chtest.Server
	tenant   uuid.UUID
	site     uuid.UUID
	router   uuid.UUID
	realm    uuid.UUID
	handler  http.Handler
	signer   *authz.Signer
	services *module.Services
	mod      module.Module
}

func setup(t *testing.T, extra flowinv.Data, environ ...string) *env {
	t.Helper()
	srv := chtest.Start(t, func(ctx context.Context, dsn, pw string) error {
		return ingester.MigrateClickHouse(ctx, []string{"HORUS_CLICKHOUSE_DSN=" + dsn, "HORUS_CLICKHOUSE_PASSWORD=" + pw,
			"HORUS_CLICKHOUSE_ANALYTICS_PASSWORD=" + analyticsPassword}, slog.New(slog.DiscardHandler))
	})
	e := &env{t: t, srv: srv, tenant: uuid.New(), site: uuid.New(), router: uuid.New(), realm: uuid.New()}
	d := flowinv.Data{
		Exporters: []flowinv.Exporter{{TenantID: e.tenant, RouterID: e.router, SiteID: e.site, Name: "rt-centro",
			TunnelIP: netip.MustParseAddr("10.255.9.1")}},
		Realms: []flowinv.Realm{{ID: e.realm, TenantID: e.tenant, Kind: flowinv.RealmNodePrivate, SiteID: e.site, Name: "Centro"}},
	}
	d.Exporters = append(d.Exporters, extra.Exporters...)
	d.Prefixes = append(d.Prefixes, extra.Prefixes...)
	d.Realms = append(d.Realms, extra.Realms...)
	b, _ := json.Marshal(d)
	inv := filepath.Join(t.TempDir(), "inv.json")
	if err := os.WriteFile(inv, b, 0o600); err != nil {
		t.Fatal(err)
	}
	key, _ := authz.GenerateKey()
	e.signer = authz.NewSigner(key, "horus-auth", time.Hour, nil)
	ks := authz.KeySet{}
	ks.Add(e.signer.PublicKey())
	e.services = module.NewServices()
	_ = e.services.Provide("auth.Verifier", authz.NewVerifier(ks, "horus-auth", nil))
	mux := httpx.NewMux(nil, slog.New(slog.DiscardHandler))
	ctx := context.Background()
	mod, err := analytics.Register(ctx, module.Deps{Logger: slog.New(slog.DiscardHandler), Common: config.Common{Env: "dev"},
		Routes: mux.ForService("analytics"), Services: e.services, Environ: append([]string{
			"HORUS_CLICKHOUSE_DSN=" + srv.DSN, "HORUS_CLICKHOUSE_PASSWORD=" + srv.Password,
			"HORUS_CLICKHOUSE_ANALYTICS_PASSWORD=" + analyticsPassword, "HORUS_FLOWS_INVENTORY_FILE=" + inv,
		}, environ...)})
	if err != nil {
		t.Fatal(err)
	}
	if err := mod.(module.Starter).Start(ctx); err != nil {
		t.Fatal(err)
	}
	e.mod = mod
	e.handler = mux.Handler()
	return e
}

func (e *env) insert(rows []row) {
	e.t.Helper()
	opts, err := ch.ParseDSN(e.srv.DSN)
	if err != nil {
		e.t.Fatal(err)
	}
	opts.Auth.Password = e.srv.Password
	conn, err := ch.Open(opts)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx := context.Background()
	b, err := conn.PrepareBatch(ctx, `INSERT INTO flows.flows_raw (tenant_id, ts, flow_start, received_at, site_id, router_id,
		realm_id, attribution_status, direction, client_ip, remote_ip, remote_port, protocol, bytes, packets, sampling_rate,
		merged_flows, flow_source, remote_asn, service_id, category_id, batch_id)`)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, r := range rows {
		site := r.site
		if site == uuid.Nil {
			site = e.site
		}
		client, remote := r.client, r.remote
		if !client.IsValid() {
			client = netip.IPv6Unspecified()
		}
		if !remote.IsValid() {
			remote = netip.MustParseAddr("8.8.8.8")
		}
		if err := b.Append(e.tenant, r.ts, r.ts, r.ts, site, e.router, r.realm, r.status, r.direction,
			netip.AddrFrom16(client.As16()), netip.AddrFrom16(remote.As16()), r.remotePort, r.proto, r.bytes, r.packets,
			uint32(1), uint16(1), "ipfix", r.asn, r.service, r.category, uuid.New()); err != nil {
			e.t.Fatal(err)
		}
	}
	if err := b.Send(); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) token(perms map[string][]string, scope string) string {
	e.t.Helper()
	typ := authz.TypeUser
	sub := uuid.NewString()
	if scope == authz.ScopeKiosk {
		typ = authz.TypeKiosk
	}
	tok, _, err := e.signer.Sign(authz.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: sub},
		Typ: typ, Scope: scope, TID: e.tenant.String(), Perms: perms})
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

func (e *env) get(path, tok string) (int, map[string]any) {
	e.t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	e.handler.ServeHTTP(w, r)
	body, _ := io.ReadAll(w.Result().Body)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	return w.Code, out
}

var all = []string{"*"}
