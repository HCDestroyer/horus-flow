package app

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// TestPublisherDrainsBatchEnqueuedAtShutdown: un lote encolado justo antes de
// parar se publica igual (Engine.Run hace el Flush final y para el publicador en
// seguida). El publicador ya vio la cola vacía cuando llegan a la vez el aviso
// del lote y la cancelación: su select elegía al azar y podía salir sin
// publicarlo (nocturna 28: N=1 sin el último lote de 88 registros).
func TestPublisherDrainsBatchEnqueuedAtShutdown(t *testing.T) {
	for i := range 300 {
		sink := &fakeSink{}
		p := NewPublisher(sink, 1<<20, "c1", NewMetrics(nil), slog.New(slog.DiscardHandler))
		ctx, cancel := context.WithCancel(context.Background())
		var once sync.Once
		p.idleHook = func() {
			once.Do(func() {
				if !p.Enqueue(batchMsg(i), 1) {
					t.Error("rejected")
				}
				cancel()
			})
		}
		done := make(chan struct{})
		go func() { defer close(done); p.Run(ctx, time.Second) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("el publicador no paró")
		}
		sink.mu.Lock()
		got := len(sink.msgs)
		sink.mu.Unlock()
		if got != 1 {
			t.Fatalf("iteración %d: publicados %d, esperado 1", i, got)
		}
	}
}
