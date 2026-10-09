package sim

import (
	"fmt"
	"math"
	"math/rand/v2"
	"net/netip"
	"time"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/expect"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/flow"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/signals"
)

// behavior genera las conexiones de un cliente que empiezan en [t, t+1s).
type behavior interface {
	emit(g *gen, c *client, t int64)
}

// newBehavior construye un comportamiento a partir de su especificación.
func newBehavior(b BehaviorSpec, c *client) (behavior, error) {
	switch b.Kind {
	case "residential":
		p := residentialParams{Intensity: 1}
		if err := decodeStrict(&b.Params, &p); err != nil {
			return nil, err
		}
		c.weight, c.bgKind = p.Intensity, "residential"
		return &background{kind: "residential"}, nil
	case "commercial":
		p := commercialParams{
			Intensity: 3, InboundPorts: []uint16{443, 25}, InboundRate: 0.4,
			BackupMbps: 5, BackupDuration: Duration(10 * time.Minute),
		}
		if err := decodeStrict(&b.Params, &p); err != nil {
			return nil, err
		}
		c.weight, c.bgKind = p.Intensity, "commercial"
		return newCommercial(p, c), nil
	case "c2", "beacon":
		p := periodicParams{Port: 443, Proto: "tcp", Interval: Duration(time.Minute), Jitter: 0.1, Responds: true, BytesUp: 320, BytesDown: 900}
		if b.Kind == "beacon" {
			p.Interval = Duration(5 * time.Minute)
			p.Jitter = 0.05
		}
		if err := decodeStrict(&b.Params, &p); err != nil {
			return nil, err
		}
		if !p.Remote.IsValid() {
			return nil, fmt.Errorf("%s: falta remote", b.Kind)
		}
		if p.Interval <= 0 {
			return nil, fmt.Errorf("%s: interval debe ser > 0", b.Kind)
		}
		return &periodic{p: p, next: p.Start.D().Milliseconds() + int64(c.rng.Float64()*float64(p.Interval.D().Milliseconds())*p.Jitter)}, nil
	case "scan":
		p := scanParams{Mode: "horizontal", Ports: []uint16{23, 2323}, Rate: 10, RespondRatio: 0.03, PortFrom: 1, PortTo: 1024}
		if err := decodeStrict(&b.Params, &p); err != nil {
			return nil, err
		}
		if p.Mode == "vertical" && !p.Target.IsValid() {
			return nil, fmt.Errorf("scan vertical: falta target")
		}
		if p.Mode != "vertical" && p.Mode != "horizontal" {
			return nil, fmt.Errorf("scan: mode %q inválido", p.Mode)
		}
		return &scan{p: p, nextPort: p.PortFrom}, nil
	case "fanout":
		p := fanoutParams{Rate: 5, SrcPort: 41234, RespondRatio: 0.2}
		if err := decodeStrict(&b.Params, &p); err != nil {
			return nil, err
		}
		return &fanout{p: p}, nil
	case "smtp":
		p := smtpParams{Rate: 0.5, RefuseRatio: 0.2}
		if err := decodeStrict(&b.Params, &p); err != nil {
			return nil, err
		}
		return &smtp{p: p}, nil
	case "ddos":
		p := ddosParams{Port: 80, PPS: 20000, PacketSize: 1000, Start: Duration(10 * time.Second), Duration: Duration(3 * time.Minute), Flows: 4}
		if err := decodeStrict(&b.Params, &p); err != nil {
			return nil, err
		}
		if len(p.Targets) == 0 || p.Flows <= 0 {
			return nil, fmt.Errorf("ddos: faltan targets o flows")
		}
		return &ddos{p: p}, nil
	case "sustained":
		p := sustainedParams{Port: 443, Mbps: 12, Duration: Duration(45 * time.Minute), PacketSize: 1400}
		if err := decodeStrict(&b.Params, &p); err != nil {
			return nil, err
		}
		if !p.Remote.IsValid() {
			return nil, fmt.Errorf("sustained: falta remote")
		}
		return &sustained{p: p}, nil
	case "lateral":
		p := lateralParams{Port: 445, Rate: 0.05}
		if err := decodeStrict(&b.Params, &p); err != nil {
			return nil, err
		}
		return &lateral{p: p}, nil
	}
	return nil, fmt.Errorf("comportamiento desconocido %q", b.Kind)
}

// --- Utilidades -----------------------------------------------------------

func poisson(r *rand.Rand, lambda float64) int {
	if lambda <= 0 {
		return 0
	}
	if lambda > 30 {
		return max(0, int(math.Round(lambda+math.Sqrt(lambda)*r.NormFloat64())))
	}
	l := math.Exp(-lambda)
	k, p := 0, 1.0
	for {
		p *= r.Float64()
		if p <= l {
			return k
		}
		k++
	}
}

func logNormal(r *rand.Rand, median, sigma float64) float64 {
	return median * math.Exp(sigma*r.NormFloat64())
}

func ephemeral(r *rand.Rand) uint16 { return uint16(32768 + r.IntN(28232)) }

