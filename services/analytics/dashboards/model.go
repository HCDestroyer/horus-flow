package dashboards

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
)

// Códigos de error del contrato.
const (
	CodeDashboardNotFound    = "DASHBOARD_NOT_FOUND"
	CodeDashboardReadOnly    = "DASHBOARD_READ_ONLY"
	CodeWidgetTypeUnknown    = "WIDGET_TYPE_UNKNOWN"
	CodeWidgetConfigInvalid  = "WIDGET_CONFIG_INVALID"
	CodeWidgetTypeNotAllowed = "WIDGET_TYPE_NOT_ALLOWED"
)

// Visibilidades.
const (
	VisSystem  = "system"
	VisTenant  = "tenant"
	VisPrivate = "private"
)

// Ranges relativos válidos.
var Ranges = []string{"15m", "1h", "6h", "24h", "7d", "30d", "90d"}

var widgetIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Position es la posición de un widget en la grilla de 12 columnas.
type Position struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// Widget es un widget del documento.
type Widget struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	Title          *string         `json:"title"`
	Position       Position        `json:"position"`
	Config         json.RawMessage `json:"config"`
	RefreshSeconds *int            `json:"refresh_seconds,omitempty"`
}

// Layout es el layout del dashboard.
type Layout struct {
	Grid        string         `json:"grid"`
	Columns     int            `json:"columns"`
	RowHeightPx int            `json:"row_height_px"`
	Breakpoints map[string]int `json:"breakpoints,omitempty"`
}

// Variables son las variables de la toolbar.
type Variables struct {
	SiteID *uuid.UUID `json:"site_id,omitempty"`
	Range  string     `json:"range,omitempty"`
}

// Dashboard es el documento (dashboard.schema.json).
type Dashboard struct {
	ID              uuid.UUID
	TenantID        *uuid.UUID
	OwnerID         *uuid.UUID
	Name            string
	Visibility      string
	TemplateKey     *string
	TemplateVersion *int
	Layout          Layout
	DefaultRange    string
	RefreshSeconds  int
	Variables       Variables
	Widgets         []Widget
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Version         int
}

// Widget devuelve un widget por id.
func (d *Dashboard) Widget(id string) (*Widget, int) {
	for i := range d.Widgets {
		if d.Widgets[i].ID == id {
			return &d.Widgets[i], i
		}
	}
	return nil, -1
}

// WidgetTypes devuelve los tipos usados.
func (d *Dashboard) WidgetTypes() []string {
	var out []string
	for _, w := range d.Widgets {
		if !slices.Contains(out, w.Type) {
			out = append(out, w.Type)
		}
	}
	return out
}

// PlaylistItem es una entrada de la rotación.
type PlaylistItem struct {
	DashboardID     uuid.UUID       `json:"dashboard_id"`
	DurationSeconds int             `json:"duration_seconds"`
	Variables       json.RawMessage `json:"variables,omitempty"`
}

// Playlist es una lista de reproducción de kiosco.
type Playlist struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	Name       string
	Items      []PlaylistItem
	Transition string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Version    int
}

type fieldErrs []problem.FieldError

func (f *fieldErrs) add(field, code, msg string) { *f = append(*f, apperr.Field(field, code, msg)) }

func (f fieldErrs) err() error {
	if len(f) == 0 {
		return nil
	}
	return apperr.Validation(f...)
}

func validateLayout(l Layout, f *fieldErrs) {
	if l.Grid != "12-col" || l.Columns != 12 {
		f.add("layout", "INVALID_VALUE", "grid = 12-col y columns = 12.")
	}
	if l.RowHeightPx < 40 || l.RowHeightPx > 200 {
		f.add("layout.row_height_px", "OUT_OF_RANGE", "Entre 40 y 200.")
	}
	for k := range l.Breakpoints {
		if !slices.Contains([]string{"lg", "md", "sm"}, k) {
			f.add("layout.breakpoints."+k, "UNKNOWN_FIELD", "Solo lg, md y sm.")
		}
	}
}

func validatePosition(field string, p Position, f *fieldErrs) {
	if p.X < 0 || p.X > 11 || p.Y < 0 || p.W < 1 || p.W > 12 || p.H < 1 || p.H > 12 || p.X+p.W > 12 {
		f.add(field, "OUT_OF_RANGE", "Posición fuera de la grilla de 12 columnas.")
	}
}

