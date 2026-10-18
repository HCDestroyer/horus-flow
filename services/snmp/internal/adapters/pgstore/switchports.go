package pgstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// LoadPortSelections devuelve las elecciones de puertos de todos los
// switches (rol de plataforma; el sondeador las recarga cada minuto).
func (s *Store) LoadPortSelections(ctx context.Context) (map[uuid.UUID]domain.PortSelection, error) {
	out := map[uuid.UUID]domain.PortSelection{}
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT router_id, updated_at, updated_by, version FROM snmp.switch_port_selection`)
		if err != nil {
			return fmt.Errorf("snmp: load port selections: %w", err)
		}
		for rows.Next() {
			var p domain.PortSelection
			if err := rows.Scan(&p.RouterID, &p.UpdatedAt, &p.UpdatedBy, &p.Version); err != nil {
				rows.Close()
				return fmt.Errorf("snmp: scan port selection: %w", err)
			}
			p.UpdatedAt, p.Ports = p.UpdatedAt.UTC(), map[string]domain.PortChoice{}
			out[p.RouterID] = p
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT router_id, name, uplink FROM snmp.switch_port`)
		if err != nil {
			return fmt.Errorf("snmp: load switch ports: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var name string
			var up bool
			if err := rows.Scan(&id, &name, &up); err != nil {
				return fmt.Errorf("snmp: scan switch port: %w", err)
			}
			if p, ok := out[id]; ok {
				p.Ports[name] = domain.PortChoice{Monitored: true, Uplink: up}
			}
		}
		return rows.Err()
	})
	return out, err
}

// GetPortSelection devuelve la elección de un switch (nil = por defecto).
func (s *Store) GetPortSelection(ctx context.Context, tenant, routerID uuid.UUID) (*domain.PortSelection, error) {
	var out *domain.PortSelection
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		var err error
		out, err = getPortSelection(ctx, tx, tenant, routerID)
		return err
	})
	return out, err
}

func getPortSelection(ctx context.Context, tx pgx.Tx, tenant, routerID uuid.UUID) (*domain.PortSelection, error) {
	p := domain.PortSelection{RouterID: routerID, Ports: map[string]domain.PortChoice{}}
	err := tx.QueryRow(ctx, `SELECT updated_at, updated_by, version FROM snmp.switch_port_selection WHERE tenant_id = $1 AND router_id = $2`,
		tenant, routerID).Scan(&p.UpdatedAt, &p.UpdatedBy, &p.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("snmp: get port selection: %w", err)
	}
	p.UpdatedAt = p.UpdatedAt.UTC()
	rows, err := tx.Query(ctx, `SELECT name, uplink FROM snmp.switch_port WHERE tenant_id = $1 AND router_id = $2`, tenant, routerID)
	if err != nil {
		return nil, fmt.Errorf("snmp: get switch ports: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var up bool
		if err := rows.Scan(&name, &up); err != nil {
			return nil, err
		}
		p.Ports[name] = domain.PortChoice{Monitored: true, Uplink: up}
	}
	return &p, rows.Err()
}

// PutPortSelection sustituye la elección de puertos de un switch (expect = 0:
// sin If-Match; si no coincide la versión, ErrVersion).
func (s *Store) PutPortSelection(ctx context.Context, tenant, routerID uuid.UUID, ports map[string]domain.PortChoice, actor string,
	expect int) (*domain.PortSelection, error) {
	var out *domain.PortSelection
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		var v int
		err := tx.QueryRow(ctx, `INSERT INTO snmp.switch_port_selection (tenant_id, router_id, updated_by) VALUES ($1, $2, $3)
			ON CONFLICT (tenant_id, router_id) DO UPDATE SET updated_by = excluded.updated_by, updated_at = now(),
				version = snmp.switch_port_selection.version + 1
			WHERE $4 = 0 OR snmp.switch_port_selection.version = $4
			RETURNING version`, tenant, routerID, actor, expect).Scan(&v)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrVersion
		}
		if err != nil {
			return fmt.Errorf("snmp: put port selection: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM snmp.switch_port WHERE tenant_id = $1 AND router_id = $2`, tenant, routerID); err != nil {
			return fmt.Errorf("snmp: clear switch ports: %w", err)
		}
		for name, c := range ports {
			if !c.Monitored {
				continue
			}
			if _, err := tx.Exec(ctx, `INSERT INTO snmp.switch_port (tenant_id, router_id, name, interface_id, uplink) VALUES ($1, $2, $3, $4, $5)`,
				tenant, routerID, name, api.InterfaceID(routerID, name), c.Uplink); err != nil {
				return fmt.Errorf("snmp: insert switch port: %w", err)
			}
		}
		out, err = getPortSelection(ctx, tx, tenant, routerID)
		return err
	})
	return out, err
}

// DeletePortSelection borra la elección (vuelve a la de por defecto).
func (s *Store) DeletePortSelection(ctx context.Context, tenant, routerID uuid.UUID) error {
	return s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM snmp.switch_port_selection WHERE tenant_id = $1 AND router_id = $2`, tenant, routerID)
		return err
	})
}