func between(r *rand.Rand, lo, hi int64) int64 {
	if hi <= lo {
		return lo
	}
	return lo + r.Int64N(hi-lo+1)
}

// conn describe una conexión bidireccional de un cliente.
type conn struct {
	proto               uint8
	v6                  bool
	remote              netip.Addr
	lport, rport        uint16
	start, dur          int64
	upPkts, upBytes     uint64
	downPkts, downBytes uint64
	upFlags, downFlags  flagMode
	tos                 uint8
	icmpUp, icmpDown    uint16
	internalTo          *client // tráfico interno hacia otro cliente del nodo
	remoteTTL           uint8
}

func (c *client) ttl() uint8 {
	if c.rng.Float64() < c.ttl64 {
		return 63
	}
	return 127
}

func remoteTTL(r *rand.Rand) uint8 {
	if r.IntN(3) == 0 {
		return uint8(110 + r.IntN(12))
	}
	return uint8(44 + r.IntN(16))
}

// addConn programa los dos sentidos de una conexión del cliente.
func (g *gen) addConn(c *client, k conn) {
	es := c.exp
	spec := es.spec
	var local netip.Addr
	var key string
	switch {
	case k.v6 && len(c.v6) > 0:
		local, key = pick(c.rng, c.v6), c.keyV6
	case c.v4.IsValid():
		local, key = c.v4, c.keyV4
	default:
		return
	}
	if k.internalTo == nil && (local.Is4() != k.remote.Is4()) {
		return
	}

	// Paquetes suficientes para que no haya huecos > inactive timeout.
	if k.dur > 0 {
		minPk := uint64(k.dur*10/(g.inactMs*8)) + 2
		if k.upPkts > 1 && k.upPkts < minPk {
			k.upPkts = minPk
		}
		if k.downPkts > 1 && k.downPkts < minPk {
			k.downPkts = minPk
		}
	}

	status := c.status
	upAt := signals.Attribution{Status: status}
	downAt := signals.Attribution{Status: status}
	switch status {
	case signals.StatusAttributed:
		upAt = signals.Attribution{Status: status, Client: key, Upload: true, Rule: expect.RuleUploadSrc}
		downAt = signals.Attribution{Status: status, Client: key, Rule: expect.RuleDownloadDst}
	case signals.StatusUnknown:
		infra := es.v4.Infrastructure
		if local.Is6() {
			infra = es.v6.Infrastructure
		}
		if containsAny(infra, k.remote) {
			// Fuera de prefijos hablando con infraestructura: §4.6 la clasifica
			// como infraestructura.
			upAt.Status, downAt.Status = signals.StatusInfrastructure, signals.StatusInfrastructure
		} else {
			upAt.Unattributed, downAt.Unattributed = local, local
		}
	}

	remote := k.remote
	remoteIf := spec.Interfaces.Upstream
	remoteMAC, routerRemoteMAC := es.gwMAC, es.wanMAC
	nextHopUp := spec.Gateway
	if local.Is6() {
		nextHopUp = spec.GatewayV6
	}
	if k.internalTo != nil {
		t := k.internalTo
		remote = t.v4
		remoteIf = t.accessIf
		remoteMAC, routerRemoteMAC = t.mac, es.routerMAC
		nextHopUp = netip.Addr{}
		upAt = signals.Attribution{Status: signals.StatusInternal, Client: key, Upload: true, Rule: expect.RuleInternal}
		downAt = signals.Attribution{Status: signals.StatusInternal, Client: t.keyV4, Upload: true, Rule: expect.RuleInternal}
	}
	localMask := uint8(24)
	if local.Is6() {
		localMask = uint8(spec.IPv6ClientLen)
	}
	remoteMask := uint8(0)
	if k.internalTo != nil {
		remoteMask = localMask
	}
	clientMAC, routerLocalMAC := c.mac, es.routerMAC
	if spec.Interfaces.Access == "pppoe" && c.status != signals.StatusTransit {
		clientMAC, routerLocalMAC = [6]byte{}, [6]byte{}
	}
	if spec.Interfaces.Access == "pppoe" && k.internalTo != nil {
		remoteMAC, routerRemoteMAC = [6]byte{}, [6]byte{}
	}

	ttlUp := c.ttl()
	ttlDown := k.remoteTTL
	if ttlDown == 0 {
		ttlDown = remoteTTL(c.rng)
		if k.internalTo != nil {
			ttlDown = k.internalTo.ttl()
		}
	}
	var flUp, flDown uint32
	if local.Is6() {
		flUp, flDown = c.rng.Uint32()&0xfffff, c.rng.Uint32()&0xfffff
	}

	up := flow.Record{
		SrcIP: local, DstIP: remote, SrcPort: k.lport, DstPort: k.rport, Proto: k.proto, ToS: k.tos,
		InIf: c.accessIf, OutIf: remoteIf, NextHop: nextHopUp, SrcMask: localMask, DstMask: remoteMask,
		ICMPTypeCode: k.icmpUp, MinTTL: ttlUp, MaxTTL: ttlUp, SrcMAC: clientMAC, DstMAC: routerLocalMAC, FlowLabel: flUp,
		PostSrcMAC: routerRemoteMAC, PostDstMAC: routerLocalMAC,
	}
	down := flow.Record{
		SrcIP: remote, DstIP: local, SrcPort: k.rport, DstPort: k.lport, Proto: k.proto, ToS: k.tos,
		InIf: remoteIf, OutIf: c.accessIf, SrcMask: remoteMask, DstMask: localMask,
		ICMPTypeCode: k.icmpDown, MinTTL: ttlDown, MaxTTL: ttlDown, SrcMAC: remoteMAC, DstMAC: routerRemoteMAC, FlowLabel: flDown,
		PostSrcMAC: routerLocalMAC, PostDstMAC: routerRemoteMAC,
	}
	if g.natFields && g.nat && local.Is4() && k.internalTo == nil && status == signals.StatusAttributed {
		// NAT en el router principal tal como lo exporta RouterOS 7
		// (docs/traffic-model.md §4.4.2): subida con src privada y
		// postNATSrc pública; bajada con dst = IP pública del NAT y la
		// privada del cliente solo en postNATDst (IE 226). El puerto se
		// conserva casi siempre (masquerade) y siempre en servicios entrantes.
		natIP := spec.NATIPs[c.idx%len(spec.NATIPs)]
		natPort := k.lport
		if signals.Initiated(k.lport, k.rport) && c.rng.IntN(50) == 0 {
			natPort = ephemeral(c.rng)
		}
		up.PostNATSrc, up.PostNATSrcPort, up.PostNATDst, up.PostNATDstPort = natIP, natPort, remote, k.rport
		down.DstIP, down.DstPort = natIP, natPort
		down.PostNATSrc, down.PostNATSrcPort, down.PostNATDst, down.PostNATDstPort = remote, k.rport, local, k.lport
		downAt.Rule = expect.RuleDownloadPostNATDst
	}
	g.addFlow(es, up, k.start, k.start+k.dur, k.upPkts, k.upBytes, k.upFlags, upAt)
	if k.downPkts > 0 {
		g.addFlow(es, down, k.start, k.start+k.dur, k.downPkts, k.downBytes, k.downFlags, downAt)
	}
}

