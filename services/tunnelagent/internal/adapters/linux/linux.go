//go:build linux

// Package linux implementa las interfaces de túnel del hub con netlink:
// interfaces XFRM (route-based IPsec, if_id por túnel) y GRE del kernel
// (ip_gre, local/remoto/clave por túnel), la ruta /32 de cada router por su
// interfaz y los conjuntos ipset que usa el cortafuegos de horus-tunnel.
// Requiere CAP_NET_ADMIN en la red del anfitrión.
package linux

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/app"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/domain"
)

// RouteProtocol marca las rutas del agente (wg-agent usa 104).
const RouteProtocol = 105

// Conjuntos ipset por defecto (los usa horus-tunnel en sus reglas).
const (
	DefaultGRESet    = "horus-gre-peers"
	DefaultSourceSet = "horus-tunnel-src"
)

// Options configura el adaptador.
type Options struct {
	// Underlay es la interfaz física de las XFRM (vacía = la de la ruta por
	// defecto).
	Underlay  string
	GRESet    string
	SourceSet string
}

// Host es el adaptador.
type Host struct {
	mu sync.Mutex
	o  Options
	h  *netlink.Handle
}

var _ app.Links = (*Host)(nil)

// Open abre el socket netlink (no toca nada hasta el primer estado deseado).
func Open(o Options) (*Host, error) {
	if o.GRESet == "" {
		o.GRESet = DefaultGRESet
	}
	if o.SourceSet == "" {
		o.SourceSet = DefaultSourceSet
	}
	h, err := netlink.NewHandle(unix.NETLINK_ROUTE, unix.NETLINK_NETFILTER)
	if err != nil {
		return nil, fmt.Errorf("tunnelagent: netlink: %w", err)
	}
	return &Host{o: o, h: h}, nil
}

// Close libera el socket (las interfaces quedan: fail-static).
func (x *Host) Close() error {
	x.h.Close()
	return nil
}

// List implementa app.Links.
func (x *Host) List(context.Context) ([]app.LinkState, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	links, err := x.h.LinkList()
	if err != nil {
		return nil, fmt.Errorf("link list: %w", err)
	}
	var out []app.LinkState
	for _, l := range links {
		a := l.Attrs()
		transport, idx, ok := domain.ParseInterface(a.Name)
		if !ok {
			continue
		}
		st := app.LinkState{Transport: transport, Index: idx, Up: a.Flags&net.FlagUp != 0}
		if s := a.Statistics; s != nil {
			st.RxBytes, st.TxBytes, st.RxPackets = s.RxBytes, s.TxBytes, s.RxPackets
		}
		out = append(out, st)
	}
	return out, nil
}

func (x *Host) underlay() (int, error) {
	if x.o.Underlay != "" {
		l, err := x.h.LinkByName(x.o.Underlay)
		if err != nil {
			return 0, fmt.Errorf("underlay %s: %w", x.o.Underlay, err)
		}
		return l.Attrs().Index, nil
	}
	routes, err := x.h.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return 0, fmt.Errorf("route list: %w", err)
	}
	for _, r := range routes {
		if (r.Dst == nil || r.Dst.String() == "0.0.0.0/0") && r.LinkIndex > 0 && r.Table == unix.RT_TABLE_MAIN {
			return r.LinkIndex, nil
		}
	}
	return 0, errors.New("no default route: set HORUS_TUNNELAGENT_UNDERLAY_INTERFACE")
}

func ip(a netip.Addr) net.IP { return net.IP(a.AsSlice()) }

// wanted construye la interfaz deseada.
func (x *Host) wanted(t domain.Tunnel) (netlink.Link, error) {
	attrs := netlink.LinkAttrs{Name: t.Interface(), MTU: t.MTU}
	if t.Transport == domain.TransportGRE {
		// Clave GRE (RFC 2890: identifica el túnel, no es un secreto; 0 =
		// sin clave, RouterOS no la admite). TTL 64 y PMTU discovery activo.
		return &netlink.Gretun{LinkAttrs: attrs, Local: ip(t.Local), Remote: ip(t.Remote), IKey: t.GREKey, OKey: t.GREKey, Ttl: 64, PMtuDisc: 1}, nil
	}
	parent, err := x.underlay()
	if err != nil {
		return nil, err
	}
	attrs.ParentIndex = parent
	return &netlink.Xfrmi{LinkAttrs: attrs, Ifid: t.Index}, nil
}

// same indica si la interfaz existente ya es la deseada.
func same(cur, want netlink.Link) bool {
	switch w := want.(type) {
	case *netlink.Gretun:
		c, ok := cur.(*netlink.Gretun)
		return ok && c.Local.Equal(w.Local) && c.Remote.Equal(w.Remote) && c.IKey == w.IKey && c.OKey == w.OKey
	case *netlink.Xfrmi:
		c, ok := cur.(*netlink.Xfrmi)
		return ok && c.Ifid == w.Ifid
	}
	return false
}

