package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

// AllowEntry es una entrada de la allowlist del ISP.
type AllowEntry struct {
	Prefix *netip.Prefix
	ASN    *uint32
	Kinds  []string
}

// Matches indica si la entrada cubre el kind y la dirección o el ASN.
func (a AllowEntry) Matches(kind string, addr netip.Addr, asn uint32) bool {
	if len(a.Kinds) > 0 && !slices.Contains(a.Kinds, kind) {
		return false
	}
	if a.Prefix != nil && addr.IsValid() && a.Prefix.Contains(addr.Unmap()) {
		return true
	}
	return a.ASN != nil && asn != 0 && *a.ASN == asn
}

// Sink es la capa de hallazgos (app): configuración por ISP y aplicación de
// candidatos (deduplicación, silencio, reincidencia, eventos).
type Sink interface {
	Params(ctx context.Context, tenant uuid.UUID) (domain.Params, error)
	Allowlist(ctx context.Context, tenant uuid.UUID) ([]AllowEntry, error)
	Apply(ctx context.Context, tenant uuid.UUID, cands []domain.Candidate, now time.Time) (ApplyResult, error)
	Expire(ctx context.Context, tenant uuid.UUID, idle time.Duration, now time.Time) (int, error)
	State(ctx context.Context, tenant uuid.UUID, key string) (string, error)
	SetState(ctx context.Context, tenant uuid.UUID, key, value string) error
}

// ApplyResult resume lo que hizo Apply.
type ApplyResult struct {
	Opened, Updated, Unchanged, Silenced int
}

// Reputation da el snapshot de reputación vigente (puede ser nil).
type Reputation interface {
	Current() *reputation.Snapshot
}

// Options del motor.
type Options struct {
	// Lag: margen para flujos tardíos (active timeout 1 min + ingesta).
	Lag    time.Duration
	Logger *slog.Logger
	// MaxCatchup acota cuánto hacia atrás recupera el motor las ventanas no
	// evaluadas tras una parada (por defecto 24 h; más allá se registra el
	// hueco y se empieza desde ahí).
	MaxCatchup time.Duration
	// CatchupSteps es el máximo de ventanas atrasadas por detector y pasada
	// (por defecto 24): la recuperación avanza en varias pasadas sin
	// bloquear al resto de tenants.
	CatchupSteps int
}

// stateWindowPrefix es la clave de engine_state con el fin de la última
// ventana evaluada (y aplicada) de cada detector: "window:<detector>" →
// segundos Unix. Sobrevive a reinicios: el motor retoma desde ahí y evalúa
// las ventanas perdidas mientras el rol estuvo parado (D23).
const stateWindowPrefix = "window:"

// Engine evalúa los detectores por tenant.
type Engine struct {
	sig  Signals
	sink Sink
	rep  Reputation
	lag  time.Duration
	log  *slog.Logger
	mu   sync.Mutex
	last map[string]time.Time // tenant|detector → fin de la última ventana evaluada (caché de engine_state)

	maxCatchup   time.Duration
	catchupSteps int
}

