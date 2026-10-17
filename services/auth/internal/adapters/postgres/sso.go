package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

var _ app.SSOStore = (*Store)(nil)

const ssoTicketCols = `id, tenant_id, system, connector_id, api_token_id, ticket_hash, external_user_id, external_email, external_name,
	target_type, target_id, created_at, expires_at, redeemed_at, link_consumed_at`

func scanSSOTicket(row pgx.Row) (*domain.SSOTicket, error) {
	var t domain.SSOTicket
	if err := row.Scan(&t.ID, &t.TenantID, &t.System, &t.ConnectorID, &t.APITokenID, &t.Hash, &t.ExternalUserID, &t.ExternalEmail,
		&t.ExternalName, &t.TargetType, &t.TargetID, &t.CreatedAt, &t.ExpiresAt, &t.RedeemedAt, &t.LinkConsumedAt); err != nil {
		return nil, notFound(err)
	}
	return &t, nil
}

// CreateSSOTicket implementa app.SSOStore.
func (s *Store) CreateSSOTicket(ctx context.Context, t *domain.SSOTicket) error {
	return s.db.TenantTx(ctx, pgdb.TenantID(t.TenantID), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO auth.sso_ticket (id, tenant_id, system, connector_id, api_token_id, ticket_hash, external_user_id,
			external_email, external_name, target_type, target_id, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			t.ID, t.TenantID, t.System, t.ConnectorID, t.APITokenID, t.Hash, t.ExternalUserID, t.ExternalEmail, t.ExternalName,
			t.TargetType, t.TargetID, t.CreatedAt, t.ExpiresAt)
		return err
	})
}

// RedeemSSOTicket implementa app.SSOStore (multi-tenant declarado: el canje
// público aún no conoce el ISP; CAS de un solo uso).
func (s *Store) RedeemSSOTicket(ctx context.Context, hash []byte, now time.Time, ip string) (*domain.SSOTicket, error) {
	var t *domain.SSOTicket
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var err error
		t, err = scanSSOTicket(tx.QueryRow(ctx, `UPDATE auth.sso_ticket SET redeemed_at = $2, redeemed_ip = $3::inet
			WHERE ticket_hash = $1 AND redeemed_at IS NULL AND expires_at > $2 RETURNING `+ssoTicketCols, hash, now, nullIP(ip)))
		return err
	})
	return t, err
}

// SSOTicketByID implementa app.SSOStore (multi-tenant declarado: el
// link_token lleva el id del ticket, no el ISP).
func (s *Store) SSOTicketByID(ctx context.Context, id uuid.UUID) (*domain.SSOTicket, error) {
	var t *domain.SSOTicket
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var err error
		t, err = scanSSOTicket(tx.QueryRow(ctx, `SELECT `+ssoTicketCols+` FROM auth.sso_ticket WHERE id = $1`, id))
		return err
	})
	return t, err
}

// ConsumeSSOLinkToken implementa app.SSOStore.
func (s *Store) ConsumeSSOLinkToken(ctx context.Context, tenant, ticketID uuid.UUID, now time.Time) (bool, error) {
	var ok bool
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE auth.sso_ticket SET link_consumed_at = $2
			WHERE id = $1 AND redeemed_at IS NOT NULL AND link_consumed_at IS NULL`, ticketID, now)
		ok = tag.RowsAffected() == 1
		return err
	})
	return ok, err
}

// PurgeSSOTickets implementa app.SSOStore (multi-tenant declarado: limpieza).
func (s *Store) PurgeSSOTickets(ctx context.Context, before time.Time) (int, error) {
	var n int
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM auth.sso_ticket WHERE expires_at < $1`, before)
		n = int(tag.RowsAffected())
		return err
	})
	return n, err
}

const ssoLinkSelect = `SELECT l.id, l.tenant_id, t.name, l.system, l.connector_id, l.external_user_id, l.external_email, l.external_name,
	l.user_id, u.email::text, l.created_at, l.last_used_at
	FROM auth.sso_link l JOIN auth.tenant t ON t.id = l.tenant_id JOIN auth."user" u ON u.id = l.user_id`

func scanSSOLink(row pgx.Row) (*domain.SSOLink, error) {
	var l domain.SSOLink
	if err := row.Scan(&l.ID, &l.TenantID, &l.TenantName, &l.System, &l.ConnectorID, &l.ExternalUserID, &l.ExternalEmail, &l.ExternalName,
		&l.UserID, &l.UserEmail, &l.CreatedAt, &l.LastUsedAt); err != nil {
		return nil, notFound(err)
	}
	return &l, nil
}

