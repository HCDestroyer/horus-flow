// Package postgres implementa app.Store sobre el esquema `wireguard`. Las
// operaciones de un ISP corren en TenantTx (RLS) y filtran por tenant_id;
// la IPAM (unicidad global de la /32), el enrolamiento público (búsqueda por
// hash del token) y los reportes del agente usan PlatformTx (métodos
// multi-tenant declarados) y fijan siempre tenant_id de forma explícita.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/app"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/domain"
)

// Store es el repositorio.
type Store struct{ db *pgdb.DB }

// New crea el repositorio.
func New(db *pgdb.DB) *Store { return &Store{db: db} }

var _ app.Store = (*Store)(nil)

const schema = "wireguard"

// Quarantine: una IP liberada no se reasigna en 24 h (database.md §2.4).
const Quarantine = domain.Quarantine

func emit(ctx context.Context, tx pgx.Tx, evs []outbox.Event) error {
	for _, ev := range evs {
		if err := outbox.Insert(ctx, tx, schema, ev); err != nil {
			return err
		}
	}
	return nil
}

// Emit implementa app.Store.
func (s *Store) Emit(ctx context.Context, evs []outbox.Event) error {
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error { return emit(ctx, tx, evs) })
}

// ------------------------------------------------------------------ hub

// EnsureHub implementa app.Store. La configuración (HORUS_WG_*) da de alta
// el hub; después solo actualiza lo que no se editó desde la consola
// (console_overrides). La clave pública, el puerto (lo publica Docker) y la
// red de servicios siempre vienen del entorno. Los rangos nuevos del entorno
// se insertan; ninguno se borra (ni los quitados del entorno ni los añadidos
// desde la consola).
func (s *Store) EnsureHub(ctx context.Context, h app.Hub) ([]app.EnvIgnored, error) {
	var ignored []app.EnvIgnored
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		ignored = nil
		var (
			name, endpoint string
			overrides      []string
		)
		err := tx.QueryRow(ctx, `SELECT name, endpoint, console_overrides FROM wireguard.server WHERE id = $1 FOR UPDATE`, h.ID).
			Scan(&name, &endpoint, &overrides)
		created := errors.Is(err, pgx.ErrNoRows)
		switch {
		case created:
			if _, err := tx.Exec(ctx, `INSERT INTO wireguard.server (id, name, endpoint, listen_port, public_key, services_cidr)
				VALUES ($1, $2, $3, $4, nullif($5, ''), $6::cidr)`,
				h.ID, h.Name, h.Endpoint, h.ListenPort, h.PublicKey, h.ServicesCIDR.String()); err != nil {
				return fmt.Errorf("ensure hub: %w", err)
			}
		case err != nil:
			return fmt.Errorf("ensure hub: %w", err)
		default:
			if slices.Contains(overrides, domain.FieldName) && name != h.Name {
				ignored = append(ignored, app.EnvIgnored{Field: domain.FieldName, Env: h.Name, Current: name})
			}
			if slices.Contains(overrides, domain.FieldEndpoint) && endpoint != h.Endpoint {
				ignored = append(ignored, app.EnvIgnored{Field: domain.FieldEndpoint, Env: h.Endpoint, Current: endpoint})
			}
			// version (ETag) sube solo si cambia algo visible del hub.
			if _, err := tx.Exec(ctx, `UPDATE wireguard.server s SET name = n.name, endpoint = n.endpoint, listen_port = n.listen_port,
					public_key = n.public_key, services_cidr = n.services_cidr, updated_at = now(),
					version = s.version + CASE WHEN (n.name, n.endpoint, n.listen_port, n.public_key, n.services_cidr)
						IS DISTINCT FROM (s.name, s.endpoint, s.listen_port, s.public_key, s.services_cidr) THEN 1 ELSE 0 END
				FROM (SELECT CASE WHEN 'name' = ANY (o.console_overrides) THEN o.name ELSE $2 END AS name,
						CASE WHEN 'endpoint' = ANY (o.console_overrides) THEN o.endpoint ELSE $3 END AS endpoint,
						$4::integer AS listen_port, coalesce(nullif($5, ''), o.public_key) AS public_key, $6::cidr AS services_cidr
					FROM wireguard.server o WHERE o.id = $1) n
				WHERE s.id = $1`,
				h.ID, h.Name, h.Endpoint, h.ListenPort, h.PublicKey, h.ServicesCIDR.String()); err != nil {
				return fmt.Errorf("ensure hub: %w", err)
			}
		}
		added := false
		for _, p := range h.Pools {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM wireguard.ip_pool WHERE server_id = $1 AND cidr = $2::cidr)`,
				h.ID, p.String()).Scan(&exists); err != nil {
				return err
			}
			if exists {
				continue
			}
			if _, err := tx.Exec(ctx, `INSERT INTO wireguard.ip_pool (id, server_id, cidr, family) VALUES ($1, $2, $3::cidr, $4)`,
				uuid.Must(uuid.NewV7()), h.ID, p.String(), family(p)); err != nil {
				if pgdb.IsCode(err, pgdb.SQLStateExclusionViolation) {
					return fmt.Errorf("%w: %s overlaps an existing tunnel range", domain.ErrInvalidPool, p)
				}
				return fmt.Errorf("ensure pool %s: %w", p, err)
			}
			added = true
		}
		if added && !created {
			// Rango nuevo en un hub existente: nueva versión del hub y del estado deseado.
			if _, err := tx.Exec(ctx, `UPDATE wireguard.server SET version = version + 1, desired_version = desired_version + 1,
				updated_at = now() WHERE id = $1`, h.ID); err != nil {
				return err
			}
		}
		return nil
	})
	return ignored, err
}

func family(p netip.Prefix) int {
	if p.Addr().Is4() {
		return 4
	}
	return 6
}

// poolsOrder ordena los rangos como los recorre la IPAM.
const poolsOrder = `ORDER BY p.created_at, p.id`

const hubCols = `s.id, s.name, s.endpoint, s.listen_port, coalesce(s.public_key, ''), s.services_cidr::text, s.status, s.desired_version,
	s.applied_version, s.last_report_at, s.mtu, s.persistent_keepalive_seconds, s.version, s.console_overrides,
	coalesce((SELECT array_agg(p.id ` + poolsOrder + `) FROM wireguard.ip_pool p WHERE p.server_id = s.id), '{}'),
	coalesce((SELECT array_agg(p.cidr::text ` + poolsOrder + `) FROM wireguard.ip_pool p WHERE p.server_id = s.id), '{}'),
	coalesce((SELECT array_agg(p.created_at ` + poolsOrder + `) FROM wireguard.ip_pool p WHERE p.server_id = s.id), '{}'),
	coalesce((SELECT array_agg((SELECT count(*) FROM wireguard.ip_allocation a WHERE a.pool_id = p.id AND a.released_at IS NULL) ` +
	poolsOrder + `) FROM wireguard.ip_pool p WHERE p.server_id = s.id), '{}'),
	(SELECT count(*) FROM wireguard.peer pe WHERE pe.server_id = s.id AND pe.status = 'active'),
	(SELECT count(DISTINCT pe.tenant_id) FROM wireguard.peer pe WHERE pe.server_id = s.id AND pe.revoked_at IS NULL)`

func scanHub(row pgx.Row) (*app.Hub, error) {
	var (
		h        app.Hub
		services string
		ids      []uuid.UUID
		cidrs    []string
		created  []time.Time
		used     []int64
	)
	if err := row.Scan(&h.ID, &h.Name, &h.Endpoint, &h.ListenPort, &h.PublicKey, &services, &h.Status, &h.DesiredVersion,
		&h.AppliedVersion, &h.LastReportAt, &h.MTU, &h.Keepalive, &h.Version, &h.Overrides, &ids, &cidrs, &created, &used,
		&h.PeersActive, &h.Tenants); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	h.ServicesCIDR, _ = netip.ParsePrefix(services)
	for i, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil || i >= len(ids) || i >= len(created) || i >= len(used) {
			continue
		}
		h.Pools = append(h.Pools, p)
		h.PoolInfo = append(h.PoolInfo, app.Pool{ID: ids[i], CIDR: p, CreatedAt: created[i], Used: int(used[i])})
		h.AddressesUsed += int(used[i])
	}
	return &h, nil
}

func getHubTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*app.Hub, error) {
	return scanHub(tx.QueryRow(ctx, `SELECT `+hubCols+` FROM wireguard.server s WHERE s.id = $1`, id))
}

// GetHub implementa app.Store.
func (s *Store) GetHub(ctx context.Context, id uuid.UUID) (*app.Hub, error) {
	var h *app.Hub
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var err error
		h, err = getHubTx(ctx, tx, id)
		return err
	})
	return h, err
}

// ListHubs implementa app.Store.
func (s *Store) ListHubs(ctx context.Context) ([]app.Hub, error) {
	var out []app.Hub
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+hubCols+` FROM wireguard.server s ORDER BY s.created_at, s.id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			h, err := scanHub(rows)
			if err != nil {
				return err
			}
			out = append(out, *h)
		}
		return rows.Err()
	})
	return out, err
}

