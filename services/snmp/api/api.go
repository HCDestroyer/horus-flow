// Package api es el contrato público del rol snmp dentro del proceso
// (module.Services): el estado observado y la última muestra de cada router,
// que analytics usa para el widget routers_status_grid y el gateway para los
// temas WebSocket. La forma coincide con packages/schemas/openapi/v0/snmp.yaml
// (RouterStatus, RouterLiveMetrics) y con los eventos de events.md §8.4.
package api

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ServiceRouterStatus → RouterStatusReader.
const ServiceRouterStatus = "snmp.RouterStatus"

// Estados efectivos (RouterEffectiveStatus del contrato).
const (
	StatusOnline      = "online"
	StatusWarning     = "warning"
	StatusCritical    = "critical"
	StatusDegraded    = "degraded"
	StatusOffline     = "offline"
	StatusStale       = "stale"
	StatusUnknown     = "unknown"
	StatusMaintenance = "maintenance"
	// StatusUnreachable: un switch vía router no se alcanza porque el túnel
	// de su router del sitio está caído (razón via_router_tunnel_down,
	// ADR-0032): no es un fallo del switch.
	StatusUnreachable = "unreachable"
)

// Razones del estado observado (enum abierto RouterStateReason).
const (
	ReasonOK              = "ok"
	ReasonCPUHigh         = "cpu_high"
	ReasonMemoryHigh      = "memory_high"
	ReasonTemperatureHigh = "temperature_high"
	ReasonSNMPUnreachable = "snmp_unreachable"
	ReasonSNMPAuthFailed  = "snmp_auth_failed"
	ReasonTunnelDown      = "tunnel_down"
	ReasonHostUnreachable = "host_unreachable_via_tunnel"
	ReasonExporterSilent  = "exporter_silent"
	ReasonInterfaceDown   = "interface_down"
	// ReasonInterfaceFlapping: un puerto de subida de un switch cambió de
	// estado operativo FlapThreshold veces o más en la última hora.
	ReasonInterfaceFlapping    = "interface_flapping"
	ReasonPSUFailed            = "psu_failed"
	ReasonFanFailed            = "fan_failed"
	ReasonNoCredentials        = "no_snmp_credentials"
	ReasonNoTunnel             = "no_tunnel"
	ReasonViaRouterTunnelDown  = "via_router_tunnel_down"
	StatusOrderProblemsFirst   = "status" // orden por defecto de la lista
	StatusOrderByName          = "name"
	StatusOrderByCPU           = "cpu"
	StatusOrderByTraffic       = "traffic"
	DefaultIntervalSeconds     = 60
	DefaultHealthInterval      = 300
	DefaultICMPIntervalSeconds = 30
)

// Signals son las señales que sostienen el estado.
type Signals struct {
	ICMP      string  `json:"icmp"`      // ok | fail | unknown
	SNMP      string  `json:"snmp"`      // ok | fail | auth_failed | unknown
	WireGuard string  `json:"wireguard"` // ok | stale | never | unknown
	Exporter  *string `json:"exporter"`  // FlowExporterState o nil
}

// Sensor es un sensor del fabricante (mtxrGaugeTable).
type Sensor struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Unit  *string `json:"unit"`
}

// DeviceSample es la última muestra del dispositivo (RouterDeviceSample).
type DeviceSample struct {
	SampledAt             *time.Time `json:"sampled_at"`
	PollDurationMs        *int       `json:"poll_duration_ms"`
	UptimeSeconds         *int64     `json:"uptime_seconds"`
	CPUPercent            *float64   `json:"cpu_percent"`
	CPUMaxCorePercent     *float64   `json:"cpu_max_core_percent"`
	CPUCores              *int       `json:"cpu_cores"`
	MemoryPercent         *float64   `json:"memory_percent"`
	MemoryUsedBytes       *uint64    `json:"memory_used_bytes,string"`
	MemoryTotalBytes      *uint64    `json:"memory_total_bytes,string"`
	TemperatureCelsius    *float64   `json:"temperature_celsius"`
	CPUTemperatureCelsius *float64   `json:"cpu_temperature_celsius"`
	Voltage               *float64   `json:"voltage"`
	Firmware              *string    `json:"firmware"`
	Model                 *string    `json:"model"`
	SerialNumber          *string    `json:"serial_number"`
	ICMPRTTMs             *float64   `json:"icmp_rtt_ms"`
	ICMPLossRatio         *float64   `json:"icmp_loss_ratio"`
	Sensors               []Sensor   `json:"sensors"`
}

// Uplink resume la interfaz de subida (RouterUplink).
type Uplink struct {
	InterfaceID uuid.UUID `json:"interface_id"`
	Name        string    `json:"name"`
	RxBps       *float64  `json:"rx_bps"`
	TxBps       *float64  `json:"tx_bps"`
	SpeedBps    *int64    `json:"speed_bps"`
	Utilization *float64  `json:"utilization"`
}

// ThresholdHit es un umbral superado (RouterThresholdHit).
type ThresholdHit struct {
	Metric        string  `json:"metric"`
	Level         string  `json:"level"` // warning | critical
	Value         float64 `json:"value"`
	Threshold     float64 `json:"threshold"`
	InterfaceName *string `json:"interface_name"`
}

