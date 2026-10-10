package observability_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

func TestLoggerHomogeneousKeysAndCause(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l, _, err := observability.NewLogger(&buf, observability.LogConfig{Service: "horus"})
	if err != nil {
		t.Fatal(err)
	}
	root := errors.New("dial tcp 10.0.0.1:5432: connection refused")
	wrapped := fmt.Errorf("pgdb: begin: %w", root)
	ctx := observability.WithEventID(context.Background(), "0192f1c2-7d1e-7c3a-9e55-1f2a3b4c5d6e")
	ctx = observability.WithRouter(ctx, "router-1")
	l.WarnContext(ctx, "detector failed", "err", wrapped, "tenant", "t-1")
	m := decodeLines(t, &buf)[0]
	if m["error"] != wrapped.Error() || m["error_cause"] != root.Error() || m["error_type"] != "*errors.errorString" {
		t.Fatalf("error fields: %v", m)
	}
	if _, ok := m["err"]; ok {
		t.Fatal("err not renamed")
	}
	if m["tenant_id"] != "t-1" || m["event_id"] != "0192f1c2-7d1e-7c3a-9e55-1f2a3b4c5d6e" || m["router_id"] != "router-1" {
		t.Fatalf("correlation fields: %v", m)
	}
	if strings.Count(buf.String(), `"tenant_id"`) != 1 {
		t.Fatalf("duplicated tenant_id: %s", buf.String())
	}
	// Sin contexto de evento/router esas claves no aparecen.
	buf.Reset()
	l.Info("plain")
	if strings.Contains(buf.String(), "event_id") || strings.Contains(buf.String(), "router_id") {
		t.Fatalf("optional keys without context: %s", buf.String())
	}
}

func TestTraceParent(t *testing.T) {
	t.Parallel()
	tid, sid := observability.NewTraceID(), observability.NewSpanID()
	tp := observability.FormatTraceParent(tid, sid)
	gotT, gotS, ok := observability.ParseTraceParent(tp)
	if !ok || gotT != tid || gotS != sid {
		t.Fatalf("round trip %s", tp)
	}
	for _, bad := range []string{"", "01-" + tid + "-" + sid + "-01", "00-" + strings.Repeat("0", 32) + "-" + sid + "-01", "00-XYZ-" + sid + "-01"} {
		if _, _, ok := observability.ParseTraceParent(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
	ctx := observability.ContinueTrace(context.Background(), tp)
	ct, cs := observability.TraceFrom(ctx)
	if ct != tid || cs == sid || len(cs) != 16 {
		t.Fatalf("continue: %s %s", ct, cs)
	}
	if observability.TraceParentFrom(ctx) != observability.FormatTraceParent(tid, cs) {
		t.Fatal("TraceParentFrom")
	}
	if c2 := observability.EnsureTrace(ctx); observability.TraceParentFrom(c2) != observability.TraceParentFrom(ctx) {
		t.Fatal("EnsureTrace replaced an existing trace")
	}
	if tid2, _ := observability.TraceFrom(observability.EnsureTrace(context.Background())); len(tid2) != 32 {
		t.Fatal("EnsureTrace without trace")
	}
}
