// Package engine es el motor de detección (I1-10, I1-11, I1-30): evalúa, por
// ISP y con sus parámetros, los detectores de traffic-model.md §8 sobre los
// agregados de ClickHouse (flows.client_security_1m/1h, client_port_1m,
// reputation_hit) y, para verificar candidatos, sobre flows_raw. Cada
// detector devuelve candidatos explicables (kind, severidad, confianza,
// razones con dato, evidencia agregada y objetivo principal) que el motor
// completa (cliente, router, muestreo, allowlist) y entrega a la capa de
// hallazgos.
//
// Tráfico `internal` (cliente → cliente del mismo nodo): NO entra en los
// detectores de I1. Los agregados de seguridad solo cuentan como "saliente"
// la dirección upload y todas las consultas de verificación filtran
// direction = 'upload'. La captura real del PO mostró un escaneo vertical
// entre dos hosts internos (285 puertos): es típico de un equipo de gestión
// o monitorización del ISP y, sin una lista de hosts de gestión, generaría
// hallazgos falsos contra la red propia. La propagación interna (gusano en
// el nodo, fila "Propagación interna" de §8) queda para un detector propio
// con allowlist de hosts de gestión (I2).
package engine

import (
	"context"
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// ClientKey identifica un cliente (D1, D22): realm + IP (IPv4 o dirección
// base del prefijo IPv6 delegado). IP siempre sin mapear (Unmap).
type ClientKey struct {
	Realm uuid.UUID
	IP    netip.Addr
}

// Having filtra candidatos de Security (condiciones en OR; 0 = no aplica).
type Having struct {
	MinRemoteIPs     uint64
	MinUpPackets     uint64
	MinUpBytes       uint64
	MinSMTPRemoteIPs uint64
}

// SecurityRow son los agregados de client_security_1m de un cliente en la ventana.
type SecurityRow struct {
	Key                       ClientKey
	Site                      uuid.UUID
	First, Last               time.Time
	UpBytes, DownBytes        uint64
	UpPackets, FlowsOut       uint64
	SynOnlyOut, SmallFlowsOut uint64
	RemoteIPs, Nets24         uint64
	RemotePorts, InboundPorts uint64
	SMTPFlows, SMTPRemoteIPs  uint64
	MinSampling, MaxSampling  uint32
}

// InitiatedStats son estadísticas de los flujos de subida iniciados por el
// cliente (heurística de iniciador: puerto local ≥ 1024 o remoto < 1024; así
// las respuestas de un servidor comercial no cuentan como fan-out).
type InitiatedStats struct {
	Key                      ClientKey
	TCP, SynOnly             uint64
	SynDests, SynNets24      uint64
	Dests, Nets24, ASNs      uint64
	Sample                   []string
	First, Last              time.Time
	MinSampling, MaxSampling uint32
}

// PortStat son los destinos de un cliente por puerto remoto.
type PortStat struct {
	Port         uint16
	Protocol     uint8
	Destinations uint64
	Flows        uint64
	SynOnly      uint64
}

// VerticalRow es un destino con muchos puertos distintos.
type VerticalRow struct {
	Key     ClientKey
	Remote  netip.Addr
	ASN     uint32
	Ports   uint64
	Flows   uint64
	Small   uint64
	SynOnly uint64
	MinPort uint16
	MaxPort uint16
	First   time.Time
	Last    time.Time
}

// WatchRow son los flujos de un cliente a puertos vigilados (client_port_1m).
type WatchRow struct {
	Key          ClientKey
	Destinations uint64
	Flows        uint64
	SynOnly      uint64
	Ports        []PortStat
}

// FlowRec es un flujo de un cliente (flows_raw) para repartir por minutos.
type FlowRec struct {
	Key        ClientKey
	Up         bool
	Remote     netip.Addr
	RemotePort uint16
	Protocol   uint8
	ASN        uint32
	Start, End time.Time
	Bytes      uint64
	Packets    uint64
	Sampling   uint32
}

// RepPair es un par cliente → IP remota con reputación (marca en ingesta) o
// la verificación de flows_raw para ese par.
type RepPair struct {
	Key        ClientKey
	Site       uuid.UUID
	Router     uuid.UUID
	Remote     netip.Addr
	RemotePort uint16
	Protocol   uint8
	ASN        uint32
	Category   string // enum reputation_category de flows_raw
	SourceID   uint16
	Confidence uint8
	Version    uint32
	Conns      uint64 // flujos de subida con SYN (o no TCP)
	SynOnly    uint64
	Responded  uint64 // flujos de bajada con datos (o ACK en TCP)
	Bytes      uint64
	Packets    uint64
	First      time.Time
	Last       time.Time
	Sampling   uint32
}

// BeaconRow es una serie de conexiones de un cliente a un mismo destino.
type BeaconRow struct {
	Key        ClientKey
	Site       uuid.UUID
	Router     uuid.UUID
	Remote     netip.Addr
	RemotePort uint16
	Protocol   uint8
	ASN        uint32
	Starts     []time.Time
	MeanBytes  float64
	Sampling   uint32
}

// Location es el nodo y el router principal por el que se ve a un cliente.
type Location struct {
	Site, Router uuid.UUID
}

// Customer es el cliente de dim.customer (o el derivado si devices aún no lo
// ha proyectado).
type Customer struct {
	ID    uuid.UUID
	Kind  string
	Alias *string
	Site  uuid.UUID
	Known bool
}

// Signals es la fuente de señales (adaptador ClickHouse). Toda consulta es
// de un tenant y respeta las row policies (SQL_horus_tenant).
type Signals interface {
	Security(ctx context.Context, tenant uuid.UUID, from, to time.Time, h Having) ([]SecurityRow, error)
	Initiated(ctx context.Context, tenant uuid.UUID, from, to time.Time, keys []ClientKey) (map[ClientKey]InitiatedStats, error)
	PortsByClient(ctx context.Context, tenant uuid.UUID, from, to time.Time, keys []ClientKey, synOnly bool, limit int) (map[ClientKey][]PortStat, error)
	Vertical(ctx context.Context, tenant uuid.UUID, from, to time.Time, minPorts int) ([]VerticalRow, error)
	WatchPorts(ctx context.Context, tenant uuid.UUID, from, to time.Time, minDest int, exclude []uint16) ([]WatchRow, error)
	DestinationsPerMinute(ctx context.Context, tenant uuid.UUID, from, to time.Time, keys []ClientKey) (map[ClientKey][]uint64, error)
	ClientFlows(ctx context.Context, tenant uuid.UUID, from, to time.Time, keys []ClientKey, minBytes uint64) ([]FlowRec, error)
	ReputationPairs(ctx context.Context, tenant uuid.UUID, from, to time.Time, categories []string) ([]RepPair, error)
	IndicatorFlows(ctx context.Context, tenant uuid.UUID, from, to time.Time, remotes []netip.Addr) ([]RepPair, error)
	BeaconSeries(ctx context.Context, tenant uuid.UUID, from, to time.Time, minConns int, minSpan time.Duration, maxMeanBytes float64, exclude []uint16) ([]BeaconRow, error)
	Locate(ctx context.Context, tenant uuid.UUID, from, to time.Time, keys []ClientKey) (map[ClientKey]Location, error)
	Customers(ctx context.Context, tenant uuid.UUID, keys []ClientKey) (map[ClientKey]Customer, error)
	Timezone(ctx context.Context, tenant uuid.UUID) (string, error)
}
