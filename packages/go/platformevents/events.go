// Package platformevents es el registro de eventos de plataforma de Horus
// (D23, docs/observability.md §11): arranques y paradas de cada rol con su
// versión, caídas detectadas (parada no limpia, healthcheck, reinicio del
// agente WireGuard), migraciones aplicadas, degradaciones (búfer de flujos al
// 70 %, ClickHouse inaccesible, spool activo), fallos definitivos de envío de
// alertas y cambios de configuración efectiva entre arranques.
//
// Los eventos se guardan en PostgreSQL (`platform_events.event`, retención
// configurable, 90 días por defecto) y se escriben además como línea de log
// `platform event` (rastro aunque PostgreSQL esté caído). Si PostgreSQL no
// responde, el registrador los retiene en memoria (acotado) y, si hay
// directorio de datos, en un spool en disco que vuelca al recuperarse.
//
// Nunca llevan datos de clientes: ni IPs de clientes ni secretos; tenant_id
// solo cuando el evento es de un ISP (p. ej. fallo de envío de una alerta).
//
// Los módulos registran con [Emit] (no-op si el proceso no tiene registrador);
// el proceso `horus` crea el [Recorder] y lo fija con [SetDefault].
package platformevents

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Tipos de evento (columna kind; enumerado cerrado, documentado en
// docs/observability.md §11).
const (
	KindProcessStarted      = "process_started"
	KindProcessStopped      = "process_stopped"
	KindUncleanShutdown     = "unclean_shutdown"
	KindRoleStarted         = "role_started"
	KindRoleStopped         = "role_stopped"
	KindRoleFailed          = "role_failed"
	KindDependencyDown      = "dependency_down"
	KindDependencyRecovered = "dependency_recovered"
	KindMigrationApplied    = "migration_applied"
	KindBufferHigh          = "buffer_high"
	KindBufferRecovered     = "buffer_recovered"
	KindSpoolActive         = "spool_active"
	KindSpoolDrained        = "spool_drained"
	KindAlertDeliveryFailed = "alert_delivery_failed"
	KindConfigChanged       = "config_changed"
	KindAgentRestarted      = "agent_restarted"
	KindContainerRestarted  = "container_restarted"
)

// Kinds es la lista de tipos válidos (filtro de la API).
var Kinds = []string{KindProcessStarted, KindProcessStopped, KindUncleanShutdown, KindRoleStarted, KindRoleStopped, KindRoleFailed,
	KindDependencyDown, KindDependencyRecovered, KindMigrationApplied, KindBufferHigh, KindBufferRecovered, KindSpoolActive,
	KindSpoolDrained, KindAlertDeliveryFailed, KindConfigChanged, KindAgentRestarted, KindContainerRestarted}

// Severidades.
const (
	SeverityInfo  = "info"
	SeverityWarn  = "warn"
	SeverityError = "error"
)

// Event es un evento de plataforma.
type Event struct {
	ID         uuid.UUID      `json:"id"`
	OccurredAt time.Time      `json:"occurred_at"`
	Kind       string         `json:"kind"`
	Severity   string         `json:"severity"`
	Process    string         `json:"process"`
	Instance   string         `json:"instance"`
	Role       string         `json:"role,omitempty"`
	Version    string         `json:"version,omitempty"`
	TenantID   *uuid.UUID     `json:"tenant_id,omitempty"`
	Message    string         `json:"message"`
	Details    map[string]any `json:"details,omitempty"`
	TraceID    string         `json:"trace_id,omitempty"`
}

// Emitter registra eventos.
type Emitter interface {
	Record(ctx context.Context, ev Event)
}

var def atomic.Pointer[emitterBox]

type emitterBox struct{ e Emitter }

// SetDefault fija el registrador del proceso (nil lo quita).
func SetDefault(e Emitter) {
	if e == nil {
		def.Store(nil)
		return
	}
	def.Store(&emitterBox{e: e})
}

// Emit registra ev con el registrador del proceso (no-op sin registrador).
func Emit(ctx context.Context, ev Event) {
	if b := def.Load(); b != nil {
		b.e.Record(ctx, ev)
	}
}
