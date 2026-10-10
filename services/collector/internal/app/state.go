package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/services/collector/api"
)

// Observation es lo que aporta un datagrama al estado de su exportador.
type Observation struct {
	At         time.Time
	Records    int
	Lost       uint64
	Skew       time.Duration
	FlowSource string
	Sampling   uint32
	Gap        bool
}

// KV guarda el estado por router (bucket flow_exporter_state).
type KV interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, value []byte) error
}

// JetStreamKV adapta jetstream.KeyValue.
type JetStreamKV struct{ KV jetstream.KeyValue }

// Get implementa KV (nil, nil si no existe).
func (k JetStreamKV) Get(ctx context.Context, key string) ([]byte, error) {
	e, err := k.KV.Get(ctx, key)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return e.Value(), nil
}

// Put implementa KV.
func (k JetStreamKV) Put(ctx context.Context, key string, v []byte) error {
	_, err := k.KV.Put(ctx, key, v)
	return err
}

const bucketWidth = 30 * time.Second

type bucket struct {
	start    time.Time
	received uint64
	lost     uint64
}

type exporterState struct {
	cur      api.FlowExporter
	loaded   bool
	lastFlow time.Time
	// firstObs es el primer datagrama visto por este proceso (reinicio del collector).
	firstObs   time.Time
	lastSkew   time.Duration
	hasSkew    bool
	flowSource string
	sampling   uint32
	buckets    []bucket
	gaps, lost uint64
	dirty      bool
	lastWrite  time.Time
}

type unregistered struct {
	first, last, lastEmit time.Time
	datagrams             uint64
}

// StateOptions son los umbrales de I1-09.
type StateOptions struct {
	SilentAfter   time.Duration
	LossThreshold float64
	LossWindow    time.Duration
	ClockSkew     time.Duration
	Interval      time.Duration
}

// States calcula el estado de cada exportador (I1-09), lo guarda en KV y
// publica horus.flows.exporter.{state_changed,silent,recovered} y
// horus.flows.exporter.unregistered.
type States struct {
	opts StateOptions
	inv  *flowinv.Store
	kv   KV
	sink Sink
	log  *slog.Logger
	now  func() time.Time

	mu    sync.Mutex
	byID  map[uuid.UUID]*exporterState
	unreg map[netip.Addr]*unregistered
}

// NewStates crea el evaluador.
func NewStates(o StateOptions, inv *flowinv.Store, kv KV, sink Sink, log *slog.Logger) *States {
	return &States{opts: o, inv: inv, kv: kv, sink: sink, log: log, now: time.Now,
		byID: map[uuid.UUID]*exporterState{}, unreg: map[netip.Addr]*unregistered{}}
}

func (s *States) get(id uuid.UUID) *exporterState {
	st := s.byID[id]
	if st == nil {
		st = &exporterState{}
		s.byID[id] = st
	}
	return st
}

// Observe registra un datagrama válido de exp.
func (s *States) Observe(exp *flowinv.Exporter, o Observation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.get(exp.RouterID)
	if o.Records > 0 {
		st.lastFlow = o.At
	}
	if st.firstObs.IsZero() {
		st.firstObs = o.At
	}
	st.lastSkew, st.hasSkew = o.Skew, true
	st.flowSource, st.sampling = o.FlowSource, o.Sampling
	if o.Gap {
		st.gaps++
	}
	st.lost += o.Lost
	start := o.At.Truncate(bucketWidth)
	if n := len(st.buckets); n == 0 || !st.buckets[n-1].start.Equal(start) {
		st.buckets = append(st.buckets, bucket{start: start})
	}
	b := &st.buckets[len(st.buckets)-1]
	b.received += uint64(o.Records) //nolint:gosec // >= 0
	b.lost += o.Lost
	if st.cur.State == "" || st.cur.State == api.StatePendingConfiguration || st.cur.State == api.StateSilent {
		st.dirty = true // primera llegada o recuperación: evaluar ya (≤ 30 s, criterio 1)
	}
}

