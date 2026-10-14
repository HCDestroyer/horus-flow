// Package postgres es el almacén PostgreSQL del rol snmp (esquema `snmp`):
// inventario de servicios de infraestructura, estado de sus chequeos y
// outbox. Las transacciones de negocio fijan el tenant (RLS fail-closed); la
// carga del planificador usa el rol de plataforma (multi-tenant declarado).
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/app"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/snmp/migrations"
)

// Store implementa app.InfraStore.
type Store struct{ db *pgdb.DB }

// New crea el almacén.
func New(db *pgdb.DB) *Store { return &Store{db: db} }

var _ app.InfraStore = (*Store)(nil)

const serviceCols = `id, tenant_id, name, site_id, host(target), kind, checks, interval_seconds, failures_before_down,
	recoveries_before_up, enabled, probe_via_router_id, state, state_reason, state_check_type, state_since, created_at, updated_at, version`

const stateCols = `service_id, check_id, check_type, state, reason, detail, latency_ms, consecutive_failures,
	consecutive_successes, had_traffic, since, last_checked_at, recent`

func scanService(row pgx.Row) (*domain.Service, error) {
	var s domain.Service
	var target string
	var checks []byte
	var st domain.ServiceStatus
	if err := row.Scan(&s.ID, &s.TenantID, &s.Name, &s.SiteID, &target, &s.Kind, &checks, &s.IntervalSeconds, &s.FailuresBeforeDown,
		&s.RecoveriesBeforeUp, &s.Enabled, &s.ProbeViaRouterID, &st.State, &st.Reason, &st.CheckType, &st.Since, &s.CreatedAt,
		&s.UpdatedAt, &s.Version); err != nil {
		return nil, err
	}
	a, err := netip.ParseAddr(target)
	if err != nil {
		return nil, fmt.Errorf("snmp: target: %w", err)
	}
	s.Target = a
	if err := json.Unmarshal(checks, &s.Checks); err != nil {
		return nil, fmt.Errorf("snmp: checks: %w", err)
	}
	s.Status = st
	return &s, nil
}

func scanState(row pgx.Row) (uuid.UUID, domain.CheckStatus, error) {
	var sid uuid.UUID
	var c domain.CheckStatus
	var recent []byte
	err := row.Scan(&sid, &c.CheckID, &c.Type, &c.State, &c.Reason, &c.Detail, &c.LatencyMs, &c.ConsecutiveFailures,
		&c.ConsecutiveSuccesses, &c.HadTraffic, &c.Since, &c.LastCheckedAt, &recent)
	if err == nil && len(recent) > 0 {
		err = json.Unmarshal(recent, &c.Recent)
	}
	return sid, c, err
}

// load lee servicios y estados con la consulta de servicios dada.
func load(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]domain.Service, error) {
	rows, err := tx.Query(ctx, `SELECT `+serviceCols+` FROM snmp.infra_service `+where+` ORDER BY lower(name), id`, args...)
	if err != nil {
		return nil, err
	}
	var out []domain.Service
	idx := map[uuid.UUID]int{}
	for rows.Next() {
		s, err := scanService(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		idx[s.ID] = len(out)
		out = append(out, *s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, 0, len(out))
	for _, s := range out {
		ids = append(ids, s.ID)
	}
	srows, err := tx.Query(ctx, `SELECT `+stateCols+` FROM snmp.infra_check_state WHERE service_id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	states := map[uuid.UUID]map[uuid.UUID]domain.CheckStatus{}
	for srows.Next() {
		sid, c, err := scanState(srows)
		if err != nil {
			return nil, err
		}
		if states[sid] == nil {
			states[sid] = map[uuid.UUID]domain.CheckStatus{}
		}
		states[sid][c.CheckID] = c
	}
	if err := srows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		s := &out[i]
		for _, c := range s.Checks {
			if st, ok := states[s.ID][c.ID]; ok {
				st.Type = c.Type
				s.CheckStatus = append(s.CheckStatus, st)
			} else {
				s.CheckStatus = append(s.CheckStatus, domain.NewCheckStatus(c))
			}
		}
	}
	return out, nil
}

// List devuelve los servicios del tenant.
func (st *Store) List(ctx context.Context, t pgdb.TenantID) ([]domain.Service, error) {
	var out []domain.Service
	err := st.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = load(ctx, tx, `WHERE tenant_id = $1`, t.UUID())
		return err
	})
	return out, err
}

// Get devuelve un servicio (app.ErrInfraNotFound si no es del tenant).
func (st *Store) Get(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Service, error) {
	var out []domain.Service
	err := st.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = load(ctx, tx, `WHERE tenant_id = $1 AND id = $2`, t.UUID(), id)
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, app.ErrInfraNotFound
	}
	return &out[0], nil
}

// Count cuenta los servicios del tenant.
func (st *Store) Count(ctx context.Context, t pgdb.TenantID) (int, error) {
	var n int
	err := st.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM snmp.infra_service WHERE tenant_id = $1`, t.UUID()).Scan(&n)
	})
	return n, err
}

