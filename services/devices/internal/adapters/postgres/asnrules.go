package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

var _ app.AsnRuleStore = (*Store)(nil)

const asnRuleCols = `a.id, a.tenant_id, a.asn, a.name, a.category_slug, a.service_slug, a.note, a.created_by,
	a.created_at, a.updated_at, a.deleted_at, a.version`

func scanAsnRule(row pgx.Row) (*domain.AsnRule, error) {
	var (
		r   domain.AsnRule
		asn int64
	)
	err := row.Scan(&r.ID, &r.TenantID, &asn, &r.Name, &r.Category, &r.Service, &r.Note, &r.CreatedBy,
		&r.CreatedAt, &r.UpdatedAt, &r.DeletedAt, &r.Version)
	if err != nil {
		return nil, notFound(err)
	}
	r.ASN = uint32(asn) //nolint:gosec // CHECK 1..4294967295
	return &r, nil
}

func asnRuleErr(err error) error {
	code, constraint, ok := pgdb.ConstraintError(err)
	if ok && code == pgdb.SQLStateUniqueViolation && constraint == "ux_asn_rule__tenant_asn" {
		return app.ErrASNTaken
	}
	return err
}

// ListAsnRules implementa app.AsnRuleStore (reglas vivas del tenant).
func (s *Store) ListAsnRules(ctx context.Context, t pgdb.TenantID) ([]domain.AsnRule, error) {
	var out []domain.AsnRule
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+asnRuleCols+` FROM devices.asn_rule a
			WHERE a.tenant_id = $1 AND a.deleted_at IS NULL ORDER BY a.asn`, t.UUID())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanAsnRule(rows)
			if err != nil {
				return err
			}
			out = append(out, *r)
		}
		return rows.Err()
	})
	return out, err
}

// GetAsnRule implementa app.AsnRuleStore.
func (s *Store) GetAsnRule(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.AsnRule, error) {
	var out *domain.AsnRule
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = scanAsnRule(tx.QueryRow(ctx, `SELECT `+asnRuleCols+` FROM devices.asn_rule a
			WHERE a.tenant_id = $1 AND a.id = $2 AND a.deleted_at IS NULL`, t.UUID(), id))
		return err
	})
	return out, err
}

// CreateAsnRule implementa app.AsnRuleStore: alta + outbox en la misma transacción.
func (s *Store) CreateAsnRule(ctx context.Context, t pgdb.TenantID, r *domain.AsnRule, ev app.Events[domain.AsnRule]) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO devices.asn_rule (id, tenant_id, asn, name, category_slug, service_slug, note, created_by,
				created_at, updated_at, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9, 1)`,
			r.ID, t.UUID(), int64(r.ASN), r.Name, r.Category, r.Service, r.Note, r.CreatedBy, r.CreatedAt)
		if err != nil {
			return asnRuleErr(err)
		}
		return emit(ctx, tx, ev(r))
	})
}

// UpdateAsnRule implementa app.AsnRuleStore con control de versión.
func (s *Store) UpdateAsnRule(ctx context.Context, t pgdb.TenantID, r *domain.AsnRule, expect int, ev app.Events[domain.AsnRule]) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `UPDATE devices.asn_rule SET name = $4, category_slug = $5, service_slug = $6, note = $7,
				updated_at = $8, version = version + 1
			WHERE tenant_id = $1 AND id = $2 AND version = $3 AND deleted_at IS NULL RETURNING version`,
			t.UUID(), r.ID, expect, r.Name, r.Category, r.Service, r.Note, r.UpdatedAt).Scan(&r.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			return app.ErrVersionChanged
		}
		if err != nil {
			return asnRuleErr(err)
		}
		return emit(ctx, tx, ev(r))
	})
}

// DeleteAsnRule implementa app.AsnRuleStore: baja lógica (el ASN queda libre para otra regla).
func (s *Store) DeleteAsnRule(ctx context.Context, t pgdb.TenantID, id uuid.UUID, expect int, ev app.Events[domain.AsnRule]) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		r, err := scanAsnRule(tx.QueryRow(ctx, `UPDATE devices.asn_rule a SET deleted_at = now(), updated_at = now(), version = version + 1
			WHERE a.tenant_id = $1 AND a.id = $2 AND a.version = $3 AND a.deleted_at IS NULL RETURNING `+asnRuleCols, t.UUID(), id, expect))
		if errors.Is(err, domain.ErrNotFound) {
			return app.ErrVersionChanged
		}
		if err != nil {
			return err
		}
		return emit(ctx, tx, ev(r))
	})
}
