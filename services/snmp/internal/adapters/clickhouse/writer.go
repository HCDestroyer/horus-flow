package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

// DeviceRow es una fila de snmp.device_metrics.
type DeviceRow struct {
	TenantID, SiteID, RouterID uuid.UUID
	TS                         time.Time
	PollDurationMs             uint32
	ICMPOK, SNMPOK             bool
	UptimeSeconds              *uint64
	CPUPercent                 *float32
	CPUMaxCorePercent          *float32
	MemoryPercent              *float32
	MemoryUsedBytes            *uint64
	MemoryTotalBytes           *uint64
	TemperatureCelsius         *float32
	CPUTemperatureCelsius      *float32
	Voltage                    *float32
	ICMPRTTMs                  *float32
	ICMPLossRatio              *float32
}

// InterfaceRow es una fila de snmp.interface_metrics.
type InterfaceRow struct {
	TenantID, SiteID, RouterID, InterfaceID uuid.UUID
	IfIndex                                 uint32
	Name                                    string
	TS                                      time.Time
	OperUp                                  bool
	SpeedBps                                *uint64
	RxBps, TxBps, RxPps, TxPps              *float64
	RxErrors, TxErrors, RxDrops, TxDrops    *uint64
	UtilizationRx, UtilizationTx            *float32
}

// Batch es lo escrito en un ciclo.
type Batch struct {
	ID         string // token de deduplicación (ciclo + router)
	Devices    []DeviceRow
	Interfaces []InterfaceRow
}

// Writer escribe lotes en segundo plano con una cola acotada: si ClickHouse
// no responde la cola se llena y se descartan los lotes más viejos
// (horus_snmp_clickhouse_dropped_total); el sondeo nunca espera.
type Writer struct {
	conn    driver.Conn
	queue   chan Batch
	log     *slog.Logger
	dropped prometheus.Counter
	rows    *prometheus.CounterVec
	up      prometheus.Gauge
	wg      sync.WaitGroup
	once    sync.Once
	closing chan struct{}
}

// Open abre la conexión nativa (user sustituye al del DSN si no está vacío).
func Open(dsn, user, password string, queue int, reg prometheus.Registerer, log *slog.Logger) (*Writer, error) {
	if user != "" {
		u, err := url.Parse(dsn)
		if err != nil {
			return nil, fmt.Errorf("snmp: clickhouse dsn: %w", err)
		}
		u.User = url.User(user)
		dsn = u.String()
	}
	opts, err := ch.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("snmp: clickhouse dsn: %w", err)
	}
	if password != "" {
		opts.Auth.Password = password
	}
	conn, err := ch.Open(opts)
	if err != nil {
		return nil, err
	}
	if queue <= 0 {
		queue = 64
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	w := &Writer{conn: conn, queue: make(chan Batch, queue), log: log, closing: make(chan struct{}),
		dropped: prometheus.NewCounter(prometheus.CounterOpts{Name: "horus_snmp_clickhouse_dropped_total",
			Help: "Lotes de métricas SNMP descartados por cola llena o error persistente de ClickHouse."}),
		rows: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_snmp_clickhouse_insert_rows_total",
			Help: "Filas insertadas en ClickHouse por tabla."}, []string{"table"}),
		up: prometheus.NewGauge(prometheus.GaugeOpts{Name: "horus_clickhouse_up", Help: "ClickHouse responde (visto desde el rol).",
			ConstLabels: prometheus.Labels{"service": "snmp"}}),
	}
	if reg != nil {
		for _, c := range []prometheus.Collector{w.dropped, w.rows, w.up} {
			if err := reg.Register(c); err != nil {
				var already prometheus.AlreadyRegisteredError
				if !errors.As(err, &already) {
					return nil, err
				}
			}
		}
	}
	w.wg.Add(1)
	go w.run()
	return w, nil
}

// Ping comprueba la conexión (readiness).
func (w *Writer) Ping(ctx context.Context) error {
	err := w.conn.Ping(ctx)
	if err != nil {
		w.up.Set(0)
	} else {
		w.up.Set(1)
	}
	return err
}

// Enqueue encola un lote sin bloquear: con la cola llena se descarta el más
// viejo para hacer sitio.
func (w *Writer) Enqueue(b Batch) {
	if len(b.Devices) == 0 && len(b.Interfaces) == 0 {
		return
	}
	for {
		select {
		case w.queue <- b:
			return
		default:
		}
		select {
		case <-w.queue:
			w.dropped.Inc()
		default:
		}
	}
}

