// Package app es el sondeador del rol snmp: objetivos (routers con túnel y
// alta avanzada), ciclo SNMPv3 por el túnel con concurrencia acotada, ping
// ICMP, correlación de señales con histéresis (estado observado), estado
// efectivo para la API, persistencia en NATS KV, eventos, telemetría y filas
// para ClickHouse. Sin NATS ni ClickHouse funciona solo en memoria.
package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	collectorapi "github.com/hcdestroyer/horus-flow/services/collector/api"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/clickhouse"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/icmp"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/snmpclient"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/config"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// Tipos de evento (docs/events.md §8.4).
const (
	TypeStateChanged         = "horus.snmp.router.state_changed"
	TypeRebooted             = "horus.snmp.router.rebooted"
	TypeInterfacesDiscovered = "horus.snmp.router.interfaces_discovered"
	TypeOperStatusChanged    = "horus.snmp.interface.oper_status_changed"
	TypeHeartbeat            = "horus.snmp.poller.heartbeat"
	TypeDeviceSampled        = "horus.snmp.device.sampled"
	TypeInterfaceSampled     = "horus.snmp.interface.sampled"

	heartbeatEvery   = 15 * time.Second
	targetsEvery     = time.Minute
	manualPollWindow = 10 * time.Second
	icmpProbes       = 3
	icmpFailCycles   = 3
	credentialRetry  = 5 * time.Minute
	verifyRetry      = 5 * time.Minute
)

// instanceNamespace deriva el entity_id del heartbeat (UUIDv5 de la instancia).
var instanceNamespace = uuid.MustParse("0192f2a0-6c1d-7e42-8f5b-3a9d7e2c1b04")

// Event es un evento de dominio a publicar (SNMP_EVENTS).
type Event struct {
	Type          string
	Entity        string
	TenantID      *uuid.UUID
	AggregateType string
	Version       int
	At            time.Time
	Data          any
}

// EventPublisher publica con confirmación.
type EventPublisher interface {
	Publish(ctx context.Context, ev Event) error
}

// Sample es una muestra de telemetría (TLM_SNMP).
type Sample struct {
	Subject  string
	Type     string
	TenantID uuid.UUID
	BatchID  string
	At       time.Time
	Body     []byte
}

// TelemetryPublisher publica telemetría.
type TelemetryPublisher interface {
	Publish(ctx context.Context, s Sample) error
}

