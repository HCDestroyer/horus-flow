// Package securityreport es el contrato en proceso con el que el rol
// reporting (services/analytics/reporting, I3) pide a SEC los datos de
// seguridad del reporte semanal de un ISP: hallazgos abiertos, resueltos y
// falsos positivos del periodo, MTTR, clientes con más hallazgos abiertos,
// hallazgos críticos/altos vigentes, estado de seguridad de los clientes y
// contactos con destinos de reputación.
//
// Los datos personales (IP y alias del cliente) van completos: el reporte
// es un archivo que solo se descarga por la API con reports.read y nunca
// viaja en una URL. Quien llama decide qué hace con ellos.
package securityreport

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ServiceSecurityReport es el nombre en module.Services de la implementación (Reader).
const ServiceSecurityReport = "detection.SecurityReport"

// Counts son los hallazgos de un grupo en el periodo.
type Counts struct {
	Opened        int // abiertos (opened_at en el periodo)
	Resolved      int // resueltos (resolved_at en el periodo, veredicto resolved)
	FalsePositive int // marcados falso positivo en el periodo
}

// KindRow son los conteos de un tipo de hallazgo.
type KindRow struct {
	Kind string
	Counts
}

// SeverityRow son los conteos de una severidad.
type SeverityRow struct {
	Severity string
	Counts
}

// CustomerRow es un cliente con hallazgos activos (open o acknowledged) al final del periodo.
type CustomerRow struct {
	CustomerID    uuid.UUID
	Address       string
	Alias         *string
	Site          string
	OpenFindings  int
	MaxSeverity   string
	TopKind       string
	SecurityState string
}

// FindingRow es un hallazgo crítico o alto activo.
type FindingRow struct {
	ID         uuid.UUID
	Kind       string
	Severity   string
	State      string
	Summary    string
	Address    string
	Alias      *string
	Site       string
	OpenedAt   time.Time
	LastSeenAt time.Time
}

// Report son los datos de seguridad del periodo [From, To).
type Report struct {
	Totals     Counts
	ByKind     []KindRow     // ordenado por abiertos desc, después por nombre
	BySeverity []SeverityRow // critical, high, medium, low
	// MTTRSeconds es la media de opened_at → resolved_at de los hallazgos
	// resueltos (veredicto resolved) en el periodo; nil si no hubo ninguno.
	MTTRSeconds *float64
	MTTRSamples int
	// ActiveNow son los hallazgos activos ahora (open o acknowledged).
	ActiveNow int
	// TopCustomers: hasta 10 clientes con más hallazgos activos.
	TopCustomers []CustomerRow
	// OpenCriticalHigh: hasta 50 hallazgos críticos o altos activos (más graves y recientes primero).
	OpenCriticalHigh []FindingRow
	// SecurityStates: clientes por estado de seguridad actual (clean, suspected, infected, mitigated).
	SecurityStates map[string]int
	// ReputationHits: hallazgos abiertos en el periodo por contacto con
	// destinos de listas de reputación (C2 y reputation_hit) y clientes afectados.
	ReputationHits      int
	ReputationCustomers int
}

// Reader devuelve los datos de seguridad de un tenant en [from, to).
type Reader interface {
	SecurityReport(ctx context.Context, tenant uuid.UUID, from, to time.Time) (*Report, error)
}
