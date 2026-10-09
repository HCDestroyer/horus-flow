// Package postgres implementa app.Store sobre el esquema `auth`. Las tablas
// de identidad global (user, session, refresh_token, tenant, totp) se usan
// con el rol auth_app; las tablas con tenant_id se tocan dentro del tenant
// (SET LOCAL horus.tenant_id, RLS) salvo los métodos multi-tenant
// declarados, que usan auth_platform: Memberships (las membresías de una
// persona en todos sus ISP), SyncCatalog (roles de sistema sin tenant) e
// InsertAudit (cadena de hashes por tenant y de plataforma).
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// Store es el repositorio.
type Store struct{ db *pgdb.DB }

// New crea el repositorio.
func New(db *pgdb.DB) *Store { return &Store{db: db} }

var _ app.Store = (*Store)(nil)

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

const userCols = `id, email::text, display_name, coalesce(password_hash, ''), status, is_platform_admin, mfa_enforced,
	must_change_password, failed_logins, locked_until, locale, timezone, default_tenant_id, version`

func scanUser(row pgx.Row) (*domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.PasswordHash, &u.Status, &u.IsPlatformAdmin, &u.MFAEnforced,
		&u.MustChangePassword, &u.FailedLogins, &u.LockedUntil, &u.Locale, &u.Timezone, &u.DefaultTenantID, &u.Version)
	if err != nil {
		return nil, notFound(err)
	}
	return &u, nil
}

// UserByEmail implementa app.Store.
func (s *Store) UserByEmail(ctx context.Context, email string) (*domain.User, error) {
	var u *domain.User
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		var err error
		u, err = scanUser(tx.QueryRow(ctx, `SELECT `+userCols+` FROM auth."user" WHERE email = $1 AND deleted_at IS NULL`, email))
		return err
	})
	return u, err
}

// UserByID implementa app.Store.
func (s *Store) UserByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	var u *domain.User
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		var err error
		u, err = scanUser(tx.QueryRow(ctx, `SELECT `+userCols+` FROM auth."user" WHERE id = $1 AND deleted_at IS NULL`, id))
		return err
	})
	return u, err
}

func (s *Store) exec(ctx context.Context, sql string, args ...any) error {
	return s.db.AppTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, sql, args...)
		return err
	})
}

// SetLoginFailure implementa app.Store.
func (s *Store) SetLoginFailure(ctx context.Context, id uuid.UUID, failed int, lockedUntil *time.Time) error {
	return s.exec(ctx, `UPDATE auth."user" SET failed_logins = $2, locked_until = $3, updated_at = now() WHERE id = $1`, id, failed, lockedUntil)
}

// SetLoginSuccess implementa app.Store.
func (s *Store) SetLoginSuccess(ctx context.Context, id uuid.UUID, now time.Time, rehash string) error {
	return s.exec(ctx, `UPDATE auth."user" SET failed_logins = 0, locked_until = NULL, last_login_at = $2,
		password_hash = coalesce(nullif($3, ''), password_hash) WHERE id = $1`, id, now, rehash)
}

// ChangePassword implementa app.Store: nueva contraseña, fin de
// must_change_password y revocación de las demás sesiones.
func (s *Store) ChangePassword(ctx context.Context, id uuid.UUID, hash string, now time.Time, keep uuid.UUID) error {
	return s.db.AppTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE auth."user" SET password_hash = $2, password_changed_at = $3, must_change_password = false,
			failed_logins = 0, locked_until = NULL, updated_at = $3, version = version + 1 WHERE id = $1`, id, hash, now); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `UPDATE auth.session SET revoked_at = $3, revoked_reason = 'password_changed'
			WHERE user_id = $1 AND id <> $2 AND revoked_at IS NULL RETURNING id`, id, keep, now)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, sid := range ids {
			ev := app.OutboxEvent{ID: uuid.Must(uuid.NewV7()), Type: "horus.auth.session.revoked", AggregateType: "session",
				AggregateID: sid, AggregateVersion: 1, OccurredAt: now, Actor: app.Actor{Type: "user", ID: id.String()},
				Data: map[string]any{"session_id": sid.String(), "user_id": id.String(), "reason": "password_changed"}}
			if err := insertOutbox(ctx, tx, ev); err != nil {
				return err
			}
		}
		return nil
	})
}

func insertRefresh(ctx context.Context, tx pgx.Tx, rt *domain.RefreshToken) error {
	_, err := tx.Exec(ctx, `INSERT INTO auth.refresh_token (id, session_id, token_hash, issued_at, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		rt.ID, rt.SessionID, rt.Hash, rt.IssuedAt, rt.ExpiresAt)
	return err
}

