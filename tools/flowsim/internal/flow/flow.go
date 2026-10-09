// Package flow define el registro de flujo que maneja el simulador, los
// elementos de información (IE) de NetFlow v9/IPFIX que usa y las plantillas
// que imitan lo que exporta MikroTik RouterOS 7 (docs/vendors/mikrotik.md §2.3).
//
// Las plantillas IPFIX por defecto (perfil routeros7) reproducen campo a
// campo las de una captura real de RouterOS 7 (docs/traffic-model.md §4.4.2):
// ID 258 para IPv4 (37 campos) y 259 para IPv6 (34 campos). El perfil legacy
// conserva las plantillas supuestas de I0-10 (ID 256/257) y NetFlow v9 las
// sigue usando porque aún no hay captura real de v9.
package flow

import (
	"fmt"
	"net/netip"
	"strings"
	"time"
)

// Protocol es el formato de exportación.
type Protocol uint8

// Formatos soportados. NetFlow v5 queda fuera: no transporta IPv6
// (docs/traffic-model.md §3).
const (
	IPFIX Protocol = 10
	V9    Protocol = 9
)

// ParseProtocol interpreta "ipfix", "10", "v9" o "9".
func ParseProtocol(s string) (Protocol, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "ipfix", "10", "v10":
		return IPFIX, nil
	case "v9", "9", "netflow9", "nfv9":
		return V9, nil
	default:
		return 0, fmt.Errorf("protocolo desconocido %q (usa ipfix o v9)", s)
	}
}

// String devuelve el nombre corto usado en ficheros y flags.
func (p Protocol) String() string {
	switch p {
	case IPFIX:
		return "ipfix"
	case V9:
		return "v9"
	default:
		return fmt.Sprintf("proto(%d)", uint8(p))
	}
}

// DefaultPort es el puerto UDP estándar del colector para el protocolo.
func (p Protocol) DefaultPort() uint16 {
	if p == V9 {
		return 2055
	}
	return 4739
}

// Record es un flujo unidireccional tal como lo exporta el router.
type Record struct {
	Start, End       time.Time // primer y último paquete (resolución ms)
	SrcIP, DstIP     netip.Addr
	SrcPort, DstPort uint16
	Proto            uint8
	TCPFlags         uint8 // OR acumulado
	ToS              uint8
	Bytes, Packets   uint64
	InIf, OutIf      uint32
	NextHop          netip.Addr
	SrcMask, DstMask uint8
	ICMPTypeCode     uint16
	MinTTL, MaxTTL   uint8
	SrcMAC, DstMAC   [6]byte
	// MAC de origen y destino a la salida del router (IE 81/57). En RouterOS 7
	// postDestinationMacAddress repite destinationMacAddress (verificado).
	PostSrcMAC, PostDstMAC [6]byte
	FlowLabel              uint32 // solo IPv6

	// Campos post-NAT (IE 225-228). Con NAT en el router principal RouterOS
	// los rellena así (docs/traffic-model.md §4.4): subida con PostNATSrc =
	// IP pública del NAT; bajada con DstIP = IP pública del NAT y PostNATDst =
	// IP privada del cliente. Sin traducción repiten los valores previos; el
	// perfil routeros7 los copia si aquí quedan vacíos.
	PostNATSrc, PostNATDst         netip.Addr
	PostNATSrcPort, PostNATDstPort uint16
}

// IsV6 indica si el flujo es IPv6.
func (r *Record) IsV6() bool { return r.SrcIP.Is6() && !r.SrcIP.Is4In6() }

// Protocolos IP usados por el simulador.
const (
	ProtoICMP   uint8 = 1
	ProtoTCP    uint8 = 6
	ProtoUDP    uint8 = 17
	ProtoICMPv6 uint8 = 58
)

// Bits de TCP flags (IE 6).
const (
	FIN uint8 = 0x01
	SYN uint8 = 0x02
	RST uint8 = 0x04
	PSH uint8 = 0x08
	ACK uint8 = 0x10
)

