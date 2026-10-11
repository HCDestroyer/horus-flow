package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// likePattern escapa los comodines de LIKE de una búsqueda libre.
func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

// UpdateProfile implementa app.Store.
func (s *Store) UpdateProfile(ctx context.Context, u *domain.User, now time.Time) error {
	return s.db.AppTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `UPDATE auth."user" SET display_name = $2, locale = $3, timezone = $4, default_tenant_id = $5,
				updated_at = $6, version = version + 1
			WHERE id = $1 AND deleted_at IS NULL RETURNING version`,
			u.ID, u.DisplayName, u.Locale, u.Timezone, u.DefaultTenantID, now).Scan(&u.Version)
	})
}

// ListUsers implementa app.Store (multi-tenant declarado: auth_platform).
func (s *Store) ListUsers(ctx context.Context, f app.UserFilter) ([]domain.PlatformUser, error) {
	conds := []string{"u.deleted_at IS NULL"}
	var args []any
	if f.Q != "" {
		args = append(args, likePattern(f.Q))
		conds = append(conds, fmt.Sprintf("(u.email::text ILIKE $%d OR u.display_name ILIKE $%d)", len(args), len(args)))
	}
	if f.AfterCreated != nil {
		args = append(args, *f.AfterCreated, f.AfterID)
		conds = append(conds, fmt.Sprintf("(u.created_at, u.id) > ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, f.Limit)
	var out []domain.PlatformUser
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, fmt.Sprintf(`
			SELECT u.id, u.email::text, u.display_name, u.status, u.created_at,
			       EXISTS (SELECT 1 FROM auth.totp_credential c WHERE c.user_id = u.id AND c.confirmed_at IS NOT NULL),
			       coalesce((SELECT array_agg(p.role_key ORDER BY p.role_key) FROM auth.platform_role_assignment p WHERE p.user_id = u.id), '{}')
			FROM auth."user" u WHERE %s ORDER BY u.created_at, u.id LIMIT $%d`, strings.Join(conds, " AND "), len(args)), args...)
		if err != nil {
			return err
		}
		idx := map[uuid.UUID]int{}
		var ids []uuid.UUID
		for rows.Next() {
			var u domain.PlatformUser
			if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Status, &u.CreatedAt, &u.MFAEnabled, &u.PlatformRoles); err != nil {
				rows.Close()
				return err
			}
			u.Memberships = []domain.PlatformMembership{}
			idx[u.ID] = len(out)
			ids = append(ids, u.ID)
			out = append(out, u)
		}
		rows.Close()
		if err := rows.Err(); err != nil || len(ids) == 0 {
			return err
		}
		rows, err = tx.Query(ctx, `
			SELECT m.user_id, t.id, t.slug,
			       coalesce(array_agg(DISTINCT r.key ORDER BY r.key) FILTER (WHERE r.key IS NOT NULL), '{}')
			FROM auth.tenant_membership m
			JOIN auth.tenant t ON t.id = m.tenant_id AND t.deleted_at IS NULL
			LEFT JOIN auth.role_assignment a ON a.tenant_id = m.tenant_id AND a.membership_id = m.id
			LEFT JOIN auth.role r ON r.id = a.role_id
			WHERE m.user_id = ANY($1) AND m.status <> 'revoked'
			GROUP BY m.user_id, t.id, t.slug ORDER BY t.slug`, ids)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var uid uuid.UUID
			var pm domain.PlatformMembership
			if err := rows.Scan(&uid, &pm.TenantID, &pm.TenantSlug, &pm.RoleKeys); err != nil {
				return err
			}
			i := idx[uid]
			out[i].Memberships = append(out[i].Memberships, pm)
		}
		return rows.Err()
	})
	return out, err
}

const memberCols = `m.id, m.user_id, m.tenant_id, m.status, m.version, m.created_at, u.display_name, u.email::text,
	EXISTS (SELECT 1 FROM auth.totp_credential c WHERE c.user_id = u.id AND c.confirmed_at IS NOT NULL)`

func scanMember(row pgx.Row) (*domain.Member, error) {
	var m domain.Member
	if err := row.Scan(&m.MembershipID, &m.UserID, &m.TenantID, &m.Status, &m.Version, &m.CreatedAt, &m.DisplayName, &m.Email, &m.MFAEnabled); err != nil {
		return nil, notFound(err)
	}
	m.Assignments = []domain.Assignment{}
	return &m, nil
}

