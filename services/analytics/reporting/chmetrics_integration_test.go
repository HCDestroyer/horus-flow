//go:build integration

package reporting

import (
	"context"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/chmigrate"
	"github.com/hcdestroyer/horus-flow/packages/go/chtest"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/chread"
	ingesterschema "github.com/hcdestroyer/horus-flow/services/ingester/api/chschema"
	snmpapi "github.com/hcdestroyer/horus-flow/services/snmp/api"
)

const chAnalyticsPassword = "reporting-analytics-test-0123"

// Disponibilidad (snmp.device_1h) y tráfico (flows.site_1h) del reporte
// sobre un ClickHouse real con las migraciones de ingester y snmp, leídos
// como horus_analytics (row policy por tenant).
func TestCHMetrics(t *testing.T) {
	srv := chtest.Start(t, func(ctx context.Context, dsn, pw string) error {
		return ingesterschema.MigrateClickHouse(ctx, []string{"HORUS_CLICKHOUSE_DSN=" + dsn, "HORUS_CLICKHOUSE_PASSWORD=" + pw,
			"HORUS_CLICKHOUSE_ANALYTICS_PASSWORD=" + chAnalyticsPassword}, slog.New(slog.DiscardHandler))
	})
	ctx := context.Background()
	mdb, err := chmigrate.Open(srv.DSN, srv.Password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chmigrate.Up(ctx, mdb, snmpapi.ClickHouseMigrations(), chmigrate.Options{VersionTable: snmpapi.ClickHouseVersionTable,
		BootstrapDatabase: snmpapi.ClickHouseBootstrapDatabase}); err != nil {
		t.Skipf("migración ClickHouse de snmp no aplicable en este servidor: %v", err)
	}
	_ = mdb.Close()

	opts, err := ch.ParseDSN(srv.DSN)
	if err != nil {
		t.Fatal(err)
	}
	opts.Auth.Password = srv.Password
	conn, err := ch.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	tenant, other := uuid.New(), uuid.New()
	site, rtA, rtB := uuid.New(), uuid.New(), uuid.New()
	loc, _ := time.LoadLocation("America/Mexico_City")
	p := LastWeek(time.Date(2026, 10, 12, 14, 0, 0, 0, time.UTC), loc) // 05/10 06:00 UTC → 12/10 06:00 UTC

	exec := func(q string, args ...any) {
		t.Helper()
		if err := conn.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO dim.site (tenant_id, site_id, name, parent_id, timezone, version) VALUES (?, ?, 'Nodo Centro', ?, 'UTC', 1)`, tenant, site, uuid.Nil)
	exec(`INSERT INTO dim.router (tenant_id, router_id, site_id, name, is_primary, version) VALUES (?, ?, ?, 'rt-centro', 1, 1), (?, ?, ?, 'rt-sur', 0, 1)`,
		tenant, rtA, site, tenant, rtB, site)

	// rt-centro: 10 sondeos en el periodo, 9 ICMP correctos; uno fuera del periodo (no cuenta). Otro tenant: invisible.
	b, err := conn.PrepareBatch(ctx, `INSERT INTO snmp.device_metrics (tenant_id, site_id, router_id, ts, poll_duration_ms, icmp_ok, snmp_ok)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		ok := uint8(1)
		if i == 0 {
			ok = 0
		}
		_ = b.Append(tenant, site, rtA, p.From.Add(time.Duration(i)*time.Hour), uint32(100), ok, uint8(1))
	}
	_ = b.Append(tenant, site, rtA, p.From.Add(-time.Hour), uint32(100), uint8(0), uint8(0))
	_ = b.Append(other, site, rtA, p.From.Add(time.Hour), uint32(100), uint8(0), uint8(0))
	if err := b.Send(); err != nil {
		t.Fatal(err)
	}

	// Tráfico: bajada 5 GB el martes (local) y 1 GB la semana anterior; subida 2 GB.
	fb, err := conn.PrepareBatch(ctx, `INSERT INTO flows.flows_raw (tenant_id, ts, flow_start, received_at, site_id, router_id,
		realm_id, attribution_status, direction, client_ip, remote_ip, remote_port, protocol, bytes, packets, sampling_rate,
		merged_flows, flow_source, remote_asn, service_id, category_id, batch_id)`)
	if err != nil {
		t.Fatal(err)
	}
	add := func(tn uuid.UUID, ts time.Time, dir string, bytes uint64) {
		c := netip.AddrFrom16(netip.MustParseAddr("100.64.0.10").As16())
		r := netip.AddrFrom16(netip.MustParseAddr("192.0.2.1").As16())
		if err := fb.Append(tn, ts, ts, ts, site, rtA, uuid.New(), "attributed", dir, c, r, uint16(443), uint8(6), bytes, uint64(10),
			uint32(1), uint16(1), "ipfix", uint32(64500), uuid.Nil, uuid.Nil, uuid.New()); err != nil {
			t.Fatal(err)
		}
	}
	tuesday := time.Date(2026, 10, 6, 12, 0, 0, 0, loc)
	add(tenant, tuesday, "download", 5_000_000_000)
	add(tenant, tuesday, "upload", 2_000_000_000)
	add(tenant, tuesday.AddDate(0, 0, -7), "download", 1_000_000_000)
	add(other, tuesday, "download", 9_000_000_000)
	if err := fb.Send(); err != nil {
		t.Fatal(err)
	}

	r, err := chread.Open(srv.DSN, chread.UserAnalytics, chAnalyticsPassword, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	m := CHMetrics{Q: r}
	av, err := m.Availability(ctx, tenant, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(av.Routers) != 2 || av.Routers[0].Name != "rt-centro" || av.Routers[0].Site != "Nodo Centro" || av.Routers[0].Samples != 10 ||
		av.Routers[0].ICMPRatio == nil || *av.Routers[0].ICMPRatio != 0.9 || av.Routers[1].Name != "rt-sur" || av.Routers[1].ICMPRatio != nil {
		t.Fatalf("disponibilidad = %+v", av.Routers)
	}
	if av.Overall == nil || *av.Overall != 0.9 {
		t.Fatalf("media = %v", av.Overall)
	}
	tr, err := m.Traffic(ctx, tenant, p)
	if err != nil {
		t.Fatal(err)
	}
	if tr.DownBytes != 5_000_000_000 || tr.UpBytes != 2_000_000_000 || tr.PrevDownBytes != 1_000_000_000 || len(tr.Daily) != 7 {
		t.Fatalf("tráfico = %+v", tr)
	}
	if !tr.Daily[1].HasSamples || tr.Daily[1].DownBytes != 5_000_000_000 || tr.Daily[0].HasSamples || tr.Daily[1].Day.Format("02/01") != "06/10" {
		t.Fatalf("diario = %+v", tr.Daily)
	}
}
