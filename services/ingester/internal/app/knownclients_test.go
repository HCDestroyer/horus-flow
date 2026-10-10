package app

import (
	"context"
	"encoding/json"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus/flowbustest"
	"github.com/hcdestroyer/horus-flow/packages/go/flowstate"
)

// TestKnownClientsAfterRestart (D23): tras reiniciar el ingester el conjunto
// de clientes conocidos se reconstruye (antes era un durable con el estado
// en memoria y se reenviaban first_seen de todos los clientes ya conocidos),
// también si los eventos ya caducaron en DEVICES_EVENTS (instantánea).
func TestKnownClientsAfterRestart(t *testing.T) {
	nc, err := nats.Connect(flowbustest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stream, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "DEVICES_EVENTS", Subjects: []string{"horus.devices.>"}})
	if err != nil {
		t.Fatal(err)
	}
	// El patrón antiguo: un durable con todo confirmado ya no reentregaría nada.
	if _, err := js.CreateOrUpdateConsumer(ctx, "DEVICES_EVENTS", jetstream.ConsumerConfig{Durable: KnownClientsConsumer,
		AckPolicy: jetstream.AckExplicitPolicy}); err != nil {
		t.Fatal(err)
	}
	tenant, realm := uuid.New(), uuid.New()
	publish := func(verb, addr string) {
		d, _ := json.Marshal(map[string]any{"address": addr, "realm_id": realm})
		b, _ := json.Marshal(map[string]any{"type": "horus.devices.customer." + verb, "tenant_id": tenant.String(), "data": json.RawMessage(d)})
		if _, err := js.Publish(ctx, "horus.devices.customer."+verb+"."+uuid.NewString(), b); err != nil {
			t.Fatal(err)
		}
	}
	publish("discovered", "10.20.0.5")
	publish("discovered", "10.20.0.6")
	publish("discovered", "10.20.0.7")
	publish("purged", "10.20.0.7")

	states := &flowstate.MemStore{}
	run := func() (*Discovery, func()) {
		d := NewDiscovery(DiscoveryOptions{}, &memPub{}, nil, nil)
		rctx, rcancel := context.WithCancel(ctx)
		ready, done := make(chan struct{}), make(chan struct{})
		go func() { defer close(done); d.RunKnownClients(rctx, js, states, nil, ready) }()
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("known clients not ready")
		}
		return d, func() { rcancel(); <-done }
	}
	known := func(d *Discovery, addr string) bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		r := d.realms[realm]
		if r == nil {
			return false
		}
		_, ok := r.known[netip.MustParseAddr(addr)]
		return ok
	}
	d, stop := run()
	if !known(d, "10.20.0.5") || !known(d, "10.20.0.6") || known(d, "10.20.0.7") {
		t.Fatal("known set not built from DEVICES_EVENTS")
	}
	stop()
	if _, err := js.Consumer(ctx, "DEVICES_EVENTS", KnownClientsConsumer); err == nil {
		t.Fatal("legacy durable not removed")
	}

	// Caducan los eventos (max_age) y llega un cliente nuevo.
	if err := stream.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	publish("discovered", "10.20.0.8")
	d, stop = run()
	defer stop()
	for _, a := range []string{"10.20.0.5", "10.20.0.6", "10.20.0.8"} {
		if !known(d, a) {
			t.Fatalf("%s not known after restart", a)
		}
	}
	if known(d, "10.20.0.7") {
		t.Fatal("purged client known after restart")
	}
}