// --- Tráfico de fondo residencial y comercial -----------------------------

type residentialParams struct {
	Intensity float64 `yaml:"intensity"`
}

type commercialParams struct {
	Intensity      float64  `yaml:"intensity"`
	InboundPorts   []uint16 `yaml:"inbound_ports"`
	InboundRate    float64  `yaml:"inbound_rate"`
	BackupMbps     float64  `yaml:"backup_mbps"`
	BackupDuration Duration `yaml:"backup_duration"`
}

// shape es la forma (sin direcciones) de una conexión de fondo.
type shape struct {
	proto               uint8
	service             string // clave de services, "resolver" o "cdn"
	rport               uint16
	dur                 int64
	upPkts, upBytes     uint64
	downPkts, downBytes uint64
	v6ok                bool
	heavy               bool // sesión larga y pesada (streaming, videollamada)
}

func webShape(r *rand.Rand, proto uint8, svcs []string) shape {
	down := uint64(min(logNormal(r, 40_000, 1.2), 5e6))
	dp := down/1300 + 4
	up := dp/2 + 3
	return shape{
		proto: proto, service: pick(r, svcs), rport: 443,
		dur:    int64(min(logNormal(r, 3000, 1.0), 30_000)) + 200,
		upPkts: up, upBytes: up*60 + uint64(between(r, 500, 3000)),
		downPkts: dp, downBytes: down, v6ok: true,
	}
}

func dnsShape(r *rand.Rand) shape {
	return shape{
		proto: flow.ProtoUDP, service: "resolver", rport: 53, dur: 0,
		upPkts: 1, upBytes: uint64(between(r, 60, 90)), downPkts: 1, downBytes: uint64(between(r, 90, 300)), v6ok: true,
	}
}

func residentialShape(r *rand.Rand) shape {
	x := r.Float64()
	switch {
	case x < 0.40:
		return dnsShape(r)
	case x < 0.745:
		return webShape(r, flow.ProtoTCP, []string{"google", "meta", "cloudflare", "akamai", "amazon", "microsoft", "apple"})
	case x < 0.89:
		return webShape(r, flow.ProtoUDP, []string{"google", "meta", "cloudflare"})
	case x < 0.895: // streaming: pocas sesiones largas
		dur := between(r, 60_000, 900_000)
		mbps := 2 + r.Float64()*6
		down := uint64(mbps * 1e6 / 8 * float64(dur) / 1000)
		dp := down / 1400
		return shape{
			proto: flow.ProtoTCP, service: pick(r, []string{"netflix", "google", "akamai"}), rport: 443, dur: dur,
			upPkts: dp / 3, upBytes: dp / 3 * 52, downPkts: dp, downBytes: down, v6ok: true, heavy: true,
		}
	case x < 0.97:
		dur := between(r, 120_000, 600_000)
		pk := uint64(dur/15_000) + 3
		return shape{
			proto: flow.ProtoTCP, service: pick(r, []string{"apple", "google"}), rport: pick(r, []uint16{443, 5223}), dur: dur,
			upPkts: pk, upBytes: pk * 110, downPkts: pk, downBytes: pk * 140, v6ok: true,
		}
	default:
		return shape{proto: flow.ProtoUDP, service: "ntp", rport: 123, upPkts: 1, upBytes: 76, downPkts: 1, downBytes: 76, v6ok: true}
	}
}

