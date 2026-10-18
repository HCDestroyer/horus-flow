// Package domain contiene las reglas puras del agente del hub WireGuard:
// validación del estado deseado (sin claves privadas de routers: solo claves
// públicas, /32 de túnel y direcciones Horus de equipos vía router) y el cálculo del plan de cambios sobre los peers
// observados en la interfaz. Fail-static: el plan solo borra peers ausentes
// de un estado deseado completo y válido; nunca por un error del control.
package domain

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"time"
)

// Peer es un peer del hub (deseado u observado).
type Peer struct {
	PublicKey  string
	AllowedIPs []netip.Prefix
	Keepalive  time.Duration
	// Solo observados.
	Endpoint      string
	LastHandshake time.Time
	RxBytes       uint64
	TxBytes       uint64
}

// DesiredPeer es un peer del estado deseado.
type DesiredPeer struct {
	PeerID     string
	TenantID   string
	PublicKey  string
	AllowedIPs []string
	Keepalive  int32
}

// ErrInvalidState indica un estado deseado inválido (se rechaza entero).
var ErrInvalidState = errors.New("wgagent: invalid desired state")

// ValidKey indica si k es una clave WireGuard (32 B en base64 estándar).
func ValidKey(k string) bool {
	if len(k) != 44 {
		return false
	}
	b, err := base64.StdEncoding.DecodeString(k)
	return err == nil && len(b) == 32
}

// MinRoutedBits es el prefijo IPv4 más grande admitido como allowed-ip
// además de las /32: el bloque Horus de una red de gestión declarada en un
// router del sitio (switches vía router, ADR-0032). Más grande que /22 se
// rechaza: un error del control nunca debe poder enrutar medio pool a un
// solo router.
const MinRoutedBits = 22

// Normalize valida el estado deseado y lo convierte en peers del hub. Cada
// peer trae al menos una dirección única (su /32 de túnel) y, además, las
// direcciones Horus de los equipos que se alcanzan a través de él (switches
// vía router: /32 o un bloque IPv4 canónico de /22 a /31, ADR-0032). Ningún
// allowed-ip puede solaparse con otro, de ningún peer: WireGuard elige el
// peer por la dirección de destino (cryptokey routing) y un solape mandaría
// el tráfico de un ISP al router de otro. Por eso el hub nunca recibe las
// IP privadas de los ISP, solo direcciones de su propio pool.
func Normalize(desired []DesiredPeer) (map[string]Peer, error) {
	out := make(map[string]Peer, len(desired))
	type owned struct {
		pfx  netip.Prefix
		peer string
	}
	var all []owned
	for _, d := range desired {
		if !ValidKey(d.PublicKey) {
			return nil, fmt.Errorf("%w: peer %s: public key", ErrInvalidState, d.PeerID)
		}
		if _, dup := out[d.PublicKey]; dup {
			return nil, fmt.Errorf("%w: duplicated public key (peer %s)", ErrInvalidState, d.PeerID)
		}
		if len(d.AllowedIPs) == 0 {
			return nil, fmt.Errorf("%w: peer %s: no allowed ips", ErrInvalidState, d.PeerID)
		}
		p := Peer{PublicKey: d.PublicKey, Keepalive: time.Duration(d.Keepalive) * time.Second}
		single := false
		for _, s := range d.AllowedIPs {
			pfx, err := netip.ParsePrefix(s)
			if err != nil || !validAllowed(pfx) {
				return nil, fmt.Errorf("%w: peer %s: allowed ip %q must be a single address or a canonical IPv4 block /%d-/31",
					ErrInvalidState, d.PeerID, s, MinRoutedBits)
			}
			single = single || pfx.IsSingleIP()
			for _, o := range all {
				if o.pfx.Overlaps(pfx) {
					return nil, fmt.Errorf("%w: allowed ip %s in peer %s overlaps %s in peer %s", ErrInvalidState, pfx, d.PeerID, o.pfx, o.peer)
				}
			}
			all = append(all, owned{pfx: pfx, peer: d.PeerID})
			p.AllowedIPs = append(p.AllowedIPs, pfx)
		}
		if !single {
			return nil, fmt.Errorf("%w: peer %s: no tunnel address (single ip)", ErrInvalidState, d.PeerID)
		}
		slices.SortFunc(p.AllowedIPs, comparePrefix)
		out[d.PublicKey] = p
	}
	return out, nil
}

// validAllowed: una dirección única (IPv4 /32 o IPv6 /128) o un bloque IPv4
// canónico entre /MinRoutedBits y /31.
func validAllowed(p netip.Prefix) bool {
	if !p.IsValid() || p.Masked() != p {
		return false
	}
	if p.IsSingleIP() {
		return true
	}
	return p.Addr().Is4() && p.Bits() >= MinRoutedBits
}

func comparePrefix(a, b netip.Prefix) int {
	if c := a.Addr().Compare(b.Addr()); c != 0 {
		return c
	}
	return a.Bits() - b.Bits()
}

// Plan son los cambios a aplicar en la interfaz.
type Plan struct {
	Upsert []Peer
	Remove []string
}

func samePeer(a, b Peer) bool {
	return a.Keepalive == b.Keepalive && slices.Equal(a.AllowedIPs, b.AllowedIPs)
}

// Diff calcula el plan para pasar de observed a desired (ambos por clave
// pública). Los peers iguales no se tocan.
func Diff(desired, observed map[string]Peer) Plan {
	var p Plan
	for k, d := range desired {
		if o, ok := observed[k]; !ok || !samePeer(d, normalizeObserved(o)) {
			p.Upsert = append(p.Upsert, d)
		}
	}
	for k := range observed {
		if _, ok := desired[k]; !ok {
			p.Remove = append(p.Remove, k)
		}
	}
	slices.SortFunc(p.Upsert, func(a, b Peer) int { return compare(a.PublicKey, b.PublicKey) })
	slices.Sort(p.Remove)
	return p
}

func normalizeObserved(o Peer) Peer {
	ips := slices.Clone(o.AllowedIPs)
	slices.SortFunc(ips, comparePrefix)
	o.AllowedIPs = ips
	return o
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
