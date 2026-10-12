package app

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/domain"
)

// PermManage es el permiso de plataforma para editar el hub y sus rangos
// (x-reauth, x-audited).
const PermManage = "platform.wireguard.manage"

// Acciones de auditoría de la administración del hub (cadena de plataforma).
const (
	AuditHubUpdated = "platform.wireguard.hub_updated"
	AuditPoolAdded  = "platform.wireguard.pool_added"
	AuditPoolDelete = "platform.wireguard.pool_deleted"
)

// AuditMeta son los datos de la petición que van a la auditoría.
type AuditMeta struct {
	IP        string
	UserAgent string
	RequestID string
}

// PoolInput es el cuerpo de POST /platform/wireguard/hubs/{id}/pools.
type PoolInput struct {
	CIDR *string `json:"cidr"`
}

func platformPrincipal(ctx context.Context) (*authz.Principal, error) {
	p := authz.FromContext(ctx)
	if p == nil || p.Scope != authz.ScopePlatform {
		return nil, apperr.Forbidden(problem.CodeTokenScopeInvalid, "")
	}
	if !p.Has(PermManage) {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	return p, nil
}

func (s *Service) platformAudit(ctx context.Context, p *authz.Principal, meta AuditMeta, action, rtype, rid string, changes map[string]any) {
	s.audit(ctx, authapi.AuditEntry{ActorType: p.Type, ActorID: p.Subject, ViaPlatform: p.ViaPlatform, Action: action,
		ResourceType: rtype, ResourceID: rid, Scope: "platform", Outcome: "success", IP: meta.IP, UserAgent: meta.UserAgent,
		RequestID: meta.RequestID, Changes: changes})
}

func errHubNotFound() error {
	return &apperr.Error{Kind: apperr.KindNotFound, Code: domain.CodeHubNotFound, Title: "Hub WireGuard no encontrado"}
}

func errPool(kind apperr.Kind, code, title, detail string) error {
	e := &apperr.Error{Kind: kind, Code: code, Title: title, Detail: detail}
	if kind == apperr.KindInvalid {
		e.Fields = []problem.FieldError{apperr.Field("cidr", code, detail)}
	}
	return e
}

// fill completa los datos derivados del hub: clave pública del agente en
// proceso si la base aún no la tiene y capacidad de cada rango.
func (s *Service) fill(h *Hub) {
	if h.PublicKey == "" && h.ID == s.o.Hub.ID {
		h.PublicKey = s.o.HubPublicKey()
	}
	h.AddressesTotal = 0
	for i := range h.PoolInfo {
		h.PoolInfo[i].Total = domain.Capacity(h.PoolInfo[i].CIDR, h.ServicesCIDR)
		h.AddressesTotal += h.PoolInfo[i].Total
	}
}

// ListHubs lista los hubs con sus rangos y su ocupación (plataforma).
func (s *Service) ListHubs(ctx context.Context) ([]Hub, error) {
	hubs, err := s.o.Store.ListHubs(ctx)
	if err != nil {
		return nil, err
	}
	for i := range hubs {
		s.fill(&hubs[i])
	}
	return hubs, nil
}

func (s *Service) getHub(ctx context.Context, id uuid.UUID) (*Hub, error) {
	h, err := s.o.Store.GetHub(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, errHubNotFound()
	}
	if err != nil {
		return nil, err
	}
	s.fill(h)
	return h, nil
}

// validatePatch comprueba los campos del PATCH (todos los errores a la vez).
func validatePatch(in HubPatch) error {
	if in.Name == nil && in.Endpoint == nil && in.MTU == nil && in.Keepalive == nil {
		return apperr.New(apperr.KindInvalid, problem.CodeValidationFailed,
			"Indique al menos un campo: name, endpoint, mtu o persistent_keepalive_seconds.")
	}
	var fields []problem.FieldError
	if in.Name != nil && !domain.ValidHubName(*in.Name) {
		fields = append(fields, apperr.Field("name", "INVALID_VALUE", fmt.Sprintf("Entre 1 y %d caracteres imprimibles.", domain.MaxNameLen)))
	}
	if in.Endpoint != nil && !domain.ValidEndpoint(*in.Endpoint) {
		fields = append(fields, apperr.Field("endpoint", "INVALID_FORMAT",
			"Nombre DNS o IP pública, sin esquema, puerto ni ruta (p. ej. horus.isp.example o 203.0.113.10)."))
	}
	if in.MTU != nil && (*in.MTU < domain.MinMTU || *in.MTU > domain.MaxMTU) {
		fields = append(fields, apperr.Field("mtu", "OUT_OF_RANGE", fmt.Sprintf("Entre %d y %d.", domain.MinMTU, domain.MaxMTU)))
	}
	if in.Keepalive != nil && (*in.Keepalive < 0 || *in.Keepalive > domain.MaxKeepalive) {
		fields = append(fields, apperr.Field("persistent_keepalive_seconds", "OUT_OF_RANGE",
			fmt.Sprintf("Entre 0 (desactivado) y %d segundos.", domain.MaxKeepalive)))
	}
	if len(fields) > 0 {
		return apperr.Validation(fields...)
	}
	return nil
}

// UpdateHub edita el hub (If-Match = version). Los campos editados quedan
// marcados: el arranque deja de aplicar HORUS_WG_HUB_NAME/HORUS_WG_ENDPOINT.
// Los scripts de alta nuevos usan endpoint, MTU y keepalive del hub; los
// routers ya conectados no cambian hasta un script nuevo.
func (s *Service) UpdateHub(ctx context.Context, id uuid.UUID, version int, in HubPatch, meta AuditMeta) (*Hub, error) {
	p, err := platformPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	cur, err := s.getHub(ctx, id)
	if err != nil {
		return nil, err
	}
	if version != cur.Version {
		return nil, apperr.PreconditionFailed(cur)
	}
	if err := validatePatch(in); err != nil {
		return nil, err
	}
	h, err := s.o.Store.UpdateHub(ctx, id, version, in)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil, errHubNotFound()
	case errors.Is(err, ErrVersionChanged):
		if cur, err = s.getHub(ctx, id); err != nil {
			return nil, err
		}
		return nil, apperr.PreconditionFailed(cur)
	case errors.Is(err, ErrHubNameInUse):
		return nil, apperr.Conflict(problem.CodeAlreadyExists, "Ya hay otro hub con ese nombre.")
	case err != nil:
		return nil, err
	}
	s.fill(h)
	changes := map[string]any{}
	diff := func(field string, from, to any) {
		if from != to {
			changes[field] = map[string]any{"from": from, "to": to}
		}
	}
	diff(domain.FieldName, cur.Name, h.Name)
	diff(domain.FieldEndpoint, cur.Endpoint, h.Endpoint)
	diff(domain.FieldMTU, cur.MTU, h.MTU)
	diff(domain.FieldKeepalive, cur.Keepalive, h.Keepalive)
	s.platformAudit(ctx, p, meta, AuditHubUpdated, "wireguard_hub", id.String(), changes)
	return h, nil
}

