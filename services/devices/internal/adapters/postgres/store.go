// Package postgres implementa app.Store sobre el esquema `devices`. Toda
// consulta corre en una transacción del tenant (devices_app + SET LOCAL
// horus.tenant_id, RLS) y filtra además por tenant_id explícitamente.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// Store es el repositorio.
type Store struct{ db *pgdb.DB }

// New crea el repositorio.
func New(db *pgdb.DB) *Store { return &Store{db: db} }

var _ app.Store = (*Store)(nil)

const schema = "devices"

func emit(ctx context.Context, tx pgx.Tx, evs []outbox.Event) error {
	for _, ev := range evs {
		if err := outbox.Insert(ctx, tx, schema, ev); err != nil {
			return err
		}
	}
	return nil
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

// where construye condiciones con argumentos numerados.
type where struct {
	conds []string
	args  []any
}

func (w *where) add(cond string, args ...any) {
	for _, a := range args {
		w.args = append(w.args, a)
		cond = strings.Replace(cond, "?", fmt.Sprintf("$%d", len(w.args)), 1)
	}
	w.conds = append(w.conds, cond)
}

func (w *where) sql() string { return strings.Join(w.conds, " AND ") }

// keyset añade el filtro (col, id) > (after) según el orden y devuelve ORDER BY.
func keyset(w *where, alias, nameCol string, q app.ListQuery) string {
	col := alias + ".created_at"
	cast := "::timestamptz"
	if q.SortCol == "name" {
		col, cast = alias+"."+nameCol, "::text"
	}
	op, dir := ">", "ASC"
	if q.Desc {
		op, dir = "<", "DESC"
	}
	if q.AfterKey != "" {
		w.add(fmt.Sprintf("(%s, %s.id) %s (?%s, ?::uuid)", col, alias, op, cast), q.AfterKey, q.AfterID)
	}
	return fmt.Sprintf("ORDER BY %s %s, %s.id %s", col, dir, alias, dir)
}

// ------------------------------------------------------------------ sites

const siteCols = `s.id, s.tenant_id, s.parent_id, s.code, s.name, s.kind, s.address, s.latitude, s.longitude, s.timezone, s.tags,
	s.created_at, s.updated_at, s.deleted_at, s.version,
	coalesce((SELECT r.id FROM devices.ip_realm r WHERE r.tenant_id = s.tenant_id AND r.site_id = s.id AND r.kind = 'node_private' AND r.deleted_at IS NULL), '00000000-0000-0000-0000-000000000000'),
	(SELECT ro.id FROM devices.router ro WHERE ro.tenant_id = s.tenant_id AND ro.site_id = s.id AND ro.is_primary AND ro.deleted_at IS NULL),
	(SELECT count(*) FROM devices.client_prefix cp WHERE cp.tenant_id = s.tenant_id AND cp.site_id = s.id AND cp.deleted_at IS NULL)`

func scanSite(row pgx.Row) (*domain.Site, error) {
	var s domain.Site
	err := row.Scan(&s.ID, &s.TenantID, &s.ParentID, &s.Code, &s.Name, &s.Kind, &s.Address, &s.Latitude, &s.Longitude,
		&s.Timezone, &s.Tags, &s.CreatedAt, &s.UpdatedAt, &s.DeletedAt, &s.Version, &s.PrivateRealmID, &s.PrimaryRouterID, &s.PrefixCount)
	if err != nil {
		return nil, notFound(err)
	}
	return &s, nil
}

func siteWhere(t pgdb.TenantID, q app.ListQuery) *where {
	w := &where{}
	w.add("s.tenant_id = ?", t.UUID())
	w.add("s.deleted_at IS NULL")
	if q.Q != "" {
		w.add("(s.name ILIKE '%' || ? || '%' OR s.code ILIKE ? || '%')", q.Q, q.Q)
	}
	if q.Kind != "" {
		w.add("s.kind = ?", q.Kind)
	}
	if q.ParentID != nil {
		w.add("s.parent_id = ?", *q.ParentID)
	}
	if q.Sites != nil {
		w.add("s.id = ANY(?)", q.Sites)
	}
	return w
}

// ListSites implementa app.Store.
func (s *Store) ListSites(ctx context.Context, t pgdb.TenantID, q app.ListQuery) ([]domain.Site, error) {
	var out []domain.Site
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		w := siteWhere(t, q)
		order := keyset(w, "s", "name", q)
		w.args = append(w.args, q.Limit)
		rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s FROM devices.site s WHERE %s %s LIMIT $%d`, siteCols, w.sql(), order, len(w.args)), w.args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			x, err := scanSite(rows)
			if err != nil {
				return err
			}
			out = append(out, *x)
		}
		return rows.Err()
	})
	return out, err
}

// CountSites implementa app.Store.
func (s *Store) CountSites(ctx context.Context, t pgdb.TenantID, q app.ListQuery) (int, error) {
	var n int
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		w := siteWhere(t, q)
		return tx.QueryRow(ctx, `SELECT count(*) FROM devices.site s WHERE `+w.sql(), w.args...).Scan(&n)
	})
	return n, err
}

func getSite(ctx context.Context, tx pgx.Tx, t pgdb.TenantID, id uuid.UUID, lock bool) (*domain.Site, error) {
	q := `SELECT ` + siteCols + ` FROM devices.site s WHERE s.tenant_id = $1 AND s.id = $2 AND s.deleted_at IS NULL`
	if lock {
		q += ` FOR UPDATE OF s`
	}
	return scanSite(tx.QueryRow(ctx, q, t.UUID(), id))
}

// GetSite implementa app.Store.
func (s *Store) GetSite(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Site, error) {
	var out *domain.Site
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = getSite(ctx, tx, t, id, false)
		return err
	})
	return out, err
}

func siteErr(err error) error {
	code, constraint, ok := pgdb.ConstraintError(err)
	switch {
	case !ok:
		return err
	case code == pgdb.SQLStateUniqueViolation && constraint == "ux_site__tenant_code":
		return app.ErrCodeTaken
	case code == pgdb.SQLStateForeignKey:
		return app.ErrRefNotFound
	}
	return err
}

func checkParent(ctx context.Context, tx pgx.Tx, t pgdb.TenantID, parent *uuid.UUID) error {
	if parent == nil {
		return nil
	}
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM devices.site WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL)`,
		t.UUID(), *parent).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return app.ErrRefNotFound
	}
	return nil
}

