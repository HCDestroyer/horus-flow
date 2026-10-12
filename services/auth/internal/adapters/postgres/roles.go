package postgres

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// roleCols lee un rol visible en el tenant de la transacción (RLS de
// auth.role: plantillas + propios; de auth.role_assignment: solo las del
// tenant, así que assignments_count es el de este ISP).
const roleCols = `r.id, r.tenant_id, r.key, r.name, r.is_system, r.requires_mfa, r.description, r.version,
	coalesce((SELECT array_agg(rp.permission_key ORDER BY rp.permission_key) FROM auth.role_permission rp WHERE rp.role_id = r.id), '{}'),
	(SELECT count(*) FROM auth.role_assignment a WHERE a.role_id = r.id)`

func scanRole(row pgx.Row) (*domain.TenantRoleRecord, error) {
	var r domain.TenantRoleRecord
	if err := row.Scan(&r.ID, &r.TenantID, &r.Key, &r.Name, &r.IsSystem, &r.RequiresMFA, &r.Description, &r.Version,
		&r.Permissions, &r.AssignmentsCount); err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

// TenantRole implementa app.Store.
func (s *Store) TenantRole(ctx context.Context, tenant, id uuid.UUID) (*domain.TenantRoleRecord, error) {
	var r *domain.TenantRoleRecord
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		var err error
		r, err = scanRole(tx.QueryRow(ctx, `SELECT `+roleCols+` FROM auth.role r WHERE r.id = $1`, id))
		return err
	})
	return r, err
}

func insertRolePermissions(ctx context.Context, tx pgx.Tx, id uuid.UUID, perms []string) error {
	_, err := tx.Exec(ctx, `INSERT INTO auth.role_permission (role_id, permission_key) SELECT $1, unnest($2::text[])`, id, perms)
	return err
}

// CreateRole implementa app.Store (dentro del tenant: la RLS exige
// tenant_id = el del tenant).
func (s *Store) CreateRole(ctx context.Context, tenant uuid.UUID, r *domain.TenantRoleRecord) error {
	return s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO auth.role (id, tenant_id, key, name, description, is_system, requires_mfa, version)
			VALUES ($1, $2, $3, $4, $5, false, $6, 1)`, r.ID, tenant, r.Key, r.Name, r.Description, r.RequiresMFA); err != nil {
			if pgdb.IsCode(err, pgdb.SQLStateUniqueViolation) {
				return app.ErrRoleKeyTaken
			}
			return err
		}
		r.Version = 1
		return insertRolePermissions(ctx, tx, r.ID, r.Permissions)
	})
}

// customRoleForUpdate bloquea el rol propio id del tenant y comprueba la
// versión.
func customRoleForUpdate(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, expect int) (*domain.TenantRoleRecord, error) {
	cur, err := scanRole(tx.QueryRow(ctx, `SELECT `+roleCols+` FROM auth.role r
		WHERE r.id = $1 AND r.tenant_id = $2 AND NOT r.is_system FOR UPDATE OF r`, id, tenant))
	if err != nil {
		return nil, err
	}
	if cur.Version != expect {
		return nil, &app.RoleVersionError{Current: cur}
	}
	return cur, nil
}

// UpdateRole implementa app.Store.
func (s *Store) UpdateRole(ctx context.Context, tenant uuid.UUID, r *domain.TenantRoleRecord, expect int, now time.Time) error {
	return s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		cur, err := customRoleForUpdate(ctx, tx, tenant, r.ID, expect)
		if err != nil {
			return err
		}
		var v int
		if err := tx.QueryRow(ctx, `UPDATE auth.role SET name = $2, description = $3, requires_mfa = $4, updated_at = $5, version = version + 1
			WHERE id = $1 RETURNING version`, r.ID, r.Name, r.Description, r.RequiresMFA, now).Scan(&v); err != nil {
			return err
		}
		if !slices.Equal(cur.Permissions, r.Permissions) {
			if _, err := tx.Exec(ctx, `DELETE FROM auth.role_permission WHERE role_id = $1`, r.ID); err != nil {
				return err
			}
			if err := insertRolePermissions(ctx, tx, r.ID, r.Permissions); err != nil {
				return err
			}
		}
		r.Version, r.AssignmentsCount = v, cur.AssignmentsCount
		return nil
	})
}

// DeleteRole implementa app.Store. Una asignación concurrente choca con la
// FK de role_assignment (sin cascada) y también es ErrRoleInUse.
func (s *Store) DeleteRole(ctx context.Context, tenant, id uuid.UUID, expect int) error {
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		cur, err := customRoleForUpdate(ctx, tx, tenant, id, expect)
		if err != nil {
			return err
		}
		if cur.AssignmentsCount > 0 {
			return app.ErrRoleInUse
		}
		_, err = tx.Exec(ctx, `DELETE FROM auth.role WHERE id = $1 AND tenant_id = $2 AND NOT is_system`, id, tenant)
		return err
	})
	if pgdb.IsCode(err, pgdb.SQLStateForeignKey) {
		return app.ErrRoleInUse
	}
	return err
}

// PlatformUser implementa app.Store.
func (s *Store) PlatformUser(ctx context.Context, id uuid.UUID) (*domain.PlatformUser, error) {
	rows, err := s.ListUsers(ctx, app.UserFilter{ID: id, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, domain.ErrNotFound
	}
	return &rows[0], nil
}

// lockPlatformAdmins serializa los cambios que pueden dejar la instalación
// sin platform_admin activo (bloqueos y cambios de roles de plataforma).
func lockPlatformAdmins(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('horus.auth:platform_admins'))`)
	return err
}