func nullIP(ip string) any {
	if ip == "" {
		return nil
	}
	return ip
}

// CreateSession implementa app.Store.
func (s *Store) CreateSession(ctx context.Context, se *domain.Session, rt *domain.RefreshToken) error {
	return s.db.AppTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO auth.session (id, user_id, created_at, last_seen_at, expires_at, ip, user_agent, amr, mfa_verified_at)
			VALUES ($1, $2, $3, $3, $4, $5::inet, nullif($6, ''), $7, $8)`,
			se.ID, se.UserID, se.CreatedAt, se.ExpiresAt, nullIP(se.IP), se.UserAgent, se.AMR, se.MFAVerifiedAt); err != nil {
			return err
		}
		if rt != nil {
			return insertRefresh(ctx, tx, rt)
		}
		return nil
	})
}

// Session implementa app.Store.
func (s *Store) Session(ctx context.Context, id uuid.UUID) (*domain.Session, error) {
	var se domain.Session
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, user_id, created_at, expires_at, revoked_at, coalesce(revoked_reason, ''), amr, mfa_verified_at
			FROM auth.session WHERE id = $1`, id).
			Scan(&se.ID, &se.UserID, &se.CreatedAt, &se.ExpiresAt, &se.RevokedAt, &se.RevokedReason, &se.AMR, &se.MFAVerifiedAt)
	})
	if err != nil {
		return nil, notFound(err)
	}
	return &se, nil
}

// SetSessionMFA implementa app.Store (un solo uso: solo si aún no se verificó).
func (s *Store) SetSessionMFA(ctx context.Context, sid uuid.UUID, now time.Time, amr []string, rt *domain.RefreshToken) (bool, error) {
	ok := false
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE auth.session SET mfa_verified_at = $2, amr = $3, last_seen_at = $2
			WHERE id = $1 AND mfa_verified_at IS NULL AND revoked_at IS NULL AND expires_at > $2`, sid, now, amr)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		ok = true
		if rt != nil {
			return insertRefresh(ctx, tx, rt)
		}
		return nil
	})
	return ok, err
}

// RevokeSession implementa app.Store.
func (s *Store) RevokeSession(ctx context.Context, sid uuid.UUID, reason string, now time.Time, ev *app.OutboxEvent) error {
	return s.db.AppTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE auth.session SET revoked_at = $2, revoked_reason = $3 WHERE id = $1 AND revoked_at IS NULL`, sid, now, reason)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 || ev == nil {
			return nil
		}
		return insertOutbox(ctx, tx, *ev)
	})
}

// RefreshByHash implementa app.Store.
func (s *Store) RefreshByHash(ctx context.Context, hash []byte) (*domain.RefreshToken, error) {
	var rt domain.RefreshToken
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, session_id, token_hash, issued_at, expires_at, used_at FROM auth.refresh_token WHERE token_hash = $1`, hash).
			Scan(&rt.ID, &rt.SessionID, &rt.Hash, &rt.IssuedAt, &rt.ExpiresAt, &rt.UsedAt)
	})
	if err != nil {
		return nil, notFound(err)
	}
	return &rt, nil
}

// RotateRefresh implementa app.Store: marca el refresh como usado (solo si
// no lo estaba) e inserta el siguiente en la misma transacción.
func (s *Store) RotateRefresh(ctx context.Context, oldID uuid.UUID, next *domain.RefreshToken, now time.Time) (bool, error) {
	ok := false
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE auth.refresh_token SET used_at = $2, replaced_by = $3 WHERE id = $1 AND used_at IS NULL`, oldID, now, next.ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		ok = true
		if _, err := tx.Exec(ctx, `UPDATE auth.session SET last_seen_at = $2 WHERE id = $1`, next.SessionID, now); err != nil {
			return err
		}
		return insertRefresh(ctx, tx, next)
	})
	return ok, err
}

