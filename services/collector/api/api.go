// Package api es el contrato público del rol collector: el estado de cada
// exportador que guarda en el bucket NATS KV `flow_exporter_state` (decisión
// C1 de G0) y que sirve el rol ingester en GET /flow-exporters (OpenAPI
// packages/schemas/openapi/v0/flows.yaml, esquema FlowExporter).
package api

import (
	"time"

	"github.com/google/uuid"
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
	SequenceGaps uint64 `json:"sequence_gaps,omitempty"`
	LostRecords  uint64 `json:"lost_records,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}
