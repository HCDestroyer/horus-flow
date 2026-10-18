// Package app implementa el agente de túneles IPsec IKEv2 y GRE del hub (rol
// tunnel-agent): aplica el estado deseado que envía wireguard
// (ApplyTunnels: estado completo + versión, idempotente) y reporta el estado
// observado cada 15 s (ReportTunnelStatus). Fail-static como wg-agent: si
// el control no responde no toca nada; tras reiniciar no borra nada hasta
// recibir un estado deseado completo; al parar no limpia las interfaces.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	tunapi "github.com/hcdestroyer/horus-flow/services/tunnelagent/api"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/api/tunnelv1"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/domain"
)

// ErrWrongHub indica un estado deseado para otro hub.
var ErrWrongHub = errors.New("tunnelagent: desired state for another hub")

// AliveWindow: un túnel GRE sin paquetes recibidos en este tiempo se reporta
// como no vivo (mismo umbral que el handshake de WireGuard).
const AliveWindow = 180 * time.Second

// Options configura el agente.
type Options struct {
	Links  Links
	IKE    IKE
	HubID  string // vacío = adopta el del primer estado deseado
	Logger *slog.Logger
	Now    func() time.Time
	// ReportEvery es el periodo del reporte (15 s).
	ReportEvery time.Duration
}

// Agent es el agente.
type Agent struct {
	o         Options
	mu        sync.Mutex
	applied   int64
	desired   map[uint32]domain.Tunnel
	fps       map[uint32]string
	errs      map[uint32]string
	rxPackets map[uint32]uint64
	lastPkt   map[uint32]time.Time
	controlMu sync.RWMutex
	control   tunapi.Control
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
	return &Agent{o: o, fps: map[uint32]string{}, errs: map[uint32]string{}, rxPackets: map[uint32]uint64{}, lastPkt: map[uint32]time.Time{}}
}

var _ tunapi.Agent = (*Agent)(nil)

// SetControl fija el cliente del control (en proceso o gRPC).
func (a *Agent) SetControl(c tunapi.Control) {
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

// fingerprint resume la configuración de t sin guardar la PSK.
func fingerprint(t domain.Tunnel) string {
	d := ""
	if t.PSK != "" {
		s := sha256.Sum256([]byte("horus-psk-fp:" + t.PSK))
		d = hex.EncodeToString(s[:8])
	}
	return domain.Fingerprint(t, d)
}

func toDesired(req *tunnelv1.ApplyTunnelsRequest) []domain.DesiredTunnel {
	out := make([]domain.DesiredTunnel, 0, len(req.GetTunnels()))
	for _, t := range req.GetTunnels() {
		out = append(out, domain.DesiredTunnel{PeerID: t.GetPeerId(), TenantID: t.GetTenantId(), Transport: t.GetTransport(),
			Index: t.GetIndex(), TunnelAddress: t.GetTunnelAddress(), HubAddress: t.GetHubTunnelAddress(), LocalAddress: t.GetLocalAddress(),
			RemoteIP: t.GetRemoteAddress(), LocalID: t.GetLocalId(), RemoteID: t.GetRemoteId(), PSK: t.GetPsk(), MTU: t.GetMtu(), GREKey: t.GetGreKey(),
			RoutedPrefixes: t.GetRoutedPrefixes()})
	}
	return out
}

// ApplyTunnels implementa api.Agent.
func (a *Agent) ApplyTunnels(ctx context.Context, req *tunnelv1.ApplyTunnelsRequest) (*tunnelv1.ApplyTunnelsResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.o.HubID == "" {
		a.o.HubID = req.GetHubId()
	}
	if req.GetHubId() != a.o.HubID {
		return nil, ErrWrongHub
	}
	if req.GetDesiredVersion() < a.applied {
		return &tunnelv1.ApplyTunnelsResponse{AppliedVersion: a.applied}, nil // reintento tardío: no retrocede
	}
	want, err := domain.Normalize(toDesired(req))
	if err != nil {
		return nil, err
	}
	links, err := a.o.Links.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tunnel interfaces: %w", err)
	}
	observed := make([]domain.Observed, 0, len(links))
	for _, l := range links {
		observed = append(observed, domain.Observed{Transport: l.Transport, Index: l.Index, Fingerprint: a.fps[l.Index]})
	}
	plan := domain.Diff(want, observed, fingerprint)
	var errs []error
	// Conexiones IKE huérfanas (p. ej. de un túnel pasado a GRE o borrado
	// mientras el agente estaba parado). Sin demonio IKE y sin túneles ipsec
	// deseados no es un error.
	conns, cerr := a.o.IKE.Conns(ctx)
	if cerr != nil && hasIPsec(want) {
		errs = append(errs, fmt.Errorf("ike daemon: %w", cerr))
	}
	for _, idx := range conns {
		if t, ok := want[idx]; !ok || t.Transport != domain.TransportIPsec {
			if err := a.o.IKE.Unload(ctx, idx); err != nil {
				errs = append(errs, fmt.Errorf("unload %s: %w", domain.ConnName(idx), err))
			}
		}
	}
	for _, o := range plan.Remove {
		if err := a.o.Links.Remove(ctx, o.Transport, o.Index); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", domain.InterfaceName(o.Transport, o.Index), err))
			continue
		}
		delete(a.fps, o.Index)
		delete(a.rxPackets, o.Index)
		delete(a.lastPkt, o.Index)
	}
	clear(a.errs)
	for _, t := range plan.Upsert {
		if err := a.upsert(ctx, t); err != nil {
			a.errs[t.Index] = err.Error()
			errs = append(errs, fmt.Errorf("%s: %w", t.Interface(), err))
			continue
		}
		a.fps[t.Index] = fingerprint(t)
	}
	// Una conexión IKE puede faltar aunque la interfaz esté bien (el demonio
	// se reinició): se recarga.
	if cerr == nil {
		loaded := map[uint32]bool{}
		for _, idx := range conns {
			loaded[idx] = true
		}
		for idx, t := range want {
			if t.Transport == domain.TransportIPsec && !loaded[idx] && !slices.ContainsFunc(plan.Upsert, func(u domain.Tunnel) bool { return u.Index == idx }) {
				if err := a.o.IKE.Load(ctx, t); err != nil {
					a.errs[idx] = err.Error()
					errs = append(errs, fmt.Errorf("reload %s: %w", t.ConnName(), err))
				}
			}
		}
	}
	if err := a.o.Links.SyncFilters(ctx, grePeers(want), bindings(want)); err != nil {
		errs = append(errs, fmt.Errorf("firewall sets: %w", err))
	}
	a.desired = want
	if len(errs) > 0 {
		// Lo que sí se aplicó queda (fail-static); la versión no avanza y el
		// control reenvía el estado.
		return nil, errors.Join(errs...)
	}
	a.applied = req.GetDesiredVersion()
	a.o.Logger.InfoContext(ctx, "tunnel desired state applied", slog.Int64("version", a.applied), slog.Int("tunnels", len(want)),
		slog.Int("upserted", len(plan.Upsert)), slog.Int("removed", len(plan.Remove)))
	return &tunnelv1.ApplyTunnelsResponse{AppliedVersion: a.applied, TunnelsAdded: int32(len(plan.Upsert)), //nolint:gosec // acotado por el estado
		TunnelsRemoved: int32(len(plan.Remove))}, nil //nolint:gosec // acotado por el estado
}

