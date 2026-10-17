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

// ErrCapacityValidFrom: la vigencia del cambio de capacidad empieza antes
// del cambio anterior.
var ErrCapacityValidFrom = errors.New("snmp: capacity valid_from before the previous change")

const historyCols = `valid_from, valid_to, contracted_bps, changed_by`

func scanPeriod(row pgx.Row, extra ...any) (domain.CapacityPeriod, error) {
	var p domain.CapacityPeriod
	err := row.Scan(append(extra, &p.ValidFrom, &p.ValidTo, &p.ContractedBps, &p.ChangedBy)...)
	p.ValidFrom = p.ValidFrom.UTC()
	if p.ValidTo != nil {
		t := p.ValidTo.UTC()
		p.ValidTo = &t
	}
	return p, err
}

// recordCapacityChange abre una vigencia nueva de la capacidad contratada
// desde `from` y cierra la vigente (en la transacción de la configuración).
func recordCapacityChange(ctx context.Context, tx pgx.Tx, tenant, iface uuid.UUID, contracted *int64, actor string, from time.Time) error {
	var last *time.Time
	if err := tx.QueryRow(ctx, `SELECT max(greatest(valid_from, coalesce(valid_to, valid_from))) FROM snmp.uplink_capacity_history
		WHERE tenant_id = $1 AND interface_id = $2`, tenant, iface).Scan(&last); err != nil {
		return fmt.Errorf("snmp: capacity history: %w", err)
	}
	if last != nil && !from.After(*last) {
		return ErrCapacityValidFrom
	}
	if _, err := tx.Exec(ctx, `UPDATE snmp.uplink_capacity_history SET valid_to = $3
		WHERE tenant_id = $1 AND interface_id = $2 AND valid_to IS NULL`, tenant, iface, from); err != nil {
		return fmt.Errorf("snmp: close capacity period: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO snmp.uplink_capacity_history (tenant_id, interface_id, valid_from, contracted_bps, changed_by)
		VALUES ($1, $2, $3, $4, $5)`, tenant, iface, from, contracted, actor); err != nil {
		return fmt.Errorf("snmp: open capacity period: %w", err)
	}
	return nil
}

// closeCapacityPeriod cierra la vigencia abierta (configuración borrada).
func closeCapacityPeriod(ctx context.Context, tx pgx.Tx, tenant, iface uuid.UUID) error {
	if _, err := tx.Exec(ctx, `UPDATE snmp.uplink_capacity_history SET valid_to = greatest(now(), valid_from + interval '1 second')
		WHERE tenant_id = $1 AND interface_id = $2 AND valid_to IS NULL`, tenant, iface); err != nil {
		return fmt.Errorf("snmp: close capacity period: %w", err)
	}
	return nil
}

// ListCapacityHistory devuelve la historia de capacidad de los enlaces del
// ISP, ordenada por inicio de vigencia.
func (s *Store) ListCapacityHistory(ctx context.Context, tenant uuid.UUID) (map[uuid.UUID][]domain.CapacityPeriod, error) {
	out := map[uuid.UUID][]domain.CapacityPeriod{}
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT interface_id, `+historyCols+` FROM snmp.uplink_capacity_history
			WHERE tenant_id = $1 ORDER BY interface_id, valid_from`, tenant)
		if err != nil {
			return fmt.Errorf("snmp: list capacity history: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			p, err := scanPeriod(rows, &id)
			if err != nil {
				return fmt.Errorf("snmp: scan capacity period: %w", err)
			}
			out[id] = append(out[id], p)
		}
		return rows.Err()
	})
	return out, err
}

// loadHistory carga la historia de todos los ISP (rol de plataforma).
func loadHistory(ctx context.Context, tx pgx.Tx, out map[uuid.UUID]map[uuid.UUID][]domain.CapacityPeriod) error {
	rows, err := tx.Query(ctx, `SELECT tenant_id, interface_id, `+historyCols+` FROM snmp.uplink_capacity_history
		ORDER BY tenant_id, interface_id, valid_from`)
	if err != nil {
		return fmt.Errorf("snmp: load capacity history: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t, id uuid.UUID
		p, err := scanPeriod(rows, &t, &id)
		if err != nil {
			return fmt.Errorf("snmp: scan capacity period: %w", err)
		}
		if out[t] == nil {
			out[t] = map[uuid.UUID][]domain.CapacityPeriod{}
		}
		out[t][id] = append(out[t][id], p)
	}
	return rows.Err()
}
