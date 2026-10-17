package domain

import (
	"crypto/sha1" //nolint:gosec // solo para acortar nombres, no es seguridad
	"encoding/hex"
	"fmt"
	"net/netip"
	"strings"
)

// Sondas por enlace en Huawei VRP (docs/vendors/huawei.md §4): pruebas NQA
// ICMP que el OPERADOR configura en el router (Horus nunca escribe) y que
// Horus solo LEE por SNMP (DISMAN-PING-MIB / NQA-MIB). Convención:
// admin-name `horus`; test-name `gw-<interfaz>` (puerta de enlace del
// proveedor por esa interfaz) o `intl-<ip>` (referencia internacional).

// NQAOwner es el admin-name de las pruebas de Horus (pingCtlOwnerIndex).
const NQAOwner = "horus"

// ProbeSourceRouterNQA: lectura de una prueba NQA del router.
const ProbeSourceRouterNQA = "router_nqa"

const (
	nqaGatewayPrefix = "gw-"
	nqaIntlPrefix    = "intl-"
	nqaHashedPrefix  = "gw-h"
	// nqaTestNameMax es la longitud máxima del test-name de NQA (1..32).
	nqaTestNameMax = 32
)

// NQAInterfaceToken convierte el nombre de una interfaz en la parte del
// test-name: solo letras, dígitos, '.', '-' y '_' ('/' y ':' → '_').
func NQAInterfaceToken(ifName string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(ifName) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// NQAGatewayTestName es el test-name de la sonda a la puerta de enlace por
// una interfaz; si no cabe en 32 caracteres se usa un resumen estable
// (`gw-h` + 8 hex del SHA-1 del nombre).
func NQAGatewayTestName(ifName string) string {
	if n := nqaGatewayPrefix + NQAInterfaceToken(ifName); len(n) <= nqaTestNameMax {
		return n
	}
	sum := sha1.Sum([]byte(strings.TrimSpace(ifName))) //nolint:gosec // identificador, no seguridad
	return nqaHashedPrefix + hex.EncodeToString(sum[:4])
}

// NQAIntlTestName es el test-name de una referencia internacional.
func NQAIntlTestName(a netip.Addr) string { return nqaIntlPrefix + a.String() }

// ParseNQATestName interpreta un test-name de Horus: (ref gw, nombre en
// test-name) o (ref intl). ok=false si no sigue la convención.
func ParseNQATestName(test string) (ref string, token string, ok bool) {
	switch {
	case strings.HasPrefix(test, nqaIntlPrefix):
		return ProbeRefInternational, "", true
	case strings.HasPrefix(test, nqaGatewayPrefix) && len(test) > len(nqaGatewayPrefix):
		return ProbeRefGateway, test, true
	}
	return "", "", false
}

// NQAMatchesInterface indica si el test-name de puerta de enlace corresponde
// a la interfaz ifName (mismo cálculo que el que generó la configuración).
func NQAMatchesInterface(test, ifName string) bool {
	return ifName != "" && NQAGatewayTestName(ifName) == test
}

// BuildNQASnippet genera la configuración VRP que el USUARIO revisa y pega
// (Horus nunca escribe en routers; solo comandos, sin comentarios, porque en
// la CLI de VRP cada línea pegada se ejecuta): una prueba NQA ICMP por enlace hacia la
// puerta de enlace del proveedor ({GATEWAY_n}) con la interfaz de origen, y
// una por referencia internacional; 10 sondas cada 30 s. La lectura es por
// SNMP con la vista de la configuración de alta (DISMAN-PING-MIB y NQA-MIB).
// Sintaxis según la documentación de VRP V8 (NE40E/NetEngine 8000) y V5
// (AR): PENDIENTE DE VALIDACIÓN EN EQUIPO REAL (docs/vendors/huawei.md §6).
func BuildNQASnippet(uplinks []SnippetUplink, refs []netip.Addr) NetwatchSnippet {
	out := NetwatchSnippet{Placeholders: []NetwatchPlaceholder{}, Warnings: []string{}}
	var b strings.Builder
	b.WriteString("system-view\n")
	if len(uplinks) == 0 {
		out.Warnings = append(out.Warnings, "El router no tiene enlaces de subida detectados ni configurados: solo se generan las referencias internacionales.")
	}
	for i, u := range uplinks {
		key := fmt.Sprintf("{GATEWAY_%d}", i+1)
		test := NQAGatewayTestName(u.Name)
		out.Placeholders = append(out.Placeholders, NetwatchPlaceholder{Key: key, Interface: u.Name,
			Description: "IP de la puerta de enlace del proveedor por " + u.Name + " (" + safeComment(u.Label) + ")"})
		fmt.Fprintf(&b, "nqa test-instance %s %s\n", NQAOwner, test)
		b.WriteString(" test-type icmp\n")
		fmt.Fprintf(&b, " destination-address ipv4 %s\n", key)
		if vrpIfName(u.Name) {
			fmt.Fprintf(&b, " source-interface %s\n", u.Name)
		} else {
			out.Warnings = append(out.Warnings, "Nombre de interfaz no reconocido ("+safeComment(u.Name)+"): añada source-interface a mano.")
		}
		writeNQATiming(&b)
	}
	for _, r := range refs {
		if !r.Is4() {
			continue
		}
		fmt.Fprintf(&b, "nqa test-instance %s %s\n", NQAOwner, NQAIntlTestName(r))
		b.WriteString(" test-type icmp\n")
		fmt.Fprintf(&b, " destination-address ipv4 %s\n", r)
		writeNQATiming(&b)
	}
	b.WriteString("return\n")
	out.Script = b.String()
	return out
}

// writeNQATiming: 10 sondas a 1 s con 2 s de espera cada 30 s (frequency >
// (probe-count − 1) × interval + timeout, como exige VRP) y arranque inmediato.
func writeNQATiming(b *strings.Builder) {
	b.WriteString(" probe-count 10\n interval seconds 1\n timeout 2\n frequency 30\n start now\n quit\n")
}

// vrpIfName acepta nombres de interfaz de VRP sin espacios ni caracteres que
// rompan la línea (GigabitEthernet0/1/0, 100GE2/0/0.100, Eth-Trunk1…).
func vrpIfName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '/', r == '.', r == '-', r == ':', r == '_':
		default:
			return false
		}
	}
	return true
}