// CreateSite implementa app.Store: nodo + realm node_private + outbox.
func (s *Store) CreateSite(ctx context.Context, t pgdb.TenantID, site *domain.Site, ev app.Events[domain.Site]) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		if err := checkParent(ctx, tx, t, site.ParentID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO devices.site (id, tenant_id, parent_id, code, name, kind, address, latitude, longitude, timezone, tags,
				created_at, updated_at, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12, 1)`,
			site.ID, t.UUID(), site.ParentID, site.Code, site.Name, site.Kind, site.Address, site.Latitude, site.Longitude,
			site.Timezone, site.Tags, site.CreatedAt)
		if err != nil {
			return siteErr(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO devices.ip_realm (id, tenant_id, kind, site_id, name, created_at) VALUES ($1, $2, 'node_private', $3, $4, $5)`,
			site.PrivateRealmID, t.UUID(), site.ID, site.Name+" (privado)", site.CreatedAt); err != nil {
			return err
		}
		return emit(ctx, tx, ev(site))
	})
}

// UpdateSite implementa app.Store.
func (s *Store) UpdateSite(ctx context.Context, t pgdb.TenantID, site *domain.Site, expect int, ev app.Events[domain.Site]) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		if err := checkParent(ctx, tx, t, site.ParentID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `UPDATE devices.site SET parent_id = $4, code = $5, name = $6, kind = $7, address = $8, latitude = $9,
				longitude = $10, timezone = $11, tags = $12, updated_at = $13, version = version + 1
			WHERE tenant_id = $1 AND id = $2 AND version = $3 AND deleted_at IS NULL RETURNING version`,
			t.UUID(), site.ID, expect, site.ParentID, site.Code, site.Name, site.Kind, site.Address, site.Latitude, site.Longitude,
			site.Timezone, site.Tags, site.UpdatedAt).Scan(&site.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			return app.ErrVersionChanged
		}
		if err != nil {
			return siteErr(err)
		}
		return emit(ctx, tx, ev(site))
	})
}