// RouterStatus es la fila de GET /routers/status.
type RouterStatus struct {
	RouterID        uuid.UUID      `json:"router_id"`
	SiteID          uuid.UUID      `json:"site_id"`
	Name            string         `json:"name"`
	SiteName        string         `json:"site_name"`
	Status          string         `json:"status"`
	ObservedState   *string        `json:"observed_state"`
	Reason          *string        `json:"reason"`
	Since           *time.Time     `json:"since"`
	LastObservedAt  *time.Time     `json:"last_observed_at"`
	Signals         Signals        `json:"signals"`
	Device          DeviceSample   `json:"device"`
	Uplink          *Uplink        `json:"uplink"`
	InterfacesUp    *int           `json:"interfaces_up"`
	InterfacesTotal *int           `json:"interfaces_total"`
	Thresholds      []ThresholdHit `json:"thresholds"`
	// LastFlowAt es el último flujo recibido del exportador del router
	// (estado del collector leído en el último sondeo; nil = nunca).
	LastFlowAt *time.Time `json:"last_flow_at"`
	// ConnectionMode y ProtectionLevel del router (docs/tunnels.md): el NOC
	// marca los transportes sin cifrado.
	ConnectionMode  string `json:"connection_mode,omitempty"`
	ProtectionLevel string `json:"protection_level,omitempty"`
	// DeviceType: "switch" en los switches (vacío = router: el JSON de los
	// routers no cambia).
	DeviceType string `json:"device_type,omitempty"`
}

// PollerHealth es meta.poller de GET /routers/status.
type PollerHealth struct {
	Healthy         bool       `json:"healthy"`
	LastCycleAt     *time.Time `json:"last_cycle_at"`
	IntervalSeconds int        `json:"interval_seconds"`
	Targets         int        `json:"targets"`
	Enabled         bool       `json:"enabled"`
}

// InterfaceSample es la última muestra de una interfaz (InterfaceSample).
type InterfaceSample struct {
	InterfaceID          uuid.UUID `json:"interface_id"`
	IfIndex              int       `json:"if_index"`
	Name                 string    `json:"name"`
	Alias                *string   `json:"alias"`
	Uplink               bool      `json:"uplink"`
	OperStatus           *string   `json:"oper_status"`
	SpeedBps             *int64    `json:"speed_bps"`
	RxBps                *float64  `json:"rx_bps"`
	TxBps                *float64  `json:"tx_bps"`
	RxPps                *float64  `json:"rx_pps"`
	TxPps                *float64  `json:"tx_pps"`
	RxErrorsDelta        *int64    `json:"rx_errors_delta"`
	TxErrorsDelta        *int64    `json:"tx_errors_delta"`
	RxDropsDelta         *int64    `json:"rx_drops_delta"`
	TxDropsDelta         *int64    `json:"tx_drops_delta"`
	UtilizationRx        *float64  `json:"utilization_rx"`
	UtilizationTx        *float64  `json:"utilization_tx"`
	CounterDiscontinuity bool      `json:"counter_discontinuity"`
}

// RouterLiveMetrics es GET /routers/{id}/metrics/live.
type RouterLiveMetrics struct {
	Status          RouterStatus      `json:"status"`
	IntervalSeconds int               `json:"interval_seconds"`
	Interfaces      []InterfaceSample `json:"interfaces"`
}

// RouterStatusReader es lo que el rol snmp publica en module.Services para
// los demás roles del proceso (analytics: widget; gateway: snapshot del tema
// router.{id}.metrics). sites vacío = todo el tenant.
type RouterStatusReader interface {
	// List devuelve el estado de los routers del tenant (ordenados: problemas
	// primero, después por nombre) y la salud del sondeador.
	List(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID) ([]RouterStatus, PollerHealth, error)
	// Live devuelve la última muestra completa de un router (ok=false si no
	// es del tenant o no existe).
	Live(ctx context.Context, tenant, routerID uuid.UUID) (*RouterLiveMetrics, bool, error)
}

// Señales (valores de Signals).
const (
	SignalOK         = "ok"
	SignalFail       = "fail"
	SignalUnknown    = "unknown"
	SignalAuthFailed = "auth_failed" // solo SNMP
	SignalStale      = "stale"       // solo WireGuard
	SignalNever      = "never"       // solo WireGuard
)

// Estados operativos de una interfaz (InterfaceOperStatus).
const (
	OperUp             = "up"
	OperDown           = "down"
	OperTesting        = "testing"
	OperUnknown        = "unknown"
	OperDormant        = "dormant"
	OperNotPresent     = "not_present"
	OperLowerLayerDown = "lower_layer_down"
)

