package app

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/platformevents"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	wgapi "github.com/hcdestroyer/horus-flow/services/wgagent/api"
	"github.com/hcdestroyer/horus-flow/services/wgagent/api/agentv1"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/domain"
)

var _ wgapi.Control = (*Service)(nil)

func errAttr(err error) slog.Attr { return slog.String("error", err.Error()) }

func devTunnel(p *domain.Peer) devapi.TunnelUpdate {
	return devapi.TunnelUpdate{PeerID: p.ID, Address: p.Address, OnboardingState: onboardingFor(p.Status)}
}

func desiredRequest(hub uuid.UUID, version int64, peers []domain.Peer) *agentv1.ApplyDesiredStateRequest {
	req := &agentv1.ApplyDesiredStateRequest{HubId: hub.String(), DesiredVersion: version}
	for _, p := range peers {
		if p.PublicKey == nil {
			continue
		}
		req.Peers = append(req.Peers, &agentv1.DesiredPeer{PeerId: p.ID.String(), TenantId: p.TenantID.String(), PublicKey: *p.PublicKey,
			AllowedIps: []string{p.AllowedIP()}, PersistentKeepaliveSeconds: int32(p.Keepalive)}) //nolint:gosec // 25 s
	}
	return req
}

// ReportStatus implementa api.Control (wg-agent cada 15 s): guarda
// handshakes y contadores, activa los peers con su primer handshake,
// detecta túneles caídos/recuperados y, si el agente va por detrás (p. ej.
// tras reiniciar: applied_version = 0), le reenvía el estado completo.
func (s *Service) ReportStatus(ctx context.Context, req *agentv1.ReportStatusRequest) (*agentv1.ReportStatusResponse, error) {
	if req.GetHubId() != "" && req.GetHubId() != s.o.Hub.ID.String() {
		return nil, errors.New("wireguard: report for an unknown hub")
	}
	now := s.now()
	obs := make([]Observed, 0, len(req.GetPeers()))
	for _, p := range req.GetPeers() {
		o := Observed{PublicKey: p.GetPublicKey(), RxBytes: p.GetRxBytes(), TxBytes: p.GetTxBytes()}
		if p.Endpoint != nil {
			ep := p.GetEndpoint()
			o.Endpoint = &ep
		}
		if p.GetLastHandshakeAt() != nil && p.GetLastHandshakeAt().AsTime().Unix() > 0 {
			hs := p.GetLastHandshakeAt().AsTime().UTC()
			o.LastHandshake = &hs
		}
		obs = append(obs, o)
	}
	desired, trs, err := s.o.Store.ApplyReport(ctx, s.o.Hub.ID, req.GetAppliedVersion(), req.GetInterfaceUp(), now, obs,
		func(tr Transition) []outbox.Event {
			var evs []outbox.Event
			p := tr.Peer
			if tr.Activated {
				evs = append(evs, peerEvent("horus.wireguard.peer.activated", &p, svcActor, now, PeerEventData(&p, []string{"status"})))
			}
			if tr.Handshake != "" {
				var last any
				if p.LastHandshakeAt != nil {
					last = p.LastHandshakeAt.UTC().Format(outbox.TimeFormat)
				}
				evs = append(evs, peerEvent("horus.wireguard.peer.handshake_"+tr.Handshake, &p, svcActor, now, map[string]any{
					"id": p.ID.String(), "server_id": p.ServerID.String(), "router_id": p.RouterID.String(), "last_handshake_at": last,
					"stale_after_seconds": int(domain.StaleAfter / time.Second), "detected_at": now.Format(outbox.TimeFormat),
				}))
			}
			return evs
		})
	if err != nil {
		return nil, err
	}
	if d, ok := s.o.Devices(); ok {
		for _, tr := range trs {
			if tr.Activated {
				p := tr.Peer
				if err := d.SetTunnel(ctx, p.TenantID, p.RouterID, devTunnel(&p)); err != nil {
					s.o.Logger.WarnContext(ctx, "project tunnel_up on router failed", errAttr(err))
				}
			}
		}
	}
	if req.GetAppliedVersion() < desired {
		s.mu.Lock()
		s.lastPush = 0 // fuerza el reenvío
		noted := s.agentZeroNoted
		s.agentZeroNoted = req.GetAppliedVersion() == 0
		s.mu.Unlock()
		if req.GetAppliedVersion() == 0 && !noted {
			// applied_version = 0: el agente arrancó (o se reinició) sin estado;
			// queda en el registro de plataforma y se le reenvía todo.
			platformevents.Emit(ctx, platformevents.Event{Kind: platformevents.KindAgentRestarted, Severity: platformevents.SeverityWarn,
				Role: "wireguard", Message: "wg-agent reported no applied state (started or restarted); full desired state re-sent",
				Details: map[string]any{"hub_id": s.o.Hub.ID.String(), "desired_version": desired, "interface_up": req.GetInterfaceUp(),
					"observed_peers": len(req.GetPeers())}})
		}
		s.Kick()
	} else {
		s.mu.Lock()
		s.agentZeroNoted = false
		s.mu.Unlock()
	}
	return &agentv1.ReportStatusResponse{DesiredVersion: desired}, nil
}

// ---------------------------------------------------------------- lectura

