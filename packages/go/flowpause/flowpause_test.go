package flowpause

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus/flowbustest"
	"github.com/hcdestroyer/horus-flow/packages/go/flowstate"
)

// TestPauseAcrossRestart: suspender con pause_ingest pausa, reanudar quita la
// pausa, y el conjunto sobrevive a un reinicio aunque los eventos caduquen.
func TestPauseAcrossRestart(t *testing.T) {
	nc, err := nats.Connect(flowbustest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stream, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "AUTH_EVENTS", Subjects: []string{"horus.auth.>"}})
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	pub := func(verb string, id uuid.UUID, v int, pause bool) {
		body, _ := json.Marshal(map[string]any{"type": "horus.auth.tenant." + verb, "aggregate_version": v,
			"data": map[string]any{"id": id, "version": v, "pause_ingest": pause}})
		if _, err := js.Publish(ctx, "horus.auth.tenant."+verb+"."+id.String(), body); err != nil {
			t.Fatal(err)
		}
	}
	pub("suspended", a, 2, true)
	pub("suspended", b, 2, true)
	pub("resumed", b, 3, false)
	pub("suspended", c, 2, false) // suspendido sin pausar la ingesta
	states := &flowstate.MemStore{}
	run := func() (*Set, func()) {
		s := New()
		rctx, rcancel := context.WithCancel(ctx)
		ready, done := make(chan struct{}), make(chan struct{})
		go func() { defer close(done); s.Run(rctx, js, states, "flows-pause-test", nil, ready) }()
		<-ready
		return s, func() { rcancel(); <-done }
	}
	s, stop := run()
	if !s.Paused(a) || s.Paused(b) || s.Paused(c) {
		t.Fatalf("paused a=%v b=%v c=%v", s.Paused(a), s.Paused(b), s.Paused(c))
	}
	stop()
	if err := stream.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	pub("suspended", b, 4, true)
	s, stop = run()
	defer stop()
	if !s.Paused(a) || !s.Paused(b) || s.Paused(c) {
		t.Fatalf("after restart paused a=%v b=%v c=%v", s.Paused(a), s.Paused(b), s.Paused(c))
	}
}
