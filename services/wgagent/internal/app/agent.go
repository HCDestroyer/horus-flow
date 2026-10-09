// Package app implementa el agente del hub WireGuard (rol wg-agent):
// aplica el estado deseado que envía wireguard (ApplyDesiredState,
// idempotente: estado completo + versión) y reporta el estado observado cada
// 15 s (ReportStatus). Es fail-static: si el control no responde no toca los
// peers; tras reiniciar no borra nada hasta recibir un estado deseado
// completo (el control lo reenvía al ver applied_version = 0).
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	wgapi "github.com/hcdestroyer/horus-flow/services/wgagent/api"
	"github.com/hcdestroyer/horus-flow/services/wgagent/api/agentv1"
	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/domain"
)

// ErrWrongHub indica un estado deseado para otro hub.
var ErrWrongHub = errors.New("wgagent: desired state for another hub")

// Options configura el agente.
type Options struct {
	Device     Device
	HubID      string // vacío = adopta el del primer estado deseado
	PrivateKey string
	PublicKey  string
	ListenPort int
	Logger     *slog.Logger
	Now        func() time.Time
	// ReportEvery es el periodo del reporte (15 s).
	ReportEvery time.Duration
}

// Agent es el agente.
type Agent struct {
	o          Options
	mu         sync.Mutex
	applied    int64
	configured bool
	controlMu  sync.RWMutex
	control    wgapi.Control
}

// New crea el agente.
func New(o Options) *Agent {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.ReportEvery <= 0 {
		o.ReportEvery = 15 * time.Second
	}
	return &Agent{o: o}
}

var _ wgapi.Agent = (*Agent)(nil)

// HubPublicKey implementa api.HubKey.
func (a *Agent) HubPublicKey() string { return a.o.PublicKey }

// SetControl fija el cliente del control (en proceso o gRPC).
func (a *Agent) SetControl(c wgapi.Control) {
	a.controlMu.Lock()
	a.control = c
	a.controlMu.Unlock()
}

// AppliedVersion devuelve la versión aplicada (0 = ninguna desde el arranque).
func (a *Agent) AppliedVersion() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.applied
}

func (a *Agent) ensureConfigured(ctx context.Context) error {
	if a.configured {
		return nil
	}
	if err := a.o.Device.Configure(ctx, a.o.PrivateKey, a.o.ListenPort); err != nil {
		return fmt.Errorf("configure interface: %w", err)
	}
	a.configured = true
	return nil
}

// ApplyDesiredState implementa api.Agent.
func (a *Agent) ApplyDesiredState(ctx context.Context, req *agentv1.ApplyDesiredStateRequest) (*agentv1.ApplyDesiredStateResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.o.HubID == "" {
		a.o.HubID = req.GetHubId()
	}
	if req.GetHubId() != a.o.HubID {
		return nil, ErrWrongHub
	}
	if req.GetDesiredVersion() < a.applied {
		// Estado viejo (reintento tardío): no retrocede.
		return &agentv1.ApplyDesiredStateResponse{AppliedVersion: a.applied}, nil
	}
	desired := make([]domain.DesiredPeer, 0, len(req.GetPeers()))
	for _, p := range req.GetPeers() {
		desired = append(desired, domain.DesiredPeer{PeerID: p.GetPeerId(), TenantID: p.GetTenantId(), PublicKey: p.GetPublicKey(),
			AllowedIPs: p.GetAllowedIps(), Keepalive: p.GetPersistentKeepaliveSeconds()})
	}
	want, err := domain.Normalize(desired)
	if err != nil {
		return nil, err
	}
	if err := a.ensureConfigured(ctx); err != nil {
		return nil, err
	}
	obs, err := a.o.Device.Peers(ctx)
	if err != nil {
		return nil, fmt.Errorf("read peers: %w", err)
	}
	observed := make(map[string]domain.Peer, len(obs))
	for _, p := range obs {
		observed[p.PublicKey] = p
	}
	plan := domain.Diff(want, observed)
	if err := a.o.Device.Apply(ctx, plan.Upsert, plan.Remove); err != nil {
		return nil, fmt.Errorf("apply peers: %w", err)
	}
	a.applied = req.GetDesiredVersion()
	a.o.Logger.InfoContext(ctx, "wireguard desired state applied", slog.Int64("version", a.applied),
		slog.Int("peers", len(want)), slog.Int("upserted", len(plan.Upsert)), slog.Int("removed", len(plan.Remove)))
	return &agentv1.ApplyDesiredStateResponse{AppliedVersion: a.applied, PeersAdded: int32(len(plan.Upsert)), //nolint:gosec // acotado por el estado
		PeersRemoved: int32(len(plan.Remove))}, nil //nolint:gosec // acotado por el estado
}

// Status construye el reporte del estado observado.
func (a *Agent) Status(ctx context.Context) *agentv1.ReportStatusRequest {
	a.mu.Lock()
	hub, applied := a.o.HubID, a.applied
	a.mu.Unlock()
	req := &agentv1.ReportStatusRequest{HubId: hub, AppliedVersion: applied, ObservedAt: timestamppb.New(a.o.Now())}
	peers, err := a.o.Device.Peers(ctx)
	if err != nil {
		a.o.Logger.WarnContext(ctx, "wireguard interface unreadable", slog.String("error", err.Error()))
		return req
	}
	req.InterfaceUp = true
	for _, p := range peers {
		op := &agentv1.ObservedPeer{PublicKey: p.PublicKey, RxBytes: p.RxBytes, TxBytes: p.TxBytes}
		if p.Endpoint != "" {
			ep := p.Endpoint
			op.Endpoint = &ep
		}
		if !p.LastHandshake.IsZero() {
			op.LastHandshakeAt = timestamppb.New(p.LastHandshake)
		}
		req.Peers = append(req.Peers, op)
	}
	return req
}

// Report envía un reporte al control. Un error no cambia nada (fail-static).
func (a *Agent) Report(ctx context.Context) error {
	a.controlMu.RLock()
	c := a.control
	a.controlMu.RUnlock()
	if c == nil {
		return errors.New("wgagent: no control client")
	}
	if _, err := c.ReportStatus(ctx, a.Status(ctx)); err != nil {
		return fmt.Errorf("report status: %w", err)
	}
	return nil
}

// Run reporta cada ReportEvery hasta que ctx se cancela.
func (a *Agent) Run(ctx context.Context) error {
	t := time.NewTicker(a.o.ReportEvery)
	defer t.Stop()
	for {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := a.Report(rctx); err != nil && ctx.Err() == nil {
			a.o.Logger.WarnContext(ctx, "wireguard control unreachable (fail-static: peers untouched)", slog.String("error", err.Error()))
		}
		cancel()
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