// IDs de elementos de información (IANA IPFIX; coinciden con NetFlow v9 para
// todos los que usa el simulador).
const (
	IEOctetDeltaCount       uint16 = 1
	IEPacketDeltaCount      uint16 = 2
	IEProtocol              uint16 = 4
	IEToS                   uint16 = 5
	IETCPFlags              uint16 = 6
	IESrcPort               uint16 = 7
	IESrcIPv4               uint16 = 8
	IESrcMaskV4             uint16 = 9
	IEIngressIf             uint16 = 10
	IEDstPort               uint16 = 11
	IEDstIPv4               uint16 = 12
	IEDstMaskV4             uint16 = 13
	IEEgressIf              uint16 = 14
	IENextHopV4             uint16 = 15
	IESrcAS                 uint16 = 16
	IEDstAS                 uint16 = 17
	IEFlowEndSysUpTime      uint16 = 21
	IEFlowStartSysUpTime    uint16 = 22
	IESrcIPv6               uint16 = 27
	IEDstIPv6               uint16 = 28
	IESrcMaskV6             uint16 = 29
	IEDstMaskV6             uint16 = 30
	IEFlowLabelV6           uint16 = 31
	IEICMPTypeCodeV4        uint16 = 32
	IEMinTTL                uint16 = 52
	IEMaxTTL                uint16 = 53
	IESrcMAC                uint16 = 56
	IENextHopV6             uint16 = 62
	IEDstMAC                uint16 = 80
	IEICMPTypeCodeV6        uint16 = 139
	IEFlowStartMilliseconds uint16 = 152
	IEFlowEndMilliseconds   uint16 = 153
	IESysInitTimeMs         uint16 = 160
	IEPostNATSrcIPv4        uint16 = 225
	IEPostNATDstIPv4        uint16 = 226
	IEPostNAPTSrcPort       uint16 = 227
	IEPostNAPTDstPort       uint16 = 228

	// Campos que añade la plantilla real de RouterOS 7.
	IEIGMPType          uint16 = 33
	IEPostDstMAC        uint16 = 57
	IEIPVersion         uint16 = 60
	IEPostSrcMAC        uint16 = 81
	IEICMPTypeV4        uint16 = 176
	IEICMPCodeV4        uint16 = 177
	IEICMPTypeV6        uint16 = 178
	IEICMPCodeV6        uint16 = 179
	IETCPSeq            uint16 = 184
	IETCPAck            uint16 = 185
	IETCPWindow         uint16 = 186
	IEIPHeaderLength    uint16 = 189
	IEIPTTL             uint16 = 192
	IEUDPMessageLength  uint16 = 205
	IEIsMulticast       uint16 = 206
	IEIPTotalLength     uint16 = 224
)

// Field es un campo de plantilla.
type Field struct {
	ID  uint16
	Len uint16
}

// Template es una plantilla de datos.
type Template struct {
	ID     uint16
	Fields []Field
}

// RecordLen es la longitud en bytes de un registro de esta plantilla.
func (t Template) RecordLen() int {
	n := 0
	for _, f := range t.Fields {
		n += int(f.Len)
	}
	return n
}

// IDs de plantilla del perfil legacy (I0-10).
const (
	TemplateIDv4 uint16 = 256
	TemplateIDv6 uint16 = 257
)

// IDs de plantilla reales de RouterOS 7 (captura del 2026-10-09).
const (
	RouterOSTemplateIDv4 uint16 = 258
	RouterOSTemplateIDv6 uint16 = 259
)

// Profile elige el juego de plantillas IPFIX.
type Profile string

// Perfiles de plantilla.
const (
	// ProfileRouterOS7 reproduce las plantillas 258/259 de un RouterOS 7 real
	// con todos los campos activados, incluidos los NAT (defecto en IPFIX).
	ProfileRouterOS7 Profile = "routeros7"
	// ProfileLegacy son las plantillas supuestas de I0-10 (256/257), con los
	// campos NAT opcionales. NetFlow v9 siempre usa este perfil.
	ProfileLegacy Profile = "legacy"
)

