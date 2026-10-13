package domain

import (
	"sort"
	"strings"

	"github.com/hcdestroyer/horus-flow/services/snmp/api"
)

// Valores de ifType (IANAifType) que maneja el rol.
const (
	IfTypeOther       = 1
	IfTypeEthernet    = 6
	IfTypePPP         = 23
	IfTypeLoopback    = 24
	IfTypePropVirtual = 53
	IfTypeWLAN        = 71
	IfTypeTunnel      = 131
	IfTypeL2VLAN      = 135
	IfTypeLAG         = 161
	IfTypeBridge      = 209
	IfTypeL3IPVLAN    = 136
)

// IfTypeName devuelve el nombre IANA del ifType (RouterInterface.type).
func IfTypeName(t int) string {
	switch t {
	case IfTypeEthernet:
		return "ethernetCsmacd"
	case IfTypeL2VLAN:
		return "l2vlan"
	case IfTypeBridge:
		return "bridge"
	case IfTypeLAG:
		return "ieee8023adLag"
	case IfTypePPP:
		return "ppp"
	case IfTypeTunnel:
		return "tunnel"
	case IfTypeWLAN:
		return "ieee80211"
	case IfTypeLoopback:
		return "softwareLoopback"
	case IfTypePropVirtual:
		return "propVirtual"
	case IfTypeL3IPVLAN:
		return "l3ipvlan"
	default:
		return "other"
	}
}

// OperStatusName traduce ifOperStatus.
func OperStatusName(v int) string {
	switch v {
	case 1:
		return api.OperUp
	case 2:
		return api.OperDown
	case 3:
		return api.OperTesting
	case 5:
		return api.OperDormant
	case 6:
		return api.OperNotPresent
	case 7:
		return api.OperLowerLayerDown
	default:
		return api.OperUnknown
	}
}

// Monitored decide si una interfaz se sondea cada intervalo: físicas
// (ethernet, sfp, wlan), VLAN, bridge y bonding; nunca las dinámicas por
// cliente (`<pppoe-usuario>`, entre ángulos), PPP, túneles ni loopback
// (docs/vendors/mikrotik.md §3.3).
func Monitored(ifType int, name string) bool {
	n := strings.TrimSpace(name)
	if n == "" || strings.HasPrefix(n, "<") || strings.HasPrefix(strings.ToLower(n), "pppoe-") || n == "lo" {
		return false
	}
	switch ifType {
	case IfTypeEthernet, IfTypeWLAN, IfTypeL2VLAN, IfTypeBridge, IfTypeLAG:
		return true
	}
	return false
}

// UplinkCandidate describe una interfaz para elegir la de subida.
type UplinkCandidate struct {
	IfIndex int
	IfType  int
	Name    string
	Alias   string
	// Upstream: marcada como upstream en el inventario (flow_role).
	Upstream bool
	OperUp   bool
	// AvgTxBps es la media móvil de tx_bps (nil = sin tasas aún).
	AvgTxBps *float64
}

var uplinkHints = []string{"UPLINK", "WAN", "TRANSIT", "UPSTREAM"}

// AliasHintsUplink indica si el alias sugiere la subida.
func AliasHintsUplink(alias string) bool {
	a := strings.ToUpper(alias)
	for _, h := range uplinkHints {
		if strings.Contains(a, h) {
			return true
		}
	}
	return false
}

// ChooseUplink elige la interfaz física o VLAN (no bridge) con mayor tx_bps
// medio; las marcadas upstream en el inventario o cuyo alias contiene
// UPLINK/WAN/TRANSIT/UPSTREAM ganan a las demás. Devuelve el ifIndex o 0.
func ChooseUplink(cands []UplinkCandidate) int {
	type scored struct {
		idx   int
		class int
		tx    float64
		name  string
	}
	var list []scored
	for _, c := range cands {
		switch c.IfType {
		case IfTypeEthernet, IfTypeWLAN, IfTypeL2VLAN, IfTypeLAG:
		default:
			continue
		}
		s := scored{idx: c.IfIndex, name: c.Name}
		if c.Upstream {
			s.class = 2
		} else if AliasHintsUplink(c.Alias) {
			s.class = 1
		}
		if c.AvgTxBps != nil {
			s.tx = *c.AvgTxBps
		}
		if !c.OperUp && s.class == 0 && s.tx == 0 {
			continue
		}
		list = append(list, s)
	}
	if len(list) == 0 {
		return 0
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].class != list[j].class {
			return list[i].class > list[j].class
		}
		if list[i].tx != list[j].tx {
			return list[i].tx > list[j].tx
		}
		return list[i].name < list[j].name
	})
	return list[0].idx
}

// EWMA actualiza una media móvil exponencial (alpha 0.3) con una muestra.
func EWMA(prev *float64, sample float64) *float64 {
	if prev == nil {
		return &sample
	}
	v := 0.7**prev + 0.3*sample
	return &v
}
