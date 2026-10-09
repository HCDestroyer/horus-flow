package api

import (
	"context"
	"net/netip"

	"github.com/google/uuid"
)

// ServiceKiosks → KioskChecker.
const ServiceKiosks = "auth.KioskService"

// KioskStatus es el estado vigente de un kiosco (I1-14; security.md §5.5).
type KioskStatus struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	Name     string
	// Active: enrolado, no revocado ni caducado.
	Active                bool
	Status                string // pending_enrollment | active | revoked | expired
	AllowedCIDRs          []netip.Prefix
	DashboardIDs          []uuid.UUID
	PlaylistID            *uuid.UUID
	ShowPersonalData      bool
	CriticalFindingBanner bool
}

// AllowsIP indica si ip está dentro de allowed_cidrs (sin CIDR = cualquiera).
func (k *KioskStatus) AllowsIP(ip string) bool {
	if len(k.AllowedCIDRs) == 0 {
		return true
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range k.AllowedCIDRs {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// KioskChecker devuelve el estado de un kiosco (gateway: revocación,
// allowed_cidrs y cierre del WebSocket con 4409; analytics: dashboards
// asignados y política de datos personales).
type KioskChecker interface {
	CheckKiosk(ctx context.Context, tenantID, kioskID uuid.UUID) (*KioskStatus, error)
}
