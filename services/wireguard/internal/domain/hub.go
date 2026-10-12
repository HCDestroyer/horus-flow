package domain

import (
	"fmt"
	"net/netip"
	"strings"
	"unicode"
)

// Códigos de error de la administración del hub (contrato platform.yaml).
const (
	CodeHubNotFound  = "WIREGUARD_HUB_NOT_FOUND"
	CodePoolNotFound = "WIREGUARD_POOL_NOT_FOUND"
	CodePoolOverlap  = "WIREGUARD_POOL_OVERLAP"
	CodePoolInvalid  = "WIREGUARD_POOL_INVALID"
	CodePoolInUse    = "WIREGUARD_POOL_IN_USE"
)

// Límites editables del hub (contrato y CHECK de wireguard.server).
const (
	DefaultMTU   = 1420
	MinMTU       = 1280
	MaxMTU       = 1500
	MaxKeepalive = 120
	MaxNameLen   = 64
	MaxHostLen   = 253
)

// Campos del hub que, editados desde la consola, el arranque ya no
// sobrescribe (wireguard.server.console_overrides).
const (
	FieldName      = "name"
	FieldEndpoint  = "endpoint"
	FieldMTU       = "mtu"
	FieldKeepalive = "persistent_keepalive_seconds"
)

// Tamaños admitidos al añadir un rango desde la consola.
const (
	MinPoolBits4 = 16
	MaxPoolBits4 = 28
	MinPoolBits6 = 64
	MaxPoolBits6 = 120
)

type reservedRange struct {
	p    netip.Prefix
	what string
}

// reserved4 son rangos IPv4 que nunca pueden ser de túnel (además de CGNAT).
var reserved4 = []reservedRange{
	{netip.MustParsePrefix("0.0.0.0/8"), "«esta red» (0.0.0.0/8)"},
	{netip.MustParsePrefix("127.0.0.0/8"), "loopback (127.0.0.0/8)"},
	{netip.MustParsePrefix("169.254.0.0/16"), "link-local (169.254.0.0/16)"},
	{netip.MustParsePrefix("224.0.0.0/4"), "multicast (224.0.0.0/4)"},
	{netip.MustParsePrefix("240.0.0.0/4"), "reservado (240.0.0.0/4)"},
}

// reserved6 son rangos IPv6 que nunca pueden ser de túnel.
var reserved6 = []reservedRange{
	{netip.MustParsePrefix("::/8"), "reservado (::/8: loopback, no especificada, IPv4 mapeada)"},
	{netip.MustParsePrefix("fe80::/10"), "link-local (fe80::/10)"},
	{netip.MustParsePrefix("ff00::/8"), "multicast (ff00::/8)"},
}

// PoolError es un rango rechazado: Overlap distingue el solape (409
// WIREGUARD_POOL_OVERLAP) del rango inválido (422 WIREGUARD_POOL_INVALID).
type PoolError struct {
	Overlap bool
	Detail  string
}

func (e *PoolError) Error() string { return "wireguard: pool rejected: " + e.Detail }

func invalidPool(format string, a ...any) *PoolError {
	return &PoolError{Detail: fmt.Sprintf(format, a...)}
}

// ValidateNewPool valida un rango que se añade desde la consola: CIDR
// canónico (con bits de host se rechaza indicando la dirección de red),
// IPv4 entre /16 y /28, fuera de CGNAT (100.64.0.0/10), de 0.0.0.0/8,
// loopback, link-local, multicast y reservados, sin solaparse con la red de
// servicios ni con los rangos existentes (de cualquier hub: la /32 es única
// en toda la plataforma).
//
// IPv6 se valida igual (/64–/120, reservados) pero se rechaza al final: la
// IP de túnel del router es IPv4 (script de alta, origen de IPFIX) y un
// rango que nunca se asigna solo confundiría al operador.
func ValidateNewPool(raw string, services netip.Prefix, existing []netip.Prefix) (netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	p, err := netip.ParsePrefix(raw)
	if err != nil {
		return netip.Prefix{}, invalidPool("%q no es un CIDR válido (p. ej. 10.254.0.0/16).", raw)
	}
	if p.Addr().Is4In6() {
		return netip.Prefix{}, invalidPool("%s: escriba el rango IPv4 en su forma IPv4.", p)
	}
	if m := p.Masked(); m != p {
		return netip.Prefix{}, invalidPool("%s tiene bits de host: la dirección de red es %s.", p, m)
	}
	reserved := reserved4
	if p.Addr().Is4() {
		if p.Bits() < MinPoolBits4 || p.Bits() > MaxPoolBits4 {
			return netip.Prefix{}, invalidPool("%s: un rango IPv4 debe estar entre /%d y /%d.", p, MinPoolBits4, MaxPoolBits4)
		}
		if p.Overlaps(CGNAT) {
			return netip.Prefix{}, invalidPool("%s se solapa con 100.64.0.0/10 (CGNAT de los clientes del ISP).", p)
		}
	} else {
		reserved = reserved6
		if p.Bits() < MinPoolBits6 || p.Bits() > MaxPoolBits6 {
			return netip.Prefix{}, invalidPool("%s: un rango IPv6 debe estar entre /%d y /%d.", p, MinPoolBits6, MaxPoolBits6)
		}
	}
	for _, r := range reserved {
		if p.Overlaps(r.p) {
			return netip.Prefix{}, invalidPool("%s se solapa con un rango %s.", p, r.what)
		}
	}
	if services.IsValid() && p.Overlaps(services) {
		return netip.Prefix{}, &PoolError{Overlap: true, Detail: fmt.Sprintf("%s se solapa con la red de servicios %s.", p, services)}
	}
	for _, q := range existing {
		if p.Overlaps(q) {
			return netip.Prefix{}, &PoolError{Overlap: true, Detail: fmt.Sprintf("%s se solapa con el rango %s.", p, q)}
		}
	}
	if !p.Addr().Is4() {
		return netip.Prefix{}, invalidPool("%s: los rangos IPv6 aún no se asignan a routers (la IP de túnel del router es IPv4).", p)
	}
	return p, nil
}

// ValidEndpoint indica si s es un nombre DNS o una IP literal sin esquema,
// puerto, zona ni ruta (lo que los routers usan como endpoint-address).
func ValidEndpoint(s string) bool {
	if s == "" || len(s) > MaxHostLen {
		return false
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Zone() == "" && !a.IsUnspecified() && !a.IsMulticast() && !a.IsLoopback()
	}
	labels := strings.Split(s, ".")
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, c := range l {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	// Un nombre con la última etiqueta numérica (p. ej. 1.2.3) no es ni IP ni DNS.
	last := labels[len(labels)-1]
	return strings.IndexFunc(last, func(r rune) bool { return r < '0' || r > '9' }) >= 0
}

// ValidHubName: 1–64 caracteres imprimibles, sin espacios al principio ni al final.
func ValidHubName(s string) bool {
	if s == "" || len([]rune(s)) > MaxNameLen || strings.TrimSpace(s) != s {
		return false
	}
	return strings.IndexFunc(s, func(r rune) bool { return !unicode.IsPrint(r) }) < 0
}