func commercialShape(r *rand.Rand) shape {
	x := r.Float64()
	switch {
	case x < 0.30:
		return dnsShape(r)
	case x < 0.55:
		return webShape(r, flow.ProtoTCP, []string{"microsoft", "google", "amazon", "cloudflare"})
	case x < 0.70:
		s := webShape(r, flow.ProtoTCP, []string{"microsoft"})
		s.upBytes *= 3
		return s
	case x < 0.73:
		dur := between(r, 300_000, 1_800_000)
		pk := uint64(dur / 20) // 50 pps
		return shape{
			proto: flow.ProtoUDP, service: "zoom", rport: 8801, dur: dur,
			upPkts: pk, upBytes: pk * 900, downPkts: pk, downBytes: pk * 1000, heavy: true,
		}
	case x < 0.90:
		s := webShape(r, flow.ProtoTCP, []string{"akamai"})
		s.service = "cdn"
		return s
	case x < 0.93:
		up := uint64(between(r, 5_000, 200_000))
		return shape{
			proto: flow.ProtoTCP, service: "microsoft", rport: 587, dur: between(r, 1000, 6000),
			upPkts: up/1300 + 6, upBytes: up, downPkts: 8, downBytes: 1800, v6ok: true,
		}
	case x < 0.95:
		return shape{proto: flow.ProtoUDP, service: "ntp", rport: 123, upPkts: 1, upBytes: 76, downPkts: 1, downBytes: 76, v6ok: true}
	default:
		return webShape(r, flow.ProtoUDP, []string{"google", "cloudflare"})
	}
}

// recordsPerConn estima los registros exportados por conexión de un perfil.
func (g *gen) recordsPerConn(kind string) float64 {
	r := rand.New(rand.NewPCG(1, 2))
	total := 0
	const samples = 4000
	for i := 0; i < samples; i++ {
		var s shape
		if kind == "commercial" {
			s = commercialShape(r)
		} else {
			s = residentialShape(r)
		}
		dur := min(s.dur, g.durMs)
		total += len(segments(0, dur, s.upPkts, s.upBytes, flagsNone, g.activeMs, g.inactMs))
		total += len(segments(0, dur, s.downPkts, s.downBytes, flagsNone, g.activeMs, g.inactMs))
	}
	return float64(total) / samples
}

func (g *gen) remoteFor(c *client, s shape, v6 bool, cdn []netip.Prefix) netip.Addr {
	set := c.exp.v4
	if v6 {
		set = c.exp.v6
	}
	switch s.service {
	case "resolver":
		return set.Resolver
	case "cdn":
		return randomIn(c.rng, pick(c.rng, cdn))
	}
	// Un hogar reutiliza un conjunto acotado de servidores por servicio (lo
	// que le devuelve su DNS): realista y sin fan-out espurio a tasas altas.
	key := s.service + "/4"
	if v6 {
		key = s.service + "/6"
	}
	pool := c.remotes[key]
	if len(pool) > 0 && (len(pool) >= remotePoolSize || c.rng.Float64() > 0.2) {
		return pick(c.rng, pool)
	}
	sv := services[s.service]
	var a netip.Addr
	if v6 && len(sv.v6) > 0 {
		a = randomIn(c.rng, pick(c.rng, sv.v6))
	} else {
		a = randomIn(c.rng, pick(c.rng, sv.v4))
	}
	if c.remotes == nil {
		c.remotes = map[string][]netip.Addr{}
	}
	c.remotes[key] = append(pool, a)
	return a
}

// remotePoolSize es el máximo de servidores distintos por servicio y familia
// que usa un cliente.
const remotePoolSize = 16

// maxHeavy es el máximo de sesiones pesadas simultáneas por cliente: a tasas
// altas sube el nº de conexiones, no el nº de películas a la vez.
const maxHeavy = 2

