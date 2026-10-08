package testkit_test

import (
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/testkit"
)

func TestLoggerCapturesJSON(t *testing.T) {
	t.Parallel()
	l, buf := testkit.Logger(t)
	l.Debug("one", "k", 1)
	l.Info("two")
	if recs := buf.Records(t); len(recs) != 2 || recs[0]["k"] != float64(1) {
		t.Fatalf("records = %v", recs)
	}
	if got := buf.Find(t, "two"); len(got) != 1 || got[0]["level"] != "info" {
		t.Fatalf("find = %v", got)
	}
}

func TestEnviron(t *testing.T) {
	t.Parallel()
	if got := testkit.Environ("A", "1", "B", "", "odd"); !slices.Equal(got, []string{"A=1", "B="}) {
		t.Fatalf("Environ = %v", got)
	}
}

func TestEventually(t *testing.T) {
	t.Parallel()
	var n atomic.Int32
	testkit.Eventually(t, time.Second, func() bool { return n.Add(1) >= 3 }, "contador")
}
