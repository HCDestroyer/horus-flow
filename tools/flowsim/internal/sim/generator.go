package sim

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"net/netip"
	"strings"
	"time"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/capture"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/expect"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/export"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/flow"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/signals"
)

// Options ajusta una ejecución del generador. Los punteros nil usan el valor
// del escenario.
type Options struct {
	Seed     uint64
	Protocol flow.Protocol
	Rate     float64       // registros/s del tráfico de fondo; 0 = escenario
	Duration time.Duration // 0 = escenario
	NAT      *bool
	IPv6     *bool
	// Start fija el instante simulado de arranque; cero = start del escenario.
	Start time.Time
	// Fixture usa la duración y tasa reducidas del bloque fixture.
	Fixture bool
	// NATFields añade los campos post-NAT (IE 225-228) en IPFIX.
	NATFields bool
	// CollectorPort; 0 = puerto estándar del protocolo.
	CollectorPort uint16
	// MaxDatagram; 0 = escenario.
	MaxDatagram int
	// AllowUnmet no falla si las señales calculadas no coinciden con las
	// declaradas por el escenario (solo avisa en Expected.Warnings).
	AllowUnmet bool
}

// Emit recibe cada datagrama generado, en orden cronológico.
type Emit func(d capture.Datagram, exporter int) error

const tickMs = 1000

type pending struct {
	rec flow.Record
	at  signals.Attribution
}

type expState struct {
	idx       int
	spec      *ExporterSpec
	v4        PrefixSet
	v6        PrefixSet
	exp       *export.Exporter
	clients   []*client
	byPop     map[string][]*client
	buckets   map[int64][]pending
	rng       *rand.Rand
	routerMAC [6]byte
	wanMAC    [6]byte
	gwMAC     [6]byte
	srcPort   uint16
	tally     *expect.Tally
	router    *routerBehavior
}

type gen struct {
	sc        *Scenario
	opt       Options
	nat, ipv6 bool
	start     time.Time
	durMs     int64
	activeMs  int64
	inactMs   int64
	rate      float64
	exps      []*expState
	acc       *signals.Accumulator
	pendingN  int
	collPort  uint16
	indicator map[netip.Addr]bool
}

