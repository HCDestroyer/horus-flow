// Package dashboards es el submódulo de dashboards de analytics (dueño CORE,
// I1-15; api.md §2.11, contrato C9 en packages/schemas/dashboard/v0):
// catálogo de tipos de widget en servidor, dashboards como documento con
// versión/If-Match, plantillas de sistema "NOC del ISP" y "Seguridad",
// playlists de kiosco, GET /kiosk/config y la puerta de los datos de cada
// widget (permiso del tipo, asignación del kiosco) que resuelve FLOW por
// module.Services (analytics/api/trafficwidgets).
package dashboards

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"
)

//go:embed contract/widget-types.v0.json contract/templates/*.json
var contractFS embed.FS

// ContractFS devuelve las copias embebidas del contrato (test de sincronía).
func ContractFS() fs.FS { return contractFS }

// WidgetType es una entrada del catálogo (widget-type.schema.json).
type WidgetType struct {
	Type                  string          `json:"type"`
	Title                 string          `json:"title"`
	Description           string          `json:"description"`
	Category              string          `json:"category"`
	ConfigSchema          json.RawMessage `json:"config_schema"`
	RequiredPermission    string          `json:"required_permission"`
	AdditionalPermissions []string        `json:"additional_permissions,omitempty"`
	DataEndpointKind      string          `json:"data_endpoint_kind"`
	RealtimeTopic         *string         `json:"realtime_topic"`
	ContainsPersonalData  bool            `json:"contains_personal_data"`
	KioskAllowed          bool            `json:"kiosk_allowed"`
	Sizes                 json.RawMessage `json:"sizes"`
	Increment             string          `json:"increment"`
	Version               int             `json:"version,omitempty"`

	schema *schemaNode
}

// Catalog es el catálogo de tipos de widget.
type Catalog struct {
	Version string
	Types   []WidgetType
	byType  map[string]*WidgetType
}

