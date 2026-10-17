package domain

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// Configuración VRP del alta en modo directo (Huawei, docs/vendors/huawei.md
// §5 y §6): la genera Horus y la pega el OPERADOR (Horus nunca escribe en el
// router). Sintaxis de VRP V8 (NE40E / NetEngine 8000) según la documentación
// oficial de Huawei; PENDIENTE DE VALIDACIÓN EN EQUIPO REAL. Solo comandos,
// sin líneas de comentario: al pegar en la CLI de VRP cada línea se ejecuta
// (las explicaciones van en Notes).

// Valores por defecto del alta directa.
const (
	VRPDefaultSamplingRate = 1000 // 1 de cada 1000 paquetes (NetStream por muestreo)
	VRPDefaultCollectorV9  = 2055
	VRPSNMPACL             = 2990 // ACL básica que limita SNMP al sondeador de Horus
	VRPSNMPGroup           = "horus-ro"
	VRPSNMPView            = "horus-ro"
	vrpMaxSampling         = 65535
)

// Marcadores que el operador completa antes de pegar.
const (
	VRPPlaceholderCollector = "{HORUS_COLLECTOR_IP}"
	VRPPlaceholderPoller    = "{HORUS_POLLER_IP}"
	VRPPlaceholderInterface = "{INTERFAZ_NETSTREAM}"
)

// VRPMIBViews son los subárboles que Horus lee (vista SNMP mínima, solo
// lectura): system, interfaces, ifMIB, entityMIB, DISMAN-PING-MIB,
// HUAWEI-ENTITY-EXTENT-MIB y NQA-MIB.
var VRPMIBViews = []string{
	"1.3.6.1.2.1.1", "1.3.6.1.2.1.2", "1.3.6.1.2.1.31", "1.3.6.1.2.1.47", "1.3.6.1.2.1.80",
	"1.3.6.1.4.1.2011.5.25.31", "1.3.6.1.4.1.2011.5.25.111",
}

// VRPValues son los valores del alta directa.
type VRPValues struct {
	RouterName        string
	ManagementAddress netip.Addr
	// CollectorAddress: IP pública (o del camino seguro) del colector de
	// Horus; inválida = marcador {HORUS_COLLECTOR_IP}.
	CollectorAddress netip.Addr
	CollectorPort    int
	// PollerAddress: IP de origen del sondeo SNMP de Horus (ACL); inválida =
	// marcador {HORUS_POLLER_IP}.
	PollerAddress netip.Addr
	SNMPUser      string
	SNMPAuthPass  string
	SNMPPrivPass  string
	SamplingRate  int
	// NTPServer: IP del servidor NTP (inválida = no se configura).
	NTPServer netip.Addr
}

// VRPPlaceholder es un valor a completar.
type VRPPlaceholder struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

// VRPConfig es el resultado.
type VRPConfig struct {
	Config       string           `json:"config"`
	Notes        []string         `json:"notes"`
	Placeholders []VRPPlaceholder `json:"placeholders"`
	Warnings     []string         `json:"warnings"`
}

var (
	vrpUserRe   = regexp.MustCompile(`^[A-Za-z0-9-]{1,32}$`)
	vrpSecretRe = regexp.MustCompile(`^[A-Za-z0-9]{8,64}$`)
	// ErrVRPValue: un valor no se puede insertar sin riesgo de romper la línea.
	ErrVRPValue = errors.New("devices: invalid VRP value")
)

