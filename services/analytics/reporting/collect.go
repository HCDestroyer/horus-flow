package reporting

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"

	alertsapi "github.com/hcdestroyer/horus-flow/services/alerts/api"
	secapi "github.com/hcdestroyer/horus-flow/services/detection/api/securityreport"
)

// Notas de sección cuando una fuente no está disponible (se imprimen en el
// PDF y en el CSV en lugar de los datos: huecos, nunca ceros).
const (
	NoteSecurityUnavailable = "Datos de seguridad no disponibles: el rol detection no está en este proceso."
	NoteMetricsUnavailable  = "Datos de ClickHouse no disponibles (HORUS_CLICKHOUSE_DSN no configurado)."
	NoteAlertsUnavailable   = "Resumen de avisos no disponible: el rol alerts no está en este proceso."
	NoteSourceFailed        = "No se pudieron leer los datos: "
)

// RouterAvailability es la disponibilidad de un router en el periodo
// (snmp.device_1h: proporción de sondeos ICMP y SNMP correctos).
type RouterAvailability struct {
	RouterID  uuid.UUID
	Name      string
	Site      string
	ICMPRatio *float64 // nil = sin sondeos en el periodo
	SNMPRatio *float64
	Samples   uint64
}

// Availability es la sección de disponibilidad.
type Availability struct {
	Routers []RouterAvailability // peor disponibilidad primero
	// Overall es la media ponderada por sondeos de ICMPRatio.
	Overall *float64
}

// DayVolume es el volumen de un día local.
type DayVolume struct {
	Day        time.Time // medianoche local
	DownBytes  uint64
	UpBytes    uint64
	HasSamples bool
}

// Traffic es la sección de contexto de tráfico (flows.site_1h).
type Traffic struct {
	DownBytes, UpBytes         uint64
	PrevDownBytes, PrevUpBytes uint64
	Daily                      []DayVolume
}

// Data son los datos de un reporte de seguridad semanal.
type Data struct {
	TenantName  string
	Period      Period
	GeneratedAt time.Time
	// Masked: variante sin datos personales de clientes (MaskData).
	Masked bool

	Security     *secapi.Report
	SecurityNote string

	Availability     *Availability
	AvailabilityNote string

	Traffic     *Traffic
	TrafficNote string

	Alerts     []alertsapi.NotificationCount
	AlertsNote string
}

// Metrics lee de ClickHouse la disponibilidad y el tráfico de un tenant.
type Metrics interface {
	Availability(ctx context.Context, tenant uuid.UUID, p Period) (*Availability, error)
	Traffic(ctx context.Context, tenant uuid.UUID, p Period) (*Traffic, error)
}

// Collector reúne las secciones desde sus fuentes. Cada fuente es opcional:
// si falta o falla, la sección lleva una nota y el resto del reporte sigue.
type Collector struct {
	Security func() (secapi.Reader, bool)
	Alerts   func() (alertsapi.NotificationStats, bool)
	Metrics  func() Metrics // nil o devuelve nil = sin ClickHouse
	Now      func() time.Time
	Logger   *slog.Logger
}

func (c *Collector) logger() *slog.Logger {
	if c.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return c.Logger
}

// Collect reúne los datos del reporte de tenant en p.
func (c *Collector) Collect(ctx context.Context, tenant uuid.UUID, tenantName string, p Period) *Data {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	d := &Data{TenantName: tenantName, Period: p, GeneratedAt: now().UTC()}
	fail := func(section string, err error) string {
		c.logger().WarnContext(ctx, "reporting: section unavailable", slog.String("section", section),
			slog.String("tenant_id", tenant.String()), slog.Any("error", err))
		if errors.Is(err, context.DeadlineExceeded) {
			return NoteSourceFailed + "tiempo de espera agotado."
		}
		return NoteSourceFailed + "error interno (ver registros del rol reporting)."
	}

	if sec, ok := lookup(c.Security); ok {
		r, err := sec.SecurityReport(ctx, tenant, p.From, p.To)
		if err != nil {
			d.SecurityNote = fail("security", err)
		} else {
			d.Security = r
		}
	} else {
		d.SecurityNote = NoteSecurityUnavailable
	}

	var m Metrics
	if c.Metrics != nil {
		m = c.Metrics()
	}
	if m == nil {
		d.AvailabilityNote, d.TrafficNote = NoteMetricsUnavailable, NoteMetricsUnavailable
	} else {
		if a, err := m.Availability(ctx, tenant, p); err != nil {
			d.AvailabilityNote = fail("availability", err)
		} else {
			d.Availability = a
		}
		if t, err := m.Traffic(ctx, tenant, p); err != nil {
			d.TrafficNote = fail("traffic", err)
		} else {
			d.Traffic = t
		}
	}

	if al, ok := lookup(c.Alerts); ok {
		rows, err := al.NotificationStats(ctx, tenant, p.From, p.To)
		if err != nil {
			d.AlertsNote = fail("alerts", err)
		} else {
			d.Alerts = rows
			if d.Alerts == nil {
				d.Alerts = []alertsapi.NotificationCount{}
			}
		}
	} else {
		d.AlertsNote = NoteAlertsUnavailable
	}
	return d
}

func lookup[T any](f func() (T, bool)) (T, bool) {
	var zero T
	if f == nil {
		return zero, false
	}
	return f()
}

// SortAvailability ordena peor disponibilidad primero (sin datos al final) y por nombre.
func SortAvailability(rows []RouterAvailability) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].ICMPRatio, rows[j].ICMPRatio
		switch {
		case a == nil && b == nil:
			return rows[i].Name < rows[j].Name
		case a == nil:
			return false
		case b == nil:
			return true
		case *a != *b:
			return *a < *b
		}
		return rows[i].Name < rows[j].Name
	})
}
