package app

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/netip"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/probe"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// ActiveProber ejecuta un chequeo activo (adapters/probe).
type ActiveProber interface {
	Run(ctx context.Context, r probe.Request) domain.CheckObservation
}

// FlowReader lee el tráfico por minuto de varios objetivos de un tenant en
// [from, to) (adapters/clickhouse.FlowReader).
type FlowReader interface {
	TargetTraffic(ctx context.Context, tenant uuid.UUID, targets []netip.Addr, from, to time.Time) (map[netip.Addr][]domain.MinuteTraffic, error)
}

// SchedulerOptions configura el planificador.
type SchedulerOptions struct {
	Store   InfraStore
	Prober  ActiveProber
	Flows   FlowReader
	Devices func() (devapi.Onboarding, bool)
	// Concurrency: servicios sondeados a la vez.
	Concurrency int
	// Timeout por chequeo activo (se acota a la mitad del intervalo).
	Timeout time.Duration
	// FlowLag: margen para la latencia de exportación de flujos (active
	// timeout del router 1 min); la ventana pasiva termina en now-FlowLag.
	FlowLag time.Duration
	Metrics prometheus.Registerer
	Logger  *slog.Logger
	Now     func() time.Time
}

type infraRuntime struct {
	svc     domain.Service
	next    time.Time
	running bool
}

// InfraScheduler ejecuta los chequeos de los servicios de infraestructura de
// todos los ISP: cada servicio cada interval_seconds (con jitter), activos en
// paralelo con concurrencia acotada y pasivos agrupados por tenant (una
// consulta a ClickHouse por tenant y ciclo). Mantiene el estado en memoria,
// lo guarda en PostgreSQL y emite horus.snmp.service.state_changed por el
// outbox cuando cambia el estado del servicio. Un único rol snmp activo por
// instalación (como el sondeador de routers).
type InfraScheduler struct {
	o       SchedulerOptions
	log     *slog.Logger
	now     func() time.Time
	sem     chan struct{}
	refresh chan struct{}

	mu        sync.Mutex
	services  map[uuid.UUID]*infraRuntime
	lastCycle *time.Time
	lastLoad  time.Time

	checks   *prometheus.CounterVec
	statesG  *prometheus.GaugeVec
	disabled bool
}

// NewInfraScheduler crea el planificador.
func NewInfraScheduler(o SchedulerOptions) *InfraScheduler {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 32
	}
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Second
	}
	if o.FlowLag <= 0 {
		o.FlowLag = 2 * time.Minute
	}
	if o.Devices == nil {
		o.Devices = func() (devapi.Onboarding, bool) { return nil, false }
	}
	s := &InfraScheduler{o: o, log: o.Logger, now: o.Now, sem: make(chan struct{}, o.Concurrency), refresh: make(chan struct{}, 1),
		services: map[uuid.UUID]*infraRuntime{},
		checks: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_snmp_infra_checks_total",
			Help: "Chequeos de servicios de infraestructura ejecutados por tipo y resultado observado."}, []string{"type", "result"}),
		statesG: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "horus_snmp_infra_services",
			Help: "Servicios de infraestructura vigilados por estado."}, []string{"state"}),
	}
	if o.Metrics != nil {
		for _, c := range []prometheus.Collector{s.checks, s.statesG} {
			_ = o.Metrics.Register(c)
		}
	}
	return s
}

// Disable deja el planificador sin sondear (HORUS_SNMP_ENABLED=false).
func (s *InfraScheduler) Disable() {
	if s != nil {
		s.disabled = true
	}
}

// SetFlows fija el lector de flujos antes de Run (se abre en Start).
func (s *InfraScheduler) SetFlows(f FlowReader) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.o.Flows = f
	s.mu.Unlock()
}

// Refresh pide recargar el inventario (tras un alta, edición o baja).
func (s *InfraScheduler) Refresh() {
	if s == nil {
		return
	}
	select {
	case s.refresh <- struct{}{}:
	default:
	}
}