// StateStore persiste el estado por router (NATS KV snmp_router_state).
type StateStore interface {
	Load(ctx context.Context) (map[uuid.UUID][]byte, error)
	Save(ctx context.Context, id uuid.UUID, b []byte) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// Sink recibe los lotes para ClickHouse.
type Sink interface {
	Enqueue(b clickhouse.Batch)
}

// Options configura el sondeador.
type Options struct {
	Config config.Config
	// Devices lista los routers (objetivos); Credentials revela la credencial SNMPv3.
	Devices     func() (devapi.Onboarding, bool)
	Credentials func() (devapi.SNMPCredentialReader, bool)
	// Verifier marca como verificado un router en modo directo tras la
	// primera respuesta SNMPv3 authPriv en su dirección (opcional).
	Verifier func() (devapi.DirectVerifier, bool)
	// Inv da nombres de router y nodo (opcional).
	Inv *flowinv.Store
	// Exporters: estado del exportador de flujos (opcional).
	Exporters collectorapi.States
	Dialer    snmpclient.Dialer
	// Pinger nil = sin ICMP (señal unknown).
	Pinger    icmp.Pinger
	Events    EventPublisher
	Telemetry TelemetryPublisher
	Store     StateStore
	Sink      Sink
	// Thresholds carga los umbrales propios de cada ISP (opcional: sin
	// PostgreSQL rigen los de la instalación).
	Thresholds ThresholdLoader
	Metrics    prometheus.Registerer
	Logger     *slog.Logger
	Now        func() time.Time
}

// ThresholdLoader devuelve los umbrales propios de todos los ISP.
type ThresholdLoader interface {
	LoadAll(ctx context.Context) (map[uuid.UUID]domain.ThresholdValues, error)
}

// Engine es el sondeador.
type Engine struct {
	o   Options
	log *slog.Logger
	now func() time.Time
	m   *metrics

	mu      sync.RWMutex
	routers map[uuid.UUID]*router
	// saved: estado cargado del KV de routers aún no vistos como objetivo.
	saved     map[uuid.UUID][]byte
	lastCycle *time.Time
	lastErrs  int
	cycle     uint64

	refresh    chan struct{}
	manual     chan uuid.UUID
	instanceID string
	sem        chan struct{}
	thresholds domain.Thresholds
	// Umbrales propios por ISP (consola): mutex propio porque se leen con
	// r.mu tomado y e.mu se toma antes que r.mu en otros caminos.
	thMu     sync.RWMutex
	tenantTh map[uuid.UUID]domain.ThresholdValues
}

// New crea el sondeador.
func New(o Options) *Engine {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Dialer == nil {
		o.Dialer = snmpclient.GoSNMP{Timeout: o.Config.Timeout, Retries: o.Config.Retries}
	}
	id := o.Config.InstanceID
	if id == "" {
		id, _ = os.Hostname()
		if id == "" {
			id = "snmp-1"
		}
	}
	conc := o.Config.Concurrency
	if conc <= 0 {
		conc = 32
	}
	e := &Engine{o: o, log: o.Logger, now: o.Now, routers: map[uuid.UUID]*router{}, saved: map[uuid.UUID][]byte{},
		refresh: make(chan struct{}, 1), manual: make(chan uuid.UUID, 256), instanceID: id, sem: make(chan struct{}, conc),
		thresholds: domain.Thresholds{CPUWarning: o.Config.CPUWarning, CPUCritical: o.Config.CPUCritical,
			MemoryWarning: o.Config.MemoryWarning, MemoryCritical: o.Config.MemoryCritical,
			TemperatureWarning: o.Config.TemperatureWarning, TemperatureCritical: o.Config.TemperatureCritical, SustainedCycles: 3}}
	if e.thresholds.CPUWarning == 0 {
		e.thresholds = domain.DefaultThresholds
	}
	e.m = newMetrics(o.Metrics)
	return e
}

// Attach sustituye los adaptadores (Store, Events, Telemetry, Sink,
// Exporters, Pinger) antes de Start: el módulo construye el engine en
// Register (para publicarlo en Services) y abre las conexiones en Start.
func (e *Engine) Attach(o Options) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.o.Store, e.o.Events, e.o.Telemetry, e.o.Sink, e.o.Exporters = o.Store, o.Events, o.Telemetry, o.Sink, o.Exporters
	if o.Thresholds != nil {
		e.o.Thresholds = o.Thresholds
	}
	if o.Pinger != nil {
		e.o.Pinger = o.Pinger
	}
}

// Interval devuelve el intervalo de sondeo.
func (e *Engine) Interval() time.Duration {
	if e.o.Config.Interval <= 0 {
		return api.DefaultIntervalSeconds * time.Second
	}
	return e.o.Config.Interval
}

// Start carga el estado persistido y los objetivos iniciales.
func (e *Engine) Start(ctx context.Context) error {
	if e.o.Store != nil {
		saved, err := e.o.Store.Load(ctx)
		if err != nil {
			e.log.WarnContext(ctx, "snmp: router state not restored from KV", "error", err)
		} else {
			e.mu.Lock()
			e.saved = saved
			e.mu.Unlock()
			e.log.InfoContext(ctx, "snmp: router state restored", "routers", len(saved))
		}
	}
	e.refreshTargets(ctx)
	e.reloadThresholds(ctx)
	return nil
}

// DefaultThresholds son los umbrales de la instalación (HORUS_SNMP_*).
func (e *Engine) DefaultThresholds() domain.ThresholdValues { return e.thresholds.Values() }

// thresholdsFor devuelve los umbrales vigentes del ISP.
func (e *Engine) thresholdsFor(tenant uuid.UUID) domain.Thresholds {
	e.thMu.RLock()
	defer e.thMu.RUnlock()
	if v, ok := e.tenantTh[tenant]; ok {
		return e.thresholds.With(v)
	}
	return e.thresholds
}

