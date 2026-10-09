package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	wgapi "github.com/hcdestroyer/horus-flow/services/wgagent/api"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/domain"
)

// Options configura el servicio.
type Options struct {
	Store Store
	// Dependencias en proceso, resueltas al usarlas (los proveedores pueden
	// registrarse después que wireguard).
	Devices func() (devapi.Onboarding, bool)
	Agent   func() (wgapi.Agent, bool)
	Audit   func() (authapi.AuditRecorder, bool)
	// Hub de plataforma (ID, nombre, endpoint, puerto, red de servicios, rangos).
	Hub Hub
	// HubPublicKey resuelve la clave pública del hub (configuración o agente local).
	HubPublicKey func() string
	CollectorIP  netip.Addr
	// PublicBaseURL es HORUS_PUBLIC_BASE_URL (https://…): destino del /tool fetch.
	PublicBaseURL string
	AccessMode    string
	// CACertPEM es el certificado público a importar en el router con TLS
	// self_signed/provided (D19); vacío con ACME.
	CACertPEM    string
	NTPServer    string
	CacheEntries string
	Now          func() time.Time
	Logger       *slog.Logger
}

// Service implementa los casos de uso.
type Service struct {
	o        Options
	kick     chan struct{}
	mu       sync.Mutex
	lastPush int64
}

// New crea el servicio.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Devices == nil {
		o.Devices = func() (devapi.Onboarding, bool) { return nil, false }
	}
	if o.Agent == nil {
		o.Agent = func() (wgapi.Agent, bool) { return nil, false }
	}
	if o.Audit == nil {
		o.Audit = func() (authapi.AuditRecorder, bool) { return nil, false }
	}
	if o.HubPublicKey == nil {
		o.HubPublicKey = func() string { return o.Hub.PublicKey }
	}
	if o.NTPServer == "" {
		o.NTPServer = "pool.ntp.org"
	}
	if o.CacheEntries == "" {
		o.CacheEntries = "256k"
	}
	return &Service{o: o, kick: make(chan struct{}, 1)}
}

func (s *Service) now() time.Time { return s.o.Now().UTC() }

// Init valida los rangos y registra el hub.
func (s *Service) Init(ctx context.Context) error {
	if _, err := domain.ValidatePools(s.o.Hub.Pools, s.o.Hub.ServicesCIDR); err != nil {
		return err
	}
	h := s.o.Hub
	h.PublicKey = s.o.HubPublicKey()
	return s.o.Store.EnsureHub(ctx, h)
}

// Kick pide reenviar el estado deseado al agente.
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// ---------------------------------------------------------------- eventos

// PeerEventData es el payload horus.events.wireguard.v1.Peer.
func PeerEventData(p *domain.Peer, changed []string) map[string]any {
	if changed == nil {
		changed = []string{}
	}
	ts := func(t *time.Time) any {
		if t == nil {
			return nil
		}
		return t.UTC().Format(outbox.TimeFormat)
	}
	var from any
	if p.EnrolledFromIP != nil {
		from = p.EnrolledFromIP.String()
	}
	return map[string]any{
		"id": p.ID.String(), "version": p.Version, "server_id": p.ServerID.String(), "router_id": p.RouterID.String(),
		"status": p.Status, "public_key": p.PublicKey, "address": p.AllowedIP(), "allowed_ips": []string{p.AllowedIP()},
		"persistent_keepalive_seconds": p.Keepalive, "enrolled_from_ip": from, "enrolled_at": ts(p.EnrolledAt),
		"activated_at": ts(p.ActivatedAt), "reason": p.RevokedReason, "created_at": ts(&p.CreatedAt), "changed_fields": changed,
	}
}

func peerEvent(typ string, p *domain.Peer, actor outbox.Actor, at time.Time, data any) outbox.Event {
	tid := p.TenantID
	return outbox.Event{Type: typ, Source: "horus/wireguard", TenantID: &tid, AggregateType: "peer", AggregateID: p.ID,
		AggregateVersion: p.Version, Actor: actor, OccurredAt: at, Data: data}
}

var svcActor = outbox.Actor{Type: "service", ID: "svc:wireguard"}