// SessionStatus implementa app.Store.
func (s *Store) SessionStatus(ctx context.Context, sid, userID, tenantID uuid.UUID) (app.SessionStatus, error) {
	var st app.SessionStatus
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT s.revoked_at IS NULL AND s.expires_at > now(), coalesce(s.revoked_reason, ''),
				u.status = 'active' AND u.deleted_at IS NULL
			FROM auth.session s JOIN auth."user" u ON u.id = s.user_id WHERE s.id = $1 AND s.user_id = $2`, sid, userID).
			Scan(&st.SessionActive, &st.RevokedReason, &st.UserActive)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil || tenantID == uuid.Nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT status = 'active' AND deleted_at IS NULL FROM auth.tenant WHERE id = $1`, tenantID).Scan(&st.TenantActive)
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return st, err
	}
	if tenantID == uuid.Nil {
		return st, nil
	}
	err = s.db.TenantTx(ctx, pgdb.TenantID(tenantID), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM auth.tenant_membership WHERE user_id = $1 AND status = 'active'
			AND (expires_at IS NULL OR expires_at > now()))`, userID).Scan(&st.MembershipActive)
	})
	return st, err
}

// PlatformRoles implementa app.Store.
func (s *Store) PlatformRoles(ctx context.Context, userID uuid.UUID) ([]string, error) {
	var out []string
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT role_key FROM auth.platform_role_assignment WHERE user_id = $1 ORDER BY role_key`, userID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	return out, err
}

// Memberships implementa app.Store (multi-tenant declarado: auth_platform).
func (s *Store) Memberships(ctx context.Context, userID uuid.UUID) ([]domain.Membership, error) {
	var out []domain.Membership
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT m.id, m.status, t.id, t.slug, t.name, t.status,
			       r.id, r.key, r.requires_mfa, a.scope_type, a.scope_id,
			       coalesce((SELECT array_agg(rp.permission_key ORDER BY rp.permission_key) FROM auth.role_permission rp WHERE rp.role_id = r.id), '{}')
			FROM auth.tenant_membership m
			JOIN auth.tenant t ON t.id = m.tenant_id AND t.deleted_at IS NULL
			LEFT JOIN auth.role_assignment a ON a.tenant_id = m.tenant_id AND a.membership_id = m.id
			LEFT JOIN auth.role r ON r.id = a.role_id
			WHERE m.user_id = $1 AND m.status = 'active' AND (m.expires_at IS NULL OR m.expires_at > now())
			ORDER BY t.name, t.id, r.key`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		idx := map[uuid.UUID]int{}
		for rows.Next() {
			var (
				m        domain.Membership
				roleID   *uuid.UUID
				roleKey  *string
				reqMFA   *bool
				scopeTyp *string
				scopeID  *uuid.UUID
				perms    []string
			)
			if err := rows.Scan(&m.ID, &m.Status, &m.Tenant.ID, &m.Tenant.Slug, &m.Tenant.Name, &m.Tenant.Status,
				&roleID, &roleKey, &reqMFA, &scopeTyp, &scopeID, &perms); err != nil {
				return err
			}
			i, ok := idx[m.ID]
			if !ok {
				out = append(out, m)
				i = len(out) - 1
				idx[m.ID] = i
			}
			if roleID != nil {
				out[i].Assignments = append(out[i].Assignments, domain.Assignment{
					RoleID: *roleID, RoleKey: *roleKey, RequiresMFA: *reqMFA, ScopeType: *scopeTyp, ScopeID: scopeID, Permissions: perms,
				})
			}
		}
		return rows.Err()
	})
	return out, err
}

// TOTP implementa app.Store.
func (s *Store) TOTP(ctx context.Context, userID uuid.UUID) (*domain.TOTP, error) {
	var t domain.TOTP
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT user_id, secret_ciphertext, dek_wrapped, kek_id, confirmed_at, coalesce(last_used_step, 0)
			FROM auth.totp_credential WHERE user_id = $1`, userID).
			Scan(&t.UserID, &t.Ciphertext, &t.DEKWrapped, &t.KEKID, &t.ConfirmedAt, &t.LastUsedStep)
	})
	if err != nil {
		return nil, notFound(err)
	}
	return &t, nil
}

