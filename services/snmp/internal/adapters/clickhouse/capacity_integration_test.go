//go:build integration

package clickhouse_test

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
	ingesterschema "github.com/hcdestroyer/horus-flow/services/ingester/api/chschema"
	chadapter "github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/clickhouse"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

const capSNMPPassword = "snmp-capacity-password-1"

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// TestCapacitySeries comprueba contra un ClickHouse real (flows + snmp
// migrados) las series del análisis de capacidad con el usuario horus_snmp:
// intervalos de 5 min de interfaz, sondas (crudo → 5 min → 1 h), picos
// diarios sustituibles (ReplacingMergeTree), volumen remoto por país del ASN
// (flows.site_1d + diccionarios) y el aislamiento por tenant (p_tenant).
func TestCapacitySeries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	srv := chtest.Start(t, func(ctx context.Context, dsn, pw string) error {
		if err := ingesterschema.MigrateClickHouse(ctx, []string{"HORUS_CLICKHOUSE_DSN=" + dsn, "HORUS_CLICKHOUSE_PASSWORD=" + pw},
			slog.New(slog.DiscardHandler)); err != nil {
			return err
		}
		return chadapter.Migrate(ctx, dsn, pw, capSNMPPassword, nil)
	})
	admin, err := chmigrate.Open(srv.DSN, srv.Password)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	w, err := chadapter.Open(srv.DSN, chadapter.UserSNMP, capSNMPPassword, 4, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close(ctx) }()
	c, err := chadapter.OpenCapacity(srv.DSN, chadapter.UserSNMP, capSNMPPassword)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	tenantA, tenantB, site, router := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	iface := uuid.New()
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(5 * time.Minute)
	speed := uint64(1_000_000_000)
	b := chadapter.Batch{ID: "cap:" + uuid.NewString()}
	for i := range 4 {
		rx, tx, pps := 400e6+float64(i)*100e6, 40e6, 1000.0
		drops := uint64(2)
		b.Interfaces = append(b.Interfaces, chadapter.InterfaceRow{TenantID: tenantA, SiteID: site, RouterID: router, InterfaceID: iface, IfIndex: 1,
			Name: "sfp1", TS: base.Add(time.Duration(i) * 5 * time.Minute), OperUp: true, SpeedBps: &speed, RxBps: &rx, TxBps: &tx, RxPps: &pps,
			TxPps: &pps, RxDrops: &drops})
	}
	if err := w.Insert(ctx, b); err != nil {
		t.Fatal(err)
	}
	buckets, meta, err := c.Buckets(ctx, tenantA, []uuid.UUID{iface}, base, base.Add(time.Hour))
	if err != nil || len(buckets[iface]) != 4 || meta[iface].SpeedBps != 1e9 || meta[iface].SiteID != site || meta[iface].Name != "sfp1" {
		t.Fatalf("intervalos: %v %d %+v", err, len(buckets[iface]), meta[iface])
	}
	if got := *buckets[iface][3].RxBps; got != 700e6 || buckets[iface][3].Drops != 2 {
		t.Fatalf("intervalo 4: %v drops %d", got, buckets[iface][3].Drops)
	}
	if other, _, err := c.Buckets(ctx, tenantB, []uuid.UUID{iface}, base, base.Add(time.Hour)); err != nil || len(other) != 0 {
		t.Fatalf("B ve los intervalos de A: %v %d", err, len(other))
	}
	if _, _, err := c.Buckets(ctx, uuid.Nil, []uuid.UUID{iface}, base, base.Add(time.Hour)); err == nil {
		t.Fatal("consulta sin tenant aceptada")
	}

	// Sondas: crudo → probe_5m → probe_1h.
	probe := domain.ProbeID(router, "198.51.100.1", "gw", "sfp1")
	var samples []domain.ProbeSample
	for i := range 3 {
		loss, rtt, jit := float64(i), 10.0+float64(i), 1.0
		up := true
		samples = append(samples, domain.ProbeSample{TenantID: tenantA, SiteID: site, RouterID: router, InterfaceID: iface, ProbeID: probe,
			Source: domain.ProbeSourceRouter, Ref: domain.ProbeRefGateway, Target: "198.51.100.1", Label: "Puerta de enlace por sfp1",
			At: base.Add(time.Duration(i) * time.Minute), Up: &up, LossPct: &loss, RTTAvgMs: &rtt, JitterMs: &jit})
	}
	if err := c.InsertProbes(ctx, samples); err != nil {
		t.Fatal(err)
	}
	sum, err := c.ProbeSummaries(ctx, tenantA, base.Add(-time.Hour))
	if err != nil || len(sum) != 1 || sum[0].InterfaceID != iface || *sum[0].LossPct != 1 || *sum[0].LossPctMax != 2 || *sum[0].RTTAvgMs != 11 ||
		*sum[0].UpRatio != 1 || sum[0].Source != domain.ProbeSourceRouter || sum[0].Label != "Puerta de enlace por sfp1" {
		t.Fatalf("resumen de sondas: %v %+v", err, sum)
	}
	series, err := c.ProbeSeries(ctx, tenantA, []uuid.UUID{probe}, base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil || len(series[probe]) != 1 || *series[probe][0].RTTAvgMs != 11 {
		t.Fatalf("serie de sondas: %v %+v", err, series)
	}
	if s, err := c.ProbeSummaries(ctx, tenantB, base.Add(-time.Hour)); err != nil || len(s) != 0 {
		t.Fatalf("B ve las sondas de A: %v %d", err, len(s))
	}

	// Picos diarios: recalcular un día lo sustituye.
	day := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	row := chadapter.DailyRow{SubjectID: iface, SubjectKind: domain.SubjectUplink, RouterID: router, SiteID: site,
		Stats: domain.DailyStats{Day: day, Samples: 288, RxP95Bps: 500e6, CapacityBps: 1e9, HoursAboveWarning: 1.5}}
	if err := c.InsertDaily(ctx, tenantA, []chadapter.DailyRow{row}, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	row.Stats.RxP95Bps = 650e6
	if err := c.InsertDaily(ctx, tenantA, []chadapter.DailyRow{row}, time.Now()); err != nil {
		t.Fatal(err)
	}
	daily, err := c.DailySeries(ctx, tenantA, []uuid.UUID{iface}, day.AddDate(0, 0, -1), day)
	if err != nil || len(daily[iface]) != 1 || daily[iface][0].RxP95Bps != 650e6 || daily[iface][0].HoursAboveWarning != 1.5 ||
		!daily[iface][0].Day.Equal(day) {
		t.Fatalf("serie diaria: %v %+v", err, daily)
	}
	last, err := c.LastDaily(ctx, tenantA)
	if err != nil || !last[iface].Equal(day) {
		t.Fatalf("último día: %v %v", err, last)
	}
	if other, err := c.DailySeries(ctx, tenantB, []uuid.UUID{iface}, day.AddDate(0, 0, -1), day); err != nil || len(other) != 0 {
		t.Fatalf("B ve la serie de A: %v %d", err, len(other))
	}

	// Volumen remoto por país del ASN (diccionarios de flows).
	orgGT, orgUS := uuid.New(), uuid.New()
	mustExec(t, admin, `INSERT INTO dim.organization (org_id, name, country, catalog_version) VALUES (?, 'Tigo', 'GT', 1), (?, 'Cloudflare', 'US', 1)`, orgGT, orgUS)
	mustExec(t, admin, `INSERT INTO dim.asn (asn, org_id, name, catalog_version) VALUES (14754, ?, 'Tigo', 1), (13335, ?, 'Cloudflare', 1),
		(15169, ?, 'Google', 1)`, orgGT, orgUS, orgUS)
	mustExec(t, admin, `SYSTEM RELOAD DICTIONARY dim.asn_dict`)
	mustExec(t, admin, `SYSTEM RELOAD DICTIONARY dim.organization_dict`)
	mustExec(t, admin, `INSERT INTO flows.site_1d (tenant_id, site_id, bucket, service_id, remote_asn, direction, bytes, packets, flows, clients)
		SELECT ?, ?, toDateTime(?, 'UTC'), toUUID('00000000-0000-0000-0000-000000000000'), asn, dir, b, 1, 1,
			arrayReduce('uniqState', [toUUID('00000000-0000-0000-0000-000000000000')], [toIPv6('::')])
		FROM (SELECT arrayJoin([(toUInt32(14754), 'download', toUInt64(200)), (toUInt32(13335), 'download', toUInt64(700)),
			(toUInt32(15169), 'download', toUInt64(100)), (toUInt32(0), 'upload', toUInt64(50))]) AS t,
			t.1 AS asn, t.2 AS dir, t.3 AS b)`, tenantA, site, day)
	vols, err := c.TrafficByCountry(ctx, tenantA, nil, []uint32{15169}, day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	var split domain.TrafficSplit
	for _, v := range vols {
		split.Add(v, "GT")
	}
	split.Finish()
	if split.InternationalBytes != 700 || split.LocalByCountry != 200 || split.LocalByList != 100 || split.UnknownBytes != 50 {
		t.Fatalf("reparto: %+v (%+v)", split, vols)
	}
	if v, err := c.TrafficByCountry(ctx, tenantA, []uuid.UUID{uuid.New()}, nil, day, day.AddDate(0, 0, 1)); err != nil || len(v) != 0 {
		t.Fatalf("filtro por nodo: %v %d", err, len(v))
	}
	if v, err := c.TrafficByCountry(ctx, tenantB, nil, nil, day, day.AddDate(0, 0, 1)); err != nil || len(v) != 0 {
		t.Fatalf("B ve el tráfico de A: %v %d", err, len(v))
	}

	// Lista local por prefijos: 300 de los 700 de Cloudflare van a una caché on-net (198.51.100.0/24, valor de
	// documentación) y 40 a la LAN IPv6 de un IXP; el resto no está en la lista.
	mustExec(t, admin, `INSERT INTO flows.site_remote_net_1d (tenant_id, site_id, bucket, remote_net, remote_asn, direction, bytes)
		SELECT ?, ?, toDate(?), toIPv6(net), asn, dir, b
		FROM (SELECT arrayJoin([('::ffff:198.51.100.0', toUInt32(13335), 'download', toUInt64(300)),
			('::ffff:203.0.113.0', toUInt32(13335), 'download', toUInt64(400)),
			('2001:db8:7a00::', toUInt32(0), 'upload', toUInt64(40))]) AS t, t.1 AS net, t.2 AS asn, t.3 AS dir, t.4 AS b)`,
		tenantA, site, day)
	local := []netip.Prefix{netip.MustParsePrefix("198.51.100.64/26"), netip.MustParsePrefix("2001:db8:7a00::/48")}
	moved, err := c.TrafficByLocalPrefix(ctx, tenantA, nil, []uint32{15169}, local, day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range moved {
		split.MoveToPrefix(v, "GT")
	}
	split.Finish()
	if split.InternationalBytes != 400 || split.LocalByPrefix != 340 || split.UnknownBytes != 10 || split.LocalBytes != 640 {
		t.Fatalf("reparto con prefijos locales: %+v (%+v)", split, moved)
	}
	if v, err := c.TrafficByLocalPrefix(ctx, tenantB, nil, nil, local, day, day.AddDate(0, 0, 1)); err != nil || len(v) != 0 {
		t.Fatalf("B ve el tráfico de A por prefijo: %v %d", err, len(v))
	}
}
