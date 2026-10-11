package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ActiveFindingsBySeverity cuenta por tenant los hallazgos activos (open o
// acknowledged, como Summary) de una severidad (GET /platform/overview;
// proceso de plataforma, solo conteos).
func (s *Store) ActiveFindingsBySeverity(ctx context.Context, severity string) (map[uuid.UUID]int, error) {
	out := map[uuid.UUID]int{}
	err := s.DB.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT tenant_id, count(*) FROM detection.finding
			WHERE state IN ('open', 'acknowledged') AND severity = $1 GROUP BY tenant_id`, severity)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t uuid.UUID
			var n int
			if err := rows.Scan(&t, &n); err != nil {
				return err
			}
			out[t] = n
		}
		return rows.Err()
	})
	return out, err
}