// ParseProfile interpreta "routeros7" o "legacy" (vacío = routeros7).
func ParseProfile(s string) (Profile, error) {
	switch Profile(strings.ToLower(strings.TrimSpace(s))) {
	case "", ProfileRouterOS7:
		return ProfileRouterOS7, nil
	case ProfileLegacy:
		return ProfileLegacy, nil
	}
	return "", fmt.Errorf("perfil de plantillas desconocido %q (usa routeros7 o legacy)", s)
}

// TemplateOptions ajusta las plantillas.
type TemplateOptions struct {
	// Profile elige las plantillas IPFIX (vacío = routeros7).
	Profile Profile
	// NATFields añade IE 225-228 a la plantilla IPv4 del perfil legacy (el
	// perfil routeros7 siempre los lleva).
	NATFields bool
}

// EffectiveProfile devuelve el perfil que se usa realmente con el protocolo.
func (o TemplateOptions) EffectiveProfile(p Protocol) Profile {
	if p == IPFIX && o.Profile != ProfileLegacy {
		return ProfileRouterOS7
	}
	return ProfileLegacy
}

// HasNATFields indica si la plantilla IPv4 lleva los campos IE 225-228.
func (o TemplateOptions) HasNATFields(p Protocol) bool {
	return p == IPFIX && (o.NATFields || o.EffectiveProfile(p) == ProfileRouterOS7)
}

// routerOSCommon son los 17 primeros campos de las plantillas 258/259, en el
// orden y con las longitudes de la captura real.
var routerOSCommon = []Field{
	{IEIPVersion, 1},
	{IEFlowStartSysUpTime, 4},
	{IEFlowEndSysUpTime, 4},
	{IESysInitTimeMs, 8},
	{IEPacketDeltaCount, 8},
	{IEOctetDeltaCount, 8},
	{IESrcPort, 2},
	{IEDstPort, 2},
	{IEIngressIf, 4},
	{IEEgressIf, 4},
	{IEProtocol, 1},
	{IEToS, 1},
	{IETCPFlags, 1},
	{IEPostDstMAC, 6},
	{IEDstMAC, 6},
	{IEPostSrcMAC, 6},
	{IESrcMAC, 6},
}

// routerOSTail son los campos de IP, TTL y TCP que siguen a las direcciones.
var routerOSTail = []Field{
	{IEIPTTL, 1},
	{IEIsMulticast, 1},
	{IEIPHeaderLength, 1},
	{IEIPTotalLength, 8},
	{IEUDPMessageLength, 2},
	{IETCPSeq, 4},
	{IETCPAck, 4},
	{IETCPWindow, 2},
	{IEIGMPType, 1},
}

// RouterOSTemplates devuelve las plantillas reales de RouterOS 7: 258 (IPv4,
// 37 campos) y 259 (IPv6, 34 campos).
func RouterOSTemplates() (v4, v6 Template) {
	f4 := append([]Field{}, routerOSCommon...)
	f4 = append(f4,
		Field{IESrcIPv4, 4}, Field{IEDstIPv4, 4}, Field{IENextHopV4, 4},
		Field{IESrcMaskV4, 1}, Field{IEDstMaskV4, 1},
	)
	f4 = append(f4, routerOSTail...)
	f4 = append(f4,
		Field{IEICMPTypeV4, 1}, Field{IEICMPCodeV4, 1},
		Field{IEPostNATSrcIPv4, 4}, Field{IEPostNATDstIPv4, 4},
		Field{IEPostNAPTSrcPort, 2}, Field{IEPostNAPTDstPort, 2},
	)
	f6 := append([]Field{}, routerOSCommon...)
	f6 = append(f6,
		Field{IESrcIPv6, 16}, Field{IEDstIPv6, 16}, Field{IENextHopV6, 16},
		Field{IESrcMaskV6, 1}, Field{IEDstMaskV6, 1},
	)
	f6 = append(f6, routerOSTail...)
	f6 = append(f6, Field{IEICMPTypeV6, 1}, Field{IEICMPCodeV6, 1}, Field{IEFlowLabelV6, 4})
	return Template{ID: RouterOSTemplateIDv4, Fields: f4}, Template{ID: RouterOSTemplateIDv6, Fields: f6}
}