func (g *gen) shapeConn(c *client, s shape, start int64, cdn []netip.Prefix) {
	if s.heavy {
		live := c.heavyUntil[:0]
		for _, e := range c.heavyUntil {
			if e > start {
				live = append(live, e)
			}
		}
		c.heavyUntil = live
		if len(live) >= maxHeavy {
			s = webShape(c.rng, flow.ProtoTCP, []string{"google", "cloudflare", "akamai"})
		} else {
			c.heavyUntil = append(c.heavyUntil, start+s.dur)
		}
	}
	v6 := false
	if s.v6ok && len(c.v6) > 0 {
		v6 = !c.v4.IsValid() || c.rng.Float64() < 0.35
	}
	if v6 && s.service != "resolver" && s.service != "cdn" && len(services[s.service].v6) == 0 {
		v6 = false
	}
	if v6 && s.service == "cdn" {
		v6 = false
	}
	if !v6 && !c.v4.IsValid() {
		return
	}
	fm := flagsNone
	if s.proto == flow.ProtoTCP {
		fm = flagsSession
	}
	g.addConn(c, conn{
		proto: s.proto, v6: v6, remote: g.remoteFor(c, s, v6, cdn),
		lport: ephemeral(c.rng), rport: s.rport, start: start, dur: s.dur,
		upPkts: s.upPkts, upBytes: s.upBytes, downPkts: s.downPkts, downBytes: s.downBytes,
		upFlags: fm, downFlags: fm,
	})
}

// background es el tráfico de fondo residencial (y la base del comercial).
type background struct {
	kind string
	cdn  []netip.Prefix
}

func (b *background) emit(g *gen, c *client, t int64) {
	if t == 0 {
		// Todo cliente se ve al menos una vez al principio (DNS del CPE).
		window := min(g.durMs, 60_000)
		if c.v4.IsValid() {
			g.shapeConnFamily(c, dnsShape(c.rng), c.rng.Int64N(window), false)
		}
		if len(c.v6) > 0 {
			g.shapeConnFamily(c, dnsShape(c.rng), c.rng.Int64N(window), true)
		}
	}
	n := poisson(c.rng, c.lambda)
	for i := 0; i < n; i++ {
		start := t + c.rng.Int64N(tickMs)
		if b.kind == "commercial" {
			g.shapeConn(c, commercialShape(c.rng), start, b.cdn)
		} else {
			g.shapeConn(c, residentialShape(c.rng), start, nil)
		}
	}
}

func (g *gen) shapeConnFamily(c *client, s shape, start int64, v6 bool) {
	g.addConn(c, conn{
		proto: s.proto, v6: v6, remote: g.remoteFor(c, s, v6, nil), lport: ephemeral(c.rng), rport: s.rport,
		start: start, dur: s.dur, upPkts: s.upPkts, upBytes: s.upBytes, downPkts: s.downPkts, downBytes: s.downBytes,
	})
}

type commercial struct {
	background
	p        commercialParams
	backupAt int64
}

func newCommercial(p commercialParams, c *client) *commercial {
	cm := &commercial{background: background{kind: "commercial"}, p: p}
	ak := services["akamai"].v4
	for i := 0; i < 3; i++ {
		a := randomIn(c.rng, pick(c.rng, ak))
		pf, _ := a.Prefix(24)
		cm.cdn = append(cm.cdn, pf)
	}
	cm.backupAt = -1
	return cm
}

func (cm *commercial) emit(g *gen, c *client, t int64) {
	if t == 0 && cm.p.BackupMbps > 0 {
		cm.backupAt = c.rng.Int64N(max(1, g.durMs/2))
	}
	cm.background.emit(g, c, t)
	// Servicios entrantes: clientes remotos que se conectan al servidor.
	for i, n := 0, poisson(c.rng, cm.p.InboundRate); i < n && len(cm.p.InboundPorts) > 0; i++ {
		port := pick(c.rng, cm.p.InboundPorts)
		reqBytes := uint64(between(c.rng, 400, 3000))
		respBytes := uint64(between(c.rng, 2000, 120_000))
		if port == 25 {
			reqBytes, respBytes = respBytes, reqBytes
		}
		g.addConn(c, conn{
			proto: flow.ProtoTCP, remote: randomPublicV4(c.rng), lport: port, rport: ephemeral(c.rng),
			start: t + c.rng.Int64N(tickMs), dur: between(c.rng, 200, 8000),
			upPkts: respBytes/1300 + 4, upBytes: respBytes, downPkts: reqBytes/1300 + 4, downBytes: reqBytes,
			upFlags: flagsSession, downFlags: flagsSession,
		})
	}
	if cm.backupAt >= t && cm.backupAt < t+tickMs {
		dur := cm.p.BackupDuration.D().Milliseconds()
		up := uint64(cm.p.BackupMbps * 1e6 / 8 * float64(dur) / 1000)
		pk := up / 1400
		g.addConn(c, conn{
			proto: flow.ProtoTCP, remote: randomIn(c.rng, pick(c.rng, services["s3"].v4)), lport: ephemeral(c.rng), rport: 443,
			start: cm.backupAt, dur: dur, upPkts: pk, upBytes: up, downPkts: pk / 2, downBytes: pk / 2 * 52,
			upFlags: flagsSession, downFlags: flagsSession,
		})
	}
}

// --- Bots y comportamientos de seguridad -----------------------------------

type periodicParams struct {
	Remote    netip.Addr `yaml:"remote"`
	Port      uint16     `yaml:"port"`
	Proto     string     `yaml:"proto"`
	Interval  Duration   `yaml:"interval"`
	Jitter    float64    `yaml:"jitter"`
	Responds  bool       `yaml:"responds"`
	BytesUp   uint64     `yaml:"bytes_up"`
	BytesDown uint64     `yaml:"bytes_down"`
	Start     Duration   `yaml:"start"`
}

