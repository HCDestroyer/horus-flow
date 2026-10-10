package flowbus_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowbus/flowbustest"
)

func TestPublishTelemetryAndEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	nc, js, err := flowbus.Connect(flowbustest.URL(t), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	if err := flowbus.EnsureStreams(ctx, js, 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := flowbus.EnsureStreams(ctx, js, 1<<20); err != nil {
		t.Fatal("idempotent:", err)
	}
	tel := flowbus.Telemetry{Subject: flowbus.SubjectBatchPrefix + "r1", Type: flowbus.TypeBatchReceived,
		Source: "horus/flows/collector", TenantID: "t1", MsgID: "b1", ContentType: "x", Time: time.Now(), Body: []byte{1}}
	for range 2 { // el segundo se deduplica por Nats-Msg-Id
		if _, err := js.PublishMsg(ctx, tel.Msg()); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := js.Stream(ctx, flowbus.StreamTelemetry)
	info, _ := st.Info(ctx)
	if info.State.Msgs != 1 {
		t.Fatalf("msgs = %d, want 1 (dedupe)", info.State.Msgs)
	}
	msg, err := st.GetLastMsgForSubject(ctx, tel.Subject)
	if err != nil || msg.Header.Get(flowbus.HeaderTenant) != "t1" || msg.Header.Get(flowbus.HeaderType) != flowbus.TypeBatchReceived {
		t.Fatalf("headers %v %v", err, msg)
	}

	tid := uuid.New()
	if err := flowbus.Publish(ctx, js, flowbus.Event{Type: flowbus.TypeClientFirstSeen, Source: "horus/flows/ingester",
		Entity: "realm1", TenantID: &tid, AggregateType: "realm", Data: map[string]any{"x": 1}}); err != nil {
		t.Fatal(err)
	}
	ev, _ := js.Stream(ctx, flowbus.StreamEvents)
	m, err := ev.GetLastMsgForSubject(ctx, "horus.flows.client.first_seen.realm1")
	if err != nil {
		t.Fatal(err)
	}
	var env flowbus.Envelope
	if err := json.Unmarshal(m.Data, &env); err != nil || env.TenantID == nil || *env.TenantID != tid.String() ||
		m.Header.Get(flowbus.HeaderTenant) != tid.String() || env.Subject != "realm1" {
		t.Fatalf("envelope %v %+v", err, env)
	}
}

// TestIngesterConsumerUnlimitedDeliveries: el durable del ingester acepta
// MaxDeliver ilimitado con BackOff (NATS lo valida al crearlo).
func TestIngesterConsumerUnlimitedDeliveries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	nc, js, err := flowbus.Connect(flowbustest.URL(t), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	if err := flowbus.EnsureStreams(ctx, js, 1<<20); err != nil {
		t.Fatal(err)
	}
	c, err := js.CreateOrUpdateConsumer(ctx, flowbus.StreamTelemetry, flowbus.IngesterConsumerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if got := c.CachedInfo().Config.MaxDeliver; got != -1 {
		t.Fatalf("MaxDeliver = %d, want -1", got)
	}
}