// loadAssignments completa las asignaciones de los miembros (dentro del tenant).
func loadAssignments(ctx context.Context, tx pgx.Tx, ms []*domain.Member) error {
	if len(ms) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(ms))
	byID := make(map[uuid.UUID]*domain.Member, len(ms))
	for i, m := range ms {
		ids[i] = m.MembershipID
		byID[m.MembershipID] = m
	}
	rows, err := tx.Query(ctx, `
		SELECT a.membership_id, r.id, r.key, r.requires_mfa, a.scope_type, a.scope_id,
		       coalesce((SELECT array_agg(rp.permission_key ORDER BY rp.permission_key) FROM auth.role_permission rp WHERE rp.role_id = r.id), '{}')
		FROM auth.role_assignment a JOIN auth.role r ON r.id = a.role_id
		WHERE a.membership_id = ANY($1)
		ORDER BY r.key, a.scope_type, a.scope_id NULLS FIRST`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var mid uuid.UUID
		var a domain.Assignment
		if err := rows.Scan(&mid, &a.RoleID, &a.RoleKey, &a.RequiresMFA, &a.ScopeType, &a.ScopeID, &a.Permissions); err != nil {
			return err
		}
		if m := byID[mid]; m != nil {
			m.Assignments = append(m.Assignments, a)
		}
	}
	return rows.Err()
}

func memberWhere(f app.MemberFilter, withCursor bool) (string, []any) {
	conds := []string{"u.deleted_at IS NULL"}
	var args []any
	if f.Status != "" {
		args = append(args, f.Status)
		conds = append(conds, fmt.Sprintf("m.status = $%d", len(args)))
	} else {
		conds = append(conds, "m.status <> 'revoked'")
	}
	if f.Q != "" {
		args = append(args, likePattern(f.Q))
		conds = append(conds, fmt.Sprintf("(u.email::text ILIKE $%d OR u.display_name ILIKE $%d)", len(args), len(args)))
	}
	if withCursor && f.AfterCreated != nil {
		args = append(args, *f.AfterCreated, f.AfterID)
		conds = append(conds, fmt.Sprintf("(m.created_at, m.id) > ($%d, $%d)", len(args)-1, len(args)))
	}
	return strings.Join(conds, " AND "), args
}

// ListMembers implementa app.Store (dentro del tenant, RLS).
func (s *Store) ListMembers(ctx context.Context, tenant uuid.UUID, f app.MemberFilter) ([]domain.Member, error) {
	where, args := memberWhere(f, true)
	args = append(args, f.Limit)
	var out []domain.Member
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s FROM auth.tenant_membership m JOIN auth."user" u ON u.id = m.user_id
			WHERE %s ORDER BY m.created_at, m.id LIMIT $%d`, memberCols, where, len(args)), args...)
		if err != nil {
			return err
		}
		var ms []*domain.Member
		for rows.Next() {
			m, err := scanMember(rows)
			if err != nil {
				rows.Close()
				return err
			}
			ms = append(ms, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if err := loadAssignments(ctx, tx, ms); err != nil {
			return err
		}
		for _, m := range ms {
			out = append(out, *m)
		}
		return nil
	})
	return out, err
}

// CountMembers implementa app.Store.
func (s *Store) CountMembers(ctx context.Context, tenant uuid.UUID, f app.MemberFilter) (int, error) {
	where, args := memberWhere(f, false)
	var n int
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM auth.tenant_membership m JOIN auth."user" u ON u.id = m.user_id WHERE `+where, args...).Scan(&n)
	})
	return n, err
}

// Member implementa app.Store.
func (s *Store) Member(ctx context.Context, tenant, userID uuid.UUID) (*domain.Member, error) {
	var m *domain.Member
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		var err error
		m, err = scanMember(tx.QueryRow(ctx, `SELECT `+memberCols+` FROM auth.tenant_membership m JOIN auth."user" u ON u.id = m.user_id
			WHERE m.user_id = $1`, userID))
		if err != nil {
			return err
		}
		return loadAssignments(ctx, tx, []*domain.Member{m})
	})
	return m, err
}

