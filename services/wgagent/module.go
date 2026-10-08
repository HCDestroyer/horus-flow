// Package wgagent es el módulo de el rol wg-agent de `horus`
// (ADR-0025, docs/services.md).
//
// Estado (I0-04): stub que solo se registra y queda listo en /readyz; la
// lógica de negocio llega en historias posteriores.
package wgagent

import (
	"context"

	"github.com/hcdestroyer/horus-flow/packages/go/module"
)

// Role es el nombre del rol en HORUS_ROLES: aplicación de la configuración WireGuard en el kernel.
const Role = "wg-agent"

// Register construye el módulo del rol wg-agent (firma module.Factory).
func Register(_ context.Context, _ module.Deps) (module.Module, error) {
	return module.Idle(), nil
}