// PutPendingTOTP implementa app.Store (sustituye un alta no confirmada).
func (s *Store) PutPendingTOTP(ctx context.Context, t *domain.TOTP) error {
	return s.exec(ctx, `INSERT INTO auth.totp_credential (user_id, secret_ciphertext, dek_wrapped, kek_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO UPDATE SET secret_ciphertext = EXCLUDED.secret_ciphertext, dek_wrapped = EXCLUDED.dek_wrapped,
			kek_id = EXCLUDED.kek_id, last_used_step = NULL, updated_at = now()
		WHERE auth.totp_credential.confirmed_at IS NULL`, t.UserID, t.Ciphertext, t.DEKWrapped, t.KEKID)
}

// ConfirmTOTP implementa app.Store.
func (s *Store) ConfirmTOTP(ctx context.Context, userID uuid.UUID, step int64, hashes [][]byte, now time.Time, sid uuid.UUID) error {
	return s.db.AppTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE auth.totp_credential SET confirmed_at = $2, last_used_step = $3, updated_at = $2
			WHERE user_id = $1 AND confirmed_at IS NULL`, userID, now, step)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errors.New("totp already confirmed")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM auth.recovery_code WHERE user_id = $1`, userID); err != nil {
			return err
		}
		for _, h := range hashes {
			if _, err := tx.Exec(ctx, `INSERT INTO auth.recovery_code (id, user_id, code_hash) VALUES ($1, $2, $3)`,
				uuid.Must(uuid.NewV7()), userID, h); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE auth.session SET mfa_verified_at = $2,
			amr = (SELECT array_agg(DISTINCT x ORDER BY x) FROM unnest(amr || ARRAY['otp']) x) WHERE id = $1`, sid, now)
		return err
	})
}

// AdvanceTOTPStep implementa app.Store (impide reutilizar un código).
func (s *Store) AdvanceTOTPStep(ctx context.Context, userID uuid.UUID, step int64) (bool, error) {
	ok := false
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE auth.totp_credential SET last_used_step = $2, updated_at = now()
			WHERE user_id = $1 AND coalesce(last_used_step, 0) < $2`, userID, step)
		ok = err == nil && tag.RowsAffected() == 1
		return err
	})
	return ok, err
}

// UseRecoveryCode implementa app.Store.
func (s *Store) UseRecoveryCode(ctx context.Context, userID uuid.UUID, hash []byte, now time.Time) (bool, error) {
	ok := false
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE auth.recovery_code SET used_at = $3 WHERE id = (
			SELECT id FROM auth.recovery_code WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL LIMIT 1)`, userID, hash, now)
		ok = err == nil && tag.RowsAffected() == 1
		return err
	})
	return ok, err
}

const tenantCols = `t.id, t.slug, t.name, t.status, t.country, t.timezone, t.quotas, t.settings, t.support_access_policy,
	t.created_by, t.created_at, t.updated_at, t.version`

func scanTenant(row pgx.Row, withMembers bool) (*domain.Tenant, error) {
	var t domain.Tenant
	dest := []any{&t.ID, &t.Slug, &t.Name, &t.Status, &t.Country, &t.Timezone, &t.Quotas, &t.Settings, &t.SupportAccessPolicy,
		&t.CreatedBy, &t.CreatedAt, &t.UpdatedAt, &t.Version}
	if withMembers {
		dest = append(dest, &t.Members)
	}
	if err := row.Scan(dest...); err != nil {
		return nil, notFound(err)
	}
	return &t, nil
}

// membersCount necesita ver membresías de cualquier tenant: se calcula con
// auth_platform en las vistas de plataforma.
const membersCount = `(SELECT count(*) FROM auth.tenant_membership m WHERE m.tenant_id = t.id AND m.status <> 'revoked')`

func tenantWhere(f app.TenantFilter) (string, []any) {
	conds := []string{"t.deleted_at IS NULL"}
	var args []any
	add := func(c string, v any) {
		args = append(args, v)
		conds = append(conds, strings.ReplaceAll(c, "?", fmt.Sprintf("$%d", len(args))))
	}
	if f.Q != "" {
		args = append(args, f.Q)
		n := len(args)
		conds = append(conds, fmt.Sprintf("(t.name ILIKE '%%' || $%d || '%%' OR t.slug ILIKE $%d || '%%')", n, n))
	}
	if f.Status != "" {
		add("t.status = ?", f.Status)
	}
	if f.AfterCreated != nil {
		args = append(args, *f.AfterCreated, f.AfterID)
		conds = append(conds, fmt.Sprintf("(t.created_at, t.id) > ($%d, $%d)", len(args)-1, len(args)))
	}
	return strings.Join(conds, " AND "), args
}

// ListTenants implementa app.Store.
func (s *Store) ListTenants(ctx context.Context, f app.TenantFilter) ([]domain.Tenant, error) {
	where, args := tenantWhere(f)
	args = append(args, f.Limit)
	var out []domain.Tenant
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s, %s FROM auth.tenant t WHERE %s ORDER BY t.created_at, t.id LIMIT $%d`,
			tenantCols, membersCount, where, len(args)), args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanTenant(rows, true)
			if err != nil {
				return err
			}
			out = append(out, *t)
		}
		return rows.Err()
	})
	return out, err
}

