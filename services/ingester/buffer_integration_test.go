//go:build integration

package ingester

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx/natstest"
	"github.com/hcdestroyer/horus-flow/services/ingester/internal/app"
)

// La sonda del búfer TLM_FLOWS corre en su goroutine mientras app.Consumer.Run
// llama a Info() del consumidor que consume. Antes la sonda usaba ese mismo
// objeto y -race lo detectaba (jetstream.pullConsumer.Info escribe sin
// cerrojo); ahora pide la info con un objeto propio.
func TestTelemetryBufferStateConcurrentWithConsumerInfo(t *testing.T) {
	_, js := natstest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := "buf-test-" + uuid.NewString()[:8]
	subject := flowbus.SubjectBatchPrefix + name
	cons, err := js.CreateOrUpdateConsumer(ctx, flowbus.StreamTelemetry, jetstream.ConsumerConfig{
		Durable: name, FilterSubjects: []string{subject}, AckPolicy: jetstream.AckExplicitPolicy,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = js.DeleteConsumer(context.Background(), flowbus.StreamTelemetry, name) })
	for range 3 {
		if _, err := js.Publish(ctx, subject, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	wg.Go(func() { // como el ticker de lag de app.Consumer.Run
		for range 50 {
			if _, err := cons.Info(ctx); err != nil {
				t.Error(err)
				return
			}
		}
	})
	var st app.BufferState
	wg.Go(func() {
		for range 50 {
			s, err := telemetryBufferState(ctx, js, name)
			if err != nil {
				t.Error(err)
				return
			}
			st = s
		}
	})
	wg.Wait()
	if st.Pending != 3 {
		t.Fatalf("Pending = %d, quería 3", st.Pending)
	}
	if st.Msgs < 3 {
		t.Fatalf("Msgs = %d, quería ≥ 3", st.Msgs)
	}
}