// Unregistered anota un datagrama de una IP que no es exportador registrado.
func (s *States) Unregistered(ip netip.Addr, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.unreg[ip]
	if u == nil {
		u = &unregistered{first: at}
		s.unreg[ip] = u
	}
	u.last = at
	u.datagrams++
}

// Run evalúa periódicamente hasta que ctx se cancela.
func (s *States) Run(ctx context.Context) {
	t := time.NewTicker(s.opts.Interval)
	defer t.Stop()
	fast := time.NewTicker(time.Second)
	defer fast.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Evaluate(ctx, false)
		case <-fast.C:
			s.Evaluate(ctx, true)
		}
	}
}

type pendingWrite struct {
	key   string
	value api.FlowExporter
	prev  string
	ev    bool
}

// Evaluate recalcula los estados. onlyDirty evalúa solo los que cambiaron
// de forma urgente (primer lote, recuperación).
func (s *States) Evaluate(ctx context.Context, onlyDirty bool) {
	inv := s.inv.Load()
	now := s.now()
	var writes []pendingWrite
	var unreg []netip.Addr
	var unregData []unregistered
	s.mu.Lock()
	for _, exp := range inv.Data().Exporters {
		st := s.get(exp.RouterID)
		if onlyDirty && !st.dirty {
			continue
		}
		if !st.loaded {
			st.loaded = true
			s.mu.Unlock()
			prev := s.load(ctx, exp.RouterID)
			s.mu.Lock()
			if prev != nil {
				st.cur = *prev
				// Contadores acumulados del exportador: siguen desde el KV.
				st.gaps += prev.SequenceGaps
				st.lost += prev.LostRecords
				if w, ok := s.restore(st, now); ok {
					writes = append(writes, w)
				}
			}
		}
		next := s.compute(&exp, st, now)
		changed := next.State != st.cur.State
		if changed || st.dirty || now.Sub(st.lastWrite) >= 30*time.Second || st.cur.Version == 0 {
			prev := st.cur.State
			if changed || st.cur.Version == 0 {
				next.Version = st.cur.Version + 1
				next.StateSince = now
			} else {
				next.Version, next.StateSince = st.cur.Version, st.cur.StateSince
			}
			st.cur, st.dirty, st.lastWrite = next, false, now
			writes = append(writes, pendingWrite{key: exp.RouterID.String(), value: next, prev: prev, ev: changed})
		}
	}
	if !onlyDirty {
		for ip, u := range s.unreg {
			if now.Sub(u.lastEmit) >= 10*time.Minute {
				u.lastEmit = now
				unreg = append(unreg, ip)
				unregData = append(unregData, *u)
			}
			if now.Sub(u.last) > time.Hour {
				delete(s.unreg, ip)
			}
		}
	}
	s.mu.Unlock()
	for _, w := range writes {
		s.persist(ctx, w)
	}
	for i, ip := range unreg {
		s.emitUnregistered(ctx, ip, unregData[i])
	}
}

func (s *States) load(ctx context.Context, router uuid.UUID) *api.FlowExporter {
	if s.kv == nil {
		return nil
	}
	b, err := s.kv.Get(ctx, router.String())
	if err != nil || b == nil {
		return nil
	}
	var fe api.FlowExporter
	if json.Unmarshal(b, &fe) != nil {
		return nil
	}
	return &fe
}