// Health es meta.scheduler de GET /infra-services.
type Health struct {
	Enabled       bool
	Healthy       bool
	LastCycleAt   *time.Time
	PassiveChecks bool
}

// Health devuelve el estado del planificador.
func (s *InfraScheduler) Health() Health {
	if s == nil || s.disabled {
		return Health{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	h := Health{Enabled: true, PassiveChecks: s.o.Flows != nil, LastCycleAt: s.lastCycle}
	h.Healthy = s.lastCycle != nil && s.now().Sub(*s.lastCycle) < time.Minute
	return h
}

// Run planifica hasta que ctx se cancela.
func (s *InfraScheduler) Run(ctx context.Context) error {
	if s == nil || s.disabled || s.o.Store == nil {
		return nil
	}
	s.load(ctx)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.refresh:
			s.load(ctx)
		case <-tick.C:
			if s.now().Sub(s.lastLoad) > time.Minute {
				s.load(ctx)
			}
			s.Tick(ctx)
		}
	}
}

// load relee el inventario conservando la planificación y el estado en memoria.
func (s *InfraScheduler) load(ctx context.Context) {
	all, err := s.o.Store.LoadAll(ctx)
	if err != nil {
		s.log.WarnContext(ctx, "snmp: infra services not loaded (keeping previous)", "error", err)
		return
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastLoad = now
	seen := map[uuid.UUID]bool{}
	for k := range all {
		svc := all[k]
		seen[svc.ID] = true
		rt, ok := s.services[svc.ID]
		if !ok {
			// Primer turno repartido en el intervalo para no sondear todo a la vez.
			off := time.Duration(rand.Int64N(int64(time.Duration(max(svc.IntervalSeconds, 1)) * time.Second))) //nolint:gosec // jitter
			s.services[svc.ID] = &infraRuntime{svc: svc, next: now.Add(off)}
			continue
		}
		if rt.running {
			// El ciclo en curso guarda su estado; se adopta solo la configuración.
			svc.Status, svc.CheckStatus = rt.svc.Status, rt.svc.CheckStatus
		}
		if svc.IntervalSeconds != rt.svc.IntervalSeconds {
			rt.next = now.Add(time.Duration(svc.IntervalSeconds) * time.Second)
		}
		rt.svc = svc
	}
	for id := range s.services {
		if !seen[id] {
			delete(s.services, id)
		}
	}
	s.gauge()
}

func (s *InfraScheduler) gauge() {
	n := map[string]float64{domain.StateUp: 0, domain.StateDown: 0, domain.StateDegraded: 0, domain.StateUnknown: 0}
	for _, rt := range s.services {
		if rt.svc.Enabled {
			n[rt.svc.Status.State]++
		}
	}
	for st, v := range n {
		s.statesG.WithLabelValues(st).Set(v)
	}
}

// Tick lanza los servicios a los que les toca (exportado para los tests) y
// espera a que terminen.
func (s *InfraScheduler) Tick(ctx context.Context) {
	now := s.now()
	s.mu.Lock()
	byTenant := map[uuid.UUID][]*infraRuntime{}
	for _, rt := range s.services {
		if !rt.svc.Enabled || rt.running || now.Before(rt.next) {
			continue
		}
		rt.running = true
		iv := time.Duration(rt.svc.IntervalSeconds) * time.Second
		jitter := time.Duration(rand.Int64N(int64(max(iv/10, time.Second)))) //nolint:gosec // jitter
		rt.next = now.Add(iv + jitter)
		byTenant[rt.svc.TenantID] = append(byTenant[rt.svc.TenantID], rt)
	}
	s.mu.Unlock()
	if len(byTenant) == 0 {
		s.markCycle(now)
		return
	}
	var wg sync.WaitGroup
	for tenant, rts := range byTenant {
		passive := s.passive(ctx, tenant, rts, now)
		for _, rt := range rts {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.sem <- struct{}{}
				defer func() { <-s.sem }()
				s.runService(ctx, rt, passive[rt.svc.ID], now)
			}()
		}
	}
	wg.Wait()
	s.markCycle(now)
}

