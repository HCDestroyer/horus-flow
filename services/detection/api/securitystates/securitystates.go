// Package securitystates es el contrato en proceso con el que devices
// (estado de seguridad por lotes de las integraciones, ADR-0030; contrato
// ArgosISP §3.5) pide a SEC el estado de seguridad actual de varios clientes
// a la vez. La implementación aplica el principal del contexto
// (security.findings.read y sus alcances por nodo).
package securitystates

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ServiceSecurityStates es el nombre en module.Services de la implementación (Reader).
const ServiceSecurityStates = "detection.SecurityStates"

// MaxCustomers es el máximo de clientes por llamada.
const MaxCustomers = 1000

// State es el estado de seguridad actual de un cliente (detection.customer_security).
type State struct {
	// State: clean, suspected, infected o mitigated.
	State        string
	OpenFindings int
	// MaxSeverity: severidad del hallazgo activo más grave (nil sin hallazgos activos).
	MaxSeverity *string
	// Since: desde cuándo el cliente está en State.
	Since time.Time
}

// Reader lee el estado de seguridad de los clientes dados (≤ MaxCustomers)
// con el principal de ctx. Los clientes sin estado guardado (nunca tuvieron
// un hallazgo) o fuera del alcance del principal no aparecen en el mapa.
// Devuelve el error de permiso del módulo si el principal no puede leer
// hallazgos.
type Reader interface {
	SecurityStates(ctx context.Context, customerIDs []uuid.UUID) (map[uuid.UUID]State, error)
}
