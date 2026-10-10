//go:build integration

package natsx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx/natstest"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) lines() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	for _, l := range strings.Split(s.b.String(), "\n") {
		m := map[string]any{}
		if json.Unmarshal([]byte(l), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// D23 / observability.md §4.2: el trace_id de una petición HTTP viaja por el
// outbox (fila y sobre), el relay (cabecera traceparent en NATS) y llega al
// consumidor, cuyos logs llevan el mismo trace_id, tenant_id y event_id.
func TestTracePropagatesHTTPToOutboxToNATSToConsumer(t *testing.T) {
	ctx := context.Background()
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: pgtest.New(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Pool.Exec(ctx, `CREATE SCHEMA ttest; CREATE TABLE ttest.outbox (
		id uuid PRIMARY KEY, seq bigint GENERATED ALWAYS AS IDENTITY, tenant_id uuid, subject text NOT NULL,
		aggregate_id uuid NOT NULL, headers jsonb NOT NULL DEFAULT '{}', payload jsonb NOT NULL,
		occurred_at timestamptz NOT NULL, published_at timestamptz, attempts integer NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	_, js := natstest.New(t)
	var logs syncBuffer
	logger, _, err := observability.NewLogger(&logs, observability.LogConfig{Service: "horus", Process: "test"})
	if err != nil {
		t.Fatal(err)
	}
	tenant := uuid.Must(uuid.NewV7())
	agg := uuid.Must(uuid.NewV7())

	// 1. HTTP: la petición crea el evento en el outbox dentro de su transacción.
	reg := observability.NewRegistry(observability.BuildInfo{Service: "test"})
	metrics, _ := httpx.NewMetrics(reg)
	mux := httpx.NewMux(metrics, logger)
	mux.ForService("devices").HandleFunc("POST /api/v1/sites", func(w http.ResponseWriter, r *http.Request) {
		rctx := observability.WithTenant(r.Context(), tenant.String())
		logger.InfoContext(rctx, "site created")
		err := db.PlatformTx(rctx, func(tx pgx.Tx) error {
			return outbox.Insert(rctx, tx, "ttest", outbox.Event{Type: "horus.devices.site.created", Source: "horus/devices",
				TenantID: &tenant, AggregateType: "site", AggregateID: agg, AggregateVersion: 1, Data: map[string]any{"id": agg}})
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	srv := httptest.NewServer(mux.Handler())
	defer srv.Close()
	clientTrace := observability.NewTraceID()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/v1/sites", nil)
	req.Header.Set("traceparent", observability.FormatTraceParent(clientTrace, observability.NewSpanID()))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if tid, _, ok := observability.ParseTraceParent(resp.Header.Get("traceparent")); !ok || tid != clientTrace {
		t.Fatalf("response traceparent %q", resp.Header.Get("traceparent"))
	}
	var rowTP string
	if err := db.Pool.QueryRow(ctx, `SELECT payload->>'trace_parent' FROM ttest.outbox`).Scan(&rowTP); err != nil {
		t.Fatal(err)
	}
	if tid, _, ok := observability.ParseTraceParent(rowTP); !ok || tid != clientTrace {
		t.Fatalf("outbox trace_parent %q", rowTP)
	}

	// 2. Relay → NATS → 3. consumidor.
	type seen struct {
		header, traceID, tenant, event string
		envelopeTP                     string
	}
	got := make(chan seen, 1)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_ = natsx.Consume(cctx, js, natsx.ConsumerConfig{Service: "test", Stream: "DEVICES_EVENTS", Durable: "test-trace-" + uuid.NewString()[:8],
			FilterSubjects: []string{"horus.devices.site.created." + agg.String()}, Logger: logger},
			func(hctx context.Context, m *natsx.Message) error {
				logger.InfoContext(hctx, "projection updated", slog.String("subject", m.Subject))
				tid, _ := observability.TraceFrom(hctx)
				s := seen{header: m.Header.Get("traceparent"), traceID: tid, tenant: observability.TenantFrom(hctx),
					event: observability.EventIDFrom(hctx)}
				if m.Envelope.TraceParent != nil {
					s.envelopeTP = *m.Envelope.TraceParent
				}
				got <- s
				return nil
			})
	}()
	if n, err := (&natsx.Relay{DB: db, Schema: "ttest", JS: js}).Once(ctx); err != nil || n != 1 {
		t.Fatalf("relay n=%d err=%v", n, err)
	}
	var s seen
	select {
	case s = <-got:
	case <-time.After(15 * time.Second):
		t.Fatal("message not consumed")
	}
	if hdr, _, _ := observability.ParseTraceParent(s.header); hdr != clientTrace {
		t.Fatalf("NATS header traceparent %q", s.header)
	}
	if env, _, _ := observability.ParseTraceParent(s.envelopeTP); env != clientTrace {
		t.Fatalf("envelope trace_parent %q", s.envelopeTP)
	}
	if s.traceID != clientTrace || s.tenant != tenant.String() || s.event == "" {
		t.Fatalf("consumer context: %+v", s)
	}
	// Los logs del handler HTTP y del consumidor comparten trace_id.
	var httpLine, consumerLine map[string]any
	for _, l := range logs.lines() {
		switch l["msg"] {
		case "site created":
			httpLine = l
		case "projection updated":
			consumerLine = l
		}
	}
	if httpLine == nil || consumerLine == nil {
		t.Fatal("log lines missing")
	}
	if httpLine["trace_id"] != clientTrace || consumerLine["trace_id"] != clientTrace {
		t.Fatalf("trace_id in logs: http=%v consumer=%v", httpLine["trace_id"], consumerLine["trace_id"])
	}
	if consumerLine["event_id"] != s.event || consumerLine["tenant_id"] != tenant.String() {
		t.Fatalf("consumer log correlation: %v", consumerLine)
	}
	if httpLine["span_id"] == consumerLine["span_id"] {
		t.Fatal("consumer must open its own span")
	}
}