func (s *InfraScheduler) markCycle(at time.Time) {
	s.mu.Lock()
	s.lastCycle = &at
	s.gauge()
	s.mu.Unlock()
}

// passive evalúa los chequeos no_traffic y ddos_inbound de los servicios de
// un tenant con una sola consulta (tráfico por minuto de cada objetivo).
func (s *InfraScheduler) passive(ctx context.Context, tenant uuid.UUID, rts []*infraRuntime, now time.Time) map[uuid.UUID]map[uuid.UUID]domain.CheckObservation {
	out := map[uuid.UUID]map[uuid.UUID]domain.CheckObservation{}
	window := time.Duration(0)
	var targets []netip.Addr
	seen := map[netip.Addr]bool{}
	for _, rt := range rts {
		for _, c := range rt.svc.Checks {
			switch c.Type {
			case domain.CheckNoTraffic:
				window = max(window, c.Window())
			case domain.CheckDDoS:
				window = max(window, domain.DDoSWindow)
			default:
				continue
			}
			if !seen[rt.svc.Target] {
				seen[rt.svc.Target] = true
				targets = append(targets, rt.svc.Target)
			}
		}
	}
	if len(targets) == 0 {
		return out
	}
	to := now.Add(-s.o.FlowLag).Truncate(time.Minute)
	from := to.Add(-window)
	var data map[netip.Addr][]domain.MinuteTraffic
	var err error
	s.mu.Lock()
	flows := s.o.Flows
	s.mu.Unlock()
	if flows != nil {
		qctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		data, err = flows.TargetTraffic(qctx, tenant, targets, from, to)
		cancel()
		if err != nil {
			s.log.WarnContext(ctx, "snmp: infra passive checks without flow data", "error", err)
		}
	}
	for _, rt := range rts {
		for _, c := range rt.svc.Checks {
			if c.Type != domain.CheckNoTraffic && c.Type != domain.CheckDDoS {
				continue
			}
			var o domain.CheckObservation
			switch {
			case flows == nil:
				o = domain.CheckObservation{State: domain.StateUnknown, Reason: domain.ReasonFlowsOff}
			case err != nil:
				o = domain.CheckObservation{State: domain.StateUnknown, Reason: domain.ReasonNoFlowData}
			case c.Type == domain.CheckNoTraffic:
				o = evalNoTraffic(c, rt.svc.StatusOf(c), data[rt.svc.Target], to)
			default:
				o = evalDDoS(c, data[rt.svc.Target], to)
			}
			if out[rt.svc.ID] == nil {
				out[rt.svc.ID] = map[uuid.UUID]domain.CheckObservation{}
			}
			out[rt.svc.ID][c.ID] = o
		}
	}
	return out
}

func sumWindow(rows []domain.MinuteTraffic, from, to time.Time) (bytes, inBytes, inPackets uint64) {
	for _, r := range rows {
		if !r.Minute.Before(from) && r.Minute.Before(to) {
			bytes += r.Bytes
			inBytes += r.InBytes
			inPackets += r.InPackets
		}
	}
	return bytes, inBytes, inPackets
}

