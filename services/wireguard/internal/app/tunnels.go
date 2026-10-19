package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/platformevents"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	tunapi "github.com/hcdestroyer/horus-flow/services/tunnelagent/api"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/api/tunnelv1"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/tunnelcfg"
)

// Códigos de error de los túneles IPsec/GRE.
const (
	// CodeTunnelTransportDisabled: el hub no tiene habilitado ese transporte
	// (HORUS_TUNNEL_TRANSPORTS) o le falta su IP pública.
	CodeTunnelTransportDisabled = "TUNNEL_TRANSPORT_DISABLED"
)

// TunnelHub son los ajustes del hub para IPsec/GRE (HORUS_TUNNEL_*).
type TunnelHub struct {
	// Transports habilitados: ipsec, gre.
	Transports []string
	// Address: IPv4 pública del hub (inválida = sin configurar).
	Address netip.Addr
	// LocalID: identidad IKE del hub (IP o FQDN; vacía = Address).
	LocalID string
	MTU     int
}

// Enabled indica si el transporte está habilitado.
func (h TunnelHub) Enabled(transport string) bool { return slices.Contains(h.Transports, transport) }

// ikeID es la identidad IKE del hub (forma canónica de Horus).
func (h TunnelHub) ikeID() string {
	if h.LocalID != "" {
		return h.LocalID
	}
	if h.Address.IsValid() {
		return h.Address.String()
	}
	return ""
}

var _ tunapi.Control = (*Service)(nil)

func (s *Service) tunnelAgent() (tunapi.Agent, bool) {
	if s.o.TunnelAgent == nil {
		return nil, false
	}
	return s.o.TunnelAgent()
}

func (s *Service) tunnelSecrets() (devapi.TunnelSecrets, bool) {
	if s.o.TunnelSecrets == nil {
		return nil, false
	}
	return s.o.TunnelSecrets()
}

// transportOf es el transporte del túnel de un router (los routers sin
// connection_mode son MikroTik con WireGuard).
func transportOf(r devapi.RouterInfo) string {
	switch r.ConnectionMode {
	case devapi.ConnectionIPsec:
		return domain.TransportIPsec
	case devapi.ConnectionGRE:
		return domain.TransportGRE
	}
	return domain.TransportWireGuard
}

func eqAddr(a, b *netip.Addr) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

// peerMatches indica si el túnel vivo corresponde al transporte y a la IP
// pública/identidad del router (si no, se sustituye conservando su /32).
func peerMatches(p *domain.Peer, r devapi.RouterInfo) bool {
	want := transportOf(r)
	if p.Transport != want {
		return false
	}
	switch want {
	case domain.TransportIPsec:
		return eqAddr(p.RemoteAddress, r.TunnelRemoteAddress) && p.RemoteID != nil && *p.RemoteID == r.TunnelRemoteID
	case domain.TransportGRE:
		return eqAddr(p.RemoteAddress, r.TunnelRemoteAddress)
	}
	return true
}

// newTunnelPeer prepara el túnel de un router con su transporte.
func (s *Service) newTunnelPeer(r devapi.RouterInfo, preferred netip.Addr, now time.Time) (*domain.Peer, error) {
	p := &domain.Peer{ID: uuid.Must(uuid.NewV7()), TenantID: r.TenantID, ServerID: s.o.Hub.ID, RouterID: r.ID, Address: preferred,
		Status: domain.StatusAwaitingEnrollment, HandshakeState: domain.HandshakeNever, Keepalive: domain.Keepalive,
		CreatedAt: now, UpdatedAt: now, Version: 1, Transport: transportOf(r)}
	switch p.Transport {
	case domain.TransportIPsec:
		if r.TunnelRemoteID == "" {
			return nil, errors.New("wireguard: ipsec router without IKE identity")
		}
		id := r.TunnelRemoteID
		p.RemoteID, p.RemoteAddress = &id, r.TunnelRemoteAddress
	case domain.TransportGRE:
		if r.TunnelRemoteAddress == nil {
			return nil, errors.New("wireguard: gre router without public address")
		}
		p.RemoteAddress = r.TunnelRemoteAddress
		// RouterOS no admite clave GRE; VRP y Linux sí (clave = índice).
		if r.Vendor != devapi.VendorMikroTik && r.Vendor != "" {
			p.GREKey = domain.GREKeyFromIndex
		}
	}
	return p, nil
}

