// Package wireguard es el módulo del rol wireguard de `horus` (ADR-0025,
// docs/services.md): IPAM de túneles de plataforma, un peer por router con su
// /32 (identidad del exportador), script RouterOS de alta con token de
// enrolamiento de un uso y script inverso, enrolamiento público de la clave
// pública (POST /api/v1/enroll/wireguard), estado deseado del hub hacia
// wg-agent y estado observado (handshakes).
//
// Dependencias en proceso (module.Services): devices (routers, proyección
// del túnel y credenciales), auth (validador de tokens y auditoría) y, si
// comparte proceso, wg-agent. Si wg-agent está en otro contenedor se habla
// por gRPC con mTLS (HORUS_WGAGENT_ADDR; ControlService en
// HORUS_WIREGUARD_GRPC_ADDR). Sin HORUS_POSTGRES_DSN en dev queda inactivo.
package wireguard

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/grpcx"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	wgapi "github.com/hcdestroyer/horus-flow/services/wgagent/api"
	"github.com/hcdestroyer/horus-flow/services/wgagent/api/agentv1"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/adapters/grpcapi"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/adapters/httpapi"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/app"
	modcfg "github.com/hcdestroyer/horus-flow/services/wireguard/internal/config"
	"github.com/hcdestroyer/horus-flow/services/wireguard/migrations"
)

// Role es el nombre del rol en HORUS_ROLES: control WireGuard: IPAM de plataforma, peers y scripts.
const Role = "wireguard"

// hubNamespace deriva el ID del hub de su nombre si no se configura.
var hubNamespace = uuid.MustParse("0192f0d2-7a1e-7c3a-9b1d-2f6e8a4c1d55")

type mod struct {
	db       *pgdb.DB
	svc      *app.Service
	cfg      modcfg.Config
	logger   *slog.Logger
	services *module.Services
	tls      grpcx.TLSConfig
	dev      bool

	agentMu sync.RWMutex
	agent   wgapi.Agent
}

// Register construye el módulo del rol wireguard (firma module.Factory).
func Register(ctx context.Context, deps module.Deps) (module.Module, error) {
	cfg, err := config.Load[modcfg.Config](deps.Environ)
	if err != nil {
		return nil, fmt.Errorf("%s config: %w", Role, err)
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	dev := deps.Common.Env == "" || deps.Common.Env == config.EnvDev
	if cfg.PostgresDSN == "" {
		if !dev {
			return nil, errors.New("wireguard: HORUS_POSTGRES_DSN is required")
		}
		logger.WarnContext(ctx, "wireguard inactive: HORUS_POSTGRES_DSN not set (dev)")
		return module.Idle(), nil
	}
	verifier, ok := module.Lookup[*authz.Verifier](deps.Services, authapi.ServiceVerifier)
	if !ok {
		if cfg.PublicKeys == "" {
			return nil, errors.New("wireguard: no token verifier (auth role not local and HORUS_JWT_PUBLIC_KEYS_FILE unset)")
		}
		keys, err := authz.ParsePublicKeysPEM([]byte(cfg.PublicKeys))
		if err != nil {
			return nil, fmt.Errorf("wireguard: HORUS_JWT_PUBLIC_KEYS_FILE: %w", err)
		}
		verifier = authz.NewVerifier(keys, cfg.Issuer, nil)
	}
	hub, collector, err := hubFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	base, accessMode, err := access(cfg, dev)
	if err != nil {
		return nil, err
	}
	caPEM := ""
	if cfg.TLSMode == "self_signed" || cfg.TLSMode == "provided" || (cfg.TLSMode == "" && accessMode == "ip_only") {
		caPEM = strings.TrimSpace(cfg.PublicCertPEM)
		if caPEM == "" && !dev {
			return nil, errors.New("wireguard: HORUS_PUBLIC_TLS_CERT_FILE is required with self_signed/provided TLS (the RouterOS script must trust it; check-certificate=no is never used)")
		}
	}
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: cfg.PostgresDSN, Password: cfg.PostgresPassword.Reveal(),
		AppRole: cfg.AppRole, PlatformRole: cfg.PlatformRole, MaxConns: 10})
	if err != nil {
		return nil, err
	}
	m := &mod{db: db, cfg: cfg, logger: logger, services: deps.Services, dev: dev,
		tls: grpcx.TLSConfig{Mode: cfg.GRPCTLSMode, CAFile: cfg.TLSCAFile, CertFile: cfg.TLSCertFile, KeyFile: cfg.TLSKeyFile, ServerName: "wg-agent"}}
	m.svc = app.New(app.Options{
		Store: postgres.New(db),
		Devices: func() (devapi.Onboarding, bool) {
			return module.Lookup[devapi.Onboarding](deps.Services, devapi.ServiceOnboarding)
		},
		Agent: m.lookupAgent,
		Audit: func() (authapi.AuditRecorder, bool) {
			return module.Lookup[authapi.AuditRecorder](deps.Services, authapi.ServiceAudit)
		},
		Hub: hub, HubPublicKey: m.hubPublicKey, CollectorIP: collector, PublicBaseURL: base, AccessMode: accessMode,
		CACertPEM: caPEM, NTPServer: cfg.NTPServer, CacheEntries: cfg.CacheEntries, Logger: logger,
	})
	if deps.Services != nil {
		if err := deps.Services.Provide(wgapi.ServiceControl, m.svc); err != nil {
			db.Close()
			return nil, err
		}
	}
	if deps.Health != nil {
		deps.Health.AddCheck(health.Check{Name: "postgres", Critical: true, Probe: db.Ping})
	}
	if deps.Routes != nil {
		httpapi.New(m.svc, authz.NewGuard(verifier), pagination.NewCodec([]byte(cfg.CursorKey.Reveal())), logger).Mount(deps.Routes)
	}
	return natsx.WithRelay(ctx, deps, m, db, migrations.Schema)
}

