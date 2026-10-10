package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/lifecycle"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/platformevents"
)

// platformConfig configura el registro de eventos de plataforma del proceso.
type platformConfig struct {
	PostgresDSN      string               `env:"HORUS_POSTGRES_DSN"`
	PostgresPassword observability.Secret `env:"HORUS_POSTGRES_PASSWORD"`
	DataDir          string               `env:"HORUS_DATA_DIR"`
	// Enabled desactiva el registro en PostgreSQL (los eventos siguen en logs).
	Enabled bool `env:"HORUS_PLATFORM_EVENTS" envDefault:"true"`
	// Retention de platform_events.event; la aplica el proceso con el rol
	// gateway (uno por instalación).
	Retention time.Duration `env:"HORUS_PLATFORM_EVENTS_RETENTION" envDefault:"2160h"`
	// Instance distingue réplicas del mismo proceso (por defecto el hostname:
	// el id del contenedor, estable en `docker restart`).
	Instance string `env:"HORUS_INSTANCE"`
	// MonitorEvery es el periodo del vigilante de salud y búferes.
	MonitorEvery time.Duration `env:"HORUS_PLATFORM_MONITOR_INTERVAL" envDefault:"15s"`
	// BufferHighRatio: ocupación de un stream de telemetría que se registra
	// como degradación (70 %, architecture.md §9.4).
	BufferHighRatio float64 `env:"HORUS_PLATFORM_BUFFER_HIGH_RATIO" envDefault:"0.7"`
}

// platform es el registro de eventos de plataforma del proceso: crea el
// Recorder, registra arranque y parada, detecta paradas no limpias y
// cambios de configuración entre arranques y vigila la salud.
type platform struct {
	cfg     platformConfig
	common  config.Common
	environ []string
	roles   []string
	logger  *slog.Logger
	rec     *platformevents.Recorder
	store   *platformevents.Store
	db      *pgdb.DB
	started time.Time
	hreg    *health.Registry
	bus     func() *natsx.Bus
}

func newPlatform(cfg config.Common, environ []string, logger *slog.Logger) (*platform, error) {
	pc, err := config.Load[platformConfig](environ)
	if err != nil {
		return nil, fmt.Errorf("platform events config: %w", err)
	}
	if pc.Instance == "" {
		pc.Instance, _ = os.Hostname()
	}
	p := &platform{cfg: pc, common: cfg, environ: environ, roles: cfg.Roles, logger: logger, started: time.Now().UTC()}
	opts := platformevents.Options{Logger: logger, Process: cfg.Process, Instance: pc.Instance, Version: version}
	if pc.Enabled && pc.PostgresDSN != "" {
		db, err := pgdb.Open(context.Background(), pgdb.Config{DSN: pc.PostgresDSN, Password: pc.PostgresPassword.Reveal(), MaxConns: 2,
			StatementTimeout: 10 * time.Second, AppRole: platformevents.AppRole, PlatformRole: platformevents.PlatformRole})
		if err != nil {
			return nil, fmt.Errorf("platform events: %w", err)
		}
		p.db, p.store = db, platformevents.NewStore(db)
		opts.Store = p.store
		if slices.Contains(cfg.Roles, "gateway") {
			opts.Retention = pc.Retention
		}
		if pc.DataDir != "" && writableDir(pc.DataDir) {
			opts.SpoolDir = filepath.Join(pc.DataDir, "platform-events", cfg.Process)
		}
	}
	p.rec = platformevents.NewRecorder(opts)
	return p, nil
}

func writableDir(dir string) bool {
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return false
	}
	f, err := os.CreateTemp(dir, ".horus-wtest-*")
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return true
}

