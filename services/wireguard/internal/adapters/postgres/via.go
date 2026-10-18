package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/app"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/domain"
)

// Direcciones Horus de los equipos alcanzados vía router (ADR-0032). Salen
// de la MISMA IPAM que las IP de túnel (wireguard.ip_allocation, con
// via_mapping_id en lugar de peer_id): únicas en la plataforma, en
// cuarentena 24 h al liberarse y serializadas con el bloqueo del hub.

const viaCols = `m.id, m.tenant_id, m.server_id, m.router_id, m.kind, m.device_id, m.subnet_id, m.parent_id, m.target::text,
	m.mapped::text, m.created_at, m.updated_at, m.released_at, m.version`

func scanVia(row pgx.Row) (*domain.ViaMapping, error) {
	var (
		m              domain.ViaMapping
		target, mapped string
	)
	if err := row.Scan(&m.ID, &m.TenantID, &m.ServerID, &m.RouterID, &m.Kind, &m.DeviceID, &m.SubnetID, &m.ParentID, &target, &mapped,
		&m.CreatedAt, &m.UpdatedAt, &m.ReleasedAt, &m.Version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	var err error
	if m.Target, err = netip.ParsePrefix(target); err != nil {
		return nil, fmt.Errorf("wireguard: bad via target %q: %w", target, err)
	}
	if m.Mapped, err = netip.ParsePrefix(mapped); err != nil {
		return nil, fmt.Errorf("wireguard: bad via mapped %q: %w", mapped, err)
	}
	return &m, nil
}

func collectVia(rows pgx.Rows) ([]domain.ViaMapping, error) {
	defer rows.Close()
	var out []domain.ViaMapping
	for rows.Next() {
		m, err := scanVia(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func viaErr(err error) error {
	code, constraint, ok := pgdb.ConstraintError(err)
	switch {
	case !ok:
		return err
	case code == pgdb.SQLStateUniqueViolation && constraint == "ux_via_mapping__router_target":
		return domain.ErrViaTargetInUse
	case constraint == "ex_via_mapping__router_subnet":
		return domain.ErrViaSubnetOverlap
	}
	return err
}

// usedAddrs devuelve las direcciones del pool asignadas o liberadas después
// de since (cuarentena), de peers y de direcciones Horus.
func usedAddrs(ctx context.Context, tx pgx.Tx, poolID uuid.UUID, since time.Time) (map[netip.Addr]bool, error) {
	rows, err := tx.Query(ctx, `SELECT host(address) FROM wireguard.ip_allocation
		WHERE pool_id = $1 AND (released_at IS NULL OR released_at > $2)`, poolID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	used := map[netip.Addr]bool{}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		if ad, err := netip.ParseAddr(a); err == nil {
			used[ad] = true
		}
	}
	return used, rows.Err()
}

type poolRow struct {
	id   uuid.UUID
	cidr netip.Prefix
}

func hubPools(ctx context.Context, tx pgx.Tx, hubID uuid.UUID) ([]poolRow, error) {
	rows, err := tx.Query(ctx, `SELECT id, cidr::text FROM wireguard.ip_pool WHERE server_id = $1 AND family = 4 ORDER BY created_at, id`, hubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []poolRow
	for rows.Next() {
		var (
			p poolRow
			c string
		)
		if err := rows.Scan(&p.id, &c); err != nil {
			return nil, err
		}
		p.cidr, _ = netip.ParsePrefix(c)
		out = append(out, p)
	}
	return out, rows.Err()
}

// allocateBlock reserva el primer bloque libre de /bits en los rangos del hub
// (el llamante tiene el hub bloqueado) y asigna cada dirección al mapping.
func allocateBlock(ctx context.Context, tx pgx.Tx, hubID uuid.UUID, services netip.Prefix, tenant, mappingID uuid.UUID, bits int,
	at time.Time,
) (netip.Prefix, error) {
	pools, err := hubPools(ctx, tx, hubID)
	if err != nil {
		return netip.Prefix{}, err
	}
	for _, pl := range pools {
		used, err := usedAddrs(ctx, tx, pl.id, at.Add(-Quarantine))
		if err != nil {
			return netip.Prefix{}, err
		}
		blk, err := domain.NextFreeBlock(pl.cidr, services, used, bits)
		if errors.Is(err, domain.ErrPoolExhausted) {
			continue
		}
		if err != nil {
			return netip.Prefix{}, err
		}
		// Una fila por dirección: la IPAM de las IP de túnel (NextFree) las ve
		// ocupadas sin cambiar su lógica.
		if _, err := tx.Exec(ctx, `INSERT INTO wireguard.ip_allocation (id, pool_id, tenant_id, address, via_mapping_id, allocated_at)
			SELECT gen_random_uuid(), $1, $2, set_masklen($3::inet + g, 32), $4, $5
			FROM generate_series(0, $6::bigint - 1) g`, pl.id, tenant, blk.Addr().String(), mappingID, at,
			int64(1)<<(32-blk.Bits())); err != nil {
			return netip.Prefix{}, err
		}
		return blk, nil
	}
	return netip.Prefix{}, domain.ErrPoolExhausted
}

func lockHubServices(ctx context.Context, tx pgx.Tx, hubID uuid.UUID) (netip.Prefix, error) {
	var services string
	if err := tx.QueryRow(ctx, `SELECT services_cidr::text FROM wireguard.server WHERE id = $1 FOR UPDATE`, hubID).Scan(&services); err != nil {
		return netip.Prefix{}, fmt.Errorf("hub %s: %w", hubID, err)
	}
	svc, _ := netip.ParsePrefix(services)
	return svc, nil
}

// bumpBoth sube la versión deseada de wg-agent y de tunnel-agent (las
// direcciones Horus viajan con el peer del router, sea cual sea su transporte).
func bumpBoth(ctx context.Context, tx pgx.Tx, hubID uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE wireguard.server SET desired_version = desired_version + 1,
		tunnel_desired_version = tunnel_desired_version + 1, updated_at = now() WHERE id = $1`, hubID)
	return err
}

func releaseViaTx(ctx context.Context, tx pgx.Tx, m *domain.ViaMapping, at time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE wireguard.via_mapping SET released_at = $3, updated_at = $3, version = version + 1
		WHERE tenant_id = $1 AND id = $2 AND released_at IS NULL`, m.TenantID, m.ID, at); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE wireguard.ip_allocation SET released_at = $3
		WHERE tenant_id = $1 AND via_mapping_id = $2 AND released_at IS NULL`, m.TenantID, m.ID, at); err != nil {
		return err
	}
	if m.ParentID == nil {
		return nil
	}
	// El bloque de la red vuelve al pool cuando ya no lo usa ningún switch.
	var children int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM wireguard.via_mapping WHERE tenant_id = $1 AND parent_id = $2 AND released_at IS NULL`,
		m.TenantID, *m.ParentID).Scan(&children); err != nil {
		return err
	}
	if children > 0 {
		return nil
	}
	parent, err := scanVia(tx.QueryRow(ctx, `SELECT `+viaCols+` FROM wireguard.via_mapping m WHERE m.tenant_id = $1 AND m.id = $2 AND m.released_at IS NULL`,
		m.TenantID, *m.ParentID))
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return releaseViaTx(ctx, tx, parent, at)
}

// EnsureViaMapping implementa app.Store.
func (s *Store) EnsureViaMapping(ctx context.Context, t pgdb.TenantID, req app.ViaRequest) (*domain.ViaMapping, bool, error) {
	var (
		out     *domain.ViaMapping
		changed bool
	)
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		services, err := lockHubServices(ctx, tx, req.ServerID)
		if err != nil {
			return err
		}
		tenant := t.UUID()
		target := netip.PrefixFrom(req.Target.Unmap(), 32)
		cur, err := scanVia(tx.QueryRow(ctx, `SELECT `+viaCols+` FROM wireguard.via_mapping m
			WHERE m.tenant_id = $1 AND m.device_id = $2 AND m.kind = 'device' AND m.released_at IS NULL`, tenant, req.DeviceID))
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		insert := func(m *domain.ViaMapping) error {
			_, err := tx.Exec(ctx, `INSERT INTO wireguard.via_mapping (id, tenant_id, server_id, router_id, kind, device_id, subnet_id, parent_id,
					target, mapped, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::cidr, $10::cidr, $11, $11)`, m.ID, tenant, m.ServerID, m.RouterID, m.Kind, m.DeviceID,
				m.SubnetID, m.ParentID, m.Target.String(), m.Mapped.String(), m.CreatedAt)
			return viaErr(err)
		}
		if req.Subnet != nil {
			// Red de gestión declarada: bloque Horus del mismo tamaño (una
			// regla netmap en el router para toda la red).
			sid := req.Subnet.ID
			sub, err := scanVia(tx.QueryRow(ctx, `SELECT `+viaCols+` FROM wireguard.via_mapping m
				WHERE m.tenant_id = $1 AND m.subnet_id = $2 AND m.kind = 'subnet' AND m.released_at IS NULL`, tenant, sid))
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return err
			}
			if sub != nil && (sub.RouterID != req.RouterID || sub.Target != req.Subnet.Prefix.Masked()) {
				return domain.ErrViaSubnetOverlap // la red declarada es inmutable (router y prefijo)
			}
			if sub == nil {
				sub = &domain.ViaMapping{ID: uuid.Must(uuid.NewV7()), TenantID: tenant, ServerID: req.ServerID, RouterID: req.RouterID,
					Kind: domain.ViaKindSubnet, SubnetID: &sid, Target: req.Subnet.Prefix.Masked(), CreatedAt: req.At, UpdatedAt: req.At, Version: 1}
				if sub.Mapped, err = allocateBlock(ctx, tx, req.ServerID, services, tenant, sub.ID, sub.Target.Bits(), req.At); err != nil {
					return err
				}
				if err := insert(sub); err != nil {
					return err
				}
				changed = true
			}
			addr, ok := domain.MapInto(sub.Target, sub.Mapped, target.Addr())
			if !ok {
				return fmt.Errorf("wireguard: %s outside subnet %s", target, sub.Target)
			}
			if cur != nil && cur.ParentID != nil && *cur.ParentID == sub.ID && cur.Target == target && cur.RouterID == req.RouterID {
				out = cur
				return nil
			}
			if cur != nil {
				if err := releaseViaTx(ctx, tx, cur, req.At); err != nil {
					return err
				}
			}
			dev := req.DeviceID
			out = &domain.ViaMapping{ID: uuid.Must(uuid.NewV7()), TenantID: tenant, ServerID: req.ServerID, RouterID: req.RouterID,
				Kind: domain.ViaKindDevice, DeviceID: &dev, SubnetID: &sid, ParentID: &sub.ID, Target: target,
				Mapped: netip.PrefixFrom(addr, 32), CreatedAt: req.At, UpdatedAt: req.At, Version: 1}
			if err := insert(out); err != nil {
				return err
			}
			changed = true
			return bumpBoth(ctx, tx, req.ServerID)
		}
		switch {
		case cur != nil && cur.Routed() && cur.RouterID == req.RouterID && cur.Target == target:
			out = cur
			return nil
		case cur != nil && cur.Routed():
			// Misma dirección Horus (identidad estable): cambia el router o la
			// IP privada; el router nuevo debe pegar su configuración.
			if _, err := tx.Exec(ctx, `UPDATE wireguard.via_mapping SET router_id = $3, target = $4::cidr, updated_at = $5, version = version + 1
				WHERE tenant_id = $1 AND id = $2`, tenant, cur.ID, req.RouterID, target.String(), req.At); err != nil {
				return viaErr(err)
			}
			cur.RouterID, cur.Target, cur.UpdatedAt = req.RouterID, target, req.At
			cur.Version++
			out, changed = cur, true
			return bumpBoth(ctx, tx, req.ServerID)
		case cur != nil:
			// Estaba dentro de una red declarada que ya no aplica.
			if err := releaseViaTx(ctx, tx, cur, req.At); err != nil {
				return err
			}
		}
		dev := req.DeviceID
		out = &domain.ViaMapping{ID: uuid.Must(uuid.NewV7()), TenantID: tenant, ServerID: req.ServerID, RouterID: req.RouterID,
			Kind: domain.ViaKindDevice, DeviceID: &dev, Target: target, CreatedAt: req.At, UpdatedAt: req.At, Version: 1}
		if out.Mapped, err = allocateBlock(ctx, tx, req.ServerID, services, tenant, out.ID, 32, req.At); err != nil {
			return err
		}
		if err := insert(out); err != nil {
			return err
		}
		changed = true
		return bumpBoth(ctx, tx, req.ServerID)
	})
	return out, changed, err
}