// DeleteSite implementa app.Store: baja lógica del nodo vacío, su realm y sus prefijos.
func (s *Store) DeleteSite(ctx context.Context, t pgdb.TenantID, id uuid.UUID, expect int, ev func(*domain.Site, []domain.ClientPrefix) []outbox.Event) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		site, err := getSite(ctx, tx, t, id, true)
		if err != nil {
			return err
		}
		if site.Version != expect {
			return app.ErrVersionChanged
		}
		var routers int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM devices.router WHERE tenant_id = $1 AND site_id = $2 AND deleted_at IS NULL`,
			t.UUID(), id).Scan(&routers); err != nil {
			return err
		}
		if routers > 0 {
			return app.ErrSiteNotEmpty
		}
		var children int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM devices.site WHERE tenant_id = $1 AND parent_id = $2 AND deleted_at IS NULL`,
			t.UUID(), id).Scan(&children); err != nil {
			return err
		}
		if children > 0 {
			return app.ErrSiteNotEmpty
		}
		prefixes, err := listPrefixes(ctx, tx, t, app.ListQuery{SiteID: id, Limit: 100000, SortCol: "created_at"})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE devices.client_prefix SET deleted_at = now(), version = version + 1
			WHERE tenant_id = $1 AND site_id = $2 AND deleted_at IS NULL`, t.UUID(), id); err != nil {
			return err
		}
		for i := range prefixes {
			prefixes[i].Version++
		}
		if _, err := tx.Exec(ctx, `UPDATE devices.ip_realm SET deleted_at = now(), version = version + 1
			WHERE tenant_id = $1 AND site_id = $2 AND deleted_at IS NULL`, t.UUID(), id); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `UPDATE devices.site SET deleted_at = now(), version = version + 1 WHERE tenant_id = $1 AND id = $2
			RETURNING version`, t.UUID(), id).Scan(&site.Version); err != nil {
			return err
		}
		return emit(ctx, tx, ev(site, prefixes))
	})
}

// ------------------------------------------------------------------ routers

const routerCols = `r.id, r.tenant_id, r.site_id, r.hostname, r.display_name, r.vendor, r.model, r.is_primary, r.admin_state,
	r.onboarding_state, r.routeros_version, r.routeros_version_detected, r.routeros_version_supported, host(r.tunnel_address),
	r.mgmt_wireguard_peer_id, r.tags, r.created_at, r.updated_at, r.deleted_at, r.version`

func scanRouter(row pgx.Row) (*domain.Router, error) {
	var r domain.Router
	err := row.Scan(&r.ID, &r.TenantID, &r.SiteID, &r.Hostname, &r.DisplayName, &r.Vendor, &r.Model, &r.IsPrimary, &r.AdminState,
		&r.OnboardingState, &r.RouterOSVersion, &r.RouterOSVersionDetected, &r.RouterOSVersionOK, &r.TunnelAddress,
		&r.WireguardPeerID, &r.Tags, &r.CreatedAt, &r.UpdatedAt, &r.DeletedAt, &r.Version)
	if err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

func routerWhere(t pgdb.TenantID, q app.ListQuery) *where {
	w := &where{}
	w.add("r.tenant_id = ?", t.UUID())
	w.add("r.deleted_at IS NULL")
	if q.Q != "" {
		w.add("(r.hostname ILIKE '%' || ? || '%' OR r.display_name ILIKE '%' || ? || '%')", q.Q, q.Q)
	}
	if len(q.SiteIDs) > 0 {
		w.add("r.site_id = ANY(?)", q.SiteIDs)
	}
	if q.OnboardingState != "" {
		w.add("r.onboarding_state = ?", q.OnboardingState)
	}
	if q.IsPrimary != nil {
		w.add("r.is_primary = ?", *q.IsPrimary)
	}
	if q.Sites != nil {
		w.add("r.site_id = ANY(?)", q.Sites)
	}
	return w
}

// ListRouters implementa app.Store.
func (s *Store) ListRouters(ctx context.Context, t pgdb.TenantID, q app.ListQuery) ([]domain.Router, error) {
	var out []domain.Router
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		w := routerWhere(t, q)
		order := keyset(w, "r", "hostname", q)
		w.args = append(w.args, q.Limit)
		rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s FROM devices.router r WHERE %s %s LIMIT $%d`, routerCols, w.sql(), order, len(w.args)), w.args...)
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

