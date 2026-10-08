package tenanttest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// APIPrefix es el prefijo de las rutas del bundle (`servers: …/api/v1`).
const APIPrefix = "/api/v1"

// Operation es una operación del bundle OpenAPI.
type Operation struct {
	ID         string
	Method     string
	Path       string // /api/v1/sites/{site_id}
	Module     string
	Scope      string
	Permission string
	Increment  string
	Principals []string
}

// PathParams devuelve los parámetros de ruta en orden.
func (o Operation) PathParams() []string {
	var out []string
	for _, m := range paramRe.FindAllStringSubmatch(o.Path, -1) {
		out = append(out, m[1])
	}
	return out
}

var paramRe = regexp.MustCompile(`\{([a-z_]+)\}`)

// LoadOperations lee las operaciones del bundle OpenAPI
// (packages/schemas/openapi/dist/horus-api.v0.yaml).
func LoadOperations(path string) ([]Operation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("tenanttest: %w", err)
	}
	return ParseOperations(data)
}

// ParseOperations interpreta un documento OpenAPI 3.1.
func ParseOperations(data []byte) ([]Operation, error) {
	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("tenanttest: parse openapi: %w", err)
	}
	var ops []Operation
	for p, item := range doc.Paths {
		for m, node := range item {
			method := strings.ToUpper(m)
			switch method {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			default:
				continue
			}
			var op struct {
				ID         string   `yaml:"operationId"`
				Module     string   `yaml:"x-module"`
				Scope      string   `yaml:"x-scope"`
				Permission string   `yaml:"x-permission"`
				Increment  string   `yaml:"x-increment"`
				Principals []string `yaml:"x-principals"`
			}
			if err := node.Decode(&op); err != nil {
				return nil, fmt.Errorf("tenanttest: %s %s: %w", method, p, err)
			}
			if op.ID == "" || op.Scope == "" {
				return nil, fmt.Errorf("tenanttest: %s %s sin operationId o x-scope", method, p)
			}
			ops = append(ops, Operation{ID: op.ID, Method: method, Path: APIPrefix + p, Module: op.Module, Scope: op.Scope,
				Permission: op.Permission, Increment: op.Increment, Principals: op.Principals})
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].ID < ops[j].ID })
	return ops, nil
}

// Kind es el tipo de caso de aislamiento.
type Kind int

// Tipos de caso.
const (
	// ByID: con el token de A sobre recursos de B (IDs de B en la ruta) → 404.
	ByID Kind = iota + 1
	// List: el listado de A no contiene ningún ID de B.
	List
	// Create: un tenant_id de B en el cuerpo → 403 TENANT_MISMATCH; si la
	// ruta tiene padre, el padre de B → 404.
	Create
	// Pending: el endpoint aún no existe (otra historia/incremento). Falla si
	// está montado.
	Pending
)

// Case es el caso de aislamiento declarado de una operación.
type Case struct {
	Kind Kind
	// Body es el cuerpo de las peticiones de escritura.
	Body map[string]any
	// Owner explica un Pending (historia o agente que lo entregará).
	Owner string
}

// Check devuelve los problemas de cobertura: operaciones `x-scope: tenant`
// sin caso declarado y casos de operaciones que ya no existen. Es lo que
// hace fallar la suite cuando se añade un endpoint al contrato sin caso.
func Check(ops []Operation, cases map[string]Case) []string {
	var out []string
	seen := map[string]bool{}
	for _, op := range ops {
		if op.Scope != "tenant" {
			continue
		}
		seen[op.ID] = true
		c, ok := cases[op.ID]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s (%s %s): operación de ISP sin caso de aislamiento declarado", op.ID, op.Method, op.Path))
		case c.Kind == Pending && c.Owner == "":
			out = append(out, op.ID+": caso Pending sin responsable")
		}
	}
	for id := range cases {
		if !seen[id] {
			out = append(out, id+": caso declarado para una operación que no existe o no es de ISP")
		}
	}
	sort.Strings(out)
	return out
}

// Suite ejecuta la batería "A no ve B" sobre una API en marcha.
type Suite struct {
	Ops   []Operation
	Cases map[string]Case
	// Do ejecuta una petición y devuelve estado y cuerpo.
	Do func(token, method, path string, body any, header map[string]string) (int, []byte)
	// TokenA es un token de ISP de A con todos los permisos; SessionA un
	// token de sesión (sin tid) del mismo usuario.
	TokenA, SessionA string
	// TenantB es el ID del ISP B.
	TenantB string
	// ParamsA/ParamsB: parámetro de ruta → ID de un recurso de A / de B.
	ParamsA, ParamsB map[string]string
	// ForeignIDs son todos los IDs de recursos de B.
	ForeignIDs []string
	// Mounted indica si un módulo local atiende (método, plantilla).
	Mounted func(method, pathTemplate string) bool
}

func fill(path string, params map[string]string) (string, error) {
	var missing string
	out := paramRe.ReplaceAllStringFunc(path, func(m string) string {
		name := m[1 : len(m)-1]
		v, ok := params[name]
		if !ok {
			missing = name
		}
		return v
	})
	if missing != "" {
		return "", fmt.Errorf("sin fixture para {%s}", missing)
	}
	return out, nil
}

func code(body []byte) string {
	var p struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &p)
	return p.Code
}

