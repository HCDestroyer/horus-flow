package datasets

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
)

// reserved son los rangos que no son Internet pública: ninguna lista de
// reputación debe marcarlos y ninguna descarga de una fuente personalizada
// debe conectarse a ellos. Los de documentación (RFC 5737, RFC 3849) no
// están: los usan los fixtures.
var reserved = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8",      // "esta red"
		"10.0.0.0/8",     // RFC 1918
		"100.64.0.0/10",  // CGNAT (RFC 6598)
		"127.0.0.0/8",    // loopback
		"169.254.0.0/16", // enlace local (incl. metadatos de nube)
		"172.16.0.0/12",  // RFC 1918
		"192.0.0.0/24",   // asignaciones de protocolo IETF
		"192.168.0.0/16", // RFC 1918
		"198.18.0.0/15",  // pruebas de rendimiento (RFC 2544)
		"224.0.0.0/4",    // multicast
		"240.0.0.0/4",    // reservado y difusión limitada
		"::/128",         // sin especificar
		"::1/128",        // loopback
		"::ffff:0:0/96",  // IPv4 mapeada (se comprueba la IPv4)
		"64:ff9b::/96",   // NAT64 (RFC 6052)
		"64:ff9b:1::/48", // NAT64 local (RFC 8215)
		"100::/64",       // descarte (RFC 6666)
		"fc00::/7",       // ULA
		"fe80::/10",      // enlace local
		"ff00::/8",       // multicast
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// OverlapsReserved indica si p solapa algún rango no público.
func OverlapsReserved(p netip.Prefix) bool {
	for _, r := range reserved {
		if r.Overlaps(p) {
			return true
		}
	}
	return false
}

// ErrEgressDenied indica que una descarga apuntaba (directamente, tras
// resolver DNS o tras una redirección) a una dirección no permitida.
var ErrEgressDenied = errors.New("destino de descarga no permitido")

// Resolver resuelve nombres (net.DefaultResolver lo implementa).
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// EgressGuard limita a qué direcciones se conecta HTTPFetcher al descargar
// fuentes personalizadas (D20): solo https y solo direcciones públicas que no
// sean de la propia instalación. Se comprueba la URL inicial y cada
// redirección (resolviendo DNS) y, en conexión directa, la dirección real a
// la que se conecta el socket (protege de DNS rebinding). Detrás de un proxy
// HTTP(S) la conexión la abre el proxy: queda la comprobación por DNS.
type EgressGuard struct {
	// Deny: prefijos adicionales prohibidos (p. ej. las direcciones de la
	// propia instalación, ver InstallationPrefixes).
	Deny []netip.Prefix
	// Resolver: nil = net.DefaultResolver.
	Resolver Resolver
	// MaxRedirects: 0 = 5.
	MaxRedirects int
}

// InstallationPrefixes devuelve las direcciones de las interfaces locales
// (las de la propia instalación) como /32 y /128.
func InstallationPrefixes() []netip.Prefix {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []netip.Prefix
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			if ip, ok := netip.AddrFromSlice(n.IP); ok {
				ip = ip.Unmap()
				out = append(out, netip.PrefixFrom(ip, ip.BitLen()))
			}
		}
	}
	return out
}

// CheckAddr rechaza direcciones no públicas o prohibidas.
func (g *EgressGuard) CheckAddr(a netip.Addr) error {
	a = a.Unmap().WithZone("")
	p := netip.PrefixFrom(a, a.BitLen())
	if !a.IsValid() || OverlapsReserved(p) {
		return fmt.Errorf("%w: %s no es una dirección pública", ErrEgressDenied, a)
	}
	for _, d := range g.Deny {
		if d.Contains(a) {
			return fmt.Errorf("%w: %s es una dirección de la instalación o prohibida", ErrEgressDenied, a)
		}
	}
	return nil
}

// CheckURL exige https y que el host (o todas sus direcciones) sea público.
func (g *EgressGuard) CheckURL(ctx context.Context, u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("%w: %s no es https", ErrEgressDenied, u.Redacted())
	}
	host := u.Hostname()
	if a, err := netip.ParseAddr(host); err == nil {
		return g.CheckAddr(a)
	}
	var r Resolver = net.DefaultResolver
	if g.Resolver != nil {
		r = g.Resolver
	}
	addrs, err := r.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("%w: resolver %s: %v", ErrEgressDenied, host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("%w: %s no resuelve", ErrEgressDenied, host)
	}
	for _, a := range addrs {
		if err := g.CheckAddr(a); err != nil {
			return fmt.Errorf("%s: %w", host, err)
		}
	}
	return nil
}

// control comprueba la dirección real del socket (net.Dialer.Control).
func (g *EgressGuard) control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrEgressDenied, address)
	}
	return g.CheckAddr(ap.Addr())
}

// checkRedirect valida cada redirección.
func (g *EgressGuard) checkRedirect(req *http.Request, via []*http.Request) error {
	limit := g.MaxRedirects
	if limit <= 0 {
		limit = 5
	}
	if len(via) >= limit {
		return fmt.Errorf("%w: más de %d redirecciones", ErrEgressDenied, limit)
	}
	if err := g.CheckURL(req.Context(), req.URL); err != nil {
		return fmt.Errorf("redirección: %w", err)
	}
	return nil
}

// guardedClient construye el cliente de una descarga protegida. Si la URL
// sale por proxy, el socket lo abre el proxy y solo se puede comprobar por
// DNS; si no, el dialer comprueba la dirección real.
func (g *EgressGuard) guardedClient(base *http.Client, req *http.Request) *http.Client {
	c := *base
	c.CheckRedirect = g.checkRedirect
	var tr *http.Transport
	if t, ok := base.Transport.(*http.Transport); ok {
		tr = t.Clone()
	} else {
		tr = http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // tipo documentado
	}
	if tr.Proxy != nil {
		if pu, err := tr.Proxy(req); err == nil && pu != nil {
			c.Transport = tr
			return &c
		}
	}
	tr.Proxy = nil
	d := &net.Dialer{Control: g.control}
	tr.DialContext = d.DialContext
	c.Transport = tr
	return &c
}