// PeerPage es una página de peers.
type PeerPage struct {
	Data []domain.Peer
	Page pagination.Page
}

// ListPeers lista los peers del ISP (alcance por nodo: los de routers de
// nodos fuera del alcance no se muestran).
func (s *Service) ListPeers(ctx context.Context, qv url.Values, cursor *pagination.Codec) (*PeerPage, error) {
	p, t, err := scope(ctx, "wireguard.read")
	if err != nil {
		return nil, err
	}
	req, err := pagination.ParseRequest(qv)
	if err != nil {
		return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
	}
	q := PeerQuery{Status: qv.Get("status"), HandshakeState: qv.Get("handshake_state"), Limit: req.Limit + 1}
	switch qv.Get("sort") {
	case "", "last_handshake_at":
	case "-last_handshake_at":
		q.SortDesc = true
	default:
		return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidSortField, "")
	}
	if q.Status != "" && !slices.Contains([]string{domain.StatusAwaitingEnrollment, domain.StatusPendingHandshake, domain.StatusActive, domain.StatusRevoked}, q.Status) {
		return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "status")
	}
	if q.HandshakeState != "" && !slices.Contains([]string{domain.HandshakeNever, domain.HandshakeOK, domain.HandshakeStale}, q.HandshakeState) {
		return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "handshake_state")
	}
	if v := qv.Get("router_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "router_id")
		}
		q.RouterID = &id
	}
	fk := pagination.FilterKey(qv.Get("status"), qv.Get("handshake_state"), qv.Get("router_id"))
	sortKey := "last_handshake_at" + map[bool]string{true: "-", false: ""}[q.SortDesc]
	if req.Cursor != "" {
		cur, err := cursor.Decode(req.Cursor, sortKey, fk, t.String())
		if err != nil || len(cur.Keys) != 2 {
			return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		id, err := uuid.Parse(cur.Keys[1])
		if err != nil {
			return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		q.AfterID = id
		if cur.Keys[0] == "" {
			q.AfterNil = true
		} else if ts, err := time.Parse(time.RFC3339Nano, cur.Keys[0]); err == nil {
			q.AfterKey = &ts
		} else {
			return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
	}
	rows, err := s.o.Store.ListPeers(ctx, t, q)
	if err != nil {
		return nil, err
	}
	rows, err = s.filterScope(ctx, p, t, "wireguard.read", rows)
	if err != nil {
		return nil, err
	}
	page := pagination.Page{Limit: req.Limit}
	if len(rows) > req.Limit {
		rows = rows[:req.Limit]
		last := rows[len(rows)-1]
		key := ""
		if last.LastHandshakeAt != nil {
			key = last.LastHandshakeAt.UTC().Format(time.RFC3339Nano)
		}
		next := cursor.Encode(pagination.Cursor{Keys: []string{key, last.ID.String()}, Sort: sortKey, Filter: fk, Tenant: t.String()})
		page.NextCursor, page.HasMore = &next, true
	}
	if rows == nil {
		rows = []domain.Peer{}
	}
	return &PeerPage{Data: rows, Page: page}, nil
}

// filterScope quita los peers de routers fuera del alcance por nodo.
func (s *Service) filterScope(ctx context.Context, p *authz.Principal, t pgdb.TenantID, perm string, rows []domain.Peer) ([]domain.Peer, error) {
	if p.TenantWide(perm) {
		return rows, nil
	}
	d, err := s.devices()
	if err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, r := range rows {
		info, err := d.GetRouter(ctx, t.UUID(), r.RouterID)
		if err == nil && p.HasScope(perm, "site:"+info.SiteID.String()) {
			out = append(out, r)
		}
	}
	return out, nil
}

// GetPeer devuelve un peer del ISP.
func (s *Service) GetPeer(ctx context.Context, id uuid.UUID) (*domain.Peer, error) {
	p, t, err := scope(ctx, "wireguard.read")
	if err != nil {
		return nil, err
	}
	peer, err := s.o.Store.GetPeer(ctx, t, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, apperr.NotFound(problem.CodeNotFound)
		}
		return nil, err
	}
	rows, err := s.filterScope(ctx, p, t, "wireguard.read", []domain.Peer{*peer})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, apperr.NotFound(problem.CodeNotFound)
	}
	return peer, nil
}

// ListHubs lista los hubs con su ocupación (plataforma).
func (s *Service) ListHubs(ctx context.Context) ([]Hub, error) {
	hubs, err := s.o.Store.ListHubs(ctx)
	if err != nil {
		return nil, err
	}
	for i := range hubs {
		if hubs[i].PublicKey == "" && hubs[i].ID == s.o.Hub.ID {
			hubs[i].PublicKey = s.o.HubPublicKey()
		}
		hubs[i].AddressesTotal = 0
		for _, pool := range hubs[i].Pools {
			hubs[i].AddressesTotal += domain.Capacity(pool, hubs[i].ServicesCIDR)
		}
	}
	return hubs, nil
}

// HandshakeState es el estado de handshake de p ahora.
func (s *Service) HandshakeState(p *domain.Peer) string {
	return domain.HandshakeStateAt(p.LastHandshakeAt, s.now())
}

// Now es el reloj del servicio.
func (s *Service) Now() time.Time { return s.now() }
