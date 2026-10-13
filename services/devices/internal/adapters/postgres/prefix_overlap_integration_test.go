//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/devices/migrations"
)

// OverlappingPrefix devuelve el prefijo activo que impide declarar otro, con
// la misma condición que la restricción de exclusión: realm privado del nodo
// (no el de otro nodo) o realm público del ISP (cualquier nodo); los borrados
// no cuentan y, si el nuevo cubre varios, gana el más específico.
func TestOverlappingPrefix(t *testing.T) {
	ctx := context.Background()
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: pgtest.New(t), AppRole: "devices_app", PlatformRole: "devices_platform"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := pgdb.Migrate(ctx, db, migrations.Schema, migrations.Postgres(), nil); err != nil {
		t.Fatal(err)
	}
	store := postgres.New(db)
	tenant := pgdb.TenantID(uuid.Must(uuid.NewV7()))
	now := time.Now().UTC()
	noEv := func(*domain.ClientPrefix) []outbox.Event { return nil }
	newSite := func(name string) *domain.Site {
		s := &domain.Site{ID: uuid.Must(uuid.NewV7()), TenantID: tenant.UUID(), Name: name, Kind: "node", Tags: []string{}, CreatedAt: now, UpdatedAt: now,
			Version: 1, PrivateRealmID: uuid.Must(uuid.NewV7())}
		if err := store.CreateSite(ctx, tenant, s, func(*domain.Site) []outbox.Event { return nil }); err != nil {
			t.Fatal(err)
		}
		return s
	}
	add := func(site *domain.Site, p, role string) *domain.ClientPrefix {
		c := &domain.ClientPrefix{ID: uuid.Must(uuid.NewV7()), TenantID: tenant.UUID(), SiteID: site.ID, Prefix: netip.MustParsePrefix(p), Role: role,
			DefaultKind: "residential", AssignmentMode: "unknown", Source: "manual", Confirmed: true, CreatedAt: now, UpdatedAt: now, Version: 1}
		c.RealmKind = domain.RealmKindFor(c.Prefix)
		if err := store.CreatePrefix(ctx, tenant, c, noEv); err != nil {
			t.Fatalf("crear %s: %v", p, err)
		}
		return c
	}
	central, norte := newSite("Central"), newSite("Norte")
	add(central, "10.0.0.0/9", "customers")
	excluded := add(central, "10.200.0.0/20", "excluded")
	add(norte, "190.0.0.0/22", "infrastructure")

	// Mismo nodo: un /8 que cubre los dos → el más específico (el /20 excluido).
	c, err := store.OverlappingPrefix(ctx, tenant, central.ID, "node_private", netip.MustParsePrefix("10.0.0.0/8"))
	if err != nil || c.Prefix.String() != "10.200.0.0/20" || c.Role != "excluded" || c.SiteID != central.ID || c.SiteName != "Central" {
		t.Fatalf("mismo nodo = %+v, %v", c, err)
	}
	// El realm privado de otro nodo no cuenta.
	if _, err := store.OverlappingPrefix(ctx, tenant, norte.ID, "node_private", netip.MustParsePrefix("10.200.1.0/24")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("privado de otro nodo = %v", err)
	}
	// Público: es del ISP, choca desde cualquier nodo.
	c, err = store.OverlappingPrefix(ctx, tenant, central.ID, "public", netip.MustParsePrefix("190.0.0.0/16"))
	if err != nil || c.Prefix.String() != "190.0.0.0/22" || c.SiteName != "Norte" {
		t.Fatalf("público = %+v, %v", c, err)
	}
	// Un prefijo quitado deja de bloquear (coincide con la restricción).
	if err := store.DeletePrefix(ctx, tenant, excluded.ID, 1, noEv); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OverlappingPrefix(ctx, tenant, central.ID, "node_private", netip.MustParsePrefix("10.200.1.0/24")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("tras quitar = %v", err)
	}
	c, err = store.OverlappingPrefix(ctx, tenant, central.ID, "node_private", netip.MustParsePrefix("10.1.0.0/16"))
	if err != nil || c.Prefix.String() != "10.0.0.0/9" || c.Role != "customers" {
		t.Fatalf("contenido en el /9 = %+v, %v", c, err)
	}
	// Y la restricción sigue siendo la que manda.
	dup := &domain.ClientPrefix{ID: uuid.Must(uuid.NewV7()), TenantID: tenant.UUID(), SiteID: central.ID, Prefix: netip.MustParsePrefix("10.1.0.0/16"),
		RealmKind: "node_private", Role: "customers", DefaultKind: "residential", AssignmentMode: "unknown", Source: "manual", Confirmed: true,
		CreatedAt: now, UpdatedAt: now, Version: 1}
	if err := store.CreatePrefix(ctx, tenant, dup, noEv); !errors.Is(err, app.ErrOverlap) {
		t.Fatalf("exclusión: %v", err)
	}
}
