package kernel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"

	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/app"
)

var _ app.Router = (*Device)(nil)

// routeProtocol marca las rutas del agente (rtm_protocol propio, como hacen
// bird o zebra): al arrancar se reconocen y se borran las de peers que ya no
// existen sin tocar las del despliegue (horus-tunnel, proto boot/kernel).
const routeProtocol = 104

// rtmsgLen es sizeof(struct rtmsg).
const rtmsgLen = 12

// SyncRoutes implementa app.Router: una ruta por allowed-ip por la interfaz
// (scope link, tabla main). La primera vez lee las rutas propias que ya hay
// (de un arranque anterior) para borrar las que sobran.
func (d *Device) SyncRoutes(_ context.Context, want []netip.Prefix) error {
	d.routesMu.Lock()
	defer d.routesMu.Unlock()
	ifi, err := net.InterfaceByName(d.name)
	if err != nil {
		return fmt.Errorf("interface %s: %w", d.name, err)
	}
	c, err := netlink.Dial(unix.NETLINK_ROUTE, nil)
	if err != nil {
		return fmt.Errorf("rtnetlink: %w", err)
	}
	defer func() { _ = c.Close() }()
	if d.routes == nil {
		have, err := ownRoutes(c, ifi.Index)
		if err != nil {
			return err
		}
		d.routes = have
	}
	wanted := make(map[netip.Prefix]bool, len(want))
	var errs []error
	for _, p := range want {
		p = p.Masked()
		wanted[p] = true
		if d.routes[p] {
			continue
		}
		if err := route(c, unix.RTM_NEWROUTE, netlink.Create|netlink.Replace, ifi.Index, p); err != nil {
			errs = append(errs, fmt.Errorf("add route %s dev %s: %w", p, d.name, err))
			continue
		}
		d.routes[p] = true
	}
	for p := range d.routes {
		if wanted[p] {
			continue
		}
		if err := route(c, unix.RTM_DELROUTE, 0, ifi.Index, p); err != nil && !errors.Is(err, unix.ESRCH) {
			errs = append(errs, fmt.Errorf("delete route %s dev %s: %w", p, d.name, err))
			continue
		}
		delete(d.routes, p)
	}
	return errors.Join(errs...)
}

func rtmsg(p netip.Prefix) []byte {
	b := make([]byte, rtmsgLen)
	b[0] = unix.AF_INET
	if p.Addr().Is6() {
		b[0] = unix.AF_INET6
	}
	b[1] = byte(p.Bits())
	b[4] = unix.RT_TABLE_MAIN
	b[5] = routeProtocol
	b[6] = unix.RT_SCOPE_LINK
	b[7] = unix.RTN_UNICAST
	return b
}

func route(c *netlink.Conn, typ uint16, flags netlink.HeaderFlags, ifindex int, p netip.Prefix) error {
	ae := netlink.NewAttributeEncoder()
	ae.Bytes(unix.RTA_DST, p.Addr().AsSlice())
	ae.Uint32(unix.RTA_OIF, uint32(ifindex)) //nolint:gosec // índice de interfaz del kernel
	attrs, err := ae.Encode()
	if err != nil {
		return fmt.Errorf("encode route: %w", err)
	}
	_, err = c.Execute(netlink.Message{
		Header: netlink.Header{Type: netlink.HeaderType(typ), Flags: netlink.Request | netlink.Acknowledge | flags},
		Data:   append(rtmsg(p), attrs...),
	})
	return err //nolint:wrapcheck // lo envuelve el llamador con la ruta
}

// ownRoutes lee las rutas de la tabla main por la interfaz con el protocolo
// del agente (IPv4 e IPv6).
func ownRoutes(c *netlink.Conn, ifindex int) (map[netip.Prefix]bool, error) {
	out := map[netip.Prefix]bool{}
	for _, fam := range []byte{unix.AF_INET, unix.AF_INET6} {
		req := make([]byte, rtmsgLen)
		req[0] = fam
		msgs, err := c.Execute(netlink.Message{
			Header: netlink.Header{Type: netlink.HeaderType(unix.RTM_GETROUTE), Flags: netlink.Request | netlink.Dump},
			Data:   req,
		})
		if err != nil {
			return nil, fmt.Errorf("dump routes: %w", err)
		}
		for _, m := range msgs {
			if p, ok := parseOwnRoute(m.Data, ifindex); ok {
				out[p] = true
			}
		}
	}
	return out, nil
}

func parseOwnRoute(b []byte, ifindex int) (netip.Prefix, bool) {
	if len(b) < rtmsgLen || b[4] != unix.RT_TABLE_MAIN || b[5] != routeProtocol {
		return netip.Prefix{}, false
	}
	bits := int(b[1])
	ad, err := netlink.NewAttributeDecoder(b[rtmsgLen:])
	if err != nil {
		return netip.Prefix{}, false
	}
	var (
		dst netip.Addr
		oif uint32
	)
	for ad.Next() {
		switch ad.Type() {
		case unix.RTA_DST:
			dst, _ = netip.AddrFromSlice(ad.Bytes())
		case unix.RTA_OIF:
			oif = ad.Uint32()
		}
	}
	if ad.Err() != nil || !dst.IsValid() || int(oif) != ifindex {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(dst.Unmap(), bits), true
}