func (a *Agent) upsert(ctx context.Context, t domain.Tunnel) error {
	if err := a.o.Links.Ensure(ctx, t); err != nil {
		return fmt.Errorf("interface: %w", err)
	}
	if t.Transport == domain.TransportIPsec {
		if err := a.o.IKE.Load(ctx, t); err != nil {
			return fmt.Errorf("ike: %w", err)
		}
	}
	return nil
}

func hasIPsec(want map[uint32]domain.Tunnel) bool {
	for _, t := range want {
		if t.Transport == domain.TransportIPsec {
			return true
		}
	}
	return false
}

func grePeers(want map[uint32]domain.Tunnel) []netip.Addr {
	var out []netip.Addr
	for _, t := range want {
		if t.Transport == domain.TransportGRE {
			out = append(out, t.Remote)
		}
	}
	slices.SortFunc(out, func(x, y netip.Addr) int { return x.Compare(y) })
	return out
}

// bindings liga a cada interfaz su /32 de túnel y las direcciones Horus de
// los equipos alcanzados por ese router (ADR-0032).
func bindings(want map[uint32]domain.Tunnel) []Binding {
	out := make([]Binding, 0, len(want))
	for _, t := range want {
		out = append(out, Binding{Prefix: netip.PrefixFrom(t.TunnelAddress, 32), Interface: t.Interface()})
		for _, r := range t.Routed {
			out = append(out, Binding{Prefix: r, Interface: t.Interface()})
		}
	}
	slices.SortFunc(out, func(x, y Binding) int {
		if c := x.Prefix.Addr().Compare(y.Prefix.Addr()); c != 0 {
			return c
		}
		return x.Prefix.Bits() - y.Prefix.Bits()
	})
	return out
}

