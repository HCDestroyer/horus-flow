package domain

import (
	"net/netip"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// Códigos de error de la importación (I1-28).
const (
	CodeRouterUnreachable    = "ROUTER_UNREACHABLE"
	CodeTLSFingerprintChange = "ROUTER_TLS_FINGERPRINT_CHANGED"
)

// RouterFacts es lo leído del router (solo lectura) para proponer prefijos.
type RouterFacts struct {
	Version       string
	IPPools       []IPPool
	IPv6Pools     []IPv6Pool
	PPPProfiles   []PPPProfile
	DHCPv6Servers []DHCPv6Server
	Addresses     []IfAddress
}

// IPPool es una fila de /ip/pool (ranges: "a-b,c-d" o CIDR).
type IPPool struct{ Name, Ranges string }

// IPv6Pool es una fila de /ipv6/pool.
type IPv6Pool struct {
	Name         string
	Prefix       string
	PrefixLength int
}

// PPPProfile es una fila de /ppp/profile (pools IPv6).
type PPPProfile struct {
	Name      string
	PDPool    string // dhcpv6-pd-pool
	LinkPool  string // remote-ipv6-prefix-pool
	LinkReuse bool   // remote-ipv6-prefix-reuse
}

// DHCPv6Server es una fila de /ipv6/dhcp-server.
type DHCPv6Server struct{ Name, Interface, PrefixPool, AddressPool string }

// IfAddress es una dirección de interfaz (/ip/address o /ipv6/address).
type IfAddress struct {
	Address   string // con bits de host: 10.30.0.1/24
	Interface string
	Dynamic   bool
	Disabled  bool
}

// ImportItem es una entrada de PrefixImportPreview.
type ImportItem struct {
	Prefix                  netip.Prefix
	Origin                  string // ip_pool | ipv6_pool | interface_address
	OriginName              string
	SuggestedRole           string
	SuggestedAssignmentMode string
	DelegatedPrefixLength   *int
	SuggestedIPv6ClientLen  *int
	IPv6PoolUsage           *string
	Diff                    string // new | exists | overlaps
	ExistingID              *uuid.UUID
}

// CoverRange devuelve el menor prefijo que contiene [a, b].
func CoverRange(a, b netip.Addr) (netip.Prefix, bool) {
	if !a.IsValid() || !b.IsValid() || a.Is4() != b.Is4() || b.Less(a) {
		return netip.Prefix{}, false
	}
	for bits := a.BitLen(); bits >= 0; bits-- {
		p, err := a.Prefix(bits)
		if err == nil && p.Contains(b) {
			return p, true
		}
	}
	return netip.Prefix{}, false
}

// poolPrefixes convierte los ranges de un /ip/pool en prefijos.
func poolPrefixes(ranges string) []netip.Prefix {
	var out []netip.Prefix
	for _, part := range strings.Split(ranges, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			out = append(out, p.Masked())
			continue
		}
		lo, hi, ok := strings.Cut(part, "-")
		if !ok {
			hi = lo
		}
		a, err1 := netip.ParseAddr(strings.TrimSpace(lo))
		b, err2 := netip.ParseAddr(strings.TrimSpace(hi))
		if err1 != nil || err2 != nil {
			continue
		}
		if p, ok := CoverRange(a, b); ok {
			out = append(out, p)
		}
	}
	return out
}

func intp(v int) *int       { return &v }
func strp(v string) *string { return &v }

// clientLen propone ipv6_client_len desde el prefix-length del pool.
func clientLen(pl int) *int {
	if slices.Contains(IPv6ClientLens, pl) {
		return intp(pl)
	}
	return nil
}

// ipv6Usage clasifica un pool IPv6 según /ppp/profile y /ipv6/dhcp-server
// (docs/vendors/mikrotik.md §11.3.1 y §11.4).
func ipv6Usage(name string, f RouterFacts) string {
	for _, p := range f.PPPProfiles {
		if p.PDPool == name {
			return "dhcpv6_pd"
		}
	}
	for _, s := range f.DHCPv6Servers {
		if s.PrefixPool == name {
			return "dhcpv6_pd"
		}
	}
	for _, p := range f.PPPProfiles {
		if p.LinkPool == name {
			if p.LinkReuse {
				return "ppp_link_shared"
			}
			return "ppp_link"
		}
	}
	for _, s := range f.DHCPv6Servers {
		if s.AddressPool == name {
			return "dhcpv6_address"
		}
	}
	return "unused"
}

