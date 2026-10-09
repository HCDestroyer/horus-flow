package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

var _ app.ImportStore = (*Store)(nil)

// CreatePrefixes implementa app.ImportStore: todo o nada en una transacción.
func (s *Store) CreatePrefixes(ctx context.Context, t pgdb.TenantID, ps []*domain.ClientPrefix, ev app.Events[domain.ClientPrefix]) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		if err := checkParent(ctx, tx, t, &ps[0].SiteID); errors.Is(err, app.ErrRefNotFound) {
			return domain.ErrNotFound
		} else if err != nil {
			return err
		}
		for _, p := range ps {
			realm, err := realmFor(ctx, tx, t, p.SiteID, p.RealmKind)
			if err != nil {
				return err
			}
			p.RealmID = realm
			if _, err := tx.Exec(ctx, `INSERT INTO devices.client_prefix (id, tenant_id, site_id, realm_id, prefix, role, default_kind, assignment_mode,
					ipv6_client_len, source, confirmed, note, created_at, updated_at, version)
				VALUES ($1, $2, $3, $4, $5::cidr, $6, $7, $8, $9, $10, $11, $12, $13, $13, 1)`,
				p.ID, t.UUID(), p.SiteID, p.RealmID, p.Prefix.String(), p.Role, p.DefaultKind, p.AssignmentMode, p.IPv6ClientLen,
				p.Source, p.Confirmed, p.Note, p.CreatedAt); err != nil {
				return prefixErr(err)
			}
			if err := emit(ctx, tx, ev(p)); err != nil {
				return err
			}
		}
		return nil
	})
}

// Emit implementa app.ImportStore (eventos sueltos, p. ej. auditoría).
func (s *Store) Emit(ctx context.Context, evs []outbox.Event) error {
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error { return emit(ctx, tx, evs) })
}
