// Package kernel aplica el estado del hub a una interfaz WireGuard real con
// wgctrl: netlink si el kernel tiene el módulo `wireguard`, o el socket UAPI
// (/var/run/wireguard/<if>.sock) si la interfaz la crea `wireguard-go`
// (espacio de usuario; sirve en hosts y CI sin módulo de kernel). La interfaz
// y su dirección las crea el despliegue (`ip link add wg0 type wireguard` o
// `wireguard-go wg0`; `ip addr add 10.255.0.1/16 dev wg0; ip link set wg0 up`):
// el agente solo gestiona clave, puerto y peers. Requiere CAP_NET_ADMIN.
package kernel

import (
	"context"
	"fmt"
	"net"
	"net/netip"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/app"
	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/domain"
)

// Device es la interfaz WireGuard name.
type Device struct {
	name   string
	client *wgctrl.Client
}

var _ app.Device = (*Device)(nil)

// Open abre el cliente wgctrl para la interfaz name.
func Open(name string) (*Device, error) {
	c, err := wgctrl.New()
	if err != nil {
		return nil, fmt.Errorf("wgctrl: %w", err)
	}
	return &Device{name: name, client: c}, nil
}

// Close libera el cliente.
func (d *Device) Close() error { return d.client.Close() }

// Configure implementa app.Device.
func (d *Device) Configure(_ context.Context, privateKey string, listenPort int) error {
	k, err := wgtypes.ParseKey(privateKey)
	if err != nil {
		return fmt.Errorf("hub private key: %w", err)
	}
	port := listenPort
	if err := d.client.ConfigureDevice(d.name, wgtypes.Config{PrivateKey: &k, ListenPort: &port}); err != nil {
		return fmt.Errorf("configure %s: %w", d.name, err)
	}
	return nil
}

// Peers implementa app.Device.
func (d *Device) Peers(context.Context) ([]domain.Peer, error) {
	dev, err := d.client.Device(d.name)
	if err != nil {
		return nil, fmt.Errorf("device %s: %w", d.name, err)
	}
	out := make([]domain.Peer, 0, len(dev.Peers))
	for _, p := range dev.Peers {
		dp := domain.Peer{PublicKey: p.PublicKey.String(), Keepalive: p.PersistentKeepaliveInterval, LastHandshake: p.LastHandshakeTime,
			RxBytes: uint64(max(p.ReceiveBytes, 0)), TxBytes: uint64(max(p.TransmitBytes, 0))} //nolint:gosec // contadores no negativos
		if p.Endpoint != nil {
			dp.Endpoint = p.Endpoint.String()
		}
		for _, n := range p.AllowedIPs {
			if pfx, ok := toPrefix(n); ok {
				dp.AllowedIPs = append(dp.AllowedIPs, pfx)
			}
		}
		out = append(out, dp)
	}
	return out, nil
}

func toPrefix(n net.IPNet) (netip.Prefix, bool) {
	a, ok := netip.AddrFromSlice(n.IP)
	if !ok {
		return netip.Prefix{}, false
	}
	ones, _ := n.Mask.Size()
	return netip.PrefixFrom(a.Unmap(), ones), true
}

// Apply implementa app.Device.
func (d *Device) Apply(_ context.Context, upsert []domain.Peer, remove []string) error {
	var cfgs []wgtypes.PeerConfig
	for _, p := range upsert {
		k, err := wgtypes.ParseKey(p.PublicKey)
		if err != nil {
			return fmt.Errorf("peer key: %w", err)
		}
		ka := p.Keepalive
		pc := wgtypes.PeerConfig{PublicKey: k, ReplaceAllowedIPs: true, PersistentKeepaliveInterval: &ka}
		for _, a := range p.AllowedIPs {
			pc.AllowedIPs = append(pc.AllowedIPs, net.IPNet{IP: a.Addr().AsSlice(), Mask: net.CIDRMask(a.Bits(), a.Addr().BitLen())})
		}
		cfgs = append(cfgs, pc)
	}
	for _, r := range remove {
		k, err := wgtypes.ParseKey(r)
		if err != nil {
			continue
		}
		cfgs = append(cfgs, wgtypes.PeerConfig{PublicKey: k, Remove: true})
	}
	if len(cfgs) == 0 {
		return nil
	}
	if err := d.client.ConfigureDevice(d.name, wgtypes.Config{Peers: cfgs}); err != nil {
		return fmt.Errorf("configure peers %s: %w", d.name, err)
	}
	return nil
}