// TenantRoles implementa app.Store: la RLS de auth.role deja ver las
// plantillas de sistema y los roles propios del tenant.
func (s *Store) TenantRoles(ctx context.Context, tenant uuid.UUID) ([]domain.TenantRoleRecord, error) {
	var out []domain.TenantRoleRecord
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT r.id, r.tenant_id, r.key, r.name, r.is_system, r.requires_mfa,
			       coalesce((SELECT array_agg(rp.permission_key ORDER BY rp.permission_key) FROM auth.role_permission rp WHERE rp.role_id = r.id), '{}')
			FROM auth.role r ORDER BY r.is_system DESC, r.key`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r domain.TenantRoleRecord
			if err := rows.Scan(&r.ID, &r.TenantID, &r.Key, &r.Name, &r.IsSystem, &r.RequiresMFA, &r.Permissions); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// checkRoles comprueba dentro del tenant (RLS) que todos los roles son
// visibles: la FK de role_assignment no pasa por la RLS.
func checkRoles(ctx context.Context, tx pgx.Tx, as []domain.Assignment) error {
	if len(as) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(as))
	for _, a := range as {
		ids = append(ids, a.RoleID)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(DISTINCT id) FROM auth.role WHERE id = ANY($1)`, ids).Scan(&n); err != nil {
		return err
	}
	distinct := map[uuid.UUID]bool{}
	for _, id := range ids {
		distinct[id] = true
	}
	if n != len(distinct) {
		return app.ErrUnknownRole
	}
	return nil
}

func insertAssignments(ctx context.Context, tx pgx.Tx, tenant, membershipID uuid.UUID, as []domain.Assignment) error {
	for _, a := range as {
		if _, err := tx.Exec(ctx, `INSERT INTO auth.role_assignment (id, tenant_id, membership_id, role_id, scope_type, scope_id)
			VALUES ($1, $2, $3, $4, $5, $6)`, uuid.Must(uuid.NewV7()), tenant, membershipID, a.RoleID, a.ScopeType, a.ScopeID); err != nil {
			return err
		}
	}
	return nil
}