// audit registra una acción (auth en proceso o horus.wireguard.audit.recorded).
func (s *Service) audit(ctx context.Context, e authapi.AuditEntry) {
	e.OccurredAt = s.now()
	if e.Scope == "" {
		e.Scope = "tenant"
	}
	if rec, ok := s.o.Audit(); ok {
		if err := rec.Record(ctx, e); err != nil {
			s.o.Logger.ErrorContext(ctx, "audit record failed", slog.String("action", e.Action), slog.Any("error", err))
		}
		return
	}
	id := uuid.Must(uuid.NewV7())
	var tid *uuid.UUID
	if e.TenantID != uuid.Nil {
		t := e.TenantID
		tid = &t
	}
	actor := outbox.Actor{Type: e.ActorType, ID: e.ActorID, ViaPlatform: e.ViaPlatform}
	var tenant any
	if tid != nil {
		tenant = tid.String()
	}
	data := map[string]any{
		"id": id.String(), "tenant_id": tenant, "occurred_at": e.OccurredAt.Format(outbox.TimeFormat), "source_service": "wireguard",
		"actor": actor, "ip": nilIfEmpty(e.IP), "user_agent": nilIfEmpty(e.UserAgent), "action": e.Action,
		"resource_type": e.ResourceType, "resource_id": e.ResourceID, "scope": e.Scope, "outcome": e.Outcome, "reason": nil,
		"changes": orEmpty(e.Changes), "request_id": nilIfEmpty(e.RequestID), "trace_id": nilIfEmpty(e.TraceID),
	}
	ev := outbox.Event{ID: id, Type: "horus.wireguard.audit.recorded", Source: "horus/wireguard", TenantID: tid, AggregateType: "audit",
		AggregateID: id, AggregateVersion: 1, Actor: actor, OccurredAt: e.OccurredAt, Data: data}
	if err := s.o.Store.Emit(ctx, []outbox.Event{ev}); err != nil {
		s.o.Logger.ErrorContext(ctx, "audit outbox failed", slog.String("action", e.Action), slog.Any("error", err))
	}
}

func nilIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func userAudit(p *authz.Principal, t pgdb.TenantID, action, rtype, rid string, changes map[string]any) authapi.AuditEntry {
	return authapi.AuditEntry{TenantID: t.UUID(), ActorType: p.Type, ActorID: p.Subject, ViaPlatform: p.ViaPlatform, Action: action,
		ResourceType: rtype, ResourceID: rid, Outcome: "success", Changes: changes}
}

// ---------------------------------------------------------------- túnel por router

func (s *Service) devices() (devapi.Onboarding, error) {
	d, ok := s.o.Devices()
	if !ok {
		return nil, apperr.New(apperr.KindUnavailable, problem.CodeServiceUnavailable, "El inventario (devices) no está disponible en este proceso.")
	}
	return d, nil
}

// EnsurePeer asigna la IP de túnel del router si aún no tiene peer
// (consumo de horus.devices.router.created; idempotente) y la proyecta en
// el router.
func (s *Service) EnsurePeer(ctx context.Context, r devapi.RouterInfo) (*domain.Peer, error) {
	t := pgdb.TenantID(r.TenantID)
	p, err := s.o.Store.ActivePeer(ctx, t, r.ID)
	if errors.Is(err, domain.ErrNotFound) {
		now := s.now()
		p = &domain.Peer{ID: uuid.Must(uuid.NewV7()), TenantID: r.TenantID, ServerID: s.o.Hub.ID, RouterID: r.ID,
			Status: domain.StatusAwaitingEnrollment, HandshakeState: domain.HandshakeNever, Keepalive: domain.Keepalive,
			CreatedAt: now, UpdatedAt: now, Version: 1}
		err = s.o.Store.CreatePeer(ctx, t, p, func(v *domain.Peer) []outbox.Event {
			return []outbox.Event{peerEvent("horus.wireguard.peer.created", v, svcActor, now, PeerEventData(v, nil))}
		})
		if errors.Is(err, ErrPeerExists) {
			p, err = s.o.Store.ActivePeer(ctx, t, r.ID)
		}
	}
	if err != nil {
		return nil, err
	}
	if r.TunnelAddress == nil || *r.TunnelAddress != p.Address {
		if d, ok := s.o.Devices(); ok {
			if err := d.SetTunnel(ctx, r.TenantID, r.ID, devapi.TunnelUpdate{PeerID: p.ID, Address: p.Address,
				OnboardingState: onboardingFor(p.Status)}); err != nil && !errors.Is(err, devapi.ErrRouterNotFound) {
				return p, fmt.Errorf("project tunnel: %w", err)
			}
		}
	}
	return p, nil
}

func onboardingFor(status string) string {
	switch status {
	case domain.StatusPendingHandshake:
		return devapi.OnboardingKeyReceived
	case domain.StatusActive:
		return devapi.OnboardingTunnelUp
	}
	return devapi.OnboardingPending
}

func routerKey(t, r uuid.UUID) string { return t.String() + "/" + r.String() }