// BuildVRPDirectConfig genera la configuración de alta directa: ACL de SNMP,
// SNMPv3 authPriv de solo lectura con vista mínima, NTP y NetStream (v9)
// hacia el colector de Horus con la dirección de gestión como origen. Todos
// los valores salen de tipos validados (IP, enteros) o de patrones estrictos.
func BuildVRPDirectConfig(v VRPValues) (VRPConfig, error) {
	out := VRPConfig{Notes: []string{}, Placeholders: []VRPPlaceholder{}, Warnings: []string{}}
	if !vrpUserRe.MatchString(v.SNMPUser) || !vrpSecretRe.MatchString(v.SNMPAuthPass) || !vrpSecretRe.MatchString(v.SNMPPrivPass) {
		return out, fmt.Errorf("%w: snmp credential", ErrVRPValue)
	}
	if !v.ManagementAddress.IsValid() {
		return out, fmt.Errorf("%w: management address", ErrVRPValue)
	}
	if v.SamplingRate <= 0 {
		v.SamplingRate = VRPDefaultSamplingRate
	}
	if v.SamplingRate > vrpMaxSampling {
		return out, fmt.Errorf("%w: sampling rate", ErrVRPValue)
	}
	if v.CollectorPort <= 0 || v.CollectorPort > 65535 {
		v.CollectorPort = VRPDefaultCollectorV9
	}
	collector := VRPPlaceholderCollector
	if v.CollectorAddress.IsValid() {
		collector = v.CollectorAddress.Unmap().String()
	} else {
		out.Placeholders = append(out.Placeholders, VRPPlaceholder{Key: VRPPlaceholderCollector,
			Description: "IP del colector de flujos de Horus por el camino que usted asegura (HORUS_DEVICES_DIRECT_COLLECTOR_ADDRESS no está configurada)."})
	}
	poller := VRPPlaceholderPoller
	if v.PollerAddress.IsValid() {
		poller = v.PollerAddress.Unmap().String()
	} else {
		out.Placeholders = append(out.Placeholders, VRPPlaceholder{Key: VRPPlaceholderPoller,
			Description: "IP de origen con la que Horus consulta SNMP (HORUS_DEVICES_DIRECT_POLLER_ADDRESS no está configurada)."})
	}
	out.Placeholders = append(out.Placeholders, VRPPlaceholder{Key: VRPPlaceholderInterface,
		Description: "Interfaz donde activar NetStream (subida al proveedor o lado clientes). Repita el bloque interface por cada una."})
	mgmt := v.ManagementAddress.Unmap()
	if !mgmt.Is4() || (v.CollectorAddress.IsValid() && !v.CollectorAddress.Unmap().Is4()) || (v.PollerAddress.IsValid() && !v.PollerAddress.Unmap().Is4()) {
		out.Warnings = append(out.Warnings, "La configuración generada usa comandos IPv4 (acl number, ip netstream): con direcciones IPv6 hay que "+
			"adaptarla (acl ipv6, ipv6 netstream).")
	}

	out.Notes = append(out.Notes,
		"PENDIENTE DE VALIDACIÓN EN EQUIPO REAL: sintaxis de VRP V8 (NE40E/NetEngine 8000) tomada de la documentación oficial de Huawei. En VRP V5 (AR) algunos comandos cambian (docs/vendors/huawei.md §6).",
		"Modo DIRECTO, sin túnel: SNMP y NetStream viajan por el camino que usted asegura (VRF de gestión, VPN de sitio existente o relé MikroTik/Linux en el sitio). Nunca exponga SNMP a Internet.",
		fmt.Sprintf("Horus solo acepta flujos con IP de origen %s y solo después de verificar la credencial SNMPv3 en esa dirección.", mgmt),
		"Las contraseñas SNMP aparecen una sola vez: tras «authentication-mode» y «privacy-mode» VRP pide la contraseña dos veces; las líneas siguientes son esas respuestas.",
		fmt.Sprintf("La ACL %d limita SNMP al sondeador de Horus; si ya usa ese número, cámbielo en las tres líneas que lo citan.", VRPSNMPACL),
		"NetStream en equipos distribuidos (NE40E/NetEngine 8000 con tarjetas) puede requerir además el muestreo por slot («ip netstream sampler to slot self» en la vista slot): consulte la guía de su modelo.",
	)

	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w("system-view")
	// 1) ACL básica: solo el sondeador de Horus consulta SNMP.
	w("acl number %d", VRPSNMPACL)
	w(" description horus-snmp-ro")
	w(" rule 5 permit source %s 0", poller)
	w(" rule 10 deny")
	w(" quit")
	// 2) SNMPv3 authPriv, solo lectura, vista mínima.
	w("snmp-agent")
	w("snmp-agent sys-info version v3")
	for _, oid := range VRPMIBViews {
		w("snmp-agent mib-view included %s %s", VRPSNMPView, oid)
	}
	w("snmp-agent group v3 %s privacy read-view %s acl %d", VRPSNMPGroup, VRPSNMPView, VRPSNMPACL)
	w("snmp-agent usm-user v3 %s group %s", v.SNMPUser, VRPSNMPGroup)
	w("snmp-agent usm-user v3 %s authentication-mode sha2-256", v.SNMPUser)
	w("%s", v.SNMPAuthPass)
	w("%s", v.SNMPAuthPass)
	w("snmp-agent usm-user v3 %s privacy-mode aes128", v.SNMPUser)
	w("%s", v.SNMPPrivPass)
	w("%s", v.SNMPPrivPass)
	w("snmp-agent usm-user v3 %s acl %d", v.SNMPUser, VRPSNMPACL)
	// 3) Reloj (los tiempos de NetStream son relativos al arranque del equipo).
	if v.NTPServer.IsValid() {
		w("ntp-service unicast-server %s", v.NTPServer.Unmap())
	} else {
		out.Warnings = append(out.Warnings, "Sin servidor NTP: configure «ntp-service unicast-server» en el router (los tiempos de los flujos dependen del reloj).")
	}
	// 4) NetStream v9 hacia Horus: plantillas cada minuto, opciones de muestreo, origen = dirección de gestión.
	w("ip netstream export version 9")
	w("ip netstream export template timeout-rate 1")
	w("ip netstream export template option sampler")
	w("ip netstream export source %s", mgmt)
	w("ip netstream export host %s %d", collector, v.CollectorPort)
	w("ip netstream timeout active 1")
	w("ip netstream timeout inactive 15")
	w("ip netstream sampler fix-packets %d inbound", v.SamplingRate)
	w("ip netstream sampler fix-packets %d outbound", v.SamplingRate)
	w("interface %s", VRPPlaceholderInterface)
	w(" ip netstream inbound")
	w(" ip netstream outbound")
	w(" quit")
	w("return")
	out.Config = b.String()
	return out, nil
}
