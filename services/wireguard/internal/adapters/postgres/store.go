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
const Quarantine = 24 * time.Hour

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

// EnsureHub implementa app.Store.
func (s *Store) EnsureHub(ctx context.Context, h app.Hub) error {
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO wireguard.server (id, name, endpoint, listen_port, public_key, services_cidr)
			VALUES ($1, $2, $3, $4, nullif($5, ''), $6::cidr)
			ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, endpoint = EXCLUDED.endpoint, listen_port = EXCLUDED.listen_port,
				public_key = coalesce(EXCLUDED.public_key, wireguard.server.public_key), services_cidr = EXCLUDED.services_cidr, updated_at = now()`,
			h.ID, h.Name, h.Endpoint, h.ListenPort, h.PublicKey, h.ServicesCIDR.String())
		if err != nil {
			return fmt.Errorf("ensure hub: %w", err)
		}
		for _, p := range h.Pools {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM wireguard.ip_pool WHERE server_id = $1 AND cidr = $2::cidr)`,
				h.ID, p.String()).Scan(&exists); err != nil {
				return err
			}
			if exists {
				continue
			}
			if _, err := tx.Exec(ctx, `INSERT INTO wireguard.ip_pool (id, server_id, cidr) VALUES ($1, $2, $3::cidr)`,
				uuid.Must(uuid.NewV7()), h.ID, p.String()); err != nil {
				if pgdb.IsCode(err, pgdb.SQLStateExclusionViolation) {
					return fmt.Errorf("%w: %s overlaps an existing tunnel range", domain.ErrInvalidPool, p)
				}
				return fmt.Errorf("ensure pool %s: %w", p, err)
			}
		}
		return nil
	})
}

func parsePrefixes(ss []string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(ss))
	for _, s := range ss {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p)
		}
	}
	return out
}

const hubCols = `s.id, s.name, s.endpoint, s.listen_port, coalesce(s.public_key, ''), s.services_cidr::text, s.status, s.desired_version,
	s.applied_version, s.last_report_at,
	coalesce((SELECT array_agg(p.cidr::text ORDER BY p.created_at) FROM wireguard.ip_pool p WHERE p.server_id = s.id), '{}'),
	(SELECT count(*) FROM wireguard.ip_allocation a JOIN wireguard.ip_pool p ON p.id = a.pool_id WHERE p.server_id = s.id AND a.released_at IS NULL),
	(SELECT count(*) FROM wireguard.peer pe WHERE pe.server_id = s.id AND pe.status = 'active'),
	(SELECT count(DISTINCT pe.tenant_id) FROM wireguard.peer pe WHERE pe.server_id = s.id AND pe.revoked_at IS NULL)`

func scanHub(row pgx.Row) (*app.Hub, error) {
	var (
		h        app.Hub
		services string
		pools    []string
	)
	if err := row.Scan(&h.ID, &h.Name, &h.Endpoint, &h.ListenPort, &h.PublicKey, &services, &h.Status, &h.DesiredVersion,
		&h.AppliedVersion, &h.LastReportAt, &pools, &h.AddressesUsed, &h.PeersActive, &h.Tenants); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	h.ServicesCIDR, _ = netip.ParsePrefix(services)
	h.Pools = parsePrefixes(pools)
	return &h, nil
}

