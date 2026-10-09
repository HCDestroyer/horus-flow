// Package gateway es el módulo del rol gateway de `horus` (ADR-0025,
// docs/services.md): borde HTTP de la app. Monta en proceso las rutas de los
// módulos locales detrás de su middleware de borde (tabla gateway-routes del
// contrato C5: autenticación, ámbito del token, permiso grueso, revocación,
// re-autenticación, Idempotency-Key, TENANT_MISMATCH, rate limit y auditoría
// de accesos de plataforma) y sirve GET /api/v1/system/status.
//
// Debe registrarse después de los módulos que proveen sus dependencias en
// module.Services (auth: validador de tokens, sesiones y auditoría). Si auth
// no es local, las claves públicas vienen de HORUS_JWT_PUBLIC_KEYS_FILE y la
// comprobación remota de sesiones llega con el cliente gRPC de
// SessionService (I1); sin ella las rutas autenticadas responden 503.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/gateway/internal/edge"
	"github.com/hcdestroyer/horus-flow/services/gateway/internal/routes"
)

// Role es el nombre del rol en HORUS_ROLES: borde HTTP de la app: authN, rate limit, WebSocket y montaje de las rutas de los roles locales.
const Role = "gateway"

// Config del gateway.
type Config struct {
	// PublicKeys PEM aceptadas si auth no está en el proceso
	// (HORUS_JWT_PUBLIC_KEYS_FILE).
	PublicKeys string `env:"HORUS_JWT_PUBLIC_KEYS"`
	Issuer     string `env:"HORUS_AUTH_ISSUER" envDefault:"horus-auth"`
}

// Register construye el módulo del rol gateway (firma module.Factory).
func Register(ctx context.Context, deps module.Deps) (module.Module, error) {
	cfg, err := config.Load[Config](deps.Environ)
	if err != nil {
		return nil, fmt.Errorf("%s config: %w", Role, err)
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	dev := deps.Common.Env == "" || deps.Common.Env == config.EnvDev
	verifier, ok := module.Lookup[*authz.Verifier](deps.Services, authapi.ServiceVerifier)
	if !ok && cfg.PublicKeys != "" {
		keys, err := authz.ParsePublicKeysPEM([]byte(cfg.PublicKeys))
		if err != nil {
			return nil, fmt.Errorf("gateway: HORUS_JWT_PUBLIC_KEYS_FILE: %w", err)
		}
		verifier, ok = authz.NewVerifier(keys, cfg.Issuer, nil), true
	}
	if !ok {
		if !dev {
			return nil, errors.New("gateway: no token verifier (auth role not local and HORUS_JWT_PUBLIC_KEYS_FILE unset)")
		}
		logger.WarnContext(ctx, "gateway inactive: no token verifier (dev; auth role not configured)")
		return module.Idle(), nil
	}
	table, err := routes.Load()
	if err != nil {
		return nil, err
	}
	sessions, _ := module.Lookup[authapi.SessionChecker](deps.Services, authapi.ServiceSessions)
	audit, _ := module.Lookup[authapi.AuditRecorder](deps.Services, authapi.ServiceAudit)
	if sessions == nil {
		logger.WarnContext(ctx, "gateway: auth not local; authenticated routes answer 503 until the remote SessionService client exists")
	}
	e := edge.New(edge.Options{
		Routes: table, Verifier: verifier, Sessions: sessions, Audit: audit, Local: deps.Routes, Logger: logger,
	})
	if err := deps.Routes.SetEdge(e.Middleware); err != nil {
		return nil, err
	}
	deps.Routes.HandleFunc("GET /api/v1/system/status", systemStatus(deps.Routes))
	logger.InfoContext(ctx, "gateway edge mounted", slog.Int("routes", len(table)), slog.Bool("session_check", sessions != nil))
	return module.Idle(), nil
}

// systemStatus sirve GET /api/v1/system/status: capacidades según qué
// módulos atienden rutas en este proceso. El detalle de componentes es de
// plataforma (I1).
func systemStatus(local edge.Matcher) http.HandlerFunc {
	probe := func(method, path string) string {
		r, _ := http.NewRequestWithContext(context.Background(), method, path, nil)
		if local.Matches(r) {
			return "ok"
		}
		return "unavailable"
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		caps := map[string]string{
			"auth":          probe(http.MethodGet, "/api/v1/me"),
			"inventory":     probe(http.MethodGet, "/api/v1/sites"),
			"wireguard":     probe(http.MethodGet, "/api/v1/wireguard/peers"),
			"ingest":        probe(http.MethodGet, "/api/v1/flow-exporters"),
			"realtime":      probe(http.MethodPost, "/api/v1/ws/tickets"),
			"analytics":     probe(http.MethodGet, "/api/v1/analytics/traffic/top"),
			"detection":     probe(http.MethodGet, "/api/v1/security/summary"),
			"notifications": probe(http.MethodGet, "/api/v1/notification-channels"),
		}
		status := "ok"
		if caps["auth"] != "ok" || caps["inventory"] != "ok" {
			status = "degraded"
		}
		jsonapi.Write(w, http.StatusOK, map[string]any{
			"status": status, "checked_at": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
			"capabilities": caps, "components": []any{}, "disk_usage_ratio": nil,
		})
	}
}