// periodic es un bot que contacta periódicamente con su C2 (o un beacon).
type periodic struct {
	p    periodicParams
	next int64
}

func (b *periodic) emit(g *gen, c *client, t int64) {
	iv := float64(b.p.Interval.D().Milliseconds())
	for b.next < t+tickMs {
		if b.next >= t {
			proto := flow.ProtoTCP
			fm := flagsSession
			if b.p.Proto == "udp" {
				proto, fm = flow.ProtoUDP, flagsNone
			}
			k := conn{
				proto: proto, remote: b.p.Remote, lport: ephemeral(c.rng), rport: b.p.Port, start: b.next,
				dur:    between(c.rng, 150, 900),
				upPkts: 6, upBytes: jitterBytes(c.rng, b.p.BytesUp), downPkts: 5, downBytes: jitterBytes(c.rng, b.p.BytesDown),
				upFlags: fm, downFlags: fm,
			}
			if !b.p.Responds {
				k.dur, k.upPkts, k.upBytes, k.downPkts = 3000, 3, 180, 0
				if proto == flow.ProtoTCP {
					k.upFlags = flagsSynOnly
				}
			}
			g.addConn(c, k)
		}
		b.next += int64(iv * (1 + b.p.Jitter*(2*c.rng.Float64()-1)))
	}
}

func jitterBytes(r *rand.Rand, b uint64) uint64 {
	return uint64(float64(b) * (0.9 + 0.2*r.Float64()))
}

type scanParams struct {
	Mode         string     `yaml:"mode"`
	Ports        []uint16   `yaml:"ports"`
	Rate         float64    `yaml:"rate"`
	RespondRatio float64    `yaml:"respond_ratio"`
	Target       netip.Addr `yaml:"target"`
	PortFrom     uint16     `yaml:"port_from"`
	PortTo       uint16     `yaml:"port_to"`
	Start        Duration   `yaml:"start"`
}

// scan es un escaneo horizontal (Mirai: SYN a 23/2323 de IPs aleatorias) o
// vertical (muchos puertos de un destino).
type scan struct {
	p        scanParams
	nextPort uint16
	done     bool
	carry    float64
}

var openPorts = map[uint16]bool{22: true, 80: true, 443: true}

func (s *scan) emit(g *gen, c *client, t int64) {
	if t < s.p.Start.D().Milliseconds() || s.done {
		return
	}
	if s.p.Mode == "vertical" {
		s.carry += s.p.Rate
		n := int(s.carry)
		s.carry -= float64(n)
		for i := 0; i < n; i++ {
			port := s.nextPort
			start := t + int64(i)*tickMs/int64(n)
			k := conn{
				proto: flow.ProtoTCP, remote: s.p.Target, lport: ephemeral(c.rng), rport: port, start: start,
				upPkts: 1, upBytes: 60, upFlags: flagsSynOnly, downPkts: 1, downBytes: 40, downFlags: flagsRstAck,
			}
			if openPorts[port] {
				k.upPkts, k.upBytes, k.upFlags = 2, 100, flagsSynRst
				k.downFlags, k.downBytes = flagsSynAck, 44
				k.dur = 5
			}
			g.addConn(c, k)
			if s.nextPort >= s.p.PortTo {
				s.done = true
				return
			}
			s.nextPort++
		}
		return
	}
	for i, n := 0, poisson(c.rng, s.p.Rate); i < n; i++ {
		k := conn{
			proto: flow.ProtoTCP, remote: randomPublicV4(c.rng), lport: ephemeral(c.rng), rport: pick(c.rng, s.p.Ports),
			start: t + c.rng.Int64N(tickMs), upPkts: 1, upBytes: 60, upFlags: flagsSynOnly,
		}
		if c.rng.Float64() < 0.5 {
			k.upPkts, k.upBytes, k.dur = 2, 120, 1000 // retransmisión
		}
		if c.rng.Float64() < s.p.RespondRatio {
			// El destino responde: intento de login por telnet.
			k.dur = between(c.rng, 2000, 5000)
			k.upPkts, k.upBytes, k.upFlags = 8, 600, flagsSession
			k.downPkts, k.downBytes, k.downFlags = 10, 900, flagsSession
		}
		g.addConn(c, k)
	}
}

type fanoutParams struct {
	Rate         float64 `yaml:"rate"`
	SrcPort      uint16  `yaml:"src_port"`
	RespondRatio float64 `yaml:"respond_ratio"`
}

// fanout es un bot P2P que habla por UDP con cientos de IPs dispersas.
type fanout struct{ p fanoutParams }

