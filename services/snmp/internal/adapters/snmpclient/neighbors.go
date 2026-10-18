package snmpclient

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Vecinos de capa 2 de un switch, en solo lectura (docs/vendors/mikrotik.md
// §13.4, docs/vendors/huawei.md §11.4):
//
//   - LLDP-MIB (IEEE 802.1AB, rama 1.0.8802.1.1.2): lldpRemTable con el
//     nombre del sistema y el puerto del vecino, lldpRemManAddrTable con su
//     dirección de gestión y lldpLocPortTable para traducir el puerto local
//     (lldpLocPortNum) al ifIndex de IF-MIB por nombre.
//   - MIKROTIK-MIB mtxrNeighborTable (1.3.6.1.4.1.14988.1.1.11.1.1): vecinos
//     de /ip neighbor (MNDP, CDP y LLDP) con la identidad, la IP y el ifIndex
//     local. RouterOS no documenta LLDP-MIB entre sus MIB; esta tabla es el
//     respaldo y se usa cuando LLDP-MIB no devuelve filas.
//
// SwOS no tiene ninguna de las dos (sin LLDP): devuelve lista vacía.
const (
	OIDLldpLocPortTable  = ".1.0.8802.1.1.2.1.3.7.1"
	OIDLldpRemTable      = ".1.0.8802.1.1.2.1.4.1.1"
	OIDLldpRemManAddrTab = ".1.0.8802.1.1.2.1.4.2.1"
	OIDMtxrNeighborTable = ".1.3.6.1.4.1.14988.1.1.11.1.1"

	lldpLocColPortIDSubtype = "2"
	lldpLocColPortID        = "3"
	lldpLocColPortDesc      = "4"
	lldpRemColChassisSub    = "4"
	lldpRemColChassisID     = "5"
	lldpRemColPortIDSubtype = "6"
	lldpRemColPortID        = "7"
	lldpRemColPortDesc      = "8"
	lldpRemColSysName       = "9"
	lldpRemManColIfSubtype  = "3"

	mtxrNbColIP        = "2"
	mtxrNbColMAC       = "3"
	mtxrNbColPlatform  = "5"
	mtxrNbColIdentity  = "6"
	mtxrNbColInterface = "8"

	// Subtipos LLDP (IEEE 802.1AB): chassis/port macAddress(4/3),
	// interfaceName(6/5), local(7/7), networkAddress(5/4).
	lldpChassisMAC = 4
	lldpPortMAC    = 3
	maxNeighbors   = 1024
)

// Neighbor es un vecino visto en un puerto del switch.
type Neighbor struct {
	// LocalIfIndex es el ifIndex local (0 si no se pudo traducir).
	LocalIfIndex int
	// LocalPort es el puerto local tal como lo publica el MIB.
	LocalPort   string
	SysName     string
	PortID      string
	PortDesc    string
	ChassisID   string
	MgmtAddress string
	Platform    string
	// Source: lldp | mndp (tabla de vecinos de MikroTik).
	Source string
}

// ReadNeighbors lee LLDP-MIB y, si no hay filas, la tabla de vecinos de
// MIKROTIK-MIB. disc traduce el puerto local LLDP al ifIndex por nombre.
// Un OID ausente no es error: devuelve lo que haya.
func ReadNeighbors(ctx context.Context, t Transport, disc []Discovered) ([]Neighbor, error) {
	out, err := readLLDP(ctx, t, disc)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		if out, err = readMtxrNeighbors(ctx, t, disc); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LocalIfIndex != out[j].LocalIfIndex {
			return out[i].LocalIfIndex < out[j].LocalIfIndex
		}
		return out[i].SysName < out[j].SysName
	})
	if len(out) > maxNeighbors {
		out = out[:maxNeighbors]
	}
	return out, nil
}

type lldpLocal struct {
	subtype int64
	id      string
	desc    string
}

