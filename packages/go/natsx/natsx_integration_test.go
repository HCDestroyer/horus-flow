//go:build integration

package natsx_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx/natstest"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
)

func TestRelayPublishesOutboxWithTenantHeader(t *testing.T) {
	ctx := context.Background()
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: pgtest.New(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Pool.Exec(ctx, `CREATE SCHEMA rtest; CREATE TABLE rtest.outbox (
		id uuid PRIMARY KEY, seq bigint GENERATED ALWAYS AS IDENTITY, tenant_id uuid, subject text NOT NULL,
		aggregate_id uuid NOT NULL, headers jsonb NOT NULL DEFAULT '{}', payload jsonb NOT NULL,
		occurred_at timestamptz NOT NULL, published_at timestamptz, attempts integer NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	_, js := natstest.New(t)
	tenant := uuid.Must(uuid.NewV7())
	agg := uuid.Must(uuid.NewV7())
	if err := db.PlatformTx(ctx, func(tx pgx.Tx) error {
		for i := 1; i <= 3; i++ {
			if err := outbox.Insert(ctx, tx, "rtest", outbox.Event{
				Type: "horus.devices.site.updated", Source: "horus/devices", TenantID: &tenant, AggregateType: "site",
				AggregateID: agg, AggregateVersion: i, Data: map[string]any{"id": agg, "version": i},
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	got := make(chan *natsx.Message, 10)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	durable := "test-relay-" + uuid.NewString()[:8]
	go func() {
		_ = natsx.Consume(cctx, js, natsx.ConsumerConfig{Service: "test", Stream: "DEVICES_EVENTS", Durable: durable,
			FilterSubjects: []string{"horus.devices.site.updated." + agg.String()}}, func(_ context.Context, m *natsx.Message) error {
			got <- m
			return nil
		})
	}()
	r := &natsx.Relay{DB: db, Schema: "rtest", JS: js}
	n, err := r.Once(ctx)
	if err != nil || n != 3 {
		t.Fatalf("relay: n=%d err=%v", n, err)
	}
	if n, _ := r.Once(ctx); n != 0 {
		t.Fatalf("segunda pasada publicó %d", n)
	}
	for i := 1; i <= 3; i++ {
		select {
		case m := <-got:
			if m.Tenant != tenant || m.Header.Get(natsx.HeaderTenant) != tenant.String() {
				t.Fatalf("tenant = %s / %s", m.Tenant, m.Header.Get(natsx.HeaderTenant))
			}
			if m.Envelope.AggregateVersion != i {
				t.Fatalf("orden: versión %d, quiero %d", m.Envelope.AggregateVersion, i)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("mensaje %d no recibido", i)
		}
	}
	var pending int
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM rtest.outbox WHERE published_at IS NULL`).Scan(&pending)
	if pending != 0 {
		t.Fatalf("%d filas sin marcar", pending)
	}
}

func TestConsumeRejectsTenantMismatchToDLQ(t *testing.T) {
	ctx := context.Background()
	nc, js := natstest.New(t)
	durable := "test-dlq-" + uuid.NewString()[:8]
	agg := uuid.Must(uuid.NewV7())
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	dlq := make(chan *nats.Msg, 4)
	sub, err := nc.ChanSubscribe("horus.dlq.test."+durable, dlq)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	// Cabecera de B con sobre de A → DLQ; sin cabecera → DLQ; correcto → handler.
	env := natsx.Envelope{Type: "horus.devices.site.created", Source: "horus/devices", Subject: agg.String(), TenantID: &a, Data: []byte(`{}`)}
	if err := natsx.Publish(ctx, js, &env); err != nil {
		t.Fatal(err)
	}
	bad := nats.NewMsg("horus.devices.site.created." + agg.String())
	bad.Data = []byte(`{"id":"` + uuid.NewString() + `","type":"horus.devices.site.created","tenant_id":"` + a.String() + `","data":{}}`)
	bad.Header.Set(natsx.HeaderTenant, b.String())
	if _, err := js.PublishMsg(ctx, bad); err != nil {
		t.Fatal(err)
	}
	none := nats.NewMsg("horus.devices.site.created." + agg.String())
	none.Data = bad.Data
	if _, err := js.PublishMsg(ctx, none); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var handled []uuid.UUID
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_ = natsx.Consume(cctx, js, natsx.ConsumerConfig{Service: "test", Stream: "DEVICES_EVENTS", Durable: durable,
			FilterSubjects: []string{"horus.devices.site.created." + agg.String()}}, func(_ context.Context, m *natsx.Message) error {
			mu.Lock()
			handled = append(handled, m.Tenant)
			mu.Unlock()
			return nil
		})
	}()
	for i := 0; i < 2; i++ {
		select {
		case m := <-dlq:
			if m.Header.Get("Horus-Dlq-Error") == "" {
				t.Fatal("DLQ sin causa")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("mensaje inválido no llegó a la DLQ")
		}
	}
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(handled) != 1 || handled[0] != a {
		t.Fatalf("handled = %v", handled)
	}
}

func TestConsumePermanentErrorGoesToDLQ(t *testing.T) {
	ctx := context.Background()
	nc, js := natstest.New(t)
	durable := "test-perm-" + uuid.NewString()[:8]
	agg := uuid.Must(uuid.NewV7())
	a := uuid.Must(uuid.NewV7())
	dlq := make(chan *nats.Msg, 1)
	sub, _ := nc.ChanSubscribe("horus.dlq.test."+durable, dlq)
	defer func() { _ = sub.Unsubscribe() }()
	env := natsx.Envelope{Type: "horus.devices.site.deleted", Source: "horus/devices", Subject: agg.String(), TenantID: &a, Data: []byte(`{}`)}
	if err := natsx.Publish(ctx, js, &env); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_ = natsx.Consume(cctx, js, natsx.ConsumerConfig{Service: "test", Stream: "DEVICES_EVENTS", Durable: durable,
			FilterSubjects: []string{"horus.devices.site.deleted." + agg.String()}}, func(context.Context, *natsx.Message) error {
			return natsx.Permanent(errors.New("invariante violada"))
		})
	}()
	select {
	case <-dlq:
	case <-time.After(10 * time.Second):
		t.Fatal("error permanente sin DLQ")
	}
	_ = jetstream.ErrConsumerNotFound
}
