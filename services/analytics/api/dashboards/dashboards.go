// Package dashboards es el contrato en proceso del submódulo de dashboards de
// analytics (CORE, I1-15) para otros módulos (gateway: temas WebSocket de un
// kiosco y acceso a dashboard.<id>).
package dashboards

import (
	"context"

	"github.com/google/uuid"
)

// ServiceAccess es el nombre en module.Services de Access.
const ServiceAccess = "analytics.DashboardAccess"

// Access responde preguntas de acceso a dashboards.
type Access interface {
	// KioskTopics devuelve los topics WebSocket permitidos a un kiosco: los de
	// los widgets kiosk_allowed de sus dashboards asignados, dashboard.<id> de
	// cada uno y system.
	KioskTopics(ctx context.Context, tenantID, kioskID uuid.UUID) ([]string, error)
	// CanRead indica si un usuario (con sus permisos) puede leer el dashboard.
	CanRead(ctx context.Context, tenantID, userID uuid.UUID, perms map[string][]string, dashboardID uuid.UUID) (bool, error)
}
