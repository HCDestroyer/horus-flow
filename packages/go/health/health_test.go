package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/health"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func get(t *testing.T, h http.Handler, target string) (int, health.Report) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	var rep health.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("respuesta no JSON: %v: %s", err, rec.Body.String())
	}
	return rec.Code, rep
}

func ok(context.Context) error { return nil }

// Criterio de aceptación 3 de I0-04: una dependencia que no responde degrada
// solo su rol; los demás siguen listos y /healthz sigue en 200.
func TestDependencyDownDegradesOnlyItsRole(t *testing.T) {
	t.Parallel()
	reg := health.NewRegistry(health.Options{Service: "horus", Version: "v1", Timeout: 20 * time.Millisecond})
	analytics := reg.Role("analytics")
	analytics.AddCheck(health.Check{Name: "postgres", Critical: true, Probe: ok})
	analytics.AddCheck(health.Check{Name: "clickhouse", Probe: func(ctx context.Context) error {
		<-ctx.Done() // no responde
		return ctx.Err()
	}})
	analytics.SetState(health.StateRunning)
	devices := reg.Role("devices")
	devices.AddCheck(health.Check{Name: "postgres", Critical: true, Probe: ok})
	devices.SetState(health.StateRunning)

	code, rep := get(t, reg.ReadyHandler(), "/readyz")
	if code != http.StatusOK || rep.Status != health.StatusDegraded || !rep.Ready {
		t.Fatalf("readyz = %d %+v", code, rep)
	}
	a := rep.Roles["analytics"]
	if a.Status != health.StatusDegraded || !a.Ready || a.Checks["clickhouse"].Status != health.CheckFail ||
		a.Checks["clickhouse"].Error == "" || a.Checks["postgres"].Status != health.CheckOK {
		t.Fatalf("analytics = %+v", a)
	}
	if d := rep.Roles["devices"]; d.Status != health.StatusOK || !d.Ready {
		t.Fatalf("devices = %+v", d)
	}
	rec := httptest.NewRecorder()
	reg.LiveHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
}

func TestCriticalFailureMakesProcessUnavailable(t *testing.T) {
	t.Parallel()
	reg := health.NewRegistry(health.Options{})
	auth := reg.Role("auth")
	auth.AddCheck(health.Check{Name: "postgres", Critical: true, Probe: func(context.Context) error {
		return errors.New("connection refused")
	}})
	auth.SetState(health.StateRunning)
	devices := reg.Role("devices")
	devices.SetState(health.StateRunning)

	code, rep := get(t, reg.ReadyHandler(), "/readyz")
	if code != http.StatusServiceUnavailable || rep.Status != health.StatusUnavailable {
		t.Fatalf("readyz = %d %+v", code, rep)
	}
	if rep.Roles["auth"].Status != health.StatusUnavailable || rep.Roles["devices"].Status != health.StatusOK {
		t.Fatalf("roles = %+v", rep.Roles)
	}
	code, rep = get(t, reg.ReadyHandler(), "/readyz?role=devices")
	if code != http.StatusOK || len(rep.Roles) != 1 {
		t.Fatalf("readyz?role=devices = %d %+v", code, rep)
	}
	code, _ = get(t, reg.ReadyHandler(), "/readyz?role=auth")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("readyz?role=auth = %d", code)
	}
}

func TestUnknownRole(t *testing.T) {
	t.Parallel()
	reg := health.NewRegistry(health.Options{})
	rec := httptest.NewRecorder()
	reg.ReadyHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz?role=nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestLifecycleStates(t *testing.T) {
	t.Parallel()
	reg := health.NewRegistry(health.Options{})
	ro := reg.Role("jobs")
	if reg.Role("jobs") != ro || ro.Name() != "jobs" {
		t.Fatal("Role debe ser idempotente")
	}
	code, rep := get(t, reg.ReadyHandler(), "/readyz")
	if code != http.StatusServiceUnavailable || rep.Roles["jobs"].Status != health.StatusStarting {
		t.Fatalf("starting: %d %+v", code, rep)
	}
	ro.SetState(health.StateRunning)
	if code, _ := get(t, reg.ReadyHandler(), "/readyz"); code != http.StatusOK {
		t.Fatalf("running: %d", code)
	}
	reg.SetDraining()
	code, rep = get(t, reg.ReadyHandler(), "/readyz")
	if code != http.StatusServiceUnavailable || rep.Status != health.StatusDraining {
		t.Fatalf("draining: %d %+v", code, rep)
	}
	ro.SetState(health.StateStopping)
	_, rep = get(t, reg.ReadyHandler(), "/readyz")
	if rep.Roles["jobs"].Status != health.StatusStopping {
		t.Fatalf("stopping: %+v", rep)
	}
}

func TestResultsAreCached(t *testing.T) {
	t.Parallel()
	clk := &fakeClock{t: time.Unix(0, 0)}
	reg := health.NewRegistry(health.Options{Now: clk.Now, CacheTTL: 5 * time.Second})
	var calls atomic.Int32
	ro := reg.Role("snmp")
	ro.AddCheck(health.Check{Name: "targets", Critical: true, Probe: func(context.Context) error {
		calls.Add(1)
		return nil
	}})
	ro.SetState(health.StateRunning)
	ctx := context.Background()
	reg.Report(ctx)
	reg.Report(ctx)
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (cache)", calls.Load())
	}
	clk.Advance(6 * time.Second)
	reg.Report(ctx)
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2 tras expirar la caché", calls.Load())
	}
}

func TestNoCacheWhenTTLNegative(t *testing.T) {
	t.Parallel()
	reg := health.NewRegistry(health.Options{CacheTTL: -1})
	var calls atomic.Int32
	ro := reg.Role("x")
	ro.AddCheck(health.Check{Name: "dep", Probe: func(context.Context) error { calls.Add(1); return nil }})
	reg.Report(context.Background())
	reg.Report(context.Background())
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestInvalidCheckIsVisible(t *testing.T) {
	t.Parallel()
	reg := health.NewRegistry(health.Options{})
	ro := reg.Role("x")
	ro.AddCheck(health.Check{})
	ro.SetState(health.StateRunning)
	rep, _ := reg.Report(context.Background())
	c := rep.Roles["x"].Checks["invalid_check"]
	if rep.Ready || c.Status != health.CheckFail || !c.Critical {
		t.Fatalf("rep = %+v", rep)
	}
}

func TestCallerCancellationDoesNotPoisonCache(t *testing.T) {
	t.Parallel()
	reg := health.NewRegistry(health.Options{})
	ro := reg.Role("x")
	ro.AddCheck(health.Check{Name: "dep", Critical: true, Probe: func(ctx context.Context) error { return ctx.Err() }})
	ro.SetState(health.StateRunning)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rep, _ := reg.Report(ctx)
	if !rep.Ready {
		t.Fatalf("la cancelación del llamador no debe hacer fallar el chequeo: %+v", rep)
	}
}