// CountRouters implementa app.Store.
func (s *Store) CountRouters(ctx context.Context, t pgdb.TenantID, q app.ListQuery) (int, error) {
	var n int
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		w := routerWhere(t, q)
		return tx.QueryRow(ctx, `SELECT count(*) FROM devices.router r WHERE `+w.sql(), w.args...).Scan(&n)
	})
	return n, err
}

// GetRouter implementa app.Store.
func (s *Store) GetRouter(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Router, error) {
	var out *domain.Router
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = scanRouter(tx.QueryRow(ctx, `SELECT `+routerCols+` FROM devices.router r WHERE r.tenant_id = $1 AND r.id = $2 AND r.deleted_at IS NULL`, t.UUID(), id))
		return err
	})
	return out, err
}

func routerErr(err error) error {
	code, constraint, ok := pgdb.ConstraintError(err)
	switch {
	case !ok:
		return err
	case code == pgdb.SQLStateUniqueViolation && constraint == "ux_router__primary_per_site":
		return app.ErrPrimaryExists
	case code == pgdb.SQLStateUniqueViolation && constraint == "ux_router__tenant_hostname":
		return app.ErrHostnameTaken
	case code == pgdb.SQLStateForeignKey:
		return app.ErrRefNotFound
	}
	return err
}

