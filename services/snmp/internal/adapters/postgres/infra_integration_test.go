//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	pgadapter "github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
	snmpmig "github.com/hcdestroyer/horus-flow/services/snmp/migrations"
)

// Esquema snmp: RLS fail-closed en las tablas de negocio; el estado y el
// evento horus.snmp.service.state_changed se guardan en la misma
// transacción del tenant (outbox), con el payload fijo del contrato.
func TestInfraStoreRLSAndOutbox(t *testing.T) {
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
	st := pgadapter.New(db)
	tenantA, tenantB := uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	chk := domain.Check{ID: uuid.New(), Type: domain.CheckICMP}
	svc := &domain.Service{ID: uuid.New(), TenantID: tenantA, Name: "DNS", Target: netip.MustParseAddr("192.0.2.53"), Kind: "dns",
		Checks: []domain.Check{chk}, IntervalSeconds: 60, FailuresBeforeDown: 3, RecoveriesBeforeUp: 2, Enabled: true,
		CheckStatus: []domain.CheckStatus{domain.NewCheckStatus(chk)}, CreatedAt: now, UpdatedAt: now, Version: 1}
	if err := st.Insert(ctx, pgdb.TenantID(tenantA), svc); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, pgdb.TenantID(tenantB), svc.ID); err == nil {
		t.Fatal("B lee el servicio de A")
	}
	// Sin tenant (rol de la aplicación): cero filas.
	var n int
	if err := db.AppTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM snmp.infra_service`).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("fail-closed: %d filas (%v)", n, err)
	}
	cs := domain.NewCheckStatus(chk)
	cs.Apply(domain.CheckObservation{State: domain.StateUp, Reason: domain.ReasonOK, LatencyMs: new(float64)}, 3, 2, now)
	ct := domain.CheckICMP
	status := domain.ServiceStatus{State: domain.StateUp, CheckType: &ct, Since: &now}
	ev := outbox.Event{Type: "horus.snmp.service.state_changed", Source: "horus/snmp", TenantID: &tenantA, AggregateType: "infra_service",
		AggregateID: svc.ID, AggregateVersion: 1, Actor: outbox.Actor{Type: "service", ID: "svc:snmp"}, OccurredAt: now,
		Data: map[string]any{"tenant_id": tenantA.String(), "service_id": svc.ID.String(), "site_id": nil, "name": "DNS", "check_type": "icmp",
			"target": "192.0.2.53", "previous_state": "unknown", "state": "up", "reason": "ok", "observed_at": now.Format(outbox.TimeFormat)}}
	// Con el tenant equivocado no se guarda nada (RLS): el servicio "no existe".
	if err := st.SaveState(ctx, pgdb.TenantID(tenantB), svc.ID, status, []domain.CheckStatus{cs}, &ev); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveState(ctx, pgdb.TenantID(tenantA), svc.ID, status, []domain.CheckStatus{cs}, &ev); err != nil {
		t.Fatal(err)
	}
	var subject string
	var payload []byte
	if err := db.PlatformTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM snmp.outbox`).Scan(&n); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT subject, payload FROM snmp.outbox`).Scan(&subject, &payload)
	}); err != nil || n != 1 {
		t.Fatalf("outbox: %d filas (%v)", n, err)
	}
	if subject != "horus.snmp.service.state_changed."+svc.ID.String() {
		t.Fatalf("subject = %s", subject)
	}
	var env struct {
		Type     string         `json:"type"`
		TenantID string         `json:"tenant_id"`
		Data     map[string]any `json:"data"`
	}
	if err := json.Unmarshal(payload, &env); err != nil || env.Type != "horus.snmp.service.state_changed" || env.TenantID != tenantA.String() ||
		env.Data["state"] != "up" || env.Data["check_type"] != "icmp" || len(env.Data) != 10 {
		t.Fatalf("sobre: %s (%v)", payload, err)
	}
	got, err := st.Get(ctx, pgdb.TenantID(tenantA), svc.ID)
	if err != nil || got.Status.State != domain.StateUp || len(got.CheckStatus) != 1 || got.CheckStatus[0].State != domain.StateUp ||
		len(got.CheckStatus[0].Recent) != 1 {
		t.Fatalf("estado guardado: %+v (%v)", got, err)
	}
	all, err := st.LoadAll(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("LoadAll (plataforma): %d (%v)", len(all), err)
	}
}