// GetHub implementa app.Store.
func (s *Store) GetHub(ctx context.Context, id uuid.UUID) (*app.Hub, error) {
	var h *app.Hub
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var err error
		h, err = scanHub(tx.QueryRow(ctx, `SELECT `+hubCols+` FROM wireguard.server s WHERE s.id = $1`, id))
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

// ------------------------------------------------------------------ peers

const peerCols = `p.id, p.tenant_id, p.server_id, p.router_id, host(p.address), p.public_key, p.status, p.handshake_state,
	p.persistent_keepalive_seconds, p.enrolled_at, host(p.enrolled_from_ip), p.activated_at, p.revoked_at, p.revoked_reason,
	p.last_handshake_at, p.endpoint, p.rx_bytes::text, p.tx_bytes::text, p.created_at, p.updated_at, p.version,
	t.id, t.expires_at, t.used_at, t.revoked_at, t.failed_attempts, t.created_at`

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
	)
	err := row.Scan(&p.ID, &p.TenantID, &p.ServerID, &p.RouterID, &addr, &p.PublicKey, &p.Status, &p.HandshakeState, &p.Keepalive,
		&p.EnrolledAt, &from, &p.ActivatedAt, &p.RevokedAt, &p.RevokedReason, &p.LastHandshakeAt, &p.Endpoint, &rx, &tx,
		&p.CreatedAt, &p.UpdatedAt, &p.Version, &tokID, &tokExp, &tokUsed, &tokRev, &tokFailed, &tokCreated)
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
// (serializa las asignaciones) y unicidad global por índice.
func (s *Store) CreatePeer(ctx context.Context, t pgdb.TenantID, p *domain.Peer, ev app.PeerEvents) error {
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var services string
		if err := tx.QueryRow(ctx, `SELECT services_cidr::text FROM wireguard.server WHERE id = $1 FOR UPDATE`, p.ServerID).Scan(&services); err != nil {
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
		rows, err := tx.Query(ctx, `SELECT id, cidr::text FROM wireguard.ip_pool WHERE server_id = $1 ORDER BY created_at, id`, p.ServerID)
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
			used := map[netip.Addr]bool{}
			urows, err := tx.Query(ctx, `SELECT host(address) FROM wireguard.ip_allocation
				WHERE pool_id = $1 AND (released_at IS NULL OR released_at > $2)`, pl.id, p.CreatedAt.Add(-Quarantine))
			if err != nil {
				return err
			}
			for urows.Next() {
				var a string
				if err := urows.Scan(&a); err != nil {
					urows.Close()
					return err
				}
				if ad, err := netip.ParseAddr(a); err == nil {
					used[ad] = true
				}
			}
			urows.Close()
			addr, err := domain.NextFree(pl.cidr, svc, used)
			if errors.Is(err, domain.ErrPoolExhausted) {
				continue
			}
			if err != nil {
				return err
			}
			p.Address = addr
			if _, err := tx.Exec(ctx, `INSERT INTO wireguard.peer (id, tenant_id, server_id, kind, router_id, address, status, handshake_state,
					persistent_keepalive_seconds, created_at, updated_at, version)
				VALUES ($1, $2, $3, 'router', $4, $5::inet, $6, $7, $8, $9, $9, 1)`,
				p.ID, t.UUID(), p.ServerID, p.RouterID, netip.PrefixFrom(addr, addr.BitLen()).String(), p.Status, p.HandshakeState,
				p.Keepalive, p.CreatedAt); err != nil {
				if pgdb.IsCode(err, pgdb.SQLStateUniqueViolation) {
					return app.ErrPeerExists
				}
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO wireguard.ip_allocation (id, pool_id, tenant_id, address, peer_id, allocated_at)
				VALUES ($1, $2, $3, $4::inet, $5, $6)`, uuid.Must(uuid.NewV7()), pl.id, t.UUID(),
				netip.PrefixFrom(addr, addr.BitLen()).String(), p.ID, p.CreatedAt); err != nil {
				return err
			}
			return emit(ctx, tx, ev(p))
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
		return emit(ctx, tx, ev(p))
	})
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
		tag, err := tx.Exec(ctx, `UPDATE wireguard.peer SET public_key = $3, status = 'pending_handshake', handshake_state = 'never',
				last_handshake_at = NULL, activated_at = NULL, enrolled_at = $4, enrolled_from_ip = $5::inet, updated_at = $4, version = version + 1
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
			WHERE p.server_id = $1 AND p.revoked_at IS NULL AND p.public_key IS NOT NULL ORDER BY p.address`, hubID)
		if err != nil {
			return err
		}
		peers, err = collectPeers(rows)
		return err
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
			WHERE p.server_id = $1 AND p.revoked_at IS NULL AND p.public_key IS NOT NULL FOR UPDATE OF p`, hubID)
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
