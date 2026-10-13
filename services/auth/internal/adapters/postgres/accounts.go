package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// Cuentas: identidad, pertenencia, baja lógica y enlaces de restablecimiento
// de contraseña (docs/security.md §4.5). La cuenta es global: estas
// operaciones van con auth_app o, cuando tocan membresías de varios tenants,
// con auth_platform (multi-tenant declarado).

// UpdateUserIdentity implementa app.Store.
func (s *Store) UpdateUserIdentity(ctx context.Context, in app.UserIdentityChange) (*domain.User, error) {
	var out *domain.User
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		u, err := scanUser(tx.QueryRow(ctx, `SELECT `+userCols+` FROM auth."user" WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, in.UserID))
		if err != nil {
			return err
		}
		var changed []string
		if in.Email != nil && *in.Email != u.Email {
			u.Email = *in.Email
			changed = append(changed, "email")
		}
		if in.DisplayName != nil && *in.DisplayName != u.DisplayName {
			u.DisplayName = *in.DisplayName
			changed = append(changed, "display_name")
		}
		out = u
		if len(changed) == 0 {
			return nil
		}
		err = tx.QueryRow(ctx, `UPDATE auth."user" SET email = $2, display_name = $3, updated_at = $4, version = version + 1
			WHERE id = $1 RETURNING version`, u.ID, u.Email, u.DisplayName, in.Now).Scan(&u.Version)
		if pgdb.IsCode(err, pgdb.SQLStateUniqueViolation) {
			return app.ErrEmailTaken
		}
		if err != nil {
			return err
		}
		if in.Event == nil {
			return nil
		}
		return insertOutbox(ctx, tx, in.Event(*u, changed))
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UserSharedOutside implementa app.Store.
func (s *Store) UserSharedOutside(ctx context.Context, userID, tenant uuid.UUID) (bool, error) {
	var shared bool
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM auth.tenant_membership m JOIN auth.tenant t ON t.id = m.tenant_id AND t.deleted_at IS NULL
				WHERE m.user_id = $1 AND m.tenant_id <> $2 AND m.status <> 'revoked')
			OR EXISTS (SELECT 1 FROM auth.platform_role_assignment p WHERE p.user_id = $1)`, userID, tenant).Scan(&shared)
	})
	return shared, err
}

