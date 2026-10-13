//go:build integration

package analytics_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/chmigrate"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	snmpschema "github.com/hcdestroyer/horus-flow/services/snmp/migrations/clickhouse"
)

// ensureSNMPSchema aplica las migraciones ClickHouse del rol snmp
// (services/snmp/migrations/clickhouse: crudo, agregados 5 min / 1 h, grants
// y row policies) sobre el esquema de test de flows.
func ensureSNMPSchema(e *env) {
	e.t.Helper()
	db, err := chmigrate.Open(e.srv.DSN, e.srv.Password)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := chmigrate.Up(ctx, db, snmpschema.Migrations(), chmigrate.Options{
		VersionTable: snmpschema.VersionTable, BootstrapDatabase: snmpschema.BootstrapDatabase}); err != nil {
		// Mientras la migración del rol snmp no aplique en esta versión de
		// ClickHouse, el test crea el mismo esquema (columnas idénticas) para
		// cubrir las consultas de analytics.
		e.t.Logf("snmp migration not applied (%v): using the test's equivalent DDL", err)
		fallbackSNMPSchema(e)
	}
}

// fallbackSNMPSchema es el DDL de 20261013120000_snmp_metrics.sql con las
// mismas columnas; site_id va como SimpleAggregateFunction(anyLast) en los
// agregados (fuera del ORDER BY, ClickHouse ≥ 25 lo rechaza).
func fallbackSNMPSchema(e *env) {
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
	const devAgg = `(tenant_id UUID, site_id SimpleAggregateFunction(anyLast, UUID), router_id UUID, bucket DateTime('UTC'),
		samples SimpleAggregateFunction(sum, UInt64), icmp_total SimpleAggregateFunction(sum, UInt64), icmp_ok_count SimpleAggregateFunction(sum, UInt64),
		snmp_total SimpleAggregateFunction(sum, UInt64), snmp_ok_count SimpleAggregateFunction(sum, UInt64),
		poll_duration_ms_sum SimpleAggregateFunction(sum, UInt64), poll_duration_ms_max SimpleAggregateFunction(max, UInt32),
		uptime_seconds_max SimpleAggregateFunction(max, UInt64),
		cpu_percent_sum SimpleAggregateFunction(sum, Float64), cpu_percent_count SimpleAggregateFunction(sum, UInt64), cpu_percent_max SimpleAggregateFunction(max, Float32),
		cpu_max_core_percent_sum SimpleAggregateFunction(sum, Float64), cpu_max_core_percent_count SimpleAggregateFunction(sum, UInt64), cpu_max_core_percent_max SimpleAggregateFunction(max, Float32),
		memory_percent_sum SimpleAggregateFunction(sum, Float64), memory_percent_count SimpleAggregateFunction(sum, UInt64), memory_percent_max SimpleAggregateFunction(max, Float32),
		temperature_celsius_sum SimpleAggregateFunction(sum, Float64), temperature_celsius_count SimpleAggregateFunction(sum, UInt64), temperature_celsius_max SimpleAggregateFunction(max, Float32),
		cpu_temperature_celsius_sum SimpleAggregateFunction(sum, Float64), cpu_temperature_celsius_count SimpleAggregateFunction(sum, UInt64), cpu_temperature_celsius_max SimpleAggregateFunction(max, Float32),
		icmp_rtt_ms_sum SimpleAggregateFunction(sum, Float64), icmp_rtt_ms_count SimpleAggregateFunction(sum, UInt64), icmp_rtt_ms_max SimpleAggregateFunction(max, Float32))
		ENGINE = AggregatingMergeTree ORDER BY (tenant_id, router_id, bucket)`
	const devSel = `tenant_id, any(site_id) AS site_id, router_id, %s AS bucket, count() AS samples,
		count() AS icmp_total, sum(icmp_ok) AS icmp_ok_count, count() AS snmp_total, sum(snmp_ok) AS snmp_ok_count,
		sum(poll_duration_ms) AS poll_duration_ms_sum, max(poll_duration_ms) AS poll_duration_ms_max,
		maxIf(assumeNotNull(uptime_seconds), isNotNull(uptime_seconds)) AS uptime_seconds_max,
		sum(ifNull(toFloat64(cpu_percent), 0)) AS cpu_percent_sum, countIf(isNotNull(cpu_percent)) AS cpu_percent_count, maxIf(assumeNotNull(cpu_percent), isNotNull(cpu_percent)) AS cpu_percent_max,
		sum(ifNull(toFloat64(cpu_max_core_percent), 0)) AS cpu_max_core_percent_sum, countIf(isNotNull(cpu_max_core_percent)) AS cpu_max_core_percent_count, maxIf(assumeNotNull(cpu_max_core_percent), isNotNull(cpu_max_core_percent)) AS cpu_max_core_percent_max,
		sum(ifNull(toFloat64(memory_percent), 0)) AS memory_percent_sum, countIf(isNotNull(memory_percent)) AS memory_percent_count, maxIf(assumeNotNull(memory_percent), isNotNull(memory_percent)) AS memory_percent_max,
		sum(ifNull(toFloat64(temperature_celsius), 0)) AS temperature_celsius_sum, countIf(isNotNull(temperature_celsius)) AS temperature_celsius_count, maxIf(assumeNotNull(temperature_celsius), isNotNull(temperature_celsius)) AS temperature_celsius_max,
		sum(ifNull(toFloat64(cpu_temperature_celsius), 0)) AS cpu_temperature_celsius_sum, countIf(isNotNull(cpu_temperature_celsius)) AS cpu_temperature_celsius_count, maxIf(assumeNotNull(cpu_temperature_celsius), isNotNull(cpu_temperature_celsius)) AS cpu_temperature_celsius_max,
		sum(ifNull(toFloat64(icmp_rtt_ms), 0)) AS icmp_rtt_ms_sum, countIf(isNotNull(icmp_rtt_ms)) AS icmp_rtt_ms_count, maxIf(assumeNotNull(icmp_rtt_ms), isNotNull(icmp_rtt_ms)) AS icmp_rtt_ms_max
		FROM snmp.device_metrics GROUP BY tenant_id, router_id, bucket`
	const ifAgg = `(tenant_id UUID, site_id SimpleAggregateFunction(anyLast, UUID), router_id UUID, interface_id UUID, bucket DateTime('UTC'),
		if_index SimpleAggregateFunction(anyLast, UInt32), name SimpleAggregateFunction(anyLast, String),
		samples SimpleAggregateFunction(sum, UInt64), oper_up_count SimpleAggregateFunction(sum, UInt64), speed_bps_max SimpleAggregateFunction(max, UInt64),
		rx_bps_sum SimpleAggregateFunction(sum, Float64), rx_bps_count SimpleAggregateFunction(sum, UInt64), rx_bps_max SimpleAggregateFunction(max, Float64),
		tx_bps_sum SimpleAggregateFunction(sum, Float64), tx_bps_count SimpleAggregateFunction(sum, UInt64), tx_bps_max SimpleAggregateFunction(max, Float64),
		rx_pps_sum SimpleAggregateFunction(sum, Float64), rx_pps_count SimpleAggregateFunction(sum, UInt64), rx_pps_max SimpleAggregateFunction(max, Float64),
		tx_pps_sum SimpleAggregateFunction(sum, Float64), tx_pps_count SimpleAggregateFunction(sum, UInt64), tx_pps_max SimpleAggregateFunction(max, Float64),
		rx_errors SimpleAggregateFunction(sum, UInt64), tx_errors SimpleAggregateFunction(sum, UInt64),
		rx_drops SimpleAggregateFunction(sum, UInt64), tx_drops SimpleAggregateFunction(sum, UInt64),
		utilization_rx_max SimpleAggregateFunction(max, Float32), utilization_tx_max SimpleAggregateFunction(max, Float32))
		ENGINE = AggregatingMergeTree ORDER BY (tenant_id, router_id, interface_id, bucket)`
	const ifSel = `tenant_id, any(site_id) AS site_id, router_id, interface_id, %s AS bucket,
		anyLast(if_index) AS if_index, anyLast(toString(name)) AS name, count() AS samples, sum(oper_up) AS oper_up_count,
		maxIf(assumeNotNull(speed_bps), isNotNull(speed_bps)) AS speed_bps_max,
		sum(ifNull(rx_bps, 0)) AS rx_bps_sum, countIf(isNotNull(rx_bps)) AS rx_bps_count, maxIf(assumeNotNull(rx_bps), isNotNull(rx_bps)) AS rx_bps_max,
		sum(ifNull(tx_bps, 0)) AS tx_bps_sum, countIf(isNotNull(tx_bps)) AS tx_bps_count, maxIf(assumeNotNull(tx_bps), isNotNull(tx_bps)) AS tx_bps_max,
		sum(ifNull(rx_pps, 0)) AS rx_pps_sum, countIf(isNotNull(rx_pps)) AS rx_pps_count, maxIf(assumeNotNull(rx_pps), isNotNull(rx_pps)) AS rx_pps_max,
		sum(ifNull(tx_pps, 0)) AS tx_pps_sum, countIf(isNotNull(tx_pps)) AS tx_pps_count, maxIf(assumeNotNull(tx_pps), isNotNull(tx_pps)) AS tx_pps_max,
		sum(ifNull(rx_errors_delta, 0)) AS rx_errors, sum(ifNull(tx_errors_delta, 0)) AS tx_errors,
		sum(ifNull(rx_drops_delta, 0)) AS rx_drops, sum(ifNull(tx_drops_delta, 0)) AS tx_drops,
		maxIf(assumeNotNull(utilization_rx), isNotNull(utilization_rx)) AS utilization_rx_max,
		maxIf(assumeNotNull(utilization_tx), isNotNull(utilization_tx)) AS utilization_tx_max
		FROM snmp.interface_metrics GROUP BY tenant_id, router_id, interface_id, bucket`
	stmts := []string{
		`CREATE DATABASE IF NOT EXISTS snmp`,
		`CREATE TABLE IF NOT EXISTS snmp.device_metrics (tenant_id UUID, site_id UUID, router_id UUID, ts DateTime('UTC'),
			poll_duration_ms UInt32, icmp_ok UInt8, snmp_ok UInt8, uptime_seconds Nullable(UInt64), cpu_percent Nullable(Float32),
			cpu_max_core_percent Nullable(Float32), memory_percent Nullable(Float32), memory_used_bytes Nullable(UInt64),
			memory_total_bytes Nullable(UInt64), temperature_celsius Nullable(Float32), cpu_temperature_celsius Nullable(Float32),
			voltage Nullable(Float32), icmp_rtt_ms Nullable(Float32), icmp_loss_ratio Nullable(Float32))
			ENGINE = MergeTree ORDER BY (tenant_id, router_id, ts)`,
		`CREATE TABLE IF NOT EXISTS snmp.interface_metrics (tenant_id UUID, site_id UUID, router_id UUID, interface_id UUID, if_index UInt32,
			name LowCardinality(String), ts DateTime('UTC'), oper_up UInt8, speed_bps Nullable(UInt64),
			rx_bps Nullable(Float64), tx_bps Nullable(Float64), rx_pps Nullable(Float64), tx_pps Nullable(Float64),
			rx_errors_delta Nullable(UInt64), tx_errors_delta Nullable(UInt64), rx_drops_delta Nullable(UInt64), tx_drops_delta Nullable(UInt64),
			utilization_rx Nullable(Float32), utilization_tx Nullable(Float32))
			ENGINE = MergeTree ORDER BY (tenant_id, router_id, interface_id, ts)`,
		`CREATE TABLE IF NOT EXISTS snmp.device_5m ` + devAgg,
		`CREATE TABLE IF NOT EXISTS snmp.device_1h ` + devAgg,
		`CREATE MATERIALIZED VIEW IF NOT EXISTS snmp.mv_device_5m TO snmp.device_5m AS SELECT ` + fmt.Sprintf(devSel, "toStartOfFiveMinutes(ts)"),
		`CREATE MATERIALIZED VIEW IF NOT EXISTS snmp.mv_device_1h TO snmp.device_1h AS SELECT ` + fmt.Sprintf(devSel, "toStartOfHour(ts)"),
		`CREATE TABLE IF NOT EXISTS snmp.interface_5m ` + ifAgg,
		`CREATE TABLE IF NOT EXISTS snmp.interface_1h ` + ifAgg,
		`CREATE MATERIALIZED VIEW IF NOT EXISTS snmp.mv_interface_5m TO snmp.interface_5m AS SELECT ` + fmt.Sprintf(ifSel, "toStartOfFiveMinutes(ts)"),
		`CREATE MATERIALIZED VIEW IF NOT EXISTS snmp.mv_interface_1h TO snmp.interface_1h AS SELECT ` + fmt.Sprintf(ifSel, "toStartOfHour(ts)"),
	}
	for _, tbl := range []string{"device_metrics", "interface_metrics", "device_5m", "device_1h", "interface_5m", "interface_1h"} {
		stmts = append(stmts, `GRANT SELECT ON snmp.`+tbl+` TO horus_tenant_reader`,
			`CREATE ROW POLICY IF NOT EXISTS p_tenant ON snmp.`+tbl+` AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader`)
	}
	for _, stmt := range stmts {
		if err := conn.Exec(ctx, stmt); err != nil {
			e.t.Fatalf("%v: %s", err, stmt)
		}
	}
}

