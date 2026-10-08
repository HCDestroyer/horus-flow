// Package analytics es el módulo de los roles analytics, reporting de `horus`
// (ADR-0025, docs/services.md).
//
// Estado (I0-04): stub que solo se registra y queda listo en /readyz; la
// lógica de negocio llega en historias posteriores.
package analytics

import (
	"context"

	"github.com/hcdestroyer/horus-flow/packages/go/module"
)

// Role es el nombre del rol en HORUS_ROLES: consultas, dashboards y widgets.
const Role = "analytics"

// Register construye el módulo del rol analytics (firma module.Factory).
func Register(_ context.Context, _ module.Deps) (module.Module, error) {
	return module.Idle(), nil
}

// RoleReporting es el nombre del rol en HORUS_ROLES: generación de reportes (CPU intensiva).
const RoleReporting = "reporting"

// RegisterReporting construye el módulo del rol reporting (firma module.Factory).
func RegisterReporting(_ context.Context, _ module.Deps) (module.Module, error) {
	return module.Idle(), nil
}
