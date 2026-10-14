// Package api es el contrato público en proceso del módulo alerts
// (ADR-0025 §1), registrado en module.Services.
//
// LibreNMSInfrastructure es el gancho para el inventario de servicios de
// infraestructura del ISP (DNS, RADIUS, OLT, etc.): mantiene el grupo
// «Horus · Infraestructura» de LibreNMS (vigilado por ping, regla
// «Horus · Servicio de infraestructura caído») con exactamente la lista
// recibida, en cada canal librenms ya aprovisionado del tenant. Los servicios
// que salen de la lista se quitan del grupo pero nunca se borran de LibreNMS.
package api

import (
	"context"

	"github.com/google/uuid"
)

// ServiceLibreNMSInfrastructure → LibreNMSInfrastructure.
const ServiceLibreNMSInfrastructure = "alerts.LibreNMSInfrastructure"

// InfraService es un servicio de infraestructura a vigilar (nombre único por
// tenant e IP alcanzable desde LibreNMS; nunca la IP de un cliente).
type InfraService struct {
	Name string
	IP   string
}

// InfraSyncStep resume lo que se hizo con un objeto de LibreNMS.
type InfraSyncStep struct {
	Kind     string
	Key      string
	Name     string
	Action   string // created | adopted | unchanged | recreated | removed | failed
	RemoteID int64
	Error    string
}

// LibreNMSInfrastructure sincroniza el grupo de infraestructura.
type LibreNMSInfrastructure interface {
	SyncInfrastructure(ctx context.Context, tenant uuid.UUID, services []InfraService) ([]InfraSyncStep, error)
}
