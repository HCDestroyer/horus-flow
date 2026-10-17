// Package consumption es el contrato en proceso con el que devices (consulta
// 360 por IP y consumo por lotes de las integraciones, ADR-0030) pide a FLOW
// el consumo de clientes sobre los agregados de ClickHouse
// (flows.customer_5m/1h/1d). Cada ventana es un cliente (realm + dirección
// canónica) en un intervalo [From, To): así se suman las IPs que tuvo un
// servicio de ArgosISP, cada una solo dentro de su vigencia.
//
// Quien llama ya comprobó el permiso (traffic.customer.read) y el tenant.
// Las direcciones son datos personales: no se registran.
package consumption

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// ServiceConsumption es el nombre en module.Services de la implementación (Reader).
const ServiceConsumption = "analytics.Consumption"

// ErrUnavailable: ClickHouse caído o sin configurar.
var ErrUnavailable = errors.New("consumption: analytics unavailable")

// MaxWindows acota una petición.
const MaxWindows = 5000

// Window es un cliente en un intervalo semiabierto [From, To). Precisión de
// los bordes: 1 h (rango ≤ 35 días) o 1 día; cada bucket cuenta para la
// ventana que contiene su inicio, así dos dueños consecutivos de una IP
// nunca suman el mismo bucket.
type Window struct {
	RealmID uuid.UUID
	Address netip.Addr
	From    time.Time
	To      time.Time
}

// Day es el consumo de un día UTC.
type Day struct {
	Day       time.Time
	UpBytes   uint64
	DownBytes uint64
}

// Totals son bytes de subida (upload + internal) y bajada.
type Totals struct {
	UpBytes   uint64
	DownBytes uint64
}

// Service es un servicio con sus bytes.
type Service struct {
	Name  string
	Bytes uint64
}

// Summary es el consumo reciente de un cliente para la consulta 360.
type Summary struct {
	Last24h, Last7d, Last30d Totals
	PeakDownBps24h           float64
	TopServices7d            []Service
}

// Reader lee consumo de clientes.
type Reader interface {
	// Daily devuelve, por ventana y en el mismo orden, el consumo por día UTC
	// (solo días con tráfico, ordenados).
	Daily(ctx context.Context, tenant uuid.UUID, windows []Window) ([][]Day, error)
	// Summary devuelve el consumo de las últimas 24 h, 7 y 30 días hasta at,
	// el pico de bajada de 24 h y los 5 servicios con más bytes en 7 días.
	// since (reinicio del cliente) recorta el inicio si no es nil.
	Summary(ctx context.Context, tenant, realm uuid.UUID, addr netip.Addr, at time.Time, since *time.Time) (*Summary, error)
}