func (f *fanout) emit(g *gen, c *client, t int64) {
	for i, n := 0, poisson(c.rng, f.p.Rate); i < n; i++ {
		k := conn{
			proto: flow.ProtoUDP, remote: randomPublicV4(c.rng), lport: f.p.SrcPort, rport: uint16(1024 + c.rng.IntN(64000)),
			start: t + c.rng.Int64N(tickMs), upPkts: 1, upBytes: uint64(between(c.rng, 100, 400)),
		}
		if c.rng.Float64() < f.p.RespondRatio {
			k.downPkts, k.downBytes, k.dur = 1, uint64(between(c.rng, 100, 600)), 0
		}
		g.addConn(c, k)
	}
}

type smtpParams struct {
	Rate        float64 `yaml:"rate"`
	RefuseRatio float64 `yaml:"refuse_ratio"`
}

// smtp es un spambot que entrega correo directo a servidores MX (25/tcp).
type smtp struct{ p smtpParams }

func (s *smtp) emit(g *gen, c *client, t int64) {
	for i, n := 0, poisson(c.rng, s.p.Rate); i < n; i++ {
		k := conn{
			proto: flow.ProtoTCP, remote: randomPublicV4(c.rng), lport: ephemeral(c.rng), rport: 25,
			start: t + c.rng.Int64N(tickMs),
		}
		if c.rng.Float64() < s.p.RefuseRatio {
			k.upPkts, k.upBytes, k.upFlags = 1, 60, flagsSynOnly
			k.downPkts, k.downBytes, k.downFlags = 1, 40, flagsRstAck
		} else {
			up := uint64(between(c.rng, 5_000, 30_000))
			k.dur = between(c.rng, 2000, 10_000)
			k.upPkts, k.upBytes, k.upFlags = up/1300+15, up, flagsSession
			k.downPkts, k.downBytes, k.downFlags = 12, uint64(between(c.rng, 1000, 3000)), flagsSession
		}
		g.addConn(c, k)
	}
}

type ddosParams struct {
	Targets    []netip.Addr `yaml:"targets"`
	Port       uint16       `yaml:"port"`
	PPS        float64      `yaml:"pps"`
	PacketSize uint64       `yaml:"packet_size"`
	Start      Duration     `yaml:"start"`
	Duration   Duration     `yaml:"duration"`
	Flows      int          `yaml:"flows"`
}

// ddos es una inundación UDP saliente hacia pocos destinos.
type ddos struct{ p ddosParams }

func (d *ddos) emit(g *gen, c *client, t int64) {
	st := d.p.Start.D().Milliseconds()
	if st < t || st >= t+tickMs {
		return
	}
	dur := d.p.Duration.D().Milliseconds()
	for _, tg := range d.p.Targets {
		for f := 0; f < d.p.Flows; f++ {
			pk := uint64(d.p.PPS / float64(len(d.p.Targets)*d.p.Flows) * float64(dur) / 1000)
			g.addConn(c, conn{
				proto: flow.ProtoUDP, remote: tg, lport: ephemeral(c.rng), rport: d.p.Port, start: st, dur: dur,
				upPkts: pk, upBytes: pk * d.p.PacketSize,
			})
		}
	}
}

type sustainedParams struct {
	Remote     netip.Addr `yaml:"remote"`
	Port       uint16     `yaml:"port"`
	Mbps       float64    `yaml:"mbps"`
	Start      Duration   `yaml:"start"`
	Duration   Duration   `yaml:"duration"`
	PacketSize uint64     `yaml:"packet_size"`
}

// sustained es una subida sostenida (proxy residencial, exfiltración).
type sustained struct{ p sustainedParams }

func (s *sustained) emit(g *gen, c *client, t int64) {
	st := s.p.Start.D().Milliseconds()
	if st < t || st >= t+tickMs {
		return
	}
	dur := s.p.Duration.D().Milliseconds()
	up := uint64(s.p.Mbps * 1e6 / 8 * float64(dur) / 1000)
	pk := up / s.p.PacketSize
	g.addConn(c, conn{
		proto: flow.ProtoTCP, remote: s.p.Remote, lport: ephemeral(c.rng), rport: s.p.Port, start: st, dur: dur,
		upPkts: pk, upBytes: up, downPkts: pk / 2, downBytes: pk / 2 * 52, upFlags: flagsSession, downFlags: flagsSession,
	})
}

type lateralParams struct {
	Port uint16  `yaml:"port"`
	Rate float64 `yaml:"rate"`
}

// lateral son sesiones entre clientes del mismo nodo (tráfico interno).
type lateral struct{ p lateralParams }

func (l *lateral) emit(g *gen, c *client, t int64) {
	for i, n := 0, poisson(c.rng, l.p.Rate); i < n; i++ {
		var cands []*client
		for _, o := range c.exp.clients {
			if o != c && o.v4.IsValid() && o.status == signals.StatusAttributed {
				cands = append(cands, o)
			}
		}
		if len(cands) == 0 || !c.v4.IsValid() || c.status != signals.StatusAttributed {
			return
		}
		g.addConn(c, conn{
			proto: flow.ProtoTCP, internalTo: pick(c.rng, cands), lport: ephemeral(c.rng), rport: l.p.Port,
			start: t + c.rng.Int64N(tickMs), dur: between(c.rng, 500, 4000),
			upPkts: 12, upBytes: uint64(between(c.rng, 1500, 9000)), downPkts: 10, downBytes: uint64(between(c.rng, 1200, 6000)),
			upFlags: flagsSession, downFlags: flagsSession,
		})
	}
}

