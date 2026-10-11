// Package platformoverview es el contrato en proceso (ADR-0025 §1) con el
// que GET /api/v1/platform/overview (rol gateway, contrato v0 platform.yaml,
// esquema TenantOverview) compone el estado por ISP sin datos de clientes:
// cada módulo dueño de un dato registra en module.Services una Source con su
// nombre y el gateway fusiona los conteos por tenant. Una fuente ausente (rol
// en otro proceso) o caída no tumba la vista: sus campos quedan sin valor.
package platformoverview

import (
	"context"

	"github.com/google/uuid"
)

// Nombres en module.Services (cada uno → Source).
const (
	// ServiceExporters: estado de los exportadores de flujos (ingester).
	ServiceExporters = "platformoverview.Exporters"
	// ServiceTunnels: túneles WireGuard caídos (wireguard).
	ServiceTunnels = "platformoverview.Tunnels"
	// ServiceFindings: hallazgos activos críticos (detection).
	ServiceFindings = "platformoverview.Findings"
)

// Services son las fuentes que consulta el gateway, en orden.
var Services = []string{ServiceExporters, ServiceTunnels, ServiceFindings}

// Stats son los conteos de un tenant; nil = la fuente no aporta el dato.
// Solo conteos y agregados: nunca clientes ni direcciones.
type Stats struct {
	RoutersTotal         *int
	ExportersExporting   *int
	ExportersSilent      *int
	TunnelsDown          *int
	OpenFindingsCritical *int
	IngestFlowsPerSecond *float64
	FlowsDroppedQuota1h  *uint64
	FlowCoverageRatio    *float64
}

// Merge copia en s los campos que o trae informados.
func (s *Stats) Merge(o Stats) {
	merge(&s.RoutersTotal, o.RoutersTotal)
	merge(&s.ExportersExporting, o.ExportersExporting)
	merge(&s.ExportersSilent, o.ExportersSilent)
	merge(&s.TunnelsDown, o.TunnelsDown)
	merge(&s.OpenFindingsCritical, o.OpenFindingsCritical)
	merge(&s.IngestFlowsPerSecond, o.IngestFlowsPerSecond)
	merge(&s.FlowsDroppedQuota1h, o.FlowsDroppedQuota1h)
	merge(&s.FlowCoverageRatio, o.FlowCoverageRatio)
}

func merge[T any](dst **T, v *T) {
	if v != nil {
		*dst = v
	}
}

// Source devuelve los conteos de todos los tenants que conoce (rol de
// plataforma; multi-tenant declarado). Un tenant ausente del mapa no tiene
// nada que contar en esa fuente (cero), no "desconocido".
type Source interface {
	OverviewStats(ctx context.Context) (map[uuid.UUID]Stats, error)
}

// SourceFunc adapta una función a Source.
type SourceFunc func(ctx context.Context) (map[uuid.UUID]Stats, error)

// OverviewStats implementa Source.
func (f SourceFunc) OverviewStats(ctx context.Context) (map[uuid.UUID]Stats, error) { return f(ctx) }

// Ptr devuelve un puntero a v (ayuda para rellenar Stats).
func Ptr[T any](v T) *T { return &v }