func dupOf(err error) error {
	if code, constraint, ok := pgdb.ConstraintError(err); ok && code == pgdb.SQLStateUniqueViolation && constraint == "ux_infra_service__tenant_name" {
		return app.ErrInfraDuplicate
	}
	return err
}

// Insert da de alta el servicio y el estado inicial de sus chequeos.
func (st *Store) Insert(ctx context.Context, t pgdb.TenantID, s *domain.Service) error {
	checks, err := json.Marshal(s.Checks)
	if err != nil {
		return err
	}
	err = st.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO snmp.infra_service (id, tenant_id, name, site_id, target, kind, checks, interval_seconds,
			failures_before_down, recoveries_before_up, enabled, probe_via_router_id, state, created_at, updated_at, version)
			VALUES ($1, $2, $3, $4, $5::inet, $6, $7, $8, $9, $10, $11, $12, 'unknown', $13, $13, 1)`,
			s.ID, t.UUID(), s.Name, s.SiteID, s.Target.String(), s.Kind, checks, s.IntervalSeconds, s.FailuresBeforeDown,
			s.RecoveriesBeforeUp, s.Enabled, s.ProbeViaRouterID, s.CreatedAt); err != nil {
			return dupOf(err)
		}
		return saveStates(ctx, tx, t, s.ID, s.CheckStatus)
	})
	return err
}

// Update guarda la configuración (bloqueo optimista) y borra el estado de
// los chequeos eliminados.
func (st *Store) Update(ctx context.Context, t pgdb.TenantID, s *domain.Service, expect int) error {
	checks, err := json.Marshal(s.Checks)
	if err != nil {
		return err
	}
	return st.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE snmp.infra_service SET name = $3, site_id = $4, target = $5::inet, kind = $6, checks = $7,
			interval_seconds = $8, failures_before_down = $9, recoveries_before_up = $10, enabled = $11, probe_via_router_id = $12,
			updated_at = $13, version = version + 1 WHERE tenant_id = $1 AND id = $2 AND version = $14`,
			t.UUID(), s.ID, s.Name, s.SiteID, s.Target.String(), s.Kind, checks, s.IntervalSeconds, s.FailuresBeforeDown,
			s.RecoveriesBeforeUp, s.Enabled, s.ProbeViaRouterID, s.UpdatedAt, expect)
		if err != nil {
			return dupOf(err)
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM snmp.infra_service WHERE tenant_id = $1 AND id = $2)`, t.UUID(), s.ID).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return app.ErrInfraVersion
			}
			return app.ErrInfraNotFound
		}
		keep := make([]uuid.UUID, 0, len(s.Checks))
		for _, c := range s.Checks {
			keep = append(keep, c.ID)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM snmp.infra_check_state WHERE tenant_id = $1 AND service_id = $2 AND NOT (check_id = ANY($3))`,
			t.UUID(), s.ID, keep); err != nil {
			return err
		}
		return saveStates(ctx, tx, t, s.ID, s.CheckStatus)
	})
}

