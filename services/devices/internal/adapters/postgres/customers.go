package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

var _ app.CustomerStore = (*Store)(nil)

const customerCols = `c.id, c.tenant_id, c.realm_id, c.address, c.site_id, c.client_prefix_id, c.kind, c.kind_source, c.kind_locked,
	c.kind_confidence, c.kind_changed_at, c.commercial_use_suspected, c.security_state, c.open_findings, c.alias, c.alias_source,
	c.notes, c.status, c.inactive_reason, c.first_seen, c.last_seen, c.reset_at, c.created_at, c.updated_at, c.version,
	coalesce((SELECT cp.default_kind FROM devices.client_prefix cp WHERE cp.tenant_id = c.tenant_id AND cp.id = c.client_prefix_id), 'residential')`

func scanCustomer(row pgx.Row) (*domain.Customer, error) {
	var c domain.Customer
	var conf *int16
	err := row.Scan(&c.ID, &c.TenantID, &c.RealmID, &c.Address, &c.SiteID, &c.ClientPrefixID, &c.Kind, &c.KindSource, &c.KindLocked,
		&conf, &c.KindChangedAt, &c.CommercialUseSuspected, &c.SecurityState, &c.OpenFindings, &c.Alias, &c.AliasSource,
		&c.Notes, &c.Status, &c.InactiveReason, &c.FirstSeen, &c.LastSeen, &c.ResetAt, &c.CreatedAt, &c.UpdatedAt, &c.Version,
		&c.DefaultKind)
	if err != nil {
		return nil, notFound(err)
	}
	if conf != nil {
		v := int(*conf)
		c.KindConfidence = &v
	}
	return &c, nil
}

func collectCustomers(rows pgx.Rows) ([]domain.Customer, error) {
	defer rows.Close()
	var out []domain.Customer
	for rows.Next() {
		c, err := scanCustomer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("devices: customers: %w", err)
	}
	return out, nil
}

func customerWhere(t pgdb.TenantID, q app.CustomerQuery) *where {
	w := &where{}
	w.add("c.tenant_id = ?", t.UUID())
	if q.Sites != nil {
		w.add("c.site_id = ANY(?)", q.Sites)
	}
	if q.Q != "" {
		w.add("c.alias ILIKE '%' || ? || '%'", q.Q)
	}
	if len(q.SiteIDs) > 0 {
		w.add("c.site_id = ANY(?)", q.SiteIDs)
	}
	if len(q.RealmIDs) > 0 {
		w.add("c.realm_id = ANY(?)", q.RealmIDs)
	}
	if len(q.PrefixIDs) > 0 {
		w.add("c.client_prefix_id = ANY(?)", q.PrefixIDs)
	}
	if len(q.Statuses) > 0 {
		w.add("c.status = ANY(?)", q.Statuses)
	}
	if len(q.Kinds) > 0 {
		w.add("c.kind = ANY(?)", q.Kinds)
	}
	if len(q.KindSources) > 0 {
		w.add("c.kind_source = ANY(?)", q.KindSources)
	}
	if len(q.SecurityStates) > 0 {
		w.add("c.security_state = ANY(?)", q.SecurityStates)
	}
	if q.KindLocked != nil {
		w.add("c.kind_locked = ?", *q.KindLocked)
	}
	if q.CommercialSuspected != nil {
		w.add("c.commercial_use_suspected = ?", *q.CommercialSuspected)
	}
	if q.HasOpenFindings != nil {
		if *q.HasOpenFindings {
			w.add("c.open_findings > 0")
		} else {
			w.add("c.open_findings = 0")
		}
	}
	if q.LastSeenGTE != nil {
		w.add("c.last_seen >= ?", *q.LastSeenGTE)
	}
	if q.LastSeenLTE != nil {
		w.add("c.last_seen <= ?", *q.LastSeenLTE)
	}
	if q.Contains != nil {
		w.add("c.address >>= ?::inet", q.Contains.String())
	}
	if q.Within != nil {
		w.add("c.address <<= ?::inet", q.Within.String())
	}
	return w
}