// Ensure implementa app.Links.
func (x *Host) Ensure(_ context.Context, t domain.Tunnel) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	want, err := x.wanted(t)
	if err != nil {
		return err
	}
	cur, err := x.h.LinkByName(t.Interface())
	var notFound netlink.LinkNotFoundError
	switch {
	case errors.As(err, &notFound):
		cur = nil
	case err != nil:
		return fmt.Errorf("link %s: %w", t.Interface(), err)
	}
	if cur != nil && !same(cur, want) {
		if err := x.h.LinkDel(cur); err != nil {
			return fmt.Errorf("replace %s: %w", t.Interface(), err)
		}
		cur = nil
	}
	if cur == nil {
		if err := x.h.LinkAdd(want); err != nil {
			return fmt.Errorf("add %s (%s): %w", t.Interface(), want.Type(), err)
		}
		if cur, err = x.h.LinkByName(t.Interface()); err != nil {
			return fmt.Errorf("link %s: %w", t.Interface(), err)
		}
	}
	if cur.Attrs().MTU != t.MTU {
		if err := x.h.LinkSetMTU(cur, t.MTU); err != nil {
			return fmt.Errorf("mtu %s: %w", t.Interface(), err)
		}
	}
	if cur.Attrs().Flags&net.FlagUp == 0 {
		if err := x.h.LinkSetUp(cur); err != nil {
			return fmt.Errorf("up %s: %w", t.Interface(), err)
		}
	}
	// La /32 del router y las direcciones Horus de los equipos alcanzados a
	// través de él (switches vía router, ADR-0032).
	routes4 := append([]netip.Prefix{netip.PrefixFrom(t.TunnelAddress, 32)}, t.Routed...)
	keep := map[string]bool{}
	for _, p := range routes4 {
		dst := &net.IPNet{IP: ip(p.Addr()), Mask: net.CIDRMask(p.Bits(), 32)}
		keep[dst.String()] = true
		r := &netlink.Route{LinkIndex: cur.Attrs().Index, Dst: dst, Src: ip(t.HubAddress), Scope: netlink.SCOPE_LINK, Protocol: RouteProtocol}
		if err := x.h.RouteReplace(r); err != nil {
			// Sin la IP de servicios en el host (wg0 aún sin dirección) la ruta
			// va sin origen preferido.
			r.Src = nil
			if err2 := x.h.RouteReplace(r); err2 != nil {
				return fmt.Errorf("route %s dev %s: %w", dst, t.Interface(), err)
			}
		}
	}
	// Rutas propias (protocolo del agente) que ya no se piden por esta
	// interfaz: un switch dado de baja o pasado a otro router.
	routes, err := x.h.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{LinkIndex: cur.Attrs().Index, Protocol: RouteProtocol},
		netlink.RT_FILTER_OIF|netlink.RT_FILTER_PROTOCOL)
	if err != nil {
		return fmt.Errorf("route list dev %s: %w", t.Interface(), err)
	}
	for i := range routes {
		if routes[i].Dst == nil || keep[routes[i].Dst.String()] {
			continue
		}
		if err := x.h.RouteDel(&routes[i]); err != nil && !errors.Is(err, unix.ESRCH) {
			return fmt.Errorf("delete route %s dev %s: %w", routes[i].Dst, t.Interface(), err)
		}
	}
	return nil
}

// Remove implementa app.Links.
func (x *Host) Remove(_ context.Context, transport string, index uint32) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	l, err := x.h.LinkByName(domain.InterfaceName(transport, index))
	var notFound netlink.LinkNotFoundError
	if errors.As(err, &notFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return x.h.LinkDel(l)
}

func (x *Host) ensureSet(name, typ string) error {
	err := x.h.IpsetCreate(name, typ, netlink.IpsetCreateOptions{Family: unix.AF_INET})
	if err == nil {
		return nil
	}
	if _, lerr := x.h.IpsetList(name); lerr == nil {
		return nil // ya existe (lo crea horus-tunnel antes que sus reglas)
	}
	return fmt.Errorf("ipset create %s %s: %w", name, typ, err)
}

// SyncFilters implementa app.Links.
func (x *Host) SyncFilters(_ context.Context, peers []netip.Addr, bindings []app.Binding) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if err := x.ensureSet(x.o.GRESet, "hash:ip"); err != nil {
		return err
	}
	if err := x.ensureSet(x.o.SourceSet, "hash:net,iface"); err != nil {
		return err
	}
	wantPeers := map[string]*netlink.IPSetEntry{}
	for _, p := range peers {
		wantPeers[p.String()] = &netlink.IPSetEntry{IP: ip(p), Replace: true}
	}
	wantSrc := map[string]*netlink.IPSetEntry{}
	for _, b := range bindings {
		wantSrc[b.Prefix.String()+","+b.Interface] = &netlink.IPSetEntry{IP: ip(b.Prefix.Addr()), CIDR: uint8(b.Prefix.Bits()), //nolint:gosec // 0-32
			IFace: b.Interface, Replace: true}
	}
	return errors.Join(x.syncSet(x.o.GRESet, wantPeers, func(e netlink.IPSetEntry) string { return e.IP.String() }),
		x.syncSet(x.o.SourceSet, wantSrc, srcKey))
}

// srcKey es la clave de una entrada hash:net,iface (prefijo,interfaz); el
// kernel devuelve CIDR 0 para las /32 en algunas versiones.
func srcKey(e netlink.IPSetEntry) string {
	bits := int(e.CIDR)
	if bits == 0 {
		bits = 32
	}
	a, _ := netip.AddrFromSlice(e.IP.To4())
	return netip.PrefixFrom(a, bits).String() + "," + e.IFace
}

func (x *Host) syncSet(name string, want map[string]*netlink.IPSetEntry, key func(netlink.IPSetEntry) string) error {
	res, err := x.h.IpsetList(name)
	if err != nil {
		return fmt.Errorf("ipset list %s: %w", name, err)
	}
	have := map[string]bool{}
	var errs []error
	for _, e := range res.Entries {
		k := key(e)
		if _, ok := want[k]; ok {
			have[k] = true
			continue
		}
		del := e
		if err := x.h.IpsetDel(name, &del); err != nil {
			errs = append(errs, fmt.Errorf("ipset del %s %s: %w", name, k, err))
		}
	}
	for k, e := range want {
		if have[k] {
			continue
		}
		if err := x.h.IpsetAdd(name, e); err != nil {
			errs = append(errs, fmt.Errorf("ipset add %s %s: %w", name, k, err))
		}
	}
	return errors.Join(errs...)
}