// DeleteUser implementa app.Store (auth_platform: revoca membresías de todos
// los tenants). El correo queda libre porque ux_user__email es parcial sobre
// deleted_at IS NULL; la fila se conserva para auditoría e historial.
func (s *Store) DeleteUser(ctx context.Context, in app.UserDelete) (*app.UserDeleteResult, error) {
	var res *app.UserDeleteResult
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		if err := lockPlatformAdmins(ctx, tx); err != nil {
			return err
		}
		u, err := scanUser(tx.QueryRow(ctx, `SELECT `+userCols+` FROM auth."user" WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, in.UserID))
		if err != nil {
			return err
		}
		var roles []string
		if err := tx.QueryRow(ctx, `SELECT coalesce(array_agg(role_key ORDER BY role_key), '{}') FROM auth.platform_role_assignment WHERE user_id = $1`,
			u.ID).Scan(&roles); err != nil {
			return err
		}
		res = &app.UserDeleteResult{Email: u.Email, PlatformRoles: roles}
		if u.IsPlatformAdmin && u.Status == domain.UserActive {
			n, err := otherActiveAdmins(ctx, tx, u.ID)
			if err != nil {
				return err
			}
			if n == 0 {
				return app.ErrLastPlatformAdmin
			}
		}
		// Membresías: revocadas con su evento (el gateway corta el acceso al tenant).
		rows, err := tx.Query(ctx, `SELECT id, tenant_id, version FROM auth.tenant_membership WHERE user_id = $1 AND status <> 'revoked' FOR UPDATE`, u.ID)
		if err != nil {
			return err
		}
		type ms struct {
			id, tenant uuid.UUID
			version    int
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ms, error) {
			var m ms
			err := r.Scan(&m.id, &m.tenant, &m.version)
			return m, err
		})
		if err != nil {
			return err
		}
		for _, m := range list {
			if _, err := tx.Exec(ctx, `UPDATE auth.tenant_membership SET status = 'revoked', updated_at = $2, version = version + 1 WHERE id = $1`,
				m.id, in.Now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM auth.role_assignment WHERE membership_id = $1`, m.id); err != nil {
				return err
			}
			tid := m.tenant
			ev := app.OutboxEvent{ID: uuid.Must(uuid.NewV7()), Type: "horus.auth.membership.revoked", TenantID: &tid, AggregateType: "membership",
				AggregateID: m.id, AggregateVersion: m.version + 1, Actor: in.Actor, OccurredAt: in.Now,
				Data: map[string]any{"id": m.id.String(), "user_id": u.ID.String(), "version": m.version + 1, "status": domain.MembershipRevoked,
					"assignments": []any{}, "previous_assignments": []any{}, "reason": "user_deleted"}}
			if err := insertOutbox(ctx, tx, ev); err != nil {
				return err
			}
			res.Tenants = append(res.Tenants, m.tenant)
		}
		for _, q := range []string{
			`DELETE FROM auth.invitation WHERE user_id = $1`,
			`DELETE FROM auth.password_reset WHERE user_id = $1`,
			`DELETE FROM auth.platform_role_assignment WHERE user_id = $1`,
			`DELETE FROM auth.recovery_code WHERE user_id = $1`,
			`DELETE FROM auth.totp_credential WHERE user_id = $1`,
		} {
			if _, err := tx.Exec(ctx, q, u.ID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE auth."user" SET status = 'disabled', is_platform_admin = false, deleted_at = $2, updated_at = $2,
			version = version + 1 WHERE id = $1`, u.ID, in.Now); err != nil {
			return err
		}
		if res.Revoked, err = revokeUserSessions(ctx, tx, u.ID, "user_deleted", in.Actor, in.Now); err != nil {
			return err
		}
		if in.Event == nil {
			return nil
		}
		u.Version++
		return insertOutbox(ctx, tx, in.Event(*u, res.Tenants))
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// CreatePasswordReset implementa app.Store.
func (s *Store) CreatePasswordReset(ctx context.Context, in app.NewPasswordReset) error {
	return s.db.AppTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM auth.password_reset WHERE user_id = $1 AND used_at IS NULL`, in.UserID); err != nil {
			return err
		}
		var by *uuid.UUID
		if in.CreatedBy != uuid.Nil {
			by = &in.CreatedBy
		}
		_, err := tx.Exec(ctx, `INSERT INTO auth.password_reset (id, user_id, token_hash, expires_at, created_by, issued_in_tenant, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, in.ID, in.UserID, in.TokenHash, in.ExpiresAt, by, in.TenantID, in.Now)
		return err
	})
}

// PasswordResetByHash implementa app.Store.
func (s *Store) PasswordResetByHash(ctx context.Context, hash []byte) (*domain.PasswordReset, error) {
	var r domain.PasswordReset
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT p.id, p.user_id, p.expires_at, p.used_at, u.status, u.email::text, u.display_name
			FROM auth.password_reset p JOIN auth."user" u ON u.id = p.user_id AND u.deleted_at IS NULL
			WHERE p.token_hash = $1`, hash).Scan(&r.ID, &r.UserID, &r.ExpiresAt, &r.UsedAt, &r.UserStatus, &r.UserEmail, &r.UserDisplayName)
	})
	if err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

// ConsumePasswordReset implementa app.Store.
func (s *Store) ConsumePasswordReset(ctx context.Context, in app.ConsumedPasswordReset) (*app.PasswordResetResult, error) {
	res := &app.PasswordResetResult{}
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE auth.password_reset SET used_at = $2 WHERE id = $1 AND used_at IS NULL AND expires_at > $2`, in.Reset.ID, in.Now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return app.ErrResetUsed
		}
		tag, err = tx.Exec(ctx, `UPDATE auth."user" SET password_hash = $2, password_changed_at = $3, must_change_password = false,
				failed_logins = 0, locked_until = NULL, updated_at = $3, version = version + 1
			WHERE id = $1 AND status = 'active' AND deleted_at IS NULL`, in.Reset.UserID, in.PasswordHash, in.Now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return app.ErrResetUsed
		}
		if _, err := tx.Exec(ctx, `DELETE FROM auth.password_reset WHERE user_id = $1 AND used_at IS NULL`, in.Reset.UserID); err != nil {
			return err
		}
		res.Revoked, err = revokeUserSessions(ctx, tx, in.Reset.UserID, "password_reset", app.Actor{Type: "user", ID: in.Reset.UserID.String()}, in.Now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}
