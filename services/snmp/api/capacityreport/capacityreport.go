// Package capacityreport es el contrato estrecho, en proceso, con el que el
// rol reporting (services/analytics/reporting) pide al rol snmp la sección de
// capacidad del reporte semanal de un ISP: por nodo, sus enlaces de subida con
// el p95 del periodo (cada día contra la capacidad vigente ese día), horas
// sobre los umbrales de saturación, pérdida y latencia de su sonda, nivel de
// pronóstico y recomendación; la salida internacional agregada y el reparto
// de tráfico local / internacional.
//
// No lleva datos personales (enlaces, routers y nodos): las variantes
// completa y enmascarada del reporte son iguales en esta sección.
package capacityreport

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ServiceCapacityReport → Reader en module.Services.
const ServiceCapacityReport = "snmp.CapacityReport"

// Uplink es un enlace de subida (o la salida internacional) en el periodo.
type Uplink struct {
	InterfaceID uuid.UUID
	Label       string
	RouterName  string
	Provider    string
	// CapacityBps es la capacidad vigente al generar el reporte (0 = desconocida).
	CapacityBps int64
	// UtilP95 es el peor día del periodo contra la capacidad vigente ese día
	// (nil sin datos o sin capacidad).
	UtilP95 *float64
	// PeakP95Bps es el mayor pico diario (p95 de las medias de 5 min).
	PeakP95Bps         float64
	HoursAboveWarning  float64
	HoursAboveCritical float64
	Days               int // días con datos en el periodo
	// LossPct y LatencyMs son la media de su sonda (nil sin sonda).
	LossPct   *float64
	LatencyMs *float64
	// ForecastLevel: ok, planning, urgent, insufficient_data, no_capacity o "".
	ForecastLevel  string
	Recommendation string
	// PlanningDate es el cruce estimado del umbral de planificación.
	PlanningDate *time.Time
}

// Site es un nodo con su estado de salud y sus enlaces.
type Site struct {
	SiteID   uuid.UUID
	SiteName string
	Status   string
	Summary  string
	Uplinks  []Uplink
}

// Traffic es el reparto local / internacional del periodo.
type Traffic struct {
	LocalBytes         uint64
	LocalByPrefixBytes uint64
	InternationalBytes uint64
	UnknownBytes       uint64
	LocalShare         *float64
	InternationalShare *float64
	Country            *string
}

// Report es la sección de capacidad del reporte.
type Report struct {
	From, To           time.Time
	SaturationWarning  float64
	SaturationCritical float64
	PlanningThreshold  float64
	Sites              []Site
	Egress             *Uplink
	Traffic            *Traffic
	// History=false: sin ClickHouse (solo la configuración, sin p95 ni pronóstico).
	History bool
}

// Reader devuelve la sección de capacidad de un ISP para [from, to).
type Reader interface {
	CapacityReport(ctx context.Context, tenant uuid.UUID, from, to time.Time) (*Report, error)
}