// evalNoTraffic: menos de min_bytes en la ventana → down, pero solo si el
// objetivo tuvo tráfico antes (un host ocioso desde siempre no alerta).
func evalNoTraffic(c domain.Check, st domain.CheckStatus, rows []domain.MinuteTraffic, to time.Time) domain.CheckObservation {
	b, _, _ := sumWindow(rows, to.Add(-c.Window()), to)
	detail := fmt.Sprintf("%d bytes en %d min", b, int(c.Window().Minutes()))
	if int64(b) >= c.MinBytesOrDefault() { //nolint:gosec // bytes en una ventana < 2^63
		return domain.CheckObservation{State: domain.StateUp, Reason: domain.ReasonOK, Detail: detail, HadTraffic: true}
	}
	if !st.HadTraffic {
		return domain.CheckObservation{State: domain.StateUnknown, Reason: domain.ReasonNoBaseline, Detail: detail}
	}
	return domain.CheckObservation{State: domain.StateDown, Reason: domain.ReasonNoTraffic, Detail: detail}
}

// evalDDoS: pps/bps entrantes medios de los últimos 5 min por encima de un
// umbral → degraded con razón ddos_inbound.
func evalDDoS(c domain.Check, rows []domain.MinuteTraffic, to time.Time) domain.CheckObservation {
	_, ib, ip := sumWindow(rows, to.Add(-domain.DDoSWindow), to)
	secs := domain.DDoSWindow.Seconds()
	pps, bps := float64(ip)/secs, float64(ib)*8/secs
	detail := fmt.Sprintf("%.0f pps / %.0f bps entrantes", pps, bps)
	if (c.PPSThreshold != nil && pps > *c.PPSThreshold) || (c.BPSThreshold != nil && bps > *c.BPSThreshold) {
		return domain.CheckObservation{State: domain.StateDegraded, Reason: domain.ReasonDDoSInbound, Detail: detail}
	}
	return domain.CheckObservation{State: domain.StateUp, Reason: domain.ReasonOK, Detail: detail}
}

// destination resuelve a dónde se sondea un chequeo activo.
func (s *InfraScheduler) destination(ctx context.Context, svc *domain.Service, c domain.Check, routers map[uuid.UUID]*netip.Addr) (netip.Addr, int, *domain.CheckObservation) {
	if svc.ProbeViaRouterID == nil {
		return svc.Target, c.EffectivePort(), nil
	}
	if c.Type == domain.CheckICMP {
		return netip.Addr{}, 0, &domain.CheckObservation{State: domain.StateUnknown, Reason: domain.ReasonNotForwardable}
	}
	rid := *svc.ProbeViaRouterID
	addr, ok := routers[rid]
	if !ok {
		addr = nil
		if dev, found := s.o.Devices(); found {
			if r, err := dev.GetRouter(ctx, svc.TenantID, rid); err == nil && r.TunnelAddress != nil {
				a := *r.TunnelAddress
				addr = &a
			}
		}
		routers[rid] = addr
	}
	if addr == nil || c.ForwardPort == nil {
		return netip.Addr{}, 0, &domain.CheckObservation{State: domain.StateUnknown, Reason: domain.ReasonRouterOff}
	}
	return *addr, *c.ForwardPort, nil
}

