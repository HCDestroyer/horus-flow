package domain

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// Switches monitoreados (docs/vendors/mikrotik.md §13, docs/vendors/huawei.md
// §11). Un switch es una fila de devices.router con device_type = 'switch':
// reutiliza el estado, la credencial SNMP cifrada, el modo directo y la
// prueba de control (snmp_verified) de los routers, pero es SOLO de
// monitoreo: nunca es router principal, nunca es exportador primario, nunca
// crea clientes ni participa en la atribución. Siempre en modo directo por la
// red de gestión del ISP (camino privado declarado).

// Tipos de equipo (devices.router.device_type).
const (
	DeviceTypeRouter = "router"
	DeviceTypeSwitch = "switch"
)

// DeviceTypes son los tipos admitidos.
var DeviceTypes = []string{DeviceTypeRouter, DeviceTypeSwitch}

// Plataformas de switch (devices.router.switch_platform): el sistema del
// switch decide la versión de SNMP, la configuración generada y si hay sFlow.
const (
	// PlatformRouterOS: MikroTik CRS3xx/CRS5xx con RouterOS 7 (SNMPv3).
	PlatformRouterOS = "routeros"
	// PlatformSwOS: MikroTik CSS/CRS con SwOS (solo SNMP v1/v2c, sin LLDP).
	PlatformSwOS = "swos"
	// PlatformVRP: Huawei serie S (S5700, S6700…) con VRP (SNMPv3, LLDP, sFlow).
	PlatformVRP = "vrp"
)

// SwitchPlatforms son las plataformas admitidas.
var SwitchPlatforms = []string{PlatformRouterOS, PlatformSwOS, PlatformVRP}

// Códigos de error de los switches.
const (
	// CodeSwitchOnly: la operación solo existe para switches.
	CodeSwitchOnly = "DEVICE_NOT_A_SWITCH"
	// CodeSwitchNotSupported: la operación no existe para switches (túnel,
	// API REST de RouterOS, router principal…).
	CodeSwitchNotSupported = "SWITCH_NOT_SUPPORTED"
	// CodeSFlowNotSupported: la plataforma del switch no exporta sFlow.
	CodeSFlowNotSupported = "SFLOW_NOT_SUPPORTED"
)

// IsSwitch indica si el equipo es un switch (filas anteriores al campo: router).
func (r *Router) IsSwitch() bool { return r.DeviceType == DeviceTypeSwitch }

// VendorForPlatform es el fabricante de una plataforma de switch.
func VendorForPlatform(p string) string {
	if p == PlatformVRP {
		return VendorHuawei
	}
	return VendorMikroTik
}

// SNMPv2cOnly indica si la plataforma solo habla SNMP v1/v2c (SwOS): la
// credencial es una comunidad generada por Horus, en claro en la red.
func SNMPv2cOnly(p string) bool { return p == PlatformSwOS }

// SFlowSupported indica si la plataforma exporta sFlow v5: solo Huawei serie
// S. RouterOS no tiene sFlow (Traffic Flow exporta NetFlow/IPFIX y en CRS
// solo ve el tráfico que pasa por la CPU, no el conmutado por el chip) y SwOS
// no exporta flujos.
func SFlowSupported(p string) bool { return p == PlatformVRP }

// Valores del alta de switches.
const (
	SwitchDefaultSamplingRate = 4096 // 1 de cada 4096 paquetes por puerto (entrada)
	switchMinSampling         = 256
	switchMaxSampling         = 65535 // máximo del chip de muestreo de la serie S
	SwitchSNMPACL             = 2991  // ACL básica que limita SNMP al sondeador de Horus
	SwitchSFlowCollectorID    = 1
	// SwitchPlaceholderInterface: puertos donde activar sFlow (los monitoreados).
	SwitchPlaceholderInterface = "{PUERTO_SFLOW}"
	// SwitchPlaceholderMgmtNet: red de gestión (SwOS «Allow From»).
	SwitchPlaceholderMgmtNet = "{RED_DE_GESTION}"
)

// SwitchMIBViews son los subárboles que Horus lee de un switch Huawei serie S
// (vista SNMP mínima, solo lectura): system, interfaces, ifMIB, ENTITY-MIB,
// LLDP-MIB y HUAWEI-ENTITY-EXTENT-MIB.
var SwitchMIBViews = []string{
	"1.3.6.1.2.1.1", "1.3.6.1.2.1.2", "1.3.6.1.2.1.31", "1.3.6.1.2.1.47", "1.0.8802.1.1.2",
	"1.3.6.1.4.1.2011.5.25.31",
}