// Close vacía la cola (con plazo) y cierra la conexión.
func (w *Writer) Close(ctx context.Context) error {
	w.once.Do(func() { close(w.closing) })
	done := make(chan struct{})
	go func() { w.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	return w.conn.Close()
}

func (w *Writer) run() {
	defer w.wg.Done()
	for {
		select {
		case b := <-w.queue:
			w.write(b)
		case <-w.closing:
			for {
				select {
				case b := <-w.queue:
					w.write(b)
				default:
					return
				}
			}
		}
	}
}

func (w *Writer) write(b Batch) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := w.Insert(ctx, b); err != nil {
		w.up.Set(0)
		w.dropped.Inc()
		w.log.Warn("snmp: clickhouse insert failed (batch dropped, polling continues)", "error", err,
			"devices", len(b.Devices), "interfaces", len(b.Interfaces))
		return
	}
	w.up.Set(1)
}

// Insert escribe el lote de forma síncrona (lo usan run y los tests).
func (w *Writer) Insert(ctx context.Context, b Batch) error {
	if len(b.Devices) > 0 {
		c := ctx
		if b.ID != "" {
			c = ch.Context(ctx, ch.WithSettings(ch.Settings{"insert_deduplication_token": b.ID + ":device", "insert_deduplicate": 1}))
		}
		batch, err := w.conn.PrepareBatch(c, `INSERT INTO snmp.device_metrics (tenant_id, site_id, router_id, ts, poll_duration_ms, icmp_ok, snmp_ok,
			uptime_seconds, cpu_percent, cpu_max_core_percent, memory_percent, memory_used_bytes, memory_total_bytes, temperature_celsius,
			cpu_temperature_celsius, voltage, icmp_rtt_ms, icmp_loss_ratio)`)
		if err != nil {
			return fmt.Errorf("prepare device_metrics: %w", err)
		}
		for _, r := range b.Devices {
			if err := batch.Append(r.TenantID, r.SiteID, r.RouterID, r.TS.UTC(), r.PollDurationMs, b2u(r.ICMPOK), b2u(r.SNMPOK), r.UptimeSeconds,
				r.CPUPercent, r.CPUMaxCorePercent, r.MemoryPercent, r.MemoryUsedBytes, r.MemoryTotalBytes, r.TemperatureCelsius,
				r.CPUTemperatureCelsius, r.Voltage, r.ICMPRTTMs, r.ICMPLossRatio); err != nil {
				_ = batch.Abort()
				return fmt.Errorf("append device_metrics: %w", err)
			}
		}
		if err := batch.Send(); err != nil {
			return fmt.Errorf("insert device_metrics: %w", err)
		}
		w.rows.WithLabelValues("snmp.device_metrics").Add(float64(len(b.Devices)))
	}
	if len(b.Interfaces) > 0 {
		c := ctx
		if b.ID != "" {
			c = ch.Context(ctx, ch.WithSettings(ch.Settings{"insert_deduplication_token": b.ID + ":interface", "insert_deduplicate": 1}))
		}
		batch, err := w.conn.PrepareBatch(c, `INSERT INTO snmp.interface_metrics (tenant_id, site_id, router_id, interface_id, if_index, name, ts,
			oper_up, speed_bps, rx_bps, tx_bps, rx_pps, tx_pps, rx_errors_delta, tx_errors_delta, rx_drops_delta, tx_drops_delta,
			utilization_rx, utilization_tx)`)
		if err != nil {
			return fmt.Errorf("prepare interface_metrics: %w", err)
		}
		for _, r := range b.Interfaces {
			if err := batch.Append(r.TenantID, r.SiteID, r.RouterID, r.InterfaceID, r.IfIndex, r.Name, r.TS.UTC(), b2u(r.OperUp), r.SpeedBps,
				r.RxBps, r.TxBps, r.RxPps, r.TxPps, r.RxErrors, r.TxErrors, r.RxDrops, r.TxDrops, r.UtilizationRx, r.UtilizationTx); err != nil {
				_ = batch.Abort()
				return fmt.Errorf("append interface_metrics: %w", err)
			}
		}
		if err := batch.Send(); err != nil {
			return fmt.Errorf("insert interface_metrics: %w", err)
		}
		w.rows.WithLabelValues("snmp.interface_metrics").Add(float64(len(b.Interfaces)))
	}
	return nil
}

func b2u(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}
