//go:build integration

package pgstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/pgstore"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
	snmpmig "github.com/hcdestroyer/horus-flow/services/snmp/migrations"
)

// Elección de puertos de los switches (migración 20261018203737): versión (If-Match), sustitución completa,
// RLS por tenant, carga de plataforma y vuelta a la elección por defecto.
func TestSwitchPortStore(t *testing.T) {
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
	tenantA, tenantB, sw := uuid.New(), uuid.New(), uuid.New()
	if got, err := st.GetPortSelection(ctx, tenantA, sw); err != nil || got != nil {
		t.Fatalf("sin elección: %+v %v", got, err)
	}
	p, err := st.PutPortSelection(ctx, tenantA, sw, map[string]domain.PortChoice{"ether1": {Monitored: true, Uplink: true},
		"ether2": {Monitored: true}, "ether3": {Monitored: false}}, "user:1", 0)
	if err != nil || p.Version != 1 || len(p.Ports) != 2 || !p.Ports["ether1"].Uplink || p.UpdatedBy == nil || *p.UpdatedBy != "user:1" {
		t.Fatalf("alta: %+v %v", p, err)
	}
	if _, err := st.PutPortSelection(ctx, tenantA, sw, nil, "user:1", 9); !errors.Is(err, pgstore.ErrVersion) {
		t.Fatalf("If-Match viejo: %v", err)
	}
	p, err = st.PutPortSelection(ctx, tenantA, sw, map[string]domain.PortChoice{"ether2": {Monitored: true, Uplink: true}}, "user:2", 1)
	if err != nil || p.Version != 2 || len(p.Ports) != 1 || !p.Ports["ether2"].Uplink {
		t.Fatalf("sustitución: %+v %v", p, err)
	}
	if got, _ := st.GetPortSelection(ctx, tenantB, sw); got != nil {
		t.Fatal("B lee la elección de A (RLS)")
	}
	all, err := st.LoadPortSelections(ctx)
	if err != nil || len(all[sw].Ports) != 1 || all[sw].Version != 2 {
		t.Fatalf("carga de plataforma: %+v %v", all, err)
	}
	if err := st.DeletePortSelection(ctx, tenantB, sw); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetPortSelection(ctx, tenantA, sw); got == nil {
		t.Fatal("B borró la elección de A (RLS)")
	}
	if err := st.DeletePortSelection(ctx, tenantA, sw); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetPortSelection(ctx, tenantA, sw); got != nil {
		t.Fatal("la elección debía volver a la de por defecto")
	}
	var n int
	if err := db.PlatformTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM snmp.switch_port`).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("puertos en cascada: %d %v", n, err)
	}
}