func collectSSOLinks(rows pgx.Rows) ([]domain.SSOLink, error) {
	defer rows.Close()
	out := []domain.SSOLink{}
	for rows.Next() {
		l, err := scanSSOLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// SSOLinkByExternal implementa app.SSOStore.
func (s *Store) SSOLinkByExternal(ctx context.Context, tenant, connector uuid.UUID, externalUserID string) (*domain.SSOLink, error) {
	var l *domain.SSOLink
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		var err error
		l, err = scanSSOLink(tx.QueryRow(ctx, ssoLinkSelect+` WHERE l.connector_id = $1 AND l.external_user_id = $2`, connector, externalUserID))
		return err
	})
	return l, err
}

// CreateSSOLink implementa app.SSOStore.
func (s *Store) CreateSSOLink(ctx context.Context, l *domain.SSOLink) error {
	err := s.db.TenantTx(ctx, pgdb.TenantID(l.TenantID), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO auth.sso_link (id, tenant_id, system, connector_id, external_user_id, external_email, external_name,
			user_id, created_at, last_used_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			l.ID, l.TenantID, l.System, l.ConnectorID, l.ExternalUserID, l.ExternalEmail, l.ExternalName, l.UserID, l.CreatedAt, l.LastUsedAt)
		return err
	})
	if pgdb.IsCode(err, pgdb.SQLStateUniqueViolation) {
		return app.ErrSSOLinkTaken
	}
	return err
}

// TouchSSOLink implementa app.SSOStore.
func (s *Store) TouchSSOLink(ctx context.Context, tenant, id uuid.UUID, now time.Time) error {
	return s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE auth.sso_link SET last_used_at = $2 WHERE id = $1`, id, now)
		return err
	})
}

// ListSSOLinksByUser implementa app.SSOStore (multi-tenant declarado: las
// vinculaciones del propio usuario en todos sus ISP).
func (s *Store) ListSSOLinksByUser(ctx context.Context, userID uuid.UUID) ([]domain.SSOLink, error) {
	var out []domain.SSOLink
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, ssoLinkSelect+` WHERE l.user_id = $1 ORDER BY l.created_at DESC, l.id DESC LIMIT 200`, userID)
		if err != nil {
			return err
		}
		out, err = collectSSOLinks(rows)
		return err
	})
	return out, err
}

// ListSSOLinks implementa app.SSOStore.
func (s *Store) ListSSOLinks(ctx context.Context, tenant uuid.UUID) ([]domain.SSOLink, error) {
	var out []domain.SSOLink
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, ssoLinkSelect+` ORDER BY l.created_at DESC, l.id DESC LIMIT 1000`)
		if err != nil {
			return err
		}
		out, err = collectSSOLinks(rows)
		return err
	})
	return out, err
}

// DeleteSSOLink implementa app.SSOStore. Con tenant = uuid.Nil (el usuario
// quita la suya en cualquiera de sus ISP) exige userID y usa auth_platform.
func (s *Store) DeleteSSOLink(ctx context.Context, tenant, id uuid.UUID, userID *uuid.UUID) (*domain.SSOLink, error) {
	var l *domain.SSOLink
	run := func(tx pgx.Tx) error {
		var err error
		if userID != nil {
			l, err = scanSSOLink(tx.QueryRow(ctx, ssoLinkSelect+` WHERE l.id = $1 AND l.user_id = $2`, id, *userID))
		} else {
			l, err = scanSSOLink(tx.QueryRow(ctx, ssoLinkSelect+` WHERE l.id = $1`, id))
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM auth.sso_link WHERE id = $1`, id)
		return err
	}
	var err error
	switch {
	case tenant != uuid.Nil:
		err = s.db.TenantTx(ctx, pgdb.TenantID(tenant), run)
	case userID != nil:
		err = s.db.PlatformTx(ctx, run)
	default:
		return nil, errors.New("auth: delete sso link without tenant nor user")
	}
	return l, err
}

// DeleteSSOLinks implementa app.SSOStore.
func (s *Store) DeleteSSOLinks(ctx context.Context, tenant uuid.UUID) (int, error) {
	var n int
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM auth.sso_link`)
		n = int(tag.RowsAffected())
		return err
	})
	return n, err
}
