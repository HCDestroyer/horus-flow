//go:build integration

package platformevents_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/packages/go/platformevents"
)

func TestRecorderStoresListsPurgesAndSpools(t *testing.T) {
	ctx := context.Background()
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: pgtest.New(t), AppRole: platformevents.AppRole, PlatformRole: platformevents.PlatformRole})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := platformevents.Migrate(ctx, db, nil); err != nil {
		t.Fatal(err)
	}
	st := platformevents.NewStore(db)
	spool := t.TempDir()
	rec := platformevents.NewRecorder(platformevents.Options{Store: st, Process: "horus-app", Instance: "c1", Version: "1.2.3", SpoolDir: spool})
	start := time.Now().UTC().Add(-time.Minute)
	rec.Record(ctx, platformevents.Event{Kind: platformevents.KindProcessStarted, OccurredAt: start, Message: "process started"})
	tenant := uuid.New()
	rec.Record(ctx, platformevents.Event{Kind: platformevents.KindAlertDeliveryFailed, Severity: platformevents.SeverityWarn, Role: "alerts",
		TenantID: &tenant, Message: "x", Details: map[string]any{"attempts": 8}})
	if err := rec.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	evs, err := st.List(ctx, platformevents.Query{Severity: platformevents.SeverityWarn})
	if err != nil || len(evs) != 1 || evs[0].Kind != platformevents.KindAlertDeliveryFailed || *evs[0].TenantID != tenant ||
		evs[0].Process != "horus-app" || evs[0].Version != "1.2.3" || evs[0].Details["attempts"] != float64(8) {
		t.Fatalf("list warn: %v %+v", err, evs)
	}
	// Parada no limpia: el último evento de ciclo de vida es process_started.
	last, err := st.LastLifecycle(ctx, "horus-app", "c1", time.Now())
	if err != nil || last == nil || last.Kind != platformevents.KindProcessStarted {
		t.Fatalf("last lifecycle: %v %+v", err, last)
	}

	// PostgreSQL caído: los eventos van al spool y se vuelcan al volver.
	db.Close()
	down := platformevents.NewRecorder(platformevents.Options{Store: st, Process: "horus-app", Instance: "c1", SpoolDir: spool})
	down.Record(ctx, platformevents.Event{Kind: platformevents.KindDependencyDown, Message: "postgres down"})
	if err := down.Flush(ctx); err == nil {
		t.Fatal("expected flush error with the pool closed")
	}
	db2, err := pgdb.Open(ctx, pgdb.Config{DSN: pgtest.New(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db2.Close)
	if _, err := platformevents.Migrate(ctx, db2, nil); err != nil {
		t.Fatal(err)
	}
	st2 := platformevents.NewStore(db2)
	up := platformevents.NewRecorder(platformevents.Options{Store: st2, Process: "horus-app", SpoolDir: spool})
	if err := up.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	evs, _ = st2.List(ctx, platformevents.Query{Kinds: []string{platformevents.KindDependencyDown}})
	if len(evs) != 1 {
		t.Fatalf("spooled events not replayed: %+v", evs)
	}
	// Retención.
	if n, err := st2.Purge(ctx, time.Now().Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
}