// restore continúa el estado guardado en KV tras un reinicio del collector
// (I1-26): sin él, el exportador volvía a pending_configuration si la
// primera evaluación llegaba antes que su primer datagrama, y una caída del
// propio collector más larga que SilentAfter no dejaba rastro de Silencioso.
// Si el hueco entre el último flujo guardado y el primer datagrama de este
// proceso (o ahora) supera SilentAfter, registra la transición a silent que
// nadie pudo calcular mientras el collector estaba caído.
func (s *States) restore(st *exporterState, now time.Time) (pendingWrite, bool) {
	if st.cur.LastFlowAt == nil {
		return pendingWrite{}, false
	}
	last := *st.cur.LastFlowAt
	if st.lastFlow.IsZero() || st.lastFlow.Before(last) {
		st.lastFlow = last
	}
	if st.cur.State == api.StateSilent || st.cur.State == api.StatePendingConfiguration {
		return pendingWrite{}, false
	}
	end := now
	if !st.firstObs.IsZero() {
		end = st.firstObs
	}
	if end.Sub(last) <= s.opts.SilentAfter {
		return pendingWrite{}, false
	}
	sil := st.cur
	prev := sil.State
	sil.State = api.StateSilent
	sil.Version++
	sil.StateSince = last.Add(s.opts.SilentAfter)
	sil.UpdatedAt = now
	sil.FlowsPerSecond, sil.LossRatio5m = nil, nil
	sil.Hints = []string{api.HintCheckTunnel, api.HintCheckTrafficFlowTarget, api.HintCheckFirewall}
	st.cur = sil
	return pendingWrite{key: sil.RouterID.String(), value: sil, prev: prev, ev: true}, true
}

func f64(v float64) *float64 { return &v }

// compute deriva el estado con histéresis (silent una sola vez; lossy entra
// > umbral y sale < umbral/2).
func (s *States) compute(exp *flowinv.Exporter, st *exporterState, now time.Time) api.FlowExporter {
	ip := exp.TunnelIP.Unmap().String()
	fe := api.FlowExporter{TenantID: exp.TenantID, RouterID: exp.RouterID, SiteID: exp.SiteID,
		ExporterIP: &ip, DroppedRecordsQuota1h: "0", Hints: []string{}, UpdatedAt: now,
		SequenceGaps: st.gaps, LostRecords: st.lost}
	// Ventana de pérdida.
	cut := now.Add(-s.opts.LossWindow)
	kept := st.buckets[:0]
	var recv, lost, recv1m uint64
	for _, b := range st.buckets {
		if b.start.Add(bucketWidth).Before(cut) {
			continue
		}
		kept = append(kept, b)
		recv += b.received
		lost += b.lost
		if now.Sub(b.start) <= time.Minute {
			recv1m += b.received
		}
	}
	st.buckets = kept
	if st.lastFlow.IsZero() {
		fe.State = api.StatePendingConfiguration
		fe.Hints = append(fe.Hints, api.HintCheckTrafficFlowTarget, api.HintCheckTunnel)
		return fe
	}
	lf := st.lastFlow
	fe.LastFlowAt = &lf
	src := st.flowSource
	fe.FlowSource = &src
	sr := 1
	if st.sampling > 1 {
		sr = int(st.sampling)
	}
	fe.SamplingRate = &sr
	if st.hasSkew {
		fe.ClockSkewSeconds = f64(math.Round(st.lastSkew.Seconds()*1000) / 1000)
	}
	ratio := 0.0
	if recv+lost > 0 {
		ratio = float64(lost) / float64(recv+lost)
	}
	fe.LossRatio5m = f64(ratio)
	fe.FlowsPerSecond = f64(math.Round(float64(recv1m)/60*10) / 10)
	switch {
	case now.Sub(st.lastFlow) > s.opts.SilentAfter:
		fe.State = api.StateSilent
		fe.FlowsPerSecond, fe.LossRatio5m = nil, nil
		fe.Hints = append(fe.Hints, api.HintCheckTunnel, api.HintCheckTrafficFlowTarget, api.HintCheckFirewall)
	case st.hasSkew && math.Abs(st.lastSkew.Seconds()) > s.opts.ClockSkew.Seconds():
		fe.State = api.StateClockSkew
		fe.Hints = append(fe.Hints, api.HintCheckNTP)
	case ratio > s.opts.LossThreshold || (st.cur.State == api.StateLossy && ratio > s.opts.LossThreshold/2):
		fe.State = api.StateLossy
	default:
		fe.State = api.StateExporting
	}
	return fe
}