// SwitchValues son los valores del alta de un switch.
type SwitchValues struct {
	Name              string
	Platform          string
	ManagementAddress netip.Addr
	// PollerAddress: IP de origen del sondeo SNMP de Horus (inválida = marcador).
	PollerAddress netip.Addr
	// CollectorAddress/CollectorPort: colector de flujos (sFlow; inválida = marcador).
	CollectorAddress netip.Addr
	CollectorPort    int
	// SNMPv3 (routeros, vrp).
	SNMPUser     string
	SNMPAuthPass string
	SNMPPrivPass string
	// Community: SNMP v2c (swos).
	Community string
	// SFlow: añadir la configuración de sFlow (solo vrp).
	SFlow        bool
	SamplingRate int
	// Via: camino vía router del sitio (nil = modo directo por la red de
	// gestión). PollerAddress y CollectorAddress son entonces la IP de
	// servicios del hub y el colector, alcanzados por el túnel del router.
	Via *SwitchVia
}

// SwitchVia describe el camino de un switch vía router (ADR-0032).
type SwitchVia struct {
	RouterName string
	// Transport del túnel del router: wireguard | ipsec | gre.
	Transport string
	// HorusAddress: dirección Horus del switch (NAT 1:1 en el router).
	HorusAddress netip.Addr
}

// SwitchConfig es la configuración generada para pegar (o, en SwOS, los
// pasos de la interfaz web).
type SwitchConfig struct {
	// Language: routeros | vrp | swos_web.
	Language     string           `json:"language"`
	Config       string           `json:"config"`
	SFlowConfig  *string          `json:"sflow_config"`
	Notes        []string         `json:"notes"`
	Placeholders []VRPPlaceholder `json:"placeholders"`
	Warnings     []string         `json:"warnings"`
}

var (
	// La comunidad v2c la genera Horus (alfabeto sin ambigüedades).
	communityRe = regexp.MustCompile(`^[A-Za-z0-9]{16,64}$`)
	// ErrSwitchValue: un valor no se puede insertar sin riesgo.
	ErrSwitchValue = fmt.Errorf("devices: invalid switch value")
)

// ValidSwitchSampling indica si la tasa de muestreo sFlow es admisible.
func ValidSwitchSampling(n int) bool { return n >= switchMinSampling && n <= switchMaxSampling }

