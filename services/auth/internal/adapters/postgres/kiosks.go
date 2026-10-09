package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

var _ app.KioskStore = (*Store)(nil)

const kioskCols = `id, tenant_id, name, status, playlist_id, dashboard_ids, allowed_cidrs, show_personal_data, show_personal_data_reason,
	critical_finding_banner, credential_hash, credential_family_id, expires_at, last_seen_at, last_ip, created_at, updated_at, version`

func scanKiosk(row pgx.Row) (*domain.Kiosk, error) {
	var k domain.Kiosk
	var cidrs []netip.Prefix
	err := row.Scan(&k.ID, &k.TenantID, &k.Name, &k.Status, &k.PlaylistID, &k.DashboardIDs, &cidrs, &k.ShowPersonalData,
		&k.ShowPersonalDataReason, &k.CriticalFindingBanner, &k.CredentialHash, &k.CredentialFamily, &k.ExpiresAt, &k.LastSeenAt,
		&k.LastIP, &k.CreatedAt, &k.UpdatedAt, &k.Version)
	if err != nil {
		return nil, notFound(err)
	}
	k.AllowedCIDRs = cidrs
	if k.DashboardIDs == nil {
		k.DashboardIDs = []uuid.UUID{}
	}
	return &k, nil
}

func cidrStrings(ps []netip.Prefix) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.String())
	}
	return out
}

func emitAll(ctx context.Context, tx pgx.Tx, evs []outbox.Event) error {
	for _, ev := range evs {
		if err := outbox.Insert(ctx, tx, "auth", ev); err != nil {
			return err
		}
	}
	return nil
}

func events(ev app.KioskEvents, k *domain.Kiosk) []outbox.Event {
	if ev == nil {
		return nil
	}
	return ev(k)
}

// ListKiosks implementa app.KioskStore.
func (s *Store) ListKiosks(ctx context.Context, t pgdb.TenantID, afterCreated *time.Time, afterID uuid.UUID, limit int) ([]domain.Kiosk, error) {
	var out []domain.Kiosk
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		args := []any{t.UUID(), limit}
		cond := ""
		if afterCreated != nil {
			cond = " AND (created_at, id) > ($3, $4)"
			args = append(args, *afterCreated, afterID)
		}
		rows, err := tx.Query(ctx, `SELECT `+kioskCols+` FROM auth.kiosk WHERE tenant_id = $1`+cond+` ORDER BY created_at, id LIMIT $2`, args...)
		if err != nil {
			return fmt.Errorf("auth: list kiosks: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			k, err := scanKiosk(rows)
			if err != nil {
				return err
			}
			out = append(out, *k)
		}
		return rows.Err()
	})
	return out, err
}

// GetKiosk implementa app.KioskStore.
func (s *Store) GetKiosk(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Kiosk, error) {
	var out *domain.Kiosk
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = scanKiosk(tx.QueryRow(ctx, `SELECT `+kioskCols+` FROM auth.kiosk WHERE tenant_id = $1 AND id = $2`, t.UUID(), id))
		return err
	})
	return out, err
}