func (s *States) persist(ctx context.Context, w pendingWrite) {
	if s.kv != nil {
		if b, err := json.Marshal(w.value); err == nil {
			if err := s.kv.Put(ctx, w.key, b); err != nil {
				s.log.Warn("exporter state not stored", "router_id", w.key, "error", err)
			}
		}
	}
	if !w.ev || s.sink == nil {
		return
	}
	fe := w.value
	tenant := fe.TenantID
	prev := w.prev
	if prev == "" {
		prev = api.StatePendingConfiguration
	}
	if prev == fe.State {
		return
	}
	data := map[string]any{
		"router_id": fe.RouterID, "site_id": fe.SiteID, "version": fe.Version,
		"previous_state": prev, "state": fe.State, "changed_at": fe.StateSince.UTC().Format(flowbus.TimeFormat),
	}
	if fe.LastFlowAt != nil {
		data["last_flow_at"] = fe.LastFlowAt.UTC().Format(flowbus.TimeFormat)
	}
	if fe.LossRatio5m != nil {
		data["loss_ratio_5m"] = *fe.LossRatio5m
	}
	if fe.ClockSkewSeconds != nil {
		data["clock_skew_seconds"] = *fe.ClockSkewSeconds
	}
	events := []flowbus.Event{{Type: flowbus.TypeExporterStateChanged, Data: data}}
	silence := map[string]any{"router_id": fe.RouterID, "exporter_ip": fe.ExporterIP,
		"silent_after_seconds": int(s.opts.SilentAfter.Seconds())}
	if fe.LastFlowAt != nil {
		silence["last_flow_at"] = fe.LastFlowAt.UTC().Format(flowbus.TimeFormat)
	}
	switch {
	case fe.State == api.StateSilent:
		events = append(events, flowbus.Event{Type: flowbus.TypeExporterSilent, Data: silence})
	case prev == api.StateSilent:
		events = append(events, flowbus.Event{Type: flowbus.TypeExporterRecovered, Data: silence})
	}
	for _, e := range events {
		e.Source, e.Entity, e.TenantID = "horus/flows/collector", fe.RouterID.String(), &tenant
		e.AggregateType, e.AggregateVersion, e.Time = "router", fe.Version, fe.StateSince
		m, err := e.Msg()
		if err == nil {
			err = s.sink.PublishMsg(ctx, m)
		}
		if err != nil {
			s.log.Warn("exporter event not published", "type", e.Type, "router_id", fe.RouterID, "error", err)
		}
	}
}

func (s *States) emitUnregistered(ctx context.Context, ip netip.Addr, u unregistered) {
	if s.sink == nil {
		return
	}
	e := flowbus.Event{Type: flowbus.TypeExporterUnregistered, Source: "horus/flows/collector",
		Entity:        uuid.NewSHA1(uuid.NameSpaceOID, []byte("horus-exporter:"+ip.String())).String(),
		AggregateType: "exporter", AggregateVersion: 1, Time: u.last,
		Data: map[string]any{"exporter_ip": ip.String(), "first_seen_at": u.first.UTC().Format(flowbus.TimeFormat),
			"last_seen_at": u.last.UTC().Format(flowbus.TimeFormat), "datagrams": uintString(u.datagrams)}}
	m, err := e.Msg()
	if err == nil {
		err = s.sink.PublishMsg(ctx, m)
	}
	if err != nil {
		s.log.Warn("exporter.unregistered not published", "error", err)
	}
}

// Snapshot devuelve el último estado calculado de cada router (tests y diagnóstico).
func (s *States) Snapshot() []api.FlowExporter {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]api.FlowExporter, 0, len(s.byID))
	for _, st := range s.byID {
		if st.cur.State != "" {
			out = append(out, st.cur)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RouterID.String() < out[j].RouterID.String() })
	return out
}
