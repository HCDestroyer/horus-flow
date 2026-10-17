package api

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ServiceNotificationStats → NotificationStats.
const ServiceNotificationStats = "alerts.NotificationStats"

// NotificationCount son las notificaciones de un canal y tipo de evento.
type NotificationCount struct {
	ChannelKind string // email | telegram | librenms
	EventType   string // finding_opened, router_down… ("" si no consta)
	Sent        int
	Failed      int
}

// NotificationStats resume las notificaciones (sin las de prueba) creadas en
// [from, to) para el reporte semanal del rol reporting (I3).
type NotificationStats interface {
	NotificationStats(ctx context.Context, tenant uuid.UUID, from, to time.Time) ([]NotificationCount, error)
}
