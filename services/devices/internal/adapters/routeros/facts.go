package routeros

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// Paths leídos (todos GET; docs/vendors/mikrotik.md §4.1 y §11.4).
var Paths = []string{"system/resource", "ip/pool", "ipv6/pool", "ppp/profile", "ipv6/dhcp-server", "ip/address", "ipv6/address"}

func truthy(v string) bool { return v == "true" || v == "yes" }

// ReadFacts lee pools, perfiles PPP, servidores DHCPv6 y direcciones.
func (c *Client) ReadFacts(ctx context.Context) (domain.RouterFacts, error) {
	var f domain.RouterFacts
	res, err := c.Print(ctx, "system/resource", "version")
	if err != nil {
		return f, err
	}
	if len(res) > 0 {
		// "7.12 (stable)" → "7.12"
		if v := strings.Fields(res[0]["version"]); len(v) > 0 {
			f.Version = v[0]
		}
	}
	pools, err := c.Print(ctx, "ip/pool", "name", "ranges")
	if err != nil {
		return f, err
	}
	for _, r := range pools {
		f.IPPools = append(f.IPPools, domain.IPPool{Name: r["name"], Ranges: r["ranges"]})
	}
	// IPv6 puede estar deshabilitado: un fallo de estos menús no aborta.
	if v6, err := c.Print(ctx, "ipv6/pool", "name", "prefix", "prefix-length"); err == nil {
		for _, r := range v6 {
			pl, _ := strconv.Atoi(r["prefix-length"])
			f.IPv6Pools = append(f.IPv6Pools, domain.IPv6Pool{Name: r["name"], Prefix: r["prefix"], PrefixLength: pl})
		}
	} else if !optional(err) {
		return f, err
	}
	if prof, err := c.Print(ctx, "ppp/profile", "name", "dhcpv6-pd-pool", "remote-ipv6-prefix-pool", "remote-ipv6-prefix-reuse"); err == nil {
		for _, r := range prof {
			f.PPPProfiles = append(f.PPPProfiles, domain.PPPProfile{Name: r["name"], PDPool: r["dhcpv6-pd-pool"],
				LinkPool: r["remote-ipv6-prefix-pool"], LinkReuse: truthy(r["remote-ipv6-prefix-reuse"])})
		}
	} else if !optional(err) {
		return f, err
	}
	if srv, err := c.Print(ctx, "ipv6/dhcp-server", "name", "interface", "prefix-pool", "address-pool"); err == nil {
		for _, r := range srv {
			f.DHCPv6Servers = append(f.DHCPv6Servers, domain.DHCPv6Server{Name: r["name"], Interface: r["interface"],
				PrefixPool: r["prefix-pool"], AddressPool: r["address-pool"]})
		}
	} else if !optional(err) {
		return f, err
	}
	for _, path := range []string{"ip/address", "ipv6/address"} {
		addrs, err := c.Print(ctx, path, "address", "interface", "dynamic", "disabled")
		if err != nil {
			if path == "ipv6/address" && optional(err) {
				continue
			}
			return f, err
		}
		for _, r := range addrs {
			f.Addresses = append(f.Addresses, domain.IfAddress{Address: r["address"], Interface: r["interface"],
				Dynamic: truthy(r["dynamic"]), Disabled: truthy(r["disabled"])})
		}
	}
	return f, nil
}

// optional: un menú que no existe (paquete IPv6 deshabilitado) responde
// HTTP 400/404; no impide la importación IPv4.
func optional(err error) bool {
	return errors.Is(err, ErrUnreachable) && !errors.Is(err, ErrAuth) && (strings.Contains(err.Error(), "HTTP 400") || strings.Contains(err.Error(), "HTTP 404"))
}

// Reader implementa app.RouterReader con este cliente.
type Reader struct {
	Timeout time.Duration
	Dial    Dialer
	// Scheme/Port permiten apuntar a fixtures en tests ("" = https://<ip>).
	BaseURL func(tunnelIP string) string
}

var _ app.RouterReader = Reader{}

// Read implementa app.RouterReader.
func (r Reader) Read(ctx context.Context, t app.ReadTarget) (domain.RouterFacts, string, error) {
	base := "https://" + t.TunnelIP
	if r.BaseURL != nil {
		base = r.BaseURL(t.TunnelIP)
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	c := New(Target{BaseURL: base, User: t.User, Password: t.Password, Pinned: t.Pinned}, timeout, r.Dial)
	f, err := c.ReadFacts(ctx)
	switch {
	case errors.Is(err, ErrFingerprintChanged):
		return f, c.ObservedFingerprint(), app.ErrRouterFingerprint
	case errors.Is(err, ErrAuth):
		return f, c.ObservedFingerprint(), app.ErrRouterAuth
	case err != nil:
		return f, c.ObservedFingerprint(), app.ErrRouterUnreachable
	}
	return f, c.ObservedFingerprint(), nil
}
