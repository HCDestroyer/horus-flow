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

// ErrRouterNotFound: el router no existe en el tenant (o está de baja).
var ErrRouterNotFound = errors.New("devices: router not found")

// RouterInfo es lo que wireguard necesita de un router.
type RouterInfo struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	SiteID          uuid.UUID
	Name            string
	RouterOSVersion *string
	OnboardingState string
	TunnelAddress   *netip.Addr
}

// Estados de alta (devices.router.onboarding_state).
const (
	OnboardingPending     = "pending_configuration"
	OnboardingKeyReceived = "key_received"
	OnboardingTunnelUp    = "tunnel_up"
	OnboardingExporting   = "exporting"
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