// RouterInterface es una interfaz descubierta por SNMP
// (GET /routers/{id}/interfaces, esquema RouterInterface).
type RouterInterface struct {
	ID         uuid.UUID  `json:"id"`
	IfIndex    int        `json:"if_index"`
	Name       string     `json:"name"`
	Alias      *string    `json:"alias"`
	Type       string     `json:"type"`
	SpeedBps   *int64     `json:"speed_bps"`
	Monitored  bool       `json:"monitored"`
	Uplink     bool       `json:"uplink"`
	OperStatus *string    `json:"oper_status"`
	AdminUp    *bool      `json:"admin_up"`
	LastSeenAt *time.Time `json:"last_seen_at"`
}

// RouterInterfaces es la respuesta de GET /routers/{id}/interfaces.
type RouterInterfaces struct {
	Data         []RouterInterface `json:"data"`
	DiscoveredAt *time.Time        `json:"discovered_at"`
}

// InterfaceLister es opcional en el mismo registro (lo implementa el rol
// snmp junto a RouterStatusReader): inventario de interfaces de un router.
type InterfaceLister interface {
	Interfaces(ctx context.Context, tenant, routerID uuid.UUID) (*RouterInterfaces, bool, error)
}

// InterfaceNamespace es el espacio de UUIDv5 de interface_id =
// uuid5(router_id, ifName): estable entre reinicios y re-descubrimientos.
var InterfaceNamespace = uuid.MustParse("0192f2a0-5e8c-7b31-9d4a-6c1e2f3a4b5c")

// InterfaceID deriva el interface_id estable de una interfaz.
func InterfaceID(routerID uuid.UUID, ifName string) uuid.UUID {
	return uuid.NewSHA1(uuid.NewSHA1(InterfaceNamespace, routerID[:]), []byte(ifName))
}

// SwitchStatus es la fila de GET /switches/status: el estado del switch como
// el de un router (RouterStatus, device_type = switch) más su plataforma y
// el resumen de puertos.
type SwitchStatus struct {
	RouterStatus
	// Platform: routeros | swos | vrp.
	Platform       string `json:"platform"`
	SFlowEnabled   bool   `json:"sflow_enabled"`
	PortsMonitored int    `json:"ports_monitored"`
	Uplinks        int    `json:"uplinks"`
	UplinksDown    int    `json:"uplinks_down"`
	PortsFlapping  int    `json:"ports_flapping"`
	NeighborsCount int    `json:"neighbors_count"`
}

// SwitchNeighbor es un vecino LLDP (o de la tabla de vecinos de MikroTik)
// visto en un puerto del switch.
type SwitchNeighbor struct {
	LocalIfIndex int     `json:"local_if_index"`
	LocalPort    string  `json:"local_port"`
	SysName      string  `json:"sys_name"`
	PortID       string  `json:"port_id"`
	PortDesc     string  `json:"port_desc"`
	ChassisID    string  `json:"chassis_id"`
	MgmtAddress  *string `json:"mgmt_address"`
	Platform     *string `json:"platform"`
	// Source: lldp | mndp.
	Source string `json:"source"`
	// KnownDevice: nombre del router o switch de Horus que es el vecino.
	KnownDevice *string `json:"known_device"`
}

// SwitchNeighbors es GET /switches/{id}/neighbors.
type SwitchNeighbors struct {
	Data         []SwitchNeighbor `json:"data"`
	DiscoveredAt *time.Time       `json:"discovered_at"`
}

// SwitchPort es un puerto del switch con su elección y su última muestra.
type SwitchPort struct {
	InterfaceID     uuid.UUID        `json:"interface_id"`
	IfIndex         int              `json:"if_index"`
	Name            string           `json:"name"`
	Alias           *string          `json:"alias"`
	Type            string           `json:"type"`
	SpeedBps        *int64           `json:"speed_bps"`
	AdminUp         *bool            `json:"admin_up"`
	OperStatus      *string          `json:"oper_status"`
	Monitored       bool             `json:"monitored"`
	Uplink          bool             `json:"uplink"`
	SuggestedUplink bool             `json:"suggested_uplink"`
	Flaps1h         int              `json:"flaps_1h"`
	Flaps24h        int              `json:"flaps_24h"`
	LastChangeAt    *time.Time       `json:"last_change_at"`
	Sample          *InterfaceSample `json:"sample"`
	Neighbors       []SwitchNeighbor `json:"neighbors"`
}

// PortSelectionMeta dice si la elección es la del operador (configured) o la
// de por defecto, y su versión (ETag de PUT).
type PortSelectionMeta struct {
	Configured bool       `json:"configured"`
	Version    *int       `json:"version"`
	UpdatedAt  *time.Time `json:"updated_at"`
	UpdatedBy  *string    `json:"updated_by"`
}

// SwitchPorts es GET /switches/{id}/ports.
type SwitchPorts struct {
	RouterID        uuid.UUID         `json:"router_id"`
	Platform        string            `json:"platform"`
	IntervalSeconds int               `json:"interval_seconds"`
	MaxPorts        int               `json:"max_ports"`
	MonitoredCount  int               `json:"monitored_count"`
	Selection       PortSelectionMeta `json:"selection"`
	DiscoveredAt    *time.Time        `json:"discovered_at"`
	NeighborsAt     *time.Time        `json:"neighbors_at"`
	Data            []SwitchPort      `json:"data"`
}
