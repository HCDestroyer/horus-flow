// Package trafficwidgets es el contrato en proceso con el que el catálogo de
// dashboards (CORE, I1-15) pide a FLOW los datos de los widgets de tráfico
// resueltos en servidor (GET /dashboards/{id}/widgets/{wid}/data y
// POST /widget-data/preview; api.md §2.11). La respuesta cumple
// packages/schemas/dashboard/v0/widget-data.schema.json (C9) con las formas
// que lee el frontend (apps/frontend/app/widgets/shapes.ts).
//
// CORE comprueba el required_permission del tipo, la asignación del kiosco
// y la existencia del widget; FLOW resuelve la consulta con el tenant y los
// nodos visibles del espectador, enmascara IPs si ShowPersonalData es false y
// cachea por (tenant, tipo, config, rango, alcance, enmascarado).
package trafficwidgets

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ServiceWidgetData es el nombre en module.Services de la implementación.
const ServiceWidgetData = "analytics.TrafficWidgetData"

// Errores del proveedor.
var (
	// ErrUnsupportedType: el tipo no es de FLOW (CORE lo resuelve con otro proveedor).
	ErrUnsupportedType = errors.New("trafficwidgets: unsupported widget type")
	// ErrUnavailable: ClickHouse caído o sin configurar (503 ANALYTICS_UNAVAILABLE).
	ErrUnavailable = errors.New("trafficwidgets: analytics unavailable")
	// ErrInvalidConfig: config o rango inválidos (422).
	ErrInvalidConfig = errors.New("trafficwidgets: invalid config")
)

// Request es una petición de datos de un widget.
type Request struct {
	TenantID uuid.UUID
	Type     string
	// Config es la config guardada del widget (JSON del config_schema del tipo).
	Config json.RawMessage
	// Range relativo (15m…90d) o From/To absolutos; vacío = el de la config o el del tipo.
	Range    string
	From, To *time.Time
	// SiteIDs: variable de dashboard site_id (vacío = la config o todo el ISP).
	SiteIDs []uuid.UUID
	// AllowedSites: nodos visibles para el espectador (nil = todo el tenant).
	AllowedSites []uuid.UUID
	// ShowPersonalData: false para kioscos sin show_personal_data o usuarios
	// sin customers.read ⇒ IPs enmascaradas (meta.masked_personal_data).
	ShowPersonalData bool
}

// Meta es WidgetData.meta.
type Meta struct {
	WidgetType         string     `json:"widget_type"`
	DataEndpointKind   string     `json:"data_endpoint_kind"`
	GeneratedAt        time.Time  `json:"generated_at"`
	From               *time.Time `json:"from,omitempty"`
	To                 *time.Time `json:"to,omitempty"`
	Step               *int       `json:"step,omitempty"`
	Partial            bool       `json:"partial"`
	Coverage           *float64   `json:"coverage,omitempty"`
	FreshnessSeconds   *int       `json:"freshness_seconds,omitempty"`
	MaskedPersonalData bool       `json:"masked_personal_data"`
	Cache              string     `json:"cache,omitempty"`
}

// Column es una columna de TableData.
type Column struct {
	Key          string `json:"key"`
	Type         string `json:"type"`
	PersonalData bool   `json:"personal_data,omitempty"`
}

// Point es [timestamp, valor|null].
type Point [2]any

// Series es una serie de SeriesData.
type Series struct {
	Metric string  `json:"metric"`
	Unit   string  `json:"unit"`
	Group  *string `json:"group"`
	Points []Point `json:"points"`
}

// Data es WidgetData.data (state | series | table).
type Data struct {
	Kind    string           `json:"kind"`
	Values  map[string]any   `json:"values,omitempty"`
	Series  []Series         `json:"series,omitempty"`
	Columns []Column         `json:"columns,omitempty"`
	Rows    []map[string]any `json:"rows,omitempty"`
	Others  map[string]any   `json:"others,omitempty"`
}

// MarshalJSON emite solo los campos de cada kind (additionalProperties: false).
func (d Data) MarshalJSON() ([]byte, error) {
	switch d.Kind {
	case "state":
		v := d.Values
		if v == nil {
			v = map[string]any{}
		}
		return json.Marshal(struct {
			Kind   string         `json:"kind"`
			Values map[string]any `json:"values"`
		}{d.Kind, v})
	case "series":
		s := d.Series
		if s == nil {
			s = []Series{}
		}
		return json.Marshal(struct {
			Kind   string   `json:"kind"`
			Series []Series `json:"series"`
		}{d.Kind, s})
	default:
		r := d.Rows
		if r == nil {
			r = []map[string]any{}
		}
		return json.Marshal(struct {
			Kind    string           `json:"kind"`
			Columns []Column         `json:"columns"`
			Rows    []map[string]any `json:"rows"`
			Others  map[string]any   `json:"others"`
		}{d.Kind, d.Columns, r, d.Others})
	}
}

// WidgetData es la respuesta completa.
type WidgetData struct {
	Data Data `json:"data"`
	Meta Meta `json:"meta"`
}

// Provider resuelve datos de widgets de tráfico.
type Provider interface {
	// Types son los tipos que resuelve FLOW.
	Types() []string
	Resolve(ctx context.Context, req Request) (*WidgetData, error)
}

// WidgetDataProvider es el valor que se registra en module.Services.
type WidgetDataProvider = Provider
