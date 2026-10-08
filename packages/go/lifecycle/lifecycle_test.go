package lifecycle_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/lifecycle"
)

type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(e string) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *recorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func hook(rec *recorder, name string) lifecycle.Hook {
	return lifecycle.Hook{
		Name:  name,
		Start: func(context.Context) error { rec.add("start " + name); return nil },
		Run: func(ctx context.Context) error {
			<-ctx.Done()
			rec.add("run-end " + name)
			return nil
		},
		Stop: func(context.Context) error { rec.add("stop " + name); return nil },
	}
}

func TestOrderedStartAndReverseStop(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	drained := make(chan struct{})
	m := lifecycle.New(lifecycle.Options{
		ShutdownTimeout: time.Second,
		OnDrain:         func() { rec.add("drain"); close(drained) },
	})
	m.Append(hook(rec, "a"), hook(rec, "b"), lifecycle.Hook{Name: "empty"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- m.Run(ctx) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	<-drained
	ev := rec.get()
	if !slices.Equal(ev[:3], []string{"start a", "start b", "drain"}) {
		t.Fatalf("eventos = %v", ev)
	}
	if !slices.Equal(ev[len(ev)-2:], []string{"stop b", "stop a"}) {
		t.Fatalf("stop en orden inverso esperado: %v", ev)
	}
	// Los Run terminan antes de cualquier Stop.
	firstStop := slices.Index(ev, "stop b")
	for _, e := range []string{"run-end a", "run-end b"} {
		if i := slices.Index(ev, e); i < 0 || i > firstStop {
			t.Fatalf("%s debe ocurrir antes de los Stop: %v", e, ev)
		}
	}
}

func TestStartFailureStopsStartedHooksOnly(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	boom := errors.New("boom")
	bad := lifecycle.Hook{
		Name:  "bad",
		Start: func(context.Context) error { return boom },
		Stop:  func(context.Context) error { rec.add("stop bad"); return nil },
	}
	m := lifecycle.New(lifecycle.Options{})
	m.Append(hook(rec, "a"), bad, hook(rec, "c"))
	err := m.Run(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if ev := rec.get(); !slices.Equal(ev, []string{"start a", "stop a"}) {
		t.Fatalf("eventos = %v", ev)
	}
}

func TestStartTimeout(t *testing.T) {
	t.Parallel()
	m := lifecycle.New(lifecycle.Options{StartTimeout: 10 * time.Millisecond})
	m.Append(lifecycle.Hook{Name: "slow", Start: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}})
	if err := m.Run(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestRunFailureTriggersShutdown(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	boom := errors.New("crashed")
	m := lifecycle.New(lifecycle.Options{DrainDelay: time.Hour, ShutdownTimeout: time.Second})
	m.Append(hook(rec, "a"), lifecycle.Hook{Name: "crash", Run: func(context.Context) error { return boom }})
	err := m.Run(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if ev := rec.get(); !slices.Contains(ev, "stop a") || !slices.Contains(ev, "run-end a") {
		t.Fatalf("eventos = %v", ev)
	}
}

func TestRunReturningNilEarlyIsNotFatal(t *testing.T) {
	t.Parallel()
	m := lifecycle.New(lifecycle.Options{})
	m.Append(lifecycle.Hook{Name: "oneshot", Run: func(context.Context) error { return nil }})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.Run(ctx); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestShutdownTimeoutWhenComponentHangs(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	defer close(release)
	m := lifecycle.New(lifecycle.Options{ShutdownTimeout: 20 * time.Millisecond, DrainDelay: time.Hour})
	m.Append(lifecycle.Hook{Name: "stuck", Run: func(context.Context) error { <-release; return nil }})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	err := m.Run(ctx)
	if !errors.Is(err, lifecycle.ErrShutdownTimeout) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("el plazo de apagado no se respetó")
	}
}

func TestDrainDelayIsHonoured(t *testing.T) {
	t.Parallel()
	m := lifecycle.New(lifecycle.Options{DrainDelay: 50 * time.Millisecond, ShutdownTimeout: time.Second})
	var cancelledAt time.Time
	m.Append(lifecycle.Hook{Name: "r", Run: func(ctx context.Context) error {
		<-ctx.Done()
		cancelledAt = time.Now()
		return nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	cancel()
	if err := m.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if cancelledAt.Sub(start) < 50*time.Millisecond {
		t.Fatalf("Run cancelado antes del DrainDelay: %v", cancelledAt.Sub(start))
	}
}

func TestStopErrorsAndRunErrorsDuringShutdownAreReported(t *testing.T) {
	t.Parallel()
	stopErr := errors.New("stop failed")
	runErr := errors.New("run failed on shutdown")
	m := lifecycle.New(lifecycle.Options{})
	m.Append(
		lifecycle.Hook{Name: "s", Stop: func(context.Context) error { return stopErr }},
		lifecycle.Hook{Name: "r", Run: func(ctx context.Context) error { <-ctx.Done(); return runErr }},
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := m.Run(ctx)
	if !errors.Is(err, stopErr) || !errors.Is(err, runErr) {
		t.Fatalf("err = %v", err)
	}
}
