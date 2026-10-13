package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// Delegation implementa api.Delegator: tokens de corta vida para actuar en
// nombre de un usuario con un subconjunto de sus permisos actuales (servidor
// MCP del asistente de IA). Sin sesión: el token es `typ = service`; la
// comprobación de usuario, membresía e ISP activos se hace en cada emisión.
type Delegation struct {
	store  Store
	signer *authz.Signer
}

// NewDelegation crea el emisor. signer debe tener una vida corta (2 min).
func NewDelegation(store Store, signer *authz.Signer) *Delegation {
	return &Delegation{store: store, signer: signer}
}

// DelegatedToken implementa api.Delegator.
func (d *Delegation) DelegatedToken(ctx context.Context, req api.DelegationRequest) (string, error) {
	if req.UserID == uuid.Nil || req.TenantID == uuid.Nil || len(req.Permissions) == 0 {
		return "", api.ErrDelegationDenied
	}
	u, err := d.store.UserByID(ctx, req.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", api.ErrDelegationDenied
	}
	if err != nil {
		return "", err
	}
	if u.Status != domain.UserActive {
		return "", api.ErrDelegationDenied
	}
	ms, err := d.store.Memberships(ctx, req.UserID)
	if err != nil {
		return "", err
	}
	var m *domain.Membership
	for i := range ms {
		if ms[i].Tenant.ID == req.TenantID {
			m = &ms[i]
		}
	}
	// Sin acceso de plataforma (via_platform): solo miembros activos de un ISP activo.
	if m == nil || m.Status != domain.MembershipActive || m.Tenant.Status != domain.TenantActive {
		return "", api.ErrDelegationDenied
	}
	perms := map[string][]string{}
	for p, scopes := range m.EffectivePermissions() {
		if slices.Contains(req.Permissions, p) {
			perms[p] = scopes
		}
	}
	if len(perms) == 0 {
		return "", api.ErrDelegationDenied
	}
	c := authz.Claims{Typ: authz.TypeService, Scope: authz.ScopeTenant, TID: req.TenantID.String(), Perms: perms}
	c.Subject = req.UserID.String()
	c.ID = fmt.Sprintf("%s:%s", req.Purpose, uuid.Must(uuid.NewV7()))
	tok, _, err := d.signer.Sign(c)
	if err != nil {
		return "", err
	}
	return tok, nil
}