// otherActiveAdmins cuenta los platform_admin activos distintos de userID.
func otherActiveAdmins(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM auth.platform_role_assignment p JOIN auth."user" u ON u.id = p.user_id
		WHERE p.role_key = 'platform_admin' AND u.status = 'active' AND u.deleted_at IS NULL AND u.id <> $1`, userID).Scan(&n)
	return n, err
}

// revokeUserSessions revoca las sesiones vigentes de userID y escribe su
// horus.auth.session.revoked.
func revokeUserSessions(ctx context.Context, tx pgx.Tx, userID uuid.UUID, reason string, actor app.Actor, now time.Time) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `UPDATE auth.session SET revoked_at = $2, revoked_reason = $3
		WHERE user_id = $1 AND revoked_at IS NULL RETURNING id`, userID, now, reason)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	for _, sid := range ids {
		ev := app.OutboxEvent{ID: uuid.Must(uuid.NewV7()), Type: "horus.auth.session.revoked", AggregateType: "session",
			AggregateID: sid, AggregateVersion: 1, OccurredAt: now, Actor: actor,
			Data: map[string]any{"session_id": sid.String(), "user_id": userID.String(), "reason": reason, "revoked_by": actor.ID}}
		if err := insertOutbox(ctx, tx, ev); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// SetUserStatus implementa app.Store.
func (s *Store) SetUserStatus(ctx context.Context, in app.UserStatusChange) (*app.UserStatusResult, error) {
	var res *app.UserStatusResult
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		if err := lockPlatformAdmins(ctx, tx); err != nil {
			return err
		}
		var status string
		var hasPassword, isAdmin bool
		err := tx.QueryRow(ctx, `SELECT u.status, u.password_hash IS NOT NULL,
				EXISTS (SELECT 1 FROM auth.platform_role_assignment p WHERE p.user_id = u.id AND p.role_key = 'platform_admin')
			FROM auth."user" u WHERE u.id = $1 AND u.deleted_at IS NULL FOR UPDATE`, in.UserID).Scan(&status, &hasPassword, &isAdmin)
		if err != nil {
			return notFound(err)
		}
		res = &app.UserStatusResult{Previous: status, Status: status}
		if in.Disable {
			if status == domain.UserDisabled {
				return nil
			}
			if isAdmin && status == domain.UserActive {
				n, err := otherActiveAdmins(ctx, tx, in.UserID)
				if err != nil {
					return err
				}
				if n == 0 {
					return app.ErrLastPlatformAdmin
				}
			}
			res.Status = domain.UserDisabled
		} else {
			if status != domain.UserDisabled {
				return nil
			}
			// Una cuenta sin contraseña aún no se activó: vuelve a pendiente.
			res.Status = domain.UserActive
			if !hasPassword {
				res.Status = domain.UserPending
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE auth."user" SET status = $2, updated_at = $3, version = version + 1 WHERE id = $1`,
			in.UserID, res.Status, in.Now); err != nil {
			return err
		}
		if in.Disable {
			res.Revoked, err = revokeUserSessions(ctx, tx, in.UserID, "user_disabled", in.Actor, in.Now)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// ReplacePlatformRoles implementa app.Store. is_platform_admin sigue al rol
// platform_admin y mfa_enforced se fija al conceder cualquier rol de
// plataforma (todos exigen 2FA); quitar roles no lo relaja.
func (s *Store) ReplacePlatformRoles(ctx context.Context, in app.PlatformRolesChange) (*app.PlatformRolesResult, error) {
	var res *app.PlatformRolesResult
	err := s.db.AppTx(ctx, func(tx pgx.Tx) error {
		if err := lockPlatformAdmins(ctx, tx); err != nil {
			return err
		}
		var status string
		var prev []string
		err := tx.QueryRow(ctx, `SELECT u.status,
				coalesce((SELECT array_agg(p.role_key ORDER BY p.role_key) FROM auth.platform_role_assignment p WHERE p.user_id = u.id), '{}')
			FROM auth."user" u WHERE u.id = $1 AND u.deleted_at IS NULL FOR UPDATE`, in.UserID).Scan(&status, &prev)
		if err != nil {
			return notFound(err)
		}
		res = &app.PlatformRolesResult{Previous: prev}
		admin := slices.Contains(in.Roles, domain.RolePlatformAdmin)
		if slices.Contains(prev, domain.RolePlatformAdmin) && !admin && status == domain.UserActive {
			n, err := otherActiveAdmins(ctx, tx, in.UserID)
			if err != nil {
				return err
			}
			if n == 0 {
				return app.ErrLastPlatformAdmin
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM auth.platform_role_assignment WHERE user_id = $1 AND NOT (role_key = ANY($2))`,
			in.UserID, in.Roles); err != nil {
			return err
		}
		var by *uuid.UUID
		if in.GrantedBy != uuid.Nil {
			by = &in.GrantedBy
		}
		if _, err := tx.Exec(ctx, `INSERT INTO auth.platform_role_assignment (user_id, role_key, granted_by, granted_at)
			SELECT $1, unnest($2::text[]), $3, $4 ON CONFLICT DO NOTHING`, in.UserID, in.Roles, by, in.Now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE auth."user" SET is_platform_admin = $2, mfa_enforced = mfa_enforced OR $3,
				updated_at = $4, version = version + 1 WHERE id = $1`, in.UserID, admin, len(in.Roles) > 0, in.Now); err != nil {
			return err
		}
		removed := false
		for _, r := range prev {
			removed = removed || !slices.Contains(in.Roles, r)
		}
		if removed {
			res.Revoked, err = revokeUserSessions(ctx, tx, in.UserID, "roles_changed", in.Actor, in.Now)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}
