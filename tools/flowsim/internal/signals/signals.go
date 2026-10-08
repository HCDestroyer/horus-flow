// Package signals calcula, a partir de flujos ya atribuidos, las señales de
// docs/traffic-model.md §8 (botnets) y §9 (comercial) con las reglas
// orientativas allí descritas. Lo usan el generador (sobre la verdad de
// terreno) y el verificador (sobre lo decodificado): si ambos coinciden, la
// salida del simulador transporta las señales que cada escenario promete.
//
// No es el detector de Horus (mod:detection, I1-10/I1-11): solo comprueba que
// los datos del escenario contienen la señal con margen sobre el umbral.
package signals

import (
	"fmt"
	"math"
	"net/netip"
	"slices"
	"sort"
	"time"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/flow"
)

// Estados de atribución (docs/traffic-model.md §4.6, más "excluded" y
// "tunnel", que el colector descarta).
const (
	StatusAttributed     = "attributed"
	StatusInternal       = "internal"
	StatusInfrastructure = "infrastructure"
	StatusTransit        = "transit"
	StatusUnknown        = "unknown"
	StatusExcluded       = "excluded"
	StatusTunnel         = "tunnel"
)

// Nombres de señal.
const (
	SignalC2Contact      = "c2_contact"
	SignalScanHorizontal = "scan_horizontal"
	SignalScanVertical   = "scan_vertical"
	SignalFanout         = "fanout"
	SignalWatchPorts     = "watch_ports"
	SignalSMTP           = "smtp_outbound"
	SignalDDoS           = "ddos"
	SignalBeaconing      = "beaconing"
	SignalSustainedUp    = "sustained_upload"
	SignalInboundService = "inbound_service"
)

// AllSignals enumera las señales conocidas.
var AllSignals = []string{
	SignalC2Contact, SignalScanHorizontal, SignalScanVertical, SignalFanout,
	SignalWatchPorts, SignalSMTP, SignalDDoS, SignalBeaconing, SignalSustainedUp,
	SignalInboundService,
}

// Umbrales (docs/traffic-model.md §8 y §9).
const (
	Window             = 5 * time.Minute
	ScanSynOnlyRatio   = 0.7
	ScanMinDsts        = 100
	VerticalMinPorts   = 50
	FanoutMinIPs       = 500 // estrictamente mayor
	FanoutMinNets24    = 200 // estrictamente mayor
	WatchMinDsts       = 20
	SMTPWindow         = time.Hour
	SMTPMinDsts        = 20
	DDoSMinPPS         = 2000
	DDoSMinMinutes     = 2
	DDoSMaxTargets     = 3
	SustainedMinutes   = 30
	SustainedUpShare   = 0.8
	SustainedMinBps    = 5e6
	BeaconMinConns     = 10
	BeaconMinSpan      = 6 * time.Hour
	BeaconMaxCV        = 0.2
	BeaconMaxMeanBytes = 4096
	InboundMinRemotes  = 20
)

// WatchPorts son los puertos típicos de botnet de §8.
var WatchPorts = map[uint16]bool{
	23: true, 2323: true, 37215: true, 52869: true, 7547: true, 5555: true,
	445: true, 139: true, 6667: true, 6697: true, 3389: true, 1433: true, 8291: true,
}

// Attribution es el resultado de atribuir un flujo a un cliente.
type Attribution struct {
	Status string
	// Client es la clave del cliente (IPv4 o prefijo IPv6 /len) si Status es
	// attributed o internal.
	Client string
	// Upload indica que el cliente es el origen del flujo.
	Upload bool
	// Unattributed es la IP del lado customer_edge cuando Status es unknown.
	Unattributed netip.Addr
}

// Signal es una señal detectada para un cliente.
type Signal struct {
	Exporter string         `json:"exporter"`
	Client   string         `json:"client"`
	Name     string         `json:"signal"`
	Detail   map[string]any `json:"detail"`
}

// Key identifica la señal sin el detalle.
func (s Signal) Key() string { return s.Exporter + "|" + s.Client + "|" + s.Name }

// Indicator es un indicador de reputación del escenario (feed de prueba).
type Indicator struct {
	IP         netip.Addr `json:"ip"`
	Kind       string     `json:"kind"`
	Confidence string     `json:"confidence"`
	Source     string     `json:"source"`
}

