// Package domain contiene las reglas puras del agente del hub WireGuard:
// validación del estado deseado (sin claves privadas de routers: solo claves
// públicas y /32 de túnel) y el cálculo del plan de cambios sobre los peers
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

// Normalize valida el estado deseado y lo convierte en peers del hub. Cada
// allowed-ip debe ser una /32 IPv4 (o /128) única: un peer de router nunca
// enruta más que su IP de túnel.
func Normalize(desired []DesiredPeer) (map[string]Peer, error) {
	out := make(map[string]Peer, len(desired))
	seenIP := map[netip.Prefix]string{}
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
		for _, s := range d.AllowedIPs {
			pfx, err := netip.ParsePrefix(s)
			if err != nil || !pfx.IsSingleIP() {
				return nil, fmt.Errorf("%w: peer %s: allowed ip %q must be a single address", ErrInvalidState, d.PeerID, s)
			}
			if other, dup := seenIP[pfx]; dup {
				return nil, fmt.Errorf("%w: allowed ip %s in peers %s and %s", ErrInvalidState, pfx, other, d.PeerID)
			}
			seenIP[pfx] = d.PeerID
			p.AllowedIPs = append(p.AllowedIPs, pfx)
		}
		slices.SortFunc(p.AllowedIPs, func(a, b netip.Prefix) int { return a.Addr().Compare(b.Addr()) })
		out[d.PublicKey] = p
	}
	return out, nil
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
	slices.SortFunc(ips, func(a, b netip.Prefix) int { return a.Addr().Compare(b.Addr()) })
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