// hook es el componente del ciclo de vida: arranca justo después del
// servidor de administración y para el penúltimo (después de los roles).
func (p *platform) hook() lifecycle.Hook {
	return lifecycle.Hook{
		Name: "platform-events",
		Start: func(ctx context.Context) error {
			platformevents.SetDefault(p.rec)
			pgdb.SetMigrationObserver(p.migrationObserved)
			cfg := effectiveConfig(p.environ)
			p.rec.Record(ctx, platformevents.Event{Kind: platformevents.KindProcessStarted, OccurredAt: p.started,
				Message: "process started", Details: map[string]any{"roles": p.roles, "commit": commit(), "go_version": runtime.Version(),
					"config_hash": configHash(cfg), "config": cfg, "pid": os.Getpid()}})
			return nil
		},
		Run: func(ctx context.Context) error {
			if p.store != nil {
				p.prepare(ctx)
			}
			p.rec.Run(ctx)
			return nil
		},
		Stop: func(ctx context.Context) error {
			p.rec.Record(ctx, platformevents.Event{Kind: platformevents.KindProcessStopped, Message: "process stopped cleanly",
				Details: map[string]any{"uptime_seconds": int(time.Since(p.started).Seconds())}})
			fctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			err := p.rec.Flush(fctx)
			if err != nil {
				p.logger.WarnContext(ctx, "platform events not flushed on shutdown (kept in logs and spool)", slog.Any("error", err))
			}
			pgdb.SetMigrationObserver(nil)
			platformevents.SetDefault(nil)
			if p.db != nil {
				p.db.Close()
			}
			return nil
		},
	}
}

// prepare aplica la migración del registro (reintenta mientras PostgreSQL
// no responda), registra una parada no limpia del arranque anterior y el
// cambio de configuración efectiva.
func (p *platform) prepare(ctx context.Context) {
	backoff := time.Second
	for {
		if _, err := platformevents.Migrate(ctx, p.db, p.logger); err == nil {
			break
		} else if ctx.Err() != nil {
			return
		} else {
			p.logger.WarnContext(ctx, "platform events: migration pending (PostgreSQL unavailable?)", slog.Any("error", err),
				slog.Duration("retry_in", backoff))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(2*backoff, 30*time.Second)
	}
	last, err := p.store.LastLifecycle(ctx, p.common.Process, p.cfg.Instance, p.started)
	if err == nil && last != nil && last.Kind == platformevents.KindProcessStarted {
		p.rec.Record(ctx, platformevents.Event{Kind: platformevents.KindUncleanShutdown, Severity: platformevents.SeverityError,
			Message: "previous run ended without a clean stop (kill -9, OOM, crash or host reboot)",
			Details: map[string]any{"previous_started_at": last.OccurredAt, "previous_version": last.Version,
				"previous_event_id": last.ID.String()}})
	}
	prev, err := p.store.LastStarted(ctx, p.common.Process, p.started)
	if err != nil || prev == nil {
		return
	}
	prevCfg := map[string]string{}
	if m, ok := prev.Details["config"].(map[string]any); ok {
		for k, v := range m {
			prevCfg[k] = fmt.Sprint(v)
		}
	}
	cur := effectiveConfig(p.environ)
	changed := configDiff(prevCfg, cur)
	if len(changed) > 0 || prev.Version != version {
		p.rec.Record(ctx, platformevents.Event{Kind: platformevents.KindConfigChanged, Severity: platformevents.SeverityInfo,
			Message: "effective configuration or version changed since the previous start",
			Details: map[string]any{"changed_keys": changed, "previous_version": prev.Version, "previous_config_hash": prev.Details["config_hash"],
				"config_hash": configHash(cur)}})
	}
}

func (p *platform) migrationObserved(ctx context.Context, schema string, applied int, v int64, err error) {
	ev := platformevents.Event{Kind: platformevents.KindMigrationApplied, Message: "database migrations applied",
		Details: map[string]any{"schema": schema, "applied": applied, "version": v}}
	if err != nil {
		ev.Severity, ev.Message = platformevents.SeverityError, "database migration failed"
		ev.Details["error"] = err.Error()
	}
	p.rec.Record(ctx, ev)
}

// monitorHook vigila la salud de los roles (cambios de estado de cada
// dependencia confirmados en dos muestras) y, en el proceso con el rol
// gateway, la ocupación de los streams de telemetría de NATS.
func (p *platform) monitorHook() lifecycle.Hook {
	return lifecycle.Hook{Name: "platform-monitor", Run: func(ctx context.Context) error {
		if p.hreg == nil {
			return nil
		}
		m := &monitor{p: p, checks: map[string]*checkTrack{}, high: map[string]bool{}}
		t := time.NewTicker(p.cfg.MonitorEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
			}
			mctx := observability.EnsureTrace(context.WithoutCancel(ctx))
			rctx, cancel := context.WithTimeout(mctx, 5*time.Second)
			m.health(rctx)
			if slices.Contains(p.roles, "gateway") && p.bus != nil {
				if b := p.bus(); b != nil {
					m.buffers(rctx, b.JS)
				}
			}
			cancel()
		}
	}}
}

