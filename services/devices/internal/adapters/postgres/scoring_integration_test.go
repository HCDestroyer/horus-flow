//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/archtest"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/devices/migrations"
)

// El consumidor devices-customer-detection contra PostgreSQL real: aplica la
// sugerencia del scoring (tipo, historial inmutable con razones y causa,
// evento por outbox), respeta el tipo manual y la lista de posibles
// comerciales sale del filtro commercial_candidate.
func TestKindSuggestionPostgres(t *testing.T) {
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
	tid := uuid.Must(uuid.NewV7())
	tenant := pgdb.TenantID(tid)
	now := time.Now().UTC()
	site := &domain.Site{ID: uuid.Must(uuid.NewV7()), TenantID: tid, Name: "Centro", Kind: "node", Tags: []string{}, CreatedAt: now,
		UpdatedAt: now, Version: 1, PrivateRealmID: uuid.Must(uuid.NewV7())}
	if err := store.CreateSite(ctx, tenant, site, func(*domain.Site) []outbox.Event { return nil }); err != nil {
		t.Fatal(err)
	}
	cp := &domain.ClientPrefix{ID: uuid.Must(uuid.NewV7()), TenantID: tid, SiteID: site.ID, Prefix: netip.MustParsePrefix("10.20.0.0/24"),
		Role: "customers", DefaultKind: "residential", AssignmentMode: "static", Source: "manual", Confirmed: true, CreatedAt: now, UpdatedAt: now, Version: 1}
	cp.RealmKind = domain.RealmKindFor(cp.Prefix)
	if err := store.CreatePrefix(ctx, tenant, cp, func(*domain.ClientPrefix) []outbox.Event { return nil }); err != nil {
		t.Fatal(err)
	}
	created, err := store.DiscoverCustomers(ctx, tenant, site.PrivateRealmID, []app.NewCustomer{
		{Address: netip.MustParseAddr("10.20.0.10"), FirstSeen: now}, {Address: netip.MustParseAddr("10.20.0.11"), FirstSeen: now},
	}, func(*domain.Customer) []outbox.Event { return nil })
	if err != nil || len(created) != 2 {
		t.Fatalf("alta: %v %d", err, len(created))
	}
	office, locked := created[0], created[1]
	svc := app.NewCustomers(store, nil, nil, nil, nil)
	// El segundo cliente tiene el tipo fijado a mano (residencial).
	locked.SetKind("residential", "Contrato residencial verificado", nil, now)
	if err := store.UpdateCustomer(ctx, tenant, &locked, locked.Version, nil, func(*domain.Customer) []outbox.Event { return nil }); err != nil {
		t.Fatal(err)
	}

	root, err := archtest.FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(filepath.Join(root, "packages/events/examples/horus.detection.customer.kind_suggested.json"))
	if err != nil {
		t.Fatal(err)
	}
	deliver := func(customer uuid.UUID) *natsx.Message {
		env, err := natsx.DecodeEnvelope(golden)
		if err != nil {
			t.Fatal(err)
		}
		var d map[string]any
		_ = json.Unmarshal(env.Data, &d)
		d["customer_id"] = customer.String()
		env.Data, _ = json.Marshal(d)
		env.ID = uuid.Must(uuid.NewV7())
		env.Time = now
		m := &natsx.Message{Envelope: env, Tenant: tid}
		if err := svc.HandleDetection(ctx, m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	msg := deliver(office.ID)
	deliver(locked.ID)

	got, err := store.GetCustomer(ctx, tenant, office.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "commercial" || got.KindSource != "scoring" || *got.KindConfidence != 87 || *got.CommercialScore != 87 ||
		*got.SuggestedKind != "commercial" || *got.SuggestedModel != "commercial-v1" || got.SuggestedAt == nil {
		t.Fatalf("oficina = %+v", got)
	}
	hist, err := store.ListKindChanges(ctx, tenant, office.ID, nil, uuid.Nil, 10)
	if err != nil || len(hist) != 1 {
		t.Fatalf("historial = %v, %v", hist, err)
	}
	h := hist[0]
	if h.Source != "scoring" || *h.ModelRef != "commercial-v1@2026-10-08" || len(h.ReasonCodes) != 4 || *h.Confidence != 87 {
		t.Fatalf("entrada = %+v", h)
	}
	var causation uuid.UUID
	if err := db.PlatformTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT causation_event_id FROM devices.customer_kind_change WHERE id = $1`, h.ID).Scan(&causation)
	}); err != nil || causation != msg.Envelope.ID {
		t.Fatalf("causation_event_id = %v, %v", causation, err)
	}
	var kindChanged int
	if err := db.PlatformTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM devices.outbox WHERE subject LIKE 'horus.devices.customer.kind_changed.%'`).Scan(&kindChanged)
	}); err != nil || kindChanged != 1 {
		t.Fatalf("eventos kind_changed = %d, %v", kindChanged, err)
	}
	lk, err := store.GetCustomer(ctx, tenant, locked.ID)
	if err != nil || lk.Kind != "residential" || !lk.KindLocked || lk.SuggestedKind == nil || *lk.SuggestedKind != "commercial" {
		t.Fatalf("bloqueado = %+v, %v", lk, err)
	}
	// Posibles comerciales: la oficina (sugerencia comercial, sin bloquear); el bloqueado no.
	yes := true
	rows, err := store.ListCustomers(ctx, tenant, app.CustomerQuery{SortCol: "last_seen", Desc: true, Limit: 50, CommercialCandidate: &yes})
	if err != nil || len(rows) != 1 || rows[0].ID != office.ID {
		t.Fatalf("candidatos = %v, %v", rows, err)
	}
	// Confirmar = set-kind manual (bloquea): sale de la lista.
	got.SetKind("commercial", "Confirmado por el operador", nil, now)
	if err := store.UpdateCustomer(ctx, tenant, got, got.Version, nil, func(*domain.Customer) []outbox.Event { return nil }); err != nil {
		t.Fatal(err)
	}
	if rows, err := store.ListCustomers(ctx, tenant, app.CustomerQuery{SortCol: "last_seen", Desc: true, Limit: 50, CommercialCandidate: &yes}); err != nil || len(rows) != 0 {
		t.Fatalf("tras confirmar = %v, %v", rows, err)
	}
}