// UpdateHub implementa app.Store.
func (s *Store) UpdateHub(ctx context.Context, id uuid.UUID, expect int, patch app.HubPatch) (*app.Hub, error) {
	var fields []string
	if patch.Name != nil {
		fields = append(fields, domain.FieldName)
	}
	if patch.Endpoint != nil {
		fields = append(fields, domain.FieldEndpoint)
	}
	if patch.MTU != nil {
		fields = append(fields, domain.FieldMTU)
	}
	if patch.Keepalive != nil {
		fields = append(fields, domain.FieldKeepalive)
	}
	var out *app.Hub
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE wireguard.server SET name = coalesce($3, name), endpoint = coalesce($4, endpoint),
				mtu = coalesce($5, mtu), persistent_keepalive_seconds = coalesce($6, persistent_keepalive_seconds),
				console_overrides = ARRAY(SELECT DISTINCT f FROM unnest(console_overrides || $7::text[]) f ORDER BY f),
				version = version + 1, updated_at = now()
			WHERE id = $1 AND version = $2`, id, expect, patch.Name, patch.Endpoint, patch.MTU, patch.Keepalive, fields)
		if err != nil {
			if pgdb.IsCode(err, pgdb.SQLStateUniqueViolation) {
				return app.ErrHubNameInUse
			}
			return err
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM wireguard.server WHERE id = $1)`, id).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return domain.ErrNotFound
			}
			return app.ErrVersionChanged
		}
		out, err = getHubTx(ctx, tx, id)
		return err
	})
	return out, err
}

// lockHub bloquea la fila del hub: serializa la IPAM (CreatePeer) y los
// cambios de rangos.
func lockHub(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM wireguard.server WHERE id = $1 FOR UPDATE`, id).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

// bumpHub sube la versión del hub (ETag) y la deseada del agente.
func bumpHub(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE wireguard.server SET version = version + 1, desired_version = desired_version + 1,
		updated_at = now() WHERE id = $1`, id)
	return err
}

