// Package lastflow es el contrato estrecho, en proceso, del último flujo
// recibido de cada exportador (router): lo usan la consulta 360 de
// integraciones (devices) y quien necesite «último flujo hace 12 s» sin
// depender del rol snmp.
//
// La fuente es el estado que el collector ya guarda por router en el bucket
// NATS KV flow_exporter_state (FlowExporter.last_flow_at): leerlo no toca la
// ruta de ingesta ni ClickHouse. Lo provee el rol ingester (que ya sirve
// GET /flow-exporters desde ese bucket).
package lastflow

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ServiceLastFlow → Reader en module.Services.
const ServiceLastFlow = "flows.LastFlow"

// MaxRouters es el máximo de routers por llamada.
const MaxRouters = 500

// Reader devuelve el último flujo de cada router pedido del tenant. Un
// router sin flujos todavía, sin estado o de otro tenant no aparece en el
// mapa (nunca un cero).
type Reader interface {
	LastFlowAt(ctx context.Context, tenant uuid.UUID, routers []uuid.UUID) (map[uuid.UUID]time.Time, error)
}

// Exporter es lo mínimo del estado del exportador que necesita FromStates.
type Exporter struct {
	TenantID   uuid.UUID
	LastFlowAt *time.Time
}

// StateFunc lee el estado de un router (nil, nil si no hay).
type StateFunc func(ctx context.Context, router uuid.UUID) (*Exporter, error)

// FromStates construye un Reader sobre una función de lectura por router;
// filtra por tenant y descarta lo que no tiene último flujo.
func FromStates(get StateFunc) Reader { return stateReader(get) }

type stateReader StateFunc

// LastFlowAt implementa Reader.
func (f stateReader) LastFlowAt(ctx context.Context, tenant uuid.UUID, routers []uuid.UUID) (map[uuid.UUID]time.Time, error) {
	out := make(map[uuid.UUID]time.Time, len(routers))
	if len(routers) > MaxRouters {
		routers = routers[:MaxRouters]
	}
	for _, id := range routers {
		if _, seen := out[id]; seen {
			continue
		}
		e, err := f(ctx, id)
		if err != nil {
			return nil, err
		}
		if e == nil || e.TenantID != tenant || e.LastFlowAt == nil || e.LastFlowAt.IsZero() {
			continue
		}
		out[id] = e.LastFlowAt.UTC()
	}
	return out, nil
}
