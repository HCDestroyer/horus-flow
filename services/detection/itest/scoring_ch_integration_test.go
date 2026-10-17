//go:build integration

package itest

import (
	"context"
	"database/sql"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/chmigrate"
	"github.com/hcdestroyer/horus-flow/packages/go/chtest"
	chreader "github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/clickhouse"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/app"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/engine"
	ingesterschema "github.com/hcdestroyer/horus-flow/services/ingester/api/chschema"
)

const detectionCHPassword = "detection-scoring-test-0123"

func mustExecCH(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// TestScoringClickHouse comprueba las consultas del scoring contra un
// ClickHouse real: características de flows.client_security_1h con la hora
// local del ISP, pares entrantes de flows_raw, clientes de dim.customer, la
// serie diaria detection.customer_scores_1d (escrita por horus_detection) y
// el aislamiento por tenant (row policies).
func TestScoringClickHouse(t *testing.T) {
	srv := chtest.Start(t, func(ctx context.Context, dsn, pw string) error {
		return ingesterschema.MigrateClickHouse(ctx, []string{"HORUS_CLICKHOUSE_DSN=" + dsn, "HORUS_CLICKHOUSE_PASSWORD=" + pw,
			"HORUS_CLICKHOUSE_DETECTION_PASSWORD=" + detectionCHPassword}, slog.New(slog.DiscardHandler))
	})
	admin, err := chmigrate.Open(srv.DSN, srv.Password)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	r, err := chreader.Open(srv.DSN, chreader.UserDetection, detectionCHPassword, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	ctx := context.Background()
	tenant, other, realm, site, cust := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ip := netip.MustParseAddr("10.20.0.10")
	now := time.Now().UTC().Truncate(time.Hour)
	const hours = 72
	mustExecCH(t, admin, `INSERT INTO dim.tenant (tenant_id, name, timezone, version) VALUES (?, 'ISP', 'America/Mexico_City', 1)`, tenant)
	mustExecCH(t, admin, `INSERT INTO dim.customer (tenant_id, realm_id, address, customer_id, site_id, kind, kind_source, status, version)
		VALUES (?, ?, toIPv6('10.20.0.10'), ?, ?, 'residential', 'manual', 'active', 1)`, tenant, realm, cust, site)
	// 72 horas con 2 MB de subida y 3 MB de bajada, 2 puertos de servicio y 5 ASN.
	mustExecCH(t, admin, `INSERT INTO flows.client_security_1h (tenant_id, site_id, realm_id, client_ip, bucket, up_bytes, down_bytes,
			remote_asns_out, inbound_service_ports)
		SELECT ?, ?, ?, toIPv6('10.20.0.10'), toDateTime(?, 'UTC') - toIntervalHour(number + 1), 2000000, 3000000,
			arrayReduce('uniqState', [toUInt32(64500), toUInt32(64501), toUInt32(64502), toUInt32(64503), toUInt32(64504)]),
			arrayReduce('uniqState', [toUInt16(443), toUInt16(8080)])
		FROM numbers(?)`, tenant, site, realm, now, hours)
	p := domain.DefaultScoringParams()
	tz, err := r.Timezone(ctx, tenant)
	if err != nil || tz != "America/Mexico_City" {
		t.Fatalf("timezone = %q, %v", tz, err)
	}
	from, to := now.Add(-p.Window.D()), now
	rows, err := r.ScoringFeatures(ctx, tenant, from, to, tz, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("filas = %d; want 1", len(rows))
	}
	got := rows[0]
	key := engine.ClientKey{Realm: realm, IP: ip}
	loc, _ := time.LoadLocation(tz)
	var business uint64
	for h := 1; h <= hours; h++ {
		if p.IsBusinessHour(now.Add(-time.Duration(h) * time.Hour).In(loc)) {
			business += 5000000
		}
	}
	if got.Key != key || got.Site != site || got.UpBytes != hours*2000000 || got.DownBytes != hours*3000000 ||
		got.ActiveHours != hours || got.InboundPorts != 2 || got.RemoteASNs != 5 || got.BusinessBytes != business {
		t.Fatalf("características = %+v; want negocio %d", got, business)
	}
	// Con el suelo por encima de 5 MB/h ninguna hora es activa.
	p.ActiveHourBytes = 10_000_000
	if rows, _ := r.ScoringFeatures(ctx, tenant, from, to, tz, p); len(rows) != 1 || rows[0].ActiveHours != 0 {
		t.Errorf("suelo de actividad: %+v", rows)
	}
	// Conexiones entrantes a 443 desde 7 IPs distintas (SYN+ACK de subida). Van después de comprobar
	// las características: las MV de flows_raw también alimentan client_security_1h.
	mustExecCH(t, admin, `INSERT INTO flows.flows_raw (tenant_id, ts, flow_start, received_at, site_id, realm_id, attribution_status,
			direction, client_ip, client_port, remote_ip, remote_port, protocol, tcp_flags, bytes, packets, sampling_rate, merged_flows)
		SELECT ?, now64(3) - toIntervalHour(2), now64(3) - toIntervalHour(2), now64(3), ?, ?, 'attributed', 'upload',
			toIPv6('10.20.0.10'), 443, toIPv6(concat('198.51.100.', toString(number + 1))), 50000 + number, 6, 18, 1000, 4, 1, 1
		FROM numbers(7)`, tenant, site, realm)

	peers, err := r.InboundPeers(ctx, tenant, from, time.Now().UTC(), []engine.ClientKey{key})
	if err != nil || peers[key] != 7 {
		t.Fatalf("pares entrantes = %v, %v", peers, err)
	}
	custs, err := r.ScoringCustomers(ctx, tenant)
	if err != nil || custs[key].ID != cust || custs[key].KindSource != "manual" {
		t.Fatalf("clientes = %v, %v", custs, err)
	}
	day := now.Truncate(24 * time.Hour)
	rec := app.ScoreRecord{CustomerID: cust, Key: key, Site: site, Day: day, Model: domain.ScoringModel, CommercialScore: 87, RawScore: 87,
		Confidence: 0.87, KindOnDay: "residential", SuggestedKind: "commercial", Level: "commercial",
		Reasons: []string{domain.FeatInboundServices}, ComputedAt: time.Now().UTC()}
	if err := r.InsertScores(ctx, tenant, []app.ScoreRecord{rec}); err != nil {
		t.Fatal(err)
	}
	rec.CommercialScore, rec.ComputedAt = 90, rec.ComputedAt.Add(time.Second) // mismo día: sustituye
	if err := r.InsertScores(ctx, tenant, []app.ScoreRecord{rec}); err != nil {
		t.Fatal(err)
	}
	series, err := r.ScoreSeries(ctx, tenant, realm, ip, day.AddDate(0, 0, -90))
	if err != nil || len(series) != 1 || series[0].CommercialScore != 90 || series[0].Level != "commercial" {
		t.Fatalf("serie = %+v, %v", series, err)
	}
	// Otro tenant no ve nada (filtro explícito + row policy).
	if rows, err := r.ScoringFeatures(ctx, other, from, to, "UTC", p); err != nil || len(rows) != 0 {
		t.Errorf("otro tenant ve características: %v, %v", rows, err)
	}
	if s, err := r.ScoreSeries(ctx, other, realm, ip, day.AddDate(0, 0, -90)); err != nil || len(s) != 0 {
		t.Errorf("otro tenant ve la serie: %v, %v", s, err)
	}
}