// AddPool implementa app.Store.
func (s *Store) AddPool(ctx context.Context, hubID uuid.UUID, cidr netip.Prefix) (*app.Pool, error) {
	var out *app.Pool
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		if err := lockHub(ctx, tx, hubID); err != nil {
			return err
		}
		// created_at de la base, como los rangos del entorno: la IPAM los recorre en ese orden.
		p := &app.Pool{ID: uuid.Must(uuid.NewV7()), CIDR: cidr}
		if err := tx.QueryRow(ctx, `INSERT INTO wireguard.ip_pool (id, server_id, cidr, family) VALUES ($1, $2, $3::cidr, $4)
			RETURNING created_at`, p.ID, hubID, cidr.String(), family(cidr)).Scan(&p.CreatedAt); err != nil {
			switch {
			case pgdb.IsCode(err, pgdb.SQLStateExclusionViolation):
				return app.ErrPoolOverlap
			case pgdb.IsCode(err, pgdb.SQLStateCheckViolation):
				return fmt.Errorf("%w: %s", domain.ErrInvalidPool, cidr)
			}
			return err
		}
		if err := bumpHub(ctx, tx, hubID); err != nil {
			return err
		}
		out = p
		return nil
	})
	return out, err
}

// DeletePool implementa app.Store. Las asignaciones liberadas antes de la
// cuarentena se borran con el rango (el historial de direcciones sigue en
// wireguard.peer.address).
func (s *Store) DeletePool(ctx context.Context, hubID, poolID uuid.UUID, quarantineSince time.Time, guard func(app.Pool) error) (*app.Pool, error) {
	var out *app.Pool
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		if err := lockHub(ctx, tx, hubID); err != nil {
			return err
		}
		var (
			p    app.Pool
			cidr string
		)
		err := tx.QueryRow(ctx, `SELECT id, cidr::text, created_at FROM wireguard.ip_pool WHERE id = $1 AND server_id = $2`, poolID, hubID).
			Scan(&p.ID, &cidr, &p.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return app.ErrPoolNotFound
		}
		if err != nil {
			return err
		}
		p.CIDR, _ = netip.ParsePrefix(cidr)
		var pools int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM wireguard.ip_pool WHERE server_id = $1`, hubID).Scan(&pools); err != nil {
			return err
		}
		if pools <= 1 {
			return app.ErrLastPool
		}
		if guard != nil {
			if err := guard(p); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM wireguard.ip_allocation
			WHERE pool_id = $1 AND (released_at IS NULL OR released_at > $2)`, poolID, quarantineSince).Scan(&p.Used); err != nil {
			return err
		}
		if p.Used > 0 {
			return app.ErrPoolInUse
		}
		if _, err := tx.Exec(ctx, `DELETE FROM wireguard.ip_allocation WHERE pool_id = $1`, poolID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM wireguard.ip_pool WHERE id = $1`, poolID); err != nil {
			return err
		}
		if err := bumpHub(ctx, tx, hubID); err != nil {
			return err
		}
		out = &p
		return nil
	})
	return out, err
}

// ------------------------------------------------------------------ peers

const peerCols = `p.id, p.tenant_id, p.server_id, p.router_id, host(p.address), p.public_key, p.status, p.handshake_state,
	p.persistent_keepalive_seconds, p.enrolled_at, host(p.enrolled_from_ip), p.activated_at, p.revoked_at, p.revoked_reason,
	p.last_handshake_at, p.endpoint, p.rx_bytes::text, p.tx_bytes::text, p.created_at, p.updated_at, p.version,
	t.id, t.expires_at, t.used_at, t.revoked_at, t.failed_attempts, t.created_at,
	p.transport, p.tunnel_index, p.gre_key, host(p.remote_address), p.remote_id, p.tunnel_state, p.last_rekey_at, p.last_packet_at,
	p.tunnel_error`

// tokenJoin une el token más reciente del peer (vista Peer.enrollment).
const tokenJoin = ` LEFT JOIN LATERAL (SELECT * FROM wireguard.enrollment_token et WHERE et.tenant_id = p.tenant_id AND et.peer_id = p.id
	ORDER BY et.created_at DESC LIMIT 1) t ON true`

func scanPeer(row pgx.Row) (*domain.Peer, error) {
	var (
		p                  domain.Peer
		addr               string
		from               *string
		rx, tx             string
		tokID              *uuid.UUID
		tokExp, tokCreated *time.Time
		tokUsed, tokRev    *time.Time
		tokFailed          *int
		remote             *string
	)
	err := row.Scan(&p.ID, &p.TenantID, &p.ServerID, &p.RouterID, &addr, &p.PublicKey, &p.Status, &p.HandshakeState, &p.Keepalive,
		&p.EnrolledAt, &from, &p.ActivatedAt, &p.RevokedAt, &p.RevokedReason, &p.LastHandshakeAt, &p.Endpoint, &rx, &tx,
		&p.CreatedAt, &p.UpdatedAt, &p.Version, &tokID, &tokExp, &tokUsed, &tokRev, &tokFailed, &tokCreated,
		&p.Transport, &p.TunnelIndex, &p.GREKey, &remote, &p.RemoteID, &p.TunnelState, &p.LastRekeyAt, &p.LastPacketAt, &p.TunnelError)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	if p.Address, err = netip.ParseAddr(addr); err != nil {
		return nil, fmt.Errorf("wireguard: bad address %q: %w", addr, err)
	}
	if from != nil {
		if a, err := netip.ParseAddr(*from); err == nil {
			p.EnrolledFromIP = &a
		}
	}
	if remote != nil {
		if a, err := netip.ParseAddr(*remote); err == nil {
			p.RemoteAddress = &a
		}
	}
	_, _ = fmt.Sscan(rx, &p.RxBytes)
	_, _ = fmt.Sscan(tx, &p.TxBytes)
	if tokID != nil {
		p.Token = &domain.Token{ID: *tokID, TenantID: p.TenantID, RouterID: p.RouterID, PeerID: p.ID, ExpiresAt: *tokExp,
			UsedAt: tokUsed, RevokedAt: tokRev, FailedAttempts: *tokFailed, CreatedAt: *tokCreated}
	}
	return &p, nil
}

func collectPeers(rows pgx.Rows) ([]domain.Peer, error) {
	defer rows.Close()
	var out []domain.Peer
	for rows.Next() {
		p, err := scanPeer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func getPeerTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (*domain.Peer, error) {
	return scanPeer(tx.QueryRow(ctx, `SELECT `+peerCols+` FROM wireguard.peer p`+tokenJoin+` WHERE p.tenant_id = $1 AND p.id = $2`, tenant, id))
}

// ActivePeer implementa app.Store.
func (s *Store) ActivePeer(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID) (*domain.Peer, error) {
	var out *domain.Peer
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = scanPeer(tx.QueryRow(ctx, `SELECT `+peerCols+` FROM wireguard.peer p`+tokenJoin+`
			WHERE p.tenant_id = $1 AND p.router_id = $2 AND p.revoked_at IS NULL`, t.UUID(), routerID))
		return err
	})
	return out, err
}

// GetPeer implementa app.Store.
func (s *Store) GetPeer(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Peer, error) {
	var out *domain.Peer
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = scanPeer(tx.QueryRow(ctx, `SELECT `+peerCols+` FROM wireguard.peer p`+tokenJoin+`
			WHERE p.tenant_id = $1 AND p.id = $2`, t.UUID(), id))
		return err
	})
	return out, err
}

// ListPeers implementa app.Store (orden por last_handshake_at con NULL primero, id).
func (s *Store) ListPeers(ctx context.Context, t pgdb.TenantID, q app.PeerQuery) ([]domain.Peer, error) {
	var out []domain.Peer
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		conds := []string{"p.tenant_id = $1"}
		args := []any{t.UUID()}
		add := func(c string, v ...any) {
			for _, x := range v {
				args = append(args, x)
				c = strings.Replace(c, "?", fmt.Sprintf("$%d", len(args)), 1)
			}
			conds = append(conds, c)
		}
		if q.RouterID != nil {
			add("p.router_id = ?", *q.RouterID)
		}
		if q.Status != "" {
			add("p.status = ?", q.Status)
		}
		if q.HandshakeState != "" {
			add("p.handshake_state = ?", q.HandshakeState)
		}
		// Clave de orden: epoch (NULL = -infinito) e id.
		key := "coalesce(p.last_handshake_at, '-infinity'::timestamptz)"
		op, dir := ">", "ASC"
		if q.SortDesc {
			op, dir = "<", "DESC"
		}
		if q.AfterKey != nil || q.AfterNil {
			var k any = "-infinity"
			if q.AfterKey != nil {
				k = q.AfterKey.UTC().Format(time.RFC3339Nano)
			}
			add(fmt.Sprintf("(%s, p.id) %s (?::timestamptz, ?::uuid)", key, op), k, q.AfterID)
		}
		args = append(args, q.Limit)
		rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s FROM wireguard.peer p%s WHERE %s ORDER BY %s %s, p.id %s LIMIT $%d`,
			peerCols, tokenJoin, strings.Join(conds, " AND "), key, dir, dir, len(args)), args...)
		if err != nil {
			return err
		}
		out, err = collectPeers(rows)
		return err
	})
	return out, err
}

