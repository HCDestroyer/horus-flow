package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

var (
	_ app.OnboardingStore = (*Store)(nil)
	_ app.CredentialStore = (*Store)(nil)
)

// ListAllRouters implementa app.OnboardingStore (rol de plataforma: la
// reconciliación de túneles de wireguard es multi-tenant).
func (s *Store) ListAllRouters(ctx context.Context) ([]domain.Router, error) {
	var out []domain.Router
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+routerCols+` FROM devices.router r WHERE r.deleted_at IS NULL ORDER BY r.created_at, r.id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			x, err := scanRouter(rows)
			if err != nil {
				return err
			}
			out = append(out, *x)
		}
		return rows.Err()
	})
	return out, err
}

// SetRouterTunnel implementa app.OnboardingStore.
func (s *Store) SetRouterTunnel(ctx context.Context, t pgdb.TenantID, id uuid.UUID, fn func(r *domain.Router) bool, ev app.Events[domain.Router]) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		r, err := scanRouter(tx.QueryRow(ctx, `SELECT `+routerCols+` FROM devices.router r
			WHERE r.tenant_id = $1 AND r.id = $2 AND r.deleted_at IS NULL FOR UPDATE`, t.UUID(), id))
		if err != nil {
			return err
		}
		if !fn(r) {
			return nil
		}
		err = tx.QueryRow(ctx, `UPDATE devices.router SET tunnel_address = $3::inet, mgmt_wireguard_peer_id = $4, onboarding_state = $5,
				updated_at = $6, management_address = $7::inet, connection_mode = $8, protection_level = $9,
				tunnel_remote_address = $10::inet, tunnel_remote_id = $11, sflow_enabled = $12, via_router_id = $13, via_address = $14::inet,
				version = version + 1
			WHERE tenant_id = $1 AND id = $2 RETURNING version`,
			t.UUID(), id, r.TunnelAddress, r.WireguardPeerID, r.OnboardingState, r.UpdatedAt, r.ManagementAddress, connectionMode(r),
			protectionLevel(r), r.TunnelRemoteAddress, r.TunnelRemoteID, r.SFlowEnabled, r.ViaRouterID, r.ViaAddress).Scan(&r.Version)
		if err != nil {
			return routerErr(err)
		}
		return emit(ctx, tx, ev(r))
	})
}

// PutCredentials implementa app.OnboardingStore.
func (s *Store) PutCredentials(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID, creds []domain.Credential) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		for _, c := range creds {
			if _, err := tx.Exec(ctx, `UPDATE devices.credential SET deleted_at = now(), version = version + 1, secret_ciphertext = NULL,
					dek_wrapped = NULL, secret_ref = 'shredded'
				WHERE tenant_id = $1 AND router_id = $2 AND kind = $3 AND deleted_at IS NULL`, t.UUID(), routerID, c.Kind); err != nil {
				return err
			}
			// tls_fingerprint_sha256: la huella fijada que el llamante decida
			// conservar (nil = se fija en el primer contacto, TOFU).
			at := c.UpdatedAt
			if at.IsZero() {
				at = time.Now().UTC()
			}
			if _, err := tx.Exec(ctx, `INSERT INTO devices.credential (id, tenant_id, router_id, kind, username, secret_ciphertext, dek_wrapped, kek_id,
					tls_fingerprint_sha256, tls_fingerprint_pending, tls_fingerprint_pending_at, tls_fingerprint_confirmed_at,
					tls_fingerprint_confirmed_by, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)`, c.ID, t.UUID(), routerID, c.Kind, c.Username,
				c.Ciphertext, c.DEKWrapped, c.KEKID, c.TLSFingerprintSHA256, c.TLSPending, c.TLSPendingAt, c.TLSConfirmedAt, c.TLSConfirmedBy,
				at); err != nil {
				if pgdb.IsCode(err, pgdb.SQLStateForeignKey) {
					return domain.ErrNotFound
				}
				return err
			}
		}
		return nil
	})
}

// GetCredential implementa app.OnboardingStore.
func (s *Store) GetCredential(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID, kind string) (*domain.Credential, error) {
	var c domain.Credential
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, router_id, kind, username, secret_ciphertext, dek_wrapped, coalesce(kek_id, ''),
				tls_fingerprint_sha256, tls_fingerprint_pending, tls_fingerprint_pending_at, tls_fingerprint_confirmed_at,
				tls_fingerprint_confirmed_by, last_used_at, last_result, updated_at, version
			FROM devices.credential WHERE tenant_id = $1 AND router_id = $2 AND kind = $3 AND deleted_at IS NULL`,
			t.UUID(), routerID, kind).Scan(&c.ID, &c.RouterID, &c.Kind, &c.Username, &c.Ciphertext, &c.DEKWrapped, &c.KEKID,
			&c.TLSFingerprintSHA256, &c.TLSPending, &c.TLSPendingAt, &c.TLSConfirmedAt, &c.TLSConfirmedBy, &c.LastUsedAt, &c.LastResult,
			&c.UpdatedAt, &c.Version)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ObserveTLSFingerprint guarda la huella observada del router: si coincide