// Templates devuelve las plantillas IPv4 e IPv6 que el simulador anuncia
// para el protocolo dado.
//
// IPFIX con el perfil routeros7 (defecto): las plantillas reales 258/259
// (RouterOSTemplates). Con el perfil legacy: contadores de 8 bytes, tiempos
// first/last-forwarded relativos a sys-init-time (IE 22/21 + IE 160), MAC y
// TTL mínimo/máximo, y opcionalmente IE 225-228.
// NetFlow v9: contadores de 4 bytes, FIRST/LAST_SWITCHED relativos al
// sysUptime de la cabecera, sin MAC ni TTL (presencia "a verificar").
func Templates(p Protocol, opt TemplateOptions) (v4, v6 Template) {
	if opt.EffectiveProfile(p) == ProfileRouterOS7 {
		return RouterOSTemplates()
	}
	counter := uint16(4)
	if p == IPFIX {
		counter = 8
	}
	common := func(ipv6 bool) []Field {
		var fs []Field
		if p == IPFIX {
			fs = append(fs, Field{IESysInitTimeMs, 8})
		}
		fs = append(fs, Field{IEFlowStartSysUpTime, 4}, Field{IEFlowEndSysUpTime, 4})
		if ipv6 {
			fs = append(fs, Field{IESrcIPv6, 16}, Field{IEDstIPv6, 16})
		} else {
			fs = append(fs, Field{IESrcIPv4, 4}, Field{IEDstIPv4, 4})
		}
		fs = append(fs,
			Field{IESrcPort, 2},
			Field{IEDstPort, 2},
			Field{IEProtocol, 1},
			Field{IETCPFlags, 1},
			Field{IEToS, 1},
			Field{IEOctetDeltaCount, counter},
			Field{IEPacketDeltaCount, counter},
			Field{IEIngressIf, 4},
			Field{IEEgressIf, 4},
		)
		if ipv6 {
			fs = append(fs,
				Field{IENextHopV6, 16},
				Field{IESrcMaskV6, 1},
				Field{IEDstMaskV6, 1},
				Field{IEFlowLabelV6, 4},
				Field{IEICMPTypeCodeV6, 2},
			)
		} else {
			fs = append(fs,
				Field{IENextHopV4, 4},
				Field{IESrcMaskV4, 1},
				Field{IEDstMaskV4, 1},
				Field{IEICMPTypeCodeV4, 2},
			)
		}
		fs = append(fs, Field{IESrcAS, 4}, Field{IEDstAS, 4})
		if p == IPFIX {
			fs = append(fs,
				Field{IEMinTTL, 1},
				Field{IEMaxTTL, 1},
				Field{IESrcMAC, 6},
				Field{IEDstMAC, 6},
			)
			if opt.NATFields && !ipv6 {
				fs = append(fs,
					Field{IEPostNATSrcIPv4, 4},
					Field{IEPostNATDstIPv4, 4},
					Field{IEPostNAPTSrcPort, 2},
					Field{IEPostNAPTDstPort, 2},
				)
			}
		}
		return fs
	}
	return Template{ID: TemplateIDv4, Fields: common(false)},
		Template{ID: TemplateIDv6, Fields: common(true)}
}

// IsClientPrivate indica si una IPv4 es privada (RFC 1918) o CGNAT
// (100.64.0.0/10): las direcciones que se esperan con NAT en el router
// principal (D12).
func IsClientPrivate(a netip.Addr) bool {
	return a.IsPrivate() || cgnat.Contains(a)
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")