// AllPeers implementa app.Store.
func (s *Store) AllPeers(ctx context.Context) ([]domain.Peer, error) {
	var out []domain.Peer
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+peerCols+` FROM wireguard.peer p`+tokenJoin+` WHERE p.revoked_at IS NULL ORDER BY p.created_at, p.id`)
		if err != nil {
			return err
		}
		out, err = collectPeers(rows)
		return err
	})
	return out, err
}

// CreatePeer implementa app.Store: IPAM con los pools del hub bloqueados
// (serializa las asignaciones) y unicidad global por índice. Si p.Address
// viene fijada y esa IP la liberó el túnel anterior del MISMO router (cambio
// de transporte), se le vuelve a asignar sin esperar la cuarentena: la
// identidad del exportador no cambia de dueño.
func (s *Store) CreatePeer(ctx context.Context, t pgdb.TenantID, p *domain.Peer, ev app.PeerEvents) error {
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var services string
		// Los peers nuevos heredan el keepalive del hub (editable desde la consola).
		if err := tx.QueryRow(ctx, `SELECT services_cidr::text, persistent_keepalive_seconds FROM wireguard.server WHERE id = $1 FOR UPDATE`,
			p.ServerID).Scan(&services, &p.Keepalive); err != nil {
			return fmt.Errorf("hub %s: %w", p.ServerID, err)
		}
		svc, _ := netip.ParsePrefix(services)
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM wireguard.peer WHERE tenant_id = $1 AND router_id = $2 AND revoked_at IS NULL)`,
			t.UUID(), p.RouterID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return app.ErrPeerExists
		}
		if p.Transport == "" {
			p.Transport = domain.TransportWireGuard
		}
		if p.IsTunnelAgent() {
			var idx int64
			if err := tx.QueryRow(ctx, `SELECT nextval('wireguard.tunnel_index_seq')`).Scan(&idx); err != nil {
				return fmt.Errorf("tunnel index: %w", err)
			}
			p.TunnelIndex = &idx
			if p.GREKey == domain.GREKeyFromIndex {
				p.GREKey = idx
			}
		} else {
			p.TunnelIndex, p.GREKey = nil, 0
		}
		if p.GREKey < 0 {
			p.GREKey = 0
		}
		insert := func(poolID uuid.UUID, addr netip.Addr) error {
			p.Address = addr
			host := netip.PrefixFrom(addr, addr.BitLen()).String()
			var remote *string
			if p.RemoteAddress != nil {
				r := p.RemoteAddress.String()
				remote = &r
			}
			if _, err := tx.Exec(ctx, `INSERT INTO wireguard.peer (id, tenant_id, server_id, kind, router_id, address, status, handshake_state,
					persistent_keepalive_seconds, created_at, updated_at, version, transport, tunnel_index, gre_key, remote_address, remote_id)
				VALUES ($1, $2, $3, 'router', $4, $5::inet, $6, $7, $8, $9, $9, 1, $10, $11, $12, $13::inet, $14)`,
				p.ID, t.UUID(), p.ServerID, p.RouterID, host, p.Status, p.HandshakeState, p.Keepalive, p.CreatedAt,
				p.Transport, p.TunnelIndex, p.GREKey, remote, p.RemoteID); err != nil {
				if pgdb.IsCode(err, pgdb.SQLStateUniqueViolation) {
					return app.ErrPeerExists
				}
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO wireguard.ip_allocation (id, pool_id, tenant_id, address, peer_id, allocated_at)
				VALUES ($1, $2, $3, $4::inet, $5, $6)`, uuid.Must(uuid.NewV7()), poolID, t.UUID(), host, p.ID, p.CreatedAt); err != nil {
				return err
			}
			return emit(ctx, tx, ev(p))
		}
		if p.Address.IsValid() {
			// Última asignación de esa IP: liberada por un túnel del mismo router.
			var poolID uuid.UUID
			err := tx.QueryRow(ctx, `SELECT a.pool_id FROM wireguard.ip_allocation a
					JOIN wireguard.peer o ON o.id = a.peer_id AND o.tenant_id = a.tenant_id
					JOIN wireguard.ip_pool pl ON pl.id = a.pool_id AND pl.server_id = $4
				WHERE a.address = $1::inet AND a.tenant_id = $2 AND o.router_id = $3 AND a.released_at IS NOT NULL
				  AND NOT EXISTS (SELECT 1 FROM wireguard.ip_allocation b WHERE b.address = a.address
				                  AND (b.released_at IS NULL OR b.released_at > a.released_at))
				ORDER BY a.released_at DESC LIMIT 1`,
				netip.PrefixFrom(p.Address, p.Address.BitLen()).String(), t.UUID(), p.RouterID, p.ServerID).Scan(&poolID)
			switch {
			case err == nil:
				return insert(poolID, p.Address)
			case !errors.Is(err, pgx.ErrNoRows):
				return err
			}
		}
		rows, err := tx.Query(ctx, `SELECT id, cidr::text FROM wireguard.ip_pool WHERE server_id = $1 AND family = 4 ORDER BY created_at, id`, p.ServerID)
		if err != nil {
			return err
		}
		type pool struct {
			id   uuid.UUID
			cidr netip.Prefix
		}
		var pools []pool
		for rows.Next() {
			var (
				pl pool
				c  string
			)
			if err := rows.Scan(&pl.id, &c); err != nil {
				rows.Close()
				return err
			}
			pl.cidr, _ = netip.ParsePrefix(c)
			pools = append(pools, pl)
		}
		rows.Close()
		for _, pl := range pools {
			used, err := usedAddrs(ctx, tx, pl.id, p.CreatedAt.Add(-Quarantine))
			if err != nil {
				return err
			}
			addr, err := domain.NextFree(pl.cidr, svc, used)
			if errors.Is(err, domain.ErrPoolExhausted) {
				continue
			}
			if err != nil {
				return err
			}
			return insert(pl.id, addr)
		}
		return domain.ErrPoolExhausted
	})
}

