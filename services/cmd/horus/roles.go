package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/hcdestroyer/horus-flow/packages/go/module"
	example "github.com/hcdestroyer/horus-flow/services/_example"
	"github.com/hcdestroyer/horus-flow/services/alerts"
	"github.com/hcdestroyer/horus-flow/services/analytics"
	"github.com/hcdestroyer/horus-flow/services/auth"
	"github.com/hcdestroyer/horus-flow/services/collector"
	"github.com/hcdestroyer/horus-flow/services/detection"
	"github.com/hcdestroyer/horus-flow/services/devices"
	"github.com/hcdestroyer/horus-flow/services/gateway"
	"github.com/hcdestroyer/horus-flow/services/ingester"
	"github.com/hcdestroyer/horus-flow/services/jobs"
	"github.com/hcdestroyer/horus-flow/services/snmp"
	"github.com/hcdestroyer/horus-flow/services/traffic"
	"github.com/hcdestroyer/horus-flow/services/wgagent"
	"github.com/hcdestroyer/horus-flow/services/wireguard"
	// new-module.sh:imports
)

// roleSpec asocia un rol de HORUS_ROLES con el constructor de su módulo.
type roleSpec struct {
	name    string
	factory module.Factory
	// hidden: el rol existe pero no entra en "all" (módulo de ejemplo).
	hidden bool
}

// roleCatalog es el catálogo de roles (ADR-0025, docs/services.md). El orden
// es el de arranque (y el inverso, el de parada), independientemente del
// orden en HORUS_ROLES: primero los módulos de plataforma, el gateway al
// final porque monta las rutas de los demás.
var roleCatalog = []roleSpec{
	{name: auth.Role, factory: auth.Register},
	{name: devices.Role, factory: devices.Register},
	{name: wireguard.Role, factory: wireguard.Register},
	{name: snmp.Role, factory: snmp.Register},
	{name: traffic.Role, factory: traffic.Register},
	{name: detection.Role, factory: detection.Register},
	{name: alerts.Role, factory: alerts.Register},
	{name: analytics.Role, factory: analytics.Register},
	{name: analytics.RoleReporting, factory: analytics.RegisterReporting},
	{name: ingester.Role, factory: ingester.Register},
	{name: jobs.Role, factory: jobs.Register},
	{name: collector.Role, factory: collector.Register},
	{name: wgagent.Role, factory: wgagent.Register},
	{name: example.Role, factory: example.Register, hidden: true},
	// new-module.sh:roles
	{name: gateway.Role, factory: gateway.Register},
}

// publicRoles devuelve los roles que entran en "all", en orden de arranque.
func publicRoles(catalog []roleSpec) []string {
	var out []string
	for _, r := range catalog {
		if !r.hidden {
			out = append(out, r.name)
		}
	}
	return out
}

// resolveRoles interpreta HORUS_ROLES / --roles: vacío o "all" = todos los
// roles públicos; si no, la lista indicada sin duplicados. Devuelve los roles
// en orden de arranque del catálogo. Falla ante un rol desconocido.
func resolveRoles(requested []string, catalog []roleSpec) ([]roleSpec, error) {
	want := map[string]bool{}
	all := false
	for _, r := range requested {
		r = strings.TrimSpace(r)
		switch r {
		case "":
		case "all":
			all = true
		default:
			want[r] = true
		}
	}
	if len(want) == 0 {
		all = true
	}
	var out []roleSpec
	for _, spec := range catalog {
		if (all && !spec.hidden) || want[spec.name] {
			out = append(out, spec)
			delete(want, spec.name)
		}
	}
	if len(want) > 0 {
		unknown := slices.Sorted(maps.Keys(want))
		return nil, fmt.Errorf("unknown role(s) %q; available: %s",
			strings.Join(unknown, ","), strings.Join(publicRoles(catalog), ","))
	}
	return out, nil
}

func roleNames(specs []roleSpec) []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.name
	}
	return out
}
