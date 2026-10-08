// Package wireguard es el módulo de el rol wireguard de `horus`
// (ADR-0025, docs/services.md).
//
// Estado (I0-04): stub que solo se registra y queda listo en /readyz; la
// lógica de negocio llega en historias posteriores.
package wireguard

import (
	"context"

	"github.com/hcdestroyer/horus-flow/packages/go/module"
)

// Role es el nombre del rol en HORUS_ROLES: control WireGuard: IPAM de plataforma, peers y scripts.
const Role = "wireguard"

// Register construye el módulo del rol wireguard (firma module.Factory).
func Register(_ context.Context, _ module.Deps) (module.Module, error) {
	return module.Idle(), nil
}