// SetTenantThresholds aplica en caliente los umbrales de un ISP (nil = los de
// la instalación). Las demás réplicas los recogen al recargar (cada minuto).
func (e *Engine) SetTenantThresholds(tenant uuid.UUID, v *domain.ThresholdValues) {
	e.thMu.Lock()
	defer e.thMu.Unlock()
	if e.tenantTh == nil {
		e.tenantTh = map[uuid.UUID]domain.ThresholdValues{}
	}
	if v == nil {
		delete(e.tenantTh, tenant)
		return
	}
	e.tenantTh[tenant] = *v
}

// reloadThresholds recarga los umbrales propios de todos los ISP.
func (e *Engine) reloadThresholds(ctx context.Context) {
	e.mu.RLock()
	src := e.o.Thresholds
	e.mu.RUnlock()
	if src == nil {
		return
	}
	m, err := src.LoadAll(ctx)
	if err != nil {
		e.log.WarnContext(ctx, "snmp: tenant thresholds not reloaded (previous kept)", "error", err)
		return
	}
	e.thMu.Lock()
	e.tenantTh = m
	e.thMu.Unlock()
}

// Run ejecuta los bucles hasta que ctx se cancela.
func (e *Engine) Run(ctx context.Context) error {
	if !e.o.Config.Enabled {
		e.log.InfoContext(ctx, "snmp: polling disabled (HORUS_SNMP_ENABLED=false)")
		<-ctx.Done()
		return nil
	}
	var wg sync.WaitGroup
	loops := []func(context.Context){e.cycleLoop, e.heartbeatLoop, e.targetsLoop, e.manualLoop}
	if e.o.Pinger != nil {
		loops = append(loops, e.icmpLoop)
	}
	for _, l := range loops {
		wg.Add(1)
		go func() { defer wg.Done(); l(ctx) }()
	}
	wg.Wait()
	return nil
}

func (e *Engine) cycleLoop(ctx context.Context) {
	// Primer ciclo inmediato, después cada intervalo alineado.
	e.runCycle(ctx, e.now())
	t := time.NewTicker(e.Interval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case at := <-t.C:
			e.runCycle(ctx, at)
		}
	}
}

func (e *Engine) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		e.heartbeat(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (e *Engine) targetsLoop(ctx context.Context) {
	t := time.NewTicker(targetsEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-e.refresh:
		}
		e.refreshTargets(ctx)
		e.reloadThresholds(ctx)
	}
}

func (e *Engine) manualLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-e.manual:
			e.mu.RLock()
			r := e.routers[id]
			e.mu.RUnlock()
			if r == nil {
				continue
			}
			e.pollOne(ctx, r, e.now(), e.cycleNo(), true)
		}
	}
}

func (e *Engine) icmpLoop(ctx context.Context) {
	e.runICMP(ctx)
	t := time.NewTicker(e.o.Config.ICMPInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.runICMP(ctx)
		}
	}
}

func (e *Engine) cycleNo() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cycle
}

// RouterChanged recarga objetivos y credenciales de un router (evento
// horus.devices.router.*).
func (e *Engine) RouterChanged(id uuid.UUID) {
	e.mu.RLock()
	r := e.routers[id]
	e.mu.RUnlock()
	if r != nil {
		r.mu.Lock()
		r.cred, r.credAt = nil, time.Time{}
		r.mu.Unlock()
	}
	select {
	case e.refresh <- struct{}{}:
	default:
	}
}

// SetWireGuard fija la señal del túnel de un router (ok | stale).
func (e *Engine) SetWireGuard(tenant, id uuid.UUID, signal string, at time.Time) {
	e.mu.RLock()
	r := e.routers[id]
	e.mu.RUnlock()
	if r == nil || (tenant != uuid.Nil && r.TenantID != tenant) {
		return
	}
	r.mu.Lock()
	r.st.WireGuard, r.st.WireGuardAt = signal, &at
	r.mu.Unlock()
}

