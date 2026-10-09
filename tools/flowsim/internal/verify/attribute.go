package verify

import (
	"fmt"
	"net/netip"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/expect"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/flow"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/signals"
)

type role uint8

const (
	roleNone role = iota
	roleCustomer
	roleInfra
	roleExcluded
	roleOther // cliente de otro nodo del mismo ISP
)

type rolePrefix struct {
	p netip.Prefix
	r role
}

// Attributor aplica el algoritmo de docs/traffic-model.md §4.3/§4.6 con los
// prefijos que declara expected.json (trie LPM simplificado: lista ordenada).
type Attributor struct {
	prefixes  []rolePrefix
	tunnel    map[netip.Addr]bool
	upstream  uint32
	tunnelIf  uint32
	transitIf uint32
	clientLen int
}

// NewAttributor crea el atribuidor del exportador idx de expected.json.
func NewAttributor(e *expect.Expected, idx int) (*Attributor, error) {
	ex := e.Exporters[idx]
	a := &Attributor{
		tunnel:    map[netip.Addr]bool{},
		upstream:  ex.Interfaces.Upstream,
		tunnelIf:  ex.Interfaces.Tunnel,
		transitIf: ex.Interfaces.Transit,
		clientLen: ex.IPv6ClientLen,
	}
	add := func(list []string, r role) error {
		for _, s := range list {
			p, err := netip.ParsePrefix(s)
			if err != nil {
				return fmt.Errorf("prefijo %q: %w", s, err)
			}
			a.prefixes = append(a.prefixes, rolePrefix{p.Masked(), r})
		}
		return nil
	}
	if err := add(ex.Prefixes.Customers, roleCustomer); err != nil {
		return nil, err
	}
	if err := add(ex.Prefixes.Infrastructure, roleInfra); err != nil {
		return nil, err
	}
	if err := add(ex.Prefixes.Excluded, roleExcluded); err != nil {
		return nil, err
	}
	for j, o := range e.Exporters {
		if j == idx {
			continue
		}
		if err := add(o.Prefixes.Customers, roleOther); err != nil {
			return nil, err
		}
	}
	for _, s := range []string{ex.ExporterIP, ex.CollectorIP} {
		if s == "" {
			continue // p. ej. captura real con el colector dentro de la red de clientes
		}
		ip, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("IP de túnel %q: %w", s, err)
		}
		a.tunnel[ip] = true
	}
	return a, nil
}

// lookup devuelve el rol del prefijo más específico que contiene la IP. A
// igual longitud gana el del propio nodo (orden de inserción).
func (a *Attributor) lookup(ip netip.Addr) role {
	best, bits := roleNone, -1
	for _, rp := range a.prefixes {
		if rp.p.Bits() > bits && rp.p.Contains(ip) {
			best, bits = rp.r, rp.p.Bits()
		}
	}
	return best
}

func (a *Attributor) key(ip netip.Addr) string {
	if ip.Is4() {
		return ip.String()
	}
	p, _ := ip.Prefix(a.clientLen)
	return p.String()
}

func (a *Attributor) customerEdge(ifIndex uint32) bool {
	return ifIndex != 0 && ifIndex != a.upstream && ifIndex != a.tunnelIf && ifIndex != a.transitIf
}

var linkMulticastV6 = netip.MustParsePrefix("ff02::/16")

// linkScopeV6 indica una IPv6 de enlace local (fe80::/10) o multicast de
// alcance de enlace (ff02::/16): nunca es cliente (docs/traffic-model.md §4.8.3).
func linkScopeV6(a netip.Addr) bool {
	return a.Is6() && !a.Is4In6() && (a.IsLinkLocalUnicast() || linkMulticastV6.Contains(a))
}

// postNATClient devuelve la IP privada del cliente de una bajada con NAT en
// el router principal: postNATDestinationIPv4Address (IE 226) presente,
// distinta de dst y dentro de un prefijo de cliente del nodo.
func (a *Attributor) postNATClient(r *flow.Record) (netip.Addr, bool) {
	p := r.PostNATDst
	if !p.IsValid() || p.IsUnspecified() || p == r.DstIP {
		return netip.Addr{}, false
	}
	return p, a.lookup(p) == roleCustomer
}

// Attribute atribuye un flujo decodificado con la regla de
// docs/traffic-model.md §4.4 (NAT en el router principal) sobre la tabla de
// §4.6:
//
//  1. src en un prefijo de cliente → cliente = src, subida;
//  2. si no, IE 226 presente, distinta de dst y en un prefijo de cliente →
//     cliente = IE 226, bajada;
//  3. si no, dst en un prefijo de cliente → cliente = dst, bajada;
//  4. si no, unknown (o transit/infrastructure según §4.6).
//
// Antes se descartan los rangos excluidos y el tráfico del túnel, y el de
// enlace local IPv6 es infraestructura (§4.8.3); si ambos extremos son
// clientes del nodo el flujo es internal. En IPv6 el paso 2 nunca ocurre
// (la plantilla 259 no trae campos NAT, §4.8.4).
func (a *Attributor) Attribute(r *flow.Record) signals.Attribution {
	src, dst := a.lookup(r.SrcIP), a.lookup(r.DstIP)
	natClient, natOK := a.postNATClient(r)
	switch {
	case src == roleExcluded || dst == roleExcluded:
		return signals.Attribution{Status: signals.StatusExcluded}
	case a.tunnel[r.SrcIP] || a.tunnel[r.DstIP]:
		return signals.Attribution{Status: signals.StatusTunnel}
	case linkScopeV6(r.SrcIP) || linkScopeV6(r.DstIP):
		// §4.8.3: enlace local (ND, RA, DHCPv6) y multicast de enlace.
		return signals.Attribution{Status: signals.StatusInfrastructure}
	case src == roleCustomer && (dst == roleCustomer || natOK):
		return signals.Attribution{Status: signals.StatusInternal, Client: a.key(r.SrcIP), Upload: true, Rule: expect.RuleInternal}
	case src == roleCustomer:
		return signals.Attribution{Status: signals.StatusAttributed, Client: a.key(r.SrcIP), Upload: true, Rule: expect.RuleUploadSrc}
	case natOK:
		return signals.Attribution{Status: signals.StatusAttributed, Client: a.key(natClient), Rule: expect.RuleDownloadPostNATDst}
	case dst == roleCustomer:
		return signals.Attribution{Status: signals.StatusAttributed, Client: a.key(r.DstIP), Rule: expect.RuleDownloadDst}
	case src == roleOther || dst == roleOther:
		return signals.Attribution{Status: signals.StatusTransit}
	case src == roleInfra || dst == roleInfra:
		return signals.Attribution{Status: signals.StatusInfrastructure}
	}
	at := signals.Attribution{Status: signals.StatusUnknown}
	switch {
	case a.customerEdge(r.InIf):
		at.Unattributed = r.SrcIP
	case a.customerEdge(r.OutIf):
		at.Unattributed = r.DstIP
	default:
		at.Unattributed = r.SrcIP
	}
	return at
}
