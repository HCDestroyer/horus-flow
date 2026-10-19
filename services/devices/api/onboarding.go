// Package api es el contrato público en proceso del módulo devices
// (ADR-0025 §1): lo que otros módulos pueden pedir a devices cuando está en el
// mismo proceso, registrado en module.Services.
//
// Onboarding lo usa wireguard (I1-01, I1-02): leer el router de un ISP,
// proyectar el estado del túnel en el router (equivale a consumir
// horus.wireguard.peer.*: devices actualiza tunnel_address, el peer y
// onboarding_state y publica horus.devices.router.updated con la IP de túnel,
// que es la identidad del exportador) y emitir las credenciales de solo
// lectura (SNMPv3 y API RouterOS) que el script muestra una sola vez y
// devices guarda cifradas. Entre procesos falta el RPC equivalente en el
// contrato C4 (pendiente; wireguard responde 503 si devices no es local).
package api

import (
	"context"
	"errors"
	"net/netip"

	"github.com/google/uuid"
)

// ServiceOnboarding → Onboarding.
const ServiceOnboarding = "devices.Onboarding"

// ServiceSNMPCredentials → SNMPCredentialReader (lo usa el rol snmp para
// sondear por SNMPv3; la implementación de devices es el mismo Onboarding).
const ServiceSNMPCredentials = "devices.SNMPCredentials"

// ErrRouterNotFound: el router no existe en el tenant (o está de baja).
var ErrRouterNotFound = errors.New("devices: router not found")

// ErrNoCredential: el router no tiene credencial de ese tipo (nunca se
// emitió o se borró).
var ErrNoCredential = errors.New("devices: credential not found")

// RouterInfo es lo que wireguard (y el rol snmp) necesitan de un router.
type RouterInfo struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	SiteID          uuid.UUID
	Name            string
	RouterOSVersion *string
	OnboardingState string
	TunnelAddress   *netip.Addr
	// Vendor: mikrotik | huawei.
	Vendor string
	// ConnectionMode: wireguard | ipsec | gre (túnel al hub de Horus; la IP
	// de túnel sale de la IPAM de wireguard) | direct (sin túnel; el camino
	// lo asegura el operador: docs/tunnels.md, docs/vendors/huawei.md §5).
	ConnectionMode string
	// ProtectionLevel: encrypted | unencrypted_private | unencrypted_internet.
	ProtectionLevel string
	// TunnelRemoteAddress: IP pública del router (ipsec opcional, gre obligatoria).
	TunnelRemoteAddress *netip.Addr
	// TunnelRemoteID: identidad IKE del router (ipsec): IP, FQDN,
	// usuario@FQDN o keyid:<id>.
	TunnelRemoteID string
	// ManagementAddress: en modo directo, la IP de origen del router tal y
	// como la ve Horus (destino del sondeo SNMP e identidad del exportador).
	ManagementAddress *netip.Addr
	// DeviceType: router | switch. Un switch es solo de monitoreo: nunca
	// exportador primario, nunca crea clientes ni entra en la atribución.
	DeviceType string
	// SwitchPlatform: routeros | swos | vrp (solo switches; swos = SNMP v2c).
	SwitchPlatform string
	// SFlowEnabled: el switch exporta sFlow (fuente secundaria).
	SFlowEnabled bool
	// ViaRouterID: con ConnectionMode via_router, el router del sitio (con
	// túnel) por el que Horus alcanza al equipo (ADR-0032).
	ViaRouterID *uuid.UUID
	// ViaAddress: dirección Horus del equipo vía router (IPAM de túneles; el
	// router hace NAT 1:1 con ManagementAddress). Destino del sondeo SNMP e
	// identidad del exportador sFlow; nunca la IP privada.
	ViaAddress *netip.Addr
}

// Tipos de equipo (devices.router.device_type) y plataformas de switch.
const (
	DeviceTypeRouter = "router"
	DeviceTypeSwitch = "switch"
	PlatformRouterOS = "routeros"
	PlatformSwOS     = "swos"
	PlatformVRP      = "vrp"
)

// Fabricantes (devices.router.vendor).
const (
	VendorMikroTik = "mikrotik"
	VendorHuawei   = "huawei"
)

// Modos de conexión con Horus (devices.router.connection_mode).
const (
	ConnectionWireGuard = "wireguard"
	ConnectionIPsec     = "ipsec"
	ConnectionGRE       = "gre"
	ConnectionDirect    = "direct"
	// ConnectionViaRouter: equipo de monitoreo (switch) con IP de gestión
	// privada alcanzado a través del túnel de un router del sitio, con NAT
	// 1:1 en ese router hacia su dirección Horus (ADR-0032). Sin túnel propio.
	ConnectionViaRouter = "via_router"
)

// Niveles de protección (devices.router.protection_level).
const (
	ProtectionEncrypted           = "encrypted"
	ProtectionUnencryptedPrivate  = "unencrypted_private"
	ProtectionUnencryptedInternet = "unencrypted_internet"
)

// HasTunnel indica si el modo usa un túnel con el hub (wireguard, ipsec o
// gre): la IP de túnel es la identidad del exportador.
func HasTunnel(mode string) bool {
	return mode == ConnectionWireGuard || mode == ConnectionIPsec || mode == ConnectionGRE
}

