package api

import (
	"context"

	"github.com/google/uuid"
)

// ServiceInfraTargets → InfraTargets.
const ServiceInfraTargets = "snmp.InfraTargets"

// InfraTarget es un servicio de infraestructura que LibreNMS puede vigilar
// por ping (activo, con chequeo ICMP y sin sondeo a través de un router).
type InfraTarget struct {
	Name string
	IP   string
}

// InfraTargets da a alerts la lista de servicios para el grupo
// «Horus · Infraestructura» de LibreNMS.
type InfraTargets interface {
	LibreNMSTargets(ctx context.Context, tenant uuid.UUID) ([]InfraTarget, error)
}