func hasBody(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch
}

func headers() map[string]string {
	return map[string]string{"If-Match": `"1"`, "Idempotency-Key": uuid.NewString()}
}

// Run ejecuta la suite: cobertura del contrato y, por operación de ISP,
// los controles comunes (sin token → 401; token sin tid → 403
// TOKEN_SCOPE_INVALID) y el caso declarado.
func (s *Suite) Run(t *testing.T) {
	t.Helper()
	for _, p := range Check(s.Ops, s.Cases) {
		t.Error(p)
	}
	for _, op := range s.Ops {
		if op.Scope != "tenant" {
			continue
		}
		c, ok := s.Cases[op.ID]
		if !ok {
			continue
		}
		t.Run(op.ID, func(t *testing.T) {
			if c.Kind == Pending {
				if s.Mounted(op.Method, op.Path) {
					t.Fatalf("%s %s está implementada pero su caso sigue Pending (%s): declare su caso", op.Method, op.Path, c.Owner)
				}
				t.Skipf("pendiente: %s", c.Owner)
			}
			if !s.Mounted(op.Method, op.Path) {
				t.Fatalf("%s %s no está montada pero su caso no es Pending", op.Method, op.Path)
			}
			s.common(t, op, c)
			switch c.Kind {
			case ByID:
				s.byID(t, op, c)
			case List:
				s.list(t, op)
			case Create:
				s.create(t, op, c)
			default:
				t.Fatalf("tipo de caso desconocido %d", c.Kind)
			}
		})
	}
}

func (s *Suite) body(op Operation, c Case) any {
	if !hasBody(op.Method) {
		return nil
	}
	if c.Body == nil {
		return map[string]any{}
	}
	return c.Body
}

func (s *Suite) common(t *testing.T, op Operation, c Case) {
	t.Helper()
	path, err := fill(op.Path, s.ParamsA)
	if err != nil {
		t.Fatal(err)
	}
	if st, b := s.Do("", op.Method, path, s.body(op, c), headers()); st != http.StatusUnauthorized {
		t.Errorf("sin token: %d %s, quiero 401", st, b)
	}
	if st, b := s.Do(s.SessionA, op.Method, path, s.body(op, c), headers()); st != http.StatusForbidden || code(b) != "TOKEN_SCOPE_INVALID" {
		t.Errorf("token sin ISP: %d %s, quiero 403 TOKEN_SCOPE_INVALID", st, b)
	}
}

func (s *Suite) byID(t *testing.T, op Operation, c Case) {
	t.Helper()
	path, err := fill(op.Path, s.ParamsB)
	if err != nil {
		t.Fatal(err)
	}
	st, b := s.Do(s.TokenA, op.Method, path, s.body(op, c), headers())
	if st != http.StatusNotFound {
		t.Errorf("A sobre recurso de B (%s %s): %d %s, quiero 404", op.Method, path, st, b)
	}
	if hasBody(op.Method) {
		s.mismatch(t, op, c, path)
	}
}

func (s *Suite) list(t *testing.T, op Operation) {
	t.Helper()
	path, err := fill(op.Path, s.ParamsA)
	if err != nil {
		t.Fatal(err)
	}
	st, b := s.Do(s.TokenA, op.Method, path+"?limit=200", nil, nil)
	if st != http.StatusOK {
		t.Fatalf("listado de A: %d %s", st, b)
	}
	for _, id := range s.ForeignIDs {
		if strings.Contains(string(b), id) {
			t.Errorf("el listado de A contiene el ID %s de B", id)
		}
	}
	if strings.Contains(string(b), s.TenantB) {
		t.Errorf("el listado de A menciona el tenant B")
	}
	if len(op.PathParams()) > 0 {
		pb, err := fill(op.Path, s.ParamsB)
		if err != nil {
			t.Fatal(err)
		}
		if st, b := s.Do(s.TokenA, op.Method, pb, nil, nil); st != http.StatusNotFound {
			t.Errorf("listado bajo un padre de B: %d %s, quiero 404", st, b)
		}
	}
}

func (s *Suite) create(t *testing.T, op Operation, c Case) {
	t.Helper()
	path, err := fill(op.Path, s.ParamsA)
	if err != nil {
		t.Fatal(err)
	}
	s.mismatch(t, op, c, path)
	if len(op.PathParams()) > 0 {
		pb, err := fill(op.Path, s.ParamsB)
		if err != nil {
			t.Fatal(err)
		}
		if st, b := s.Do(s.TokenA, op.Method, pb, s.body(op, c), headers()); st != http.StatusNotFound {
			t.Errorf("alta bajo un padre de B: %d %s, quiero 404", st, b)
		}
	}
}

// mismatch: un tenant_id de B en el cuerpo → 403 TENANT_MISMATCH.
func (s *Suite) mismatch(t *testing.T, op Operation, c Case, path string) {
	t.Helper()
	body := map[string]any{}
	for k, v := range c.Body {
		body[k] = v
	}
	body["tenant_id"] = s.TenantB
	if st, b := s.Do(s.TokenA, op.Method, path, body, headers()); st != http.StatusForbidden || code(b) != "TENANT_MISMATCH" {
		t.Errorf("tenant_id de B en el cuerpo: %d %s, quiero 403 TENANT_MISMATCH", st, b)
	}
}
