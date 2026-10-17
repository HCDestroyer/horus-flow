// Package customerfindings es el contrato en proceso con el que devices
// (consulta 360 por IP de las integraciones, ADR-0030) pide a SEC los
// hallazgos activos (open o acknowledged) de un cliente. La implementación
// aplica el principal del contexto (security.findings.read y sus alcances
// por nodo) como GET /customers/{id}/findings.
package customerfindings

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ServiceCustomerFindings es el nombre en module.Services de la implementación (Reader).
const ServiceCustomerFindings = "detection.CustomerFindings"

// Finding es un hallazgo activo, sin dirección del cliente.
type Finding struct {
	ID       uuid.UUID
	Kind     string
	Severity string
	State    string
	Summary  string
	OpenedAt time.Time
}

// Reader lee los hallazgos activos de un cliente con el principal de ctx
// (más graves y recientes primero). Devuelve el error de permiso del módulo
// si el principal no puede leer hallazgos.
type Reader interface {
	ActiveFindings(ctx context.Context, customerID uuid.UUID, limit int) ([]Finding, error)
}