// refreshTargets lista los routers con túnel y alta avanzada y sincroniza el mapa.
func (e *Engine) refreshTargets(ctx context.Context) {
	dev, ok := e.o.Devices()
	if !ok {
		e.log.WarnContext(ctx, "snmp: devices not local in this process: no polling targets")
		return
	}
	list, err := dev.ListRouters(ctx)
	if err != nil {
		e.log.WarnContext(ctx, "snmp: routers not listed (targets kept)", "error", err)
		return
	}
	var inv *flowinv.Snapshot
	if e.o.Inv != nil {
		inv = e.o.Inv.Load()
	}
	seen := map[uuid.UUID]bool{}
	e.mu.Lock()
	for _, ri := range list {
		addr, ok := pollAddress(ri)
		if !ok {
			continue
		}
		seen[ri.ID] = true
		t := Target{RouterID: ri.ID, TenantID: ri.TenantID, SiteID: ri.SiteID, Name: ri.Name, Tunnel: addr,
			Vendor: snmpclient.ForVendor(ri.Vendor).Vendor(), Direct: ri.ConnectionMode == devapi.ConnectionDirect,
			OnboardingState: ri.OnboardingState}
		if inv != nil {
			if x, ok := inv.Router(ri.ID); ok {
				if x.Name != "" {
					t.Name = x.Name
				}
				t.SiteName = x.SiteName
				for _, i := range x.Interfaces {
					if i.FlowRole == "upstream" {
						t.Upstream = append(t.Upstream, int(i.IfIndex)) //nolint:gosec // ifIndex pequeño
					}
				}
			}
		}
		r := e.routers[ri.ID]
		if r == nil {
			r = &router{Target: t}
			if b, ok := e.saved[ri.ID]; ok {
				if err := json.Unmarshal(b, &r.st); err != nil {
					e.log.Warn("snmp: saved state ignored", "router_id", ri.ID, "error", err)
					r.st = persisted{}
				}
				delete(e.saved, ri.ID)
			}
			e.routers[ri.ID] = r
		} else {
			r.mu.Lock()
			r.Target = t
			r.mu.Unlock()
		}
	}
	for id := range e.routers {
		if !seen[id] {
			delete(e.routers, id)
			if e.o.Store != nil {
				_ = e.o.Store.Delete(ctx, id)
			}
		}
	}
	n := len(e.routers)
	e.mu.Unlock()
	e.m.targets.Set(float64(n))
}

// pollAddress devuelve la dirección por la que se sondea un router: la IP de
// túnel WireGuard con el túnel arriba (MikroTik), o la dirección de gestión
// declarada en modo directo (Huawei VRP, sin túnel; docs/vendors/huawei.md
// §5), en cuanto existe.
func pollAddress(ri devapi.RouterInfo) (netip.Addr, bool) {
	if ri.ConnectionMode == devapi.ConnectionDirect {
		if ri.ManagementAddress == nil || !ri.ManagementAddress.IsValid() {
			return netip.Addr{}, false
		}
		return ri.ManagementAddress.Unmap(), true
	}
	if ri.TunnelAddress == nil || !ri.TunnelAddress.IsValid() {
		return netip.Addr{}, false
	}
	if ri.OnboardingState != devapi.OnboardingTunnelUp && ri.OnboardingState != devapi.OnboardingExporting {
		return netip.Addr{}, false
	}
	return ri.TunnelAddress.Unmap(), true
}

// Target es un router a sondear.
type Target struct {
	RouterID uuid.UUID
	TenantID uuid.UUID
	SiteID   uuid.UUID
	Name     string
	SiteName string
	// Tunnel es la dirección de sondeo: IP de túnel o, en modo directo, la
	// dirección de gestión del router.
	Tunnel netip.Addr
	// Vendor elige el adaptador SNMP (snmpclient.ForVendor).
	Vendor string
	// Direct: modo directo sin túnel (sin señal WireGuard).
	Direct bool
	// OnboardingState del router (devices).
	OnboardingState string
	// Upstream: ifIndex marcados upstream en el inventario.
	Upstream []int
}

// router es el estado en memoria de un objetivo.
type router struct {
	mu sync.Mutex
	Target
	st        persisted
	cred      *snmpclient.Credential
	credAt    time.Time // último intento de carga
	noCred    bool
	polling   bool
	lastPoll  time.Time
	lastManua time.Time
	// verifyAt: último intento de verificar un router en modo directo.
	verifyAt time.Time
}

func (e *Engine) snapshot() []*router {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]*router, 0, len(e.routers))
	for _, r := range e.routers {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b *router) int { return compareID(a.RouterID, b.RouterID) })
	return out
}

func compareID(a, b uuid.UUID) int {
	switch {
	case a.String() < b.String():
		return -1
	case a.String() > b.String():
		return 1
	}
	return 0
}