type checkTrack struct {
	failing  bool // estado confirmado
	pending  int  // muestras seguidas en el estado contrario
	critical bool
	lastErr  string
}

type monitor struct {
	p      *platform
	checks map[string]*checkTrack
	high   map[string]bool
}

func (m *monitor) health(ctx context.Context) {
	rep, _ := m.p.hreg.Report(ctx)
	for role, rr := range rep.Roles {
		for name, cr := range rr.Checks {
			key := role + "/" + name
			tr, ok := m.checks[key]
			if !ok {
				tr = &checkTrack{critical: cr.Critical}
				m.checks[key] = tr
			}
			failing := cr.Status == health.CheckFail
			if failing == tr.failing {
				tr.pending = 0
				continue
			}
			tr.pending++
			if failing {
				tr.lastErr = cr.Error
			}
			if tr.pending < 2 {
				continue
			}
			tr.failing, tr.pending = failing, 0
			ev := platformevents.Event{Role: role, Details: map[string]any{"dependency": name, "critical": cr.Critical}}
			if failing {
				ev.Kind, ev.Severity, ev.Message = platformevents.KindDependencyDown, platformevents.SeverityWarn, "dependency check failing: "+name
				if cr.Critical {
					ev.Severity = platformevents.SeverityError
				}
				ev.Details["error"] = tr.lastErr
				ev.Details["role_status"] = string(rr.Status)
			} else {
				ev.Kind, ev.Severity, ev.Message = platformevents.KindDependencyRecovered, platformevents.SeverityInfo, "dependency check recovered: "+name
			}
			m.p.rec.Record(ctx, ev)
		}
	}
}

// bufferStreams son los streams de telemetría cuya ocupación indica
// autonomía restante ante una caída de ClickHouse (architecture.md §9.4).
var bufferStreams = []string{"TLM_FLOWS", "TLM_SNMP"}

func (m *monitor) buffers(ctx context.Context, js jetstream.JetStream) {
	for _, name := range bufferStreams {
		s, err := js.Stream(ctx, name)
		if err != nil {
			if !errors.Is(err, jetstream.ErrStreamNotFound) && ctx.Err() == nil {
				m.p.logger.DebugContext(ctx, "platform monitor: stream info", slog.String("stream", name), slog.Any("error", err))
			}
			continue
		}
		info := s.CachedInfo()
		if info == nil || info.Config.MaxBytes <= 0 {
			continue
		}
		ratio := float64(info.State.Bytes) / float64(info.Config.MaxBytes)
		high := m.high[name]
		switch {
		case !high && ratio >= m.p.cfg.BufferHighRatio:
			m.high[name] = true
			m.p.rec.Record(ctx, platformevents.Event{Kind: platformevents.KindBufferHigh, Severity: platformevents.SeverityWarn,
				Message: "telemetry buffer above threshold (ClickHouse slow or down?)",
				Details: map[string]any{"stream": name, "ratio": round2(ratio), "bytes": info.State.Bytes, "max_bytes": info.Config.MaxBytes,
					"messages": info.State.Msgs}})
		case high && ratio < m.p.cfg.BufferHighRatio-0.1:
			m.high[name] = false
			m.p.rec.Record(ctx, platformevents.Event{Kind: platformevents.KindBufferRecovered, Severity: platformevents.SeverityInfo,
				Message: "telemetry buffer back below threshold", Details: map[string]any{"stream": name, "ratio": round2(ratio)}})
		}
	}
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

// roleEvent registra el arranque, la parada o el fallo de un rol.
func roleEvent(ctx context.Context, kind, role string, err error) {
	ev := platformevents.Event{Kind: kind, Role: role, Message: strings.ReplaceAll(kind, "_", " ")}
	if err != nil {
		ev.Severity = platformevents.SeverityError
		ev.Details = map[string]any{"error": err.Error()}
	}
	platformevents.Emit(ctx, ev)
}
