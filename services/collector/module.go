// Package collector es el módulo del rol collector de `horus` (ADR-0025,
// docs/services.md): receptor UDP IPFIX / NetFlow v9 (I1-03).
//
// Acepta solo datagramas de exportadores registrados (IP de túnel /32 del
// inventario), decodifica con las plantillas de cada exportador, mide los
// saltos de secuencia y el desfase de reloj, descarta flujos del túnel y de
// prefijos `excluded`, y publica lotes FlowBatch en TLM_FLOWS
// (horus.telemetry.flows.batch.<router_id>) sin bloquear nunca la recepción.
// Calcula además el estado del exportador (I1-09) en el bucket KV
// `flow_exporter_state` y publica sus eventos.
//
// Sin HORUS_NATS_URL, en desarrollo el rol queda inactivo con un aviso.
package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/services/collector/internal/app"
	modcfg "github.com/hcdestroyer/horus-flow/services/collector/internal/config"
)

// Role es el nombre del rol en HORUS_ROLES: receptor UDP NetFlow/IPFIX/sFlow.
const Role = "collector"

// Register construye el módulo del rol collector (firma module.Factory).
func Register(ctx context.Context, deps module.Deps) (module.Module, error) {
	cfg, err := config.Load[modcfg.Config](deps.Environ)
	if err != nil {
		return nil, fmt.Errorf("%s config: %w", Role, err)
	}
	log := deps.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	dev := deps.Common.Env == "" || deps.Common.Env == config.EnvDev
	if cfg.NATSURL == "" || len(cfg.Listen) == 0 {
		if !dev {
			return nil, errors.New("collector: HORUS_NATS_URL and HORUS_COLLECTOR_LISTEN are required")
		}
		log.WarnContext(ctx, "collector inactive: HORUS_NATS_URL not set (dev)")
		return module.Idle(), nil
	}
	inv := flowinv.NewStore(nil)
	if cfg.InventoryFile != "" {
		s, err := flowinv.LoadFile(cfg.InventoryFile)
		if err != nil {
			return nil, fmt.Errorf("collector: inventory: %w", err)
		}
		inv.Swap(s)
	} else {
		log.WarnContext(ctx, "collector: HORUS_FLOWS_INVENTORY_FILE not set: every exporter is unknown")
	}
	return &mod{cfg: cfg, log: log, inv: inv, m: app.NewMetrics(deps.Metrics), health: deps.Health}, nil
}

type mod struct {
	cfg    modcfg.Config
	log    *slog.Logger
	inv    *flowinv.Store
	m      *app.Metrics
	health *health.Role
	nc     *nats.Conn
	engine *app.Engine
}

type jsSink struct{ js jetstream.JetStream }

func (s jsSink) PublishMsg(ctx context.Context, m *nats.Msg) error {
	_, err := s.js.PublishMsg(ctx, m)
	return err
}

// Start conecta a NATS, prepara el bucket de estado y abre los sockets UDP.
func (m *mod) Start(ctx context.Context) error {
	nc, js, err := flowbus.Connect(m.cfg.NATSURL, "horus-collector")
	if err != nil {
		return err
	}
	m.nc = nc
	if m.cfg.EnsureStreams {
		if err := flowbus.EnsureStreams(ctx, js, m.cfg.TLMMaxBytes); err != nil {
			return err
		}
	}
	var kv app.KV
	bucket, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: flowbus.ExporterStateBucket,
		Description: "Estado de los exportadores de flujos (I1-09)", History: 1, Storage: jetstream.FileStorage})
	if err != nil {
		m.log.WarnContext(ctx, "exporter state bucket unavailable", "error", err)
	} else {
		kv = app.JetStreamKV{KV: bucket}
	}
	m.engine = app.NewEngine(app.EngineOptions{
		Workers: m.cfg.Workers, QueueDatagrams: m.cfg.QueueDatagrams, BatchMaxRecords: m.cfg.BatchMaxRecords,
		BatchMaxAge: m.cfg.BatchMaxAge, BufferBytes: m.cfg.BufferBytes, PendingTTL: m.cfg.PendingTTL,
		CollectorID: m.cfg.CollectorID,
		State: app.StateOptions{SilentAfter: m.cfg.SilentAfter, LossThreshold: m.cfg.LossThreshold,
			LossWindow: m.cfg.LossWindow, ClockSkew: m.cfg.ClockSkew, Interval: m.cfg.StateInterval},
	}, m.inv, jsSink{js: js}, kv, m.m, m.log)
	if err := m.engine.Listen(m.cfg.Listen); err != nil {
		return fmt.Errorf("collector: listen: %w", err)
	}
	if m.health != nil {
		m.health.AddCheck(health.Check{Name: "nats", Critical: false, Probe: func(context.Context) error {
			if !nc.IsConnected() {
				return errors.New("nats disconnected")
			}
			return nil
		}})
	}
	m.log.InfoContext(ctx, "collector listening", "addrs", m.cfg.Listen, "exporters", len(m.inv.Load().Data().Exporters))
	return nil
}

// Run procesa datagramas y recarga el inventario hasta el apagado.
func (m *mod) Run(ctx context.Context) error {
	if m.cfg.InventoryFile != "" {
		go flowinv.WatchFile(ctx, m.cfg.InventoryFile, m.inv, 5*time.Second, m.log)
	}
	return m.engine.Run(ctx)
}

// Stop cierra la conexión NATS (tras vaciar los lotes en Run).
func (m *mod) Stop(context.Context) error {
	if m.nc != nil {
		_ = m.nc.Drain()
	}
	return nil
}