// RevokePeer implementa app.Store.
func (s *Store) RevokePeer(ctx context.Context, t pgdb.TenantID, id uuid.UUID, reason string, at time.Time, ev app.PeerEvents) error {
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE wireguard.peer SET status = 'revoked', revoked_at = $3, revoked_reason = $4,
				updated_at = $3, version = version + 1
			WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL`, t.UUID(), id, at, reason)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		p, err := getPeerTx(ctx, tx, t.UUID(), id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE wireguard.ip_allocation SET released_at = $3 WHERE tenant_id = $1 AND peer_id = $2 AND released_at IS NULL`,
			t.UUID(), id, at); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE wireguard.enrollment_token SET revoked_at = $3
			WHERE tenant_id = $1 AND peer_id = $2 AND used_at IS NULL AND revoked_at IS NULL`, t.UUID(), id, at); err != nil {
			return err
		}
		if p.PublicKey != nil {
			if _, err := tx.Exec(ctx, `UPDATE wireguard.server SET desired_version = desired_version + 1, updated_at = now() WHERE id = $1`,
				p.ServerID); err != nil {
				return err
			}
		}
		if p.IsTunnelAgent() {
			if err := bumpTunnels(ctx, tx, p.ServerID); err != nil {
				return err
			}
		}
		return emit(ctx, tx, ev(p))
	})
}

func bumpTunnels(ctx context.Context, tx pgx.Tx, hubID uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE wireguard.server SET tunnel_desired_version = tunnel_desired_version + 1, updated_at = now() WHERE id = $1`, hubID)
	return err
}

