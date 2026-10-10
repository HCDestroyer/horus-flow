package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/netip"
	"regexp"
	"strings"
	"sync"
)

// masker enmascara datos de clientes en el texto del paquete de diagnóstico
// (docs/security.md §8, observability.md §3.3): toda dirección IP que no
// sea de la propia infraestructura (loopback, redes de los contenedores,
// rangos de túnel y servicios WireGuard, IP del colector, --keep-cidr) se
// sustituye por un seudónimo estable dentro del paquete ([ip:xxxxxxxx], HMAC
// con una clave aleatoria que no se guarda: permite correlacionar líneas sin
// poder revertirlo), además de correos, tokens Bearer y parámetros secretos.
type masker struct {
	key  []byte
	keep []netip.Prefix

	mu     sync.Mutex
	masked int
}

var (
	ipv4Re   = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	ipv6Re   = regexp.MustCompile(`(?i)(?:[0-9a-f]{0,4}:){2,7}(?:[0-9a-f]{0,4}|(?:\d{1,3}\.){3}\d{1,3})`)
	emailRe  = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	bearerRe = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`)
	secretKV = regexp.MustCompile(`(?i)((?:password|passwd|secret|token|ticket|community|api_key|apikey|authorization)["']?\s*[:=]\s*["']?)[^"'\s,&}]{3,}`)
)

func newMasker(keep []netip.Prefix) *masker {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return &masker{key: key, keep: keep}
}

// infraPrefixes devuelve los rangos de infraestructura que se conservan:
// los de las interfaces locales (red del compose), los de túnel y servicios
// WireGuard, la IP del colector y los de extra.
func infraPrefixes(environ []string, extra []string) []netip.Prefix {
	var out []netip.Prefix
	if ifaces, err := net.InterfaceAddrs(); err == nil {
		for _, a := range ifaces {
			if p, err := netip.ParsePrefix(a.String()); err == nil {
				out = append(out, p.Masked())
			}
		}
	}
	vals := append([]string{}, extra...)
	for _, k := range []string{"HORUS_WG_TUNNEL_CIDRS", "HORUS_WG_SERVICES_CIDR", "HORUS_COLLECTOR_IP", "HORUS_DOCKER_SUBNET",
		"HORUS_GRPC_BIND", "HORUS_DIAGNOSE_KEEP_CIDRS"} {
		if v := envLookup(environ, k); v != "" {
			vals = append(vals, strings.Split(v, ",")...)
		}
	}
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if p, err := netip.ParsePrefix(v); err == nil {
			out = append(out, p.Masked())
		} else if a, err := netip.ParseAddr(v); err == nil {
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	return out
}

func envLookup(environ []string, key string) string {
	v := ""
	for _, kv := range environ {
		if k, val, ok := strings.Cut(kv, "="); ok && k == key {
			v = val
		}
	}
	return v
}

func (m *masker) keepAddr(a netip.Addr) bool {
	a = a.Unmap()
	if a.IsLoopback() || a.IsUnspecified() || a.IsMulticast() || a.IsLinkLocalUnicast() {
		return true
	}
	for _, p := range m.keep {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func (m *masker) pseudonym(a netip.Addr) string {
	h := hmac.New(sha256.New, m.key)
	h.Write([]byte(a.Unmap().String()))
	m.mu.Lock()
	m.masked++
	m.mu.Unlock()
	return "[ip:" + hex.EncodeToString(h.Sum(nil))[:8] + "]"
}

// Text enmascara s.
func (m *masker) Text(s string) string {
	s = ipv4Re.ReplaceAllStringFunc(s, func(c string) string {
		a, err := netip.ParseAddr(c)
		if err != nil || m.keepAddr(a) {
			return c
		}
		return m.pseudonym(a)
	})
	s = ipv6Re.ReplaceAllStringFunc(s, func(c string) string {
		if strings.Count(c, ":") < 2 {
			return c
		}
		a, err := netip.ParseAddr(c)
		if err != nil {
			// p. ej. "addr:port" con IPv6 entre corchetes ya separado, o falso positivo.
			return c
		}
		if m.keepAddr(a) {
			return c
		}
		return m.pseudonym(a)
	})
	s = emailRe.ReplaceAllString(s, "[email]")
	s = bearerRe.ReplaceAllString(s, "${1}[REDACTED]")
	s = secretKV.ReplaceAllString(s, "${1}[REDACTED]")
	return s
}

// Count devuelve cuántas direcciones se enmascararon.
func (m *masker) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.masked
}
