package postgres

import (
	"bytes"
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

var _ app.ConnectionLinkStore = (*Store)(nil)

const linkCols = `id, tenant_id, code_hash, created_by, created_at, expires_at, used_at, request_hash, response_ciphertext, response_dek`

func scanLink(row pgx.Row) (*domain.ConnectionLink, error) {
	var l domain.ConnectionLink
	if err := row.Scan(&l.ID, &l.TenantID, &l.CodeHash, &l.CreatedBy, &l.CreatedAt, &l.ExpiresAt, &l.UsedAt, &l.RequestHash,
		&l.ResponseCiphertext, &l.ResponseDEK); err != nil {
		return nil, notFound(err)
	}
	return &l, nil
}

// CreateConnectionLink implementa app.ConnectionLinkStore: anula los enlaces
// sin usar del ISP y guarda el nuevo (solo su hash).
func (s *Store) CreateConnectionLink(ctx context.Context, t pgdb.TenantID, l *domain.ConnectionLink) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE devices.argosisp_connection_link SET revoked_at = $1
			WHERE used_at IS NULL AND revoked_at IS NULL AND expires_at > $1`, l.CreatedAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO devices.argosisp_connection_link (id, tenant_id, code_hash, created_by, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)`, l.ID, t.UUID(), l.CodeHash, l.CreatedBy, l.CreatedAt, l.ExpiresAt)
		return err
	})
}

// ClaimConnectionLink implementa app.ConnectionLinkStore (multi-tenant
// declarado: el canje público aún no conoce el ISP). Con el enlace vigente
// sin usar lo marca usado con el hash de la petición (CAS bajo FOR UPDATE);
// usado con la misma petición y respuesta guardada, lo devuelve como
// repetición; en cualquier otro caso, domain.ErrNotFound (410).
func (s *Store) ClaimConnectionLink(ctx context.Context, hash, requestHash []byte, now time.Time, ip string) (*domain.ConnectionLink, bool, error) {
	var out *domain.ConnectionLink
	replay := false
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		l, err := scanLink(tx.QueryRow(ctx, `SELECT `+linkCols+` FROM devices.argosisp_connection_link
			WHERE code_hash = $1 AND revoked_at IS NULL AND expires_at > $2 FOR UPDATE`, hash, now))
		if err != nil {
			return err
		}
		if l.UsedAt != nil {
			if len(l.ResponseCiphertext) > 0 && bytes.Equal(l.RequestHash, requestHash) {
				out, replay = l, true
				return nil
			}
			return domain.ErrNotFound
		}
		if _, err := tx.Exec(ctx, `UPDATE devices.argosisp_connection_link SET used_at = $2, used_ip = $3::inet, request_hash = $4
			WHERE id = $1`, l.ID, now, nullIP(ip), requestHash); err != nil {
			return err
		}
		l.UsedAt, l.RequestHash = &now, requestHash
		out = l
		return nil
	})
	return out, replay, err
}

func nullIP(ip string) any {
	if ip == "" {
		return nil
	}
	return ip
}

// CompleteConnectionLink implementa app.ConnectionLinkStore.
func (s *Store) CompleteConnectionLink(ctx context.Context, t pgdb.TenantID, id uuid.UUID, ct, dek []byte, kekID string) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE devices.argosisp_connection_link SET response_ciphertext = $2, response_dek = $3, kek_id = $4
			WHERE id = $1`, id, ct, dek, kekID)
		return err
	})
}

// ReleaseConnectionLink implementa app.ConnectionLinkStore: un canje que
// falló por un error interno (sin respuesta guardada) deja el código usable
// otra vez mientras no caduque.
func (s *Store) ReleaseConnectionLink(ctx context.Context, t pgdb.TenantID, id uuid.UUID) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE devices.argosisp_connection_link SET used_at = NULL, used_ip = NULL, request_hash = NULL
			WHERE id = $1 AND response_ciphertext IS NULL`, id)
		return err
	})
}

// PurgeConnectionLinks implementa app.ConnectionLinkStore (multi-tenant
// declarado: limpieza).
func (s *Store) PurgeConnectionLinks(ctx context.Context, before time.Time) (int, error) {
	var n int
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM devices.argosisp_connection_link WHERE expires_at < $1`, before)
		n = int(tag.RowsAffected())
		return err
	})
	return n, err
}