func hubFromConfig(cfg modcfg.Config) (app.Hub, netip.Addr, error) {
	id := uuid.NewSHA1(hubNamespace, []byte(cfg.HubName))
	if cfg.HubID != "" {
		var err error
		if id, err = uuid.Parse(cfg.HubID); err != nil {
			return app.Hub{}, netip.Addr{}, fmt.Errorf("wireguard: HORUS_WG_HUB_ID: %w", err)
		}
	}
	var pools []netip.Prefix
	for _, c := range cfg.TunnelCIDRs {
		p, err := netip.ParsePrefix(strings.TrimSpace(c))
		if err != nil {
			return app.Hub{}, netip.Addr{}, fmt.Errorf("wireguard: HORUS_WG_TUNNEL_CIDRS: %w", err)
		}
		pools = append(pools, p)
	}
	services, err := netip.ParsePrefix(cfg.Services)
	if err != nil {
		return app.Hub{}, netip.Addr{}, fmt.Errorf("wireguard: HORUS_WG_SERVICES_CIDR: %w", err)
	}
	collector := services.Addr().Next()
	if cfg.CollectorIP != "" {
		if collector, err = netip.ParseAddr(cfg.CollectorIP); err != nil || !services.Contains(collector) {
			return app.Hub{}, netip.Addr{}, errors.New("wireguard: HORUS_COLLECTOR_IP must be inside HORUS_WG_SERVICES_CIDR")
		}
	}
	return app.Hub{ID: id, Name: cfg.HubName, Endpoint: cfg.Endpoint, ListenPort: cfg.Port, PublicKey: cfg.HubPubKey,
		ServicesCIDR: services, Pools: pools}, collector, nil
}

// access resuelve HORUS_PUBLIC_BASE_URL y el modo de acceso (D19: IP literal → ip_only).
func access(cfg modcfg.Config, dev bool) (string, string, error) {
	base := strings.TrimRight(cfg.PublicBaseURL, "/")
	if base == "" {
		if !dev {
			return "", "", errors.New("wireguard: HORUS_PUBLIC_BASE_URL is required (destination of the RouterOS enrollment fetch)")
		}
		base = "https://" + cfg.Endpoint
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", "", errors.New("wireguard: HORUS_PUBLIC_BASE_URL must be https://<host>")
	}
	mode := cfg.AccessMode
	if mode == "" {
		mode = "domain"
		if _, err := netip.ParseAddr(strings.Trim(u.Hostname(), "[]")); err == nil {
			mode = "ip_only"
		}
	}
	return base, mode, nil
}

func (m *mod) lookupAgent() (wgapi.Agent, bool) {
	if a, ok := module.Lookup[wgapi.Agent](m.services, wgapi.ServiceAgent); ok {
		return a, true
	}
	m.agentMu.RLock()
	defer m.agentMu.RUnlock()
	return m.agent, m.agent != nil
}

func (m *mod) hubPublicKey() string {
	if m.cfg.HubPubKey != "" {
		return m.cfg.HubPubKey
	}
	if k, ok := module.Lookup[wgapi.HubKey](m.services, wgapi.ServiceAgent); ok {
		return k.HubPublicKey()
	}
	return ""
}

func (m *mod) Start(ctx context.Context) error {
	if err := m.db.Ping(ctx); err != nil {
		return err
	}
	if m.cfg.Migrate {
		n, err := pgdb.Migrate(ctx, m.db, migrations.Schema, migrations.Postgres(), m.logger)
		if err != nil {
			return err
		}
		m.logger.InfoContext(ctx, "wireguard migrations applied", slog.Int("count", n))
	}
	return m.svc.Init(ctx)
}

// Run reconcilia peers con los routers, empuja el estado deseado al agente
// y, si el agente está en otro proceso, sirve ControlService.
func (m *mod) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	errc := make(chan error, 1)
	if _, local := module.Lookup[wgapi.Agent](m.services, wgapi.ServiceAgent); !local {
		s, err := grpcx.NewServer(m.tls, m.logger)
		switch {
		case err != nil && !m.dev:
			return err
		case err != nil:
			m.logger.WarnContext(ctx, "wireguard: no mTLS material (dev): ControlService not served", slog.String("error", err.Error()))
		default:
			grpcapi.Register(s, m.svc)
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := grpcx.Serve(ctx, s, m.cfg.GRPCAddr); err != nil {
					errc <- err
				}
			}()
		}
		if m.cfg.AgentAddr != "" {
			conn, err := grpcx.Dial(m.cfg.AgentAddr, m.tls)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			m.agentMu.Lock()
			m.agent = grpcapi.Agent{C: agentv1.NewAgentServiceClient(conn)}
			m.agentMu.Unlock()
		}
	}
	runErr := m.svc.Run(ctx, m.cfg.ReconcileEvery)
	wg.Wait()
	select {
	case err := <-errc:
		return err
	default:
	}
	return runErr
}

func (m *mod) Stop(context.Context) error {
	m.db.Close()
	return nil
}
