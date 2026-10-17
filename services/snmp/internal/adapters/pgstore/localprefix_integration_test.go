//go:build integration

package pgstore_test

import (
	"context"
	"net/netip"
	"testing"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/pgstore"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
	snmpmig "github.com/hcdestroyer/horus-flow/services/snmp/migrations"
)

// La lista local por prefijos (cidr[]) se guarda y se lee normalizada, por
// tenant y en la carga de plataforma.
func TestCapacityLocalPrefixesStore(t *testing.T) {
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
	tenant := uuid.New()
	v := domain.DefaultCapacitySettings()
	v.LocalPrefixes = []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("2001:db8:7a00::/48")}
	if _, err := st.PutCapacitySettings(ctx, tenant, v, "user:1", 0); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetCapacitySettings(ctx, tenant)
	if err != nil || len(got.Settings.LocalPrefixes) != 2 || got.Settings.LocalPrefixes[0].String() != "198.51.100.0/24" ||
		got.Settings.LocalPrefixes[1].String() != "2001:db8:7a00::/48" {
		t.Fatalf("leído = %+v %v", got, err)
	}
	if other, _ := st.GetCapacitySettings(ctx, uuid.New()); other != nil {
		t.Fatal("otro tenant ve los ajustes")
	}
	all, err := st.LoadCapacity(ctx)
	if err != nil || len(all.Settings[tenant].LocalPrefixes) != 2 {
		t.Fatalf("carga de plataforma = %+v %v", all.Settings[tenant], err)
	}
}
