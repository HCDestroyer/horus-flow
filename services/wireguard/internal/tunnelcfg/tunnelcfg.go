// Package tunnelcfg genera la configuración que el ADMINISTRADOR pega en el
// router para los túneles IPsec IKEv2 (route-based) y GRE hacia el hub de
// Horus (docs/tunnels.md §5). Horus nunca escribe en el router. Plataformas:
// MikroTik RouterOS 7, Huawei VRP (V8; V5 con diferencias anotadas) y un
// ejemplo genérico Linux/strongSwan (swanctl + iproute2) que sirve de
// referencia para otros fabricantes. Todos los valores salen de tipos
// validados (IP, enteros) o de patrones estrictos: ninguno puede cerrar una
// cadena ni inyectar comandos.
package tunnelcfg

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// Transportes.
const (
	TransportIPsec = "ipsec"
	TransportGRE   = "gre"
)

// Plataformas de la configuración generada.
const (
	PlatformRouterOS   = "routeros"
	PlatformVRP        = "vrp"
	PlatformStrongSwan = "strongswan"
)

// Marcadores que completa el administrador.
const (
	PlaceholderHubAddress    = "{HORUS_HUB_PUBLIC_IP}"
	PlaceholderRouterAddress = "{IP_PUBLICA_DEL_ROUTER}"
	PlaceholderWANInterface  = "{INTERFAZ_WAN}"
	PlaceholderNetStream     = "{INTERFAZ_NETSTREAM}"
)

// Valores por defecto.
const (
	DefaultMTU          = 1400
	DefaultSamplingRate = 1000
	IPFIXPort           = 4739
	NetStreamPort       = 2055
	vrpTunnelNumber     = 10
	vrpLoopbackNumber   = 10
	vrpACL              = 2990
)

// Propuestas que acepta el hub (deben coincidir con tunnel-agent,
// adapters/vici): AES-256-GCM/PRF SHA-384/ECP-384, AES-256/SHA-256/ECP-384 y
// AES-256/SHA-256/MODP-2048 (grupo 14, VRP antiguos).
var (
	HubIKEProposals = []string{"aes256gcm16-prfsha384-ecp384", "aes256-sha256-ecp384", "aes256-sha256-modp2048"}
	HubESPProposals = []string{"aes256gcm16-ecp384", "aes256-sha256-ecp384", "aes256-sha256-modp2048"}
)

// Values son los valores de la configuración.
type Values struct {
	Transport  string
	Vendor     string // mikrotik | huawei
	RouterID   string
	RouterName string
	// TunnelAddress es la /32 del router (identidad del exportador).
	TunnelAddress netip.Addr
	ServicesCIDR  netip.Prefix
	// CollectorIP es la IP de servicios del hub: destino de IPFIX/NetStream
	// y origen del sondeo SNMP por el túnel.
	CollectorIP netip.Addr
	// HubAddress: IPv4 pública del hub (inválida = marcador).
	HubAddress netip.Addr
	// HubID: identidad IKE del hub (IP o FQDN).
	HubID string
	// RemoteAddress: IPv4 pública del router (gre obligatoria; ipsec opcional).
	RemoteAddress netip.Addr
	// RemoteID: identidad IKE del router (ip, fqdn, user@fqdn, keyid:<id>).
	RemoteID string
	PSK      string
	GREKey   uint32
	MTU      int
	// Credenciales de solo lectura (en claro solo en esta respuesta).
	SNMPUser     string
	SNMPAuthPass string
	SNMPPrivPass string
	APIUser      string
	APIPassword  string
	NTPServer    string
	SamplingRate int
}

// Placeholder es un valor a completar.
type Placeholder struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

// Config es una configuración para pegar.
type Config struct {
	Platform     string        `json:"platform"`
	Language     string        `json:"language"`
	Title        string        `json:"title"`
	Config       string        `json:"config"`
	Notes        []string      `json:"notes"`
	Placeholders []Placeholder `json:"placeholders"`
	Warnings     []string      `json:"warnings"`
}

// ErrValue: un valor no se puede insertar sin riesgo de romper la línea.
var ErrValue = errors.New("tunnelcfg: invalid value")

