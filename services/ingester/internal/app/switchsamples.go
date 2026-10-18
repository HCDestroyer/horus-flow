package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
)

// SwitchSample es una fila de flows.switch_samples: una muestra sFlow de un
// switch (fuente SECUNDARIA, docs/traffic-model.md §13). Sin atribución,
// enriquecimiento ni descubrimiento: nunca crea clientes ni suma al tráfico
// de clientes o nodos; solo sirve para el top por puerto de la ficha del
// switch. Bytes y paquetes ya multiplicados por la tasa de muestreo.
type SwitchSample struct {
	TenantID      uuid.UUID
	SiteID        uuid.UUID
	SwitchID      uuid.UUID
	TS            time.Time
	InputIfIndex  uint32
	OutputIfIndex uint32
	SrcIP         netip.Addr
	DstIP         netip.Addr
	Protocol      uint8
	SrcPort       uint16
	DstPort       uint16
	Bytes         uint64
	Packets       uint64
	SamplingRate  uint32
	BatchID       uuid.UUID
}

// SwitchSampleInserter escribe las muestras de un lote (un INSERT con
// insert_deduplication_token = batch_id).
type SwitchSampleInserter interface {
	InsertSwitchSamples(ctx context.Context, batchID string, rows []SwitchSample) error
}

// SwitchSampleRows convierte un lote secundario en filas. El exportador debe
// ser un switch secundario del inventario y del mismo tenant que el lote y
// que Horus-Tenant; un router primario aquí es un error permanente (nunca se
// mezclan las fuentes).
func SwitchSampleRows(inv *flowinv.Snapshot, fb *flowpb.FlowBatch, headerTenant string) ([]SwitchSample, error) {
	if headerTenant == "" || headerTenant != fb.TenantID {
		return nil, fmt.Errorf("%w: Horus-Tenant %q does not match batch tenant %q", ErrPermanent, headerTenant, fb.TenantID)
	}
	tenant, err := uuid.Parse(fb.TenantID)
	if err != nil {
		return nil, fmt.Errorf("%w: tenant_id: %v", ErrPermanent, err)
	}
	sw, err := uuid.Parse(fb.RouterID)
	if err != nil {
		return nil, fmt.Errorf("%w: router_id: %v", ErrPermanent, err)
	}
	batchID, err := uuid.Parse(fb.BatchID)
	if err != nil {
		return nil, fmt.Errorf("%w: batch_id: %v", ErrPermanent, err)
	}
	exp, ok := inv.Router(sw)
	if !ok {
		return nil, ErrUnknownRouter
	}
	if exp.TenantID != tenant {
		return nil, fmt.Errorf("%w: switch %s belongs to another tenant", ErrPermanent, sw)
	}
	if !exp.Secondary {
		return nil, fmt.Errorf("%w: exporter %s is not a secondary source", ErrPermanent, sw)
	}
	out := make([]SwitchSample, 0, len(fb.Records))
	for i := range fb.Records {
		r := &fb.Records[i]
		if !r.SrcIP.IsValid() || !r.DstIP.IsValid() {
			continue
		}
		rate := r.SamplingRate
		if rate == 0 {
			rate = fb.SamplingRate
		}
		if rate == 0 {
			rate = 1
		}
		ts := r.TS
		if ts.IsZero() {
			ts = fb.ReceivedTo
		}
		out = append(out, SwitchSample{TenantID: tenant, SiteID: exp.SiteID, SwitchID: sw, TS: ts.UTC(), InputIfIndex: r.InputIfIndex,
			OutputIfIndex: r.OutputIfIndex, SrcIP: r.SrcIP, DstIP: r.DstIP, Protocol: r.Protocol, SrcPort: r.SrcPort, DstPort: r.DstPort,
			Bytes: r.Bytes * uint64(rate), Packets: r.Packets * uint64(rate), SamplingRate: rate, BatchID: batchID})
	}
	return out, nil
}

// SwitchSampleConsumer consume horus.telemetry.flows.secondary.> con el
// durable flows-switch-samples y escribe flows.switch_samples. Un INSERT por
// lote (volumen bajo: muestreo 1:N del switch).
type SwitchSampleConsumer struct {
	Inv *flowinv.Store
	Ins SwitchSampleInserter
	// Paused dice si la ingesta de un ISP está en pausa (nil = ninguno).
	Paused func(tenant string) bool
	Log    *slog.Logger
}

// Handle procesa un mensaje y lo confirma, reintenta o termina.
func (c *SwitchSampleConsumer) Handle(ctx context.Context, msg BusMsg) {
	tenant := msg.Headers().Get(flowbus.HeaderTenant)
	if c.Paused != nil && c.Paused(tenant) {
		_ = msg.DoubleAck(ctx)
		return
	}
	fb, err := flowpb.UnmarshalFlowBatch(msg.Data())
	if err != nil {
		c.Log.WarnContext(ctx, "switch samples: malformed batch", "subject", msg.Subject(), "error", err)
		_ = msg.Term()
		return
	}
	rows, err := SwitchSampleRows(c.Inv.Load(), fb, tenant)
	switch {
	case errors.Is(err, ErrUnknownRouter):
		_ = msg.NakWithDelay(30 * time.Second) // el inventario aún no conoce el switch
		return
	case err != nil:
		c.Log.WarnContext(ctx, "switch samples: batch discarded", "subject", msg.Subject(), "error", err)
		_ = msg.Term()
		return
	}
	if err := c.Ins.InsertSwitchSamples(ctx, fb.BatchID, rows); err != nil {
		c.Log.WarnContext(ctx, "switch samples: insert failed: retrying", "batch_id", fb.BatchID, "error", err)
		_ = msg.NakWithDelay(5 * time.Second)
		return
	}
	_ = msg.DoubleAck(ctx)
}

// Run consume hasta que ctx se cancela.
func (c *SwitchSampleConsumer) Run(ctx context.Context, cons jetstream.Consumer) error {
	it, err := cons.Messages(jetstream.PullMaxMessages(64))
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		it.Stop()
	}()
	for {
		m, err := it.Next()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, jetstream.ErrMsgIteratorClosed) {
				return nil
			}
			c.Log.WarnContext(ctx, "switch samples: next message", "error", err)
			continue
		}
		c.Handle(ctx, m)
	}
}

// SwitchSampleColumns son las columnas de flows.switch_samples en orden.
var SwitchSampleColumns = []string{"tenant_id", "site_id", "switch_id", "ts", "input_if_index", "output_if_index", "src_ip", "dst_ip",
	"protocol", "src_port", "dst_port", "bytes", "packets", "sampling_rate", "batch_id"}

// SwitchSampleValues devuelve los valores de una fila en el orden de
// SwitchSampleColumns (IP en forma IPv6, IPv4 mapeada).
func SwitchSampleValues(r *SwitchSample) []any {
	return []any{r.TenantID, r.SiteID, r.SwitchID, r.TS, r.InputIfIndex, r.OutputIfIndex, v6(r.SrcIP), v6(r.DstIP), r.Protocol,
		r.SrcPort, r.DstPort, r.Bytes, r.Packets, r.SamplingRate, r.BatchID}
}