// Status construye el reporte del estado observado.
func (a *Agent) Status(ctx context.Context) *tunnelv1.ReportTunnelStatusRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.o.Now().UTC()
	req := &tunnelv1.ReportTunnelStatusRequest{HubId: a.o.HubID, AppliedVersion: a.applied, ObservedAt: timestamppb.New(now)}
	links, lerr := a.o.Links.List(ctx)
	if lerr != nil {
		a.o.Logger.WarnContext(ctx, "tunnel interfaces unreadable", slog.String("error", lerr.Error()))
	}
	byIdx := make(map[uint32]LinkState, len(links))
	for _, l := range links {
		byIdx[l.Index] = l
	}
	var sas map[uint32]SA
	if hasIPsec(a.desired) {
		var err error
		if sas, err = a.o.IKE.SAs(ctx); err != nil {
			a.o.Logger.WarnContext(ctx, "ike daemon unreachable", slog.String("error", err.Error()))
		} else {
			req.IkeDaemonUp = true
		}
	}
	idxs := make([]uint32, 0, len(a.desired))
	for idx := range a.desired {
		idxs = append(idxs, idx)
	}
	slices.Sort(idxs)
	for _, idx := range idxs {
		t := a.desired[idx]
		l, hasLink := byIdx[idx]
		ot := &tunnelv1.ObservedTunnel{PeerId: t.PeerID, Transport: t.Transport, RxBytes: l.RxBytes, TxBytes: l.TxBytes}
		if e, ok := a.errs[idx]; ok {
			ot.Error = &e
		}
		switch t.Transport {
		case domain.TransportGRE:
			if hasLink && lerr == nil {
				if l.RxPackets != a.rxPackets[idx] {
					if l.RxPackets > a.rxPackets[idx] {
						a.lastPkt[idx] = now
					}
					a.rxPackets[idx] = l.RxPackets
				}
			}
			ot.State = "down"
			if hasLink && l.Up {
				ot.State = "up"
			}
			if last, ok := a.lastPkt[idx]; ok {
				ot.LastPacketAt = timestamppb.New(last)
				ot.Alive = ot.State == "up" && now.Sub(last) <= AliveWindow
			}
		case domain.TransportIPsec:
			ot.State = "down"
			if sa, ok := sas[idx]; ok {
				ot.State = sa.State
				ot.Alive = sa.State == "established" && sa.ChildInstalled && hasLink && l.Up
				if !sa.EstablishedAt.IsZero() {
					ot.EstablishedAt = timestamppb.New(sa.EstablishedAt.UTC())
				}
				if !sa.LastRekeyAt.IsZero() {
					ot.LastRekeyAt = timestamppb.New(sa.LastRekeyAt.UTC())
				}
				if sa.Endpoint != "" {
					ep := sa.Endpoint
					ot.Endpoint = &ep
				}
				if sa.RxBytes > 0 || sa.TxBytes > 0 {
					ot.RxBytes, ot.TxBytes = sa.RxBytes, sa.TxBytes
				}
			}
		}
		req.Tunnels = append(req.Tunnels, ot)
	}
	return req
}

// heal recarga las conexiones IKE que faltan (el demonio se reinició y
// perdió su configuración, que solo vive en memoria).
func (a *Agent) heal(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !hasIPsec(a.desired) {
		return
	}
	conns, err := a.o.IKE.Conns(ctx)
	if err != nil {
		return
	}
	loaded := map[uint32]bool{}
	for _, idx := range conns {
		loaded[idx] = true
	}
	for idx, t := range a.desired {
		if t.Transport != domain.TransportIPsec || loaded[idx] {
			continue
		}
		if err := a.o.IKE.Load(ctx, t); err != nil {
			a.errs[idx] = err.Error()
			a.o.Logger.WarnContext(ctx, "ike connection reload failed", slog.String("conn", t.ConnName()), slog.String("error", err.Error()))
			continue
		}
		delete(a.errs, idx)
		a.o.Logger.InfoContext(ctx, "ike connection reloaded (daemon restarted)", slog.String("conn", t.ConnName()))
	}
}

// Report envía un reporte al control. Un error no cambia nada (fail-static).
func (a *Agent) Report(ctx context.Context) error {
	a.controlMu.RLock()
	c := a.control
	a.controlMu.RUnlock()
	if c == nil {
		return errors.New("tunnelagent: no control client")
	}
	if _, err := c.ReportTunnelStatus(ctx, a.Status(ctx)); err != nil {
		return fmt.Errorf("report status: %w", err)
	}
	return nil
}

// Run recarga lo que falte en el demonio IKE y reporta cada ReportEvery
// hasta que ctx se cancela.
func (a *Agent) Run(ctx context.Context) error {
	t := time.NewTicker(a.o.ReportEvery)
	defer t.Stop()
	for {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		a.heal(rctx)
		if err := a.Report(rctx); err != nil && ctx.Err() == nil {
			a.o.Logger.WarnContext(ctx, "tunnel control unreachable (fail-static: tunnels untouched)", slog.String("error", err.Error()))
		}
		cancel()
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
