// Package app es la lógica del ingester de flujos: atribución a cliente
// (traffic-model.md §4), enriquecimiento (ASN, catálogo, reputación),
// descubrimiento de clientes (§4.5) y escritura en flows.flows_raw.
package app

import (
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// Estados de atribución (Enum8 attribution_status de flows_raw).
const (
	StatusUnknown        = "unknown"
	StatusAttributed     = "attributed"
	StatusInfrastructure = "infrastructure"
	StatusTransit        = "transit"
	StatusInternal       = "internal"
)

// Direcciones (Enum8 direction de flows_raw).
const (
	DirUnknown  = "unknown"
	DirUpload   = "upload"
	DirDownload = "download"
	DirInternal = "internal"
)

// Reglas de §4.4 (diagnóstico y tests; no se guardan).
const (
	RuleUploadSrc          = "upload_src"
	RuleDownloadPostNATDst = "download_post_nat_dst"
	RuleDownloadDst        = "download_dst"
	RuleInternal           = "internal"
)

// Row es una fila de flows.flows_raw (contrato C3).
type Row struct {
	TenantID          uuid.UUID
	TS                time.Time
	FlowStart         time.Time
	ReceivedAt        time.Time
	SiteID            uuid.UUID
	RouterID          uuid.UUID
	InputInterfaceID  uuid.UUID
	OutputInterfaceID uuid.UUID
	RealmID           uuid.UUID
	AttributionStatus string
	Direction         string
	ClientIP          netip.Addr
	ClientPort        uint16
	RemoteIP          netip.Addr
	RemotePort        uint16
	Protocol          uint8
	TCPFlags          uint8
	ICMPTypeCode      uint16
	Bytes             uint64
	Packets           uint64
	DurationMs        uint32
	SamplingRate      uint32
	MergedFlows       uint16
	FlowSource        string

	RemoteASN                uint32
	RemotePrefix             netip.Addr
	RemotePrefixLen          uint8
	RemoteOrgID              uuid.UUID
	RemoteCountry            string
	ServiceID                uuid.UUID
	ClassificationMethod     string
	ClassificationConfidence uint8
	CatalogVersion           uint32
	TenantRulesVersion       uint32
	CategoryID               uuid.UUID
	ReputationCategory       string
	ReputationSourceID       uint16
	ReputationConfidence     uint8
	ReputationVersion        uint32
	BatchID                  uuid.UUID

	// No se guardan: regla aplicada y prefijo del cliente (descubrimiento).
	Rule           string
	ClientPrefixID uuid.UUID
}

// Columns son las columnas de INSERT en el orden de Values.
var Columns = []string{
	"tenant_id", "ts", "flow_start", "received_at", "site_id", "router_id", "input_interface_id",
	"output_interface_id", "realm_id", "attribution_status", "direction", "client_ip", "client_port",
	"remote_ip", "remote_port", "protocol", "tcp_flags", "icmp_type_code", "bytes", "packets",
	"duration_ms", "sampling_rate", "merged_flows", "flow_source", "remote_asn", "remote_prefix",
	"remote_prefix_len", "remote_org_id", "remote_country", "service_id", "classification_method",
	"classification_confidence", "catalog_version", "tenant_rules_version", "category_id",
	"reputation_category", "reputation_source_id", "reputation_confidence", "reputation_version", "batch_id",
}

var zero6 = netip.IPv6Unspecified()

// v6 devuelve la dirección en forma de 16 bytes (IPv4 mapeada) para IPv6.
func v6(a netip.Addr) netip.Addr {
	if !a.IsValid() {
		return zero6
	}
	return netip.AddrFrom16(a.As16())
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// Values devuelve los valores de la fila en el orden de Columns.
func (r *Row) Values() []any {
	country := r.RemoteCountry
	if len(country) != 2 {
		country = ""
	}
	return []any{
		r.TenantID, r.TS, r.FlowStart, r.ReceivedAt, r.SiteID, r.RouterID, r.InputInterfaceID,
		r.OutputInterfaceID, r.RealmID, r.AttributionStatus, r.Direction, v6(r.ClientIP), r.ClientPort,
		v6(r.RemoteIP), r.RemotePort, r.Protocol, r.TCPFlags, r.ICMPTypeCode, r.Bytes, r.Packets,
		r.DurationMs, r.SamplingRate, r.MergedFlows, r.FlowSource, r.RemoteASN, v6(r.RemotePrefix),
		r.RemotePrefixLen, r.RemoteOrgID, country, r.ServiceID, orNone(r.ClassificationMethod),
		r.ClassificationConfidence, r.CatalogVersion, r.TenantRulesVersion, r.CategoryID,
		orNone(r.ReputationCategory), r.ReputationSourceID, r.ReputationConfidence, r.ReputationVersion, r.BatchID,
	}
}

func col[T any](rows []Row, f func(*Row) T) []T {
	out := make([]T, len(rows))
	for i := range rows {
		out[i] = f(&rows[i])
	}
	return out
}

// ColumnSlices devuelve las filas por columnas (mismo orden y conversiones que
// Values) para el INSERT columnar: sin un []any por fila ni valores en caja,
// que con grupos de 50 000 filas eran la mayor parte de la memoria y de la CPU
// de escritura del ingester.
func ColumnSlices(rows []Row) []any {
	return []any{
		col(rows, func(r *Row) uuid.UUID { return r.TenantID }),
		col(rows, func(r *Row) time.Time { return r.TS }),
		col(rows, func(r *Row) time.Time { return r.FlowStart }),
		col(rows, func(r *Row) time.Time { return r.ReceivedAt }),
		col(rows, func(r *Row) uuid.UUID { return r.SiteID }),
		col(rows, func(r *Row) uuid.UUID { return r.RouterID }),
		col(rows, func(r *Row) uuid.UUID { return r.InputInterfaceID }),
		col(rows, func(r *Row) uuid.UUID { return r.OutputInterfaceID }),
		col(rows, func(r *Row) uuid.UUID { return r.RealmID }),
		col(rows, func(r *Row) string { return r.AttributionStatus }),
		col(rows, func(r *Row) string { return r.Direction }),
		col(rows, func(r *Row) netip.Addr { return v6(r.ClientIP) }),
		col(rows, func(r *Row) uint16 { return r.ClientPort }),
		col(rows, func(r *Row) netip.Addr { return v6(r.RemoteIP) }),
		col(rows, func(r *Row) uint16 { return r.RemotePort }),
		col(rows, func(r *Row) uint8 { return r.Protocol }),
		col(rows, func(r *Row) uint8 { return r.TCPFlags }),
		col(rows, func(r *Row) uint16 { return r.ICMPTypeCode }),
		col(rows, func(r *Row) uint64 { return r.Bytes }),
		col(rows, func(r *Row) uint64 { return r.Packets }),
		col(rows, func(r *Row) uint32 { return r.DurationMs }),
		col(rows, func(r *Row) uint32 { return r.SamplingRate }),
		col(rows, func(r *Row) uint16 { return r.MergedFlows }),
		col(rows, func(r *Row) string { return r.FlowSource }),
		col(rows, func(r *Row) uint32 { return r.RemoteASN }),
		col(rows, func(r *Row) netip.Addr { return v6(r.RemotePrefix) }),
		col(rows, func(r *Row) uint8 { return r.RemotePrefixLen }),
		col(rows, func(r *Row) uuid.UUID { return r.RemoteOrgID }),
		col(rows, func(r *Row) string {
			if len(r.RemoteCountry) != 2 {
				return ""
			}
			return r.RemoteCountry
		}),
		col(rows, func(r *Row) uuid.UUID { return r.ServiceID }),
		col(rows, func(r *Row) string { return orNone(r.ClassificationMethod) }),
		col(rows, func(r *Row) uint8 { return r.ClassificationConfidence }),
		col(rows, func(r *Row) uint32 { return r.CatalogVersion }),
		col(rows, func(r *Row) uint32 { return r.TenantRulesVersion }),
		col(rows, func(r *Row) uuid.UUID { return r.CategoryID }),
		col(rows, func(r *Row) string { return orNone(r.ReputationCategory) }),
		col(rows, func(r *Row) uint16 { return r.ReputationSourceID }),
		col(rows, func(r *Row) uint8 { return r.ReputationConfidence }),
		col(rows, func(r *Row) uint32 { return r.ReputationVersion }),
		col(rows, func(r *Row) uuid.UUID { return r.BatchID }),
	}
}
