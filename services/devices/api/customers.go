package api

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ServiceCustomerDirectory → CustomerDirectory (lo usa alerts para el canal
// webhook: cliente e ids de ArgosISP de un hallazgo, ADR-0030).
const ServiceCustomerDirectory = "devices.CustomerDirectory"

// ErrCustomerNotFound: el cliente no existe en el tenant (o se purgó).
var ErrCustomerNotFound = errors.New("devices: customer not found")

// ExternalIDs son los ids del cliente en ArgosISP (identidad vigente).
// CompanyID es la empresa del servicio (ArgosISP multiempresa, contrato 1.2.0).
type ExternalIDs struct {
	System     string
	CustomerID *int64
	ServiceID  *int64
	CompanyID  *int64
}

// CustomerRef es lo que otro módulo necesita para nombrar a un cliente. Alias
// y Address son datos personales: quien llama decide si los envía.
type CustomerRef struct {
	ID       uuid.UUID
	SiteID   uuid.UUID
	Address  string
	Alias    *string
	External *ExternalIDs
}

// CustomerDirectory resuelve un cliente del tenant.
type CustomerDirectory interface {
	CustomerRef(ctx context.Context, tenant, customerID uuid.UUID) (*CustomerRef, error)
}
