package app

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/services/detection/api/customerfindings"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

var _ customerfindings.Reader = (*Service)(nil)

// ActiveFindings implementa customerfindings.Reader (consulta 360 por IP).
func (s *Service) ActiveFindings(ctx context.Context, customerID uuid.UUID, limit int) ([]customerfindings.Finding, error) {
	_, t, allowed, err := s.scope(ctx, PermRead)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var rows []domain.Finding
	err = s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		rows, err = postgres.List(ctx, tx, postgres.ListFilter{States: []string{domain.StateOpen, domain.StateAcknowledged},
			CustomerID: &customerID, AllowedSites: allowed, Sort: "-severity", Limit: limit})
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]customerfindings.Finding, 0, len(rows))
	for i := range rows {
		f := &rows[i]
		sum := f.Summary.Text
		if sum == "" {
			sum = f.Kind
		}
		out = append(out, customerfindings.Finding{ID: f.ID, Kind: f.Kind, Severity: f.Severity, State: f.State, Summary: sum, OpenedAt: f.OpenedAt})
	}
	return out, nil
}