// CreateKiosk implementa app.KioskStore.
func (s *Store) CreateKiosk(ctx context.Context, t pgdb.TenantID, k *domain.Kiosk, ev app.KioskEvents) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO auth.kiosk (id, tenant_id, name, status, playlist_id, dashboard_ids, allowed_cidrs,
			show_personal_data, show_personal_data_reason, critical_finding_banner, expires_at, created_at, updated_at, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7::cidr[], $8, $9, $10, $11, $12, $12, 1)`,
			k.ID, t.UUID(), k.Name, k.Status, k.PlaylistID, k.DashboardIDs, cidrStrings(k.AllowedCIDRs), k.ShowPersonalData,
			k.ShowPersonalDataReason, k.CriticalFindingBanner, k.ExpiresAt, k.CreatedAt)
		if err != nil {
			return fmt.Errorf("auth: create kiosk: %w", err)
		}
		return emitAll(ctx, tx, events(ev, k))
	})
}

func updateKiosk(ctx context.Context, tx pgx.Tx, k *domain.Kiosk, expect int) error {
	err := tx.QueryRow(ctx, `UPDATE auth.kiosk SET name = $3, status = $4, playlist_id = $5, dashboard_ids = $6, allowed_cidrs = $7::cidr[],
		show_personal_data = $8, show_personal_data_reason = $9, critical_finding_banner = $10, expires_at = $11,
		credential_hash = $12, credential_family_id = $13, last_seen_at = $14, last_ip = $15, updated_at = now(), version = version + 1
		WHERE tenant_id = $1 AND id = $2 AND version = $16 RETURNING version, updated_at`,
		k.TenantID, k.ID, k.Name, k.Status, k.PlaylistID, k.DashboardIDs, cidrStrings(k.AllowedCIDRs), k.ShowPersonalData,
		k.ShowPersonalDataReason, k.CriticalFindingBanner, k.ExpiresAt, k.CredentialHash, k.CredentialFamily, k.LastSeenAt,
		ipOrNil(k.LastIP), expect).Scan(&k.Version, &k.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ErrKioskVersion
	}
	if err != nil {
		return fmt.Errorf("auth: update kiosk: %w", err)
	}
	return nil
}

func ipOrNil(a *netip.Addr) any {
	if a == nil {
		return nil
	}
	return a.String()
}

// UpdateKiosk implementa app.KioskStore.
func (s *Store) UpdateKiosk(ctx context.Context, t pgdb.TenantID, k *domain.Kiosk, expect int, ev app.KioskEvents) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		if err := updateKiosk(ctx, tx, k, expect); err != nil {
			return err
		}
		if k.Status == domain.KioskRevoked {
			if _, err := tx.Exec(ctx, `UPDATE auth.kiosk_enrollment_code SET used_at = coalesce(used_at, now()) WHERE tenant_id = $1 AND kiosk_id = $2`,
				k.TenantID, k.ID); err != nil {
				return fmt.Errorf("auth: revoke codes: %w", err)
			}
		}
		return emitAll(ctx, tx, events(ev, k))
	})
}

// PutKioskCode implementa app.KioskStore.
func (s *Store) PutKioskCode(ctx context.Context, t pgdb.TenantID, kioskID, codeID uuid.UUID, hash []byte, expires time.Time) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE auth.kiosk_enrollment_code SET used_at = now() WHERE tenant_id = $1 AND kiosk_id = $2 AND used_at IS NULL`,
			t.UUID(), kioskID); err != nil {
			return fmt.Errorf("auth: kiosk codes: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO auth.kiosk_enrollment_code (id, tenant_id, kiosk_id, code_hash, expires_at) VALUES ($1, $2, $3, $4, $5)`,
			codeID, t.UUID(), kioskID, hash, expires); err != nil {
			return fmt.Errorf("auth: kiosk code: %w", err)
		}
		return nil
	})
}

// KioskByCode implementa app.KioskStore (rol de plataforma: canje público).
func (s *Store) KioskByCode(ctx context.Context, hash []byte) (*app.CodeRedemption, error) {
	var out *app.CodeRedemption
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var r app.CodeRedemption
		var tenant, kiosk uuid.UUID
		var used *time.Time
		var expires time.Time
		err := tx.QueryRow(ctx, `SELECT id, tenant_id, kiosk_id, used_at, expires_at, failures FROM auth.kiosk_enrollment_code WHERE code_hash = $1`,
			hash).Scan(&r.CodeID, &tenant, &kiosk, &used, &expires, &r.Failures)
		if err != nil {
			return notFound(err)
		}
		r.Used, r.Expired = used != nil, !time.Now().Before(expires)
		k, err := scanKiosk(tx.QueryRow(ctx, `SELECT `+kioskCols+` FROM auth.kiosk WHERE tenant_id = $1 AND id = $2`, tenant, kiosk))
		if err != nil {
			return err
		}
		r.Kiosk = k
		out = &r
		return nil
	})
	return out, err
}