var (
	userRe   = regexp.MustCompile(`^[A-Za-z0-9-]{1,32}$`)
	secretRe = regexp.MustCompile(`^[A-Za-z0-9]{8,64}$`)
	pskRe    = regexp.MustCompile(`^[A-Za-z0-9]{32,128}$`)
	nameRe   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	hostRe   = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)
	idRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@:-]{0,252}$`)
)

// IDKind clasifica una identidad IKE canónica: ip | fqdn | user_fqdn | key_id.
func IDKind(id string) string {
	if a, err := netip.ParseAddr(id); err == nil && a.Is4() {
		return "ip"
	}
	if strings.HasPrefix(id, "keyid:") {
		return "key_id"
	}
	if strings.Contains(id, "@") {
		return "user_fqdn"
	}
	return "fqdn"
}

func idValue(id string) string { return strings.TrimPrefix(id, "keyid:") }

// StrongSwanID expresa una identidad para swanctl/vici sin ambigüedad.
func StrongSwanID(id string) string {
	switch IDKind(id) {
	case "ip":
		return id
	case "key_id":
		return "keyid:" + idValue(id)
	case "user_fqdn":
		return "email:" + id
	}
	return "fqdn:" + id
}

// routerOSID: address:/fqdn:/user-fqdn:/key-id: de /ip ipsec identity.
func routerOSID(id string) string {
	switch IDKind(id) {
	case "ip":
		return "address:" + id
	case "key_id":
		return "key-id:" + idValue(id)
	case "user_fqdn":
		return "user-fqdn:" + id
	}
	return "fqdn:" + id
}

// vrpIDType: tipo de identidad de «ike peer» en VRP.
func vrpIDType(id string) string {
	switch IDKind(id) {
	case "ip":
		return "ip"
	case "key_id":
		return "key-id"
	case "user_fqdn":
		return "user-fqdn"
	}
	return "fqdn"
}

func (v *Values) validate() error {
	if v.Transport != TransportIPsec && v.Transport != TransportGRE {
		return fmt.Errorf("%w: transport", ErrValue)
	}
	if !v.TunnelAddress.Is4() || !v.CollectorIP.Is4() || !v.ServicesCIDR.IsValid() || !v.ServicesCIDR.Contains(v.CollectorIP) {
		return fmt.Errorf("%w: tunnel addresses", ErrValue)
	}
	if v.HubAddress.IsValid() && !v.HubAddress.Is4() || v.RemoteAddress.IsValid() && !v.RemoteAddress.Is4() {
		return fmt.Errorf("%w: public addresses", ErrValue)
	}
	if !nameRe.MatchString(v.RouterName) {
		v.RouterName = "router"
	}
	if !userRe.MatchString(v.SNMPUser) || !secretRe.MatchString(v.SNMPAuthPass) || !secretRe.MatchString(v.SNMPPrivPass) {
		return fmt.Errorf("%w: snmp credential", ErrValue)
	}
	if v.APIUser != "" && (!userRe.MatchString(v.APIUser) || !secretRe.MatchString(v.APIPassword)) {
		return fmt.Errorf("%w: api credential", ErrValue)
	}
	if v.NTPServer != "" && !hostRe.MatchString(v.NTPServer) {
		return fmt.Errorf("%w: ntp server", ErrValue)
	}
	if v.Transport == TransportIPsec {
		if !pskRe.MatchString(v.PSK) || !idRe.MatchString(v.HubID) || !idRe.MatchString(v.RemoteID) {
			return fmt.Errorf("%w: ipsec identities or psk", ErrValue)
		}
	}
	if v.Transport == TransportGRE && !v.RemoteAddress.IsValid() {
		return fmt.Errorf("%w: gre needs the router public address", ErrValue)
	}
	if v.MTU == 0 {
		v.MTU = DefaultMTU
	}
	if v.MTU < 1280 || v.MTU > 1500 {
		return fmt.Errorf("%w: mtu", ErrValue)
	}
	if v.SamplingRate <= 0 {
		v.SamplingRate = DefaultSamplingRate
	}
	if v.SamplingRate > 65535 {
		return fmt.Errorf("%w: sampling rate", ErrValue)
	}
	return nil
}

// MSS es el MSS de TCP que cabe en el túnel (MTU − 40 de IPv4+TCP).
func MSS(mtu int) int { return mtu - 40 }

type builder struct {
	strings.Builder
}

func (b *builder) w(format string, a ...any) { fmt.Fprintf(&b.Builder, format+"\n", a...) }

func hubOr(v Values, c *Config) string {
	if v.HubAddress.IsValid() {
		return v.HubAddress.String()
	}
	if !hasPlaceholder(c, PlaceholderHubAddress) {
		c.Placeholders = append(c.Placeholders, Placeholder{Key: PlaceholderHubAddress,
			Description: "IPv4 pública del servidor Horus (HORUS_TUNNEL_HUB_ADDRESS no está configurada en el servidor)."})
	}
	return PlaceholderHubAddress
}

func remoteOr(v Values, c *Config) string {
	if v.RemoteAddress.IsValid() {
		return v.RemoteAddress.String()
	}
	if !hasPlaceholder(c, PlaceholderRouterAddress) {
		c.Placeholders = append(c.Placeholders, Placeholder{Key: PlaceholderRouterAddress,
			Description: "IPv4 de la WAN del router (con IP dinámica o tras NAT, la que tenga la interfaz)."})
	}
	return PlaceholderRouterAddress
}

func hasPlaceholder(c *Config, k string) bool {
	for _, p := range c.Placeholders {
		if p.Key == k {
			return true
		}
	}
	return false
}

func newConfig(platform, language, title string) Config {
	return Config{Platform: platform, Language: language, Title: title, Notes: []string{}, Placeholders: []Placeholder{}, Warnings: []string{}}
}

// Build genera las configuraciones: primero la del fabricante del router y
// después el ejemplo genérico Linux/strongSwan.
func Build(v Values) ([]Config, error) {
	if err := v.validate(); err != nil {
		return nil, err
	}
	var out []Config
	switch v.Vendor {
	case "huawei":
		out = append(out, vrp(v))
	default:
		out = append(out, routerOS(v))
	}
	out = append(out, strongSwan(v))
	return out, nil
}

func commonNotes(v Values) []string {
	n := []string{
		fmt.Sprintf("IP de túnel del router: %s/32 (identidad del exportador: Horus solo atribuye flujos y SNMP que lleguen por ESTE túnel con esa IP de origen).", v.TunnelAddress),
		fmt.Sprintf("MTU del túnel %d y MSS de TCP %d: ajústelos si su enlace ya tiene cabeceras (PPPoE, MPLS).", v.MTU, MSS(v.MTU)),
	}
	if v.Transport == TransportGRE {
		n = append(n, "GRE NO CIFRA: SNMP y flujos viajan en claro. Úselo solo por un camino privado o con la confirmación de riesgo registrada en Horus.",
			"Mantenimiento de vida (keepalive) GRE desactivado: el hub Linux no responde los keepalive de GRE; Horus detecta el túnel caído por el último paquete recibido (180 s).")
	} else {
		n = append(n, "La clave precompartida (PSK) aparece UNA SOLA VEZ: guárdela en un lugar seguro o borre esta configuración tras pegarla. Generar otra configuración la sustituye.")
	}
	return n
}