// CountTenants implementa app.Store.
func (s *Store) CountTenants(ctx context.Context, f app.TenantFilter) (int, error) {
	where, args := tenantWhere(f)
	var n int
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM auth.tenant t WHERE `+where, args...).Scan(&n)
	})
	return n, err
}

// Tenant implementa app.Store.
func (s *Store) Tenant(ctx context.Context, id uuid.UUID) (*domain.Tenant, error) {
	var t *domain.Tenant
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var err error
		t, err = scanTenant(tx.QueryRow(ctx, `SELECT `+tenantCols+`, `+membersCount+` FROM auth.tenant t WHERE t.id = $1 AND t.deleted_at IS NULL`, id), true)
		return err
	})
	return t, err
}

// CreateTenant implementa app.Store: todo dentro del tenant nuevo (RLS),
// con el usuario administrador creado o reutilizado y los eventos en el
// outbox en la misma transacción.
func (s *Store) CreateTenant(ctx context.Context, nt app.NewTenant) error {
	t := nt.Tenant
	err := s.db.TenantTx(ctx, pgdb.TenantID(t.ID), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO auth.tenant (id, slug, name, status, country, timezone, quotas, settings,
				support_access_policy, created_by, created_at, updated_at, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11, 1)`,
			t.ID, t.Slug, t.Name, t.Status, t.Country, t.Timezone, t.Quotas, t.Settings, t.SupportAccessPolicy, t.CreatedBy, t.CreatedAt); err != nil {
			if pgdb.IsCode(err, pgdb.SQLStateUniqueViolation) {
				return app.ErrSlugTaken
			}
			return err
		}
		var userID uuid.UUID
		var userStatus string
		err := tx.QueryRow(ctx, `SELECT id, status FROM auth."user" WHERE email = $1 AND deleted_at IS NULL`, nt.AdminEmail).Scan(&userID, &userStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			userID, userStatus = uuid.Must(uuid.NewV7()), domain.UserPending
			local, _, _ := strings.Cut(nt.AdminEmail, "@")
			if _, err := tx.Exec(ctx, `INSERT INTO auth."user" (id, email, display_name, status) VALUES ($1, $2, $3, 'pending')
				ON CONFLICT DO NOTHING`, userID, nt.AdminEmail, local); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		mStatus := domain.MembershipInvited
		if userStatus == domain.UserActive {
			mStatus = domain.MembershipActive
		}
		mID := uuid.Must(uuid.NewV7())
		granter := uuid.Nil
		if t.CreatedBy != nil {
			granter = *t.CreatedBy
		}
		if _, err := tx.Exec(ctx, `INSERT INTO auth.tenant_membership (id, tenant_id, user_id, status, granted_by) VALUES ($1, $2, $3, $4, $5)`,
			mID, t.ID, userID, mStatus, granter); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO auth.role_assignment (id, tenant_id, membership_id, role_id, scope_type) VALUES ($1, $2, $3, $4, 'tenant')`,
			uuid.Must(uuid.NewV7()), t.ID, mID, nt.AdminRole); err != nil {
			return err
		}
		if nt.Events != nil {
			for _, ev := range nt.Events(t, mID, userID, mStatus) {
				if err := insertOutbox(ctx, tx, ev); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return err
}

// UpdateTenant implementa app.Store (bloqueo optimista por version).
func (s *Store) UpdateTenant(ctx context.Context, t *domain.Tenant, expect int, ev func(domain.Tenant) app.OutboxEvent) (bool, error) {
	ok := false
	err := s.db.TenantTx(ctx, pgdb.TenantID(t.ID), func(tx pgx.Tx) error {
		var v int
		err := tx.QueryRow(ctx, `UPDATE auth.tenant SET name = $3, country = $4, timezone = $5, quotas = $6, settings = $7,
				support_access_policy = $8, updated_at = $9, version = version + 1
			WHERE id = $1 AND version = $2 AND deleted_at IS NULL RETURNING version`,
			t.ID, expect, t.Name, t.Country, t.Timezone, t.Quotas, t.Settings, t.SupportAccessPolicy, t.UpdatedAt).Scan(&v)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		ok = true
		t.Version = v
		if ev != nil {
			return insertOutbox(ctx, tx, ev(*t))
		}
		return nil
	})
	return ok, err
}

// SyncCatalog implementa app.Store: permisos y roles de sistema de C7
// (idempotente; los permisos que desaparecen se marcan deprecated_at).
func (s *Store) SyncCatalog(ctx context.Context, c *domain.Catalog) error {
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		keys := make([]string, 0, len(c.Permissions))
		for _, p := range c.Permissions {
			keys = append(keys, p.Key)
			if _, err := tx.Exec(ctx, `INSERT INTO auth.permission (key, family, description, pii, audited, reauth) VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT (key) DO UPDATE SET family = EXCLUDED.family, description = EXCLUDED.description, pii = EXCLUDED.pii,
					audited = EXCLUDED.audited, reauth = EXCLUDED.reauth, deprecated_at = NULL`,
				p.Key, p.Family, p.Description, p.PII, p.Audited, p.Reauth); err != nil {
				return fmt.Errorf("permission %s: %w", p.Key, err)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE auth.permission SET deprecated_at = now() WHERE deprecated_at IS NULL AND NOT (key = ANY($1))`, keys); err != nil {
			return err
		}
		for _, r := range c.TenantRoles {
			if _, err := tx.Exec(ctx, `INSERT INTO auth.role (id, tenant_id, key, name, is_system, requires_mfa) VALUES ($1, NULL, $2, $3, true, $4)
				ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, requires_mfa = EXCLUDED.requires_mfa, updated_at = now()`,
				r.ID, r.Key, r.Name, r.RequiresMFA); err != nil {
				return fmt.Errorf("role %s: %w", r.Key, err)
			}
			if _, err := tx.Exec(ctx, `DELETE FROM auth.role_permission WHERE role_id = $1 AND NOT (permission_key = ANY($2))`, r.ID, r.Permissions); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO auth.role_permission (role_id, permission_key) SELECT $1, unnest($2::text[]) ON CONFLICT DO NOTHING`,
				r.ID, r.Permissions); err != nil {
				return err
			}
		}
		return nil
	})
}

// EnsureSeedAdmin implementa app.Store: crea el superadministrador si no
// existe un usuario con ese email.
func (s *Store) EnsureSeedAdmin(ctx context.Context, u *domain.User) (bool, error) {
	created := false
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM auth."user" WHERE email = $1 AND deleted_at IS NULL)`, u.Email).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
		if _, err := tx.Exec(ctx, `INSERT INTO auth."user" (id, email, display_name, password_hash, status, is_platform_admin, mfa_enforced,
				must_change_password, password_changed_at)
			VALUES ($1, $2, $3, $4, 'active', true, true, true, now())`, u.ID, u.Email, u.DisplayName, u.PasswordHash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO auth.platform_role_assignment (user_id, role_key) VALUES ($1, 'platform_admin')`, u.ID); err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
}

// InsertAudit implementa app.Store: append-only con cadena de hashes por
// tenant (o de plataforma), serializada con un advisory lock por cadena.
func (s *Store) InsertAudit(ctx context.Context, e api.AuditEntry) error {
	var tenant any
	chain := "platform"
	if e.TenantID != uuid.Nil {
		tenant, chain = e.TenantID, e.TenantID.String()
	}
	changes := e.Changes
	if changes == nil {
		changes = map[string]any{}
	}
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('horus.audit:' || $1))`, chain); err != nil {
			return err
		}
		var prev []byte
		err := tx.QueryRow(ctx, `SELECT hash FROM auth.audit_log WHERE tenant_id IS NOT DISTINCT FROM $1 ORDER BY occurred_at DESC, id DESC LIMIT 1`, tenant).Scan(&prev)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		id := uuid.Must(uuid.NewV7())
		canonical, err := json.Marshal(map[string]any{
			"id": id, "tenant_id": tenant, "occurred_at": e.OccurredAt.UTC().Format(time.RFC3339Nano), "actor_type": e.ActorType,
			"actor_id": e.ActorID, "via_platform": e.ViaPlatform, "action": e.Action, "resource_type": e.ResourceType,
			"resource_id": e.ResourceID, "outcome": e.Outcome, "changes": changes,
		})
		if err != nil {
			return err
		}
		h := sha256.New()
		h.Write(prev)
		h.Write(canonical)
		_, err = tx.Exec(ctx, `INSERT INTO auth.audit_log (id, tenant_id, occurred_at, actor_type, actor_id, via_platform, action,
				resource_type, resource_id, scope, outcome, ip, user_agent, changes, request_id, trace_id, prev_hash, hash)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, nullif($9, ''), nullif($10, ''), $11, $12::inet, nullif($13, ''), $14,
				nullif($15, ''), nullif($16, ''), $17, $18)`,
			id, tenant, e.OccurredAt, e.ActorType, e.ActorID, e.ViaPlatform, e.Action, e.ResourceType, e.ResourceID, e.Scope,
			e.Outcome, nullIP(e.IP), e.UserAgent, changes, e.RequestID, e.TraceID, prev, h.Sum(nil))
		return err
	})
}

