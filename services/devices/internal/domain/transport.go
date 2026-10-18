package domain

import (
	"net/netip"
	"regexp"
	"slices"
	"strings"
)

// Transportes de conexión con Horus (devices.router.connection_mode;
// docs/tunnels.md, ADR-0031). wireguard e ipsec cifran; gre no cifra; direct
// no tiene túnel.
const (
	ConnectionIPsec = "ipsec"
	ConnectionGRE   = "gre"
)

// Niveles de protección (devices.router.protection_level).
const (
	ProtectionEncrypted           = "encrypted"
	ProtectionUnencryptedPrivate  = "unencrypted_private"
	ProtectionUnencryptedInternet = "unencrypted_internet"
)

// Camino declarado por el administrador para un transporte sin cifrado.
const (
	PathPrivate  = "private"
	PathInternet = "internet"
)

// Códigos de error del cambio de transporte.
const (
	// CodeUnencryptedConfirmation: elegir gre/direct por Internet exige
	// confirmación explícita (confirm_unencrypted_internet = true).
	CodeUnencryptedConfirmation = "UNENCRYPTED_TRANSPORT_CONFIRMATION_REQUIRED"
	// CodeTunnelRemoteInUse: otro router ya usa esa IP pública (gre) o esa
	// identidad IKE (ipsec) en la plataforma.
	CodeTunnelRemoteInUse = "TUNNEL_REMOTE_IN_USE"
	// CodeConnectionModeNotSupported: el fabricante no admite ese transporte.
	CodeConnectionModeNotSupported = "CONNECTION_MODE_NOT_SUPPORTED"
)

// ConnectionModes son todos los transportes.
var ConnectionModes = []string{ConnectionWireGuard, ConnectionIPsec, ConnectionGRE, ConnectionDirect}

// ConnectionModesFor son los transportes que admite un fabricante: MikroTik
// (WireGuard, IKEv2 y GRE; el modo directo no se ofrece porque el túnel
// WireGuard siempre está disponible) y Huawei VRP (IKEv2, GRE o directo; VRP
// no tiene WireGuard).
func ConnectionModesFor(vendor string) []string {
	if vendor == VendorHuawei {
		return []string{ConnectionIPsec, ConnectionGRE, ConnectionDirect}
	}
	return []string{ConnectionWireGuard, ConnectionIPsec, ConnectionGRE}
}

// SuggestedConnectionMode es el transporte recomendado por fabricante (el
// mejor estándar cifrado que admite): MikroTik → WireGuard; Huawei → IPsec
// IKEv2 (GRE si el equipo no tiene IPsec, p. ej. NE40E sin tarjeta de
// servicio).
func SuggestedConnectionMode(vendor string) string {
	if vendor == VendorHuawei {
		return ConnectionIPsec
	}
	return ConnectionWireGuard
}

// HasTunnel indica si el modo usa un túnel con el hub (IP de la IPAM de
// túneles = identidad del exportador).
func HasTunnel(mode string) bool {
	return mode == ConnectionWireGuard || mode == ConnectionIPsec || mode == ConnectionGRE
}

// Encrypted indica si el transporte cifra.
func Encrypted(mode string) bool { return mode == ConnectionWireGuard || mode == ConnectionIPsec }

// ProtectionFor es el nivel de protección de un modo y el camino declarado
// (solo cuenta para los transportes sin cifrado).
func ProtectionFor(mode, path string) string {
	switch {
	case Encrypted(mode):
		return ProtectionEncrypted
	case path == PathPrivate:
		return ProtectionUnencryptedPrivate
	}
	return ProtectionUnencryptedInternet
}

// ProtectionOf es el nivel guardado o, para filas anteriores al campo, el
// que corresponde a su modo (directo = camino privado: era el requisito del
// alta directa).
func ProtectionOf(mode, stored string) string {
	if stored != "" {
		return stored
	}
	if Encrypted(mode) {
		return ProtectionEncrypted
	}
	return ProtectionUnencryptedPrivate
}

// SupportsMode indica si el fabricante admite el modo.
func SupportsMode(vendor, mode string) bool { return slices.Contains(ConnectionModesFor(vendor), mode) }

// Identidades IKE: IP, FQDN, usuario@FQDN o keyid:<id> (RFC 7296 §3.5).
var (
	fqdnRe  = regexp.MustCompile(`^(?i)[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)
	keyIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

// Tipos de identidad IKE.
const (
	IKEIDAddress  = "ip"
	IKEIDFQDN     = "fqdn"
	IKEIDUserFQDN = "user_fqdn"
	IKEIDKeyID    = "key_id"
)

// ParseIKEID valida una identidad IKE y devuelve su tipo y su forma
// canónica: IPv4 literal (ip), nombre DNS (fqdn), usuario@nombre
// (user_fqdn) o keyid:<id> (key_id). Sin espacios, comillas ni caracteres
// que rompan una línea de configuración.
func ParseIKEID(s string) (kind, canonical string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 253 {
		return "", "", false
	}
	if a, err := netip.ParseAddr(s); err == nil {
		if !a.Is4() || !ValidManagementAddress(a) {
			return "", "", false
		}
		return IKEIDAddress, a.String(), true
	}
	if v, found := strings.CutPrefix(s, "keyid:"); found {
		if !keyIDRe.MatchString(v) {
			return "", "", false
		}
		return IKEIDKeyID, "keyid:" + v, true
	}
	if user, host, found := strings.Cut(s, "@"); found {
		if !keyIDRe.MatchString(user) || !fqdnRe.MatchString(host) || !strings.Contains(host, ".") {
			return "", "", false
		}
		return IKEIDUserFQDN, strings.ToLower(user + "@" + host), true
	}
	if !fqdnRe.MatchString(s) || !strings.ContainsAny(s, ".") {
		return "", "", false
	}
	return IKEIDFQDN, strings.ToLower(s), true
}

// ValidTunnelRemoteAddress acepta la IP pública del router para IKE/GRE:
// IPv4 unicast enrutable, nunca privada de Horus ni especial.
func ValidTunnelRemoteAddress(a netip.Addr) bool {
	a = a.Unmap()
	return a.Is4() && ValidManagementAddress(a)
}
