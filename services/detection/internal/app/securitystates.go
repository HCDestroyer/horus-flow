package app

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/services/detection/api/securitystates"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/postgres"
)

var _ securitystates.Reader = (*Service)(nil)

// SecurityStates implementa securitystates.Reader (estado de seguridad por
// lotes de las integraciones, contrato ArgosISP §3.5).
func (s *Service) SecurityStates(ctx context.Context, customerIDs []uuid.UUID) (map[uuid.UUID]securitystates.State, error) {
	_, t, allowed, err := s.scope(ctx, PermRead)
	if err != nil {
		return nil, err
	}
	if len(customerIDs) > securitystates.MaxCustomers {
		return nil, fmt.Errorf("detection: %d customers (max %d)", len(customerIDs), securitystates.MaxCustomers)
	}
	out := map[uuid.UUID]securitystates.State{}
	if len(customerIDs) == 0 {
		return out, nil
	}
	var rows []postgres.SecurityDetail
	err = s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		rows, err = postgres.SecurityDetails(ctx, tx, customerIDs, allowed)
		return err
	})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.CustomerID] = securitystates.State{State: r.State, OpenFindings: r.OpenFindings, MaxSeverity: r.MaxSeverity, Since: r.Since}
	}
	return out, nil
}