// CreateRouter implementa app.Store.
func (s *Store) CreateRouter(ctx context.Context, t pgdb.TenantID, r *domain.Router, ev app.Events[domain.Router]) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		if err := checkParent(ctx, tx, t, &r.SiteID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO devices.router (id, tenant_id, site_id, hostname, display_name, vendor, model, is_primary,
				admin_state, onboarding_state, routeros_version, routeros_version_supported, tags, created_at, updated_at, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14, 1)`,
			r.ID, t.UUID(), r.SiteID, r.Hostname, r.DisplayName, r.Vendor, r.Model, r.IsPrimary, r.AdminState, r.OnboardingState,
			r.RouterOSVersion, r.RouterOSVersionOK, r.Tags, r.CreatedAt)
		if err != nil {
			return routerErr(err)
		}
		return emit(ctx, tx, ev(r))
	})
}

// UpdateRouter implementa app.Store.
func (s *Store) UpdateRouter(ctx context.Context, t pgdb.TenantID, r *domain.Router, expect int, ev app.Events[domain.Router]) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `UPDATE devices.router SET hostname = $4, display_name = $5, model = $6, routeros_version = $7,
				routeros_version_supported = $8, admin_state = $9, tags = $10, updated_at = $11, version = version + 1
			WHERE tenant_id = $1 AND id = $2 AND version = $3 AND deleted_at IS NULL RETURNING version`,
			t.UUID(), r.ID, expect, r.Hostname, r.DisplayName, r.Model, r.RouterOSVersion, r.RouterOSVersionOK, r.AdminState, r.Tags, r.UpdatedAt).Scan(&r.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			return app.ErrVersionChanged
		}
		if err != nil {
			return routerErr(err)
		}
		return emit(ctx, tx, ev(r))
	})
}

// DeleteRouter implementa app.Store.
func (s *Store) DeleteRouter(ctx context.Context, t pgdb.TenantID, id uuid.UUID, expect int, ev app.Events[domain.Router]) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		r, err := scanRouter(tx.QueryRow(ctx, `UPDATE devices.router r SET deleted_at = now(), version = version + 1
			WHERE r.tenant_id = $1 AND r.id = $2 AND r.version = $3 AND r.deleted_at IS NULL RETURNING `+routerCols, t.UUID(), id, expect))
		if errors.Is(err, domain.ErrNotFound) {
			return app.ErrVersionChanged
		}
		if err != nil {
			return err
		}
		return emit(ctx, tx, ev(r))
	})
}

// ------------------------------------------------------------------ client prefixes

const prefixCols = `p.id, p.tenant_id, p.site_id, p.realm_id, rl.kind, p.prefix::text, p.role, p.default_kind, p.assignment_mode,
	p.ipv6_client_len, p.source, p.confirmed, p.note, p.created_at, p.updated_at, p.deleted_at, p.version`

func scanPrefix(row pgx.Row) (*domain.ClientPrefix, error) {
	var (
		p   domain.ClientPrefix
		pfx string
		v6  *int16
	)
	err := row.Scan(&p.ID, &p.TenantID, &p.SiteID, &p.RealmID, &p.RealmKind, &pfx, &p.Role, &p.DefaultKind, &p.AssignmentMode,
		&v6, &p.Source, &p.Confirmed, &p.Note, &p.CreatedAt, &p.UpdatedAt, &p.DeletedAt, &p.Version)
	if err != nil {
		return nil, notFound(err)
	}
	if p.Prefix, err = netip.ParsePrefix(pfx); err != nil {
		return nil, fmt.Errorf("devices: bad prefix %q: %w", pfx, err)
	}
	if v6 != nil {
		n := int(*v6)
		p.IPv6ClientLen = &n
	}
	return &p, nil
}

func listPrefixes(ctx context.Context, tx pgx.Tx, t pgdb.TenantID, q app.ListQuery) ([]domain.ClientPrefix, error) {
	w := &where{}
	w.add("p.tenant_id = ?", t.UUID())
	w.add("p.site_id = ?", q.SiteID)
	w.add("p.deleted_at IS NULL")
	if q.Role != "" {
		w.add("p.role = ?", q.Role)
	}
	order := keyset(w, "p", "prefix", q)
	w.args = append(w.args, q.Limit)
	rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s FROM devices.client_prefix p JOIN devices.ip_realm rl ON rl.tenant_id = p.tenant_id AND rl.id = p.realm_id
		WHERE %s %s LIMIT $%d`, prefixCols, w.sql(), order, len(w.args)), w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ClientPrefix
	for rows.Next() {
		x, err := scanPrefix(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}

// ListPrefixes implementa app.Store.
func (s *Store) ListPrefixes(ctx context.Context, t pgdb.TenantID, q app.ListQuery) ([]domain.ClientPrefix, error) {
	var out []domain.ClientPrefix
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = listPrefixes(ctx, tx, t, q)
		return err
	})
	return out, err
}

func getPrefix(ctx context.Context, tx pgx.Tx, t pgdb.TenantID, id uuid.UUID) (*domain.ClientPrefix, error) {
	return scanPrefix(tx.QueryRow(ctx, `SELECT `+prefixCols+` FROM devices.client_prefix p
		JOIN devices.ip_realm rl ON rl.tenant_id = p.tenant_id AND rl.id = p.realm_id
		WHERE p.tenant_id = $1 AND p.id = $2 AND p.deleted_at IS NULL`, t.UUID(), id))
}

// GetPrefix implementa app.Store.
func (s *Store) GetPrefix(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.ClientPrefix, error) {
	var out *domain.ClientPrefix
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = getPrefix(ctx, tx, t, id)
		return err
	})
	return out, err
}