// runService ejecuta los chequeos activos del servicio, junta los pasivos,
// aplica la histéresis y guarda; si cambia el estado del servicio, emite el
// evento con el chequeo que lo causó.
func (s *InfraScheduler) runService(ctx context.Context, rt *infraRuntime, passive map[uuid.UUID]domain.CheckObservation, now time.Time) {
	s.mu.Lock()
	svc := rt.svc
	s.mu.Unlock()
	timeout := min(s.o.Timeout, time.Duration(svc.IntervalSeconds)*time.Second/2)
	obs := make([]domain.CheckObservation, len(svc.Checks))
	routers := map[uuid.UUID]*netip.Addr{}
	var wg sync.WaitGroup
	for i, c := range svc.Checks {
		if !c.Active() {
			if o, ok := passive[c.ID]; ok {
				obs[i] = o
			} else {
				obs[i] = domain.CheckObservation{State: domain.StateUnknown, Reason: domain.ReasonFlowsOff}
			}
			continue
		}
		addr, port, early := s.destination(ctx, &svc, c, routers)
		if early != nil {
			obs[i] = *early
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			obs[i] = s.o.Prober.Run(ctx, probe.Request{Check: c, Addr: addr, Port: port, Timeout: timeout})
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		s.finish(rt, nil)
		return
	}
	statuses := make([]domain.CheckStatus, len(svc.Checks))
	changed := make([]bool, len(svc.Checks))
	for i, c := range svc.Checks {
		st := svc.StatusOf(c)
		changed[i] = st.Apply(obs[i], svc.FailuresBeforeDown, svc.RecoveriesBeforeUp, now)
		statuses[i] = st
		s.checks.WithLabelValues(c.Type, obs[i].State).Inc()
	}
	prev := svc.Status
	state, idx := domain.Worst(statuses)
	next := domain.ServiceStatus{State: state, Since: prev.Since}
	cause := idx
	if state != prev.State {
		at := now.UTC()
		next.Since = &at
		// El chequeo que causó el cambio: el que cambió en este ciclo al estado nuevo.
		for i := range statuses {
			if changed[i] && statuses[i].State == state {
				cause = i
				break
			}
		}
	}
	if cause >= 0 && state != domain.StateUp && state != domain.StateUnknown {
		next.Reason, next.CheckType = statuses[cause].Reason, &statuses[cause].Type
	} else if cause >= 0 && state == domain.StateUp {
		ok := domain.ReasonOK
		next.Reason = &ok
	}
	var ev = (*eventArgs)(nil)
	if state != prev.State && cause >= 0 {
		reason := domain.ReasonOK
		if statuses[cause].Reason != nil {
			reason = *statuses[cause].Reason
		}
		prevState := prev.State
		if prevState == "" {
			prevState = domain.StateUnknown
		}
		ev = &eventArgs{checkType: statuses[cause].Type, previous: prevState, state: state, reason: reason}
	}
	svc.Status, svc.CheckStatus = next, statuses
	var err error
	if ev != nil {
		err = s.o.Store.SaveState(ctx, pgdb.TenantID(svc.TenantID), svc.ID, next, statuses,
			stateChangedEvent(&svc, ev.checkType, ev.previous, ev.state, ev.reason, now))
	} else {
		err = s.o.Store.SaveState(ctx, pgdb.TenantID(svc.TenantID), svc.ID, next, statuses, nil)
	}
	if err != nil {
		// Sin guardar no se adopta el estado: el siguiente ciclo lo reintenta (y el evento con él).
		s.log.WarnContext(ctx, "snmp: infra service state not saved", "service_id", svc.ID.String(), "error", err)
		s.finish(rt, nil)
		return
	}
	if ev != nil {
		s.log.InfoContext(ctx, "snmp: infra service state changed", "service_id", svc.ID.String(), "check_type", ev.checkType,
			"previous_state", ev.previous, "state", ev.state, "reason", ev.reason)
	}
	s.finish(rt, &svc)
}

type eventArgs struct{ checkType, previous, state, reason string }

func (s *InfraScheduler) finish(rt *infraRuntime, svc *domain.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rt.running = false
	if svc != nil && rt.svc.ID == svc.ID {
		// Se conserva la configuración más reciente (pudo recargarse durante el ciclo).
		rt.svc.Status, rt.svc.CheckStatus = svc.Status, mergeStatus(rt.svc.Checks, svc.CheckStatus)
	}
}

// mergeStatus conserva el estado de los chequeos que siguen configurados.
func mergeStatus(checks []domain.Check, st []domain.CheckStatus) []domain.CheckStatus {
	byID := map[uuid.UUID]domain.CheckStatus{}
	for _, c := range st {
		byID[c.CheckID] = c
	}
	out := make([]domain.CheckStatus, 0, len(checks))
	for _, c := range checks {
		if v, ok := byID[c.ID]; ok {
			out = append(out, v)
		} else {
			out = append(out, domain.NewCheckStatus(c))
		}
	}
	return out
}
