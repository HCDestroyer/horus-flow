// Package gateway es el módulo de el rol gateway de `horus`
// (ADR-0025, docs/services.md).
//
// Estado (I0-04): stub que solo se registra y queda listo en /readyz; la
// lógica de negocio llega en historias posteriores.
package gateway

import (
	"context"

	"github.com/hcdestroyer/horus-flow/packages/go/module"
)

// Role es el nombre del rol en HORUS_ROLES: borde HTTP de la app: authN, rate limit, WebSocket y montaje de las rutas de los roles locales.
const Role = "gateway"

// Register construye el módulo del rol gateway (firma module.Factory).
func Register(_ context.Context, _ module.Deps) (module.Module, error) {
	return module.Idle(), nil
}