// insertOutbox escribe un evento con el sobre de C4 (horus.events.v1.Envelope).
func insertOutbox(ctx context.Context, tx pgx.Tx, ev app.OutboxEvent) error {
	var tenant *string
	if ev.TenantID != nil {
		s := ev.TenantID.String()
		tenant = &s
	}
	actor := map[string]any{"type": ev.Actor.Type, "id": ev.Actor.ID, "via_platform": ev.Actor.ViaPlatform}
	if ev.Actor.SID != "" && ev.Actor.SID != uuid.Nil.String() {
		actor["sid"] = ev.Actor.SID
		actor["via"] = "session"
	}
	env := map[string]any{
		"id": ev.ID.String(), "type": ev.Type, "source": "horus/auth", "subject": ev.AggregateID.String(),
		"time": ev.OccurredAt.UTC().Format("2006-01-02T15:04:05.000Z07:00"), "schema_version": 1, "tenant_id": tenant,
		"aggregate_type": ev.AggregateType, "aggregate_version": ev.AggregateVersion, "actor": actor,
		"trace_parent": nil, "correlation_id": nil, "causation_id": nil, "data": ev.Data,
	}
	headers := map[string]any{"Nats-Msg-Id": ev.ID.String()}
	if tenant != nil {
		headers["Horus-Tenant"] = *tenant
	}
	_, err := tx.Exec(ctx, `INSERT INTO auth.outbox (id, tenant_id, subject, aggregate_id, headers, payload, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		ev.ID, ev.TenantID, ev.Type+"."+ev.AggregateID.String(), ev.AggregateID, headers, env, ev.OccurredAt)
	return err
}
