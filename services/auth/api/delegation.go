package api

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ServiceDelegation → Delegator.
const ServiceDelegation = "auth.Delegation"

// ErrDelegationDenied indica que no se puede actuar en nombre del usuario:
// usuario inactivo, membresía o ISP no activos, o ningún permiso permitido.
var ErrDelegationDenied = errors.New("auth: delegation denied")

// DelegationRequest pide un access token de corta vida para actuar en nombre
// de un usuario en un ISP con un subconjunto de sus permisos (el servidor MCP
// del asistente de IA, docs/assist.md).
type DelegationRequest struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	// Permissions es la lista blanca: el token lleva la intersección con los
	// permisos efectivos ACTUALES del usuario en el ISP (con sus alcances).
	Permissions []string
	// Purpose identifica al solicitante (p. ej. "assist-mcp"); va en el jti.
	Purpose string
}

// Delegator emite tokens delegados (typ = service, scope = tenant, sin
// sesión): auth comprueba en cada emisión que el usuario, la membresía y el
// ISP siguen activos. Su vida es corta (2 min) y solo se usan en proceso.
type Delegator interface {
	DelegatedToken(ctx context.Context, req DelegationRequest) (string, error)
}