// --- Tráfico del propio router ---------------------------------------------

// routerBehavior genera el tráfico del router: exportación de flujos y SNMP
// por el túnel (descartados por el colector), WireGuard exterior, DNS y NTP
// del router (infraestructura).
type routerBehavior struct {
	nextSNMP, nextDNS, nextNTP int64
}

func (rb *routerBehavior) emit(g *gen, es *expState, t int64) {
	spec := es.spec
	r := es.rng
	tun := signals.Attribution{Status: signals.StatusTunnel}
	infra := signals.Attribution{Status: signals.StatusInfrastructure}
	if t == 0 {
		dur := g.durMs - 1
		pk := uint64(dur/1000) * 4
		g.addFlow(es, flow.Record{
			SrcIP: spec.ExporterIP, DstIP: spec.CollectorIP, SrcPort: es.srcPort, DstPort: g.collPort, Proto: flow.ProtoUDP,
			OutIf: spec.Interfaces.Tunnel, MinTTL: 64, MaxTTL: 64,
		}, 0, dur, pk, pk*1200, flagsNone, tun)
		wgOut := flow.Record{
			SrcIP: spec.WANIP, DstIP: spec.HubIP, SrcPort: 13231, DstPort: 51820, Proto: flow.ProtoUDP,
			OutIf: spec.Interfaces.Upstream, NextHop: spec.Gateway, MinTTL: 64, MaxTTL: 64, SrcMAC: es.wanMAC, DstMAC: es.gwMAC,
		}
		g.addFlow(es, wgOut, 0, dur, pk+uint64(dur/25_000), pk*1260, flagsNone, infra)
		wgIn := flow.Record{
			SrcIP: spec.HubIP, DstIP: spec.WANIP, SrcPort: 51820, DstPort: 13231, Proto: flow.ProtoUDP,
			InIf: spec.Interfaces.Upstream, MinTTL: 52, MaxTTL: 52, SrcMAC: es.gwMAC, DstMAC: es.wanMAC,
		}
		g.addFlow(es, wgIn, 0, dur, uint64(dur/5000)+2, (uint64(dur/5000)+2)*180, flagsNone, infra)
		rb.nextSNMP = r.Int64N(60_000)
		rb.nextDNS = r.Int64N(45_000)
		rb.nextNTP = r.Int64N(300_000)
	}
	if rb.nextSNMP >= t && rb.nextSNMP < t+tickMs {
		port := ephemeral(r)
		g.addFlow(es, flow.Record{
			SrcIP: spec.CollectorIP, DstIP: spec.ExporterIP, SrcPort: port, DstPort: 161, Proto: flow.ProtoUDP,
			InIf: spec.Interfaces.Tunnel, MinTTL: 64, MaxTTL: 64,
		}, rb.nextSNMP, rb.nextSNMP+40, 4, 4*120, flagsNone, tun)
		g.addFlow(es, flow.Record{
			SrcIP: spec.ExporterIP, DstIP: spec.CollectorIP, SrcPort: 161, DstPort: port, Proto: flow.ProtoUDP,
			OutIf: spec.Interfaces.Tunnel, MinTTL: 64, MaxTTL: 64,
		}, rb.nextSNMP+5, rb.nextSNMP+45, 4, 4*600, flagsNone, tun)
		rb.nextSNMP += 60_000
	}
	routerConn := func(start int64, dst netip.Addr, sport, dport uint16, upB, downB uint64) {
		g.addFlow(es, flow.Record{
			SrcIP: spec.WANIP, DstIP: dst, SrcPort: sport, DstPort: dport, Proto: flow.ProtoUDP,
			OutIf: spec.Interfaces.Upstream, NextHop: spec.Gateway, MinTTL: 64, MaxTTL: 64, SrcMAC: es.wanMAC, DstMAC: es.gwMAC,
		}, start, start, 1, upB, flagsNone, infra)
		g.addFlow(es, flow.Record{
			SrcIP: dst, DstIP: spec.WANIP, SrcPort: dport, DstPort: sport, Proto: flow.ProtoUDP,
			InIf: spec.Interfaces.Upstream, MinTTL: 57, MaxTTL: 57, SrcMAC: es.gwMAC, DstMAC: es.wanMAC,
		}, start+15, start+15, 1, downB, flagsNone, infra)
	}
	if rb.nextDNS >= t && rb.nextDNS < t+tickMs {
		routerConn(rb.nextDNS, randomIn(r, pick(r, services["publicdns"].v4)), ephemeral(r), 53, 70, 180)
		rb.nextDNS += 30_000 + r.Int64N(30_000)
	}
	if rb.nextNTP >= t && rb.nextNTP < t+tickMs {
		routerConn(rb.nextNTP, randomIn(r, services["ntp"].v4[0]), 123, 123, 76, 76)
		rb.nextNTP += 300_000
	}
}
