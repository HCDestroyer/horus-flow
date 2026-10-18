package app

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
)

const switchSampleSwitch = "0192e333-0000-7000-8000-0000000000b2"

func switchStore(t *testing.T) *flowinv.Store {
	t.Helper()
	s, err := flowinv.Parse([]byte(strings.Replace(invYAML, "client_prefixes:", `  - tenant_id: 0192e000-0000-7000-8000-000000000001
    router_id: `+switchSampleSwitch+`
    site_id: 0192e222-0000-7000-8000-000000000022
    tunnel_ip: 10.20.0.5
    direct: true
    secondary: true
client_prefixes:`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	return flowinv.NewStore(s)
}

type memSwitchIns struct {
	rows []SwitchSample
	err  error
}

func (m *memSwitchIns) InsertSwitchSamples(_ context.Context, _ string, rows []SwitchSample) error {
	if m.err != nil {
		return m.err
	}
	m.rows = append(m.rows, rows...)
	return nil
}

func switchMsg(router string, tenant string) *fakeMsg {
	fb := &flowpb.FlowBatch{BatchID: uuid.Must(uuid.NewV7()).String(), TenantID: testTenant, RouterID: router, ReceivedTo: time.Now()}
	fb.Records = append(fb.Records, flowpb.FlowRecord{SrcIP: a("10.20.1.9"), DstIP: a("8.8.8.8"), Protocol: 6, DstPort: 443, Bytes: 1000,
		Packets: 2, SamplingRate: 4096, InputIfIndex: 7, TS: time.Now()})
	h := nats.Header{}
	h.Set(flowbus.HeaderTenant, tenant)
	return &fakeMsg{data: fb.Marshal(), hdr: h, delivered: 1}
}

// Las muestras de un switch solo van a flows.switch_samples, escaladas por la tasa; el exportador debe ser
// secundario: un lote de un router primario por este camino se descarta (nunca se mezclan las fuentes).
func TestSwitchSampleConsumer(t *testing.T) {
	t.Parallel()
	ins := &memSwitchIns{}
	c := &SwitchSampleConsumer{Inv: switchStore(t), Ins: ins, Log: slog.New(slog.DiscardHandler)}
	m := switchMsg(switchSampleSwitch, testTenant)
	c.Handle(context.Background(), m)
	if !m.isAcked() || len(ins.rows) != 1 {
		t.Fatalf("muestra: acked=%v filas=%d", m.isAcked(), len(ins.rows))
	}
	r := ins.rows[0]
	if r.Bytes != 1000*4096 || r.Packets != 2*4096 || r.InputIfIndex != 7 || r.SwitchID.String() != switchSampleSwitch ||
		r.SiteID.String() != "0192e222-0000-7000-8000-000000000022" {
		t.Fatalf("fila: %+v", r)
	}
	// Router primario por el camino secundario: se termina, no se escribe.
	prim := switchMsg("0192e333-0000-7000-8000-000000000033", testTenant)
	c.Handle(context.Background(), prim)
	if prim.trm != 1 || len(ins.rows) != 1 {
		t.Fatalf("router primario: term=%d filas=%d", prim.trm, len(ins.rows))
	}
	// Horus-Tenant distinto del lote: se termina.
	other := switchMsg(switchSampleSwitch, "0192e000-0000-7000-8000-000000000002")
	c.Handle(context.Background(), other)
	if other.trm != 1 {
		t.Fatal("tenant cruzado aceptado")
	}
	// Switch aún desconocido: se reintenta.
	unk := switchMsg(uuid.NewString(), testTenant)
	c.Handle(context.Background(), unk)
	if unk.naks != 1 {
		t.Fatal("switch desconocido: se esperaba nak")
	}
	// ClickHouse caído: nak (sin perder el lote).
	ins.err = errors.New("down")
	again := switchMsg(switchSampleSwitch, testTenant)
	c.Handle(context.Background(), again)
	if again.naks != 1 || again.isAcked() {
		t.Fatal("inserción fallida debe reintentarse")
	}
	if len(SwitchSampleValues(&r)) != len(SwitchSampleColumns) {
		t.Fatal("columnas")
	}
}

// El procesador de flows_raw rechaza los switches secundarios aunque su lote llegara por el subject
// primario (defensa en profundidad: nunca se atribuye ni crea clientes con muestras de un switch).
func TestProcessorRejectsSecondary(t *testing.T) {
	t.Parallel()
	p := &Processor{Inv: switchStore(t)}
	fb := &flowpb.FlowBatch{BatchID: uuid.Must(uuid.NewV7()).String(), TenantID: testTenant, RouterID: switchSampleSwitch, ReceivedTo: time.Now(),
		Records: []flowpb.FlowRecord{{SrcIP: a("10.20.1.9"), DstIP: a("8.8.8.8"), Bytes: 10, Packets: 1}}}
	if _, err := p.Rows(fb, testTenant); !errors.Is(err, ErrPermanent) {
		t.Fatalf("lote de switch en flows_raw: %v", err)
	}
}
