package engine

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

// statefulSink guarda engine_state en un mapa compartido entre motores
// (simula PostgreSQL entre dos arranques del proceso).
type statefulSink struct {
	fakeSink
	state    map[string]string
	failNext bool
}

func (s *statefulSink) State(_ context.Context, _ uuid.UUID, k string) (string, error) { return s.state[k], nil }

func (s *statefulSink) SetState(_ context.Context, _ uuid.UUID, k, v string) error {
	s.state[k] = v
	return nil
}

func (s *statefulSink) Apply(ctx context.Context, t uuid.UUID, c []domain.Candidate, now time.Time) (ApplyResult, error) {
	if s.failNext {
		s.failNext = false
		return ApplyResult{}, context.DeadlineExceeded
	}
	return s.fakeSink.Apply(ctx, t, c, now)
}

// windowSignals registra las ventanas que se le piden.
type windowSignals struct {
	fakeSignals
	windows [][2]time.Time
}

func (w *windowSignals) Security(_ context.Context, _ uuid.UUID, from, to time.Time, _ Having) ([]SecurityRow, error) {
	w.windows = append(w.windows, [2]time.Time{from, to})
	return nil, nil
}

// D23: tras un reinicio el motor retoma la última ventana aplicada desde
// engine_state y evalúa, sin huecos, las ventanas perdidas mientras estuvo
// parado.
func TestEngineCatchesUpAfterRestart(t *testing.T) {
	tenant := uuid.New()
	sink := &statefulSink{fakeSink: fakeSink{params: smtpOnly()}, state: map[string]string{}}
	t0 := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	en := New(&windowSignals{}, sink, nil, Options{Lag: time.Minute})
	if _, err := en.Evaluate(context.Background(), tenant, t0, false); err != nil {
		t.Fatal(err)
	}
	if sink.state[stateWindowPrefix+domain.DetSMTP] == "" {
		t.Fatal("window not persisted")
	}
	// kill -9 y 3 h parado: un motor nuevo (memoria vacía) con el mismo estado.
	sig2 := &windowSignals{}
	en2 := New(sig2, sink, nil, Options{Lag: time.Minute, CatchupSteps: 100})
	if _, err := en2.Evaluate(context.Background(), tenant, t0.Add(3*time.Hour), false); err != nil {
		t.Fatal(err)
	}
	if len(sig2.windows) < 2 {
		t.Fatalf("expected catch-up windows, got %v", sig2.windows)
	}
	prevEnd := t0.Add(-time.Minute)
	for _, w := range sig2.windows {
		if w[0].After(prevEnd) {
			t.Fatalf("gap between %v and %v", prevEnd, w[0])
		}
		prevEnd = w[1]
	}
	if want := t0.Add(3*time.Hour - time.Minute); !prevEnd.Equal(want) {
		t.Fatalf("last window ends at %v, want %v", prevEnd, want)
	}
	// Sin parada, la siguiente pasada dentro de la cadencia no reevalúa nada.
	sig2.windows = nil
	if _, err := en2.Evaluate(context.Background(), tenant, t0.Add(3*time.Hour+time.Minute), false); err != nil {
		t.Fatal(err)
	}
	if len(sig2.windows) != 0 {
		t.Fatalf("re-evaluated within cadence: %v", sig2.windows)
	}
}

// La recuperación está acotada (MaxCatchup) y avanza en varias pasadas
// (CatchupSteps) sin perder el avance entre ellas.
func TestEngineCatchupIsBounded(t *testing.T) {
	tenant := uuid.New()
	sink := &statefulSink{fakeSink: fakeSink{params: smtpOnly()}, state: map[string]string{}}
	t0 := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	if _, err := New(&windowSignals{}, sink, nil, Options{Lag: time.Minute}).Evaluate(context.Background(), tenant, t0, false); err != nil {
		t.Fatal(err)
	}
	sig := &windowSignals{}
	en := New(sig, sink, nil, Options{Lag: time.Minute, MaxCatchup: 2 * time.Hour, CatchupSteps: 4})
	now := t0.Add(48 * time.Hour)
	if _, err := en.Evaluate(context.Background(), tenant, now, false); err != nil {
		t.Fatal(err)
	}
	if len(sig.windows) != 4 {
		t.Fatalf("windows = %d, want 4", len(sig.windows))
	}
	if first := sig.windows[0][1]; first.Before(now.Add(-2*time.Hour - 2*time.Minute)) {
		t.Fatalf("catch-up started at %v, beyond MaxCatchup", first)
	}
	// Segunda pasada: continúa donde quedó.
	last := sig.windows[3][1]
	sig.windows = nil
	if _, err := en.Evaluate(context.Background(), tenant, now, false); err != nil {
		t.Fatal(err)
	}
	if len(sig.windows) == 0 || sig.windows[0][0].After(last) {
		t.Fatalf("second pass did not continue from %v: %v", last, sig.windows)
	}
}

// Si Apply falla, la ventana no se da por evaluada: la siguiente pasada la
// repite (antes se perdía aunque no hubiera reinicio).
func TestEngineDoesNotAdvanceOnApplyFailure(t *testing.T) {
	tenant := uuid.New()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	k := ClientKey{uuid.New(), netip.MustParseAddr("10.20.0.10")}
	sig := &fakeSignals{security: []SecurityRow{{Key: k, Site: uuid.New(), SMTPRemoteIPs: 500, SMTPFlows: 800, First: now.Add(-time.Hour),
		Last: now, MaxSampling: 1}}, loc: Location{Site: uuid.New(), Router: uuid.New()}}
	sink := &statefulSink{fakeSink: fakeSink{params: smtpOnly()}, state: map[string]string{}, failNext: true}
	en := New(sig, sink, nil, Options{Lag: time.Minute})
	if _, err := en.Evaluate(context.Background(), tenant, now, false); err == nil {
		t.Fatal("expected apply error")
	}
	if sink.state[stateWindowPrefix+domain.DetSMTP] != "" {
		t.Fatal("window advanced although apply failed")
	}
	if _, err := en.Evaluate(context.Background(), tenant, now, false); err != nil {
		t.Fatal(err)
	}
	if len(sink.applied) == 0 {
		t.Fatal("candidates of the failed window were lost")
	}
}

// Con el ingester atrasado (búfer de TLM_FLOWS tras una caída), el motor no
// da por evaluadas ventanas posteriores a lo ingerido: las evalúa cuando
// llegan los flujos.
func TestEngineWaitsForIngestionWatermark(t *testing.T) {
	tenant := uuid.New()
	sink := &statefulSink{fakeSink: fakeSink{params: smtpOnly()}, state: map[string]string{}}
	t0 := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	wm := t0.Add(-30 * time.Minute)
	sig := &windowSignals{}
	en := New(sig, sink, nil, Options{Lag: time.Minute, Watermark: func(context.Context) (time.Time, bool) { return wm, true }})
	if _, err := en.Evaluate(context.Background(), tenant, t0, false); err != nil {
		t.Fatal(err)
	}
	if got := sig.windows[len(sig.windows)-1][1]; !got.Equal(wm.Add(-time.Minute)) {
		t.Fatalf("window end %v, want %v (watermark - lag)", got, wm.Add(-time.Minute))
	}
	wm = t0 // el ingester se pone al día
	sig.windows = nil
	if _, err := en.Evaluate(context.Background(), tenant, t0.Add(time.Minute), false); err != nil {
		t.Fatal(err)
	}
	if len(sig.windows) == 0 || sig.windows[0][0].After(t0.Add(-31*time.Minute)) {
		t.Fatalf("held windows not evaluated after catching up: %v", sig.windows)
	}
}
