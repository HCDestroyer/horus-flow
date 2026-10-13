// Package tools es el registro de herramientas de solo lectura del asistente
// de IA (docs/assist.md §Herramientas). Lo comparten el agente de la app y el
// servidor MCP: mismas herramientas, misma validación, mismo enmascarado.
//
// Las herramientas leen los datos de Horus a través de un [Reader] que actúa
// como el usuario que pide el análisis (o el dueño del token MCP): RBAC, RLS y
// visibilidad de datos personales se aplican como en cualquier otra petición.
// Todo resultado pasa por el [mask.Masker] antes de salir hacia el proveedor.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/hcdestroyer/horus-flow/services/assist/internal/mask"
)

// MaxResultBytes es el tamaño máximo del resultado de una herramienta (JSON);
// lo que sobra se recorta para no inflar el contexto del modelo.
const MaxResultBytes = 24 << 10

// Reader lee la API de Horus como el usuario del análisis (solo GET).
type Reader interface {
	// Get devuelve el JSON decodificado de GET path?query o un *HTTPError.
	Get(ctx context.Context, path string, query url.Values) (any, error)
}

// HTTPError es una respuesta no 2xx de la API de Horus.
type HTTPError struct {
	Status int
	Code   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("horus api: %d %s", e.Status, e.Code) }

// Env es el entorno de una llamada: lector como el usuario y enmascarador.
type Env struct {
	Reader Reader
	Masker *mask.Masker
}

// Tool es una herramienta de solo lectura.
type Tool struct {
	Name        string
	Description string
	// Schema es el JSON Schema (objeto) de los argumentos.
	Schema map[string]any
	// Permissions son los permisos que necesita (informativo; los aplica la API).
	Permissions []string
	Run         func(ctx context.Context, env Env, args map[string]any) (any, error)
}

// Spec es la definición pública de una herramienta (para el proveedor y MCP).
type Spec struct {
	Name        string
	Description string
	Schema      map[string]any
}

// Registry es el conjunto de herramientas.
type Registry struct {
	tools []Tool
	by    map[string]*Tool
}

