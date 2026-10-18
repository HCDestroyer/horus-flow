// Package domain contiene las reglas puras del agente de túneles IPsec IKEv2
// y GRE del hub (rol tunnel-agent): validación del estado deseado, nombres
// de interfaz y cálculo del plan de cambios sobre lo observado en el host.
// Fail-static: el plan solo quita túneles ausentes de un estado deseado
// completo y válido; nunca por un error del control.
package domain

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Transportes que gestiona el agente (WireGuard lo gestiona wg-agent).
const (
	TransportIPsec = "ipsec"
	TransportGRE   = "gre"
)

// Prefijos de las interfaces del hub. No se usan «xfrm»/«gre» a secas: el
// kernel crea gre0 (interfaz de reserva que recibe el GRE que no casa con
// ningún túnel) y el cortafuegos acepta por prefijo (hxfrm+, hgre+).
const (
	XFRMPrefix = "hxfrm"
	GREPrefix  = "hgre"
)

// Límites.
const (
	DefaultMTU = 1400
	minMTU     = 1280
	maxMTU     = 1500
	// MaxIndex cabe en el nombre de interfaz (IFNAMSIZ 15: «hxfrm» + 10 dígitos).
	MaxIndex = 1<<31 - 1
	// MinPSKLen: las PSK que genera Horus tienen 48 caracteres; por debajo de
	// 32 se rechaza el estado (RFC 7296 §2.15: la PSK debe tener entropía de
	// clave, no de contraseña).
	MinPSKLen = 32
	maxPSKLen = 128
	maxIDLen  = 253
)

// ErrInvalidState indica un estado deseado inválido (se rechaza entero).
var ErrInvalidState = errors.New("tunnelagent: invalid desired state")

// Tunnel es un túnel deseado ya validado.
type Tunnel struct {
	PeerID    string
	TenantID  string
	Transport string
	Index     uint32
	// TunnelAddress es la /32 del router; HubAddress, la IP de servicios del
	// hub (origen preferido de las rutas hacia el router).
	TunnelAddress netip.Addr
	HubAddress    netip.Addr
	// Local/Remote: IP públicas del hub y del router (Remote inválida en
	// ipsec = cualquiera: router tras NAT o IP dinámica).
	Local    netip.Addr
	Remote   netip.Addr
	LocalID  string
	RemoteID string
	PSK      string
	MTU      int
	// GREKey: clave GRE (0 = sin clave).
	GREKey uint32
}

// Interface es el nombre de la interfaz del túnel en el hub.
func (t Tunnel) Interface() string { return InterfaceName(t.Transport, t.Index) }

// ConnName es el nombre de la conexión en el demonio IKE.
func (t Tunnel) ConnName() string { return ConnName(t.Index) }

// InterfaceName: hxfrm<index> (ipsec) o hgre<index> (gre).
func InterfaceName(transport string, index uint32) string {
	if transport == TransportGRE {
		return GREPrefix + strconv.FormatUint(uint64(index), 10)
	}
	return XFRMPrefix + strconv.FormatUint(uint64(index), 10)
}

// ConnName es el nombre de la conexión IKE de un índice.
func ConnName(index uint32) string { return "horus-" + strconv.FormatUint(uint64(index), 10) }

// ParseInterface reconoce una interfaz del agente (transporte e índice).
func ParseInterface(name string) (transport string, index uint32, ok bool) {
	var rest string
	switch {
	case strings.HasPrefix(name, XFRMPrefix):
		transport, rest = TransportIPsec, strings.TrimPrefix(name, XFRMPrefix)
	case strings.HasPrefix(name, GREPrefix):
		transport, rest = TransportGRE, strings.TrimPrefix(name, GREPrefix)
	default:
		return "", 0, false
	}
	n, err := strconv.ParseUint(rest, 10, 32)
	if err != nil || n == 0 || n > MaxIndex || strconv.FormatUint(n, 10) != rest {
		return "", 0, false
	}
	return transport, uint32(n), true
}