// ListCustomers implementa app.CustomerStore.
func (s *Store) ListCustomers(ctx context.Context, t pgdb.TenantID, q app.CustomerQuery) ([]domain.Customer, error) {
	w := customerWhere(t, q)
	col := "c." + q.SortCol
	if q.SortCol == "kind_changed_at" {
		col = "coalesce(c.kind_changed_at, 'epoch'::timestamptz)"
	}
	op, dir := ">", "ASC"
	if q.Desc {
		op, dir = "<", "DESC"
	}
	if q.AfterKey != "" {
		w.add(fmt.Sprintf("(%s, c.id) %s (?::timestamptz, ?::uuid)", col, op), q.AfterKey, q.AfterID)
	}
	var out []domain.Customer
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s FROM devices.customer c WHERE %s ORDER BY %s %s, c.id %s LIMIT %d`,
			customerCols, w.sql(), col, dir, dir, q.Limit), w.args...)
		if err != nil {
			return fmt.Errorf("devices: list customers: %w", err)
		}
		out, err = collectCustomers(rows)
		return err
	})
	return out, err
}

// CustomerStats implementa app.CustomerStore.
func (s *Store) CustomerStats(ctx context.Context, t pgdb.TenantID, sites []uuid.UUID, site *uuid.UUID, dayStart time.Time) (*app.CustomerStats, error) {
	w := &where{}
	w.add("c.tenant_id = ?", t.UUID())
	if sites != nil {
		w.add("c.site_id = ANY(?)", sites)
	}
	if site != nil {
		w.add("c.site_id = ?", *site)
	}
	st := &app.CustomerStats{ByKind: map[string]int{}, ByKindSource: map[string]int{}, ByStatus: map[string]int{},
		BySecurityState: map[string]int{}, BySite: []app.SiteCount{}}
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		args := append(append([]any{}, w.args...), dayStart)
		dayArg := fmt.Sprintf("$%d", len(args))
		rows, err := tx.Query(ctx, `SELECT c.kind, c.kind_source, c.status, c.security_state, c.site_id, (c.open_findings > 0),
			count(*), count(*) FILTER (WHERE c.first_seen >= `+dayArg+`)
			FROM devices.customer c WHERE `+w.sql()+` GROUP BY 1, 2, 3, 4, 5, 6`, args...)
		if err != nil {
			return fmt.Errorf("devices: customer stats: %w", err)
		}
		defer rows.Close()
		bySite := map[uuid.UUID]*app.SiteCount{}
		var order []uuid.UUID
		for rows.Next() {
			var kind, source, status, sec string
			var sid uuid.UUID
			var withFindings bool
			var n, today int
			if err := rows.Scan(&kind, &source, &status, &sec, &sid, &withFindings, &n, &today); err != nil {
				return fmt.Errorf("devices: customer stats: %w", err)
			}
			st.Total += n
			st.NewToday += today
			st.ByKind[kind] += n
			st.ByKindSource[source] += n
			st.ByStatus[status] += n
			st.BySecurityState[sec] += n
			sc, ok := bySite[sid]
			if !ok {
				sc = &app.SiteCount{SiteID: sid}
				bySite[sid] = sc
				order = append(order, sid)
			}
			if status == "active" {
				st.Active += n
				sc.Active += n
				if withFindings {
					sc.WithOpenFindings += n
				}
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("devices: customer stats: %w", err)
		}
		for _, id := range order {
			st.BySite = append(st.BySite, *bySite[id])
		}
		return nil
	})
	return st, err
}

// GetCustomer implementa app.CustomerStore.
func (s *Store) GetCustomer(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Customer, error) {
	var out *domain.Customer
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = scanCustomer(tx.QueryRow(ctx, `SELECT `+customerCols+` FROM devices.customer c WHERE c.tenant_id = $1 AND c.id = $2`, t.UUID(), id))
		return err
	})
	return out, err
}

func insertKindChange(ctx context.Context, tx pgx.Tx, t pgdb.TenantID, ch *domain.KindChange) error {
	var conf *int16
	if ch.Confidence != nil {
		v := int16(*ch.Confidence) //nolint:gosec // 0–100
		conf = &v
	}
	_, err := tx.Exec(ctx, `INSERT INTO devices.customer_kind_change (id, tenant_id, customer_id, from_kind, to_kind, source, reasons,
		reason_codes, model_ref, confidence, actor_id, manual_reason, changed_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		ch.ID, t.UUID(), ch.CustomerID, ch.FromKind, ch.ToKind, ch.Source, ch.Reasons, ch.ReasonCodes, ch.ModelRef, conf, ch.ActorID,
		ch.ManualReason, ch.ChangedAt)
	if err != nil {
		return fmt.Errorf("devices: kind change: %w", err)
	}
	return nil
}