// memberForUpdate lee la membresía de userID bloqueándola.
func memberForUpdate(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (*domain.Member, error) {
	m, err := scanMember(tx.QueryRow(ctx, `SELECT `+memberCols+` FROM auth.tenant_membership m JOIN auth."user" u ON u.id = m.user_id
		WHERE m.user_id = $1 FOR UPDATE OF m`, userID))
	if err != nil {
		return nil, err
	}
	if err := loadAssignments(ctx, tx, []*domain.Member{m}); err != nil {
		return nil, err
	}
	return m, nil
}

// InviteMember implementa app.Store: todo en una transacción dentro del
// tenant (usuario, membresía, asignaciones, invitación y evento).
func (s *Store) InviteMember(ctx context.Context, in app.NewInvitation) (*app.InviteResult, error) {
	var res *app.InviteResult
	err := s.db.TenantTx(ctx, pgdb.TenantID(in.TenantID), func(tx pgx.Tx) error {
		res = &app.InviteResult{}
		if err := checkRoles(ctx, tx, in.Assignments); err != nil {
			return err
		}
		var userStatus string
		err := tx.QueryRow(ctx, `SELECT id, status FROM auth."user" WHERE email = $1 AND deleted_at IS NULL`, in.Email).Scan(&res.UserID, &userStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			res.UserID, userStatus, res.UserCreated = uuid.Must(uuid.NewV7()), domain.UserPending, true
			local, _, _ := strings.Cut(in.Email, "@")
			tag, err := tx.Exec(ctx, `INSERT INTO auth."user" (id, email, display_name, status) VALUES ($1, $2, $3, 'pending') ON CONFLICT DO NOTHING`,
				res.UserID, in.Email, local)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 { // alta concurrente del mismo email
				if err := tx.QueryRow(ctx, `SELECT id, status FROM auth."user" WHERE email = $1 AND deleted_at IS NULL`, in.Email).
					Scan(&res.UserID, &userStatus); err != nil {
					return err
				}
				res.UserCreated = false
			}
		} else if err != nil {
			return err
		}
		status := domain.MembershipInvited
		if userStatus == domain.UserActive {
			status = domain.MembershipActive
		}
		cur, err := memberForUpdate(ctx, tx, res.UserID)
		switch {
		case errors.Is(err, domain.ErrNotFound):
			res.MembershipID = uuid.Must(uuid.NewV7())
			if _, err := tx.Exec(ctx, `INSERT INTO auth.tenant_membership (id, tenant_id, user_id, status, granted_by, granted_at, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $6, $6)`, res.MembershipID, in.TenantID, res.UserID, status, in.GrantedBy, in.Now); err != nil {
				return err
			}
			if err := insertAssignments(ctx, tx, in.TenantID, res.MembershipID, in.Assignments); err != nil {
				return err
			}
			res.Status = status
			if in.Event != nil {
				if err := insertOutbox(ctx, tx, in.Event(domain.Member{MembershipID: res.MembershipID, UserID: res.UserID, TenantID: in.TenantID,
					Status: status, Assignments: in.Assignments, Version: 1}, nil)); err != nil {
					return err
				}
			}
		case err != nil:
			return err
		case cur.Status == domain.MembershipRevoked:
			res.MembershipID = cur.MembershipID
			var v int
			if err := tx.QueryRow(ctx, `UPDATE auth.tenant_membership SET status = $2, granted_by = $3, granted_at = $4, expires_at = NULL,
					updated_at = $4, version = version + 1 WHERE id = $1 RETURNING version`, cur.MembershipID, status, in.GrantedBy, in.Now).Scan(&v); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM auth.role_assignment WHERE membership_id = $1`, cur.MembershipID); err != nil {
				return err
			}
			if err := insertAssignments(ctx, tx, in.TenantID, cur.MembershipID, in.Assignments); err != nil {
				return err
			}
			res.Status = status
			if in.Event != nil {
				if err := insertOutbox(ctx, tx, in.Event(domain.Member{MembershipID: cur.MembershipID, UserID: res.UserID, TenantID: in.TenantID,
					Status: status, Assignments: in.Assignments, Version: v}, cur.Assignments)); err != nil {
					return err
				}
			}
		default:
			res.MembershipID, res.Status, res.AlreadyMember = cur.MembershipID, cur.Status, true
		}
		if res.Status != domain.MembershipInvited || userStatus != domain.UserPending || len(in.TokenHash) == 0 {
			return nil
		}
		if _, err := tx.Exec(ctx, `DELETE FROM auth.invitation WHERE membership_id = $1 AND used_at IS NULL`, res.MembershipID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO auth.invitation (id, tenant_id, membership_id, user_id, token_hash, expires_at, created_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, uuid.Must(uuid.NewV7()), in.TenantID, res.MembershipID, res.UserID, in.TokenHash,
			in.ExpiresAt, in.GrantedBy, in.Now); err != nil {
			return err
		}
		res.TokenStored = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// RevokeMember implementa app.Store.
func (s *Store) RevokeMember(ctx context.Context, tenant, userID uuid.UUID, now time.Time, ev app.MemberEvent) (bool, error) {
	ok := false
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		cur, err := memberForUpdate(ctx, tx, userID)
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if cur.Status == domain.MembershipRevoked {
			return nil
		}
		var v int
		if err := tx.QueryRow(ctx, `UPDATE auth.tenant_membership SET status = 'revoked', updated_at = $2, version = version + 1
			WHERE id = $1 RETURNING version`, cur.MembershipID, now).Scan(&v); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM auth.role_assignment WHERE membership_id = $1`, cur.MembershipID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM auth.invitation WHERE membership_id = $1`, cur.MembershipID); err != nil {
			return err
		}
		ok = true
		if ev == nil {
			return nil
		}
		next := *cur
		next.Status, next.Version, next.Assignments = domain.MembershipRevoked, v, nil
		return insertOutbox(ctx, tx, ev(next, cur.Assignments))
	})
	return ok, err
}

// ReplaceAssignments implementa app.Store.
func (s *Store) ReplaceAssignments(ctx context.Context, tenant, userID uuid.UUID, expect int, as []domain.Assignment, now time.Time,
	ev app.MemberEvent,
) (*domain.Member, error) {
	var out *domain.Member
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		cur, err := memberForUpdate(ctx, tx, userID)
		if err != nil {
			return err
		}
		if cur.Status == domain.MembershipRevoked {
			return domain.ErrNotFound
		}
		if cur.Version != expect {
			return &app.MemberVersionError{Current: cur}
		}
		if err := checkRoles(ctx, tx, as); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM auth.role_assignment WHERE membership_id = $1`, cur.MembershipID); err != nil {
			return err
		}
		if err := insertAssignments(ctx, tx, tenant, cur.MembershipID, as); err != nil {
			return err
		}
		var v int
		if err := tx.QueryRow(ctx, `UPDATE auth.tenant_membership SET updated_at = $2, version = version + 1 WHERE id = $1 RETURNING version`,
			cur.MembershipID, now).Scan(&v); err != nil {
			return err
		}
		next, err := memberForUpdate(ctx, tx, userID)
		if err != nil {
			return err
		}
		next.Version = v
		if ev != nil {
			if err := insertOutbox(ctx, tx, ev(*next, cur.Assignments)); err != nil {
				return err
			}
		}
		out = next
		return nil
	})
	return out, err
}

// InvitationByHash implementa app.Store (rol de plataforma: canje público).
func (s *Store) InvitationByHash(ctx context.Context, hash []byte) (*domain.Invitation, error) {
	var inv domain.Invitation
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT i.id, i.tenant_id, i.membership_id, i.user_id, i.expires_at, i.used_at, m.status, u.status, u.email::text, u.display_name
			FROM auth.invitation i
			JOIN auth.tenant_membership m ON m.tenant_id = i.tenant_id AND m.id = i.membership_id
			JOIN auth."user" u ON u.id = i.user_id AND u.deleted_at IS NULL
			WHERE i.token_hash = $1`, hash).
			Scan(&inv.ID, &inv.TenantID, &inv.MembershipID, &inv.UserID, &inv.ExpiresAt, &inv.UsedAt, &inv.MembershipStatus,
				&inv.UserStatus, &inv.UserEmail, &inv.UserDisplayName)
	})
	if err != nil {
		return nil, notFound(err)
	}
	return &inv, nil
}

// AcceptInvitation implementa app.Store: consumo de un uso (CAS sobre
// used_at), activación del usuario pendiente y de la membresía invitada y
// evento, en una transacción dentro del tenant de la invitación.
func (s *Store) AcceptInvitation(ctx context.Context, in app.AcceptedInvitation) error {
	inv := in.Invitation
	return s.db.TenantTx(ctx, pgdb.TenantID(inv.TenantID), func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE auth.invitation SET used_at = $2 WHERE id = $1 AND used_at IS NULL AND expires_at > $2`, inv.ID, in.Now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return app.ErrInvitationUsed
		}
		if in.PasswordHash != "" {
			tag, err := tx.Exec(ctx, `UPDATE auth."user" SET password_hash = $2, display_name = $3, status = 'active', password_changed_at = $4,
					must_change_password = false, failed_logins = 0, locked_until = NULL, updated_at = $4, version = version + 1
				WHERE id = $1 AND status = 'pending' AND deleted_at IS NULL`, inv.UserID, in.PasswordHash, in.DisplayName, in.Now)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return app.ErrInvitationUsed
			}
		}
		var v int
		err = tx.QueryRow(ctx, `UPDATE auth.tenant_membership SET status = 'active', updated_at = $2, version = version + 1
			WHERE id = $1 AND status = 'invited' RETURNING version`, inv.MembershipID, in.Now).Scan(&v)
		if errors.Is(err, pgx.ErrNoRows) {
			return app.ErrInvitationUsed
		}
		if err != nil {
			return err
		}
		// Las demás invitaciones de esta persona a otros tenants siguen
		// vigentes; las de esta membresía ya no sirven.
		if _, err := tx.Exec(ctx, `DELETE FROM auth.invitation WHERE membership_id = $1 AND used_at IS NULL`, inv.MembershipID); err != nil {
			return err
		}
		if in.Event == nil {
			return nil
		}
		m, err := memberForUpdate(ctx, tx, inv.UserID)
		if err != nil {
			return err
		}
		return insertOutbox(ctx, tx, in.Event(*m, m.Assignments))
	})
}
