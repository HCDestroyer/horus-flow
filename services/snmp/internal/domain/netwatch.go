package domain

import (
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Sondas de pérdida, latencia y jitter por enlace de subida. Fuente
// preferida: las sondas ICMP de `/tool netwatch` del propio router (RouterOS
// v7 publica pérdida, RTT y jitter de cada prueba), que Horus solo LEE por la
// API REST de solo lectura; el script lo pega el usuario. Respaldo: sondas
// ICMP desde el host de Horus a destinos configurados («ruta Horus →
// destino», nunca por enlace).

// NetwatchCommentPrefix marca las sondas de Horus en el router. Formato del
// comentario: `horus-uplink;if=<interfaz>;ref=gw` (puerta de enlace del
// proveedor por ese enlace) o `horus-uplink;ref=intl-<ip>` (referencia
// internacional desde el router, sin enlace concreto).
const NetwatchCommentPrefix = "horus-uplink"

// Fuentes de una sonda (ProbeSource del contrato).
const (
	ProbeSourceRouter = "router_netwatch"
	ProbeSourceHorus  = "horus"
)

// Referencias de una sonda.
const (
	ProbeRefGateway       = "gw"
	ProbeRefInternational = "intl"
)

// DefaultInternationalRefs son las referencias internacionales del script.
var DefaultInternationalRefs = []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8")}

// ProbeRef es lo que dice el comentario de una sonda.
type ProbeRef struct {
	Interface string // nombre saneado de la interfaz ("" = sonda del router)
	Ref       string // gw | intl
}

// ParseNetwatchComment interpreta el comentario de una sonda de netwatch
// (ok=false si no es de Horus).
func ParseNetwatchComment(c string) (ProbeRef, bool) {
	parts := strings.Split(strings.TrimSpace(c), ";")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) != NetwatchCommentPrefix {
		return ProbeRef{}, false
	}
	var r ProbeRef
	for _, p := range parts[1:] {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok {
			continue
		}
		switch k {
		case "if":
			r.Interface = v
		case "ref":
			if strings.HasPrefix(v, ProbeRefInternational) {
				r.Ref = ProbeRefInternational
			} else if v == ProbeRefGateway {
				r.Ref = ProbeRefGateway
			}
		}
	}
	if r.Ref == "" {
		r.Ref = ProbeRefGateway
		if r.Interface == "" {
			r.Ref = ProbeRefInternational
		}
	}
	return r, true
}

// SafeIfName sanea un nombre de interfaz para el comentario del script (y
// para emparejarlo después con la interfaz descubierta por SNMP).
func SafeIfName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_', r == '/':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// ProbeNamespace deriva probe_id = UUIDv5(router_id o tenant, host|ref|if).
var ProbeNamespace = uuid.MustParse("0192f2a0-7d3e-7f51-8a6c-4b2e1d9c0f17")

// ProbeID es el identificador estable de una sonda.
func ProbeID(owner uuid.UUID, host, ref, iface string) uuid.UUID {
	return uuid.NewSHA1(uuid.NewSHA1(ProbeNamespace, owner[:]), []byte(host+"|"+ref+"|"+iface))
}

// ProbeSample es una lectura de una sonda (snmp.probe_metrics).
type ProbeSample struct {
	TenantID    uuid.UUID
	SiteID      uuid.UUID
	RouterID    uuid.UUID // Nil en las sondas de Horus
	InterfaceID uuid.UUID // Nil si no mide un enlace concreto
	ProbeID     uuid.UUID
	Source      string
	Ref         string
	Target      string
	Label       string
	At          time.Time
	Up          *bool
	Sent        *int
	Received    *int
	LossPct     *float64
	RTTAvgMs    *float64
	RTTMinMs    *float64
	RTTMaxMs    *float64
	JitterMs    *float64
}

// SnippetUplink es un enlace del router para el script de netwatch.
type SnippetUplink struct {
	Name  string // nombre de la interfaz (SNMP ifName)
	Label string // nombre visible (configuración de capacidad)
}

// NetwatchPlaceholder es un valor que el usuario completa antes de pegar.
type NetwatchPlaceholder struct {
	Key         string `json:"key"`
	Interface   string `json:"interface"`
	Description string `json:"description"`
}

// NetwatchSnippet es el script de sondas para un router.
type NetwatchSnippet struct {
	Script       string                `json:"script"`
	Placeholders []NetwatchPlaceholder `json:"placeholders"`
	Warnings     []string              `json:"warnings"`
}

// BuildNetwatchSnippet genera las sondas netwatch que el USUARIO pega en el
// router (Horus nunca escribe en routers): una por enlace hacia la puerta de
// enlace del proveedor (valor a completar: {GATEWAY_n}) y una por referencia
// internacional, todas ICMP con 10 paquetes cada 30 s y comentario
// `horus-uplink;…`. Ningún up-script/down-script: solo medir.
func BuildNetwatchSnippet(routerName string, uplinks []SnippetUplink, refs []netip.Addr) NetwatchSnippet {
	out := NetwatchSnippet{Placeholders: []NetwatchPlaceholder{}, Warnings: []string{}}
	var b strings.Builder
	b.WriteString("# ===== Horus Flow · sondas de pérdida, latencia y jitter por enlace · RouterOS v7 =====\n")
	fmt.Fprintf(&b, "# Router: %s\n", safeComment(routerName))
	b.WriteString("# Revise el script antes de pegarlo. Horus nunca escribe en el router: estas sondas las crea usted y\n")
	b.WriteString("# Horus solo las LEE (usuario de solo lectura, API REST por el túnel). No ejecutan scripts.\n")
	b.WriteString("# Sustituya cada {GATEWAY_n} por la IP de la puerta de enlace del proveedor en ese enlace.\n")
	b.WriteString("# Deshacer: /tool netwatch remove [find comment~\"^" + NetwatchCommentPrefix + "\"]\n\n")
	if len(uplinks) == 0 {
		out.Warnings = append(out.Warnings, "El router no tiene enlaces de subida detectados ni configurados: solo se generan las referencias internacionales.")
	}
	for i, u := range uplinks {
		key := fmt.Sprintf("{GATEWAY_%d}", i+1)
		name := SafeIfName(u.Name)
		out.Placeholders = append(out.Placeholders, NetwatchPlaceholder{Key: key, Interface: u.Name,
			Description: "IP de la puerta de enlace del proveedor por " + u.Name + " (" + safeComment(u.Label) + ")"})
		fmt.Fprintf(&b, "# Enlace %s · %s\n", name, safeComment(u.Label))
		fmt.Fprintf(&b, "/tool netwatch add type=icmp host=%s interval=30s packet-count=10 packet-interval=50ms \\\n"+
			"    comment=\"%s;if=%s;ref=%s\"\n", key, NetwatchCommentPrefix, name, ProbeRefGateway)
	}
	if len(refs) > 0 {
		b.WriteString("\n# Referencias internacionales (miden la salida del router por su ruta por defecto, no un enlace concreto)\n")
	}
	for _, r := range refs {
		if !r.Is4() {
			continue
		}
		fmt.Fprintf(&b, "/tool netwatch add type=icmp host=%s interval=30s packet-count=10 packet-interval=50ms \\\n"+
			"    comment=\"%s;ref=%s-%s\"\n", r, NetwatchCommentPrefix, ProbeRefInternational, r)
	}
	out.Script = b.String()
	return out
}
