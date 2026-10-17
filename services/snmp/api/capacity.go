package api

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ServiceCapacity → CapacityReader (lo usa analytics para el widget
// site_health de los dashboards NOC).
const ServiceCapacity = "snmp.Capacity"

// HealthReason es una razón del estado de salud de un nodo (SiteHealthReason).
type HealthReason struct {
	Status string `json:"status"`
	Code   string `json:"code"`
	Text   string `json:"text"`
}

// SiteHealthSummary es la salud de un nodo con lo justo para un widget NOC:
// estado explicable (vocabulario de equipos: online, warning, degraded,
// critical, offline, unknown), resumen legible, el peor enlace y el
// pronóstico más próximo. Sin datos personales.
type SiteHealthSummary struct {
	SiteID       uuid.UUID      `json:"site_id"`
	SiteName     string         `json:"site_name"`
	Status       string         `json:"status"`
	Summary      string         `json:"summary"`
	Reasons      []HealthReason `json:"reasons"`
	Uplinks      int            `json:"uplinks"`
	UplinksDown  int            `json:"uplinks_down"`
	WorstUtilP95 *float64       `json:"worst_util_p95"`
	WorstLossPct *float64       `json:"worst_loss_pct"`
	// ForecastLevel es el peor nivel de pronóstico de sus enlaces (ok,
	// planning, urgent, insufficient_data, no_capacity; "" sin pronóstico).
	ForecastLevel string `json:"forecast_level"`
	// NextCrossing es la fecha estimada más próxima de cruce del umbral de
	// planificación de alguno de sus enlaces.
	NextCrossing *time.Time `json:"next_crossing"`
}

// CapacityReader publica la salud de los nodos para otros roles del
// proceso. sites vacío = todo el tenant.
type CapacityReader interface {
	SiteHealth(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID) ([]SiteHealthSummary, error)
}