// ReleaseViaMapping implementa app.Store.
func (s *Store) ReleaseViaMapping(ctx context.Context, t pgdb.TenantID, id uuid.UUID, at time.Time) error {
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		m, err := scanVia(tx.QueryRow(ctx, `SELECT `+viaCols+` FROM wireguard.via_mapping m WHERE m.tenant_id = $1 AND m.id = $2 AND m.released_at IS NULL`,
			t.UUID(), id))
		if err != nil {
			return err
		}
		if _, err := lockHubServices(ctx, tx, m.ServerID); err != nil {
			return err
		}
		if err := releaseViaTx(ctx, tx, m, at); err != nil {
			return err
		}
		return bumpBoth(ctx, tx, m.ServerID)
	})
}

// ViaMappingOf implementa app.Store.
func (s *Store) ViaMappingOf(ctx context.Context, t pgdb.TenantID, deviceID uuid.UUID) (*domain.ViaMapping, error) {
	var out *domain.ViaMapping
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = scanVia(tx.QueryRow(ctx, `SELECT `+viaCols+` FROM wireguard.via_mapping m
			WHERE m.tenant_id = $1 AND m.device_id = $2 AND m.kind = 'device' AND m.released_at IS NULL`, t.UUID(), deviceID))
		return err
	})
	return out, err
}