// BuildSwitchConfig genera el alta de un switch según su plataforma. Horus
// nunca escribe en el switch: lo pega (o lo configura en la web) el operador.
func BuildSwitchConfig(v SwitchValues) (SwitchConfig, error) {
	out := SwitchConfig{Notes: []string{}, Placeholders: []VRPPlaceholder{}, Warnings: []string{}}
	if !v.ManagementAddress.IsValid() {
		return out, fmt.Errorf("%w: management address", ErrSwitchValue)
	}
	if v.SamplingRate == 0 {
		v.SamplingRate = SwitchDefaultSamplingRate
	}
	if v.SFlow && (!SFlowSupported(v.Platform) || !ValidSwitchSampling(v.SamplingRate)) {
		return out, fmt.Errorf("%w: sflow", ErrSwitchValue)
	}
	poller := VRPPlaceholderPoller
	if v.PollerAddress.IsValid() {
		poller = v.PollerAddress.Unmap().String()
	} else {
		out.Placeholders = append(out.Placeholders, VRPPlaceholder{Key: VRPPlaceholderPoller,
			Description: "IP de origen con la que Horus consulta SNMP (HORUS_DEVICES_DIRECT_POLLER_ADDRESS no está configurada)."})
	}
	out.Notes = append(out.Notes,
		"Switch de MONITOREO: Horus solo lee (SNMP de solo lectura). Nunca escribe en el switch, nunca lo usa para crear clientes ni para atribuir tráfico.")
	if v.Via != nil {
		out.Notes = append(out.Notes,
			"Vía el router "+sanitizeComment(v.Via.RouterName)+" por su túnel "+v.Via.Transport+": Horus consulta la dirección Horus "+v.Via.HorusAddress.Unmap().String()+
				" y el router la traduce (NAT 1:1) a "+v.ManagementAddress.Unmap().String()+". Pegue también la configuración del router. El switch queda verificado cuando responde con ESTA credencial por ese camino.",
			"Las consultas SNMP llegan al switch desde "+poller+" (la red de servicios de Horus) y el switch debe poder responder a esa IP a través del router (su puerta de enlace de gestión o una ruta estática).")
	} else {
		out.Notes = append(out.Notes,
			"Modo directo por la red de gestión del ISP: Horus consulta "+v.ManagementAddress.Unmap().String()+" y solo marca el switch como verificado cuando responde con ESTA credencial en esa dirección.")
	}
	switch v.Platform {
	case PlatformRouterOS:
		if !vrpUserRe.MatchString(v.SNMPUser) || !vrpSecretRe.MatchString(v.SNMPAuthPass) || !vrpSecretRe.MatchString(v.SNMPPrivPass) {
			return out, fmt.Errorf("%w: snmp credential", ErrSwitchValue)
		}
		out.Language = "routeros"
		out.Config = routerOSSwitchConfig(v, poller)
		out.Notes = append(out.Notes,
			"RouterOS 7 (CRS3xx/CRS5xx): SNMPv3 authPriv (SHA1 + AES, los que admite /snmp community) de solo lectura, limitado a la IP del sondeador.",
			"Vecinos: Horus lee la tabla de vecinos de MIKROTIK-MIB (mtxrNeighbor) y, si el equipo la publica, LLDP-MIB. Active LLDP/MNDP en /ip neighbor discovery-settings si aún no lo está (decisión del ISP).",
			"sFlow no existe en RouterOS: Traffic Flow (NetFlow/IPFIX) en un CRS solo ve el tráfico que pasa por la CPU, no el conmutado por el chip; Horus no lo pide para switches.",
		)
	case PlatformSwOS:
		if !communityRe.MatchString(v.Community) {
			return out, fmt.Errorf("%w: community", ErrSwitchValue)
		}
		out.Language = "swos_web"
		out.Config = swOSSwitchSteps(v)
		out.Placeholders = append(out.Placeholders, VRPPlaceholder{Key: SwitchPlaceholderMgmtNet,
			Description: "Red de gestión del ISP desde la que se administra el switch; debe incluir la IP del sondeador de Horus (" + poller + ")."})
		out.Warnings = append(out.Warnings,
			"SwOS solo admite SNMP v1/v2c: la comunidad viaja EN CLARO y no hay cifrado ni autenticación fuerte. Úsela solo si el switch está en la red de gestión, aislada de clientes e Internet.",
			"SwOS no tiene LLDP-MIB ni CPU/memoria por SNMP: Horus muestra puertos, tráfico, errores y estado, y la salud (temperatura, voltaje) que publique MIKROTIK-MIB.",
		)
	case PlatformVRP:
		if !vrpUserRe.MatchString(v.SNMPUser) || !vrpSecretRe.MatchString(v.SNMPAuthPass) || !vrpSecretRe.MatchString(v.SNMPPrivPass) {
			return out, fmt.Errorf("%w: snmp credential", ErrSwitchValue)
		}
		out.Language = "vrp"
		out.Config = vrpSwitchConfig(v, poller)
		out.Notes = append(out.Notes,
			"PENDIENTE DE VALIDACIÓN EN EQUIPO REAL: sintaxis de VRP de la serie S (V200R0xx) tomada de la documentación oficial de Huawei (docs/vendors/huawei.md §11).",
			"Las contraseñas SNMP aparecen una sola vez: tras «authentication-mode» y «privacy-mode» VRP pide la contraseña dos veces; las líneas siguientes son esas respuestas. Versiones antiguas sin sha2-256: use «sha» y avise a soporte.",
			fmt.Sprintf("La ACL %d limita SNMP al sondeador de Horus; si ya usa ese número, cámbielo en las líneas que lo citan.", SwitchSNMPACL),
			"LLDP: «lldp enable» en la vista de sistema si no está activo; Horus lee LLDP-MIB (lldpRemTable) en solo lectura para mostrar los vecinos y sugerir los puertos de subida.",
		)
		if v.SFlow {
			s := vrpSwitchSFlow(&out, v)
			out.SFlowConfig = &s
		}
	default:
		return out, fmt.Errorf("%w: platform", ErrSwitchValue)
	}
	return out, nil
}

func routerOSSwitchConfig(v SwitchValues, poller string) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w("# Horus Flow · switch de monitoreo · RouterOS 7 · %s", sanitizeComment(v.Name))
	w("# SNMPv3 authPriv, solo lectura, solo desde el sondeador de Horus")
	w("/snmp community add name=%s security=private \\", v.SNMPUser)
	w("    authentication-protocol=SHA1 authentication-password=\"%s\" \\", v.SNMPAuthPass)
	w("    encryption-protocol=AES encryption-password=\"%s\" \\", v.SNMPPrivPass)
	w("    addresses=%s read-access=yes write-access=no comment=\"horus\"", poller)
	w("/snmp set enabled=yes")
	w("# Recomendado (decisión del ISP): deshabilitar la comunidad 'public' por defecto")
	w("# /snmp community set [find default=yes] disabled=yes")
	w("# Para retirar a Horus:")
	w("# /snmp community remove [find where comment=\"horus\"]")
	return b.String()
}

func swOSSwitchSteps(v SwitchValues) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w("SwOS (interfaz web del switch %s):", sanitizeComment(v.Name))
	w("1. Pestaña SNMP: Enabled = sí; Community = %s; Contact Info y Location a su criterio.", v.Community)
	w("2. Pestaña System: Allow From = %s (la red de gestión; debe incluir al sondeador de Horus).", SwitchPlaceholderMgmtNet)
	w("3. Pulse «Apply All».")
	w("4. Vuelva a Horus y pulse «Verificar»: el switch queda verificado cuando responde con esta comunidad en %s.", v.ManagementAddress.Unmap())
	return b.String()
}

