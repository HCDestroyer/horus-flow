package postgres

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

var _ app.APITokenStore = (*Store)(nil)

const apiTokenCols = `id, tenant_id, user_id, name, prefix, token_hash, permissions, template, created_at, expires_at,
	last_used_at, last_used_ip, revoked_at, revoked_by`

func scanAPIToken(row pgx.Row) (*domain.APIToken, error) {
	var t domain.APIToken
	var ip *netip.Addr
	if err := row.Scan(&t.ID, &t.TenantID, &t.UserID, &t.Name, &t.Prefix, &t.Hash, &t.Permissions, &t.Template, &t.CreatedAt,
		&t.ExpiresAt, &t.LastUsedAt, &ip, &t.RevokedAt, &t.RevokedBy); err != nil {
		return nil, notFound(err)
	}
	if ip != nil {
		s := ip.String()
		t.LastUsedIP = &s
	}
	if t.Permissions == nil {
		t.Permissions = []string{}
	}
	return &t, nil
}

// CreateAPIToken implementa app.APITokenStore.
func (s *Store) CreateAPIToken(ctx context.Context, t *domain.APIToken) error {
	return s.db.TenantTx(ctx, pgdb.TenantID(t.TenantID), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO auth.api_token (id, tenant_id, user_id, name, prefix, token_hash, permissions, template,
			created_at, expires_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			t.ID, t.TenantID, t.UserID, t.Name, t.Prefix, t.Hash, t.Permissions, t.Template, t.CreatedAt, t.ExpiresAt)
		if err != nil {
			return fmt.Errorf("auth: create api token: %w", err)
		}
		return nil
	})
}

// ListAPITokens implementa app.APITokenStore (más recientes primero).
func (s *Store) ListAPITokens(ctx context.Context, tenant uuid.UUID) ([]domain.APIToken, error) {
	var out []domain.APIToken
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+apiTokenCols+` FROM auth.api_token ORDER BY created_at DESC, id DESC LIMIT 500`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanAPIToken(rows)
			if err != nil {
				return err
			}
			out = append(out, *t)
		}
		return rows.Err()
	})
	return out, err
}

// RevokeAPIToken implementa app.APITokenStore.
func (s *Store) RevokeAPIToken(ctx context.Context, tenant, id, by uuid.UUID, now time.Time) (*domain.APIToken, error) {
	var t *domain.APIToken
	var byArg any
	if by != uuid.Nil {
		byArg = by
	}
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		var err error
		t, err = scanAPIToken(tx.QueryRow(ctx, `UPDATE auth.api_token SET revoked_at = $2, revoked_by = $3
			WHERE id = $1 AND revoked_at IS NULL RETURNING `+apiTokenCols, id, now, byArg))
		return err
	})
	return t, err
}

// APITokenByHash implementa app.APITokenStore (multi-tenant declarado: el
// borde aún no conoce el ISP del token).
func (s *Store) APITokenByHash(ctx context.Context, hash []byte) (*domain.APIToken, error) {
	var t *domain.APIToken
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var err error
		t, err = scanAPIToken(tx.QueryRow(ctx, `SELECT `+apiTokenCols+` FROM auth.api_token WHERE token_hash = $1`, hash))
		return err
	})
	return t, err
}

// TouchAPIToken implementa app.APITokenStore.
func (s *Store) TouchAPIToken(ctx context.Context, id uuid.UUID, now time.Time, ip string) error {
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE auth.api_token SET last_used_at = $2, last_used_ip = $3::inet WHERE id = $1`, id, now, nullIP(ip))
		return err
	})
}