func readLLDP(ctx context.Context, t Transport, disc []Discovered) ([]Neighbor, error) {
	locals := map[string]*lldpLocal{}
	for _, col := range []string{lldpLocColPortIDSubtype, lldpLocColPortID, lldpLocColPortDesc} {
		root := OIDLldpLocPortTable + "." + col
		rows, err := t.Walk(ctx, root)
		if err != nil {
			return nil, err
		}
		for _, v := range rows {
			idx, ok := strings.CutPrefix(normOID(v.OID), root+".")
			if !ok {
				continue
			}
			l := locals[idx]
			if l == nil {
				l = &lldpLocal{}
				locals[idx] = l
			}
			switch col {
			case lldpLocColPortIDSubtype:
				l.subtype, _ = asInt(v.Value)
			case lldpLocColPortID:
				s, _ := v.Value.(string)
				l.id = s
			case lldpLocColPortDesc:
				s, _ := v.Value.(string)
				l.desc = strings.TrimSpace(s)
			}
		}
	}
	type remKey struct{ port, index string }
	rem := map[remKey]*Neighbor{}
	subtypes := map[remKey][2]int64{}
	var order []remKey
	for _, col := range []string{lldpRemColChassisSub, lldpRemColChassisID, lldpRemColPortIDSubtype, lldpRemColPortID, lldpRemColPortDesc,
		lldpRemColSysName} {
		root := OIDLldpRemTable + "." + col
		rows, err := t.Walk(ctx, root)
		if err != nil {
			return nil, err
		}
		for _, v := range rows {
			idx, ok := strings.CutPrefix(normOID(v.OID), root+".")
			if !ok {
				continue
			}
			parts := strings.Split(idx, ".") // lldpRemTimeMark.lldpRemLocalPortNum.lldpRemIndex
			if len(parts) != 3 {
				continue
			}
			k := remKey{parts[1], parts[2]}
			n := rem[k]
			if n == nil {
				n = &Neighbor{Source: "lldp"}
				rem[k] = n
				order = append(order, k)
			}
			s, _ := v.Value.(string)
			st := subtypes[k]
			switch col {
			case lldpRemColChassisSub:
				st[0], _ = asInt(v.Value)
			case lldpRemColChassisID:
				n.ChassisID = s
			case lldpRemColPortIDSubtype:
				st[1], _ = asInt(v.Value)
			case lldpRemColPortID:
				n.PortID = s
			case lldpRemColPortDesc:
				n.PortDesc = strings.TrimSpace(s)
			case lldpRemColSysName:
				n.SysName = strings.TrimSpace(s)
			}
			subtypes[k] = st
		}
	}
	if len(rem) == 0 {
		return nil, nil
	}
	// Direcciones de gestión: índice …LocalPortNum.RemIndex.AddrSubtype.Len.Octetos.
	root := OIDLldpRemManAddrTab + "." + lldpRemManColIfSubtype
	rows, err := t.Walk(ctx, root)
	if err != nil {
		return nil, err
	}
	for _, v := range rows {
		idx, ok := strings.CutPrefix(normOID(v.OID), root+".")
		if !ok {
			continue
		}
		parts := strings.Split(idx, ".")
		if len(parts) < 5 {
			continue
		}
		k := remKey{parts[1], parts[2]}
		n := rem[k]
		if n == nil || n.MgmtAddress != "" {
			continue
		}
		if a, ok := manAddr(parts[3:]); ok {
			n.MgmtAddress = a
		}
	}
	byName := ifIndexByName(disc)
	out := make([]Neighbor, 0, len(order))
	for _, k := range order {
		n := rem[k]
		st := subtypes[k]
		n.ChassisID = printable(n.ChassisID, st[0] == lldpChassisMAC)
		n.PortID = printable(n.PortID, st[1] == lldpPortMAC)
		if l := locals[k.port]; l != nil {
			n.LocalPort = printable(l.id, l.subtype == lldpPortMAC)
			n.LocalIfIndex = byName[strings.ToLower(n.LocalPort)]
			if n.LocalIfIndex == 0 && l.desc != "" {
				n.LocalIfIndex = byName[strings.ToLower(l.desc)]
				if n.LocalPort == "" || l.subtype == lldpPortMAC {
					n.LocalPort = l.desc
				}
			}
		}
		if n.LocalIfIndex == 0 {
			// Respaldo: muchos agentes numeran lldpLocPortNum = ifIndex.
			if p, err := strconv.Atoi(k.port); err == nil && hasIfIndex(disc, p) {
				n.LocalIfIndex = p
			}
		}
		out = append(out, *n)
	}
	return out, nil
}

