package app

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
	viaapi "github.com/hcdestroyer/horus-flow/services/wireguard/api"
)

// AuditSwitchOnboarding es la acción auditada al generar el alta de un
// switch (rota su credencial SNMP).
const AuditSwitchOnboarding = "devices.switch_onboarding.created"

// SwitchOnboardingInput es el cuerpo de POST /routers/{id}/switch-onboarding.
type SwitchOnboardingInput struct {
	ManagementAddress *string `json:"management_address"`
	// ConnectionMode: direct (Horus está en la misma red de gestión) o
	// via_router (por el túnel de un router del sitio; ADR-0032). nil = el
	// modo actual del switch.
	ConnectionMode *string `json:"connection_mode"`
	// ViaRouterID: router del sitio (con túnel) para via_router; nil = el
	// actual.
	ViaRouterID *string `json:"via_router_id"`
	// SFlow: incluir la configuración sFlow (solo Huawei serie S) y activar
	// la fuente secundaria; false la desactiva; nil no cambia.
	SFlow        *bool `json:"sflow"`
	SamplingRate *int  `json:"sampling_rate"`
}

// SwitchPath es el camino de un switch vía router.
type SwitchPath struct {
	RouterID         uuid.UUID
	RouterName       string
	Transport        string
	ProtectionLevel  string
	HorusAddress     string
	Block            *string
	PollerAddress    string
	CollectorAddress string
	CollectorPort    int
	TunnelStatus     string
}

// SwitchOnboardingResult es la respuesta (secretos solo en Config).
type SwitchOnboardingResult struct {
	domain.SwitchConfig
	RouterID          uuid.UUID
	Platform          string
	SNMPVersion       string
	ManagementAddress string
	SNMPUser          *string
	SFlowEnabled      bool
	SamplingRate      *int
	ConnectionMode    string
	// Path y RouterConfig solo con via_router.
	Path         *SwitchPath
	RouterConfig *viaapi.RouterConfig
}

// viaDeps son las dependencias del modo vía router.
type viaDeps struct {
	store ViaStore
	paths func() (viaapi.ViaPaths, bool)
}

// WithVia habilita el modo vía router: las redes de gestión declaradas
// (store) y el hub de túneles (paths, en proceso).
func (d *DirectOnboarding) WithVia(store ViaStore, paths func() (viaapi.ViaPaths, bool)) *DirectOnboarding {
	d.via = &viaDeps{store: store, paths: paths}
	return d
}

func (d *DirectOnboarding) viaPaths() (viaapi.ViaPaths, error) {
	if d.via == nil || d.via.paths == nil {
		return nil, apperr.New(apperr.KindUnavailable, problem.CodeServiceUnavailable, "El hub de túneles (wireguard) no está disponible en este proceso.")
	}
	p, ok := d.via.paths()
	if !ok {
		return nil, apperr.New(apperr.KindUnavailable, problem.CodeServiceUnavailable, "El hub de túneles (wireguard) no está disponible en este proceso.")
	}
	return p, nil
}

func viaRouterInvalid() error {
	return apperr.Validation(apperr.Field("via_router_id", domain.CodeViaRouterInvalid,
		"Elija un router del mismo ISP con túnel activo con Horus (WireGuard, IPsec o GRE): Horus alcanza al switch por ese túnel."))
}