// con la fijada limpia la pendiente; si no, la deja pendiente de
// confirmación (nunca toca la fijada). result, si no es vacío, actualiza
// last_result. Devuelve la credencial resultante.
func (s *Store) ObserveTLSFingerprint(ctx context.Context, t pgdb.TenantID, id uuid.UUID, observed, result string, at time.Time) (*domain.Credential, error) {
	var c domain.Credential
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `UPDATE devices.credential SET
				tls_fingerprint_pending = CASE WHEN upper(tls_fingerprint_sha256) = $3 THEN NULL ELSE $3 END,
				tls_fingerprint_pending_at = CASE WHEN upper(tls_fingerprint_sha256) = $3 THEN NULL
					WHEN tls_fingerprint_pending = $3 THEN tls_fingerprint_pending_at ELSE $4 END,
				last_result = coalesce(nullif($5, ''), last_result), last_used_at = CASE WHEN $5 = '' THEN last_used_at ELSE $4 END,
				updated_at = now(), version = version + 1
			WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
			RETURNING id, router_id, kind, username, tls_fingerprint_sha256, tls_fingerprint_pending, tls_fingerprint_pending_at,
				tls_fingerprint_confirmed_at, tls_fingerprint_confirmed_by, last_used_at, last_result, updated_at, version`,
			t.UUID(), id, observed, at, result).Scan(&c.ID, &c.RouterID, &c.Kind, &c.Username, &c.TLSFingerprintSHA256, &c.TLSPending,
			&c.TLSPendingAt, &c.TLSConfirmedAt, &c.TLSConfirmedBy, &c.LastUsedAt, &c.LastResult, &c.UpdatedAt, &c.Version)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ConfirmTLSFingerprint fija la huella pendiente si sigue siendo fp (CAS:
// una observación posterior distinta invalida la confirmación). Devuelve
// domain.ErrTLSPendingChanged si la pendiente ya no es fp.
func (s *Store) ConfirmTLSFingerprint(ctx context.Context, t pgdb.TenantID, id uuid.UUID, fp, by string, at time.Time) (*domain.Credential, error) {
	var c domain.Credential
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `UPDATE devices.credential SET tls_fingerprint_sha256 = $3, tls_fingerprint_pending = NULL,
				tls_fingerprint_pending_at = NULL, tls_fingerprint_confirmed_at = $5, tls_fingerprint_confirmed_by = $4,
				updated_at = now(), version = version + 1
			WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL AND tls_fingerprint_pending = $3
			RETURNING id, router_id, kind, username, tls_fingerprint_sha256, tls_fingerprint_pending, tls_fingerprint_pending_at,
				tls_fingerprint_confirmed_at, tls_fingerprint_confirmed_by, last_used_at, last_result, updated_at, version`,
			t.UUID(), id, fp, by, at).Scan(&c.ID, &c.RouterID, &c.Kind, &c.Username, &c.TLSFingerprintSHA256, &c.TLSPending,
			&c.TLSPendingAt, &c.TLSConfirmedAt, &c.TLSConfirmedBy, &c.LastUsedAt, &c.LastResult, &c.UpdatedAt, &c.Version)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrTLSPendingChanged
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// RecordCredentialUse guarda el resultado del último uso y, si se indica,
// la huella TLS fijada (TOFU).
func (s *Store) RecordCredentialUse(ctx context.Context, t pgdb.TenantID, id uuid.UUID, result string, fingerprint *string, at time.Time) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE devices.credential SET last_result = $3, last_used_at = $4,
				tls_fingerprint_sha256 = coalesce($5, tls_fingerprint_sha256), updated_at = now(), version = version + 1
			WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL`, t.UUID(), id, result, at, fingerprint)
		return err
	})
}