func readMtxrNeighbors(ctx context.Context, t Transport, disc []Discovered) ([]Neighbor, error) {
	rows := map[string]*Neighbor{}
	var order []string
	for _, col := range []string{mtxrNbColIP, mtxrNbColMAC, mtxrNbColPlatform, mtxrNbColIdentity, mtxrNbColInterface} {
		root := OIDMtxrNeighborTable + "." + col
		vars, err := t.Walk(ctx, root)
		if err != nil {
			return nil, err
		}
		for _, v := range vars {
			idx, ok := strings.CutPrefix(normOID(v.OID), root+".")
			if !ok {
				continue
			}
			n := rows[idx]
			if n == nil {
				n = &Neighbor{Source: "mndp"}
				rows[idx] = n
				order = append(order, idx)
			}
			s, _ := v.Value.(string)
			switch col {
			case mtxrNbColIP:
				if a, err := netip.ParseAddr(strings.TrimSpace(s)); err == nil && !a.IsUnspecified() {
					n.MgmtAddress = a.String()
				}
			case mtxrNbColMAC:
				n.ChassisID = printable(s, true)
			case mtxrNbColPlatform:
				n.Platform = strings.TrimSpace(s)
			case mtxrNbColIdentity:
				n.SysName = strings.TrimSpace(s)
			case mtxrNbColInterface:
				if i, ok := asInt(v.Value); ok {
					n.LocalIfIndex = int(i)
				}
			}
		}
	}
	names := map[int]string{}
	for _, d := range disc {
		names[d.IfIndex] = d.Name
	}
	out := make([]Neighbor, 0, len(order))
	for _, k := range order {
		n := rows[k]
		if !hasIfIndex(disc, n.LocalIfIndex) {
			n.LocalIfIndex = 0
		}
		n.LocalPort = names[n.LocalIfIndex]
		out = append(out, *n)
	}
	return out, nil
}

func ifIndexByName(disc []Discovered) map[string]int {
	m := make(map[string]int, len(disc))
	for _, d := range disc {
		if d.Name != "" {
			m[strings.ToLower(d.Name)] = d.IfIndex
		}
	}
	return m
}

func hasIfIndex(disc []Discovered, i int) bool {
	for _, d := range disc {
		if d.IfIndex == i {
			return true
		}
	}
	return false
}

// manAddr interpreta AddrSubtype.Len.Octetos de un índice LLDP (IANA
// AddressFamilyNumbers: 1 IPv4, 2 IPv6).
func manAddr(p []string) (string, bool) {
	sub, err1 := strconv.Atoi(p[0])
	n, err2 := strconv.Atoi(p[1])
	if err1 != nil || err2 != nil || len(p) < 2+n {
		return "", false
	}
	b := make([]byte, 0, n)
	for _, x := range p[2 : 2+n] {
		v, err := strconv.Atoi(x)
		if err != nil || v < 0 || v > 255 {
			return "", false
		}
		b = append(b, byte(v))
	}
	switch {
	case sub == 1 && n == 4:
		return netip.AddrFrom4([4]byte(b)).String(), true
	case sub == 2 && n == 16:
		return netip.AddrFrom16([16]byte(b)).String(), true
	}
	return "", false
}

// printable devuelve s si es texto imprimible; si no (o si es una MAC en
// bruto), los octetos en hexadecimal separados por ':'.
func printable(s string, mac bool) string {
	if s == "" {
		return ""
	}
	if mac && len(s) == 6 {
		return hexColon(s)
	}
	for _, r := range s {
		if !unicode.IsPrint(r) || r == unicode.ReplacementChar {
			return hexColon(s)
		}
	}
	return strings.TrimSpace(s)
}

func hexColon(s string) string {
	parts := make([]string, 0, len(s))
	for i := 0; i < len(s); i++ {
		parts = append(parts, fmt.Sprintf("%02x", s[i]))
	}
	return strings.Join(parts, ":")
}