// CreateSwitch genera el alta de un switch: modo directo (Horus en la misma
// red de gestión) o vía un router del sitio (Horus en otra red: el router
// hace NAT 1:1 entre la dirección Horus del switch y su IP privada). Fija la
// dirección de gestión y el camino (vuelve a pending_configuration si
// cambian), emite una credencial SNMP nueva (v3 de solo lectura o, en SwOS,
// una comunidad v2c), opcionalmente activa sFlow y devuelve la configuración
// que pega el operador (y, vía router, la del router del sitio). La
// dirección de gestión es única por ISP y camino, nunca de Horus.
func (d *DirectOnboarding) CreateSwitch(ctx context.Context, routerID uuid.UUID, in SwitchOnboardingInput) (*SwitchOnboardingResult, error) {
	p, t, err := scope(ctx, PermCredentialsWrite)
	if err != nil {
		return nil, err
	}
	r, err := d.store.GetRouter(ctx, t, routerID)
	if err != nil || !siteAllowed(p, PermCredentialsWrite, r.SiteID) {
		return nil, apperr.NotFound(domain.CodeRouterNotFound)
	}
	if !r.IsSwitch() || r.SwitchPlatform == nil {
		return nil, apperr.New(apperr.KindInvalid, domain.CodeSwitchOnly, "Este equipo es un router: use su alta (script, túnel o alta directa).")
	}
	platform := *r.SwitchPlatform
	mode := connectionModeOf(r)
	if in.ConnectionMode != nil {
		m := strings.TrimSpace(*in.ConnectionMode)
		if !slices.Contains(domain.SwitchConnectionModes, m) {
			return nil, apperr.Validation(apperr.Field("connection_mode", "INVALID_VALUE",
				"direct (Horus está en la misma red de gestión que el switch) o via_router (por el túnel de un router del sitio)."))
		}
		mode = m
	}
	var via *domain.Router
	switch {
	case mode == domain.ConnectionViaRouter:
		var vid uuid.UUID
		switch {
		case in.ViaRouterID != nil:
			if vid, err = uuid.Parse(strings.TrimSpace(*in.ViaRouterID)); err != nil {
				return nil, viaRouterInvalid()
			}
		case r.ViaRouterID != nil:
			vid = *r.ViaRouterID
		default:
			return nil, apperr.Validation(apperr.Field("via_router_id", "REQUIRED",
				"Elija el router del sitio por cuyo túnel Horus alcanza al switch."))
		}
		via, err = d.store.GetRouter(ctx, t, vid)
		if err != nil || via.IsSwitch() || via.ID == r.ID || !domain.HasTunnel(connectionModeOf(via)) || !siteAllowed(p, "devices.read", via.SiteID) {
			return nil, viaRouterInvalid()
		}
		if _, err := d.viaPaths(); err != nil {
			return nil, err
		}
	case in.ViaRouterID != nil:
		return nil, apperr.Validation(apperr.Field("via_router_id", "NOT_APPLICABLE", "Solo los switches vía router tienen router del sitio."))
	}
	var mgmt netip.Addr
	switch {
	case in.ManagementAddress != nil:
		a, err := netip.ParseAddr(strings.TrimSpace(*in.ManagementAddress))
		if err != nil || !domain.ValidManagementAddress(a) || managementDenied(a, d.settings.Deny) || (via != nil && !a.Unmap().Is4()) {
			return nil, apperr.Validation(apperr.Field("management_address", "INVALID_VALUE",
				"IP unicast del switch en la red de gestión (IPv4 vía router; sin loopback, link-local, multicast ni direcciones de Horus)."))
		}
		mgmt = a.Unmap()
	case r.ManagementAddress != nil:
		mgmt, _ = netip.ParseAddr(*r.ManagementAddress)
	}
	if !mgmt.IsValid() {
		return nil, apperr.Validation(apperr.Field("management_address", "REQUIRED",
			"Indique la IP de gestión del switch (la que consultará Horus por SNMP)."))
	}
	if via != nil && !mgmt.Is4() {
		return nil, apperr.Validation(apperr.Field("management_address", "INVALID_VALUE", "Vía router solo se admiten direcciones de gestión IPv4."))
	}
	sflow := r.SFlowEnabled
	if in.SFlow != nil {
		if *in.SFlow && !domain.SFlowSupported(platform) {
			return nil, apperr.Validation(apperr.Field("sflow", domain.CodeSFlowNotSupported,
				"Solo los switches Huawei serie S exportan sFlow (RouterOS y SwOS no lo tienen)."))
		}
		sflow = *in.SFlow
	}
	sampling := domain.SwitchDefaultSamplingRate
	if in.SamplingRate != nil {
		if !domain.ValidSwitchSampling(*in.SamplingRate) {
			return nil, apperr.Validation(apperr.Field("sampling_rate", "INVALID_VALUE", "Entre 256 y 65535 (1 de cada N paquetes)."))
		}
		sampling = *in.SamplingRate
	}
	now := d.now().UTC()
	var viaID *uuid.UUID
	if via != nil {
		id := via.ID
		viaID = &id
	}
	var changed []string
	err = d.store.SetRouterTunnel(ctx, t, r.ID, func(x *domain.Router) bool {
		changed = changed[:0]
		path := false
		if connectionModeOf(x) != mode {
			x.ConnectionMode = mode
			changed = append(changed, "connection_mode")
			path = true
		}
		if !eqUUID(x.ViaRouterID, viaID) {
			x.ViaRouterID = viaID
			changed = append(changed, "via_router_id")
			path = true
		}
		if x.ManagementAddress == nil || *x.ManagementAddress != mgmt.String() {
			s := mgmt.String()
			x.ManagementAddress = &s
			changed = append(changed, "management_address")
			path = true
		}
		if path || (mode != domain.ConnectionViaRouter && x.ViaAddress != nil) {
			if x.ViaAddress != nil {
				x.ViaAddress = nil // la asigna wireguard para el camino nuevo
				changed = append(changed, "via_address")
			}
			if x.OnboardingState != devapi.OnboardingPending {
				x.OnboardingState = devapi.OnboardingPending // camino nuevo: hay que volver a verificar
				changed = append(changed, "onboarding_state")
			}
		}
		if x.SFlowEnabled != sflow {
			x.SFlowEnabled = sflow
			changed = append(changed, "sflow_enabled")
		}
		x.UpdatedAt = now
		return len(changed) > 0
	}, func(x *domain.Router) []outbox.Event {
		return []outbox.Event{event(p, t, "horus.devices.router.updated", "router", x.ID, x.Version, now, RouterEventData(x, changed))}
	})
	if err != nil {
		return nil, mapStoreErr(err, domain.CodeRouterNotFound)
	}
	values := domain.SwitchValues{Name: r.Hostname, Platform: platform, ManagementAddress: mgmt,
		PollerAddress: d.settings.PollerAddress, CollectorAddress: d.settings.CollectorAddress, CollectorPort: d.settings.CollectorPort,
		SFlow: sflow, SamplingRate: sampling}
	var (
		swPath *SwitchPath
		rcfg   *viaapi.RouterConfig
	)
	if via != nil {
		vp, err := d.ensureVia(ctx, t, r, via, mgmt, sflow)
		if err != nil {
			return nil, err
		}
		values.PollerAddress, values.CollectorAddress, values.CollectorPort = vp.PollerAddress, vp.CollectorAddress, vp.CollectorPort
		values.Via = &domain.SwitchVia{RouterName: via.Hostname, Transport: vp.Transport, HorusAddress: vp.Address}
		swPath = &SwitchPath{RouterID: via.ID, RouterName: via.Hostname, Transport: vp.Transport, ProtectionLevel: vp.ProtectionLevel,
			HorusAddress: vp.Address.String(), PollerAddress: vp.PollerAddress.String(), CollectorAddress: vp.CollectorAddress.String(),
			CollectorPort: vp.CollectorPort, TunnelStatus: vp.TunnelStatus}
		if vp.Block != nil {
			b := vp.Block.String()
			swPath.Block = &b
		}
		cfg := vp.RouterConfig
		rcfg = &cfg
	}
	creds, community, err := d.onboard.IssueSwitchCredentials(ctx, t.UUID(), r.ID, platform)
	if errors.Is(err, devapi.ErrRouterNotFound) {
		return nil, apperr.NotFound(domain.CodeRouterNotFound)
	}
	if err != nil {
		return nil, err
	}
	values.SNMPUser, values.SNMPAuthPass, values.SNMPPrivPass, values.Community = creds.SNMPUser, creds.SNMPAuthPassword, creds.SNMPPrivPassword, community
	cfg, err := domain.BuildSwitchConfig(values)
	if err != nil {
		return nil, err
	}
	audit := map[string]any{"credentials_rotated": true, "management_address": mgmt.String(), "platform": platform, "sflow_enabled": sflow,
		"connection_mode": mode}
	if swPath != nil {
		audit["via_router_id"] = swPath.RouterID.String()
		audit["via_address"] = swPath.HorusAddress
	}
	recordAudit(ctx, d.audit, d.store, d.logger, now, p, t, AuditSwitchOnboarding, "router", r.ID.String(), "site:"+r.SiteID.String(), audit)
	res := &SwitchOnboardingResult{SwitchConfig: cfg, RouterID: r.ID, Platform: platform, SNMPVersion: "3", ManagementAddress: mgmt.String(),
		SFlowEnabled: sflow, ConnectionMode: mode, Path: swPath, RouterConfig: rcfg}
	if domain.SNMPv2cOnly(platform) {
		res.SNMPVersion = "2c"
	} else {
		u := creds.SNMPUser
		res.SNMPUser = &u
	}
	if sflow {
		res.SamplingRate = &sampling
	}
	return res, nil
}

