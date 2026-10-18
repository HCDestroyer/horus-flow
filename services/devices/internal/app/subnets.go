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
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// Acciones auditadas de las redes de gestión declaradas.
const (
	AuditManagementSubnetCreated = "devices.management_subnet.created"
	AuditManagementSubnetDeleted = "devices.management_subnet.deleted"
)

// subnetStore es lo que necesita Subnets del repositorio.
type subnetStore interface {
	GetRouter(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Router, error)
	ViaStore
	Emit(ctx context.Context, evs []outbox.Event) error
}

// Subnets gestiona las redes de gestión declaradas en un router del sitio
// (ADR-0032): los switches vía ese router con IP dentro de una red declarada
// comparten un bloque Horus del mismo tamaño y una sola regla NAT en el
// router. Las redes de un router no se solapan dentro del ISP; ISP distintos
// (o routers distintos) pueden repetir la misma red privada.
type Subnets struct {
	store  subnetStore
	deny   []netip.Prefix
	audit  func() (authapi.AuditRecorder, bool)
	logger *slog.Logger
	now    func() time.Time
}

// NewSubnets crea el servicio. deny son los rangos de Horus (túneles,
// instalación) que una red de gestión nunca puede tocar.
func NewSubnets(store subnetStore, deny []netip.Prefix, audit func() (authapi.AuditRecorder, bool), logger *slog.Logger,
	now func() time.Time,
) *Subnets {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if audit == nil {
		audit = func() (authapi.AuditRecorder, bool) { return nil, false }
	}
	return &Subnets{store: store, deny: deny, audit: audit, logger: logger, now: now}
}

func (s *Subnets) router(ctx context.Context, perm string, id uuid.UUID) (*domain.Router, pgdb.TenantID, error) {
	p, t, err := scope(ctx, perm)
	if err != nil {
		return nil, t, err
	}
	r, err := s.store.GetRouter(ctx, t, id)
	if err != nil || !siteAllowed(p, perm, r.SiteID) {
		return nil, t, apperr.NotFound(domain.CodeRouterNotFound)
	}
	if r.IsSwitch() {
		return nil, t, apperr.New(apperr.KindInvalid, domain.CodeSwitchNotSupported, "Las redes de gestión se declaran en el router del sitio, no en un switch.")
	}
	return r, t, nil
}

// List devuelve las redes declaradas del router.
func (s *Subnets) List(ctx context.Context, routerID uuid.UUID) ([]domain.ManagementSubnet, error) {
	r, t, err := s.router(ctx, "devices.read", routerID)
	if err != nil {
		return nil, err
	}
	return s.store.ListManagementSubnets(ctx, t, r.ID)
}

// SubnetInput es el cuerpo de POST /routers/{id}/management-subnets.
type SubnetInput struct {
	Prefix string `json:"prefix"`
}

// Create declara una red de gestión en el router.
func (s *Subnets) Create(ctx context.Context, routerID uuid.UUID, in SubnetInput) (*domain.ManagementSubnet, error) {
	r, t, err := s.router(ctx, "devices.update", routerID)
	if err != nil {
		return nil, err
	}
	p := authz.FromContext(ctx)
	pfx, err := netip.ParsePrefix(strings.TrimSpace(in.Prefix))
	if err != nil || !domain.ValidManagementSubnet(pfx) {
		return nil, apperr.Validation(apperr.Field("prefix", "INVALID_VALUE",
			"Red IPv4 privada (RFC 1918 o 100.64.0.0/10) en notación CIDR canónica, de /22 a /30 (p. ej. 192.168.88.0/24)."))
	}
	for _, d := range s.deny {
		if d.Overlaps(pfx) {
			return nil, apperr.Validation(apperr.Field("prefix", "INVALID_VALUE", "La red se solapa con direcciones de Horus (túneles o la propia instalación)."))
		}
	}
	cur, err := s.store.ListManagementSubnets(ctx, t, r.ID)
	if err != nil {
		return nil, err
	}
	if len(cur) >= domain.MaxManagementSubnets {
		return nil, apperr.Validation(apperr.Field("prefix", "TOO_MANY", "Máximo 16 redes de gestión por router."))
	}
	m := &domain.ManagementSubnet{ID: uuid.Must(uuid.NewV7()), TenantID: t.UUID(), RouterID: r.ID, Prefix: pfx, CreatedAt: s.now().UTC(),
		CreatedBy: p.Type + ":" + p.Subject}
	err = s.store.CreateManagementSubnet(ctx, t, m)
	switch {
	case errors.Is(err, ErrSubnetOverlap):
		return nil, apperr.Conflict(domain.CodeManagementSubnetOverlap, "La red se solapa con otra red de gestión declarada en este router.")
	case errors.Is(err, ErrRefNotFound):
		return nil, apperr.NotFound(domain.CodeRouterNotFound)
	case err != nil:
		return nil, err
	}
	recordAudit(ctx, s.audit, s.store, s.logger, m.CreatedAt, p, t, AuditManagementSubnetCreated, "router", r.ID.String(),
		"site:"+r.SiteID.String(), map[string]any{"subnet_id": m.ID.String(), "prefix": pfx.String()})
	return m, nil
}

// Delete quita una red declarada (409 si algún switch vía el router tiene su
// IP dentro: su NAT depende de ella).
func (s *Subnets) Delete(ctx context.Context, routerID, id uuid.UUID) error {
	r, t, err := s.router(ctx, "devices.update", routerID)
	if err != nil {
		return err
	}
	subs, err := s.store.ListManagementSubnets(ctx, t, r.ID)
	if err != nil {
		return err
	}
	var sub *domain.ManagementSubnet
	for i := range subs {
		if subs[i].ID == id {
			sub = &subs[i]
		}
	}
	if sub == nil {
		return apperr.NotFound(domain.CodeManagementSubnetNotFound)
	}
	n, err := s.store.ViaDevicesIn(ctx, t, r.ID, sub.Prefix)
	if err != nil {
		return err
	}
	if n > 0 {
		return apperr.Conflict(domain.CodeManagementSubnetInUse,
			"Hay switches vía este router con su IP dentro de la red: su NAT depende de ella. Muévalos o dé de baja los switches antes.")
	}
	if err := s.store.DeleteManagementSubnet(ctx, t, r.ID, id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return apperr.NotFound(domain.CodeManagementSubnetNotFound)
		}
		return err
	}
	recordAudit(ctx, s.audit, s.store, s.logger, s.now().UTC(), authz.FromContext(ctx), t, AuditManagementSubnetDeleted, "router", r.ID.String(),
		"site:"+r.SiteID.String(), map[string]any{"subnet_id": id.String(), "prefix": sub.Prefix.String()})
	return nil
}