func vrpSwitchConfig(v SwitchValues, poller string) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w("system-view")
	w("acl number %d", SwitchSNMPACL)
	w(" description horus-snmp-ro")
	w(" rule 5 permit source %s 0", poller)
	w(" rule 10 deny")
	w(" quit")
	w("snmp-agent")
	w("snmp-agent sys-info version v3")
	for _, oid := range SwitchMIBViews {
		w("snmp-agent mib-view included %s %s", VRPSNMPView, oid)
	}
	w("snmp-agent group v3 %s privacy read-view %s acl %d", VRPSNMPGroup, VRPSNMPView, SwitchSNMPACL)
	w("snmp-agent usm-user v3 %s group %s", v.SNMPUser, VRPSNMPGroup)
	w("snmp-agent usm-user v3 %s authentication-mode sha2-256", v.SNMPUser)
	w("%s", v.SNMPAuthPass)
	w("%s", v.SNMPAuthPass)
	w("snmp-agent usm-user v3 %s privacy-mode aes128", v.SNMPUser)
	w("%s", v.SNMPPrivPass)
	w("%s", v.SNMPPrivPass)
	w("snmp-agent usm-user v3 %s acl %d", v.SNMPUser, SwitchSNMPACL)
	w("return")
	return b.String()
}

// vrpSwitchSFlow genera sFlow v5 de la serie S: agente = dirección de gestión
// (identidad del exportador), colector de Horus y muestreo SOLO de entrada en
// cada puerto (cada paquete se muestrea una vez, en el puerto por el que
// entra: sin doble conteo dentro del switch). Sin muestreo de contadores: los
// contadores los lee Horus por SNMP.
func vrpSwitchSFlow(out *SwitchConfig, v SwitchValues) string {
	collector := VRPPlaceholderCollector
	if v.CollectorAddress.IsValid() {
		collector = v.CollectorAddress.Unmap().String()
	} else {
		out.Placeholders = append(out.Placeholders, VRPPlaceholder{Key: VRPPlaceholderCollector,
			Description: "IP del colector de flujos de Horus por la red de gestión (HORUS_DEVICES_DIRECT_COLLECTOR_ADDRESS no está configurada)."})
	}
	if v.Via != nil {
		out.Notes = append(out.Notes, "sFlow va al colector de Horus por el túnel del router: el router traduce el origen a la dirección Horus del switch, que es su identidad de exportador (nunca la IP privada).")
	}
	port := v.CollectorPort
	if port <= 0 || port > 65535 {
		port = VRPDefaultCollectorV9
	}
	out.Placeholders = append(out.Placeholders, VRPPlaceholder{Key: SwitchPlaceholderInterface,
		Description: "Puerto donde muestrear (uno de los monitoreados en Horus). Repita el bloque interface por cada uno; Huawei recomienda no más de 8 puertos con muestreo."})
	out.Notes = append(out.Notes,
		"sFlow del switch = fuente SECUNDARIA: Horus lo usa solo para el top de IP y protocolos por puerto en la ficha del switch. Nunca se suma al tráfico de clientes ni de nodos (que sale de los flujos del router) ni crea clientes.",
		fmt.Sprintf("Muestreo 1 de cada %d paquetes, solo de entrada en cada puerto: cada paquete se cuenta una vez, en el puerto por el que entra al switch.", v.SamplingRate),
		"El colector de Horus reconoce sFlow v5 en cualquiera de sus puertos UDP; 6343 es el estándar si su instalación lo escucha (HORUS_COLLECTOR_LISTEN).",
	)
	mgmt := v.ManagementAddress.Unmap()
	if !mgmt.Is4() {
		out.Warnings = append(out.Warnings, "sFlow se generó con «sflow agent ip» (IPv4): con gestión IPv6 use «sflow agent ipv6».")
	}
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w("system-view")
	w("sflow agent ip %s", mgmt)
	w("sflow collector %d ip %s udp-port %d description horus", SwitchSFlowCollectorID, collector, port)
	w("interface %s", SwitchPlaceholderInterface)
	w(" sflow flow-sampling collector %d", SwitchSFlowCollectorID)
	w(" sflow flow-sampling rate %d", v.SamplingRate)
	w(" sflow flow-sampling inbound")
	w(" quit")
	w("return")
	return b.String()
}

var commentRe = regexp.MustCompile(`[^A-Za-z0-9 ._-]`)

// sanitizeComment limpia un nombre para insertarlo en un comentario.
func sanitizeComment(s string) string {
	s = commentRe.ReplaceAllString(s, "")
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}