// Config parametriza el acumulador.
type Config struct {
	Start      time.Time
	Duration   time.Duration
	Indicators []Indicator
}

type clientKey struct{ exporter, client string }

type portKey struct {
	ip   netip.Addr
	port uint16
}

type windowState struct {
	initTCP     int
	synOnly     int
	synOnlyDsts map[netip.Addr]struct{}
	dstPorts    map[netip.Addr]map[uint16]struct{}
	remotes     map[netip.Addr]struct{}
	nets24      map[netip.Prefix]struct{}
	watchDsts   map[netip.Addr]struct{}
	watchPorts  map[uint16]struct{}
}

type minuteState struct {
	upBytes, downBytes float64
	dstPkts            map[netip.Addr]float64
}

type c2State struct {
	conns     int
	responded bool
}

type beaconState struct {
	starts []int64
	bytes  []uint64
}

type clientState struct {
	windows map[int64]*windowState
	smtp    map[int64]map[netip.Addr]struct{}
	minutes map[int64]*minuteState
	c2      map[portKey]*c2State
	beacons map[portKey]*beaconState
	inbound map[uint16]map[netip.Addr]struct{}
}

// Accumulator agrega flujos y produce las señales.
type Accumulator struct {
	cfg        Config
	indicators map[netip.Addr]Indicator
	clients    map[clientKey]*clientState
	beacon     bool
}

// New crea un acumulador.
func New(cfg Config) *Accumulator {
	ind := make(map[netip.Addr]Indicator, len(cfg.Indicators))
	for _, i := range cfg.Indicators {
		ind[i.IP] = i
	}
	return &Accumulator{
		cfg:        cfg,
		indicators: ind,
		clients:    make(map[clientKey]*clientState),
		beacon:     cfg.Duration >= BeaconMinSpan,
	}
}

func (a *Accumulator) state(exporter, client string) *clientState {
	k := clientKey{exporter, client}
	s := a.clients[k]
	if s == nil {
		s = &clientState{
			windows: make(map[int64]*windowState),
			smtp:    make(map[int64]map[netip.Addr]struct{}),
			minutes: make(map[int64]*minuteState),
			c2:      make(map[portKey]*c2State),
			beacons: make(map[portKey]*beaconState),
			inbound: make(map[uint16]map[netip.Addr]struct{}),
		}
		a.clients[k] = s
	}
	return s
}

// Initiated aplica la heurística de iniciador: el cliente inicia salvo que
// actúe como servidor en un puerto bien conocido (puerto local < 1024 y
// remoto efímero).
func Initiated(clientPort, remotePort uint16) bool {
	return clientPort >= 1024 || remotePort < 1024
}

func addSet[K comparable](m map[K]struct{}, k K) map[K]struct{} {
	if m == nil {
		m = make(map[K]struct{})
	}
	m[k] = struct{}{}
	return m
}

