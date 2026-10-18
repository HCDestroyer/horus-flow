package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	viaapi "github.com/hcdestroyer/horus-flow/services/wireguard/api"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/tunnelcfg"
)

// ViaRequest pide al repositorio la dirección Horus de un equipo vía router.
type ViaRequest struct {
	ServerID uuid.UUID
	RouterID uuid.UUID
	DeviceID uuid.UUID
	Target   netip.Addr
	Subnet   *viaapi.ViaSubnet
	At       time.Time
}

// ViaStore es la parte del repositorio de las direcciones Horus vía router
// (ADR-0032).
type ViaStore interface {
	// EnsureViaMapping asigna o reutiliza la dirección Horus del equipo (la
	// misma mientras viva el equipo; cambia solo al entrar o salir de una red
	// declarada) y sube la versión deseada de los agentes si cambió algo.
	EnsureViaMapping(ctx context.Context, t pgdb.TenantID, req ViaRequest) (*domain.ViaMapping, bool, error)
	// ReleaseViaMapping libera la dirección (cuarentena 24 h) y, si era la
	// última de un bloque, el bloque.
	ReleaseViaMapping(ctx context.Context, t pgdb.TenantID, id uuid.UUID, at time.Time) error
	// ViaMappingOf devuelve la dirección viva del equipo (domain.ErrNotFound si no).
	ViaMappingOf(ctx context.Context, t pgdb.TenantID, deviceID uuid.UUID) (*domain.ViaMapping, error)
	// GetViaMapping devuelve una asignación (viva o no) del tenant.
	GetViaMapping(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.ViaMapping, error)
	// AllViaMappings devuelve las asignaciones vivas de la plataforma.
	AllViaMappings(ctx context.Context) ([]domain.ViaMapping, error)
}

var _ viaapi.ViaPaths = (*Service)(nil)

// sFlowPort es el puerto estándar de sFlow (el colector lo reconoce en
// cualquiera de sus puertos; 6343 es el que se abre en el router).
const sFlowPort = 6343

// EnsureViaPath implementa api.ViaPaths: valida el router del sitio (del
// mismo ISP, router y con túnel), asigna la dirección Horus del equipo, pide
// a los agentes que la enruten por ese túnel y genera la configuración del
// router (NAT 1:1 y cortafuegos mínimo).
func (s *Service) EnsureViaPath(ctx context.Context, req viaapi.ViaPathRequest) (viaapi.ViaPath, error) {
	d, err := s.devices()
	if err != nil {
		return viaapi.ViaPath{}, err
	}
	r, err := d.GetRouter(ctx, req.TenantID, req.RouterID)
	if errors.Is(err, devapi.ErrRouterNotFound) {
		return viaapi.ViaPath{}, viaapi.ErrViaRouterInvalid
	}
	if err != nil {
		return viaapi.ViaPath{}, err
	}
	if r.DeviceType == devapi.DeviceTypeSwitch || !devapi.HasTunnel(r.ConnectionMode) || r.ID == req.DeviceID {
		return viaapi.ViaPath{}, viaapi.ErrViaRouterInvalid
	}
	target := req.Target.Unmap()
	if !target.Is4() {
		return viaapi.ViaPath{}, fmt.Errorf("%w: only IPv4 management addresses", viaapi.ErrViaRouterInvalid)
	}
	peer, err := s.EnsurePeer(ctx, r)
	if err != nil {
		if errors.Is(err, domain.ErrPoolExhausted) {
			return viaapi.ViaPath{}, viaapi.ErrViaPoolExhausted
		}
		return viaapi.ViaPath{}, err
	}
	t := pgdb.TenantID(req.TenantID)
	now := s.now()
	m, changed, err := s.o.Store.EnsureViaMapping(ctx, t, ViaRequest{ServerID: s.o.Hub.ID, RouterID: r.ID, DeviceID: req.DeviceID,
		Target: target, Subnet: req.Subnet, At: now})
	switch {
	case errors.Is(err, domain.ErrViaTargetInUse):
		return viaapi.ViaPath{}, viaapi.ErrViaTargetInUse
	case errors.Is(err, domain.ErrPoolExhausted):
		return viaapi.ViaPath{}, viaapi.ErrViaPoolExhausted
	case err != nil:
		return viaapi.ViaPath{}, err
	}
	if changed {
		s.Kick()
	}
	out := viaapi.ViaPath{Address: m.Mapped.Addr(), Transport: transportName(peer), ProtectionLevel: r.ProtectionLevel, RouterName: r.Name,
		PollerAddress: s.o.CollectorIP, ServicesCIDR: s.o.Hub.ServicesCIDR, CollectorAddress: s.o.CollectorIP, CollectorPort: sFlowPort,
		TunnelStatus: peer.Status, At: now}
	v := tunnelcfg.ViaValues{Vendor: r.Vendor, Transport: transportName(peer), RouterName: r.Name, DeviceID: req.DeviceID.String(),
		DeviceName: req.DeviceName, Target: target, Mapped: m.Mapped.Addr(), ServicesCIDR: s.o.Hub.ServicesCIDR, CollectorIP: s.o.CollectorIP,
		CollectorPort: sFlowPort, SFlow: req.SFlow}
	if m.ParentID != nil {
		parent, err := s.o.Store.GetViaMapping(ctx, t, *m.ParentID)
		if err != nil {
			return viaapi.ViaPath{}, fmt.Errorf("via subnet block: %w", err)
		}
		blk := parent.Mapped
		out.Block = &blk
		v.SubnetID, v.Subnet, v.Block = parent.SubnetID.String(), parent.Target, parent.Mapped
	}
	cfg, err := tunnelcfg.ViaRouter(v)
	if err != nil {
		return viaapi.ViaPath{}, err
	}
	out.RouterConfig = viaapi.RouterConfig{Language: cfg.Language, Title: cfg.Title, Config: cfg.Config, Notes: cfg.Notes, Warnings: cfg.Warnings}
	if p, ok := d.(devapi.ViaProjector); ok {
		if err := p.SetViaAddress(ctx, req.TenantID, req.DeviceID, r.ID, target, m.Mapped.Addr()); err != nil &&
			!errors.Is(err, devapi.ErrRouterNotFound) {
			return viaapi.ViaPath{}, fmt.Errorf("project via address: %w", err)
		}
	}
	return out, nil
}

