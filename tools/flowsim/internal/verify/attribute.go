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

// Attribute atribuye un flujo decodificado.
func (a *Attributor) Attribute(r *flow.Record) signals.Attribution {
	src, dst := a.lookup(r.SrcIP), a.lookup(r.DstIP)
	switch {
	case src == roleExcluded || dst == roleExcluded:
		return signals.Attribution{Status: signals.StatusExcluded}
	case a.tunnel[r.SrcIP] || a.tunnel[r.DstIP]:
		return signals.Attribution{Status: signals.StatusTunnel}
	case src == roleCustomer && dst == roleCustomer:
		return signals.Attribution{Status: signals.StatusInternal, Client: a.key(r.SrcIP), Upload: true}
	case src == roleCustomer:
		return signals.Attribution{Status: signals.StatusAttributed, Client: a.key(r.SrcIP), Upload: true}
	case dst == roleCustomer:
		return signals.Attribution{Status: signals.StatusAttributed, Client: a.key(r.DstIP)}
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