// UpdateCustomer implementa app.CustomerStore.
func (s *Store) UpdateCustomer(ctx context.Context, t pgdb.TenantID, c *domain.Customer, expect int, ch *domain.KindChange, ev app.CustomerEvents) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var conf *int16
		if c.KindConfidence != nil {
			v := int16(*c.KindConfidence) //nolint:gosec // 0–100
			conf = &v
		}
		err := tx.QueryRow(ctx, `UPDATE devices.customer SET kind = $3, kind_source = $4, kind_locked = $5, kind_confidence = $6,
			kind_changed_at = $7, commercial_use_suspected = $8, alias = $9, alias_source = $10, notes = $11, reset_at = $12,
			updated_at = now(), version = version + 1
			WHERE tenant_id = $1 AND id = $2 AND version = $13 RETURNING version, updated_at`,
			t.UUID(), c.ID, c.Kind, c.KindSource, c.KindLocked, conf, c.KindChangedAt, c.CommercialUseSuspected, c.Alias, c.AliasSource,
			c.Notes, c.ResetAt, expect).Scan(&c.Version, &c.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return app.ErrVersionChanged
		}
		if err != nil {
			return fmt.Errorf("devices: update customer: %w", err)
		}
		if ch != nil {
			if err := insertKindChange(ctx, tx, t, ch); err != nil {
				return err
			}
		}
		return emit(ctx, tx, ev(c))
	})
}