// ViaProjector proyecta en el equipo la dirección Horus que le asignó
// wireguard (equivale a consumir el cambio de la asignación). Solo la fija si
// el equipo sigue yendo vía ese router con esa IP privada; publica
// horus.devices.router.updated. Lo implementa el mismo Onboarding.
type ViaProjector interface {
	SetViaAddress(ctx context.Context, tenant, deviceID, routerID uuid.UUID, target, address netip.Addr) error
}

// ServiceTunnelSecrets → TunnelSecrets (lo usa wireguard para los túneles
// IPsec y para las credenciales SNMP de los routers con IPsec/GRE).
const ServiceTunnelSecrets = "devices.TunnelSecrets"

// TunnelSecrets emite y revela los secretos de los túneles IPsec/GRE: la PSK
// IKEv2 de cada router (kind ipsec_psk de devices.credential, cifrada con la
// KEK de devices; solo se muestra en claro al generarla) y la credencial
// SNMPv3 de los routers sin API REST (Huawei).
type TunnelSecrets interface {
	// IssueTunnelPSK genera una PSK nueva (invalida la anterior) y la
	// devuelve en claro una sola vez.
	IssueTunnelPSK(ctx context.Context, tenant, routerID uuid.UUID) (string, error)
	// RevealTunnelPSK descifra la PSK vigente para el agente del hub
	// (ErrNoCredential si nunca se emitió). Nunca se devuelve por HTTP.
	RevealTunnelPSK(ctx context.Context, tenant, routerID uuid.UUID) (string, error)
	// IssueSNMPCredentials genera solo la credencial SNMPv3 (SHA-256 +
	// AES-128) de un router sin API REST.
	IssueSNMPCredentials(ctx context.Context, tenant, routerID uuid.UUID) (OnboardingCredentials, error)
}

// Estados de alta (devices.router.onboarding_state).
const (
	OnboardingPending     = "pending_configuration"
	OnboardingKeyReceived = "key_received"
	OnboardingTunnelUp    = "tunnel_up"
	// OnboardingSNMPVerified (modo directo, sin túnel): el sondeador respondió
	// con la credencial SNMPv3 de ese router en su dirección de gestión.
	OnboardingSNMPVerified = "snmp_verified"
	OnboardingExporting    = "exporting"
)

// TunnelUpdate es la proyección de un peer en su router.
type TunnelUpdate struct {
	PeerID  uuid.UUID
	Address netip.Addr
	// OnboardingState nuevo; vacío = no cambia. Nunca retrocede de
	// exporting (lo decide flows).
	OnboardingState string
}

// OnboardingCredentials son las credenciales de solo lectura recién
// generadas (en claro solo en memoria, para el script).
type OnboardingCredentials struct {
	SNMPUser         string
	SNMPAuthPassword string
	SNMPPrivPassword string
	APIUser          string
	APIPassword      string
}

// Onboarding es el contrato.
type Onboarding interface {
	// GetRouter devuelve un router del tenant (ErrRouterNotFound si no).
	GetRouter(ctx context.Context, tenant, routerID uuid.UUID) (RouterInfo, error)
	// ListRouters devuelve todos los routers activos de la plataforma
	// (reconciliación de wireguard; multi-tenant declarado).
	ListRouters(ctx context.Context) ([]RouterInfo, error)
	// SetTunnel proyecta el túnel en el router y publica router.updated.
	SetTunnel(ctx context.Context, tenant, routerID uuid.UUID, u TunnelUpdate) error
	// IssueCredentials genera credenciales nuevas (invalida las anteriores),
	// las guarda cifradas y las devuelve en claro una sola vez.
	IssueCredentials(ctx context.Context, tenant, routerID uuid.UUID) (OnboardingCredentials, error)
}

// SNMPCredential es la credencial SNMP de solo lectura de un equipo, en claro
// solo en memoria (kind `snmp_v3` de devices.credential, descifrada con la KEK
// de devices). AuthProtocol: sha1 | sha256; PrivProtocol: aes128. Un switch
// SwOS (solo v1/v2c) tiene en su lugar una comunidad (kind `snmp_v2c`):
// Version = "2c" y Community; el resto vacío.
type SNMPCredential struct {
	// Version: "3" (por defecto si vacío) | "2c".
	Version      string
	Community    string
	Username     string
	AuthProtocol string
	AuthPassword string
	PrivProtocol string
	PrivPassword string
}

// SNMPCredentialReader revela la credencial SNMPv3 vigente de un router
// (I2, rol snmp). ErrRouterNotFound si el router no es del tenant;
// ErrNoCredential si no tiene credencial SNMP.
type SNMPCredentialReader interface {
	RevealSNMPCredential(ctx context.Context, tenant, routerID uuid.UUID) (SNMPCredential, error)
}

// ServiceDirectVerifier → DirectVerifier (lo usa el rol snmp).
const ServiceDirectVerifier = "devices.DirectVerifier"

// ErrManagementAddressTaken: otro router verificado ya usa esa dirección de
// gestión (la verificación no avanza).
var ErrManagementAddressTaken = errors.New("devices: management address taken")

// DirectVerifier marca como verificado un router en modo directo cuando el
// sondeador SNMP obtiene respuesta authPriv con SU credencial en addr (su
// dirección de gestión). Es la prueba de que quien registró la dirección
// controla el equipo: hasta entonces el colector no acepta flujos de esa IP
// (docs/vendors/huawei.md §5.3).
type DirectVerifier interface {
	VerifyDirect(ctx context.Context, tenant, routerID uuid.UUID, addr netip.Addr) error
}