// ------------------------------------------------------------------ tokens

// IssueToken implementa app.Store.
func (s *Store) IssueToken(ctx context.Context, t pgdb.TenantID, tok *domain.Token) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE wireguard.enrollment_token SET revoked_at = $3
			WHERE tenant_id = $1 AND router_id = $2 AND used_at IS NULL AND revoked_at IS NULL`, t.UUID(), tok.RouterID, tok.CreatedAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO wireguard.enrollment_token (id, tenant_id, router_id, peer_id, token_hash, expires_at, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, tok.ID, t.UUID(), tok.RouterID, tok.PeerID, tok.Hash, tok.ExpiresAt, tok.CreatedAt)
		return err
	})
}

// RevokeToken implementa app.Store.
func (s *Store) RevokeToken(ctx context.Context, t pgdb.TenantID, id uuid.UUID, at time.Time) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE wireguard.enrollment_token SET revoked_at = $3
			WHERE tenant_id = $1 AND id = $2 AND used_at IS NULL AND revoked_at IS NULL AND expires_at > $3`, t.UUID(), id, at)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		return nil
	})
}

// TokenHash implementa app.Store.
func (s *Store) TokenHash(ctx context.Context, t pgdb.TenantID, id uuid.UUID) ([]byte, error) {
	var hash []byte
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT token_hash FROM wireguard.enrollment_token WHERE tenant_id = $1 AND id = $2`, t.UUID(), id).Scan(&hash)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return hash, err
}

var errFailed = errors.New("enroll failed")

// Enroll implementa app.Store.
func (s *Store) Enroll(ctx context.Context, hash []byte, publicKey string, from netip.Addr, at time.Time,
	ev func(p *domain.Peer, tok *domain.Token) []outbox.Event,
) (*domain.Peer, error) {
	var (
		out     *domain.Peer
		outcome error
	)
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var tok domain.Token
		err := tx.QueryRow(ctx, `SELECT id, tenant_id, router_id, peer_id, expires_at, used_at, revoked_at, failed_attempts, created_at
			FROM wireguard.enrollment_token WHERE token_hash = $1 FOR UPDATE`, hash).Scan(&tok.ID, &tok.TenantID, &tok.RouterID, &tok.PeerID,
			&tok.ExpiresAt, &tok.UsedAt, &tok.RevokedAt, &tok.FailedAttempts, &tok.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			outcome = app.ErrTokenInvalid
			return nil
		}
		if err != nil {
			return err
		}
		if !tok.Usable(at) {
			outcome = app.ErrTokenInvalid
			return nil
		}
		fail := func(e error) error {
			_, err := tx.Exec(ctx, `UPDATE wireguard.enrollment_token SET failed_attempts = failed_attempts + 1,
				revoked_at = CASE WHEN failed_attempts + 1 >= $2 THEN $3 ELSE revoked_at END WHERE id = $1`, tok.ID, domain.MaxTokenFailures, at)
			outcome = e
			return err // commit del contador
		}
		if !domain.ValidPublicKey(publicKey) {
			return fail(app.ErrBadKey)
		}
		var inUse bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM wireguard.peer WHERE public_key = $1 AND revoked_at IS NULL AND id <> $2)`,
			publicKey, tok.PeerID).Scan(&inUse); err != nil {
			return err
		}
		if inUse {
			return fail(app.ErrKeyInUse)
		}
		var fromIP *string
		if from.IsValid() {
			v := from.String()
			fromIP = &v
		}
		// Un script nuevo lleva el keepalive actual del hub: el peer lo adopta al enrolar.
		tag, err := tx.Exec(ctx, `UPDATE wireguard.peer SET public_key = $3, status = 'pending_handshake', handshake_state = 'never',
				last_handshake_at = NULL, activated_at = NULL, enrolled_at = $4, enrolled_from_ip = $5::inet, updated_at = $4, version = version + 1,
				persistent_keepalive_seconds = (SELECT s.persistent_keepalive_seconds FROM wireguard.server s WHERE s.id = peer.server_id)
			WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL`, tok.TenantID, tok.PeerID, publicKey, at, fromIP)
		if err != nil {
			if pgdb.IsCode(err, pgdb.SQLStateUniqueViolation) {
				return errFailed
			}
			return err
		}
		if tag.RowsAffected() == 0 {
			outcome = app.ErrTokenInvalid
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE wireguard.enrollment_token SET used_at = $2 WHERE id = $1`, tok.ID, at); err != nil {
			return err
		}
		p, err := getPeerTx(ctx, tx, tok.TenantID, tok.PeerID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE wireguard.server SET desired_version = desired_version + 1, updated_at = now() WHERE id = $1`,
			p.ServerID); err != nil {
			return err
		}
		tok.UsedAt = &at
		p.Token = &tok
		out = p
		return emit(ctx, tx, ev(p, &tok))
	})
	if errors.Is(err, errFailed) {
		return nil, app.ErrKeyInUse // carrera con otro enrolamiento simultáneo
	}
	if err != nil {
		return nil, err
	}
	if outcome != nil {
		return nil, outcome
	}
	return out, nil
}

// ------------------------------------------------------------------ estado del hub