// Add incorpora un flujo atribuido.
func (a *Accumulator) Add(exporter string, r *flow.Record, at Attribution) {
	if at.Status != StatusAttributed && at.Status != StatusInternal {
		return
	}
	s := a.state(exporter, at.Client)
	var remote netip.Addr
	var clientPort, remotePort uint16
	if at.Upload {
		remote, clientPort, remotePort = r.DstIP, r.SrcPort, r.DstPort
	} else {
		remote, clientPort, remotePort = r.SrcIP, r.DstPort, r.SrcPort
	}
	startMs := r.Start.Sub(a.cfg.Start).Milliseconds()

	// Reparto por minutos (subida/bajada y paquetes por destino).
	a.spreadMinutes(s, r, at.Upload, remote)

	// Contacto con indicadores de C2.
	if _, ok := a.indicators[remote]; ok {
		pk := portKey{remote, remotePort}
		cs := s.c2[pk]
		if cs == nil {
			cs = &c2State{}
			s.c2[pk] = cs
		}
		if at.Upload {
			if r.Proto != flow.ProtoTCP || r.TCPFlags&flow.SYN != 0 {
				cs.conns++
			}
		} else if r.Packets > 0 && (r.Proto != flow.ProtoTCP || r.TCPFlags&flow.ACK != 0) {
			cs.responded = true
		}
	}

	if !at.Upload {
		return
	}
	initiated := Initiated(clientPort, remotePort)

	// Servicios entrantes aceptados (SYN+ACK desde el cliente en puerto bajo).
	if !initiated && r.Proto == flow.ProtoTCP && r.TCPFlags&(flow.SYN|flow.ACK) == flow.SYN|flow.ACK {
		s.inbound[clientPort] = addSet(s.inbound[clientPort], remote)
	}
	if !initiated {
		return
	}

	w := startMs / Window.Milliseconds()
	ws := s.windows[w]
	if ws == nil {
		ws = &windowState{}
		s.windows[w] = ws
	}
	ws.remotes = addSet(ws.remotes, remote)
	ws.nets24 = addSet(ws.nets24, net24(remote))
	if r.Proto == flow.ProtoTCP {
		ws.initTCP++
		if r.TCPFlags&flow.SYN != 0 && r.TCPFlags&flow.ACK == 0 {
			ws.synOnly++
			ws.synOnlyDsts = addSet(ws.synOnlyDsts, remote)
		}
		if ws.dstPorts == nil {
			ws.dstPorts = make(map[netip.Addr]map[uint16]struct{})
		}
		ws.dstPorts[remote] = addSet(ws.dstPorts[remote], remotePort)
	}
	if WatchPorts[remotePort] && (r.Proto == flow.ProtoTCP || r.Proto == flow.ProtoUDP) {
		ws.watchDsts = addSet(ws.watchDsts, remote)
		ws.watchPorts = addSet(ws.watchPorts, remotePort)
	}
	if r.Proto == flow.ProtoTCP && remotePort == 25 {
		h := startMs / SMTPWindow.Milliseconds()
		s.smtp[h] = addSet(s.smtp[h], remote)
	}
	if a.beacon && (r.Proto != flow.ProtoTCP || r.TCPFlags&flow.SYN != 0) {
		pk := portKey{remote, remotePort}
		bs := s.beacons[pk]
		if bs == nil {
			bs = &beaconState{}
			s.beacons[pk] = bs
		}
		bs.starts = append(bs.starts, r.Start.UnixMilli())
		bs.bytes = append(bs.bytes, r.Bytes)
	}
}

func net24(a netip.Addr) netip.Prefix {
	bits := 24
	if a.Is6() {
		bits = 48
	}
	p, _ := a.Prefix(bits)
	return p
}

