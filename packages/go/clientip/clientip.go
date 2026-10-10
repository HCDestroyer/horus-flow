// Package clientip resuelve la IP real del cliente de una petición HTTP
// cuando Horus está detrás de proxies de confianza (Traefik siempre; además,
// en el modo de TLS externo de D19, el proxy inverso de la persona: Nginx
// Proxy Manager, nginx, Caddy…). docs/security.md §5.1.
//
// Regla: X-Forwarded-For y X-Forwarded-Proto solo se tienen en cuenta si la
// conexión llega desde una red de confianza (HORUS_TRUSTED_PROXIES, lista de
// CIDR separada por comas). Entonces se recorre X-Forwarded-For de derecha a
// izquierda saltando los saltos de confianza; la primera IP que no lo es es
// la del cliente. Una cabecera falsificada por el cliente queda a la
// IZQUIERDA de la que añade el proxy, así que nunca se elige. Sin redes de
// confianza (desarrollo, tests) se usa siempre RemoteAddr.
//
// La usan el rate limit y la auditoría del gateway, auth (login, kioscos,
// tenants), wireguard (enrolamiento) y detection (auditoría de evidencias).
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// EnvTrustedProxies es la variable con las redes de confianza.
const EnvTrustedProxies = "HORUS_TRUSTED_PROXIES"

// Resolver resuelve la IP del cliente con una lista de redes de confianza.
type Resolver struct {
	trusted []netip.Prefix
}

// New crea un Resolver con las redes dadas (CIDR o IP sueltas).
func New(cidrs []string) (*Resolver, error) {
	r := &Resolver{}
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !strings.Contains(c, "/") {
			a, err := netip.ParseAddr(c)
			if err != nil {
				return nil, fmt.Errorf("clientip: %q no es una IP ni un CIDR", c)
			}
			r.trusted = append(r.trusted, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, fmt.Errorf("clientip: CIDR inválido %q: %w", c, err)
		}
		r.trusted = append(r.trusted, p.Masked())
	}
	return r, nil
}

// Parse crea un Resolver desde una lista separada por comas.
func Parse(list string) (*Resolver, error) { return New(strings.Split(list, ",")) }

// Trusted indica si la dirección pertenece a una red de confianza.
func (r *Resolver) Trusted(a netip.Addr) bool {
	if r == nil || !a.IsValid() {
		return false
	}
	a = a.Unmap()
	for _, p := range r.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Remote es la IP del extremo TCP (RemoteAddr), sin puerto.
func Remote(req *http.Request) netip.Addr {
	return parseHop(req.RemoteAddr)
}

// IP devuelve la IP real del cliente (inválida si no se puede determinar).
func (r *Resolver) IP(req *http.Request) netip.Addr {
	remote := Remote(req)
	if !r.Trusted(remote) {
		return remote
	}
	hops := forwardedFor(req)
	for i := len(hops) - 1; i >= 0; i-- {
		if !r.Trusted(hops[i]) {
			return hops[i]
		}
	}
	if len(hops) > 0 {
		return hops[0] // toda la cadena es de confianza: el primer salto
	}
	return remote
}

// FromTrustedProxy indica si la petición llega de un proxy de confianza.
func (r *Resolver) FromTrustedProxy(req *http.Request) bool { return r.Trusted(Remote(req)) }

// Proto devuelve X-Forwarded-Proto (en minúsculas) si llega de un proxy de
// confianza; "" en otro caso.
func (r *Resolver) Proto(req *http.Request) string {
	if !r.FromTrustedProxy(req) {
		return ""
	}
	v := req.Header.Get("X-Forwarded-Proto")
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i] // varios proxies: el primero es el que vio el cliente
	}
	return strings.ToLower(strings.TrimSpace(v))
}

func forwardedFor(req *http.Request) []netip.Addr {
	var out []netip.Addr
	for _, line := range req.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(line, ",") {
			if a := parseHop(strings.TrimSpace(part)); a.IsValid() {
				out = append(out, a)
			} else if strings.TrimSpace(part) != "" {
				// Un salto ilegible rompe la cadena: lo que haya a su izquierda no es fiable.
				out = out[:0]
			}
		}
	}
	return out
}

func parseHop(s string) netip.Addr {
	if s == "" {
		return netip.Addr{}
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap()
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap()
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		if a, err := netip.ParseAddr(host); err == nil {
			return a.Unmap()
		}
	}
	return netip.Addr{}
}

var (
	defOnce sync.Once
	def     atomic.Pointer[Resolver]
)

// Default es el Resolver del proceso, leído una vez de HORUS_TRUSTED_PROXIES.
// Una lista inválida deja el Resolver vacío (solo RemoteAddr): falla cerrado.
func Default() *Resolver {
	defOnce.Do(func() {
		r, err := Parse(os.Getenv(EnvTrustedProxies))
		if err != nil {
			r = &Resolver{}
		}
		def.CompareAndSwap(nil, r)
	})
	return def.Load()
}

// SetDefault sustituye el Resolver del proceso (tests y arranque).
func SetDefault(r *Resolver) {
	defOnce.Do(func() {})
	def.Store(r)
}

// String es la IP del cliente como texto ("" si no se puede determinar),
// con el Resolver del proceso.
func String(req *http.Request) string {
	if a := Default().IP(req); a.IsValid() {
		return a.String()
	}
	return ""
}

// Addr es la IP del cliente con el Resolver del proceso.
func Addr(req *http.Request) netip.Addr { return Default().IP(req) }