// DesiredState implementa app.Store.
func (s *Store) DesiredState(ctx context.Context, hubID uuid.UUID) (int64, []domain.Peer, error) {
	var (
		version int64
		peers   []domain.Peer
	)
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT desired_version FROM wireguard.server WHERE id = $1`, hubID).Scan(&version); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+peerCols+` FROM wireguard.peer p`+tokenJoin+`
			WHERE p.server_id = $1 AND p.revoked_at IS NULL AND p.transport = 'wireguard' AND p.public_key IS NOT NULL ORDER BY p.address`, hubID)
		if err != nil {
			return err
		}
		if peers, err = collectPeers(rows); err != nil {
			return err
		}
		return attachRouted(ctx, tx, hubID, peers)
	})
	return version, peers, err
}

// ApplyReport implementa app.Store.
func (s *Store) ApplyReport(ctx context.Context, hubID uuid.UUID, applied int64, up bool, at time.Time, obs []app.Observed,
	ev func(tr app.Transition) []outbox.Event,
) (int64, []app.Transition, error) {
	var (
		desired int64
		out     []app.Transition
	)
	byKey := make(map[string]app.Observed, len(obs))
	for _, o := range obs {
		byKey[o.PublicKey] = o
	}
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		status := "up"
		if !up {
			status = "down"
		}
		if err := tx.QueryRow(ctx, `UPDATE wireguard.server SET applied_version = $2, status = $3, last_report_at = $4, updated_at = now()
			WHERE id = $1 RETURNING desired_version`, hubID, applied, status, at).Scan(&desired); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+peerCols+` FROM wireguard.peer p`+tokenJoin+`
			WHERE p.server_id = $1 AND p.revoked_at IS NULL AND p.transport = 'wireguard' AND p.public_key IS NOT NULL FOR UPDATE OF p`, hubID)
		if err != nil {
			return err
		}
		peers, err := collectPeers(rows)
		if err != nil {
			return err
		}
		for i := range peers {
			p := peers[i]
			o, seen := byKey[*p.PublicKey]
			if !seen && !up {
				continue // interfaz caída: no se infiere nada del túnel
			}
			if seen {
				if o.LastHandshake != nil {
					hs := *o.LastHandshake
					p.LastHandshakeAt = &hs
				}
				p.Endpoint, p.RxBytes, p.TxBytes = o.Endpoint, o.RxBytes, o.TxBytes
			}
			var tr app.Transition
			// Un handshake anterior al enrolamiento es de una clave vieja.
			fresh := p.LastHandshakeAt != nil && (p.EnrolledAt == nil || !p.LastHandshakeAt.Before(p.EnrolledAt.Add(-time.Second)))
			if p.Status == domain.StatusPendingHandshake && fresh {
				p.Status, p.ActivatedAt, tr.Activated = domain.StatusActive, &at, true
			}
			state := domain.HandshakeStateAt(p.LastHandshakeAt, at)
			if !fresh {
				state = domain.HandshakeNever
			}
			switch {
			case p.HandshakeState == domain.HandshakeOK && state == domain.HandshakeStale:
				tr.Handshake = "stale"
			case p.HandshakeState == domain.HandshakeStale && state == domain.HandshakeOK:
				tr.Handshake = "recovered"
			}
			p.HandshakeState = state
			version := p.Version
			if tr.Activated {
				version++
			}
			if _, err := tx.Exec(ctx, `UPDATE wireguard.peer SET last_handshake_at = $3, endpoint = $4, rx_bytes = $5::numeric, tx_bytes = $6::numeric,
					status = $7, activated_at = $8, handshake_state = $9, version = $10, updated_at = CASE WHEN $10 <> version THEN $11 ELSE updated_at END
				WHERE tenant_id = $1 AND id = $2`, p.TenantID, p.ID, p.LastHandshakeAt, p.Endpoint, fmt.Sprint(p.RxBytes), fmt.Sprint(p.TxBytes),
				p.Status, p.ActivatedAt, p.HandshakeState, version, at); err != nil {
				return err
			}
			p.Version = version
			if tr.Activated || tr.Handshake != "" {
				tr.Peer = p
				if err := emit(ctx, tx, ev(tr)); err != nil {
					return err
				}
				out = append(out, tr)
			}
		}
		return nil
	})
	return desired, out, err
}

// ------------------------------------------------------------------ túneles ipsec/gre (tunnel-agent)

// MarkTunnelConfigured implementa app.Store: la configuración del router se
// generó (y, en ipsec, con una PSK nueva): awaiting_enrollment →
// pending_handshake y la versión deseada de tunnel-agent sube (la PSK nueva
// se empuja al demonio IKE).
func (s *Store) MarkTunnelConfigured(ctx context.Context, t pgdb.TenantID, id uuid.UUID, at time.Time, ev app.PeerEvents) (*domain.Peer, error) {
	var out *domain.Peer
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var hub uuid.UUID
		var moved bool
		err := tx.QueryRow(ctx, `UPDATE wireguard.peer SET
				status = CASE WHEN status = 'awaiting_enrollment' THEN 'pending_handshake' ELSE status END,
				enrolled_at = CASE WHEN status = 'awaiting_enrollment' THEN $3 ELSE enrolled_at END,
				version = CASE WHEN status = 'awaiting_enrollment' THEN version + 1 ELSE version END,
				updated_at = $3
			WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL AND transport IN ('ipsec', 'gre')
			RETURNING server_id, enrolled_at = $3`, t.UUID(), id, at).Scan(&hub, &moved)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := bumpTunnels(ctx, tx, hub); err != nil {
			return err
		}
		if out, err = getPeerTx(ctx, tx, t.UUID(), id); err != nil {
			return err
		}
		if !moved {
			return nil
		}
		return emit(ctx, tx, ev(out))
	})
	return out, err
}

// TunnelDesiredState implementa app.Store: túneles ipsec/gre configurados
// (pending_handshake o active) del hub.
func (s *Store) TunnelDesiredState(ctx context.Context, hubID uuid.UUID) (int64, []domain.Peer, error) {
	var (
		version int64
		peers   []domain.Peer
	)
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT tunnel_desired_version FROM wireguard.server WHERE id = $1`, hubID).Scan(&version); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+peerCols+` FROM wireguard.peer p`+tokenJoin+`
			WHERE p.server_id = $1 AND p.revoked_at IS NULL AND p.transport IN ('ipsec', 'gre')
			  AND p.status IN ('pending_handshake', 'active') ORDER BY p.tunnel_index`, hubID)
		if err != nil {
			return err
		}
		if peers, err = collectPeers(rows); err != nil {
			return err
		}
		return attachRouted(ctx, tx, hubID, peers)
	})
	return version, peers, err
}

