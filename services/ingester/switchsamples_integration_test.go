//go:build integration

package ingester

import (
	"context"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/chmigrate"
	"github.com/hcdestroyer/horus-flow/packages/go/chtest"
	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx/natstest"
	chadapter "github.com/hcdestroyer/horus-flow/services/ingester/internal/adapters/clickhouse"
	"github.com/hcdestroyer/horus-flow/services/ingester/internal/app"
)

// Fuente secundaria de punta a punta (docs/traffic-model.md §13): un lote de un switch con sFlow publicado
// en horus.telemetry.flows.secondary.<id> (JetStream real) lo consume el durable flows-switch-samples y lo
// escribe en flows.switch_samples (ClickHouse real) escalado por la tasa; el durable flows-ingester (el de
// flows_raw, atribución y clientes) nunca lo ve, y flows_raw y sus agregados siguen vacíos.
func TestSwitchSamplesEndToEnd(t *testing.T) {
	_, js := natstest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	srv := chtest.Start(t, func(ctx context.Context, dsn, pw string) error {
		return MigrateClickHouse(ctx, []string{"HORUS_CLICKHOUSE_DSN=" + dsn, "HORUS_CLICKHOUSE_PASSWORD=" + pw},
			slog.New(slog.DiscardHandler))
	})
	w, err := chadapter.OpenWriter(srv.DSN, "", srv.Password)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	tenant, site, sw := uuid.New(), uuid.New(), uuid.New()
	inv, err := flowinv.New(flowinv.Data{Exporters: []flowinv.Exporter{{TenantID: tenant, RouterID: sw, SiteID: site, Name: "sw-1",
		TunnelIP: mustAddr("10.20.0.5"), Direct: true, Secondary: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := flowbus.EnsureStreams(ctx, js, 1<<30); err != nil && !strings.Contains(err.Error(), "already") {
		t.Fatal(err)
	}
	prim, err := js.CreateOrUpdateConsumer(ctx, flowbus.StreamTelemetry, flowbus.IngesterConsumerConfig())
	if err != nil {
		t.Fatal(err)
	}
	sec, err := js.CreateOrUpdateConsumer(ctx, flowbus.StreamTelemetry, flowbus.SwitchSamplesConsumerConfig())
	if err != nil {
		t.Fatal(err)
	}
	primBefore, _ := prim.Info(ctx)

	fb := &flowpb.FlowBatch{BatchID: uuid.Must(uuid.NewV7()).String(), TenantID: tenant.String(), RouterID: sw.String(), ReceivedTo: time.Now(),
		Records: []flowpb.FlowRecord{{SrcIP: mustAddr("10.20.1.9"), DstIP: mustAddr("8.8.8.8"), Protocol: 6, DstPort: 443, Bytes: 1500,
			Packets: 1, SamplingRate: 4096, InputIfIndex: 54, TS: time.Now()}}}
	msg := flowbus.Telemetry{Subject: flowbus.SubjectSecondaryPrefix + sw.String(), Type: flowbus.TypeSecondaryBatchReceived,
		Source: "horus/flows/collector", TenantID: tenant.String(), MsgID: fb.BatchID, ContentType: flowpb.ContentTypeFlowBatch,
		Time: fb.ReceivedTo, Body: fb.Marshal()}.Msg()
	if _, err := js.PublishMsg(ctx, msg); err != nil {
		t.Fatal(err)
	}
	c := &app.SwitchSampleConsumer{Inv: flowinv.NewStore(inv), Ins: w, Log: slog.New(slog.DiscardHandler)}
	m, err := sec.Next(jetstream.FetchMaxWait(10 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	c.Handle(ctx, m)

	admin, err := chmigrate.Open(srv.DSN, srv.Password)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	var n, bytes uint64
	if err := admin.QueryRowContext(ctx, `SELECT count(), sum(bytes) FROM flows.switch_samples WHERE switch_id = ?`, sw).Scan(&n, &bytes); err != nil ||
		n != 1 || bytes != 1500*4096 {
		t.Fatalf("switch_samples: %d %d %v", n, bytes, err)
	}
	var raw uint64
	if err := admin.QueryRowContext(ctx, `SELECT count() FROM flows.flows_raw`).Scan(&raw); err != nil || raw != 0 {
		t.Fatalf("flows_raw con muestras de switch: %d %v", raw, err)
	}
	primAfter, err := prim.Info(ctx)
	if err != nil || primAfter.NumPending != primBefore.NumPending {
		t.Fatalf("el durable flows-ingester recibió el lote secundario: %+v → %+v %v", primBefore.NumPending, primAfter.NumPending, err)
	}
}

func mustAddr(s string) netip.Addr { return netip.MustParseAddr(s) }
