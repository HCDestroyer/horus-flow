// Package module define el contrato entre `services/cmd/horus` y cada
// módulo de `services/<módulo>/` (ADR-0025, docs/conventions.md §2.1).
//
// Cada módulo expone una función con la firma de [Factory]:
//
//	func Register(ctx context.Context, deps module.Deps) (module.Module, error)
//
// Register construye el módulo con cableado manual (lee su configuración de
// deps.Environ, registra sus chequeos en deps.Health y sus rutas en
// deps.Routes) sin abrir conexiones de larga duración; las abre en Start.
// El binario arranca solo los módulos de los roles de HORUS_ROLES.
package module

import (
	"context"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
)

// Deps son las dependencias de plataforma que recibe un módulo.
type Deps struct {
	// Role es el nombre del rol que se está registrando.
	Role string
	// Logger ya lleva service y role.
	Logger *slog.Logger
	// Health es el estado de readiness del rol: el módulo añade aquí los
	// chequeos de sus dependencias (críticas o degradables).
	Health *health.Role
	// Metrics es el registro Prometheus del proceso.
	Metrics prometheus.Registerer
	// Routes monta handlers en la API REST del proceso (HTTPAddr).
	Routes *httpx.ServiceMux
	// Common es la configuración común del proceso.
	Common config.Common
	// Environ es el entorno del proceso, para config.Load de la configuración
	// propia del módulo.
	Environ []string
	// Services es el registro en proceso de contratos entre módulos
	// (puede ser nil en tests de un solo módulo).
	Services *Services
}

// Module es un módulo en ejecución. Run trabaja hasta que ctx se cancela y
// entonces devuelve nil tras terminar lo que tenga en curso; un error antes
// de eso apaga el proceso.
type Module interface {
	Run(ctx context.Context) error
}

// Starter lo implementa un módulo que necesita inicializarse de forma
// síncrona antes de que el rol se marque listo (abrir conexiones, cargar
// cachés).
type Starter interface {
	Start(ctx context.Context) error
}

// Stopper lo implementa un módulo que libera recursos al apagar (se llama
// tras cancelar Run, en orden inverso de arranque).
type Stopper interface {
	Stop(ctx context.Context) error
}

// Factory construye el módulo de un rol.
type Factory func(ctx context.Context, deps Deps) (Module, error)

// Func adapta una función a [Module].
type Func func(ctx context.Context) error

// Run implementa Module.
func (f Func) Run(ctx context.Context) error { return f(ctx) }

// Idle devuelve un módulo sin trabajo propio que espera al apagado. Lo usan
// los roles cuya lógica aún no existe y los que solo sirven rutas HTTP.
func Idle() Module {
	return Func(func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})
}