// AddPool añade un rango de túneles al hub. Solo se añaden rangos: las
// direcciones asignadas son la identidad de cada router y no se mueven.
func (s *Service) AddPool(ctx context.Context, hubID uuid.UUID, in PoolInput, meta AuditMeta) (*Pool, error) {
	p, err := platformPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if in.CIDR == nil {
		return nil, apperr.Validation(apperr.Field("cidr", "REQUIRED", "Obligatorio."))
	}
	h, err := s.getHub(ctx, hubID)
	if err != nil {
		return nil, err
	}
	// La /32 es única en toda la plataforma: el solape se comprueba con los
	// rangos de todos los hubs (el EXCLUDE de ip_pool es global).
	hubs, err := s.o.Store.ListHubs(ctx)
	if err != nil {
		return nil, err
	}
	var existing []netip.Prefix
	for _, x := range hubs {
		existing = append(existing, x.Pools...)
	}
	cidr, err := domain.ValidateNewPool(*in.CIDR, h.ServicesCIDR, existing)
	if err != nil {
		return nil, poolError(err)
	}
	pool, err := s.o.Store.AddPool(ctx, hubID, cidr)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil, errHubNotFound()
	case errors.Is(err, ErrPoolOverlap):
		return nil, errPool(apperr.KindConflict, domain.CodePoolOverlap, "El rango se solapa con otro",
			fmt.Sprintf("%s se solapa con un rango existente.", cidr))
	case errors.Is(err, domain.ErrInvalidPool):
		return nil, errPool(apperr.KindInvalid, domain.CodePoolInvalid, "Rango de túnel no válido", err.Error())
	case err != nil:
		return nil, err
	}
	pool.Total = domain.Capacity(pool.CIDR, h.ServicesCIDR)
	s.platformAudit(ctx, p, meta, AuditPoolAdded, "wireguard_pool", pool.ID.String(),
		map[string]any{"hub_id": hubID.String(), "cidr": pool.CIDR.String(), "addresses_total": pool.Total})
	s.Kick()
	return pool, nil
}