// ParseConnName reconoce una conexión IKE del agente.
func ParseConnName(name string) (uint32, bool) {
	rest, ok := strings.CutPrefix(name, "horus-")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(rest, 10, 32)
	if err != nil || n == 0 || n > MaxIndex || strconv.FormatUint(n, 10) != rest {
		return 0, false
	}
	return uint32(n), true
}

// idRe: identidad IKE (IP, FQDN, usuario@FQDN o KeyID) sin espacios, comillas
// ni caracteres de control: viaja a vici como valor, pero se valida igual que
// en la API (defensa en profundidad).
var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@:-]*$`)

// pskRe: alfabeto de las PSK de Horus (sin símbolos que rompan una línea de
// configuración del router).
var pskRe = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// ValidIKEID indica si s es una identidad IKE aceptable.
func ValidIKEID(s string) bool { return len(s) <= maxIDLen && idRe.MatchString(s) }

// DesiredTunnel es la entrada sin validar (desde el proto).
type DesiredTunnel struct {
	PeerID, TenantID, Transport                       string
	Index                                             uint32
	TunnelAddress, HubAddress, LocalAddress, RemoteIP string
	LocalID, RemoteID, PSK                            string
	MTU                                               int32
	GREKey                                            uint32
}

func parseAddr(s string, required bool) (netip.Addr, error) {
	if s == "" {
		if required {
			return netip.Addr{}, errors.New("required")
		}
		return netip.Addr{}, nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil || !a.Is4() || a.IsUnspecified() || a.IsLoopback() || a.IsMulticast() || a.IsLinkLocalUnicast() {
		return netip.Addr{}, errors.New("must be a unicast IPv4 address")
	}
	return a, nil
}

func parseSingle(s string) (netip.Addr, error) {
	if p, err := netip.ParsePrefix(s); err == nil {
		if !p.IsSingleIP() {
			return netip.Addr{}, errors.New("must be a single address")
		}
		s = p.Addr().String()
	}
	return parseAddr(s, true)
}

// Normalize valida el estado deseado completo y lo indexa por índice. Un
// túnel inválido invalida el estado entero (no se aplica nada).
func Normalize(desired []DesiredTunnel) (map[uint32]Tunnel, error) {
	out := make(map[uint32]Tunnel, len(desired))
	seenAddr := map[netip.Addr]string{}
	seenRemoteGRE := map[netip.Addr]string{}
	seenRemoteID := map[string]string{}
	bad := func(peer, what string, a ...any) error {
		return fmt.Errorf("%w: tunnel %s: %s", ErrInvalidState, peer, fmt.Sprintf(what, a...))
	}
	for _, d := range desired {
		t := Tunnel{PeerID: d.PeerID, TenantID: d.TenantID, Transport: d.Transport, Index: d.Index, LocalID: d.LocalID,
			RemoteID: d.RemoteID, PSK: d.PSK, MTU: int(d.MTU), GREKey: d.GREKey}
		if t.Transport != TransportIPsec && t.Transport != TransportGRE {
			return nil, bad(d.PeerID, "transport %q", d.Transport)
		}
		if t.Index == 0 || t.Index > MaxIndex {
			return nil, bad(d.PeerID, "index %d", d.Index)
		}
		if _, dup := out[t.Index]; dup {
			return nil, bad(d.PeerID, "duplicated index %d", d.Index)
		}
		var err error
		if t.TunnelAddress, err = parseSingle(d.TunnelAddress); err != nil {
			return nil, bad(d.PeerID, "tunnel address: %v", err)
		}
		if other, dup := seenAddr[t.TunnelAddress]; dup {
			return nil, bad(d.PeerID, "tunnel address %s also in %s", t.TunnelAddress, other)
		}
		seenAddr[t.TunnelAddress] = d.PeerID
		if t.HubAddress, err = parseAddr(d.HubAddress, true); err != nil {
			return nil, bad(d.PeerID, "hub tunnel address: %v", err)
		}
		if t.Local, err = parseAddr(d.LocalAddress, t.Transport == TransportGRE); err != nil {
			return nil, bad(d.PeerID, "local address: %v", err)
		}
		if t.Remote, err = parseAddr(d.RemoteIP, t.Transport == TransportGRE); err != nil {
			return nil, bad(d.PeerID, "remote address: %v", err)
		}
		if t.MTU == 0 {
			t.MTU = DefaultMTU
		}
		if t.MTU < minMTU || t.MTU > maxMTU {
			return nil, bad(d.PeerID, "mtu %d", t.MTU)
		}
		switch t.Transport {
		case TransportGRE:
			// El kernel distingue los túneles GRE por (local, remoto, clave):
			// un remoto por túnel evita ambigüedades y permite el filtro por
			// IP de origen (ipset de peers GRE).
			if other, dup := seenRemoteGRE[t.Remote]; dup {
				return nil, bad(d.PeerID, "gre remote %s also in %s", t.Remote, other)
			}
			seenRemoteGRE[t.Remote] = d.PeerID
			t.PSK, t.LocalID, t.RemoteID = "", "", ""
		case TransportIPsec:
			t.GREKey = 0
			if !ValidIKEID(t.LocalID) || !ValidIKEID(t.RemoteID) {
				return nil, bad(d.PeerID, "ike identities")
			}
			if other, dup := seenRemoteID[t.RemoteID]; dup {
				return nil, bad(d.PeerID, "remote id also in %s", other)
			}
			seenRemoteID[t.RemoteID] = d.PeerID
			if len(t.PSK) < MinPSKLen || len(t.PSK) > maxPSKLen || !pskRe.MatchString(t.PSK) {
				return nil, bad(d.PeerID, "psk")
			}
		}
		out[t.Index] = t
	}
	return out, nil
}

// Observed es un túnel observado en el host (interfaz y, en ipsec, conexión).
type Observed struct {
	Transport string
	Index     uint32
	// Fingerprint resume la configuración aplicada (ver Fingerprint); vacío
	// = desconocida (p. ej. tras reiniciar el agente): se reaplica.
	Fingerprint string
}

// Fingerprint resume los campos que obligan a reconfigurar un túnel. Incluye
// un resumen de la PSK sin revelarla (la calcula el llamante con hash).
func Fingerprint(t Tunnel, pskDigest string) string {
	return strings.Join([]string{t.Transport, t.TunnelAddress.String(), t.HubAddress.String(), addrOrAny(t.Local), addrOrAny(t.Remote),
		t.LocalID, t.RemoteID, pskDigest, strconv.Itoa(t.MTU), strconv.FormatUint(uint64(t.GREKey), 10)}, "|")
}

func addrOrAny(a netip.Addr) string {
	if !a.IsValid() {
		return "%any"
	}
	return a.String()
}

// Plan son los cambios a aplicar.
type Plan struct {
	Upsert []Tunnel
	// Remove son túneles observados ausentes del estado deseado.
	Remove []Observed
}

// Diff calcula el plan. fp devuelve la huella deseada de un túnel.
func Diff(desired map[uint32]Tunnel, observed []Observed, fp func(Tunnel) string) Plan {
	var p Plan
	obs := make(map[uint32]Observed, len(observed))
	for _, o := range observed {
		obs[o.Index] = o
	}
	for idx, t := range desired {
		o, ok := obs[idx]
		if !ok || o.Transport != t.Transport || o.Fingerprint == "" || o.Fingerprint != fp(t) {
			p.Upsert = append(p.Upsert, t)
		}
	}
	for _, o := range observed {
		if t, ok := desired[o.Index]; !ok || t.Transport != o.Transport {
			p.Remove = append(p.Remove, o)
		}
	}
	slices.SortFunc(p.Upsert, func(a, b Tunnel) int { return cmpU32(a.Index, b.Index) })
	slices.SortFunc(p.Remove, func(a, b Observed) int {
		if c := cmpU32(a.Index, b.Index); c != 0 {
			return c
		}
		return strings.Compare(a.Transport, b.Transport)
	})
	return p
}

func cmpU32(a, b uint32) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