// LoadCatalog interpreta el catálogo embebido.
func LoadCatalog() (*Catalog, error) {
	b, err := contractFS.ReadFile("contract/widget-types.v0.json")
	if err != nil {
		return nil, fmt.Errorf("dashboards: catalog: %w", err)
	}
	var f struct {
		Version json.RawMessage `json:"version"`
		Data    []WidgetType    `json:"data"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("dashboards: catalog: %w", err)
	}
	c := &Catalog{Version: strings.Trim(string(f.Version), `"`), Types: f.Data, byType: map[string]*WidgetType{}}
	for i := range c.Types {
		t := &c.Types[i]
		var n schemaNode
		if err := json.Unmarshal(t.ConfigSchema, &n); err != nil {
			return nil, fmt.Errorf("dashboards: config_schema of %s: %w", t.Type, err)
		}
		t.schema = &n
		c.byType[t.Type] = t
	}
	return c, nil
}

// Get devuelve un tipo.
func (c *Catalog) Get(typ string) (*WidgetType, bool) {
	t, ok := c.byType[typ]
	return t, ok
}

// Topics devuelve los topics realtime de un conjunto de tipos.
func (c *Catalog) Topics(types []string) []string {
	var out []string
	for _, typ := range types {
		if t, ok := c.byType[typ]; ok && t.RealtimeTopic != nil && !slices.Contains(out, *t.RealtimeTopic) {
			out = append(out, *t.RealtimeTopic)
		}
	}
	sort.Strings(out)
	return out
}

// ValidateConfig valida config contra el config_schema del tipo y devuelve
// los errores (vacío = válida).
func (t *WidgetType) ValidateConfig(config json.RawMessage) []string {
	var v any
	if len(config) == 0 {
		config = json.RawMessage("{}")
	}
	if err := json.Unmarshal(config, &v); err != nil {
		return []string{"config: JSON no válido"}
	}
	var errs []string
	t.schema.validate("config", v, &errs)
	return errs
}

// schemaNode es el subconjunto de JSON Schema 2020-12 que usan los
// config_schema del catálogo: type, properties, required,
// additionalProperties, enum, const, items, minItems, maxItems, minimum,
// maximum, minLength, maxLength, pattern y format uuid.
type schemaNode struct {
	Type                 json.RawMessage        `json:"type"`
	Properties           map[string]*schemaNode `json:"properties"`
	Required             []string               `json:"required"`
	AdditionalProperties *bool                  `json:"additionalProperties"`
	Enum                 []any                  `json:"enum"`
	Const                any                    `json:"const"`
	Items                *schemaNode            `json:"items"`
	MinItems             *int                   `json:"minItems"`
	MaxItems             *int                   `json:"maxItems"`
	Minimum              *float64               `json:"minimum"`
	Maximum              *float64               `json:"maximum"`
	MinLength            *int                   `json:"minLength"`
	MaxLength            *int                   `json:"maxLength"`
	Pattern              string                 `json:"pattern"`
	Format               string                 `json:"format"`
}

func (n *schemaNode) types() []string {
	if len(n.Type) == 0 {
		return nil
	}
	var one string
	if json.Unmarshal(n.Type, &one) == nil {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(n.Type, &many)
	return many
}

func typeOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		if x == math.Trunc(x) {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

func typeMatches(want []string, v any) bool {
	if len(want) == 0 {
		return true
	}
	got := typeOf(v)
	for _, w := range want {
		if w == got || (w == "number" && got == "integer") {
			return true
		}
	}
	return false
}

func (n *schemaNode) validate(path string, v any, errs *[]string) {
	if n == nil {
		return
	}
	if !typeMatches(n.types(), v) {
		*errs = append(*errs, fmt.Sprintf("%s: tipo %s no permitido", path, typeOf(v)))
		return
	}
	if n.Enum != nil && !slices.ContainsFunc(n.Enum, func(e any) bool { return fmt.Sprint(e) == fmt.Sprint(v) && typeOf(e) == typeOf(v) }) {
		*errs = append(*errs, path+": valor no permitido")
	}
	if n.Const != nil && fmt.Sprint(n.Const) != fmt.Sprint(v) {
		*errs = append(*errs, path+": valor no permitido")
	}
	switch x := v.(type) {
	case map[string]any:
		for _, r := range n.Required {
			if _, ok := x[r]; !ok {
				*errs = append(*errs, path+"."+r+": obligatorio")
			}
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if p, ok := n.Properties[k]; ok {
				p.validate(path+"."+k, x[k], errs)
			} else if n.AdditionalProperties != nil && !*n.AdditionalProperties {
				*errs = append(*errs, path+"."+k+": campo no permitido")
			}
		}
	case []any:
		if n.MinItems != nil && len(x) < *n.MinItems {
			*errs = append(*errs, fmt.Sprintf("%s: mínimo %d elementos", path, *n.MinItems))
		}
		if n.MaxItems != nil && len(x) > *n.MaxItems {
			*errs = append(*errs, fmt.Sprintf("%s: máximo %d elementos", path, *n.MaxItems))
		}
		for i, it := range x {
			n.Items.validate(fmt.Sprintf("%s[%d]", path, i), it, errs)
		}
	case float64:
		if n.Minimum != nil && x < *n.Minimum {
			*errs = append(*errs, fmt.Sprintf("%s: mínimo %v", path, *n.Minimum))
		}
		if n.Maximum != nil && x > *n.Maximum {
			*errs = append(*errs, fmt.Sprintf("%s: máximo %v", path, *n.Maximum))
		}
	case string:
		if n.MinLength != nil && len([]rune(x)) < *n.MinLength {
			*errs = append(*errs, path+": demasiado corto")
		}
		if n.MaxLength != nil && len([]rune(x)) > *n.MaxLength {
			*errs = append(*errs, path+": demasiado largo")
		}
		if n.Pattern != "" {
			if re, err := regexp.Compile(n.Pattern); err == nil && !re.MatchString(x) {
				*errs = append(*errs, path+": formato no válido")
			}
		}
		if n.Format == "uuid" {
			if _, err := uuid.Parse(x); err != nil {
				*errs = append(*errs, path+": UUID no válido")
			}
		}
	}
}