// validateWidget valida un widget contra el catálogo. Devuelve un error con
// el código propio del contrato si el tipo no existe o la config no valida.
func validateWidget(c *Catalog, field string, w *Widget, f *fieldErrs) error {
	if !widgetIDRe.MatchString(w.ID) {
		f.add(field+".id", "INVALID_FORMAT", "Identificador ^[a-z0-9][a-z0-9_-]{0,63}$.")
	}
	if w.Title != nil && len([]rune(*w.Title)) > 80 {
		f.add(field+".title", "TOO_LONG", "Máximo 80 caracteres.")
	}
	if w.RefreshSeconds != nil && (*w.RefreshSeconds < 5 || *w.RefreshSeconds > 3600) {
		f.add(field+".refresh_seconds", "OUT_OF_RANGE", "Entre 5 y 3600.")
	}
	validatePosition(field+".position", w.Position, f)
	t, ok := c.Get(w.Type)
	if !ok {
		return &apperr.Error{Kind: apperr.KindInvalid, Code: CodeWidgetTypeUnknown,
			Detail: fmt.Sprintf("Tipo de widget desconocido: %q (GET /widget-types).", w.Type),
			Fields: []problem.FieldError{apperr.Field(field+".type", "UNKNOWN", "Tipo desconocido.")}}
	}
	if len(w.Config) == 0 {
		w.Config = json.RawMessage("{}")
	}
	if errs := t.ValidateConfig(w.Config); len(errs) > 0 {
		fe := make([]problem.FieldError, 0, len(errs))
		for _, e := range errs {
			fe = append(fe, apperr.Field(field+".config", "INVALID", e))
		}
		return &apperr.Error{Kind: apperr.KindInvalid, Code: CodeWidgetConfigInvalid,
			Detail: "La config del widget no cumple el config_schema de su tipo.", Fields: fe}
	}
	return nil
}

// validateDocument valida un dashboard completo.
func validateDocument(c *Catalog, d *Dashboard) error {
	var f fieldErrs
	if l := len([]rune(d.Name)); l == 0 || l > 120 {
		f.add("name", "INVALID_VALUE", "Texto de 1 a 120 caracteres.")
	}
	if !slices.Contains(Ranges, d.DefaultRange) {
		f.add("default_range", "INVALID_VALUE", "15m, 1h, 6h, 24h, 7d, 30d o 90d.")
	}
	if d.Variables.Range != "" && !slices.Contains(Ranges, d.Variables.Range) {
		f.add("variables.range", "INVALID_VALUE", "15m, 1h, 6h, 24h, 7d, 30d o 90d.")
	}
	if d.RefreshSeconds < 10 || d.RefreshSeconds > 3600 {
		f.add("refresh_seconds", "OUT_OF_RANGE", "Entre 10 y 3600.")
	}
	validateLayout(d.Layout, &f)
	if len(d.Widgets) > 30 {
		f.add("widgets", "TOO_MANY", "Máximo 30 widgets.")
	}
	seen := map[string]bool{}
	for i := range d.Widgets {
		field := fmt.Sprintf("widgets[%d]", i)
		if seen[d.Widgets[i].ID] {
			f.add(field+".id", "DUPLICATE", "Identificador repetido.")
		}
		seen[d.Widgets[i].ID] = true
		if err := validateWidget(c, field, &d.Widgets[i], &f); err != nil {
			return err
		}
	}
	return f.err()
}

// validatePlaylist valida nombre, items y transición.
func validatePlaylist(p *Playlist) error {
	var f fieldErrs
	if l := len([]rune(p.Name)); l == 0 || l > 80 {
		f.add("name", "INVALID_VALUE", "Texto de 1 a 80 caracteres.")
	}
	if len(p.Items) < 1 || len(p.Items) > 20 {
		f.add("items", "INVALID_VALUE", "Entre 1 y 20 dashboards.")
	}
	for i, it := range p.Items {
		if it.DashboardID == uuid.Nil {
			f.add(fmt.Sprintf("items[%d].dashboard_id", i), "REQUIRED", "Obligatorio.")
		}
		if it.DurationSeconds < 10 || it.DurationSeconds > 3600 {
			f.add(fmt.Sprintf("items[%d].duration_seconds", i), "OUT_OF_RANGE", "Entre 10 y 3600 (30 por defecto).")
		}
	}
	if p.Transition != "fade" && p.Transition != "none" {
		f.add("transition", "INVALID_VALUE", "fade o none.")
	}
	return f.err()
}