func (a *Accumulator) spreadMinutes(s *clientState, r *flow.Record, upload bool, remote netip.Addr) {
	st := r.Start.Sub(a.cfg.Start).Milliseconds()
	en := r.End.Sub(a.cfg.Start).Milliseconds()
	if en < st {
		en = st
	}
	m0, m1 := floorDiv(st, 60000), floorDiv(en, 60000)
	span := float64(en - st)
	for m := m0; m <= m1; m++ {
		frac := 1.0
		if span > 0 {
			lo, hi := max(st, m*60000), min(en, (m+1)*60000)
			frac = float64(hi-lo) / span
		} else if m != m0 {
			continue
		}
		ms := s.minutes[m]
		if ms == nil {
			ms = &minuteState{}
			s.minutes[m] = ms
		}
		if upload {
			ms.upBytes += frac * float64(r.Bytes)
			if ms.dstPkts == nil {
				ms.dstPkts = make(map[netip.Addr]float64)
			}
			ms.dstPkts[remote] += frac * float64(r.Packets)
		} else {
			ms.downBytes += frac * float64(r.Bytes)
		}
	}
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func sortedKeys[K comparable, V any](m map[K]V, less func(a, b K) int) []K {
	ks := make([]K, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.SortFunc(ks, less)
	return ks
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpPortKey(a, b portKey) int {
	if c := a.ip.Compare(b.ip); c != 0 {
		return c
	}
	return int(a.port) - int(b.port)
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// Results devuelve las señales ordenadas por exportador, cliente y nombre.
func (a *Accumulator) Results() []Signal {
	var out []Signal
	for k, s := range a.clients {
		emit := func(name string, d map[string]any) {
			out = append(out, Signal{Exporter: k.exporter, Client: k.client, Name: name, Detail: d})
		}
		a.c2Signals(s, emit)
		a.windowSignals(s, emit)
		a.smtpSignal(s, emit)
		a.minuteSignals(s, emit)
		a.beaconSignal(s, emit)
		a.inboundSignal(s, emit)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

func (a *Accumulator) c2Signals(s *clientState, emit func(string, map[string]any)) {
	if len(s.c2) == 0 {
		return
	}
	var contacts []map[string]any
	for _, pk := range sortedKeys(s.c2, cmpPortKey) {
		cs := s.c2[pk]
		contacts = append(contacts, map[string]any{
			"remote_ip":   pk.ip.String(),
			"remote_port": int(pk.port),
			"connections": cs.conns,
			"responded":   cs.responded,
			"feed":        a.indicators[pk.ip].Source,
		})
	}
	emit(SignalC2Contact, map[string]any{"contacts": contacts})
}

func (a *Accumulator) windowSignals(s *clientState, emit func(string, map[string]any)) {
	var hBest, vBest, fBest, wBest map[string]any
	for _, w := range sortedKeys(s.windows, cmpInt64) {
		ws := s.windows[w]
		win := a.cfg.Start.Add(time.Duration(w) * Window).UTC().Format(time.RFC3339)
		if ws.initTCP > 0 {
			ratio := float64(ws.synOnly) / float64(ws.initTCP)
			if ratio > ScanSynOnlyRatio && len(ws.synOnlyDsts) >= ScanMinDsts && hBest == nil {
				ports := map[uint16]struct{}{}
				for dst := range ws.synOnlyDsts {
					for p := range ws.dstPorts[dst] {
						ports[p] = struct{}{}
					}
				}
				hBest = map[string]any{
					"window_start":   win,
					"syn_only_ratio": round2(ratio),
					"destinations":   len(ws.synOnlyDsts),
					"nets24":         countNets(ws.synOnlyDsts),
					"ports":          portList(ports, 10),
				}
			}
		}
		if vBest == nil {
			var bestDst netip.Addr
			best := 0
			for _, dst := range sortedKeys(ws.dstPorts, func(x, y netip.Addr) int { return x.Compare(y) }) {
				if n := len(ws.dstPorts[dst]); n > best {
					best, bestDst = n, dst
				}
			}
			if best >= VerticalMinPorts {
				vBest = map[string]any{"window_start": win, "destination": bestDst.String(), "ports": best}
			}
		}
		if fBest == nil && len(ws.remotes) > FanoutMinIPs && len(ws.nets24) > FanoutMinNets24 {
			fBest = map[string]any{"window_start": win, "remote_ips": len(ws.remotes), "nets24": len(ws.nets24)}
		}
		if wBest == nil && len(ws.watchDsts) >= WatchMinDsts {
			wBest = map[string]any{"window_start": win, "destinations": len(ws.watchDsts), "ports": portList(ws.watchPorts, 13)}
		}
	}
	if hBest != nil {
		emit(SignalScanHorizontal, hBest)
	}
	if vBest != nil {
		emit(SignalScanVertical, vBest)
	}
	if fBest != nil {
		emit(SignalFanout, fBest)
	}
	if wBest != nil {
		emit(SignalWatchPorts, wBest)
	}
}

func countNets(m map[netip.Addr]struct{}) int {
	n := map[netip.Prefix]struct{}{}
	for a := range m {
		n[net24(a)] = struct{}{}
	}
	return len(n)
}

func portList(m map[uint16]struct{}, limit int) []int {
	ps := make([]int, 0, len(m))
	for p := range m {
		ps = append(ps, int(p))
	}
	sort.Ints(ps)
	if len(ps) > limit {
		ps = ps[:limit]
	}
	return ps
}

func (a *Accumulator) smtpSignal(s *clientState, emit func(string, map[string]any)) {
	for _, h := range sortedKeys(s.smtp, cmpInt64) {
		if n := len(s.smtp[h]); n >= SMTPMinDsts {
			emit(SignalSMTP, map[string]any{
				"window_start": a.cfg.Start.Add(time.Duration(h) * SMTPWindow).UTC().Format(time.RFC3339),
				"smtp_servers": n,
				"port":         25,
			})
			return
		}
	}
}

func (a *Accumulator) minuteSignals(s *clientState, emit func(string, map[string]any)) {
	mins := sortedKeys(s.minutes, cmpInt64)
	// DDoS: pps a ≤ 3 destinos por encima del umbral ≥ 2 minutos seguidos.
	run, bestRun := 0, 0
	prev := int64(math.MinInt64)
	var peak float64
	var targets map[netip.Addr]struct{}
	for _, m := range mins {
		ms := s.minutes[m]
		top := topN(ms.dstPkts, DDoSMaxTargets)
		var pk float64
		for _, t := range top {
			pk += ms.dstPkts[t]
		}
		pps := pk / 60
		if pps > DDoSMinPPS {
			if run > 0 && m == prev+1 {
				run++
			} else {
				run = 1
			}
			if targets == nil {
				targets = map[netip.Addr]struct{}{}
			}
			for _, t := range top {
				if ms.dstPkts[t] >= 0.1*pk { // solo destinos con peso en la ráfaga
					targets[t] = struct{}{}
				}
			}
			peak = math.Max(peak, pps)
		} else {
			run = 0
		}
		bestRun = max(bestRun, run)
		prev = m
	}
	if bestRun >= DDoSMinMinutes {
		ts := make([]string, 0, len(targets))
		for t := range targets {
			ts = append(ts, t.String())
		}
		sort.Strings(ts)
		emit(SignalDDoS, map[string]any{"minutes": bestRun, "peak_pps": math.Round(peak), "targets": ts})
	}

	// Subida sostenida.
	run, bestRun = 0, 0
	prev = math.MinInt64
	var peakBps float64
	for _, m := range mins {
		ms := s.minutes[m]
		total := ms.upBytes + ms.downBytes
		bps := ms.upBytes * 8 / 60
		if total > 0 && ms.upBytes/total > SustainedUpShare && bps > SustainedMinBps {
			if m != prev+1 {
				run = 0
			}
			run++
			peakBps = math.Max(peakBps, bps)
		} else {
			run = 0
		}
		bestRun = max(bestRun, run)
		prev = m
	}
	if bestRun >= SustainedMinutes {
		emit(SignalSustainedUp, map[string]any{"minutes": bestRun, "peak_mbps": round2(peakBps / 1e6)})
	}
}

func topN(m map[netip.Addr]float64, n int) []netip.Addr {
	ks := sortedKeys(m, func(x, y netip.Addr) int {
		if m[x] != m[y] {
			if m[x] > m[y] {
				return -1
			}
			return 1
		}
		return x.Compare(y)
	})
	if len(ks) > n {
		ks = ks[:n]
	}
	return ks
}

func (a *Accumulator) beaconSignal(s *clientState, emit func(string, map[string]any)) {
	for _, pk := range sortedKeys(s.beacons, cmpPortKey) {
		bs := s.beacons[pk]
		if len(bs.starts) < BeaconMinConns {
			continue
		}
		st := slices.Clone(bs.starts)
		slices.Sort(st)
		span := time.Duration(st[len(st)-1]-st[0]) * time.Millisecond
		if span < BeaconMinSpan {
			continue
		}
		var sum, sq float64
		n := float64(len(st) - 1)
		for i := 1; i < len(st); i++ {
			d := float64(st[i] - st[i-1])
			sum += d
			sq += d * d
		}
		mean := sum / n
		cv := math.Sqrt(math.Max(sq/n-mean*mean, 0)) / mean
		var b uint64
		for _, x := range bs.bytes {
			b += x
		}
		meanBytes := float64(b) / float64(len(bs.bytes))
		if cv < BeaconMaxCV && meanBytes < BeaconMaxMeanBytes {
			emit(SignalBeaconing, map[string]any{
				"remote_ip":      pk.ip.String(),
				"remote_port":    int(pk.port),
				"connections":    len(st),
				"interval_s":     math.Round(mean / 1000),
				"cv":             round2(cv),
				"span_h":         round2(span.Hours()),
				"mean_bytes_out": math.Round(meanBytes),
			})
			return
		}
	}
}

func (a *Accumulator) inboundSignal(s *clientState, emit func(string, map[string]any)) {
	var ports []int
	remotes := 0
	for _, p := range sortedKeys(s.inbound, func(x, y uint16) int { return int(x) - int(y) }) {
		if n := len(s.inbound[p]); n >= InboundMinRemotes {
			ports = append(ports, int(p))
			remotes = max(remotes, n)
		}
	}
	if len(ports) > 0 {
		emit(SignalInboundService, map[string]any{"ports": ports, "max_remotes": remotes})
	}
}

// Describe da una línea legible de la señal.
func Describe(s Signal) string {
	return fmt.Sprintf("%s %s %s %v", s.Exporter, s.Client, s.Name, s.Detail)
}
