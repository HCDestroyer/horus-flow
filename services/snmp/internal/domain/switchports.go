package domain

import (
	"net/netip"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Puertos de switches (docs/vendors/mikrotik.md §13, huawei.md §11): el
// operador elige qué puertos se sondean cada intervalo y cuáles son de
// subida. Sin elección se aplica la de por defecto: los puertos con vecino
// conocido (uplink sugerido por LLDP) y los que tienen descripción (ifAlias),
// con un máximo por switch para acotar la cardinalidad de las series.
const (
	// MaxSwitchPorts es el máximo de puertos sondeados por switch.
	MaxSwitchPorts = 64
	// FlapWindow es la ventana de los cambios de estado operativo que se
	// recuerdan por puerto; FlapThreshold los que en una hora hacen que un
	// puerto de subida se considere inestable.
	FlapWindow    = 24 * time.Hour
	FlapThreshold = 4
	maxFlapMarks  = 200
)

// IsSwitchPort indica si una interfaz es un puerto del switch (físico o
// agregado) elegible para el sondeo: ethernet/sfp de RouterOS (etherN,
// sfp-sfpplusN, qsfp…, bonding), SwOS (PortN, SFP+N), Huawei serie S
// (GigabitEthernet, XGigabitEthernet, 25GE/40GE/100GE, Eth-Trunk). Nunca
// gestión (MEth), VLAN, bridge, loopback, NULL ni túneles.
func IsSwitchPort(ifType int, name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || strings.Contains(n, ":") || strings.Contains(n, ".") {
		return false // subinterfaces y dinámicas
	}
	for _, p := range []string{"meth", "vlanif", "null", "inloopback", "loopback", "lo", "bridge", "vlan", "wg", "gre", "ipip", "eoip",
		"pppoe", "<"} {
		if n == p || strings.HasPrefix(n, p) {
			return false
		}
	}
	switch ifType {
	case IfTypeEthernet, IfTypeLAG, 62, 69, 117: // ethernetCsmacd, ieee8023adLag y los ifType Ethernet antiguos
		return true
	case IfTypePropVirtual:
		return strings.HasPrefix(n, "eth-trunk")
	}
	return false
}

// PortCandidate es un puerto descubierto con lo que decide su elección.
type PortCandidate struct {
	IfIndex int
	Name    string
	Alias   string
	IfType  int
	// SuggestedUplink: su vecino LLDP es un equipo conocido de Horus.
	SuggestedUplink bool
}

// PortChoice es la elección de un puerto.
type PortChoice struct {
	Monitored bool `json:"monitored"`
	Uplink    bool `json:"uplink"`
}

// DefaultPortSelection elige los puertos por defecto: primero los uplinks
// sugeridos y después los que tienen descripción, en orden de ifIndex, hasta
// MaxSwitchPorts. Los uplinks sugeridos se marcan como subida.
func DefaultPortSelection(ports []PortCandidate) map[string]PortChoice {
	elig := make([]PortCandidate, 0, len(ports))
	for _, p := range ports {
		if IsSwitchPort(p.IfType, p.Name) && (p.SuggestedUplink || strings.TrimSpace(p.Alias) != "") {
			elig = append(elig, p)
		}
	}
	sort.SliceStable(elig, func(i, j int) bool {
		if elig[i].SuggestedUplink != elig[j].SuggestedUplink {
			return elig[i].SuggestedUplink
		}
		return elig[i].IfIndex < elig[j].IfIndex
	})
	out := map[string]PortChoice{}
	for _, p := range elig {
		if len(out) >= MaxSwitchPorts {
			break
		}
		out[p.Name] = PortChoice{Monitored: true, Uplink: p.SuggestedUplink}
	}
	return out
}

// KnownDevice es un equipo de Horus del mismo ISP (router o switch) con el
// que se reconocen los vecinos LLDP.
type KnownDevice struct {
	Name  string
	Addrs []netip.Addr
}

// NormalizeSysName pasa a minúsculas y quita el dominio ("rt-core-1.isp.net"
// → "rt-core-1").
func NormalizeSysName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexByte(s, '.'); i > 0 {
		if _, err := netip.ParseAddr(s); err != nil {
			s = s[:i]
		}
	}
	return s
}

// MatchKnownDevice devuelve el nombre del equipo de Horus que es el vecino
// (por nombre de sistema o dirección de gestión; "" si ninguno).
func MatchKnownDevice(sysName, mgmt string, known []KnownDevice) string {
	n := NormalizeSysName(sysName)
	var addr netip.Addr
	if a, err := netip.ParseAddr(strings.TrimSpace(mgmt)); err == nil {
		addr = a.Unmap()
	}
	for _, k := range known {
		if n != "" && NormalizeSysName(k.Name) == n {
			return k.Name
		}
		if addr.IsValid() && slices.Contains(k.Addrs, addr) {
			return k.Name
		}
	}
	return ""
}

// AddFlap registra un cambio de estado operativo y descarta los más viejos
// que FlapWindow (como mucho maxFlapMarks).
func AddFlap(marks []time.Time, at time.Time) []time.Time {
	marks = append(marks, at)
	return TrimFlaps(marks, at)
}

// TrimFlaps descarta las marcas fuera de la ventana.
func TrimFlaps(marks []time.Time, now time.Time) []time.Time {
	i := 0
	for i < len(marks) && now.Sub(marks[i]) > FlapWindow {
		i++
	}
	marks = marks[i:]
	if len(marks) > maxFlapMarks {
		marks = marks[len(marks)-maxFlapMarks:]
	}
	return marks
}

// CountFlaps cuenta los cambios de estado en la ventana d hasta now.
func CountFlaps(marks []time.Time, now time.Time, d time.Duration) int {
	n := 0
	for _, m := range marks {
		if now.Sub(m) <= d {
			n++
		}
	}
	return n
}

// PortSelection es la elección de puertos de un switch guardada por el
// operador (snmp.switch_port_selection + snmp.switch_port). Un puerto
// presente en Ports se sondea; Uplink marca los de subida.
type PortSelection struct {
	RouterID  uuid.UUID
	Ports     map[string]PortChoice
	UpdatedAt time.Time
	UpdatedBy *string
	Version   int
}

// Dimensiones del top de muestras sFlow de un switch.
const (
	SwitchTopSrc      = "src_ip"
	SwitchTopDst      = "dst_ip"
	SwitchTopProtocol = "protocol"
)

// SwitchTopRow es una fila del top (bytes y paquetes ya escalados por la
// tasa de muestreo).
type SwitchTopRow struct {
	Key     string `json:"key"`
	Bytes   uint64 `json:"bytes,string"`
	Packets uint64 `json:"packets,string"`
}
