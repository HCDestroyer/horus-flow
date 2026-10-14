// Package pgstore guarda en PostgreSQL (esquema snmp, RLS por tenant) los
// ajustes por ISP del rol snmp: umbrales de CPU, memoria y temperatura.
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// ErrVersion: la fila cambió desde la versión esperada (If-Match).
var ErrVersion = errors.New("snmp: thresholds version changed")

// Row son los umbrales guardados de un ISP.
type Row struct {
	Values    domain.ThresholdValues
	UpdatedAt time.Time
	Version   int
}

// Store es el repositorio.
type Store struct{ db *pgdb.DB }

// New crea el repositorio.
func New(db *pgdb.DB) *Store { return &Store{db: db} }

const cols = `cpu_warning, cpu_critical, memory_warning, memory_critical, temperature_warning, temperature_critical`

func scanValues(row pgx.Row, extra ...any) (domain.ThresholdValues, error) {
	var v domain.ThresholdValues
	dst := append([]any{&v.CPUWarning, &v.CPUCritical, &v.MemoryWarning, &v.MemoryCritical, &v.TemperatureWarning, &v.TemperatureCritical}, extra...)
	return v, row.Scan(dst...)
}

// Get devuelve los umbrales del ISP (nil si usa los de la instalación).
func (s *Store) Get(ctx context.Context, tenant uuid.UUID) (*Row, error) {
	var out *Row
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		r := Row{}
		v, err := scanValues(tx.QueryRow(ctx, `SELECT `+cols+`, updated_at, version FROM snmp.router_thresholds WHERE tenant_id = $1`, tenant),
			&r.UpdatedAt, &r.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("snmp: get thresholds: %w", err)
		}
		r.Values = v
		out = &r
		return nil
	})
	return out, err
}

// Put guarda los umbrales del ISP (expect > 0: solo si la versión coincide;
// expect = 0 sin control).
func (s *Store) Put(ctx context.Context, tenant uuid.UUID, v domain.ThresholdValues, actor string, expect int) (*Row, error) {
	r := Row{Values: v}
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO snmp.router_thresholds (tenant_id, `+cols+`, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (tenant_id) DO UPDATE SET cpu_warning = excluded.cpu_warning, cpu_critical = excluded.cpu_critical,
				memory_warning = excluded.memory_warning, memory_critical = excluded.memory_critical,
				temperature_warning = excluded.temperature_warning, temperature_critical = excluded.temperature_critical,
				updated_by = excluded.updated_by, updated_at = now(), version = snmp.router_thresholds.version + 1
			WHERE $9 = 0 OR snmp.router_thresholds.version = $9
			RETURNING updated_at, version`, tenant, v.CPUWarning, v.CPUCritical, v.MemoryWarning, v.MemoryCritical,
			v.TemperatureWarning, v.TemperatureCritical, actor, expect).Scan(&r.UpdatedAt, &r.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrVersion
		}
		if err != nil {
			return fmt.Errorf("snmp: put thresholds: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Delete vuelve a los umbrales de la instalación.
func (s *Store) Delete(ctx context.Context, tenant uuid.UUID) error {
	return s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM snmp.router_thresholds WHERE tenant_id = $1`, tenant); err != nil {
			return fmt.Errorf("snmp: delete thresholds: %w", err)
		}
		return nil
	})
}

// LoadAll devuelve los umbrales propios de todos los ISP (rol de plataforma:
// el sondeador es multi-tenant).
func (s *Store) LoadAll(ctx context.Context) (map[uuid.UUID]domain.ThresholdValues, error) {
	out := map[uuid.UUID]domain.ThresholdValues{}
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT tenant_id, `+cols+` FROM snmp.router_thresholds`)
		if err != nil {
			return fmt.Errorf("snmp: load thresholds: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var v domain.ThresholdValues
			if err := rows.Scan(&id, &v.CPUWarning, &v.CPUCritical, &v.MemoryWarning, &v.MemoryCritical, &v.TemperatureWarning,
				&v.TemperatureCritical); err != nil {
				return fmt.Errorf("snmp: scan thresholds: %w", err)
			}
			out[id] = v
		}
		return rows.Err()
	})
	return out, err
}