// GetViaMapping implementa app.Store.
func (s *Store) GetViaMapping(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.ViaMapping, error) {
	var out *domain.ViaMapping
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = scanVia(tx.QueryRow(ctx, `SELECT `+viaCols+` FROM wireguard.via_mapping m WHERE m.tenant_id = $1 AND m.id = $2`, t.UUID(), id))
		return err
	})
	return out, err
}

// AllViaMappings implementa app.Store (plataforma: la reconciliación es multi-tenant).
func (s *Store) AllViaMappings(ctx context.Context) ([]domain.ViaMapping, error) {
	var out []domain.ViaMapping
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+viaCols+` FROM wireguard.via_mapping m WHERE m.released_at IS NULL ORDER BY m.created_at, m.id`)
		if err != nil {
			return err
		}
		out, err = collectVia(rows)
		return err
	})
	return out, err
}

// attachRouted añade a cada peer las direcciones Horus de primer nivel de su
// router (las que el hub enruta por su túnel).
func attachRouted(ctx context.Context, tx pgx.Tx, hubID uuid.UUID, peers []domain.Peer) error {
	rows, err := tx.Query(ctx, `SELECT tenant_id, router_id, mapped::text FROM wireguard.via_mapping
		WHERE server_id = $1 AND released_at IS NULL AND parent_id IS NULL ORDER BY mapped`, hubID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type key struct{ t, r uuid.UUID }
	routed := map[key][]netip.Prefix{}
	for rows.Next() {
		var (
			k key
			m string
		)
		if err := rows.Scan(&k.t, &k.r, &m); err != nil {
			return err
		}
		if p, err := netip.ParsePrefix(m); err == nil {
			routed[k] = append(routed[k], p)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range peers {
		peers[i].Routed = routed[key{peers[i].TenantID, peers[i].RouterID}]
	}
	return nil
}
