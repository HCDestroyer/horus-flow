// Package routes carga la tabla declarativa de rutas del gateway (C5,
// packages/schemas/openapi/v0/gateway-routes.yaml, generada de las
// extensiones x-* de las specs). Se embebe una copia; un test comprueba que
// no diverge del contrato.
package routes

import (
	_ "embed"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed gateway-routes.v0.yaml
var tableYAML []byte

// TableYAML devuelve la copia embebida (test de sincronía).
func TableYAML() []byte { return tableYAML }

// Permisos especiales (defaults.special_permissions).
const (
	PermPublic          = "public"
	PermAuthenticated   = "authenticated"
	PermRefreshCookie   = "refresh_cookie"
	PermKioskCookie     = "kiosk_cookie"
	PermEnrollmentToken = "enrollment_token"
	PermWidgetType      = "widget_type"
	PermDashboardAccess = "dashboard_access"
	PermKioskSelf       = "kiosk_self"
)

// Route es una entrada (prefijo + método) de la tabla.
type Route struct {
	Module     string
	Prefix     string // plantilla /api/v1/sites/{site_id}
	Method     string
	Scope      string // public | session | tenant | platform
	Permission string
	Principals []string
	Reauth     bool
	Idempotent bool // Idempotency-Key obligatoria
	RateLimit  string
	Audited    bool
}

// SelfAuthenticating indica rutas cuyo dueño autentica por cookie o token
// propio (el gateway no exige Bearer).
func (r Route) SelfAuthenticating() bool {
	switch r.Permission {
	case PermPublic, PermRefreshCookie, PermKioskCookie, PermEnrollmentToken:
		return true
	}
	return false
}

// AllowsKiosk indica si un JWT de kiosco puede usar la ruta.
func (r Route) AllowsKiosk() bool { return slices.Contains(r.Principals, "kiosk") }

type entry struct {
	Prefix         string            `yaml:"prefix"`
	Scope          string            `yaml:"scope"`
	Methods        map[string]string `yaml:"methods"`
	Principals     []string          `yaml:"principals"`
	Reauth         []string          `yaml:"reauth"`
	IdempotencyReq []string          `yaml:"idempotency_key_required"`
	RateLimit      string            `yaml:"rate_limit"`
	Audited        []string          `yaml:"audited"`
}

type file struct {
	Version  string `yaml:"version"`
	Defaults struct {
		Principals []string `yaml:"principals"`
	} `yaml:"defaults"`
	Modules map[string][]entry `yaml:"modules"`
}

// Load interpreta la tabla embebida.
func Load() ([]Route, error) { return Parse(tableYAML) }

// Parse interpreta una tabla.
func Parse(data []byte) ([]Route, error) {
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("gateway routes: %w", err)
	}
	var out []Route
	for mod, entries := range f.Modules {
		for _, e := range entries {
			if !strings.HasPrefix(e.Prefix, "/api/v1/") {
				return nil, fmt.Errorf("gateway routes: %s: prefix %q outside /api/v1", mod, e.Prefix)
			}
			switch e.Scope {
			case "public", "session", "tenant", "platform":
			default:
				return nil, fmt.Errorf("gateway routes: %s %s: unknown scope %q", mod, e.Prefix, e.Scope)
			}
			principals := e.Principals
			if len(principals) == 0 {
				principals = f.Defaults.Principals
			}
			for m, perm := range e.Methods {
				m = strings.ToUpper(m)
				switch m {
				case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
				default:
					return nil, fmt.Errorf("gateway routes: %s %s: method %q", mod, e.Prefix, m)
				}
				if perm == "" {
					return nil, fmt.Errorf("gateway routes: %s %s %s: empty permission", mod, m, e.Prefix)
				}
				out = append(out, Route{
					Module: mod, Prefix: e.Prefix, Method: m, Scope: e.Scope, Permission: perm, Principals: principals,
					Reauth: slices.Contains(e.Reauth, m), Idempotent: slices.Contains(e.IdempotencyReq, m),
					RateLimit: e.RateLimit, Audited: slices.Contains(e.Audited, m),
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Prefix != out[j].Prefix {
			return out[i].Prefix < out[j].Prefix
		}
		return out[i].Method < out[j].Method
	})
	return out, nil
}
