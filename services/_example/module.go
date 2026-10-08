// Package example es el módulo de ejemplo de Horus (plantilla de scripts/new-module.sh).
//
// Muestra el patrón común a todos los módulos (docs/conventions.md §2):
//
//   - configuración propia HORUS_EXAMPLE_* cargada con packages/go/config;
//   - capas internal/{config,domain,app,adapters} con cableado manual aquí;
//   - chequeos de dependencias en /readyz (degradables o críticos);
//   - rutas REST montadas en la API del proceso bajo /api/v1/example/.
package example

import (
	"context"
	"fmt"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/services/_example/internal/adapters/httpapi"
	"github.com/hcdestroyer/horus-flow/services/_example/internal/app"
	modcfg "github.com/hcdestroyer/horus-flow/services/_example/internal/config"
)

// Role es el nombre del rol en HORUS_ROLES.
const Role = "example"

// Register construye el módulo (firma module.Factory).
func Register(_ context.Context, deps module.Deps) (module.Module, error) {
	cfg, err := config.Load[modcfg.Config](deps.Environ)
	if err != nil {
		return nil, fmt.Errorf("%s config: %w", Role, err)
	}
	if cfg.DependencyAddr != "" {
		// Dependencia degradable: si no responde, el rol aparece "degraded"
		// en /readyz con el nombre "dependency" y el proceso sigue listo.
		deps.Health.AddCheck(health.Check{
			Name:  "dependency",
			Probe: health.DialProbe("tcp", cfg.DependencyAddr),
		})
	}
	svc := app.NewService(cfg.Greeting)
	httpapi.NewHandler(svc, deps.Logger).Mount(deps.Routes)
	return module.Idle(), nil
}
