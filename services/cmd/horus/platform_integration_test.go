//go:build integration

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/packages/go/platformevents"
	"github.com/hcdestroyer/horus-flow/packages/go/testkit"
)

// D23: cada arranque y parada de proceso y rol queda en platform_events con
// su versión; un arranque tras una parada no limpia lo registra; un cambio
// de configuración entre arranques también; la API lista y filtra.
func TestPlatformEventsLifecycleAndAPI(t *testing.T) {
	ctx := context.Background()
	dsn := pgtest.New(t)
	env := []string{"HORUS_POSTGRES_DSN=" + dsn, "HORUS_INSTANCE=c1", "HORUS_PLATFORM_MONITOR_INTERVAL=100ms"}
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	st := platformevents.NewStore(db)
	count := func(kind string) int {
		var n int
		_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM platform_events.event WHERE kind = $1`, kind).Scan(&n)
		return n
	}

	p := start(t, roleCatalog, "example", env...)
	testkit.Eventually(t, 10*time.Second, func() bool {
		return count(platformevents.KindProcessStarted) == 1 && count(platformevents.KindRoleStarted) == 1
	}, "process_started and role_started recorded")
	if code := p.stop(t); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if count(platformevents.KindProcessStopped) != 1 || count(platformevents.KindRoleStopped) != 1 {
		t.Fatalf("stopped events: process=%d role=%d", count(platformevents.KindProcessStopped), count(platformevents.KindRoleStopped))
	}
	if count(platformevents.KindUncleanShutdown) != 0 {
		t.Fatal("clean stop reported as unclean")
	}

	// kill -9: queda un process_started sin su process_stopped.
	if err := st.Insert(ctx, []platformevents.Event{{ID: uuid.Must(uuid.NewV7()), OccurredAt: time.Now().UTC(), Kind: platformevents.KindProcessStarted,
		Severity: "info", Process: "horus-test", Instance: "c1", Version: "dev", Message: "process started",
		Details: map[string]any{"config": map[string]any{"HORUS_POSTGRES_DSN": "x"}}}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	p2 := start(t, roleCatalog, "example", env...)
	testkit.Eventually(t, 10*time.Second, func() bool { return count(platformevents.KindUncleanShutdown) == 1 }, "unclean_shutdown recorded")
	testkit.Eventually(t, 10*time.Second, func() bool { return count(platformevents.KindConfigChanged) >= 1 }, "config_changed recorded")
	_ = p2.stop(t)

	// API (el borde y el guard ya exigen token de plataforma con platform.status.read).
	h := &eventsHandler{store: st, cursor: pagination.NewCodec([]byte("k")), logger: slog.New(slog.DiscardHandler)}
	rec := httptest.NewRecorder()
	h.list(rec, httptest.NewRequest(http.MethodGet, "/api/v1/platform/events?severity=error&limit=10", nil))
	var body struct {
		Data []platformevents.Event `json:"data"`
		Page pagination.Page        `json:"page"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		t.Fatalf("api: %d %s", rec.Code, rec.Body.String())
	}
	if len(body.Data) != 1 || body.Data[0].Kind != platformevents.KindUncleanShutdown || body.Data[0].Process != "horus-test" {
		t.Fatalf("severity=error: %+v", body.Data)
	}
	// Paginación.
	rec = httptest.NewRecorder()
	h.list(rec, httptest.NewRequest(http.MethodGet, "/api/v1/platform/events?limit=2", nil))
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Data) != 2 || !body.Page.HasMore || body.Page.NextCursor == nil {
		t.Fatalf("page 1: %+v", body.Page)
	}
	first := body.Data[1].ID
	rec = httptest.NewRecorder()
	h.list(rec, httptest.NewRequest(http.MethodGet, "/api/v1/platform/events?limit=2&cursor="+*body.Page.NextCursor, nil))
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusOK || len(body.Data) == 0 || body.Data[0].ID == first {
		t.Fatalf("page 2: %d %+v", rec.Code, body.Data)
	}
	rec = httptest.NewRecorder()
	h.list(rec, httptest.NewRequest(http.MethodGet, "/api/v1/platform/events?kind=nope", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad kind: %d", rec.Code)
	}
}