// skipAddress descarta direcciones que nunca son de clientes ni de gestión
// del ISP: túnel de Horus, loopback, link-local y /32 (/128) sueltas.
func skipAddress(p netip.Prefix, iface string, exclude []netip.Prefix) bool {
	if iface == "wg-horus" || p.Addr().IsLoopback() || p.Addr().IsLinkLocalUnicast() || p.Addr().IsMulticast() || p.IsSingleIP() {
		return true
	}
	return overlapsAny(p, exclude)
}

func overlapsAny(p netip.Prefix, set []netip.Prefix) bool {
	for _, x := range set {
		if x.Overlaps(p) {
			return true
		}
	}
	return false
}

// BuildImport propone prefijos a partir de lo leído (I1-28 criterio 1):
// pools IPv4 → Clientes (dinámicos); pools IPv6 → Clientes con el tamaño
// delegado (enlace PPP compartido → Infraestructura); redes de interfaces no
// cubiertas por un pool → Infraestructura (gestión). Calcula la diferencia
// con los prefijos existentes del nodo. exclude son los rangos de túneles de
// Horus (nunca se proponen).
func BuildImport(f RouterFacts, existing []ClientPrefix, exclude []netip.Prefix) []ImportItem {
	var items []ImportItem
	seen := map[netip.Prefix]bool{}
	add := func(it ImportItem) {
		if seen[it.Prefix] {
			return
		}
		seen[it.Prefix] = true
		it.Diff = "new"
		for i := range existing {
			e := existing[i]
			switch {
			case e.Prefix == it.Prefix:
				it.Diff, it.ExistingID = "exists", &existing[i].ID
			case e.Prefix.Overlaps(it.Prefix) && it.Diff == "new":
				it.Diff, it.ExistingID = "overlaps", &existing[i].ID
			}
		}
		items = append(items, it)
	}
	var pools []netip.Prefix
	for _, p := range f.IPPools {
		for _, pfx := range poolPrefixes(p.Ranges) {
			if overlapsAny(pfx, exclude) {
				continue
			}
			pools = append(pools, pfx)
			add(ImportItem{Prefix: pfx, Origin: "ip_pool", OriginName: p.Name, SuggestedRole: "customers", SuggestedAssignmentMode: "dynamic"})
		}
	}
	for _, p := range f.IPv6Pools {
		pfx, err := netip.ParsePrefix(p.Prefix)
		if err != nil || !pfx.Addr().Is6() {
			continue
		}
		pfx = pfx.Masked()
		pools = append(pools, pfx)
		usage := ipv6Usage(p.Name, f)
		role := "customers"
		if usage == "ppp_link_shared" {
			role = "infrastructure" // /64 compartido: no identifica a nadie (§11.3.1)
		}
		it := ImportItem{Prefix: pfx, Origin: "ipv6_pool", OriginName: p.Name, SuggestedRole: role, SuggestedAssignmentMode: "dynamic",
			IPv6PoolUsage: strp(usage)}
		if p.PrefixLength > 0 {
			it.DelegatedPrefixLength = intp(p.PrefixLength)
			it.SuggestedIPv6ClientLen = clientLen(p.PrefixLength)
		}
		add(it)
	}
	for _, a := range f.Addresses {
		if a.Dynamic || a.Disabled {
			continue
		}
		pfx, err := netip.ParsePrefix(a.Address)
		if err != nil {
			continue
		}
		pfx = pfx.Masked()
		if skipAddress(pfx, a.Interface, exclude) {
			continue
		}
		covered := false
		for _, p := range pools {
			if p.Overlaps(pfx) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		it := ImportItem{Prefix: pfx, Origin: "interface_address", OriginName: a.Interface, SuggestedRole: "infrastructure",
			SuggestedAssignmentMode: "static"}
		if pfx.Addr().Is6() {
			it.SuggestedIPv6ClientLen = intp(64)
		}
		add(it)
	}
	slices.SortStableFunc(items, func(x, y ImportItem) int {
		if x.Prefix.Addr().Is4() != y.Prefix.Addr().Is4() {
			if x.Prefix.Addr().Is4() {
				return -1
			}
			return 1
		}
		return x.Prefix.Addr().Compare(y.Prefix.Addr())
	})
	return items
}
