// Package auth es el módulo de el rol auth de `horus`
// (ADR-0025, docs/services.md).
//
// Estado (I0-04): stub que solo se registra y queda listo en /readyz; la
// lógica de negocio llega en historias posteriores.
package auth

import (
	"context"

	"github.com/hcdestroyer/horus-flow/packages/go/module"
)

// Role es el nombre del rol en HORUS_ROLES: identidad, tenants, membresías, roles, sesiones y auditoría.
const Role = "auth"

// Register construye el módulo del rol auth (firma module.Factory).
func Register(_ context.Context, _ module.Deps) (module.Module, error) {
	return module.Idle(), nil
}
