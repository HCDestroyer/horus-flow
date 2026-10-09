package app

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
)

type flakyInserter struct {
	fails, calls int
	rows         int
}

func (f *flakyInserter) Insert(_ context.Context, _ string, rows []Row) error {
	f.calls++
	if f.calls <= f.fails {
		return errors.New("clickhouse down")
	}
	f.rows += len(rows)
	return nil
}

// TestConsumerRetriesWhileClickHouseDown: el INSERT se reintenta sin soltar
// el lote (y alargando ack_wait) hasta que ClickHouse vuelve.
func TestConsumerRetriesWhileClickHouseDown(t *testing.T) {
	ins := &flakyInserter{fails: 2}
	c := &Consumer{Proc: &Processor{Inv: store(t)}, Ins: ins, M: NewMetrics(nil), Log: slog.New(slog.DiscardHandler)}
	fb := &flowpb.FlowBatch{BatchID: uuid.NewString(), TenantID: "0192e000-0000-7000-8000-000000000001",
		RouterID: "0192e333-0000-7000-8000-000000000033", ReceivedTo: time.Now(),
		Records: []flowpb.FlowRecord{{SrcIP: a("10.20.0.5"), DstIP: a("8.8.8.8"), Bytes: 1, Packets: 1}}}
	h := nats.Header{}
	h.Set(flowbus.HeaderTenant, fb.TenantID)
	progress := 0
	out, err := c.Process(context.Background(), make(chan struct{}), fb.Marshal(), h, func() { progress++ })
	if out != outAck || err != nil || ins.rows != 1 || progress != 2 {
		t.Fatalf("out=%v err=%v rows=%d progress=%d", out, err, ins.rows, progress)
	}
	h.Set(flowbus.HeaderTenant, uuid.NewString())
	if out, _ := c.Process(context.Background(), nil, fb.Marshal(), h, nil); out != outTerm {
		t.Fatalf("tenant mismatch: out=%v", out)
	}
	if out, _ := c.Process(context.Background(), nil, []byte{0xff}, h, nil); out != outTerm {
		t.Fatalf("garbage: out=%v", out)
	}
}
