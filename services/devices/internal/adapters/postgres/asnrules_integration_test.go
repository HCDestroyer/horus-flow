//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/devices/migrations"
)

func asnCtx(tenant uuid.UUID) context.Context {
	return authz.WithPrincipal(context.Background(), &authz.Principal{Type: "user", Scope: authz.ScopeTenant, TenantID: tenant,
		UserID: uuid.New(), Perms: map[string][]string{app.PermSettingsRead: {authz.ScopeAll}, app.PermSettingsManage: {authz.ScopeAll}}})
}

func fields(t *testing.T, v map[string]any) app.Fields {
	t.Helper()
	b, _ := json.Marshal(v)
	var f app.Fields
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// Reglas ASN propias sobre PostgreSQL real: alta, edición y baja con outbox
// en la misma transacción, una regla viva por (tenant, ASN) y aislamiento por
// tenant con RLS (la misma regla no existe para otro ISP).
func TestAsnRulesStore(t *testing.T) {
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
	svc := app.NewAsnRules(store, nil, nil)
	tenantA, tenantB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	ctxA, ctxB := asnCtx(tenantA), asnCtx(tenantB)

	r, err := svc.Create(ctxA, fields(t, map[string]any{"asn": 4294967295, "name": "Fibra Andina", "category": "cdn_infra", "note": "tránsito"}))
	if err != nil {
		t.Fatal(err)
	}
	// El mismo ASN en otro ISP es otra regla.
	rb, err := svc.Create(ctxB, fields(t, map[string]any{"asn": 4294967295, "name": "Otro nombre", "service": "netflix"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctxA, fields(t, map[string]any{"asn": 4294967295, "name": "Dup", "category": "web"})); err == nil {
		t.Fatal("ASN repetido aceptado")
	}
	// Carrera: la unicidad la garantiza también el índice parcial.
	dup := *r
	dup.ID = uuid.Must(uuid.NewV7())
	if err := store.CreateAsnRule(ctx, pgdb.TenantID(tenantA), &dup, func(*domain.AsnRule) []outbox.Event { return nil }); !errors.Is(err, app.ErrASNTaken) {
		t.Fatalf("índice único: %v", err)
	}

	listA, err := svc.List(ctxA)
	if err != nil || len(listA) != 1 || listA[0].ASN != 4294967295 || listA[0].Name != "Fibra Andina" || listA[0].Note == nil {
		t.Fatalf("lista A = %+v, %v", listA, err)
	}
	// RLS: B no ve ni edita la regla de A (404, no 412).
	if _, err := svc.Update(ctxB, r.ID, 1, fields(t, map[string]any{"name": "X"})); err == nil {
		t.Fatal("B editó la regla de A")
	} else if e, ok := apperr.As(err); !ok || e.Status() != 404 {
		t.Fatalf("B edita la regla de A: %v", err)
	}
	var n int
	if err := db.AppTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM devices.asn_rule`).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("sin tenant el rol de la aplicación ve %d reglas (%v), quiero 0", n, err)
	}

	up, err := svc.Update(ctxA, r.ID, 1, fields(t, map[string]any{"name": "Fibra Andina SA", "service": "netflix"}))
	if err != nil || up.Version != 2 || up.Service == nil || *up.Service != "netflix" {
		t.Fatalf("edición = %+v, %v", up, err)
	}
	if _, err := svc.Update(ctxA, r.ID, 1, fields(t, map[string]any{"name": "Y"})); err == nil {
		t.Fatal("If-Match viejo aceptado")
	}
	if err := svc.Delete(ctxA, r.ID, 2); err != nil {
		t.Fatal(err)
	}
	if l, _ := svc.List(ctxA); len(l) != 0 {
		t.Fatalf("lista tras borrar = %+v", l)
	}
	// El ASN queda libre tras la baja lógica.
	if _, err := svc.Create(ctxA, fields(t, map[string]any{"asn": 4294967295, "name": "De nuevo", "category": "web"})); err != nil {
		t.Fatalf("realta tras baja: %v", err)
	}
	if l, _ := svc.List(ctxB); len(l) != 1 || l[0].ID != rb.ID {
		t.Fatalf("lista B = %+v", l)
	}

	// Outbox: created, updated, deleted y created de A + created de B, con su tenant.
	type row struct {
		subject string
		tenant  uuid.UUID
		payload []byte
	}
	var rows []row
	if err := db.PlatformTx(ctx, func(tx pgx.Tx) error {
		q, err := tx.Query(ctx, `SELECT subject, tenant_id, payload FROM devices.outbox WHERE subject LIKE 'horus.devices.asn_rule.%' ORDER BY seq`)
		if err != nil {
			return err
		}
		defer q.Close()
		for q.Next() {
			var x row
			if err := q.Scan(&x.subject, &x.tenant, &x.payload); err != nil {
				return err
			}
			rows = append(rows, x)
		}
		return q.Err()
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"created." + r.ID.String(), "created." + rb.ID.String(), "updated." + r.ID.String(), "deleted." + r.ID.String()}
	if len(rows) != 5 {
		t.Fatalf("outbox: %d eventos, quiero 5", len(rows))
	}
	for i, w := range want {
		if rows[i].subject != "horus.devices.asn_rule."+w {
			t.Errorf("evento %d = %s, quiero %s", i, rows[i].subject, w)
		}
	}
	var env struct {
		TenantID         string         `json:"tenant_id"`
		AggregateVersion int            `json:"aggregate_version"`
		Data             map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rows[3].payload, &env); err != nil {
		t.Fatal(err)
	}
	if env.TenantID != tenantA.String() || env.AggregateVersion != 3 || env.Data["deleted_at"] == nil || env.Data["asn"] != float64(4294967295) ||
		env.Data["service"] != "netflix" {
		t.Fatalf("sobre de la baja = %s", rows[3].payload)
	}
}