// realmFor resuelve (o crea) el realm del prefijo: node_private del nodo o public del ISP.
func realmFor(ctx context.Context, tx pgx.Tx, t pgdb.TenantID, site uuid.UUID, kind string) (uuid.UUID, error) {
	var id uuid.UUID
	if kind == "node_private" {
		err := tx.QueryRow(ctx, `SELECT id FROM devices.ip_realm WHERE tenant_id = $1 AND site_id = $2 AND kind = 'node_private' AND deleted_at IS NULL`,
			t.UUID(), site).Scan(&id)
		return id, notFound(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO devices.ip_realm (id, tenant_id, kind, name) VALUES ($1, $2, 'public', 'Público')
		ON CONFLICT (tenant_id) WHERE kind = 'public' AND deleted_at IS NULL DO NOTHING`, uuid.Must(uuid.NewV7()), t.UUID()); err != nil {
		return id, err
	}
	err := tx.QueryRow(ctx, `SELECT id FROM devices.ip_realm WHERE tenant_id = $1 AND kind = 'public' AND deleted_at IS NULL`, t.UUID()).Scan(&id)
	return id, err
}

func prefixErr(err error) error {
	if pgdb.IsCode(err, pgdb.SQLStateExclusionViolation) {
		return app.ErrOverlap
	}
	if pgdb.IsCode(err, pgdb.SQLStateForeignKey) {
		return domain.ErrNotFound
	}
	return err
}

// CreatePrefix implementa app.Store.
func (s *Store) CreatePrefix(ctx context.Context, t pgdb.TenantID, p *domain.ClientPrefix, ev app.Events[domain.ClientPrefix]) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		realm, err := realmFor(ctx, tx, t, p.SiteID, p.RealmKind)
		if err != nil {
			return err
		}
		p.RealmID = realm
		_, err = tx.Exec(ctx, `INSERT INTO devices.client_prefix (id, tenant_id, site_id, realm_id, prefix, role, default_kind, assignment_mode,
				ipv6_client_len, source, confirmed, note, created_at, updated_at, version)
			VALUES ($1, $2, $3, $4, $5::cidr, $6, $7, $8, $9, $10, $11, $12, $13, $13, 1)`,
			p.ID, t.UUID(), p.SiteID, p.RealmID, p.Prefix.String(), p.Role, p.DefaultKind, p.AssignmentMode, p.IPv6ClientLen,
			p.Source, p.Confirmed, p.Note, p.CreatedAt)
		if err != nil {
			return prefixErr(err)
		}
		return emit(ctx, tx, ev(p))
	})
}

// UpdatePrefix implementa app.Store.
func (s *Store) UpdatePrefix(ctx context.Context, t pgdb.TenantID, p *domain.ClientPrefix, expect int, ev app.Events[domain.ClientPrefix]) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `UPDATE devices.client_prefix SET role = $4, default_kind = $5, assignment_mode = $6, ipv6_client_len = $7,
				note = $8, updated_at = $9, version = version + 1
			WHERE tenant_id = $1 AND id = $2 AND version = $3 AND deleted_at IS NULL RETURNING version`,
			t.UUID(), p.ID, expect, p.Role, p.DefaultKind, p.AssignmentMode, p.IPv6ClientLen, p.Note, p.UpdatedAt).Scan(&p.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			return app.ErrVersionChanged
		}
		if err != nil {
			return prefixErr(err)
		}
		return emit(ctx, tx, ev(p))
	})
}

// DeletePrefix implementa app.Store: baja lógica y clientes sin prefijo → inactive (prefix_removed).
func (s *Store) DeletePrefix(ctx context.Context, t pgdb.TenantID, id uuid.UUID, expect int, ev app.Events[domain.ClientPrefix]) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		p, err := getPrefix(ctx, tx, t, id)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE devices.client_prefix SET deleted_at = now(), version = version + 1
			WHERE tenant_id = $1 AND id = $2 AND version = $3 AND deleted_at IS NULL`, t.UUID(), id, expect)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return app.ErrVersionChanged
		}
		p.Version++
		if _, err := tx.Exec(ctx, `UPDATE devices.customer SET status = 'inactive', inactive_reason = 'prefix_removed', client_prefix_id = NULL,
				updated_at = now(), version = version + 1
			WHERE tenant_id = $1 AND client_prefix_id = $2`, t.UUID(), id); err != nil {
			return err
		}
		return emit(ctx, tx, ev(p))
	})
}
