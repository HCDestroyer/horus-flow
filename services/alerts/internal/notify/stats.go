package notify

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	alertsapi "github.com/hcdestroyer/horus-flow/services/alerts/api"
)

// Stats implementa alertsapi.NotificationStats sobre el repositorio.
type Stats struct{ Store *Store }

var _ alertsapi.NotificationStats = Stats{}

// NotificationStats cuenta, por canal y tipo de evento, las entregas
// enviadas y fallidas creadas en [from, to) (sin las de prueba; RLS del tenant).
func (s Stats) NotificationStats(ctx context.Context, tenant uuid.UUID, from, to time.Time) ([]alertsapi.NotificationCount, error) {
	var out []alertsapi.NotificationCount
	err := s.Store.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT channel_kind, coalesce(event_type, ''),
				count(*) FILTER (WHERE status = 'sent'), count(*) FILTER (WHERE status = 'failed')
			FROM alerts.notification_delivery
			WHERE NOT is_test AND created_at >= $1 AND created_at < $2
			GROUP BY 1, 2 ORDER BY 1, 2`, from, to)
		if err != nil {
			return fmt.Errorf("alerts: notification stats: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var c alertsapi.NotificationCount
			if err := rows.Scan(&c.ChannelKind, &c.EventType, &c.Sent, &c.Failed); err != nil {
				return fmt.Errorf("alerts: notification stats: %w", err)
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}
