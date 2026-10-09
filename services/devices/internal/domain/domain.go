// Package domain contiene el modelo y las reglas puras del inventario por
// ISP (docs/database.md §2.2, ADR-0018, D1, D6, D12, D15): nodos (sites),
// router principal, realms y prefijos de clientes.
package domain

import (
	"errors"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound lo devuelve el repositorio cuando no hay fila en el tenant.
var ErrNotFound = errors.New("devices: not found")

// Códigos de error del contrato (ErrorCode).
const (
	CodeSiteNotFound         = "SITE_NOT_FOUND"
	CodeSiteNotEmpty         = "SITE_NOT_EMPTY"
	CodeRouterNotFound       = "ROUTER_NOT_FOUND"
	CodeRouterPrimaryExists  = "ROUTER_PRIMARY_EXISTS"
	CodeClientPrefixNotFound = "CLIENT_PREFIX_NOT_FOUND"
	CodeClientPrefixOverlap  = "CLIENT_PREFIX_OVERLAP"
)

// Avisos del router.
const WarningRouterOSUnsupported = "routeros_version_unsupported"

// Site es un nodo (sitio) del ISP.
type Site struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	ParentID        *uuid.UUID
	Code            *string
	Name            string
	Kind            string
	Address         *string
	Latitude        *float64
	Longitude       *float64
	Timezone        *string
	Tags            []string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       *time.Time
	Version         int
	PrivateRealmID  uuid.UUID
	PrimaryRouterID *uuid.UUID
	PrefixCount     int
}

// DiscoveryMode: sin prefijos el tráfico cuenta para el nodo, no crea clientes.
func (s *Site) DiscoveryMode() bool { return s.PrefixCount == 0 }

// Router es un router MikroTik registrado.
type Router struct {
	ID                      uuid.UUID
	TenantID                uuid.UUID
	SiteID                  uuid.UUID
	Hostname                string
	DisplayName             *string
	Vendor                  string
	Model                   *string
	IsPrimary               bool
	AdminState              string
	OnboardingState         string
	RouterOSVersion         *string
	RouterOSVersionDetected *string
	RouterOSVersionOK       *bool
	TunnelAddress           *string
	WireguardPeerID         *uuid.UUID
	Tags                    []string
	CreatedAt               time.Time
	UpdatedAt               time.Time
	DeletedAt               *time.Time
	Version                 int
}

// Warnings devuelve los avisos del router.
func (r *Router) Warnings() []string {
	w := []string{}
	if r.RouterOSVersionOK != nil && !*r.RouterOSVersionOK {
		w = append(w, WarningRouterOSUnsupported)
	}
	return w
}

// Realm es el espacio donde una IP es única.
type Realm struct {
	ID     uuid.UUID
	Kind   string // public | node_private
	SiteID *uuid.UUID
}

// ClientPrefix es un prefijo de clientes de un nodo.
type ClientPrefix struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	SiteID         uuid.UUID
	RealmID        uuid.UUID
	RealmKind      string
	Prefix         netip.Prefix
	Role           string
	DefaultKind    string
	AssignmentMode string
	IPv6ClientLen  *int
	Source         string
	Confirmed      bool
	Note           *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      *time.Time
	Version        int
}

// Enumerados.
var (
	SiteKinds       = []string{"node", "region", "datacenter", "other"}
	AdminStates     = []string{"active", "maintenance", "decommissioned"}
	PrefixRoles     = []string{"customers", "infrastructure", "excluded"}
	AssignmentModes = []string{"static", "dynamic", "unknown"}
	CustomerKinds   = []string{"residential", "commercial", "unknown"}
	PrefixSources   = []string{"manual", "routeros_api", "discovery_confirmed"}
	IPv6ClientLens  = []int{48, 56, 60, 64}
)

var routerOSRe = regexp.MustCompile(`^7\.[0-9]+(\.[0-9]+)?$`)

// MinRouterOS es la versión mínima soportada (D15).
const MinRouterOSMinor = 12

// ValidRouterOSVersion comprueba el patrón 7.x[.y] (D15: solo RouterOS 7).
func ValidRouterOSVersion(v string) bool { return routerOSRe.MatchString(v) }

// RouterOSSupported indica si v ≥ 7.12 (una versión inferior se registra con aviso).
func RouterOSSupported(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) < 2 || parts[0] != "7" {
		return false
	}
	minor, err := strconv.Atoi(parts[1])
	return err == nil && minor >= MinRouterOSMinor
}

// privateRanges son los rangos que van al realm privado del nodo (RFC 1918,
// CGNAT 100.64.0.0/10 y ULA IPv6; D12: NAT en el router principal).
var privateRanges = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fc00::/7"),
}

// IsPrivate indica si p está entero dentro de un rango privado/CGNAT.
func IsPrivate(p netip.Prefix) bool {
	for _, r := range privateRanges {
		if r.Bits() <= p.Bits() && r.Contains(p.Addr()) {
			return true
		}
	}
	return false
}

// RealmKindFor decide el realm de un prefijo: privado → node_private del
// nodo; público → public del ISP.
func RealmKindFor(p netip.Prefix) string {
	if IsPrivate(p) {
		return "node_private"
	}
	return "public"
}

// ParseCanonicalPrefix interpreta un CIDR y exige que no tenga bits de host.
func ParseCanonicalPrefix(s string) (netip.Prefix, bool) {
	p, err := netip.ParsePrefix(strings.TrimSpace(s))
	if err != nil {
		return netip.Prefix{}, false
	}
	if !p.IsValid() || p.Addr().Is4In6() || p.Addr().Zone() != "" || p.Masked() != p {
		return netip.Prefix{}, false
	}
	return p, true
}