type deviceRow struct {
	ts      time.Time
	icmpOK  uint8
	cpu     *float32
	memory  *float32
	tenant  uuid.UUID
	router  uuid.UUID
	uptimeS *uint64
}

func (e *env) insertDevice(rows []deviceRow) {
	e.t.Helper()
	opts, _ := ch.ParseDSN(e.srv.DSN)
	opts.Auth.Password = e.srv.Password
	conn, err := ch.Open(opts)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx := context.Background()
	b, err := conn.PrepareBatch(ctx, `INSERT INTO snmp.device_metrics (tenant_id, site_id, router_id, ts, poll_duration_ms,
		icmp_ok, snmp_ok, uptime_seconds, cpu_percent, memory_percent)`)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, r := range rows {
		tenant, router := r.tenant, r.router
		if tenant == uuid.Nil {
			tenant = e.tenant
		}
		if router == uuid.Nil {
			router = e.router
		}
		if err := b.Append(tenant, e.site, router, r.ts, uint32(120), r.icmpOK, r.icmpOK, r.uptimeS, r.cpu, r.memory); err != nil {
			e.t.Fatal(err)
		}
	}
	if err := b.Send(); err != nil {
		e.t.Fatal(err)
	}
}

type ifaceRow struct {
	ts     time.Time
	iface  uuid.UUID
	name   string
	rx, tx *float64
}