// Reconcile asegura un peer por router activo y revoca los peers de routers
// dados de baja. Equivale a consumir horus.devices.router.{created,deleted}
// mientras no hay bus entre módulos en el proceso; es idempotente. Si
// devices no responde no revoca nada (fail-static).
func (s *Service) Reconcile(ctx context.Context) error {
	d, ok := s.o.Devices()
	if !ok {
		return nil
	}
	routers, err := d.ListRouters(ctx)
	if err != nil {
		return fmt.Errorf("list routers: %w", err)
	}
	live := make(map[string]bool, len(routers))
	for _, r := range routers {
		live[routerKey(r.TenantID, r.ID)] = true
		if _, err := s.EnsurePeer(ctx, r); err != nil {
			if errors.Is(err, domain.ErrPoolExhausted) {
				s.o.Logger.ErrorContext(ctx, "tunnel ip pool exhausted", slog.String("code", domain.CodePoolExhausted),
					slog.String("router_id", r.ID.String()))
				continue
			}
			s.o.Logger.WarnContext(ctx, "ensure peer failed", slog.String("router_id", r.ID.String()), slog.Any("error", err))
		}
	}
	peers, err := s.o.Store.AllPeers(ctx)
	if err != nil {
		return err
	}
	for i := range peers {
		p := &peers[i]
		if live[routerKey(p.TenantID, p.RouterID)] {
			continue
		}
		now := s.now()
		err := s.o.Store.RevokePeer(ctx, pgdb.TenantID(p.TenantID), p.ID, "router_deleted", now, func(v *domain.Peer) []outbox.Event {
			return []outbox.Event{peerEvent("horus.wireguard.peer.revoked", v, svcActor, now, PeerEventData(v, []string{"status"}))}
		})
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		s.Kick()
	}
	return nil
}

// Push envía el estado deseado completo al agente si cambió (o si force).
func (s *Service) Push(ctx context.Context, force bool) error {
	agent, ok := s.o.Agent()
	if !ok {
		return nil
	}
	version, peers, err := s.o.Store.DesiredState(ctx, s.o.Hub.ID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	skip := !force && s.lastPush == version
	s.mu.Unlock()
	if skip {
		return nil
	}
	req := desiredRequest(s.o.Hub.ID, version, peers)
	if _, err := agent.ApplyDesiredState(ctx, req); err != nil {
		return fmt.Errorf("apply desired state: %w", err)
	}
	s.mu.Lock()
	s.lastPush = version
	s.mu.Unlock()
	return nil
}

// Run reconcilia y empuja el estado deseado hasta que ctx se cancela.
func (s *Service) Run(ctx context.Context, every time.Duration) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
			s.o.Logger.WarnContext(ctx, "wireguard reconcile failed", slog.Any("error", err))
		}
		if err := s.Push(ctx, false); err != nil && ctx.Err() == nil {
			s.o.Logger.WarnContext(ctx, "wireguard push failed (agent keeps its peers)", slog.Any("error", err))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-s.kick:
		}
	}
}

// ---------------------------------------------------------------- permisos

func scope(ctx context.Context, perm string) (*authz.Principal, pgdb.TenantID, error) {
	p := authz.FromContext(ctx)
	tid, err := authz.TenantOf(ctx)
	if err != nil {
		return nil, pgdb.TenantID{}, apperr.Forbidden(problem.CodeTokenScopeInvalid, "")
	}
	if !p.Has(perm) {
		return nil, pgdb.TenantID{}, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	return p, pgdb.TenantID(tid), nil
}

// routerInScope devuelve el router si existe en el tenant y el principal
// tiene perm sobre su nodo (si no, 404 sin revelar nada).
func (s *Service) routerInScope(ctx context.Context, perm string, id uuid.UUID) (*authz.Principal, pgdb.TenantID, devapi.RouterInfo, error) {
	p, t, err := scope(ctx, perm)
	if err != nil {
		return nil, t, devapi.RouterInfo{}, err
	}
	d, err := s.devices()
	if err != nil {
		return nil, t, devapi.RouterInfo{}, err
	}
	r, err := d.GetRouter(ctx, t.UUID(), id)
	if errors.Is(err, devapi.ErrRouterNotFound) || (err == nil && !p.HasScope(perm, "site:"+r.SiteID.String())) {
		return nil, t, devapi.RouterInfo{}, apperr.NotFound(domain.CodeRouterNotFound)
	}
	if err != nil {
		return nil, t, devapi.RouterInfo{}, err
	}
	return p, t, r, nil
}

func strOr(p *string, def string) string {
	if p == nil || strings.TrimSpace(*p) == "" {
		return def
	}
	return strings.TrimSpace(*p)
}
