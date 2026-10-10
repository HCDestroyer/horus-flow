package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/platformevents"
	"github.com/hcdestroyer/horus-flow/services/collector/internal/spool"
)

type fakeEmitter struct {
	mu  sync.Mutex
	evs []platformevents.Event
}

func (f *fakeEmitter) Record(_ context.Context, ev platformevents.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.evs = append(f.evs, ev)
}

func (f *fakeEmitter) kinds() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int{}
	for _, e := range f.evs {
		out[e.Kind+"/"+e.Severity]++
	}
	return out
}

// TestSpoolPlatformEvents: el registro de eventos de plataforma recibe
// spool_active al pasar a disco y spool_drained al vaciarse, y spool_active
// con severidad error si el spool se llena y descarta.
func TestSpoolPlatformEvents(t *testing.T) {
	em := &fakeEmitter{}
	platformevents.SetDefault(em)
	defer platformevents.SetDefault(nil)
	sink := &fakeSink{fail: true}
	r := startSpooled(t, t.TempDir(), sink, 0, spool.Options{MaxBytes: 1 << 16, SegmentBytes: 1 << 13})
	defer r.stop()
	r.p.Enqueue(batchMsg(0), 10)
	time.Sleep(300 * time.Millisecond)
	for i := 1; i < 800; i++ {
		r.p.Enqueue(batchMsg(i), 10)
	}
	sink.setFail(false)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		k := em.kinds()
		if k["spool_active/warn"] == 1 && k["spool_drained/info"] == 1 && k["spool_active/error"] == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("platform events %v", em.kinds())
}
