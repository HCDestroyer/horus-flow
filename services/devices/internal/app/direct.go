package app

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// AuditDirectOnboarding es la acción auditada al generar la configuración
// de alta directa (rota la credencial SNMPv3).
const AuditDirectOnboarding = "devices.direct_onboarding.created"

// DirectSettings son los valores de la instalación para el modo directo
// (HORUS_DEVICES_DIRECT_*).
type DirectSettings struct {
	CollectorAddress netip.Addr
	CollectorPort    int
	PollerAddress    netip.Addr
	NTPServer        netip.Addr
	// Deny: rangos que nunca son dirección de gestión (túneles de Horus,
	// IPs de la instalación).
	Deny []netip.Prefix
}

func managementDenied(a netip.Addr, deny []netip.Prefix) bool {
	a = a.Unmap()
	for _, p := range deny {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// directStore es lo que necesita DirectOnboarding del repositorio.
type directStore interface {
	GetRouter(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Router, error)
	SetRouterTunnel(ctx context.Context, t pgdb.TenantID, id uuid.UUID, fn func(r *domain.Router) bool, ev Events[domain.Router]) error
	Emit(ctx context.Context, evs []outbox.Event) error
}

// DirectOnboarding genera el alta de un router en modo directo (Huawei VRP,
// sin túnel; docs/vendors/huawei.md §5): fija la dirección de gestión, emite
// una credencial SNMPv3 nueva (solo lectura, cifrada; en claro solo en esta
// respuesta) y devuelve la configuración VRP que pega el operador.
type DirectOnboarding struct {
	store    directStore
	onboard  *Onboarding
	settings DirectSettings
	audit    func() (authapi.AuditRecorder, bool)
	logger   *slog.Logger
	now      func() time.Time
}

// NewDirectOnboarding crea el servicio.
func NewDirectOnboarding(store directStore, onboard *Onboarding, settings DirectSettings, audit func() (authapi.AuditRecorder, bool),
	logger *slog.Logger, now func() time.Time,
) *DirectOnboarding {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if audit == nil {
		audit = func() (authapi.AuditRecorder, bool) { return nil, false }
	}
	return &DirectOnboarding{store: store, onboard: onboard, settings: settings, audit: audit, logger: logger, now: now}
}

// DirectOnboardingInput es el cuerpo de POST /routers/{id}/direct-onboarding.
type DirectOnboardingInput struct {
	ManagementAddress *string `json:"management_address"`
	SamplingRate      *int    `json:"sampling_rate"`
}

// DirectOnboardingResult es la respuesta.
type DirectOnboardingResult struct {
	domain.VRPConfig
	RouterID          uuid.UUID
	Vendor            string
	ManagementAddress string
	CollectorAddress  *string
	CollectorPort     int
	SNMPUser          string
	SamplingRate      int
}

// Create genera la configuración (rota la credencial SNMPv3: la anterior deja
// de valer).
func (d *DirectOnboarding) Create(ctx context.Context, routerID uuid.UUID, in DirectOnboardingInput) (*DirectOnboardingResult, error) {
	p, t, err := scope(ctx, PermCredentialsWrite)
	if err != nil {
		return nil, err
	}
	r, err := d.store.GetRouter(ctx, t, routerID)
	if err != nil || !siteAllowed(p, PermCredentialsWrite, r.SiteID) {
		return nil, apperr.NotFound(domain.CodeRouterNotFound)
	}
	if r.IsSwitch() {
		return nil, apperr.New(apperr.KindInvalid, domain.CodeSwitchNotSupported,
			"Los switches tienen su propio alta: POST /routers/{id}/switch-onboarding.")
	}
	if connectionModeOf(r) != domain.ConnectionDirect {
		return nil, apperr.New(apperr.KindInvalid, domain.CodeVendorNotSupported,
			"El alta directa es para routers sin WireGuard (Huawei VRP); los MikroTik usan el script de alta con túnel.")
	}
	var mgmt netip.Addr
	switch {
	case in.ManagementAddress != nil:
		a, err := netip.ParseAddr(strings.TrimSpace(*in.ManagementAddress))
		if err != nil || !domain.ValidManagementAddress(a) || managementDenied(a, d.settings.Deny) {
			return nil, apperr.Validation(apperr.Field("management_address", "INVALID_VALUE",
				"IP unicast del router tal y como la ve Horus (sin loopback, link-local, multicast ni direcciones de Horus)."))
		}
		mgmt = a.Unmap()
	case r.ManagementAddress != nil:
		mgmt, _ = netip.ParseAddr(*r.ManagementAddress)
	}
	if !mgmt.IsValid() {
		return nil, apperr.Validation(apperr.Field("management_address", "REQUIRED",
			"Indique la IP de origen del router (la que verá Horus en el sondeo SNMP y en NetStream)."))
	}
	sampling := domain.VRPDefaultSamplingRate
	if in.SamplingRate != nil {
		if *in.SamplingRate < 1 || *in.SamplingRate > 65535 {
			return nil, apperr.Validation(apperr.Field("sampling_rate", "INVALID_VALUE", "Entre 1 y 65535 (1 de cada N paquetes)."))
		}
		sampling = *in.SamplingRate
	}
	now := d.now().UTC()
	if r.ManagementAddress == nil || *r.ManagementAddress != mgmt.String() {
		err := d.store.SetRouterTunnel(ctx, t, r.ID, func(x *domain.Router) bool {
			s := mgmt.String()
			x.ManagementAddress, x.UpdatedAt = &s, now
			if x.OnboardingState != devapi.OnboardingPending {
				x.OnboardingState = devapi.OnboardingPending // dirección nueva: hay que volver a verificar
			}
			return true
		}, func(x *domain.Router) []outbox.Event {
			return []outbox.Event{event(p, t, "horus.devices.router.updated", "router", x.ID, x.Version, now,
				RouterEventData(x, []string{"management_address", "onboarding_state"}))}
		})
		if err != nil {
			return nil, mapStoreErr(err, domain.CodeRouterNotFound)
		}
	}
	creds, err := d.onboard.IssueDirectCredentials(ctx, t.UUID(), r.ID)
	if errors.Is(err, devapi.ErrRouterNotFound) {
		return nil, apperr.NotFound(domain.CodeRouterNotFound)
	}
	if err != nil {
		return nil, err
	}
	cfg, err := domain.BuildVRPDirectConfig(domain.VRPValues{RouterName: r.Hostname, ManagementAddress: mgmt,
		CollectorAddress: d.settings.CollectorAddress, CollectorPort: d.settings.CollectorPort, PollerAddress: d.settings.PollerAddress,
		SNMPUser: creds.SNMPUser, SNMPAuthPass: creds.SNMPAuthPassword, SNMPPrivPass: creds.SNMPPrivPassword, SamplingRate: sampling,
		NTPServer: d.settings.NTPServer})
	if err != nil {
		return nil, err
	}
	recordAudit(ctx, d.audit, d.store, d.logger, now, p, t, AuditDirectOnboarding, "router", r.ID.String(), "site:"+r.SiteID.String(),
		map[string]any{"credentials_rotated": true, "management_address": mgmt.String(), "sampling_rate": sampling})
	res := &DirectOnboardingResult{VRPConfig: cfg, RouterID: r.ID, Vendor: r.Vendor, ManagementAddress: mgmt.String(),
		CollectorPort: d.settings.CollectorPort, SNMPUser: creds.SNMPUser, SamplingRate: sampling}
	if res.CollectorPort <= 0 {
		res.CollectorPort = domain.VRPDefaultCollectorV9
	}
	if d.settings.CollectorAddress.IsValid() {
		s := d.settings.CollectorAddress.String()
		res.CollectorAddress = &s
	}
	return res, nil
}