// onboardingForPeer es el estado de alta que proyecta un túnel en su router.
// IPsec/GRE no tienen paso de clave: pending_configuration hasta la primera
// SA (o el primer paquete GRE) y tunnel_up después.
func onboardingForPeer(p *domain.Peer) string {
	if p.IsTunnelAgent() {
		if p.Status == domain.StatusActive {
			return devapi.OnboardingTunnelUp
		}
		return devapi.OnboardingPending
	}
	return onboardingFor(p.Status)
}

// ---------------------------------------------------------------- estado deseado de tunnel-agent

// PushTunnels envía a tunnel-agent el estado completo de los túneles
// IPsec/GRE si cambió (o si force). Las PSK se descifran aquí (devices) y
// solo viajan por mTLS. Si devices no está disponible no se envía nada: un
// estado parcial haría que el agente quitara túneles.
func (s *Service) PushTunnels(ctx context.Context, force bool) error {
	agent, ok := s.tunnelAgent()
	if !ok {
		return nil
	}
	version, peers, err := s.o.Store.TunnelDesiredState(ctx, s.o.Hub.ID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	skip := !force && s.lastTunnelPush == version
	s.mu.Unlock()
	if skip {
		return nil
	}
	req, err := s.tunnelRequest(ctx, version, peers)
	if err != nil {
		return err
	}
	if _, err := agent.ApplyTunnels(ctx, req); err != nil {
		return fmt.Errorf("apply tunnels: %w", err)
	}
	s.mu.Lock()
	s.lastTunnelPush = version
	s.mu.Unlock()
	return nil
}

func (s *Service) tunnelRequest(ctx context.Context, version int64, peers []domain.Peer) (*tunnelv1.ApplyTunnelsRequest, error) {
	hub := s.o.Tunnels
	req := &tunnelv1.ApplyTunnelsRequest{HubId: s.o.Hub.ID.String(), DesiredVersion: version}
	var secrets devapi.TunnelSecrets
	for i := range peers {
		p := &peers[i]
		if p.TunnelIndex == nil || *p.TunnelIndex <= 0 || *p.TunnelIndex > int64(^uint32(0)>>1) {
			continue
		}
		if !hub.Enabled(p.Transport) {
			continue // transporte deshabilitado en el hub: el agente lo retira
		}
		dt := &tunnelv1.DesiredTunnel{PeerId: p.ID.String(), TenantId: p.TenantID.String(), Transport: p.Transport,
			Index: uint32(*p.TunnelIndex), TunnelAddress: p.AllowedIP(), HubTunnelAddress: s.o.CollectorIP.String(), //nolint:gosec // acotado arriba
			Mtu: int32(hub.MTU), GreKey: uint32(p.GREKey), //nolint:gosec // MTU 1280-1500 y clave ≤ 2^32-1 (CHECK)
			RoutedPrefixes: p.RoutedPrefixes()}
		if hub.Address.IsValid() {
			dt.LocalAddress = hub.Address.String()
		}
		if p.RemoteAddress != nil {
			dt.RemoteAddress = p.RemoteAddress.String()
		}
		switch p.Transport {
		case domain.TransportGRE:
			if !hub.Address.IsValid() {
				s.o.Logger.WarnContext(ctx, "gre tunnel skipped: HORUS_TUNNEL_HUB_ADDRESS unset", slog.String("peer_id", p.ID.String()))
				continue
			}
		case domain.TransportIPsec:
			if p.RemoteID == nil || hub.ikeID() == "" {
				continue
			}
			if secrets == nil {
				var ok bool
				if secrets, ok = s.tunnelSecrets(); !ok {
					return nil, errors.New("wireguard: devices (tunnel secrets) not available: tunnels not pushed")
				}
			}
			psk, err := secrets.RevealTunnelPSK(ctx, p.TenantID, p.RouterID)
			if errors.Is(err, devapi.ErrNoCredential) || errors.Is(err, devapi.ErrRouterNotFound) {
				s.o.Logger.WarnContext(ctx, "ipsec tunnel without psk skipped", slog.String("peer_id", p.ID.String()))
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("reveal psk: %w", err)
			}
			dt.Psk, dt.LocalId, dt.RemoteId = psk, tunnelcfg.StrongSwanID(hub.ikeID()), tunnelcfg.StrongSwanID(*p.RemoteID)
		}
		req.Tunnels = append(req.Tunnels, dt)
	}
	return req, nil
}

// ReportTunnelStatus implementa tunnelagent/api.Control (tunnel-agent cada
// 15 s): guarda el estado de las SA y los contadores, activa los túneles con
// su primera SA (o primer paquete GRE), detecta túneles caídos/recuperados
// (mismos eventos que WireGuard: el estado del router y el NOC no
// distinguen transporte) y, si el agente va por detrás, le reenvía el estado.
func (s *Service) ReportTunnelStatus(ctx context.Context, req *tunnelv1.ReportTunnelStatusRequest) (*tunnelv1.ReportTunnelStatusResponse, error) {
	if req.GetHubId() != "" && req.GetHubId() != s.o.Hub.ID.String() {
		return nil, errors.New("wireguard: tunnel report for an unknown hub")
	}
	now := s.now()
	obs := make([]TunnelObserved, 0, len(req.GetTunnels()))
	for _, t := range req.GetTunnels() {
		id, err := uuid.Parse(t.GetPeerId())
		if err != nil {
			continue
		}
		o := TunnelObserved{PeerID: id, State: t.GetState(), Alive: t.GetAlive(), RxBytes: t.GetRxBytes(), TxBytes: t.GetTxBytes()}
		if t.LastRekeyAt != nil {
			v := t.GetLastRekeyAt().AsTime().UTC()
			o.LastRekeyAt = &v
		}
		if t.LastPacketAt != nil {
			v := t.GetLastPacketAt().AsTime().UTC()
			o.LastPacketAt = &v
		}
		if t.Endpoint != nil {
			v := t.GetEndpoint()
			o.Endpoint = &v
		}
		if t.Error != nil {
			v := t.GetError()
			if len(v) > 500 {
				v = v[:500]
			}
			o.Error = &v
		}
		obs = append(obs, o)
	}
	desired, trs, err := s.o.Store.ApplyTunnelReport(ctx, s.o.Hub.ID, req.GetAppliedVersion(), req.GetIkeDaemonUp(), now, obs,
		func(tr Transition) []outbox.Event { return s.transitionEvents(tr, now) })
	if err != nil {
		return nil, err
	}
	if d, ok := s.o.Devices(); ok {
		for _, tr := range trs {
			if tr.Activated {
				p := tr.Peer
				if err := d.SetTunnel(ctx, p.TenantID, p.RouterID, devapi.TunnelUpdate{PeerID: p.ID, Address: p.Address,
					OnboardingState: onboardingForPeer(&p)}); err != nil {
					s.o.Logger.WarnContext(ctx, "project tunnel_up on router failed", errAttr(err))
				}
			}
		}
	}
	if req.GetAppliedVersion() < desired {
		s.mu.Lock()
		s.lastTunnelPush = 0
		noted := s.tunnelZeroNoted
		s.tunnelZeroNoted = req.GetAppliedVersion() == 0
		s.mu.Unlock()
		if req.GetAppliedVersion() == 0 && !noted {
			platformevents.Emit(ctx, platformevents.Event{Kind: platformevents.KindAgentRestarted, Severity: platformevents.SeverityWarn,
				Role: "wireguard", Message: "tunnel-agent reported no applied state (started or restarted); full desired state re-sent",
				Details: map[string]any{"hub_id": s.o.Hub.ID.String(), "desired_version": desired, "ike_daemon_up": req.GetIkeDaemonUp(),
					"observed_tunnels": len(req.GetTunnels())}})
		}
		s.Kick()
	} else {
		s.mu.Lock()
		s.tunnelZeroNoted = false
		s.mu.Unlock()
	}
	return &tunnelv1.ReportTunnelStatusResponse{DesiredVersion: desired}, nil
}

// transitionEvents son los eventos de una transición (activación y
// handshake stale/recovered); comunes a todos los transportes.
func (s *Service) transitionEvents(tr Transition, now time.Time) []outbox.Event {
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
}

// ---------------------------------------------------------------- configuración del router

// TunnelConfigInput es el cuerpo de POST /routers/{id}/tunnel-config.
type TunnelConfigInput struct {
	NTPServer    *string `json:"ntp_server"`
	SamplingRate *int    `json:"sampling_rate"`
}

// TunnelConfig es la configuración generada (la PSK y las credenciales solo
// aparecen en esta respuesta).
type TunnelConfig struct {
	RouterID        uuid.UUID
	Transport       string
	ProtectionLevel string
	TunnelAddress   netip.Addr
	HubAddress      *string
	HubID           *string
	RemoteAddress   *string
	RemoteID        *string
	PSK             *string
	GREKey          *int64
	SNMPUser        string
	Configs         []tunnelcfg.Config
	Warnings        []string
}

// AuditTunnelConfig es la acción auditada al generar la configuración.
const AuditTunnelConfig = "wireguard.tunnel_config.created"

// CreateTunnelConfig genera la configuración IPsec/GRE del router: asegura
// su túnel (IP de la IPAM), emite credenciales SNMP nuevas (y el usuario de
// la API en MikroTik) y, en IPsec, una PSK nueva; todo invalida lo anterior
// y aparece solo en esta respuesta. Lo audita.
func (s *Service) CreateTunnelConfig(ctx context.Context, routerID uuid.UUID, in TunnelConfigInput) (*TunnelConfig, error) {
	p, t, r, err := s.routerInScope(ctx, "wireguard.write", routerID)
	if err != nil {
		return nil, err
	}
	transport := transportOf(r)
	if transport == domain.TransportWireGuard || r.ConnectionMode == devapi.ConnectionDirect {
		return nil, apperr.New(apperr.KindInvalid, CodeConnectionModeMismatch,
			"Este router no usa IPsec ni GRE: con WireGuard use el script de alta; en modo directo, el alta directa.")
	}
	hub := s.o.Tunnels
	if !hub.Enabled(transport) || !hub.Address.IsValid() || (transport == domain.TransportIPsec && hub.ikeID() == "") {
		return nil, apperr.Conflict(CodeTunnelTransportDisabled,
			"El servidor Horus no tiene habilitado "+strings.ToUpper(transport)+" (instalador --tunnels, HORUS_TUNNEL_TRANSPORTS y HORUS_TUNNEL_HUB_ADDRESS).")
	}
	ntp := strOr(in.NTPServer, s.o.NTPServer)
	if len(ntp) > 253 || strings.ContainsAny(ntp, " \"';$[]{}\\") {
		return nil, apperr.Validation(apperr.Field("ntp_server", "INVALID_VALUE", "Nombre DNS o IP."))
	}
	sampling := tunnelcfg.DefaultSamplingRate
	if in.SamplingRate != nil {
		if *in.SamplingRate < 1 || *in.SamplingRate > 65535 {
			return nil, apperr.Validation(apperr.Field("sampling_rate", "INVALID_VALUE", "Entre 1 y 65535 (1 de cada N paquetes)."))
		}
		sampling = *in.SamplingRate
	}
	d, err := s.devices()
	if err != nil {
		return nil, err
	}
	secrets, ok := s.tunnelSecrets()
	if !ok {
		return nil, apperr.New(apperr.KindUnavailable, problem.CodeServiceUnavailable, "El inventario (devices) no está disponible en este proceso.")
	}
	peer, err := s.EnsurePeer(ctx, r)
	if errors.Is(err, domain.ErrPoolExhausted) {
		return nil, apperr.Conflict(domain.CodePoolExhausted, "No quedan IPs de túnel libres: el operador de plataforma debe añadir un rango.")
	}
	if err != nil {
		return nil, err
	}
	var creds devapi.OnboardingCredentials
	if r.Vendor == devapi.VendorMikroTik || r.Vendor == "" {
		creds, err = d.IssueCredentials(ctx, t.UUID(), r.ID)
	} else {
		creds, err = secrets.IssueSNMPCredentials(ctx, t.UUID(), r.ID)
	}
	if err != nil {
		return nil, mapDeviceErr(err)
	}
	var psk string
	if transport == domain.TransportIPsec {
		if psk, err = secrets.IssueTunnelPSK(ctx, t.UUID(), r.ID); err != nil {
			return nil, mapDeviceErr(err)
		}
	}
	now := s.now()
	peer, err = s.o.Store.MarkTunnelConfigured(ctx, t, peer.ID, now, func(v *domain.Peer) []outbox.Event {
		return []outbox.Event{peerEvent("horus.wireguard.peer.enrolled", v, svcActor, now, PeerEventData(v, []string{"status"}))}
	})
	if err != nil {
		return nil, err
	}
	s.Kick() // la PSK nueva llega al demonio IKE en segundos
	v := tunnelcfg.Values{Transport: transport, Vendor: r.Vendor, RouterID: r.ID.String(), RouterName: r.Name, TunnelAddress: peer.Address,
		ServicesCIDR: s.o.Hub.ServicesCIDR, CollectorIP: s.o.CollectorIP, HubAddress: hub.Address, HubID: hub.ikeID(), PSK: psk,
		GREKey: uint32(peer.GREKey), MTU: hub.MTU, SNMPUser: creds.SNMPUser, SNMPAuthPass: creds.SNMPAuthPassword, //nolint:gosec // CHECK ≤ 2^32-1
		SNMPPrivPass: creds.SNMPPrivPassword, APIUser: creds.APIUser, APIPassword: creds.APIPassword, NTPServer: ntp, SamplingRate: sampling}
	if r.TunnelRemoteAddress != nil {
		v.RemoteAddress = *r.TunnelRemoteAddress
	}
	if transport == domain.TransportIPsec {
		v.RemoteID = r.TunnelRemoteID
	}
	cfgs, err := tunnelcfg.Build(v)
	if err != nil {
		return nil, err
	}
	out := &TunnelConfig{RouterID: r.ID, Transport: transport, ProtectionLevel: r.ProtectionLevel, TunnelAddress: peer.Address,
		SNMPUser: creds.SNMPUser, Configs: cfgs, Warnings: []string{}}
	ha, hid := hub.Address.String(), hub.ikeID()
	out.HubAddress = &ha
	if transport == domain.TransportIPsec {
		out.HubID, out.PSK = &hid, &psk
		rid := r.TunnelRemoteID
		out.RemoteID = &rid
	} else {
		k := peer.GREKey
		out.GREKey = &k
	}
	if r.TunnelRemoteAddress != nil {
		ra := r.TunnelRemoteAddress.String()
		out.RemoteAddress = &ra
	}
	if r.ProtectionLevel == devapi.ProtectionUnencryptedInternet {
		out.Warnings = append(out.Warnings, "Transporte SIN CIFRAR por Internet (confirmado por un administrador): SNMP y flujos se pueden leer y "+
			"suplantar en el camino. Pase a IPsec en cuanto el equipo lo permita.")
	}
	changes := map[string]any{"transport": transport, "protection_level": r.ProtectionLevel, "credentials_rotated": true,
		"psk_rotated": transport == domain.TransportIPsec, "peer_id": peer.ID.String()}
	s.audit(ctx, userAudit(p, t, AuditTunnelConfig, "router", r.ID.String(), changes))
	return out, nil
}

func mapDeviceErr(err error) error {
	if errors.Is(err, devapi.ErrRouterNotFound) {
		return apperr.NotFound(domain.CodeRouterNotFound)
	}
	return err
}

// TunnelHubStatus resume el estado de tunnel-agent para la consola de plataforma.
type TunnelHubStatus struct {
	Transports []string
	Address    *string
	LocalID    *string
}

// TunnelSettings devuelve los ajustes de IPsec/GRE del hub.
func (s *Service) TunnelSettings() TunnelHubStatus {
	h := s.o.Tunnels
	out := TunnelHubStatus{Transports: slices.Clone(h.Transports)}
	if out.Transports == nil {
		out.Transports = []string{}
	}
	if h.Address.IsValid() {
		a := h.Address.String()
		out.Address = &a
	}
	if id := h.ikeID(); id != "" {
		out.LocalID = &id
	}
	return out
}
