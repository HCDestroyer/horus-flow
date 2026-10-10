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

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/services/collector/internal/app"
	modcfg "github.com/hcdestroyer/horus-flow/services/collector/internal/config"
	"github.com/hcdestroyer/horus-flow/services/collector/internal/spool"
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
	js     jetstream.JetStream
	engine *app.Engine
	// spoolErr: HORUS_COLLECTOR_SPOOL_DIR configurado pero no utilizable.
	spoolErr error
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
	m.nc, m.js = nc, js
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
		CollectorID: m.cfg.CollectorID, UDPReadBuffer: m.cfg.UDPReadBuffer, DecodeWorkers: m.cfg.EffectiveDecodeWorkers(),
		State: app.StateOptions{SilentAfter: m.cfg.SilentAfter, LossThreshold: m.cfg.LossThreshold,
			LossWindow: m.cfg.LossWindow, ClockSkew: m.cfg.ClockSkew, Interval: m.cfg.StateInterval},
	}, m.inv, jsSink{js: js}, kv, m.m, m.log)
	if b, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: app.CollectorStateBucket,
		Description: "Plantillas y secuencias del collector para continuar tras un reinicio (D23)", History: 1,
		Storage: jetstream.FileStorage}); err != nil {
		m.log.WarnContext(ctx, "collector state bucket unavailable: templates and sequences start empty after a restart", "error", err)
	} else {
		skv := app.JetStreamStateKV{KV: b}
		tpls, seqs, err := m.engine.RestoreState(ctx, skv, m.cfg.TemplateTTL)
		if err != nil {
			m.log.WarnContext(ctx, "collector state not restored", "error", err)
		} else {
			m.log.InfoContext(ctx, "collector state restored", "templates", tpls, "sequences", seqs)
		}
		m.engine.UsePersistence(skv)
	}
	if m.cfg.SpoolDir != "" {
		sp, err := spool.Open(spool.Options{Dir: m.cfg.SpoolDir, MaxBytes: m.cfg.SpoolBytes, SegmentBytes: m.cfg.SpoolSegmentBytes,
			Fsync: m.cfg.SpoolFsync, OnDrop: m.engine.Pub.SpoolDropped, Log: m.log})
		if err != nil {
			// Sin spool el collector sigue (búfer en memoria) y /readyz lo marca.
			m.spoolErr = err
			m.log.ErrorContext(ctx, "collector spool unavailable: running with the memory buffer only", "dir", m.cfg.SpoolDir, "error", err)
		} else {
			m.engine.Pub.UseSpool(sp, m.cfg.SpoolAfterRatio)
			st := sp.Stats()
			m.log.InfoContext(ctx, "collector spool ready", "dir", m.cfg.SpoolDir, "max_bytes", m.cfg.SpoolBytes,
				"pending_batches", st.Batches, "pending_records", st.Records)
		}
	} else {
		m.log.WarnContext(ctx, "collector spool disabled (HORUS_COLLECTOR_SPOOL_DIR not set): a NATS outage longer than the memory buffer loses flows")
	}
	if err := m.engine.Listen(m.cfg.Listen); err != nil {
		return fmt.Errorf("collector: listen: %w", err)
	}
	if m.health != nil {
		m.health.AddCheck(health.Check{Name: "spool", Critical: false, Probe: func(context.Context) error {
			if m.spoolErr != nil {
				return fmt.Errorf("spool unavailable: %w", m.spoolErr)
			}
			return m.engine.Pub.SpoolHealth()
		}})
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
	go flowinv.Keep(ctx, m.inv, m.cfg.InventoryFile, m.js, "flows-inventory-"+Role, m.log)
	return m.engine.Run(ctx)
}

// Stop cierra la conexión NATS (tras vaciar los lotes en Run).
func (m *mod) Stop(context.Context) error {
	if m.nc != nil {
		_ = m.nc.Drain()
	}
	return nil
}