// Delete borra el servicio (y su estado, en cascada).
func (st *Store) Delete(ctx context.Context, t pgdb.TenantID, id uuid.UUID, expect int) error {
	return st.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM snmp.infra_service WHERE tenant_id = $1 AND id = $2 AND version = $3`, t.UUID(), id, expect)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM snmp.infra_service WHERE tenant_id = $1 AND id = $2)`, t.UUID(), id).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return app.ErrInfraVersion
			}
			return app.ErrInfraNotFound
		}
		return nil
	})
}

// LoadAll lee todos los servicios de la plataforma (planificador).
func (st *Store) LoadAll(ctx context.Context) ([]domain.Service, error) {
	var out []domain.Service
	err := st.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = load(ctx, tx, ``)
		return err
	})
	return out, err
}

func saveStates(ctx context.Context, tx pgx.Tx, t pgdb.TenantID, serviceID uuid.UUID, checks []domain.CheckStatus) error {
	for _, c := range checks {
		recent := c.Recent
		if recent == nil {
			recent = []domain.Result{}
		}
		rb, err := json.Marshal(recent)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO snmp.infra_check_state (tenant_id, service_id, check_id, check_type, state, reason, detail,
			latency_ms, consecutive_failures, consecutive_successes, had_traffic, since, last_checked_at, recent)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			ON CONFLICT (service_id, check_id) DO UPDATE SET check_type = EXCLUDED.check_type, state = EXCLUDED.state,
			reason = EXCLUDED.reason, detail = EXCLUDED.detail, latency_ms = EXCLUDED.latency_ms,
			consecutive_failures = EXCLUDED.consecutive_failures, consecutive_successes = EXCLUDED.consecutive_successes,
			had_traffic = EXCLUDED.had_traffic, since = EXCLUDED.since, last_checked_at = EXCLUDED.last_checked_at, recent = EXCLUDED.recent`,
			t.UUID(), serviceID, c.CheckID, c.Type, c.State, c.Reason, c.Detail, c.LatencyMs, c.ConsecutiveFailures,
			c.ConsecutiveSuccesses, c.HadTraffic, c.Since, c.LastCheckedAt, rb); err != nil {
			return fmt.Errorf("snmp: save check state: %w", err)
		}
	}
	return nil
}

// errGone: el servicio se borró durante el ciclo (se descarta el estado).
var errGone = errors.New("snmp: infra service deleted during the cycle")

// SaveState guarda el estado del ciclo y el evento (outbox) en una
// transacción del tenant. Si el servicio ya no existe no guarda nada.
func (st *Store) SaveState(ctx context.Context, t pgdb.TenantID, serviceID uuid.UUID, status domain.ServiceStatus, checks []domain.CheckStatus, ev *outbox.Event) error {
	err := st.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE snmp.infra_service SET state = $3, state_reason = $4, state_check_type = $5, state_since = $6
			WHERE tenant_id = $1 AND id = $2`, t.UUID(), serviceID, status.State, status.Reason, status.CheckType, status.Since)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errGone
		}
		// Solo los chequeos que siguen configurados (una edición concurrente pudo quitarlos).
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT checks FROM snmp.infra_service WHERE tenant_id = $1 AND id = $2`, t.UUID(), serviceID).Scan(&raw); err != nil {
			return err
		}
		var cfg []domain.Check
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return err
		}
		live := map[uuid.UUID]bool{}
		for _, c := range cfg {
			live[c.ID] = true
		}
		var keep []domain.CheckStatus
		for _, c := range checks {
			if live[c.CheckID] {
				keep = append(keep, c)
			}
		}
		if err := saveStates(ctx, tx, t, serviceID, keep); err != nil {
			return err
		}
		if ev != nil {
			return outbox.Insert(ctx, tx, migrations.Schema, *ev)
		}
		return nil
	})
	if errors.Is(err, errGone) {
		return nil
	}
	return err
}