// NewRegistry crea un registro. Falla si hay nombres repetidos o esquemas inválidos.
func NewRegistry(ts ...Tool) (*Registry, error) {
	r := &Registry{by: map[string]*Tool{}}
	seen := map[string]bool{}
	for _, t := range ts {
		if !nameRe.MatchString(t.Name) {
			return nil, fmt.Errorf("tools: invalid name %q", t.Name)
		}
		if seen[t.Name] {
			return nil, fmt.Errorf("tools: duplicate %q", t.Name)
		}
		seen[t.Name] = true
		if t.Schema["type"] != "object" {
			return nil, fmt.Errorf("tools: %s: schema must be an object", t.Name)
		}
		r.tools = append(r.tools, t)
	}
	for i := range r.tools {
		r.by[r.tools[i].Name] = &r.tools[i]
	}
	return r, nil
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

// Specs devuelve las definiciones en orden estable (por nombre: el prefijo
// del prompt no cambia entre peticiones y se aprovecha la caché).
func (r *Registry) Specs() []Spec {
	out := make([]Spec, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, Spec{Name: t.Name, Description: t.Description, Schema: t.Schema})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Permissions devuelve la unión de permisos que usan las herramientas.
func (r *Registry) Permissions() []string {
	var out []string
	for _, t := range r.tools {
		for _, p := range t.Permissions {
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

// ErrUnknownTool indica una herramienta que no existe.
var ErrUnknownTool = errors.New("tools: unknown tool")

// PublicError es un error con un mensaje apto para el modelo o el usuario
// (argumento inválido, falta de permiso…; se devuelve como is_error).
type PublicError struct{ Msg string }

// Public crea un PublicError.
func Public(msg string) error { return &PublicError{Msg: msg} }

func (e *PublicError) Error() string { return e.Msg }

// Call valida los argumentos crudos, ejecuta la herramienta y devuelve su
// resultado enmascarado y serializado (JSON, recortado a MaxResultBytes).
// Un error es apto para devolverse al modelo (sin datos internos).
func (r *Registry) Call(ctx context.Context, env Env, name string, raw json.RawMessage) (string, error) {
	t, ok := r.by[name]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownTool, name)
	}
	args := map[string]any{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", &PublicError{Msg: "Los argumentos deben ser un objeto JSON."}
		}
	}
	if err := Validate(t.Schema, args); err != nil {
		return "", err
	}
	if env.Masker == nil {
		env.Masker = mask.New()
	}
	res, err := t.Run(ctx, env, args)
	if err != nil {
		return "", publicError(err)
	}
	b, err := json.Marshal(res)
	if err != nil {
		return "", Public("No se pudo serializar el resultado.")
	}
	if len(b) > MaxResultBytes {
		b = append(b[:MaxResultBytes:MaxResultBytes], []byte(`… [recortado]`)...)
	}
	return string(b), nil
}

// publicError convierte un error de la API en un mensaje apto para el modelo.
func publicError(err error) error {
	var he *HTTPError
	var ae *PublicError
	switch {
	case errors.As(err, &ae):
		return ae
	case errors.As(err, &he):
		switch he.Status {
		case 400, 422:
			return Public("La API rechazó los parámetros de la consulta.")
		case 401, 403:
			return Public("El usuario no tiene permiso para consultar este dato.")
		case 404:
			return Public("No encontrado.")
		case 503, 504:
			return Public("El dato no está disponible ahora mismo (servicio degradado).")
		default:
			return Public(fmt.Sprintf("La API respondió %d.", he.Status))
		}
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return Public("La consulta tardó demasiado.")
	default:
		return Public("Error interno al consultar Horus.")
	}
}

// Validate comprueba args contra un subconjunto de JSON Schema (objeto con
// propiedades string/integer/boolean, required, enum, minimum/maximum,
// maxLength, pattern y additionalProperties: false implícito).
func Validate(schema map[string]any, args map[string]any) error {
	props, _ := schema["properties"].(map[string]any)
	for k := range args {
		if _, ok := props[k]; !ok {
			return &PublicError{Msg: fmt.Sprintf("Argumento desconocido: %s.", k)}
		}
	}
	if req, ok := schema["required"].([]string); ok {
		for _, k := range req {
			if _, ok := args[k]; !ok {
				return &PublicError{Msg: fmt.Sprintf("Falta el argumento %s.", k)}
			}
		}
	}
	for k, v := range args {
		p, _ := props[k].(map[string]any)
		if err := validateValue(k, p, v); err != nil {
			return err
		}
	}
	return nil
}

func validateValue(k string, p map[string]any, v any) error {
	bad := func(msg string) error { return &PublicError{Msg: fmt.Sprintf("%s: %s", k, msg)} }
	switch p["type"] {
	case "string":
		s, ok := v.(string)
		if !ok {
			return bad("debe ser un texto.")
		}
		if n, ok := p["maxLength"].(int); ok && len(s) > n {
			return bad("demasiado largo.")
		}
		if pat, ok := p["pattern"].(string); ok && !regexp.MustCompile(pat).MatchString(s) {
			return bad("formato no válido.")
		}
		if enum, ok := p["enum"].([]string); ok && !slices.Contains(enum, s) {
			return bad("valor no admitido (" + strings.Join(enum, ", ") + ").")
		}
	case "integer":
		f, ok := v.(float64)
		if !ok || f != float64(int64(f)) {
			return bad("debe ser un entero.")
		}
		if lo, ok := p["minimum"].(int); ok && f < float64(lo) {
			return bad(fmt.Sprintf("mínimo %d.", lo))
		}
		if hi, ok := p["maximum"].(int); ok && f > float64(hi) {
			return bad(fmt.Sprintf("máximo %d.", hi))
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return bad("debe ser true o false.")
		}
	}
	return nil
}

// Str devuelve el argumento k como texto ("" si falta).
func Str(args map[string]any, k string) string {
	s, _ := args[k].(string)
	return s
}

// Int devuelve el argumento k como entero (def si falta).
func Int(args map[string]any, k string, def int) int {
	if f, ok := args[k].(float64); ok {
		return int(f)
	}
	return def
}

// Bool devuelve el argumento k como booleano (def si falta).
func Bool(args map[string]any, k string, def bool) bool {
	if b, ok := args[k].(bool); ok {
		return b
	}
	return def
}
