package app

import (
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
)

// Errores del procesado de un lote.
var (
	// ErrPermanent: el lote nunca será válido (se termina y va a DLQ).
	ErrPermanent = errors.New("permanent")
	// ErrUnknownRouter: el inventario aún no conoce el router (se reintenta).
	ErrUnknownRouter = errors.New("router not in inventory")
)

// Enricher completa ASN/organización, servicio/categoría y reputación (I1-07, §11).
type Enricher interface {
	Enrich(tenant uuid.UUID, row *Row)
	// ASN devuelve el ASN de origen de una IP (0 si no se conoce).
	ASN(ip netip.Addr) uint32
}

// Observer recibe cada fila atribuida (descubrimiento, resúmenes).
type Observer interface {
	Observe(e Exporter, row *Row)
}

// Processor convierte un lote del collector en filas de flows_raw.
type Processor struct {
	Inv       *flowinv.Store
	Enricher  Enricher
	Observers []Observer
}

// Rows atribuye y enriquece un lote. headerTenant es Horus-Tenant.
func (p *Processor) Rows(fb *flowpb.FlowBatch, headerTenant string) ([]Row, error) {
	if headerTenant == "" || headerTenant != fb.TenantID {
		return nil, fmt.Errorf("%w: Horus-Tenant %q does not match batch tenant %q", ErrPermanent, headerTenant, fb.TenantID)
	}
	tenant, err := uuid.Parse(fb.TenantID)
	if err != nil {
		return nil, fmt.Errorf("%w: tenant_id: %v", ErrPermanent, err)
	}
	router, err := uuid.Parse(fb.RouterID)
	if err != nil {
		return nil, fmt.Errorf("%w: router_id: %v", ErrPermanent, err)
	}
	batchID, err := uuid.Parse(fb.BatchID)
	if err != nil {
		return nil, fmt.Errorf("%w: batch_id: %v", ErrPermanent, err)
	}
	inv := p.Inv.Load()
	exp, ok := inv.Router(router)
	if !ok {
		return nil, ErrUnknownRouter
	}
	if exp.TenantID != tenant {
		// El tenant lo fija el exportador; nunca se acepta otro (§4.2).
		return nil, fmt.Errorf("%w: router %s belongs to another tenant", ErrPermanent, router)
	}
	e := Exporter{TenantID: tenant, SiteID: exp.SiteID, RouterID: router}
	received := fb.ReceivedTo
	if received.IsZero() {
		received = time.Now().UTC()
	}
	rows := make([]Row, 0, len(fb.Records))
	for i := range fb.Records {
		r := &fb.Records[i]
		row := Row{TenantID: tenant, SiteID: exp.SiteID, RouterID: router, BatchID: batchID,
			TS: r.TS, FlowStart: r.FlowStart, ReceivedAt: received, Protocol: r.Protocol, TCPFlags: r.TCPFlags,
			ICMPTypeCode: r.ICMPTypeCode, MergedFlows: 1, FlowSource: r.FlowSource}
		if !Attribute(inv, e, r, &row) {
			continue
		}
		if row.FlowSource == "" {
			row.FlowSource = "ipfix"
		}
		rate := r.SamplingRate
		if rate == 0 {
			rate = fb.SamplingRate
		}
		if rate == 0 {
			rate = 1
		}
		row.SamplingRate = rate
		row.Bytes, row.Packets = r.Bytes*uint64(rate), r.Packets*uint64(rate)
		if d := r.TS.Sub(r.FlowStart).Milliseconds(); d > 0 && d < 1<<32 {
			row.DurationMs = uint32(d)
		}
		if i, ok := inv.Interface(router, r.InputIfIndex); ok {
			row.InputInterfaceID = i.InterfaceID
		}
		if i, ok := inv.Interface(router, r.OutputIfIndex); ok {
			row.OutputInterfaceID = i.InterfaceID
		}
		if row.AttributionStatus == StatusUnknown && row.ClientIP.IsValid() && !p.eligible(inv, tenant, row.ClientIP) {
			row.ClientIP = netip.Addr{}
		}
		if p.Enricher != nil {
			p.Enricher.Enrich(tenant, &row)
		}
		for _, o := range p.Observers {
			o.Observe(e, &row)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// eligible: candidata a cliente en modo descubrimiento (§4.1 punto 3): IP
// privada/CGNAT/ULA o pública de un ASN propio del ISP.
func (p *Processor) eligible(inv *flowinv.Snapshot, tenant uuid.UUID, ip netip.Addr) bool {
	if privateOrCGNAT(ip) {
		return true
	}
	if p.Enricher == nil {
		return false
	}
	asn := p.Enricher.ASN(ip)
	return asn != 0 && inv.TenantASN(tenant, asn)
}
