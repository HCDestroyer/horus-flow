// Package api es el contrato público del rol collector: el estado de cada
// exportador que guarda en el bucket NATS KV `flow_exporter_state` (decisión
// C1 de G0) y que sirve el rol ingester en GET /flow-exporters (OpenAPI
// packages/schemas/openapi/v0/flows.yaml, esquema FlowExporter).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
)

// Estados del exportador (enum abierto FlowExporterState).
const (
	StatePendingConfiguration = "pending_configuration"
	StateExporting            = "exporting"
	StateSilent               = "silent"
	StateLossy                = "lossy"
	StateClockSkew            = "clock_skew"
)

// Pistas de diagnóstico (FlowExporter.hints).
const (
	HintCheckTunnel             = "check_tunnel"
	HintCheckTrafficFlowTarget  = "check_traffic_flow_target"
	HintCheckFirewall           = "check_firewall"
	HintHardwareOffloadSuspects = "hardware_offload_suspected"
	HintCheckNTP                = "check_ntp"
)

// FlowExporter es el valor del bucket KV por router (clave = router_id) con
// la forma del esquema OpenAPI FlowExporter.
type FlowExporter struct {
	TenantID              uuid.UUID  `json:"tenant_id"`
	RouterID              uuid.UUID  `json:"router_id"`
	SiteID                uuid.UUID  `json:"site_id"`
	Version               int        `json:"version"`
	State                 string     `json:"state"`
	StateSince            time.Time  `json:"state_since"`
	ExporterIP            *string    `json:"exporter_ip"`
	LastFlowAt            *time.Time `json:"last_flow_at"`
	FlowsPerSecond        *float64   `json:"flows_per_second"`
	LossRatio5m           *float64   `json:"loss_ratio_5m"`
	ClockSkewSeconds      *float64   `json:"clock_skew_seconds"`
	FlowSource            *string    `json:"flow_source"`
	SamplingRate          *int       `json:"sampling_rate"`
	CoverageRatio         *float64   `json:"coverage_ratio"`
	DroppedRecordsQuota1h string     `json:"dropped_records_quota_1h"`
	Hints                 []string   `json:"hints"`
	// SequenceGaps y LostRecords son contadores acumulados desde el arranque
	// del collector (diagnóstico; no están en el esquema OpenAPI).
	SequenceGaps uint64    `json:"sequence_gaps,omitempty"`
	LostRecords  uint64    `json:"lost_records,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// States lee el estado que el collector guarda en NATS KV.
type States interface {
	Get(ctx context.Context, routerID uuid.UUID) (*FlowExporter, error)
}

// KVStates adapta el bucket flow_exporter_state.
type KVStates struct{ KV jetstream.KeyValue }

// Get implementa States (nil, nil si no hay estado).
func (k KVStates) Get(ctx context.Context, id uuid.UUID) (*FlowExporter, error) {
	e, err := k.KV.Get(ctx, id.String())
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var fe FlowExporter
	if err := json.Unmarshal(e.Value(), &fe); err != nil {
		return nil, err
	}
	return &fe, nil
}

// View es la representación pública (esquema OpenAPI FlowExporter, sin
// los contadores de diagnóstico).
type View struct {
	TenantID              uuid.UUID  `json:"tenant_id"`
	RouterID              uuid.UUID  `json:"router_id"`
	SiteID                uuid.UUID  `json:"site_id"`
	Version               int        `json:"version"`
	State                 string     `json:"state"`
	StateSince            time.Time  `json:"state_since"`
	ExporterIP            *string    `json:"exporter_ip"`
	LastFlowAt            *time.Time `json:"last_flow_at"`
	FlowsPerSecond        *float64   `json:"flows_per_second"`
	LossRatio5m           *float64   `json:"loss_ratio_5m"`
	ClockSkewSeconds      *float64   `json:"clock_skew_seconds"`
	FlowSource            *string    `json:"flow_source"`
	SamplingRate          *int       `json:"sampling_rate"`
	CoverageRatio         *float64   `json:"coverage_ratio"`
	DroppedRecordsQuota1h string     `json:"dropped_records_quota_1h"`
	Hints                 []string   `json:"hints"`
}

// Exporters devuelve el estado de los exportadores de un tenant (lo usa
// también analytics para el widget exporters_status). sites vacío = todos.
func Exporters(ctx context.Context, inv *flowinv.Snapshot, st States, tenant uuid.UUID, sites []uuid.UUID, now time.Time) ([]View, error) {
	var out []View
	for _, e := range inv.Data().Exporters {
		if e.TenantID != tenant || (len(sites) > 0 && !slices.Contains(sites, e.SiteID)) {
			continue
		}
		var fe *FlowExporter
		if st != nil {
			var err error
			if fe, err = st.Get(ctx, e.RouterID); err != nil {
				return nil, err
			}
		}
		ip := e.TunnelIP.Unmap().String()
		if fe == nil || fe.TenantID != tenant {
			// Sin estado del collector: el router aún no ha exportado nada.
			out = append(out, View{TenantID: tenant, RouterID: e.RouterID, SiteID: e.SiteID, Version: 1,
				State: StatePendingConfiguration, StateSince: now, ExporterIP: &ip, DroppedRecordsQuota1h: "0",
				Hints: []string{HintCheckTrafficFlowTarget, HintCheckTunnel}})
			continue
		}
		hints := fe.Hints
		if hints == nil {
			hints = []string{}
		}
		out = append(out, View{TenantID: fe.TenantID, RouterID: fe.RouterID, SiteID: fe.SiteID, Version: fe.Version,
			State: fe.State, StateSince: fe.StateSince, ExporterIP: fe.ExporterIP, LastFlowAt: fe.LastFlowAt,
			FlowsPerSecond: fe.FlowsPerSecond, LossRatio5m: fe.LossRatio5m, ClockSkewSeconds: fe.ClockSkewSeconds,
			FlowSource: fe.FlowSource, SamplingRate: fe.SamplingRate, CoverageRatio: fe.CoverageRatio,
			DroppedRecordsQuota1h: fe.DroppedRecordsQuota1h, Hints: hints})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RouterID.String() < out[j].RouterID.String() })
	return out, nil
}
