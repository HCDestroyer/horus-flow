// Package analytics es el módulo de los roles analytics, reporting de `horus`
// (ADR-0025, docs/services.md).
//
// FLOW (I1-08, I1-29): consultas de tráfico sobre los agregados de
// ClickHouse (tops, series, atribución, tráfico de un cliente), propuestas
// del modo descubrimiento y la resolución en servidor de los datos de los
// widgets de tráfico (contrato C9), que el catálogo de dashboards de CORE
// consume por module.Services (api.ServiceWidgetData). Toda consulta filtra
// por tenant_id y fija SQL_horus_tenant (row policy de ClickHouse).
package analytics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	analyticsapi "github.com/hcdestroyer/horus-flow/services/analytics/api/trafficwidgets"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/chread"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/httpapi"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/app"
	modcfg "github.com/hcdestroyer/horus-flow/services/analytics/internal/config"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

// Role es el nombre del rol en HORUS_ROLES: consultas, dashboards y widgets.
const Role = "analytics"

// Register construye el módulo del rol analytics (firma module.Factory).
func Register(ctx context.Context, deps module.Deps) (module.Module, error) {
	cfg, err := config.Load[modcfg.Config](deps.Environ)
	if err != nil {
		return nil, fmt.Errorf("%s config: %w", Role, err)
	}
	log := deps.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	inv := flowinv.NewStore(nil)
	if cfg.InventoryFile != "" {
		s, err := flowinv.LoadFile(cfg.InventoryFile)
		if err != nil {
			return nil, fmt.Errorf("%s: inventory: %w", Role, err)
		}
		inv.Swap(s)
	}
	m := &mod{cfg: cfg, log: log, inv: inv, q: &lazyQuerier{}}
	m.svc = &app.Service{Q: m.q, Inv: inv}
	m.widgets = app.NewWidgets(m.svc, cfg.CacheTTL)
	if deps.Services != nil {
		_ = deps.Services.Provide(analyticsapi.ServiceWidgetData, analyticsapi.WidgetDataProvider(m.widgets))
	}
	if deps.Routes != nil {
		if v := verifier(cfg, deps); v != nil {
			httpapi.New(authz.NewGuard(v), m.svc).Mount(deps.Routes)
		} else {
			log.WarnContext(ctx, "analytics: no token verifier (auth not local, HORUS_JWT_PUBLIC_KEYS unset): traffic API not served")
		}
	}
	if deps.Health != nil && cfg.ClickHouseDSN != "" {
		deps.Health.AddCheck(health.Check{Name: "clickhouse", Critical: false, Probe: m.q.Ping})
	}
	return m, nil
}

func verifier(cfg modcfg.Config, deps module.Deps) *authz.Verifier {
	if v, ok := module.Lookup[*authz.Verifier](deps.Services, authapi.ServiceVerifier); ok {
		return v
	}
	if cfg.PublicKeys == "" {
		return nil
	}
	keys, err := authz.ParsePublicKeysPEM([]byte(cfg.PublicKeys))
	if err != nil {
		return nil
	}
	return authz.NewVerifier(keys, cfg.Issuer, nil)
}

type mod struct {
	cfg     modcfg.Config
	log     *slog.Logger
	inv     *flowinv.Store
	q       *lazyQuerier
	svc     *app.Service
	widgets *app.Widgets
	loops   []func(context.Context)
}

// Start abre ClickHouse (si está configurado). Un ClickHouse caído no
// impide arrancar: las consultas responden 503 ANALYTICS_UNAVAILABLE.
func (m *mod) Start(ctx context.Context) error {
	if m.cfg.ClickHouseDSN == "" {
		m.log.WarnContext(ctx, "analytics: HORUS_CLICKHOUSE_DSN not set: traffic queries unavailable")
		return nil
	}
	user, pw := "", m.cfg.ClickHousePassword
	if m.cfg.AnalyticsPassword != "" {
		user, pw = chread.UserAnalytics, m.cfg.AnalyticsPassword
	}
	r, err := chread.Open(m.cfg.ClickHouseDSN, user, pw, m.cfg.QueryTimeout)
	if err != nil {
		return err
	}
	m.q.set(r)
	return m.startExtras(ctx)
}

// Run espera al apagado (y recarga el inventario).
func (m *mod) Run(ctx context.Context) error {
	if m.cfg.InventoryFile != "" {
		go flowinv.WatchFile(ctx, m.cfg.InventoryFile, m.inv, 5*time.Second, m.log)
	}
	for _, l := range m.loops {
		go l(ctx)
	}
	<-ctx.Done()
	return nil
}

// Stop cierra ClickHouse.
func (m *mod) Stop(context.Context) error {
	if r := m.q.get(); r != nil {
		_ = r.Close()
	}
	return nil
}

// RoleReporting es el nombre del rol en HORUS_ROLES: generación de reportes (CPU intensiva).
const RoleReporting = "reporting"

// RegisterReporting construye el módulo del rol reporting (firma module.Factory).
func RegisterReporting(_ context.Context, _ module.Deps) (module.Module, error) {
	return module.Idle(), nil
}

// lazyQuerier es el lector ClickHouse, disponible tras Start.
type lazyQuerier struct{ r atomic.Pointer[chread.Reader] }

func (l *lazyQuerier) set(r *chread.Reader) { l.r.Store(r) }
func (l *lazyQuerier) get() *chread.Reader  { return l.r.Load() }
func (l *lazyQuerier) Ping(ctx context.Context) error {
	r := l.get()
	if r == nil {
		return errors.New("clickhouse not configured")
	}
	return r.Ping(ctx)
}

// Query implementa app.Querier.
func (l *lazyQuerier) Query(ctx context.Context, tenant uuid.UUID, sql string, args ...any) (chread.Rows, context.CancelFunc, error) {
	r := l.get()
	if r == nil {
		return nil, nil, app.ErrNoBackend
	}
	return r.Query(ctx, tenant, sql, args...)
}
