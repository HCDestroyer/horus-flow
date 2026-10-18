package app

import (
	"context"
	"errors"
	"net/netip"
	"strings"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// AuditSwitchOnboarding es la acción auditada al generar el alta de un
// switch (rota su credencial SNMP).
const AuditSwitchOnboarding = "devices.switch_onboarding.created"

// SwitchOnboardingInput es el cuerpo de POST /routers/{id}/switch-onboarding.
type SwitchOnboardingInput struct {
	ManagementAddress *string `json:"management_address"`
	// SFlow: incluir la configuración sFlow (solo Huawei serie S) y activar
	// la fuente secundaria; false la desactiva; nil no cambia.
	SFlow        *bool `json:"sflow"`
	SamplingRate *int  `json:"sampling_rate"`
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
}

// CreateSwitch genera el alta de un switch en modo directo: fija la
// dirección de gestión (vuelve a pending_configuration si cambia), emite una
// credencial SNMP nueva (v3 de solo lectura o, en SwOS, una comunidad v2c),
// opcionalmente activa sFlow y devuelve la configuración que pega el
// operador. Mismas reglas de seguridad que el alta directa de Huawei: la
// dirección es única en la plataforma una vez verificada y nunca de Horus.
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
	var mgmt netip.Addr
	switch {
	case in.ManagementAddress != nil:
		a, err := netip.ParseAddr(strings.TrimSpace(*in.ManagementAddress))
		if err != nil || !domain.ValidManagementAddress(a) || managementDenied(a, d.settings.Deny) {
			return nil, apperr.Validation(apperr.Field("management_address", "INVALID_VALUE",
				"IP unicast del switch en la red de gestión (sin loopback, link-local, multicast ni direcciones de Horus)."))
		}
		mgmt = a.Unmap()
	case r.ManagementAddress != nil:
		mgmt, _ = netip.ParseAddr(*r.ManagementAddress)
	}
	if !mgmt.IsValid() {
		return nil, apperr.Validation(apperr.Field("management_address", "REQUIRED",
			"Indique la IP de gestión del switch (la que consultará Horus por SNMP)."))
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
	if r.ManagementAddress == nil || *r.ManagementAddress != mgmt.String() || r.SFlowEnabled != sflow {
		var changed []string
		err := d.store.SetRouterTunnel(ctx, t, r.ID, func(x *domain.Router) bool {
			changed = changed[:0]
			if x.ManagementAddress == nil || *x.ManagementAddress != mgmt.String() {
				s := mgmt.String()
				x.ManagementAddress = &s
				changed = append(changed, "management_address")
				if x.OnboardingState != devapi.OnboardingPending {
					x.OnboardingState = devapi.OnboardingPending // dirección nueva: hay que volver a verificar
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
	}
	creds, community, err := d.onboard.IssueSwitchCredentials(ctx, t.UUID(), r.ID, platform)
	if errors.Is(err, devapi.ErrRouterNotFound) {
		return nil, apperr.NotFound(domain.CodeRouterNotFound)
	}
	if err != nil {
		return nil, err
	}
	cfg, err := domain.BuildSwitchConfig(domain.SwitchValues{Name: r.Hostname, Platform: platform, ManagementAddress: mgmt,
		PollerAddress: d.settings.PollerAddress, CollectorAddress: d.settings.CollectorAddress, CollectorPort: d.settings.CollectorPort,
		SNMPUser: creds.SNMPUser, SNMPAuthPass: creds.SNMPAuthPassword, SNMPPrivPass: creds.SNMPPrivPassword, Community: community,
		SFlow: sflow, SamplingRate: sampling})
	if err != nil {
		return nil, err
	}
	recordAudit(ctx, d.audit, d.store, d.logger, now, p, t, AuditSwitchOnboarding, "router", r.ID.String(), "site:"+r.SiteID.String(),
		map[string]any{"credentials_rotated": true, "management_address": mgmt.String(), "platform": platform, "sflow_enabled": sflow})
	res := &SwitchOnboardingResult{SwitchConfig: cfg, RouterID: r.ID, Platform: platform, SNMPVersion: "3", ManagementAddress: mgmt.String(),
		SFlowEnabled: sflow}
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
