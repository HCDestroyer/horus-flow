package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

var _ app.ViaStore = (*Store)(nil)

// CountViaDevices implementa app.ViaStore.
func (s *Store) CountViaDevices(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID) (int, error) {
	var n int
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM devices.router WHERE tenant_id = $1 AND via_router_id = $2 AND deleted_at IS NULL
			AND connection_mode = 'via_router'`, t.UUID(), routerID).Scan(&n)
	})
	return n, err
}

// ViaDevicesIn implementa app.ViaStore.
func (s *Store) ViaDevicesIn(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID, p netip.Prefix) (int, error) {
	var n int
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM devices.router WHERE tenant_id = $1 AND via_router_id = $2 AND deleted_at IS NULL
			AND connection_mode = 'via_router' AND management_address <<= $3::inet`, t.UUID(), routerID, p.String()).Scan(&n)
	})
	return n, err
}

const subnetCols = `id, tenant_id, router_id, prefix::text, created_at, coalesce(created_by, '')`

func scanSubnet(row pgx.Row) (*domain.ManagementSubnet, error) {
	var (
		m domain.ManagementSubnet
		p string
	)
	if err := row.Scan(&m.ID, &m.TenantID, &m.RouterID, &p, &m.CreatedAt, &m.CreatedBy); err != nil {
		return nil, notFound(err)
	}
	var err error
	if m.Prefix, err = netip.ParsePrefix(p); err != nil {
		return nil, fmt.Errorf("devices: bad management subnet %q: %w", p, err)
	}
	return &m, nil
}

// ListManagementSubnets implementa app.ViaStore.
func (s *Store) ListManagementSubnets(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID) ([]domain.ManagementSubnet, error) {
	out := []domain.ManagementSubnet{}
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+subnetCols+` FROM devices.management_subnet WHERE tenant_id = $1 AND router_id = $2 ORDER BY prefix`,
			t.UUID(), routerID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			m, err := scanSubnet(rows)
			if err != nil {
				return err
			}
			out = append(out, *m)
		}
		return rows.Err()
	})
	return out, err
}

// CreateManagementSubnet implementa app.ViaStore.
func (s *Store) CreateManagementSubnet(ctx context.Context, t pgdb.TenantID, m *domain.ManagementSubnet) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO devices.management_subnet (id, tenant_id, router_id, prefix, created_at, created_by)
			VALUES ($1, $2, $3, $4::cidr, $5, $6)`, m.ID, t.UUID(), m.RouterID, m.Prefix.String(), m.CreatedAt, m.CreatedBy)
		code, constraint, ok := pgdb.ConstraintError(err)
		switch {
		case ok && constraint == "ex_management_subnet__overlap":
			return app.ErrSubnetOverlap
		case ok && code == pgdb.SQLStateForeignKey:
			return app.ErrRefNotFound
		}
		return err
	})
}

// DeleteManagementSubnet implementa app.ViaStore.
func (s *Store) DeleteManagementSubnet(ctx context.Context, t pgdb.TenantID, routerID, id uuid.UUID) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM devices.management_subnet WHERE tenant_id = $1 AND router_id = $2 AND id = $3`, t.UUID(), routerID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		return nil
	})
}

// ManagementSubnetFor implementa app.ViaStore.
func (s *Store) ManagementSubnetFor(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID, addr netip.Addr) (*domain.ManagementSubnet, error) {
	var out *domain.ManagementSubnet
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = scanSubnet(tx.QueryRow(ctx, `SELECT `+subnetCols+` FROM devices.management_subnet
			WHERE tenant_id = $1 AND router_id = $2 AND prefix >>= $3::inet`, t.UUID(), routerID, addr.Unmap().String()))
		return err
	})
	if errors.Is(err, domain.ErrNotFound) {
		return nil, domain.ErrNotFound
	}
	return out, err
}
