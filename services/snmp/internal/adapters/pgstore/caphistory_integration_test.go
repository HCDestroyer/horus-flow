//go:build integration

package pgstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/pgstore"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
	snmpmig "github.com/hcdestroyer/horus-flow/services/snmp/migrations"
)

// Historia de capacidad: el alta abre una vigencia, solo un cambio de
// contracted_bps abre otra (cerrando la anterior), no se puede fechar antes
// del cambio anterior, borrar cierra la vigente sin perder la historia, RLS
// por tenant y carga de plataforma.
func TestCapacityHistoryStore(t *testing.T) {
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
	half, gig := int64(500_000_000), int64(1_000_000_000)
	t0 := time.Now().UTC().Add(-30 * 24 * time.Hour).Truncate(time.Second)
	in := domain.UplinkConfigInput{RouterID: router, Label: "Proveedor A", Kind: domain.UplinkKindInternational, ContractedBps: &half,
		CapacityValidFrom: &t0}
	if _, err := st.PutUplinkConfig(ctx, tenantA, iface, in, "user:1", 0); err != nil {
		t.Fatal(err)
	}
	// Cambiar solo el nombre no abre otra vigencia.
	in.Label, in.CapacityValidFrom = "Proveedor A (fibra)", nil
	if _, err := st.PutUplinkConfig(ctx, tenantA, iface, in, "user:1", 0); err != nil {
		t.Fatal(err)
	}
	// Upgrade fechado hace 10 días.
	t1 := t0.Add(20 * 24 * time.Hour)
	in.ContractedBps, in.CapacityValidFrom = &gig, &t1
	if _, err := st.PutUplinkConfig(ctx, tenantA, iface, in, "user:2", 0); err != nil {
		t.Fatal(err)
	}
	h, err := st.ListCapacityHistory(ctx, tenantA)
	if err != nil || len(h[iface]) != 2 {
		t.Fatalf("historia = %+v %v", h, err)
	}
	p0, p1 := h[iface][0], h[iface][1]
	if !p0.ValidFrom.Equal(t0) || p0.ValidTo == nil || !p0.ValidTo.Equal(t1) || *p0.ContractedBps != half ||
		!p1.ValidFrom.Equal(t1) || p1.ValidTo != nil || *p1.ContractedBps != gig || *p1.ChangedBy != "user:2" {
		t.Fatalf("vigencias = %+v / %+v", p0, p1)
	}
	// Antes del cambio anterior → error, sin cambiar nada (la configuración tampoco).
	back := t1.Add(-time.Hour)
	in.ContractedBps, in.CapacityValidFrom = &half, &back
	if _, err := st.PutUplinkConfig(ctx, tenantA, iface, in, "user:3", 0); !errors.Is(err, pgstore.ErrCapacityValidFrom) {
		t.Fatalf("fecha anterior: %v", err)
	}
	if c, _ := st.GetUplinkConfig(ctx, tenantA, iface); *c.ContractedBps != gig {
		t.Fatalf("la configuración cambió pese al error: %+v", c)
	}
	if hb, _ := st.ListCapacityHistory(ctx, tenantB); len(hb) != 0 {
		t.Fatal("B lee la historia de A")
	}
	// Borrar cierra la vigente; la historia queda.
	if ok, err := st.DeleteUplinkConfig(ctx, tenantA, iface); !ok || err != nil {
		t.Fatalf("borrar: %v %v", ok, err)
	}
	all, err := st.LoadCapacity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := all.History[tenantA][iface]
	if len(got) != 2 || got[1].ValidTo == nil {
		t.Fatalf("historia tras borrar (carga de plataforma) = %+v", got)
	}
	if _, ok := domain.ContractedAt(got, time.Now().UTC().Add(time.Hour)); ok {
		t.Fatal("tras borrar no hay capacidad contratada vigente")
	}
}