func streamSeed(parts ...string) uint64 {
	h := fnv.New64a()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

// Run ejecuta el escenario y llama a emit con cada datagrama. Devuelve el
// expected.json calculado sobre lo realmente emitido.
func Run(ctx context.Context, sc *Scenario, opt Options, emit Emit) (*Expected, error) {
	g, err := newGen(sc, opt)
	if err != nil {
		return nil, err
	}
	if err := g.loop(ctx, emit); err != nil {
		return nil, err
	}
	return g.expected()
}

func newGen(sc *Scenario, opt Options) (*gen, error) {
	if opt.Protocol == 0 {
		opt.Protocol = flow.IPFIX
	}
	g := &gen{sc: sc, opt: opt, nat: sc.NAT, ipv6: sc.IPv6, rate: sc.Rate}
	if opt.NAT != nil {
		g.nat = *opt.NAT
	}
	if opt.IPv6 != nil {
		g.ipv6 = *opt.IPv6
	}
	dur := sc.Duration.D()
	if opt.Fixture {
		if sc.Fixture.Duration > 0 {
			dur = sc.Fixture.Duration.D()
		}
		if sc.Fixture.Rate > 0 {
			g.rate = sc.Fixture.Rate
		}
	}
	if opt.Duration > 0 {
		dur = opt.Duration
	}
	if opt.Rate > 0 {
		g.rate = opt.Rate
	}
	if dur < time.Second {
		return nil, errors.New("la duración debe ser >= 1s")
	}
	g.durMs = dur.Milliseconds()
	g.start = sc.Start.UTC().Truncate(time.Second)
	if !opt.Start.IsZero() {
		g.start = opt.Start.UTC().Truncate(time.Second)
	}
	g.activeMs = sc.Export.ActiveTimeout.D().Milliseconds()
	g.inactMs = sc.Export.InactiveTimeout.D().Milliseconds()
	g.collPort = opt.CollectorPort
	if g.collPort == 0 {
		g.collPort = opt.Protocol.DefaultPort()
	}
	maxDgram := sc.Export.MaxDatagram
	if opt.MaxDatagram > 0 {
		maxDgram = opt.MaxDatagram
	}

	inds := make([]signals.Indicator, 0, len(sc.Indicators))
	g.indicator = map[netip.Addr]bool{}
	for _, i := range sc.Indicators {
		inds = append(inds, signals.Indicator{IP: i.IP, Kind: i.Kind, Confidence: i.Confidence, Source: "flowsim-test-feed"})
		g.indicator[i.IP] = true
	}
	g.acc = signals.New(signals.Config{Start: g.start, Duration: dur, Indicators: inds})

	seedStr := fmt.Sprint(opt.Seed)
	for i := range sc.Exporters {
		spec := &sc.Exporters[i]
		es := &expState{
			idx:     i,
			spec:    spec,
			v4:      spec.Prefixes.Public,
			v6:      spec.Prefixes.V6,
			byPop:   map[string][]*client{},
			buckets: map[int64][]pending{},
			rng:     rand.New(rand.NewPCG(opt.Seed, streamSeed(seedStr, spec.Name, "router"))),
			srcPort: uint16(49152 + 7*i),
			tally:   expect.NewTally(),
		}
		if g.nat {
			es.v4 = spec.Prefixes.NAT
		}
		es.routerMAC = [6]byte{mikrotikOUI[0], mikrotikOUI[1], mikrotikOUI[2], 0x10, byte(i), 0x0c}
		es.wanMAC = [6]byte{mikrotikOUI[0], mikrotikOUI[1], mikrotikOUI[2], 0x10, byte(i), 0x01}
		es.gwMAC = [6]byte{0x00, 0x1c, 0x73, 0x20, byte(i), 0x01}
		es.exp = export.New(export.Config{
			Protocol:          opt.Protocol,
			ObservationDomain: spec.ObservationDomainID,
			Boot:              g.start.Add(-spec.Uptime.D()),
			TemplateRefresh:   sc.Export.TemplateRefresh,
			TemplateTimeout:   sc.Export.TemplateTimeout.D(),
			MaxDatagram:       maxDgram,
			Templates:         flow.TemplateOptions{NATFields: opt.NATFields},
		})
		if spec.RouterTraffic {
			es.router = &routerBehavior{}
		}
		g.exps = append(g.exps, es)
	}
	for _, es := range g.exps {
		if err := g.buildClients(es); err != nil {
			return nil, fmt.Errorf("%s: %w", es.spec.Name, err)
		}
	}
	g.calibrate()
	return g, nil
}

// client es un cliente simulado (una IPv4 y, opcionalmente, un prefijo IPv6).
type client struct {
	exp        *expState
	pop        *Population
	idx        int
	status     string
	v4         netip.Addr
	v6         []netip.Addr
	keyV4      string
	keyV6      string
	kind       string
	rng        *rand.Rand
	mac        [6]byte
	ttl64      float64 // probabilidad de TTL inicial 64 (resto 128)
	accessIf   uint32
	behaviors  []behavior
	weight     float64 // peso en el tráfico de fondo
	lambda     float64 // conexiones de fondo por segundo
	bgKind     string
	remotes    map[string][]netip.Addr
	heavyUntil []int64
}

func (g *gen) buildClients(es *expState) error {
	spec := es.spec
	v4skip := append(append([]netip.Prefix{}, es.v4.Infrastructure...), es.v4.Excluded...)
	allocs := map[string]*v4Allocator{
		"customers": {prefixes: es.v4.Customers, skip: v4skip},
		"excluded":  {prefixes: es.v4.Excluded},
		"unlisted":  {prefixes: es.v4.Unlisted},
	}
	v6allocs := map[string]*v6Allocator{
		"customers": {prefixes: es.v6.Customers, clientLen: spec.IPv6ClientLen},
		"excluded":  {prefixes: es.v6.Excluded, clientLen: spec.IPv6ClientLen},
		"unlisted":  {prefixes: es.v6.Unlisted, clientLen: spec.IPv6ClientLen},
	}
	seedStr := fmt.Sprint(g.opt.Seed)
	n := 0
	for pi := range spec.Populations {
		pop := &spec.Populations[pi]
		rng := pop.Range
		if rng == "" {
			rng = "customers"
		}
		status := signals.StatusAttributed
		switch rng {
		case "excluded":
			status = signals.StatusExcluded
		case "unlisted":
			status = signals.StatusUnknown
		}
		if other, ok := strings.CutPrefix(rng, "customers@"); ok {
			status = signals.StatusTransit
			if _, exists := allocs[rng]; !exists {
				oe := g.exporterByName(other)
				ov4 := oe.v4
				allocs[rng] = &v4Allocator{prefixes: ov4.Customers, skip: append(append([]netip.Prefix{}, ov4.Infrastructure...), ov4.Excluded...)}
				// Desplazado para no coincidir con los clientes propios del otro nodo.
				v6allocs[rng] = &v6Allocator{prefixes: oe.v6.Customers, clientLen: oe.spec.IPv6ClientLen, idx: 1 << 16}
			}
		}
		for k := 0; k < pop.Count; k++ {
			c := &client{
				exp:    es,
				pop:    pop,
				idx:    n,
				status: status,
				rng:    rand.New(rand.NewPCG(g.opt.Seed, streamSeed(seedStr, spec.Name, pop.Name, fmt.Sprint(k)))),
			}
			n++
			wantV4 := pop.Family != "v6"
			// Reparto exacto y uniforme: round(count × ipv6_share) clientes doble pila.
			dual := math.Floor(float64(k+1)*pop.IPv6Share+1e-9) > math.Floor(float64(k)*pop.IPv6Share+1e-9)
			wantV6 := pop.Family == "v6" || (g.ipv6 && dual)
			if pop.Family == "v6" && !g.ipv6 {
				continue // población solo IPv6 con IPv6 desactivado
			}
			if wantV4 {
				a, err := allocs[rng].next()
				if err != nil {
					return fmt.Errorf("%s: %w", pop.Name, err)
				}
				if status == signals.StatusTransit && containsAny(es.v4.Customers, a) {
					return fmt.Errorf("%s: la IPv4 de tránsito %s cae en prefijos propios (usa family: v6)", pop.Name, a)
				}
				c.v4 = a
				c.keyV4 = clientKey(a, spec.IPv6ClientLen)
			}
			if wantV6 {
				p, err := v6allocs[rng].next()
				if err != nil {
					return fmt.Errorf("%s: %w", pop.Name, err)
				}
				addrs := 1 + c.rng.IntN(2)
				for j := 0; j < addrs; j++ {
					c.v6 = append(c.v6, randomIn(c.rng, p))
				}
				c.keyV6 = p.Masked().String()
			}
			c.kind = pop.Kind
			ouis := residentialOUI
			c.ttl64 = 0.7
			for _, b := range pop.Behaviors {
				if b.Kind == "commercial" {
					ouis = commercialOUI
					c.ttl64 = 0.4
					if c.kind == "" {
						c.kind = "commercial"
					}
				}
			}
			if c.kind == "" {
				c.kind = "residential"
			}
			c.mac = randomMAC(c.rng, ouis)
			c.accessIf = spec.Interfaces.AccessIfIndex
			if spec.Interfaces.Access == "pppoe" {
				c.accessIf = spec.Interfaces.PPPoEBase + uint32(c.idx)
			}
			if status == signals.StatusTransit {
				c.accessIf = spec.Interfaces.Transit
			}
			for _, b := range pop.Behaviors {
				bh, err := newBehavior(b, c)
				if err != nil {
					return fmt.Errorf("%s: %w", pop.Name, err)
				}
				c.behaviors = append(c.behaviors, bh)
			}
			es.clients = append(es.clients, c)
			es.byPop[pop.Name] = append(es.byPop[pop.Name], c)
		}
	}
	return nil
}

func (g *gen) exporterByName(name string) *expState {
	for _, e := range g.exps {
		if e.spec.Name == name {
			return e
		}
	}
	return nil
}

// calibrate reparte la tasa de fondo entre los clientes según su peso y el
// nº medio de registros por conexión de su perfil.
func (g *gen) calibrate() {
	var total float64
	for _, es := range g.exps {
		for _, c := range es.clients {
			total += c.weight
		}
	}
	if total == 0 {
		return
	}
	perKind := map[string]float64{}
	for _, es := range g.exps {
		for _, c := range es.clients {
			if c.weight == 0 {
				continue
			}
			rpc, ok := perKind[c.bgKind]
			if !ok {
				rpc = g.recordsPerConn(c.bgKind)
				perKind[c.bgKind] = rpc
			}
			c.lambda = g.rate * c.weight / total / rpc
		}
	}
}

// --- Emulación de la caché de Traffic Flow ------------------------------

type flagMode uint8

const (
	flagsNone    flagMode = iota // UDP/ICMP
	flagsSession                 // sesión TCP completa
	flagsSynOnly                 // SYN sin respuesta
	flagsRstAck                  // puerto cerrado
	flagsSynRst                  // escaneo SYN que corta con RST
	flagsSynAck                  // respuesta SYN+ACK de un puerto abierto
)

func sessionFlags(first, last bool) uint8 {
	f := flow.ACK | flow.PSH
	if first {
		f |= flow.SYN
	}
	if last {
		f |= flow.FIN
	}
	return f
}

func ceilDiv(a, b int64) int64 {
	if a <= 0 {
		return a / b
	}
	return (a + b - 1) / b
}

// segment es un registro exportado de un flujo.
type segment struct {
	first, last int64
	pkts, bytes uint64
	flags       uint8
	exportAt    int64
}

// segments emula la caché de RouterOS: un flujo con paquetes uniformes entre
// start y end se exporta cada active ms desde su creación y, tras el último
// paquete, al expirar el inactive timeout. Devuelve los registros.
func segments(start, end int64, pkts, bytes uint64, fm flagMode, activeMs, inactMs int64) []segment {
	if pkts == 0 {
		return nil
	}
	if pkts == 1 || end <= start {
		end = start
	}
	dur := end - start
	n := int64(pkts)
	tAt := func(i int64) int64 {
		if n == 1 {
			return start
		}
		return start + i*dur/(n-1)
	}
	firstAtOrAfter := func(x int64) int64 { // primer índice con t >= x
		if x <= start {
			return 0
		}
		if dur == 0 {
			return n
		}
		i := ceilDiv((x-start)*(n-1), dur)
		for i > 0 && tAt(i-1) >= x {
			i--
		}
		for i < n && tAt(i) < x {
			i++
		}
		return i
	}
	base, rem := bytes/pkts, bytes%pkts
	bytesIn := func(i, j int64) uint64 { // paquetes [i, j)
		b := uint64(j-i) * base
		lo, hi := min(uint64(i), rem), min(uint64(j), rem)
		return b + hi - lo
	}
	var out []segment
	i := int64(0)
	for i < n {
		segStart := tAt(i)
		j := firstAtOrAfter(segStart + activeMs)
		final := j >= n
		if final {
			j = n
		}
		s := segment{first: segStart, last: tAt(j - 1), pkts: uint64(j - i), bytes: bytesIn(i, j)}
		if final {
			s.exportAt = s.last + inactMs
		} else {
			s.exportAt = segStart + activeMs
		}
		switch fm {
		case flagsSession:
			s.flags = sessionFlags(i == 0, final)
		case flagsSynOnly:
			s.flags = flow.SYN
		case flagsRstAck:
			s.flags = flow.RST | flow.ACK
		case flagsSynRst:
			s.flags = flow.SYN | flow.RST
		case flagsSynAck:
			s.flags = flow.SYN | flow.ACK
		}
		out = append(out, s)
		i = j
	}
	return out
}

// addFlow programa un flujo unidireccional en la caché del exportador.
func (g *gen) addFlow(es *expState, base flow.Record, start, end int64, pkts, bytes uint64, fm flagMode, at signals.Attribution) {
	if pkts == 0 || start >= g.durMs || start < 0 {
		return
	}
	if end < start {
		end = start
	}
	if end > g.durMs {
		// Recorta al final de la simulación conservando la tasa.
		frac := float64(g.durMs-1-start) / float64(end-start)
		pkts = max(1, uint64(float64(pkts)*frac))
		bytes = max(pkts*40, uint64(float64(bytes)*frac))
		end = g.durMs - 1
	}
	if bytes < pkts {
		bytes = pkts
	}
	for _, s := range segments(start, end, pkts, bytes, fm, g.activeMs, g.inactMs) {
		r := base
		r.Start = g.start.Add(time.Duration(s.first) * time.Millisecond)
		r.End = g.start.Add(time.Duration(s.last) * time.Millisecond)
		r.Packets, r.Bytes = s.pkts, s.bytes
		if base.Proto == flow.ProtoTCP {
			r.TCPFlags = s.flags
		}
		tick := ceilDiv(s.exportAt, tickMs)
		es.buckets[tick] = append(es.buckets[tick], pending{rec: r, at: at})
		g.pendingN++
	}
}

// loop avanza el reloj simulado segundo a segundo.
func (g *gen) loop(ctx context.Context, emit Emit) error {
	lastTick := ceilDiv(g.durMs, tickMs)
	for tick := int64(0); ; tick++ {
		if tick%64 == 0 {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("simulación cancelada: %w", err)
			}
		}
		t := tick * tickMs
		if t < g.durMs {
			for _, es := range g.exps {
				if es.router != nil {
					es.router.emit(g, es, t)
				}
				for _, c := range es.clients {
					for _, b := range c.behaviors {
						b.emit(g, c, t)
					}
				}
			}
		}
		now := g.start.Add(time.Duration(t) * time.Millisecond)
		for _, es := range g.exps {
			if err := g.flushTick(es, tick, now, emit); err != nil {
				return err
			}
		}
		if tick >= lastTick && g.pendingN == 0 {
			return nil
		}
	}
}

func (g *gen) flushTick(es *expState, tick int64, now time.Time, emit Emit) error {
	items := es.buckets[tick]
	delete(es.buckets, tick)
	g.pendingN -= len(items)
	// Mismo orden que codifica el exportador: IPv4 y luego IPv6.
	ordered := make([]pending, 0, len(items))
	for _, it := range items {
		if !it.rec.IsV6() {
			ordered = append(ordered, it)
		}
	}
	for _, it := range items {
		if it.rec.IsV6() {
			ordered = append(ordered, it)
		}
	}
	recs := make([]flow.Record, len(ordered))
	for i := range ordered {
		recs[i] = ordered[i].rec
		es.tally.Add(&ordered[i].rec, ordered[i].at)
		g.acc.Add(es.spec.Name, &ordered[i].rec, ordered[i].at)
	}
	src := netip.AddrPortFrom(es.spec.ExporterIP, es.srcPort)
	dst := netip.AddrPortFrom(es.spec.CollectorIP, g.collPort)
	for _, payload := range es.exp.Export(now, recs) {
		if err := emit(capture.Datagram{Time: now, Src: src, Dst: dst, Payload: payload}, es.idx); err != nil {
			return err
		}
	}
	return nil
}