// reconcileVia libera las direcciones Horus de equipos dados de baja, que ya
// no van vía router o cuyo router o IP privada cambió (cuarentena de 24 h)
// y proyecta en devices la dirección de los que siguen. Fail-static: sin la
// lista de devices no libera nada (la llama Reconcile con la lista ya leída).
func (s *Service) reconcileVia(ctx context.Context, d devapi.Onboarding, routers []devapi.RouterInfo) error {
	maps, err := s.o.Store.AllViaMappings(ctx)
	if err != nil {
		return err
	}
	byID := make(map[string]devapi.RouterInfo, len(routers))
	for _, r := range routers {
		byID[routerKey(r.TenantID, r.ID)] = r
	}
	proj, _ := d.(devapi.ViaProjector)
	for i := range maps {
		m := &maps[i]
		if m.Kind != domain.ViaKindDevice || m.DeviceID == nil {
			continue // los bloques se liberan con su último switch
		}
		dev, ok := byID[routerKey(m.TenantID, *m.DeviceID)]
		router, rok := byID[routerKey(m.TenantID, m.RouterID)]
		live := ok && rok && dev.ConnectionMode == devapi.ConnectionViaRouter && dev.ViaRouterID != nil && *dev.ViaRouterID == m.RouterID &&
			dev.ManagementAddress != nil && dev.ManagementAddress.Unmap() == m.Target.Addr() && devapi.HasTunnel(router.ConnectionMode)
		if !live {
			if err := s.o.Store.ReleaseViaMapping(ctx, pgdb.TenantID(m.TenantID), m.ID, s.now()); err != nil && !errors.Is(err, domain.ErrNotFound) {
				return err
			}
			s.o.Logger.InfoContext(ctx, "via-router address released", slog.String("device_id", m.DeviceID.String()),
				slog.String("router_id", m.RouterID.String()))
			s.Kick()
			continue
		}
		if proj != nil && (dev.ViaAddress == nil || dev.ViaAddress.Unmap() != m.Mapped.Addr()) {
			if err := proj.SetViaAddress(ctx, m.TenantID, *m.DeviceID, m.RouterID, m.Target.Addr(), m.Mapped.Addr()); err != nil &&
				!errors.Is(err, devapi.ErrRouterNotFound) {
				s.o.Logger.WarnContext(ctx, "via-router address not projected", slog.String("device_id", m.DeviceID.String()), slog.Any("error", err))
			}
		}
	}
	return nil
}
