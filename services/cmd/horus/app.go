package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"strings"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/lifecycle"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// run es main sin efectos globales: recibe argumentos, entorno y salidas, y
// devuelve el código de salida. ctx se cancela al recibir la señal de apagado.
func run(ctx context.Context, args, environ []string, stdout, stderr io.Writer, catalog []roleSpec) int {
	if len(args) > 0 && args[0] == "healthcheck" {
		return healthcheck(ctx, args[1:], environ, stdout, stderr)
	}
	fs := flag.NewFlagSet("horus", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rolesFlag := fs.String("roles", "", "comma-separated roles to run (overrides HORUS_ROLES); \"all\" = every public role")
	showVersion := fs.Bool("version", false, "print version and available roles, then exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if *showVersion {
		_, _ = fmt.Fprintf(stdout, "horus %s (commit %s)\nroles: %s\n", version, commit(),
			strings.Join(publicRoles(catalog), ", "))
		return exitOK
	}

	cfg, err := config.Load[config.Common](environ)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "horus: %v\n", err)
		return exitUsage
	}
	if *rolesFlag != "" {
		cfg.Roles = strings.Split(*rolesFlag, ",")
	}
	roles, err := resolveRoles(cfg.Roles, catalog)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "horus: %v\n", err)
		return exitUsage
	}
	cfg.Roles = roleNames(roles)

	logger, _, err := observability.NewLogger(stdout, observability.LogConfig{
		Level: cfg.LogLevel, Format: cfg.LogFormat, Service: "horus",
		Process: cfg.Process, Version: version, Env: cfg.Env,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "horus: %v\n", err)
		return exitUsage
	}
	logger.InfoContext(ctx, "horus starting", slog.Any("config", cfg))

	mgr, err := build(ctx, cfg, roles, environ, logger)
	if err != nil {
		logger.ErrorContext(ctx, "horus failed to build", slog.Any("error", err))
		return exitFailure
	}
	if err := mgr.Run(ctx); err != nil {
		// El detalle ya lo registró lifecycle.
		return exitFailure
	}
	return exitOK
}

// build cablea a mano el proceso: telemetría, servidor de administración,
// módulos de los roles y servidor de API.
func build(ctx context.Context, cfg config.Common, roles []roleSpec, environ []string, logger *slog.Logger) (*lifecycle.Manager, error) {
	hreg := health.NewRegistry(health.Options{Service: cfg.Process, Version: version})
	metrics := observability.NewRegistry(observability.BuildInfo{Service: cfg.Process, Version: version, Commit: commit()})
	httpMetrics, err := httpx.NewMetrics(metrics)
	if err != nil {
		return nil, fmt.Errorf("http metrics: %w", err)
	}
	api := httpx.NewMux(httpMetrics, logger)

	mgr := lifecycle.New(lifecycle.Options{
		Logger:          logger,
		StartTimeout:    cfg.StartTimeout,
		DrainDelay:      cfg.ShutdownDelay,
		ShutdownTimeout: cfg.ShutdownTimeout,
		OnDrain: func() {
			hreg.SetDraining()
			logger.Info("draining: readiness set to unavailable", slog.Duration("delay", cfg.ShutdownDelay))
		},
	})

	// El servidor de administración arranca el primero y para el último:
	// /healthz responde durante el arranque y /readyz informa del drenaje.
	admin := httpx.NewServer("admin", cfg.AdminAddr, httpx.AdminHandler(httpx.AdminOptions{
		Health: hreg, Gatherer: metrics, Pprof: cfg.PprofEnabled,
	}), logger, httpx.ServerOptions{})
	mgr.Append(admin.Hook())

	// Contratos entre módulos con implementación en proceso (ADR-0025 §1).
	services := module.NewServices()
	for _, spec := range roles {
		rlog := observability.ForRole(logger, spec.name)
		hrole := hreg.Role(spec.name)
		rctx := observability.WithRole(ctx, spec.name)
		mod, err := spec.factory(rctx, module.Deps{
			Role:     spec.name,
			Logger:   rlog,
			Health:   hrole,
			Metrics:  metrics,
			Routes:   api.ForService(spec.name),
			Common:   cfg,
			Environ:  environ,
			Services: services,
		})
		if err != nil {
			return nil, fmt.Errorf("register role %s: %w", spec.name, err)
		}
		mgr.Append(roleHook(spec.name, mod, hrole, rlog))
	}

	// La API arranca la última (cuando los roles están listos) y para la
	// primera, drenando las peticiones en curso antes de parar los módulos.
	if api.Routes() > 0 {
		srv := httpx.NewServer("api", cfg.HTTPAddr, api.Handler(), logger, httpx.ServerOptions{})
		mgr.Append(srv.Hook())
	}
	return mgr, nil
}

// roleHook adapta un módulo al ciclo de vida y mantiene su estado en /readyz.
func roleHook(name string, mod module.Module, hrole *health.Role, logger *slog.Logger) lifecycle.Hook {
	return lifecycle.Hook{
		Name: "role:" + name,
		Start: func(ctx context.Context) error {
			ctx = observability.WithRole(ctx, name)
			if s, ok := mod.(module.Starter); ok {
				if err := s.Start(ctx); err != nil {
					return err //nolint:wrapcheck // lifecycle añade "start role:<rol>"
				}
			}
			hrole.SetState(health.StateRunning)
			logger.InfoContext(ctx, "role started")
			return nil
		},
		Run: func(ctx context.Context) error {
			return mod.Run(observability.WithRole(ctx, name))
		},
		Stop: func(ctx context.Context) error {
			ctx = observability.WithRole(ctx, name)
			hrole.SetState(health.StateStopping)
			if s, ok := mod.(module.Stopper); ok {
				if err := s.Stop(ctx); err != nil {
					return err //nolint:wrapcheck // lifecycle añade "stop role:<rol>"
				}
			}
			logger.InfoContext(ctx, "role stopped")
			return nil
		},
	}
}

// commit devuelve la revisión VCS embebida por `go build` o "unknown".
func commit() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && s.Value != "" {
				return s.Value
			}
		}
	}
	return "unknown"
}