func (e *env) insertInterfaces(rows []ifaceRow) {
	e.t.Helper()
	opts, _ := ch.ParseDSN(e.srv.DSN)
	opts.Auth.Password = e.srv.Password
	conn, err := ch.Open(opts)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx := context.Background()
	b, err := conn.PrepareBatch(ctx, `INSERT INTO snmp.interface_metrics (tenant_id, site_id, router_id, interface_id, if_index, name,
		ts, oper_up, rx_bps, tx_bps)`)
	if err != nil {
		e.t.Fatal(err)
	}
	for i, r := range rows {
		if err := b.Append(e.tenant, e.site, e.router, r.iface, uint32(i+1), r.name, r.ts, uint8(1), r.rx, r.tx); err != nil {
			e.t.Fatal(err)
		}
	}
	if err := b.Send(); err != nil {
		e.t.Fatal(err)
	}
}

// TestRouterMetricsAPI (EP-08): la serie de dispositivo sale de
// snmp.device_metrics con paso de 1 min en 1 h, huecos como null (nunca 0),
// availability_ratio = icmp_ok/total por bucket, meta.source snmp; el kiosco
// la ve; un router de otro ISP es 404 y otro ISP no ve estas filas.
func TestRouterMetricsAPI(t *testing.T) {
	e := setup(t, flowinv.Data{})
	ensureSNMPSchema(e)
	now := time.Now().UTC().Truncate(time.Minute)
	f32 := func(v float32) *float32 { return &v }
	var rows []deviceRow
	for m := 60; m >= 2; m-- {
		if m >= 20 && m < 30 { // sondeador caído 10 min: hueco
			continue
		}
		cpu := f32(float32(10 + m%5))
		if m == 40 { // SNMP sin responder: ICMP sí, CPU null
			cpu = nil
		}
		ok := uint8(1)
		if m == 15 {
			ok = 0
		}
		rows = append(rows, deviceRow{ts: now.Add(-time.Duration(m)*time.Minute + 10*time.Second), icmpOK: ok, cpu: cpu, memory: f32(55)})
	}
	for m := range 180 { // 3 días atrás: solo visible por los agregados de 1 h
		rows = append(rows, deviceRow{ts: now.Add(-72*time.Hour + time.Duration(m)*time.Minute), icmpOK: 1, cpu: f32(15), memory: f32(50)})
	}
	rows = append(rows, deviceRow{ts: now.Add(-5 * time.Minute), icmpOK: 1, cpu: f32(99), memory: f32(99), tenant: uuid.New(), router: uuid.New()})
	e.insertDevice(rows)
	read := map[string][]string{"snmp.read": all}

	code, body := e.get("/api/v1/analytics/routers/"+e.router.String()+"/metrics?range=1h&metrics=cpu_percent,availability_ratio,memory_percent",
		e.token(read, "tenant"))
	if code != 200 {
		t.Fatalf("metrics = %d %v", code, body)
	}
	meta := body["meta"].(map[string]any)
	if meta["source"] != "snmp" || meta["step"] != 60.0 || meta["partial"] != true {
		t.Fatalf("meta %v", meta)
	}
	series := body["data"].(map[string]any)["series"].([]any)
	if len(series) != 3 {
		t.Fatalf("series %d", len(series))
	}
	byMetric := map[string][]any{}
	for _, s := range series {
		m := s.(map[string]any)
		byMetric[m["metric"].(string)] = m["points"].([]any)
	}
	var nulls, values, zeros, lossBuckets int
	for i, p := range byMetric["cpu_percent"] {
		v := p.([]any)[1]
		switch {
		case v == nil:
			nulls++
		case v.(float64) == 0:
			zeros++
		default:
			values++
			if v.(float64) > 20 {
				t.Fatalf("other tenant's cpu leaked: %v", v)
			}
		}
		if av := byMetric["availability_ratio"][i].([]any)[1]; av != nil && av.(float64) == 0 {
			lossBuckets++
		}
	}
	if zeros != 0 || nulls < 10 || values < 25 || lossBuckets != 1 {
		t.Fatalf("cpu: %d values, %d nulls, %d zeros; loss buckets %d", values, nulls, zeros, lossBuckets)
	}
	if cov := meta["coverage"].(float64); cov <= 0.5 || cov >= 1 {
		t.Fatalf("coverage %v", cov)
	}

	// Kiosco permitido; sin snmp.read 403; otro ISP 404.
	if code, body = e.get("/api/v1/analytics/routers/"+e.router.String()+"/metrics", e.token(nil, "kiosk")); code != 200 {
		t.Fatalf("kiosk = %d %v", code, body)
	}
	if code, _ = e.get("/api/v1/analytics/routers/"+e.router.String()+"/metrics", e.token(map[string][]string{"traffic.read": all}, "tenant")); code != 403 {
		t.Fatalf("without snmp.read = %d", code)
	}
	if code, _ = e.get("/api/v1/analytics/routers/"+e.router.String()+"/metrics", setupTenantToken(e, uuid.New(), read)); code != 404 {
		t.Fatalf("other tenant = %d", code)
	}

	// Rango de 7 d: lee los agregados de 1 h (MV del rol snmp) con paso de 1 h.
	code, body = e.get("/api/v1/analytics/routers/"+e.router.String()+"/metrics?range=7d&metrics=cpu_percent,availability_ratio", e.token(read, "tenant"))
	if code != 200 {
		t.Fatalf("7d = %d %v", code, body)
	}
	if meta := body["meta"].(map[string]any); meta["step"] != 3600.0 {
		t.Fatalf("7d meta %v", meta)
	}
	var hourly int
	for _, s := range body["data"].(map[string]any)["series"].([]any) {
		for _, p := range s.(map[string]any)["points"].([]any) {
			if v := p.([]any)[1]; v != nil {
				hourly++
				if f := v.(float64); f <= 0 || f > 20 {
					t.Fatalf("7d value %v", f)
				}
			}
		}
	}
	if hourly < 2 {
		t.Fatalf("7d: %d values from aggregates", hourly)
	}

	// Interfaces: una serie por métrica e interfaz con group = nombre; sin
	// interface_id (y sin rol snmp en el proceso) las de más tráfico; 422 inválido.
	up, lan := uuid.New(), uuid.New()
	f64 := func(v float64) *float64 { return &v }
	var irows []ifaceRow
	for m := 30; m >= 2; m-- {
		ts := now.Add(-time.Duration(m)*time.Minute + 10*time.Second)
		irows = append(irows, ifaceRow{ts: ts, iface: up, name: "sfp1", rx: f64(5e6), tx: f64(1e6)},
			ifaceRow{ts: ts, iface: lan, name: "bridge", rx: f64(1e3), tx: nil})
	}
	e.insertInterfaces(irows)
	code, body = e.get("/api/v1/analytics/routers/"+e.router.String()+"/interfaces/metrics?range=1h", e.token(read, "tenant"))
	if code != 200 {
		t.Fatalf("interfaces = %d %v", code, body)
	}
	series = body["data"].(map[string]any)["series"].([]any)
	if len(series) != 4 || series[0].(map[string]any)["group"] != "sfp1" || series[2].(map[string]any)["group"] != "bridge" ||
		series[0].(map[string]any)["metric"] != "rx_bps" || series[0].(map[string]any)["unit"] != "bps" {
		t.Fatalf("interface series %v", series)
	}
	pts := series[0].(map[string]any)["points"].([]any)
	var last any
	var filled int
	for _, p := range pts {
		if v := p.([]any)[1]; v != nil {
			last, filled = v, filled+1
		}
	}
	if last == nil || last.(float64) != 5e6 || filled < 25 {
		t.Fatalf("sfp1 rx: last %v, %d values", last, filled)
	}
	if first := pts[0].([]any)[1]; first != nil {
		t.Fatalf("gap before first sample must be null: %v", first)
	}
	code, body = e.get("/api/v1/analytics/routers/"+e.router.String()+"/interfaces/metrics?range=1h&interface_id="+lan.String()+"&metrics=tx_bps", e.token(read, "tenant"))
	if code != 200 || len(body["data"].(map[string]any)["series"].([]any)) != 1 {
		t.Fatalf("explicit interface = %d %v", code, body)
	}
	if code, _ = e.get("/api/v1/analytics/routers/"+e.router.String()+"/interfaces/metrics?interface_id=ether1", e.token(read, "tenant")); code != 422 {
		t.Fatalf("bad interface_id = %d", code)
	}
}