// New crea el motor.
func New(sig Signals, sink Sink, rep Reputation, o Options) *Engine {
	if o.Lag <= 0 {
		o.Lag = 2 * time.Minute
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.MaxCatchup <= 0 {
		o.MaxCatchup = 24 * time.Hour
	}
	if o.CatchupSteps <= 0 {
		o.CatchupSteps = 24
	}
	return &Engine{sig: sig, sink: sink, rep: rep, lag: o.Lag, log: o.Logger, last: map[string]time.Time{},
		maxCatchup: o.MaxCatchup, catchupSteps: o.CatchupSteps}
}

// lastEnd devuelve el fin de la última ventana aplicada de un detector: de
// la caché o, tras un arranque, de engine_state (cero si nunca se evaluó).
func (en *Engine) lastEnd(ctx context.Context, tenant uuid.UUID, det string) (time.Time, error) {
	key := tenant.String() + "|" + det
	en.mu.Lock()
	last, ok := en.last[key]
	en.mu.Unlock()
	if ok {
		return last, nil
	}
	v, err := en.sink.State(ctx, tenant, stateWindowPrefix+det)
	if err != nil {
		return time.Time{}, fmt.Errorf("window state: %w", err)
	}
	if v != "" {
		if sec, err := strconv.ParseInt(v, 10, 64); err == nil && sec > 0 {
			last = time.Unix(sec, 0).UTC()
		}
	}
	en.mu.Lock()
	en.last[key] = last
	en.mu.Unlock()
	return last, nil
}

// windowEnds decide qué ventanas evalúa un detector en esta pasada: ninguna
// si no le toca por cadencia; la actual si la última evaluada la cubre; y,
// tras una parada más larga que la ventana, las atrasadas paso a paso
// (cadencia) hasta CatchupSteps, empezando como mucho MaxCatchup atrás.
func (en *Engine) windowEnds(last, end time.Time, window, cadence time.Duration, force bool) (ends []time.Time, skipped time.Duration) {
	switch {
	case force || last.IsZero():
		return []time.Time{end}, 0
	case !end.After(last) || end.Sub(last) < cadence:
		return nil, 0
	case end.Sub(last) <= window:
		return []time.Time{end}, 0
	}
	if cadence <= 0 {
		cadence = window
	}
	start := last
	if end.Sub(start) > en.maxCatchup {
		skipped = end.Sub(start) - en.maxCatchup
		start = end.Add(-en.maxCatchup)
	}
	for e := start.Add(cadence); e.Before(end) && len(ends) < en.catchupSteps; e = e.Add(cadence) {
		ends = append(ends, e)
	}
	if len(ends) < en.catchupSteps {
		ends = append(ends, end)
	}
	return ends, skipped
}

// Report es el resultado de evaluar un tenant.
type Report struct {
	Candidates []domain.Candidate
	Applied    ApplyResult
	Expired    int
	Skipped    map[string]int // motivo → candidatos descartados
	Errors     []error
}

// detector es una función de detección sobre una ventana.
type detector struct {
	name    string
	enabled func(p *domain.Params) bool
	window  func(p *domain.Params) time.Duration
	cadence func(p *domain.Params) time.Duration
	run     func(ctx context.Context, e *env) ([]domain.Candidate, error)
}

var detectors = []detector{
	{domain.DetC2, func(p *domain.Params) bool { return p.C2.Enabled }, func(p *domain.Params) time.Duration { return p.C2.Lookback.D() },
		func(*domain.Params) time.Duration { return 5 * time.Minute }, detectC2},
	{domain.DetScan, func(p *domain.Params) bool { return p.Scan.Enabled || p.Fanout.Enabled || p.WatchPorts.Enabled },
		func(p *domain.Params) time.Duration { return p.Scan.Window.D() }, func(p *domain.Params) time.Duration { return p.Scan.Window.D() }, detectOutbound},
	{domain.DetSMTP, func(p *domain.Params) bool { return p.SMTP.Enabled }, func(p *domain.Params) time.Duration { return p.SMTP.Window.D() },
		func(*domain.Params) time.Duration { return 5 * time.Minute }, detectSMTP},
	{domain.DetDDoS, func(p *domain.Params) bool { return p.DDoS.Enabled }, func(p *domain.Params) time.Duration { return p.DDoS.Window.D() },
		func(p *domain.Params) time.Duration { return p.DDoS.Window.D() }, detectDDoS},
	{domain.DetBeacon, func(p *domain.Params) bool { return p.Beacon.Enabled }, func(p *domain.Params) time.Duration { return p.Beacon.Window.D() },
		func(p *domain.Params) time.Duration { return p.Beacon.Interval.D() }, detectBeacon},
	{domain.DetSustained, func(p *domain.Params) bool { return p.Sustained.Enabled }, func(p *domain.Params) time.Duration { return p.Sustained.Window.D() },
		func(*domain.Params) time.Duration { return 5 * time.Minute }, detectSustained},
}

// env es el contexto de una evaluación.
type env struct {
	tenant   uuid.UUID
	p        *domain.Params
	from, to time.Time
	sig      Signals
	rep      *reputation.Snapshot
	sink     Sink
	cmu      sync.Mutex
	cust     map[ClientKey]Customer
}

// customers resuelve (con caché) el cliente de cada clave.
func (e *env) customers(ctx context.Context, keys []ClientKey) (map[ClientKey]Customer, error) {
	e.cmu.Lock()
	defer e.cmu.Unlock()
	var miss []ClientKey
	for _, k := range keys {
		if _, ok := e.cust[k]; !ok && !slices.Contains(miss, k) {
			miss = append(miss, k)
		}
	}
	if len(miss) > 0 {
		got, err := e.sig.Customers(ctx, e.tenant, miss)
		if err != nil {
			return nil, err
		}
		for _, k := range miss {
			c, ok := got[k]
			if !ok {
				// devices aún no lo ha proyectado en dim.customer: mismo id
				// estable que usa analytics (UUIDv5 de la clave natural).
				c = Customer{ID: DerivedCustomerID(e.tenant, k), Kind: "residential"}
			}
			e.cust[k] = c
		}
	}
	out := make(map[ClientKey]Customer, len(keys))
	for _, k := range keys {
		out[k] = e.cust[k]
	}
	return out, nil
}

// DerivedCustomerID es el id estable de un cliente aún no proyectado por
// devices (el mismo que usa analytics: UUIDv5(tenant, "realm|ip")).
func DerivedCustomerID(tenant uuid.UUID, k ClientKey) uuid.UUID {
	return uuid.NewSHA1(tenant, []byte(k.Realm.String()+"|"+k.IP.Unmap().String()))
}

// Evaluate ejecuta los detectores que tocan para tenant en now. force
// evalúa todos sin mirar su cadencia (tests, reevaluación manual).
func (en *Engine) Evaluate(ctx context.Context, tenant uuid.UUID, now time.Time, force bool) (*Report, error) {
	p, err := en.sink.Params(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}
	end := now.UTC().Add(-en.lag).Truncate(time.Minute)
	var snap *reputation.Snapshot
	if en.rep != nil {
		snap = en.rep.Current()
	}
	rep := &Report{Skipped: map[string]int{}}
	cache := map[ClientKey]Customer{}
	// done: ventanas evaluadas en esta pasada; se guardan (memoria y
	// engine_state) solo cuando sus candidatos ya están aplicados, para que
	// un fallo o un kill -9 entre la evaluación y Apply no pierda hallazgos.
	done := map[string]time.Time{}
	for _, d := range detectors {
		if !d.enabled(&p) {
			continue
		}
		last, err := en.lastEnd(ctx, tenant, d.name)
		if err != nil {
			rep.Errors = append(rep.Errors, fmt.Errorf("%s: %w", d.name, err))
			continue
		}
		ends, skipped := en.windowEnds(last, end, d.window(&p), d.cadence(&p), force)
		if skipped > 0 {
			en.log.WarnContext(ctx, "detection catch-up limited: older windows not evaluated", slog.String("detector", d.name),
				slog.String("tenant", tenant.String()), slog.Duration("skipped", skipped))
		}
		if len(ends) > 1 {
			en.log.InfoContext(ctx, "detection catching up missed windows", slog.String("detector", d.name),
				slog.String("tenant", tenant.String()), slog.Int("windows", len(ends)), slog.Time("from", last))
		}
		for _, wEnd := range ends {
			e := &env{tenant: tenant, p: &p, from: wEnd.Add(-d.window(&p)), to: wEnd, sig: en.sig, rep: snap, sink: en.sink, cust: cache}
			cands, err := d.run(ctx, e)
			if err != nil {
				rep.Errors = append(rep.Errors, fmt.Errorf("%s: %w", d.name, err))
				en.log.WarnContext(ctx, "detector failed", slog.String("detector", d.name), slog.String("tenant", tenant.String()), slog.Any("err", err))
				break
			}
			for i := range cands {
				cands[i].Detector = d.name
			}
			rep.Candidates = append(rep.Candidates, cands...)
			// Una evaluación forzada (tests, reevaluación manual en un
			// instante arbitrario) no mueve el calendario; el avance nunca
			// retrocede.
			if !force && wEnd.After(last) {
				done[d.name] = wEnd
			}
		}
	}
	var retroState string
	// Barrido retroactivo de C2 (nuevo snapshot o cada retro_interval).
	if p.C2.Enabled && snap != nil {
		cands, st, err := en.retroSweep(ctx, tenant, &p, snap, end, force, cache)
		retroState = st
		if err != nil {
			rep.Errors = append(rep.Errors, fmt.Errorf("c2 retro: %w", err))
		}
		rep.Candidates = append(rep.Candidates, cands...)
	}
	rep.Candidates = dedupe(rep.Candidates)
	cands, err := en.complete(ctx, tenant, rep, cache, end)
	if err != nil {
		return rep, err
	}
	rep.Candidates = cands
	if len(cands) > 0 {
		res, err := en.sink.Apply(ctx, tenant, cands, now)
		if err != nil {
			return rep, fmt.Errorf("apply: %w", err)
		}
		rep.Applied = res
	}
	if err := en.commitWindows(ctx, tenant, done, retroState); err != nil {
		return rep, err
	}
	if p.AutoExpire.D() > 0 {
		n, err := en.sink.Expire(ctx, tenant, p.AutoExpire.D(), now)
		if err != nil {
			return rep, fmt.Errorf("expire: %w", err)
		}
		rep.Expired = n
	}
	return rep, errors.Join(rep.Errors...)
}

// commitWindows guarda el avance de las ventanas aplicadas (y del barrido
// retroactivo) en engine_state y en la caché.
func (en *Engine) commitWindows(ctx context.Context, tenant uuid.UUID, done map[string]time.Time, retroState string) error {
	for det, wEnd := range done {
		if err := en.sink.SetState(ctx, tenant, stateWindowPrefix+det, strconv.FormatInt(wEnd.Unix(), 10)); err != nil {
			return fmt.Errorf("window state: %w", err)
		}
		en.mu.Lock()
		en.last[tenant.String()+"|"+det] = wEnd
		en.mu.Unlock()
	}
	if retroState != "" {
		if err := en.sink.SetState(ctx, tenant, retroStateKey, retroState); err != nil {
			return fmt.Errorf("retro state: %w", err)
		}
	}
	return nil
}

// complete resuelve cliente y router, aplica muestreo y la allowlist del ISP.
func (en *Engine) complete(ctx context.Context, tenant uuid.UUID, rep *Report, cache map[ClientKey]Customer, end time.Time) ([]domain.Candidate, error) {
	if len(rep.Candidates) == 0 {
		return nil, nil
	}
	allow, err := en.sink.Allowlist(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("allowlist: %w", err)
	}
	var keys, unlocated []ClientKey
	from := end
	for _, c := range rep.Candidates {
		k := ClientKey{Realm: c.RealmID, IP: c.Client.Unmap()}
		keys = append(keys, k)
		if c.RouterID == uuid.Nil || c.SiteID == uuid.Nil {
			unlocated = append(unlocated, k)
			if c.WindowFrom.Before(from) {
				from = c.WindowFrom
			}
		}
	}
	e := &env{tenant: tenant, sig: en.sig, cust: cache}
	custs, err := e.customers(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("customers: %w", err)
	}
	locs := map[ClientKey]Location{}
	if len(unlocated) > 0 {
		if locs, err = en.sig.Locate(ctx, tenant, from, end.Add(time.Minute), unlocated); err != nil {
			return nil, fmt.Errorf("locate: %w", err)
		}
	}
	var out []domain.Candidate
	for _, c := range rep.Candidates {
		k := ClientKey{Realm: c.RealmID, IP: c.Client.Unmap()}
		cu := custs[k]
		c.CustomerID, c.CustomerKind = cu.ID, cu.Kind
		if l, ok := locs[k]; ok {
			if c.SiteID == uuid.Nil {
				c.SiteID = l.Site
			}
			if c.RouterID == uuid.Nil {
				c.RouterID = l.Router
			}
		}
		if c.SiteID == uuid.Nil && cu.Site != uuid.Nil {
			c.SiteID = cu.Site
		}
		if c.SiteID == uuid.Nil || c.RouterID == uuid.Nil {
			rep.Skipped["unlocated"]++
			continue
		}
		if allowed(allow, &c) {
			rep.Skipped["allowlisted"]++
			continue
		}
		c.ApplySampling()
		c.Confidence = domain.Clamp(c.Confidence)
		out = append(out, c)
	}
	return out, nil
}

// dedupe deja un candidato por (cliente, kind, objetivo): el de última vez
// más reciente (p. ej. marca en ingesta y barrido retroactivo del mismo C2).
func dedupe(in []domain.Candidate) []domain.Candidate {
	type k struct {
		realm  uuid.UUID
		ip     netip.Addr
		kind   string
		target domain.Target
	}
	idx := map[k]int{}
	var out []domain.Candidate
	for _, c := range in {
		key := k{c.RealmID, c.Client.Unmap(), c.Kind, c.Target}
		if i, ok := idx[key]; ok {
			if c.LastSeen.After(out[i].LastSeen) {
				out[i] = c
			}
			continue
		}
		idx[key] = len(out)
		out = append(out, c)
	}
	return out
}

func allowed(list []AllowEntry, c *domain.Candidate) bool {
	for _, a := range list {
		if a.Matches(c.Kind, c.Client, 0) || a.Matches(c.Kind, c.RemoteIP, c.RemoteASN) {
			return true
		}
	}
	return false
}

// keysOf devuelve las claves sin repetir.
func keysOf[T any](rows []T, key func(T) ClientKey) []ClientKey {
	seen := map[ClientKey]bool{}
	var out []ClientKey
	for _, r := range rows {
		k := key(r)
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// fmtInt formatea con separador de miles (espacio fino, estilo del ejemplo C8).
func fmtInt[T ~int | ~int64 | ~uint64 | ~uint32](n T) string {
	s := strconv.FormatInt(int64(n), 10)
	if len(s) <= 4 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func minutes(d time.Duration) int { return int(d.Round(time.Minute) / time.Minute) }

func portsList(ps []PortStat, limit int) []int {
	out := make([]int, 0, len(ps))
	for _, p := range ps {
		if len(out) == limit {
			break
		}
		out = append(out, int(p.Port))
	}
	return out
}

func ipStr(a netip.Addr) string { return a.Unmap().String() }

func maxU32(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}