// ensureVia pide a wireguard la dirección Horus del switch y la
// configuración del router del sitio (con la red de gestión declarada que
// contenga la IP, si la hay).
func (d *DirectOnboarding) ensureVia(ctx context.Context, t pgdb.TenantID, sw, via *domain.Router, mgmt netip.Addr, sflow bool) (viaapi.ViaPath, error) {
	paths, err := d.viaPaths()
	if err != nil {
		return viaapi.ViaPath{}, err
	}
	req := viaapi.ViaPathRequest{TenantID: t.UUID(), DeviceID: sw.ID, DeviceName: sw.Hostname, RouterID: via.ID, Target: mgmt, SFlow: sflow}
	if d.via.store != nil {
		sub, err := d.via.store.ManagementSubnetFor(ctx, t, via.ID, mgmt)
		switch {
		case err == nil:
			req.Subnet = &viaapi.ViaSubnet{ID: sub.ID, Prefix: sub.Prefix}
		case !errors.Is(err, domain.ErrNotFound):
			return viaapi.ViaPath{}, err
		}
	}
	vp, err := paths.EnsureViaPath(ctx, req)
	switch {
	case errors.Is(err, viaapi.ErrViaRouterInvalid):
		return viaapi.ViaPath{}, viaRouterInvalid()
	case errors.Is(err, viaapi.ErrViaTargetInUse):
		return viaapi.ViaPath{}, apperr.Conflict(domain.CodeManagementAddressInUse, "Otro switch ya usa esa IP de gestión a través de este router.")
	case errors.Is(err, viaapi.ErrViaPoolExhausted):
		return viaapi.ViaPath{}, apperr.New(apperr.KindUnavailable, "WIREGUARD_IP_POOL_EXHAUSTED",
			"No quedan direcciones libres en los rangos de túnel del hub: amplíelos en la plataforma.")
	case err != nil:
		return viaapi.ViaPath{}, err
	}
	return vp, nil
}

func eqUUID(a, b *uuid.UUID) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