// KioskCodeFailure implementa app.KioskStore.
func (s *Store) KioskCodeFailure(ctx context.Context, codeID uuid.UUID, maxFailures int) error {
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE auth.kiosk_enrollment_code SET failures = failures + 1,
			used_at = CASE WHEN failures + 1 >= $2 THEN coalesce(used_at, now()) ELSE used_at END WHERE id = $1`, codeID, maxFailures)
		if err != nil {
			return fmt.Errorf("auth: kiosk code failure: %w", err)
		}
		return nil
	})
}

// EnrollKiosk implementa app.KioskStore: consume el código (una sola vez) y
// fija la credencial en la misma transacción.
func (s *Store) EnrollKiosk(ctx context.Context, codeID uuid.UUID, k *domain.Kiosk, credHash []byte, ev app.KioskEvents) error {
	return s.db.TenantTx(ctx, pgdb.TenantID(k.TenantID), func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE auth.kiosk_enrollment_code SET used_at = now()
			WHERE id = $1 AND tenant_id = $2 AND used_at IS NULL AND expires_at > now()`, codeID, k.TenantID)
		if err != nil {
			return fmt.Errorf("auth: consume kiosk code: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return app.ErrKioskCodeUsed
		}
		// Un re-enrolamiento invalida la credencial anterior sin tratarla como reutilización.
		k.CredentialHash = credHash
		if err := updateKiosk(ctx, tx, k, k.Version); err != nil {
			return err
		}
		return emitAll(ctx, tx, events(ev, k))
	})
}

// KioskByCredential implementa app.KioskStore.
func (s *Store) KioskByCredential(ctx context.Context, hash []byte) (*domain.Kiosk, bool, error) {
	var out *domain.Kiosk
	rotated := false
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		k, err := scanKiosk(tx.QueryRow(ctx, `SELECT `+kioskCols+` FROM auth.kiosk WHERE credential_hash = $1`, hash))
		if err == nil {
			out = k
			return nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		k, err = scanKiosk(tx.QueryRow(ctx, `SELECT `+prefixed("k.")+` FROM auth.kiosk_credential_rotated r
			JOIN auth.kiosk k ON k.tenant_id = r.tenant_id AND k.id = r.kiosk_id WHERE r.credential_hash = $1`, hash))
		if err != nil {
			return err
		}
		out, rotated = k, true
		return nil
	})
	return out, rotated, err
}

func prefixed(p string) string {
	return p + `id, ` + p + `tenant_id, ` + p + `name, ` + p + `status, ` + p + `playlist_id, ` + p + `dashboard_ids, ` + p + `allowed_cidrs, ` +
		p + `show_personal_data, ` + p + `show_personal_data_reason, ` + p + `critical_finding_banner, ` + p + `credential_hash, ` +
		p + `credential_family_id, ` + p + `expires_at, ` + p + `last_seen_at, ` + p + `last_ip, ` + p + `created_at, ` + p + `updated_at, ` + p + `version`
}

// RotateKioskCredential implementa app.KioskStore (CAS sobre la credencial).
func (s *Store) RotateKioskCredential(ctx context.Context, k *domain.Kiosk, oldHash, newHash []byte, ip string, now time.Time) (bool, error) {
	ok := false
	err := s.db.TenantTx(ctx, pgdb.TenantID(k.TenantID), func(tx pgx.Tx) error {
		var lastIP any
		if a, err := netip.ParseAddr(ip); err == nil {
			lastIP = a.String()
		}
		tag, err := tx.Exec(ctx, `UPDATE auth.kiosk SET credential_hash = $3, last_seen_at = $4, last_ip = coalesce($5::inet, last_ip)
			WHERE tenant_id = $1 AND id = $2 AND credential_hash = $6`, k.TenantID, k.ID, newHash, now, lastIP, oldHash)
		if err != nil {
			return fmt.Errorf("auth: rotate kiosk credential: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return nil
		}
		if _, err := tx.Exec(ctx, `INSERT INTO auth.kiosk_credential_rotated (credential_hash, tenant_id, kiosk_id, family_id)
			VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, oldHash, k.TenantID, k.ID, k.CredentialFamily); err != nil {
			return fmt.Errorf("auth: rotate kiosk credential: %w", err)
		}
		ok = true
		return nil
	})
	return ok, err
}
