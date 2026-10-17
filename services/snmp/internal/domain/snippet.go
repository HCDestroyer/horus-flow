package domain

import (
	"fmt"
	"net/netip"
	"strings"
	"unicode"
)

// Snippet es el script RouterOS que el usuario pega en el router del nodo
// para que Horus alcance un servidor privado (Horus nunca escribe en
// routers) y lo que cambia del lado de Horus.
type Snippet struct {
	Applicable bool
	Script     string
	HorusSide  string
	Warnings   []string
}

// RouterInterfaceName es la interfaz WireGuard que crea el script de alta
// (services/wireguard/internal/script).
const RouterInterfaceName = "wg-horus"

// horusSide explica por qué no cambia nada en el hub: el tráfico de sondeo va
// a la /32 de túnel del router, que ya está en sus AllowedIPs; extender
// AllowedIPs con subredes privadas no es posible en un hub compartido entre
// ISPs (las redes privadas se solapan) y wg-agent lo rechaza a propósito.
const horusSide = "Del lado de Horus no hay que cambiar nada: los chequeos TCP/HTTP de este servicio van a la IP de " +
	"túnel del router (su /32, que ya está en las AllowedIPs de su peer en el hub WireGuard) en el puerto de reenvío " +
	"de cada chequeo, y el router los reenvía al servidor. Horus no amplía las AllowedIPs con subredes privadas del " +
	"ISP: el hub es compartido entre ISPs, sus redes privadas se solapan y wg-agent solo admite la /32 de cada router. " +
	"El ping (ICMP) a un servidor privado no se puede reenviar: use un chequeo TCP o HTTP."

// BuildSnippet genera las reglas de reenvío para los chequeos tcp/http con
// forward_port: dst-nat desde la IP de túnel del router hacia el servidor,
// masquerade de ese tráfico (para que la respuesta vuelva por el router) y
// aceptación en forward, todo limitado a la red de servicios de Horus y con
// comment="horus-infra". Todos los valores salen de tipos validados (IP,
// prefijo, enteros); el nombre se sanea antes de ir en un comentario.
func BuildSnippet(s *Service, routerName string, tunnelIP netip.Addr, servicesCIDR netip.Prefix) Snippet {
	out := Snippet{HorusSide: horusSide}
	if !IsPrivate(s.Target) {
		out.Warnings = append(out.Warnings, "El servidor tiene IP pública: Horus lo sondea directamente, no hace falta el script.")
	}
	if !s.Target.Is4() {
		out.Warnings = append(out.Warnings, "Solo se generan reglas para servidores IPv4.")
		return out
	}
	if !tunnelIP.Is4() {
		out.Warnings = append(out.Warnings, "El router todavía no tiene túnel con Horus: complete su alta primero.")
		return out
	}
	if !servicesCIDR.IsValid() || !servicesCIDR.Addr().Is4() {
		out.Warnings = append(out.Warnings, "Red de servicios de Horus desconocida (HORUS_WG_SERVICES_CIDR).")
		return out
	}
	var rules []string
	usedPorts := map[int]bool{}
	for _, c := range s.Checks {
		switch c.Type {
		case CheckICMP:
			out.Warnings = append(out.Warnings, "El chequeo ICMP no se puede reenviar por el router: quedará en «desconocido».")
			continue
		case CheckTCP, CheckHTTP:
		default:
			continue
		}
		if c.ForwardPort == nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("El chequeo %s al puerto %d no tiene puerto de reenvío: no se genera regla.", c.Type, c.EffectivePort()))
			continue
		}
		fp, port := *c.ForwardPort, c.EffectivePort()
		if usedPorts[fp] {
			out.Warnings = append(out.Warnings, fmt.Sprintf("El puerto de reenvío %d se repite: se usa una sola regla.", fp))
			continue
		}
		usedPorts[fp] = true
		rules = append(rules, fmt.Sprintf(`/ip firewall nat add chain=dstnat in-interface=%[1]s dst-address=%[2]s protocol=tcp dst-port=%[3]d \
    src-address=%[4]s action=dst-nat to-addresses=%[5]s to-ports=%[6]d comment="horus-infra %[7]s"
/ip firewall nat add chain=srcnat src-address=%[4]s dst-address=%[5]s protocol=tcp dst-port=%[6]d \
    action=masquerade comment="horus-infra %[7]s"
/ip firewall filter add chain=forward in-interface=%[1]s src-address=%[4]s dst-address=%[5]s protocol=tcp dst-port=%[6]d \
    action=accept comment="horus-infra %[7]s"`,
			RouterInterfaceName, tunnelIP, fp, servicesCIDR.Masked(), s.Target, port, shortID(s)))
	}
	if len(rules) == 0 {
		return out
	}
	out.Applicable = true
	var b strings.Builder
	fmt.Fprintf(&b, "# ===== Horus Flow · acceso de sondeo a un servicio de infraestructura · RouterOS v7 =====\n")
	fmt.Fprintf(&b, "# Servicio: %s · router: %s\n", safeComment(s.Name), safeComment(routerName))
	b.WriteString("# Revise el script antes de pegarlo en el router. Horus nunca escribe en el router: estas reglas las\n")
	b.WriteString("# aplica usted. Solo aceptan tráfico desde la red de servicios de Horus por la interfaz " + RouterInterfaceName + "\n")
	b.WriteString("# y se deshacen con: /ip firewall nat remove [find comment~\"^horus-infra " + shortID(s) + "\"]\n")
	b.WriteString("#                   /ip firewall filter remove [find comment~\"^horus-infra " + shortID(s) + "\"]\n")
	b.WriteString("# Si el router tiene reglas de forward propias, compruebe el orden: el bloque final sube las reglas de\n")
	b.WriteString("# Horus delante de la primera regla ajena.\n\n")
	b.WriteString(strings.Join(rules, "\n\n"))
	// Se mueve por id y no con place-before=0: validado en CHR 7.12, place-before=0 falla con "no such
	// item" si la lista está vacía y con "item referred by 'place-before' does not exist" si la
	// numeración del último print de la sesión quedó vieja.
	fmt.Fprintf(&b, `

# Colocar las reglas de forward de Horus antes de la primera regla ajena (por id)
{
    :local first [:pick [/ip firewall filter find where comment!="horus-infra %[1]s"] 0]
    :if ([:typeof $first] = "id") do={
        /ip firewall filter move [find where comment="horus-infra %[1]s"] destination=$first
    }
}
`, shortID(s))
	out.Script = b.String()
	return out
}

func shortID(s *Service) string { return s.ID.String()[:8] }

// safeComment deja solo caracteres inocuos para un comentario de script.
func safeComment(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ' ', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		case r > 0x7f && unicode.IsLetter(r):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
