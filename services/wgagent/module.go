// Package wgagent es el módulo del rol wg-agent de `horus` (ADR-0025,
// docs/services.md): aplica en la interfaz WireGuard del hub el estado
// deseado que le envía wireguard (AgentService.ApplyDesiredState) y le
// reporta cada 15 s handshakes y contadores (ControlService.ReportStatus).
//
// Único contenedor con CAP_NET_ADMIN; sin acceso a PostgreSQL ni a la KEK.
// La clave privada del hub vive solo aquí (HORUS_WGAGENT_PRIVATE_KEY_FILE).
// Fail-static: nunca borra peers por un fallo del control.
package wgagent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/grpcx"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	wgapi "github.com/hcdestroyer/horus-flow/services/wgagent/api"
	"github.com/hcdestroyer/horus-flow/services/wgagent/api/agentv1"
	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/adapters/grpcapi"
	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/adapters/kernel"
	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/adapters/memdev"
	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/app"
	modcfg "github.com/hcdestroyer/horus-flow/services/wgagent/internal/config"
)

// Role es el nombre del rol en HORUS_ROLES: aplicación de la configuración WireGuard en el kernel.
const Role = "wg-agent"

type mod struct {
	agent    *app.Agent
	cfg      modcfg.Config
	tls      grpcx.TLSConfig
	logger   *slog.Logger
	services *module.Services
	closer   func() error
	dev      bool
}

// Register construye el módulo del rol wg-agent (firma module.Factory).
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
	priv, pub, err := hubKey(cfg, dev, logger)
	if err != nil {
		return nil, err
	}
	m := &mod{cfg: cfg, logger: logger, services: deps.Services, dev: dev,
		tls: grpcx.TLSConfig{Mode: cfg.TLSMode, CAFile: cfg.TLSCAFile, CertFile: cfg.TLSCertFile, KeyFile: cfg.TLSKeyFile, ServerName: "wireguard"}}
	if cfg.TLSMode == grpcx.ModeDisabled && !dev {
		return nil, errors.New("wg-agent: GRPC_TLS_MODE=disabled is only allowed in dev (mTLS is mandatory for the agent)")
	}
	var device app.Device
	switch cfg.Driver {
	case modcfg.DriverMemory:
		if !dev {
			return nil, errors.New("wg-agent: HORUS_WGAGENT_DRIVER=memory is only allowed in dev")
		}
		device = memdev.New()
		m.closer = func() error { return nil }
	case modcfg.DriverKernel:
		k, err := kernel.Open(cfg.Interface)
		if err != nil {
			return nil, err
		}
		device, m.closer = k, k.Close
	default:
		return nil, fmt.Errorf("wg-agent: unknown HORUS_WGAGENT_DRIVER %q", cfg.Driver)
	}
	m.agent = app.New(app.Options{Device: device, HubID: cfg.HubID, PrivateKey: priv, PublicKey: pub,
		ListenPort: cfg.ListenPort, Logger: logger, ReportEvery: cfg.ReportEvery})
	if deps.Services != nil {
		if err := deps.Services.Provide(wgapi.ServiceAgent, m.agent); err != nil {
			return nil, err
		}
	}
	logger.InfoContext(ctx, "wg-agent ready", slog.Any("config", cfg), slog.String("hub_public_key", pub))
	return m, nil
}

func hubKey(cfg modcfg.Config, dev bool, logger *slog.Logger) (priv, pub string, err error) {
	v := strings.TrimSpace(cfg.PrivateKey.Reveal())
	if v == "" {
		if !dev {
			return "", "", errors.New("wg-agent: HORUS_WGAGENT_PRIVATE_KEY_FILE is required outside dev")
		}
		k, err := wgtypes.GeneratePrivateKey()
		if err != nil {
			return "", "", fmt.Errorf("wg-agent: generate key: %w", err)
		}
		logger.Warn("wg-agent: ephemeral hub key (dev): routers must be re-provisioned after restart; set HORUS_WGAGENT_PRIVATE_KEY_FILE")
		return k.String(), k.PublicKey().String(), nil
	}
	k, err := wgtypes.ParseKey(v)
	if err != nil {
		return "", "", fmt.Errorf("wg-agent: HORUS_WGAGENT_PRIVATE_KEY_FILE: %w", err)
	}
	return k.String(), k.PublicKey().String(), nil
}

// Run sirve AgentService (si wireguard no está en el proceso) y reporta el
// estado al control cada 15 s.
func (m *mod) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	errc := make(chan error, 1)
	if c, ok := module.Lookup[wgapi.Control](m.services, wgapi.ServiceControl); ok {
		m.agent.SetControl(c) // en proceso
	} else {
		s, err := grpcx.NewServer(m.tls, m.logger)
		if err != nil {
			if !m.dev {
				return err
			}
			// Desarrollo sin certificados ni wireguard en el proceso: el agente
			// queda a la espera sin servir gRPC.
			m.logger.WarnContext(ctx, "wg-agent idle (dev): no mTLS material and wireguard not in this process", slog.String("error", err.Error()))
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
			m.agent.SetControl(grpcapi.Control{C: agentv1.NewControlServiceClient(conn)})
		} else {
			m.logger.WarnContext(ctx, "wg-agent: HORUS_WIREGUARD_GRPC_ADDR_REMOTE unset: status is not reported")
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

// Stop libera el cliente wgctrl (los peers quedan en la interfaz: fail-static).
func (m *mod) Stop(context.Context) error {
	if m.closer != nil {
		return m.closer()
	}
	return nil
}
