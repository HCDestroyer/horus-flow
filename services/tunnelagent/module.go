// Package tunnelagent es el módulo del rol tunnel-agent de `horus` (ADR-0025,
// ADR-0031, docs/tunnels.md): aplica en el hub los túneles IPsec IKEv2
// route-based (interfaces XFRM + conexiones del demonio strongSwan por vici)
// y GRE (interfaces ip_gre) que le envía wireguard
// (TunnelAgentService.ApplyTunnels) y le reporta cada 15 s el estado de las
// SA y los contadores (TunnelControlService.ReportTunnelStatus).
//
// Contenedor propio con CAP_NET_ADMIN en la red del anfitrión, separado de
// wg-agent (otro secreto: las PSK; otro fallo: el demonio IKE) y del demonio
// IKE (horus-ipsec: expuesto a Internet en UDP 500/4500, sin certificados
// gRPC). Sin PostgreSQL ni KEK: las PSK llegan por mTLS y solo viven en la
// memoria del demonio IKE. Fail-static: nunca borra túneles por un fallo
// del control.
package tunnelagent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/grpcx"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	tunapi "github.com/hcdestroyer/horus-flow/services/tunnelagent/api"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/api/tunnelv1"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/adapters/grpcapi"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/adapters/linux"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/adapters/memdev"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/adapters/vici"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/app"
	modcfg "github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/config"
)

// Role es el nombre del rol en HORUS_ROLES: túneles IPsec IKEv2 y GRE del hub.
const Role = "tunnel-agent"

type mod struct {
	agent    *app.Agent
	cfg      modcfg.Config
	tls      grpcx.TLSConfig
	logger   *slog.Logger
	services *module.Services
	closer   func() error
	dev      bool
}

// Register construye el módulo del rol tunnel-agent (firma module.Factory).
// No toca el kernel ni el demonio IKE hasta el primer estado deseado.
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
	if cfg.TLSMode == grpcx.ModeDisabled && !dev {
		return nil, errors.New("tunnel-agent: GRPC_TLS_MODE=disabled is only allowed in dev (mTLS is mandatory for the agent)")
	}
	m := &mod{cfg: cfg, logger: logger, services: deps.Services, dev: dev,
		tls: grpcx.TLSConfig{Mode: cfg.TLSMode, CAFile: cfg.TLSCAFile, CertFile: cfg.TLSCertFile, KeyFile: cfg.TLSKeyFile, ServerName: "wireguard"}}
	var (
		links app.Links
		ike   app.IKE
	)
	switch cfg.Driver {
	case modcfg.DriverMemory:
		if !dev {
			return nil, errors.New("tunnel-agent: HORUS_TUNNELAGENT_DRIVER=memory is only allowed in dev")
		}
		links, ike = memdev.NewHost(), memdev.NewIKE()
		m.closer = func() error { return nil }
	case modcfg.DriverKernel:
		h, err := linux.Open(linux.Options{Underlay: cfg.Underlay, GRESet: cfg.GRESet, SourceSet: cfg.SourceSet})
		if err != nil {
			return nil, err
		}
		links, ike, m.closer = h, vici.New(cfg.ViciSocket), h.Close
	default:
		return nil, fmt.Errorf("tunnel-agent: unknown HORUS_TUNNELAGENT_DRIVER %q", cfg.Driver)
	}
	m.agent = app.New(app.Options{Links: links, IKE: ike, HubID: cfg.HubID, Logger: logger, ReportEvery: cfg.ReportEvery})
	if deps.Services != nil {
		if err := deps.Services.Provide(tunapi.ServiceAgent, m.agent); err != nil {
			return nil, err
		}
	}
	logger.InfoContext(ctx, "tunnel-agent ready", slog.Any("config", cfg))
	return m, nil
}

// Run sirve TunnelAgentService (si wireguard no está en el proceso) y
// reporta el estado al control cada 15 s.
func (m *mod) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	errc := make(chan error, 1)
	if c, ok := module.Lookup[tunapi.Control](m.services, tunapi.ServiceControl); ok {
		m.agent.SetControl(c) // en proceso
	} else {
		s, err := grpcx.NewServer(m.tls, m.logger)
		if err != nil {
			if !m.dev {
				return err
			}
			m.logger.WarnContext(ctx, "tunnel-agent idle (dev): no mTLS material and wireguard not in this process", slog.String("error", err.Error()))
			<-ctx.Done()
			return nil
		}
		grpcapi.Register(s, m.agent)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := grpcx.Serve(ctx, s, m.cfg.GRPCAddr); err != nil {
				errc <- err
			}
		}()
		if m.cfg.ControlAddr != "" {
			conn, err := grpcx.Dial(m.cfg.ControlAddr, m.tls)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			m.agent.SetControl(grpcapi.Control{C: tunnelv1.NewTunnelControlServiceClient(conn)})
		} else {
			m.logger.WarnContext(ctx, "tunnel-agent: HORUS_WIREGUARD_GRPC_ADDR_REMOTE unset: status is not reported")
		}
	}
	runErr := m.agent.Run(ctx)
	wg.Wait()
	select {
	case err := <-errc:
		return err
	default:
	}
	return runErr
}

// Stop libera el socket netlink (las interfaces y SA quedan: fail-static).
func (m *mod) Stop(context.Context) error {
	if m.closer != nil {
		return m.closer()
	}
	return nil
}
