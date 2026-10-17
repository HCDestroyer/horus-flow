//go:build integration

package pgstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/pgstore"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
	snmpmig "github.com/hcdestroyer/horus-flow/services/snmp/migrations"
)

// Capacidad en el esquema snmp: configuración por enlace y ajustes con
// versión (If-Match) y RLS por tenant; condiciones y pronósticos con su
// evento en la misma transacción (outbox); carga de plataforma.
func TestCapacityStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: pgtest.New(t), AppRole: "snmp_app", PlatformRole: "snmp_platform"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := pgdb.Migrate(ctx, db, snmpmig.Schema, snmpmig.Postgres(), nil); err != nil {
		t.Fatal(err)
	}
	st := pgstore.New(db)
	tenantA, tenantB, router, iface := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	contracted := int64(500_000_000)
	egress := true
	in := domain.UplinkConfigInput{RouterID: router, Label: "Proveedor A", Kind: domain.UplinkKindInternational, ContractedBps: &contracted,
		InternationalEgress: &egress}
	c, err := st.PutUplinkConfig(ctx, tenantA, iface, in, "user:1", 0)
	if err != nil || c.Version != 1 || *c.ContractedBps != contracted || !c.InternationalEgress {
		t.Fatalf("alta: %v %+v", err, c)
	}
	if _, err := st.PutUplinkConfig(ctx, tenantA, iface, in, "user:1", 7); !errors.Is(err, pgstore.ErrVersion) {
		t.Fatalf("If-Match viejo: %v", err)
	}
	in.Label = "Proveedor A (10G)"
	if c, err = st.PutUplinkConfig(ctx, tenantA, iface, in, "user:1", 1); err != nil || c.Version != 2 || c.Label != "Proveedor A (10G)" {
		t.Fatalf("edición: %v %+v", err, c)
	}
	if got, _ := st.GetUplinkConfig(ctx, tenantB, iface); got != nil {
		t.Fatal("B lee la configuración de A")
	}
	if l, _ := st.ListUplinkConfigs(ctx, tenantB); len(l) != 0 {
		t.Fatal("B lista enlaces de A")
	}
	if ok, err := st.DeleteUplinkConfig(ctx, tenantB, iface); err != nil || ok {
		t.Fatalf("B borra el enlace de A: %v %v", ok, err)
	}
	// Restricciones de la tabla (defensa en profundidad).
	bad := in
	bad.Kind = "fibra"
	if _, err := st.PutUplinkConfig(ctx, tenantA, uuid.New(), bad, "user:1", 0); err == nil {
		t.Fatal("tipo inválido aceptado por la base")
	}

	// Ajustes: arrays y jsonb.
	s := domain.DefaultCapacitySettings()
	gt := "GT"
	s.Country, s.LocalASNs = &gt, []uint32{2906, 15169}
	s.ProbeTargets = []domain.ProbeTarget{{Label: "Gateway", Address: netip.MustParseAddr("203.0.113.1")}}
	row, err := st.PutCapacitySettings(ctx, tenantA, s, "user:1", 0)
	if err != nil || row.Version != 1 {
		t.Fatalf("ajustes: %v %+v", err, row)
	}
	got, err := st.GetCapacitySettings(ctx, tenantA)
	if err != nil || *got.Settings.Country != "GT" || len(got.Settings.LocalASNs) != 2 || got.Settings.ProbeTargets[0].Address.String() != "203.0.113.1" {
		t.Fatalf("lectura de ajustes: %v %+v", err, got)
	}
	if other, _ := st.GetCapacitySettings(ctx, tenantB); other != nil {
		t.Fatal("B lee los ajustes de A")
	}

	// Condición y pronóstico con evento (outbox) en la misma transacción.
	now := time.Now().UTC().Truncate(time.Millisecond)
	cond := domain.ConditionRow{TenantID: tenantA, InterfaceID: iface, RouterID: router, Condition: domain.ConditionSaturation,
		Level: domain.LevelCritical, Since: now, Value: 0.93}
	ev := &outbox.Event{Type: "horus.snmp.uplink.condition_changed", Source: "horus/snmp", TenantID: &tenantA, AggregateType: "uplink",
		AggregateID: iface, AggregateVersion: 1, Actor: outbox.Actor{Type: "service", ID: "svc:snmp"}, OccurredAt: now,
		Data: map[string]any{"level": "critical"}}
	if err := st.SaveCondition(ctx, cond, ev); err != nil {
		t.Fatal(err)
	}
	if l, err := st.ListConditions(ctx, tenantA); err != nil || len(l) != 1 || l[0].Value != 0.93 {
		t.Fatalf("condiciones: %v %+v", err, l)
	}
	if l, _ := st.ListConditions(ctx, tenantB); len(l) != 0 {
		t.Fatal("B ve las condiciones de A")
	}
	f := domain.RunForecast(domain.ForecastInput{Series: []domain.DailyPeak{{Day: now, Bps: 1e8}}, CapacityBps: 1e9, Planning: 0.8,
		Urgent: 0.9, LeadDays: 90, Now: now})
	fr := domain.ForecastRow{TenantID: tenantA, SubjectID: iface, SubjectKind: domain.SubjectUplink, Label: "Proveedor A", Level: f.Level,
		ComputedAt: now, Forecast: f, Recommendation: domain.Recommendation(f, "del enlace")}
	if err := st.SaveForecast(ctx, fr, nil); err != nil {
		t.Fatal(err)
	}
	fl, err := st.ListForecasts(ctx, tenantA)
	if err != nil || len(fl) != 1 || fl[0].Level != domain.ForecastInsufficient || fl[0].Recommendation == "" || fl[0].Forecast.HistoryDays != 1 {
		t.Fatalf("pronósticos: %v %+v", err, fl)
	}
	var n int
	var payload []byte
	if err := db.PlatformTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*), max(payload::text)::jsonb FROM snmp.outbox WHERE subject = $1`,
			"horus.snmp.uplink.condition_changed."+iface.String()).Scan(&n, &payload)
	}); err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	if n != 1 || json.Unmarshal(payload, &env) != nil || env["type"] != "horus.snmp.uplink.condition_changed" {
		t.Fatalf("outbox: %d %s", n, payload)
	}

	// Carga de plataforma (todas las tenants).
	cs, err := st.LoadCapacity(ctx)
	if err != nil || len(cs.Configs[tenantA]) != 1 || cs.Settings[tenantA].LocalASNs[1] != 15169 || len(cs.Conditions) != 1 || len(cs.Forecasts) != 1 {
		t.Fatalf("carga: %v %+v", err, cs)
	}
	if ok, err := st.DeleteUplinkConfig(ctx, tenantA, iface); err != nil || !ok {
		t.Fatalf("baja: %v %v", ok, err)
	}
	if err := st.DeleteCapacitySettings(ctx, tenantA); err != nil {
		t.Fatal(err)
	}
}