func poolError(err error) error {
	var pe *domain.PoolError
	if !errors.As(err, &pe) {
		return err
	}
	if pe.Overlap {
		return errPool(apperr.KindConflict, domain.CodePoolOverlap, "El rango se solapa con otro", pe.Detail)
	}
	return errPool(apperr.KindInvalid, domain.CodePoolInvalid, "Rango de túnel no válido", pe.Detail)
}

// DeletePool quita un rango sin direcciones asignadas ni en cuarentena.
// Nunca el último del hub, ni uno de HORUS_WG_TUNNEL_CIDRS (el arranque lo
// volvería a crear), ni el que contiene la red de servicios.
func (s *Service) DeletePool(ctx context.Context, hubID, poolID uuid.UUID, meta AuditMeta) error {
	p, err := platformPrincipal(ctx)
	if err != nil {
		return err
	}
	h, err := s.getHub(ctx, hubID)
	if err != nil {
		return err
	}
	guard := func(pl Pool) error {
		if hubID == s.o.Hub.ID && slices.Contains(s.o.Hub.Pools, pl.CIDR) {
			return apperr.Conflict(problem.CodeConflict, fmt.Sprintf(
				"%s está en HORUS_WG_TUNNEL_CIDRS: quítelo de la configuración antes (si no, el arranque lo vuelve a crear).", pl.CIDR))
		}
		if h.ServicesCIDR.IsValid() && pl.CIDR.Overlaps(h.ServicesCIDR) {
			return apperr.Conflict(problem.CodeConflict, fmt.Sprintf("%s contiene la red de servicios %s (hub y colector).", pl.CIDR, h.ServicesCIDR))
		}
		return nil
	}
	pool, err := s.o.Store.DeletePool(ctx, hubID, poolID, s.now().Add(-domain.Quarantine), guard)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return errHubNotFound()
	case errors.Is(err, ErrPoolNotFound):
		return &apperr.Error{Kind: apperr.KindNotFound, Code: domain.CodePoolNotFound, Title: "Rango de túnel no encontrado"}
	case errors.Is(err, ErrLastPool):
		return apperr.Conflict(problem.CodeConflict, "Es el último rango del hub: añada otro antes de quitarlo.")
	case errors.Is(err, ErrPoolInUse):
		return errPool(apperr.KindConflict, domain.CodePoolInUse, "El rango tiene direcciones",
			"El rango tiene direcciones asignadas o liberadas hace menos de 24 h (cuarentena).")
	case err != nil:
		return err
	}
	s.platformAudit(ctx, p, meta, AuditPoolDelete, "wireguard_pool", pool.ID.String(),
		map[string]any{"hub_id": hubID.String(), "cidr": pool.CIDR.String()})
	s.Kick()
	return nil
}