// ApplyTunnelReport implementa app.Store: guarda el estado observado de los
// túneles ipsec/gre y devuelve las transiciones (activación y vida
// stale/recovered con el mismo umbral que WireGuard), emitiendo sus eventos.
func (s *Store) ApplyTunnelReport(ctx context.Context, hubID uuid.UUID, applied int64, ikeUp bool, at time.Time, obs []app.TunnelObserved,
	ev func(tr app.Transition) []outbox.Event,
) (int64, []app.Transition, error) {
	var (
		desired int64
		out     []app.Transition
	)
	byID := make(map[uuid.UUID]app.TunnelObserved, len(obs))
	for _, o := range obs {
		byID[o.PeerID] = o
	}
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		status := "up"
		if !ikeUp {
			status = "degraded" // GRE sigue; IPsec sin demonio IKE
		}
		if err := tx.QueryRow(ctx, `UPDATE wireguard.server SET tunnel_applied_version = $2, tunnel_status = $3, tunnel_last_report_at = $4,
				updated_at = now()
			WHERE id = $1 RETURNING tunnel_desired_version`, hubID, applied, status, at).Scan(&desired); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+peerCols+` FROM wireguard.peer p`+tokenJoin+`
			WHERE p.server_id = $1 AND p.revoked_at IS NULL AND p.transport IN ('ipsec', 'gre')
			  AND p.status IN ('pending_handshake', 'active') FOR UPDATE OF p`, hubID)
		if err != nil {
			return err
		}
		peers, err := collectPeers(rows)
		if err != nil {
			return err
		}
		for i := range peers {
			p := peers[i]
			o, seen := byID[p.ID]
			if !seen || (p.Transport == domain.TransportIPsec && !ikeUp) {
				continue // el agente aún no lo tiene, o sin demonio IKE no se infiere nada
			}
			if o.Alive {
				live := at
				p.LastHandshakeAt = &live
			}
			p.TunnelState, p.TunnelError, p.Endpoint = strOrNil(o.State), o.Error, o.Endpoint
			if o.LastRekeyAt != nil {
				p.LastRekeyAt = o.LastRekeyAt
			}
			if o.LastPacketAt != nil {
				p.LastPacketAt = o.LastPacketAt
			}
			p.RxBytes, p.TxBytes = o.RxBytes, o.TxBytes
			var tr app.Transition
			fresh := p.LastHandshakeAt != nil && (p.EnrolledAt == nil || !p.LastHandshakeAt.Before(p.EnrolledAt.Add(-time.Second)))
			if p.Status == domain.StatusPendingHandshake && fresh && o.Alive {
				p.Status, p.ActivatedAt, tr.Activated = domain.StatusActive, &at, true
			}
			state := domain.HandshakeStateAt(p.LastHandshakeAt, at)
			if !fresh {
				state = domain.HandshakeNever
			}
			switch {
			case p.HandshakeState == domain.HandshakeOK && state == domain.HandshakeStale:
				tr.Handshake = "stale"
			case p.HandshakeState == domain.HandshakeStale && state == domain.HandshakeOK:
				tr.Handshake = "recovered"
			}
			p.HandshakeState = state
			version := p.Version
			if tr.Activated {
				version++
			}
			if _, err := tx.Exec(ctx, `UPDATE wireguard.peer SET last_handshake_at = $3, endpoint = $4, rx_bytes = $5::numeric, tx_bytes = $6::numeric,
					status = $7, activated_at = $8, handshake_state = $9, version = $10, tunnel_state = $11, last_rekey_at = $12,
					last_packet_at = $13, tunnel_error = $14, updated_at = CASE WHEN $10 <> version THEN $15 ELSE updated_at END
				WHERE tenant_id = $1 AND id = $2`, p.TenantID, p.ID, p.LastHandshakeAt, p.Endpoint, fmt.Sprint(p.RxBytes), fmt.Sprint(p.TxBytes),
				p.Status, p.ActivatedAt, p.HandshakeState, version, p.TunnelState, p.LastRekeyAt, p.LastPacketAt, p.TunnelError, at); err != nil {
				return err
			}
			p.Version = version
			if tr.Activated || tr.Handshake != "" {
				tr.Peer = p
				if err := emit(ctx, tx, ev(tr)); err != nil {
					return err
				}
				out = append(out, tr)
			}
		}
		return nil
	})
	return desired, out, err
}

func strOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
