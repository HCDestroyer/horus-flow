package api

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ServiceRouterProbes → RouterProbeReader (lo usa el rol snmp para leer las
// sondas de pérdida, latencia y jitter que el ISP configuró en el router).
const ServiceRouterProbes = "devices.RouterProbes"

// ErrRouterAPIUnavailable: no se puede leer la API REST del router (sin
// túnel, sin credencial de la API, huella TLS aún no fijada, router que no
// responde o rechaza el usuario de solo lectura). Nunca se reintenta con
// otra credencial ni se acepta una huella nueva en segundo plano.
var ErrRouterAPIUnavailable = errors.New("devices: router API unavailable")

// NetwatchProbe es una sonda de `/tool netwatch` del router (RouterOS v7),
// leída con el usuario de solo lectura. Los campos de medida son de la última
// prueba completada; nil = el router no lo informa (tipo distinto de icmp,
// versión sin estadísticas o prueba aún sin completar).
type NetwatchProbe struct {
	Host          string
	Type          string // icmp | tcp-conn | http-get | simple
	Status        string // up | down | unknown
	Comment       string
	Disabled      bool
	SentCount     *int
	ResponseCount *int
	LossPercent   *float64
	RTTAvgMs      *float64
	RTTMinMs      *float64
	RTTMaxMs      *float64
	RTTJitterMs   *float64
	RTTStdevMs    *float64
}

// RouterProbeReader lee (solo GET, por el túnel) las sondas netwatch de un
// router del tenant. ErrRouterNotFound si el router no es del tenant;
// ErrRouterAPIUnavailable si no se puede leer.
type RouterProbeReader interface {
	ReadNetwatch(ctx context.Context, tenant, routerID uuid.UUID) ([]NetwatchProbe, error)
}