// ListKindChanges implementa app.CustomerStore.
func (s *Store) ListKindChanges(ctx context.Context, t pgdb.TenantID, customerID uuid.UUID, afterAt *time.Time, afterID uuid.UUID, limit int) ([]domain.KindChange, error) {
	var out []domain.KindChange
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		w := &where{}
		w.add("tenant_id = ?", t.UUID())
		w.add("customer_id = ?", customerID)
		if afterAt != nil {
			w.add("(changed_at, id) < (?, ?)", *afterAt, afterID)
		}
		rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT id, customer_id, from_kind, to_kind, source, reasons, reason_codes, model_ref, confidence,
			actor_id, manual_reason, changed_at FROM devices.customer_kind_change WHERE %s ORDER BY changed_at DESC, id DESC LIMIT %d`, w.sql(), limit), w.args...)
		if err != nil {
			return fmt.Errorf("devices: kind history: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var k domain.KindChange
			var conf *int16
			if err := rows.Scan(&k.ID, &k.CustomerID, &k.FromKind, &k.ToKind, &k.Source, &k.Reasons, &k.ReasonCodes, &k.ModelRef, &conf,
				&k.ActorID, &k.ManualReason, &k.ChangedAt); err != nil {
				return fmt.Errorf("devices: kind history: %w", err)
			}
			if conf != nil {
				v := int(*conf)
				k.Confidence = &v
			}
			out = append(out, k)
		}
		return rows.Err()
	})
	return out, err
}

type prefixInfo struct {
	id          uuid.UUID
	site        uuid.UUID
	defaultKind string
	v6len       *int16
}

// DiscoverCustomers implementa app.CustomerStore: un cliente por IP dentro de
// un prefijo `customers` del realm; INSERT … ON CONFLICT DO NOTHING.
func (s *Store) DiscoverCustomers(ctx context.Context, t pgdb.TenantID, realm uuid.UUID, clients []app.NewCustomer, ev app.CustomerEvents) ([]domain.Customer, error) {
	var created []domain.Customer
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		created = nil
		for _, nc := range clients {
			addr := nc.Address.Unmap()
			var pi prefixInfo
			err := tx.QueryRow(ctx, `SELECT id, site_id, default_kind, ipv6_client_len FROM devices.client_prefix
				WHERE tenant_id = $1 AND realm_id = $2 AND role = 'customers' AND deleted_at IS NULL AND prefix >>= $3::inet
				ORDER BY masklen(prefix) DESC LIMIT 1`, t.UUID(), realm, addr.String()).Scan(&pi.id, &pi.site, &pi.defaultKind, &pi.v6len)
			if errors.Is(err, pgx.ErrNoRows) {
				continue // fuera de los prefijos de clientes: no es un cliente
			}
			if err != nil {
				return fmt.Errorf("devices: discover prefix: %w", err)
			}
			l := 64
			if pi.v6len != nil {
				l = int(*pi.v6len)
			}
			key := domain.CanonicalAddress(addr, l)
			id := uuid.Must(uuid.NewV7())
			c, err := scanCustomer(tx.QueryRow(ctx, `WITH ins AS (
				INSERT INTO devices.customer (id, tenant_id, realm_id, address, site_id, client_prefix_id, kind, kind_source, first_seen, last_seen)
				VALUES ($1, $2, $3, $4::inet, $5, $6, $7, 'default', $8, $8)
				ON CONFLICT (tenant_id, realm_id, address) DO NOTHING RETURNING *)
				SELECT `+customerColsFrom()+` FROM ins c`, id, t.UUID(), realm, key.String(), pi.site, pi.id, pi.defaultKind, nc.FirstSeen))
			if errors.Is(err, domain.ErrNotFound) {
				continue // ya existía (idempotente)
			}
			if err != nil {
				return fmt.Errorf("devices: discover: %w", err)
			}
			c.DefaultKind = pi.defaultKind
			if err := emit(ctx, tx, ev(c)); err != nil {
				return err
			}
			created = append(created, *c)
		}
		return nil
	})
	return created, err
}

// customerColsFrom es customerCols sin la subconsulta de default_kind (para CTE).
func customerColsFrom() string {
	return `c.id, c.tenant_id, c.realm_id, c.address, c.site_id, c.client_prefix_id, c.kind, c.kind_source, c.kind_locked,
	c.kind_confidence, c.kind_changed_at, c.commercial_use_suspected, c.security_state, c.open_findings, c.alias, c.alias_source,
	c.notes, c.status, c.inactive_reason, c.first_seen, c.last_seen, c.reset_at, c.created_at, c.updated_at, c.version, c.kind`
}

// TouchCustomers implementa app.CustomerStore.
func (s *Store) TouchCustomers(ctx context.Context, t pgdb.TenantID, realm uuid.UUID, seen []app.SeenCustomer, ev app.CustomerEvents) (int, error) {
	n := 0
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		n = 0
		for _, sc := range seen {
			addr := sc.Address.Unmap()
			// La clave puede ser /32, /128 o el prefijo IPv6 del cliente que contiene la IP.
			c, err := scanCustomer(tx.QueryRow(ctx, `WITH old AS (
				SELECT id, status FROM devices.customer WHERE tenant_id = $1 AND realm_id = $2 AND address >>= $3::inet
				ORDER BY masklen(address) DESC LIMIT 1 FOR UPDATE),
				upd AS (UPDATE devices.customer c SET last_seen = greatest(c.last_seen, $4),
					status = 'active', inactive_reason = NULL,
					version = CASE WHEN c.status = 'inactive' THEN c.version + 1 ELSE c.version END,
					updated_at = CASE WHEN c.status = 'inactive' THEN now() ELSE c.updated_at END
					FROM old WHERE c.id = old.id AND c.tenant_id = $1 RETURNING c.*)
				SELECT `+customerColsFrom()+` FROM upd c`, t.UUID(), realm, addr.String(), sc.LastSeen))
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			if err != nil {
				return fmt.Errorf("devices: touch: %w", err)
			}
			// Reactivado si la versión subió respecto a la previa: lo detecta
			// updated_at = now() de esta transacción.
			var reactivated bool
			if err := tx.QueryRow(ctx, `SELECT $1::timestamptz = now()`, c.UpdatedAt).Scan(&reactivated); err != nil {
				return fmt.Errorf("devices: touch: %w", err)
			}
			if reactivated {
				n++
				if err := emit(ctx, tx, ev(c)); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return n, err
}

// InactivateIdle implementa app.CustomerStore.
func (s *Store) InactivateIdle(ctx context.Context, t pgdb.TenantID, before time.Time, ev app.CustomerEvents) (int, error) {
	n := 0
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE devices.customer c SET status = 'inactive', inactive_reason = 'no_traffic',
			version = version + 1, updated_at = now()
			WHERE tenant_id = $1 AND status = 'active' AND last_seen < $2 RETURNING `+customerColsFrom(), t.UUID(), before)
		if err != nil {
			return fmt.Errorf("devices: inactivate: %w", err)
		}
		list, err := collectCustomers(rows)
		if err != nil {
			return err
		}
		n = len(list)
		for i := range list {
			if err := emit(ctx, tx, ev(&list[i])); err != nil {
				return err
			}
		}
		return nil
	})
	return n, err
}

// PurgeExpired implementa app.CustomerStore (el historial cae en cascada).
func (s *Store) PurgeExpired(ctx context.Context, t pgdb.TenantID, before time.Time, ev app.CustomerEvents) (int, error) {
	n := 0
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `DELETE FROM devices.customer c WHERE tenant_id = $1 AND last_seen < $2 RETURNING `+customerColsFrom(),
			t.UUID(), before)
		if err != nil {
			return fmt.Errorf("devices: purge: %w", err)
		}
		list, err := collectCustomers(rows)
		if err != nil {
			return err
		}
		n = len(list)
		for i := range list {
			list[i].Version++
			if err := emit(ctx, tx, ev(&list[i])); err != nil {
				return err
			}
		}
		return nil
	})
	return n, err
}

// CustomerTenants implementa app.CustomerStore (rol de plataforma, solo IDs).
func (s *Store) CustomerTenants(ctx context.Context) ([]uuid.UUID, error) {
	var out []uuid.UUID
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT tenant_id FROM devices.customer`)
		if err != nil {
			return fmt.Errorf("devices: customer tenants: %w", err)
		}
		out, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	})
	return out, err
}
