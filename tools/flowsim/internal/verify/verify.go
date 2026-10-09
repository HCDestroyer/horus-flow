package verify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/capture"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/expect"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/flow"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/signals"
)

// Options ajusta la verificación.
type Options struct {
	// Tolerance es la fracción de datagramas que se admite perder (UDP en
	// vivo). Con 0 todo debe cuadrar exactamente.
	Tolerance float64
}

const maxMessages = 8

type exporterState struct {
	name       string
	att        *Attributor
	tally      *expect.Tally
	datagrams  uint64
	tmplSends  uint64
	seqStarted bool
	nextSeq    uint32
	seqErrors  int
	pkt        int64
	lastTmpl   int64
	lastTmplAt time.Time
	maxGapPkts int64
	maxGapTime time.Duration
	missing    int
	odidErrs   int
	verErrs    int
	sanity     []string
	sanityN    int
	decodeErrs []string
	decodeN    int
	exportBack int
	lastExport time.Time
	templates  map[uint16][][2]uint16
	tmplChange int
	natPool    map[netip.Addr]bool
	natUp      uint64 // subidas traducidas (postNATSrc != src)
	natDown    uint64 // bajadas atribuidas por IE 226
	natOutside uint64 // registros NAT cuya IP pública no es del pool declarado
}

// Verifier consume datagramas y produce un informe.
type Verifier struct {
	exp       *expect.Expected
	opt       Options
	dec       *Decoder
	exporters []*exporterState
	byAddr    map[netip.AddrPort]int
	byIP      map[netip.Addr]int
	acc       *signals.Accumulator
	unknown   map[string]int
	activeMax time.Duration
}

// New crea un verificador para un expected.json.
func New(e *expect.Expected, opt Options) (*Verifier, error) {
	v := &Verifier{
		exp:       e,
		opt:       opt,
		dec:       NewDecoder(),
		byAddr:    map[netip.AddrPort]int{},
		byIP:      map[netip.Addr]int{},
		unknown:   map[string]int{},
		activeMax: time.Duration(e.Export.ActiveTimeoutSeconds * float64(time.Second)),
	}
	for i, ex := range e.Exporters {
		att, err := NewAttributor(e, i)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ex.Name, err)
		}
		es := &exporterState{
			name: ex.Name, att: att, tally: expect.NewTally(), lastTmpl: -1,
			templates: map[uint16][][2]uint16{}, natPool: map[netip.Addr]bool{},
		}
		for _, s := range ex.NATIPs {
			ip, err := netip.ParseAddr(s)
			if err != nil {
				return nil, fmt.Errorf("%s: nat_ips %q: %w", ex.Name, s, err)
			}
			es.natPool[ip] = true
		}
		v.exporters = append(v.exporters, es)
		if ex.SentFrom != "" {
			ap, err := netip.ParseAddrPort(ex.SentFrom)
			if err != nil {
				return nil, fmt.Errorf("%s: sent_from %q: %w", ex.Name, ex.SentFrom, err)
			}
			v.byAddr[ap] = i
			if _, dup := v.byIP[ap.Addr()]; !dup {
				v.byIP[ap.Addr()] = i
			}
			continue
		}
		ip, err := netip.ParseAddr(ex.ExporterIP)
		if err != nil {
			return nil, fmt.Errorf("%s: exporter_ip: %w", ex.Name, err)
		}
		v.byIP[ip] = i
	}
	var inds []signals.Indicator
	inds = append(inds, e.Indicators...)
	v.acc = signals.New(signals.Config{
		Start:      e.Start,
		Duration:   time.Duration(e.DurationSeconds * float64(time.Second)),
		Indicators: inds,
	})
	return v, nil
}

func (v *Verifier) exporterFor(src netip.AddrPort) (int, bool) {
	src = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())
	if i, ok := v.byAddr[src]; ok {
		return i, true
	}
	i, ok := v.byIP[src.Addr()]
	return i, ok
}

func note(list *[]string, n *int, format string, args ...any) {
	*n++
	if len(*list) < maxMessages {
		*list = append(*list, fmt.Sprintf(format, args...))
	}
}

// Feed procesa un datagrama.
func (v *Verifier) Feed(d capture.Datagram) {
	idx, ok := v.exporterFor(d.Src)
	if !ok {
		v.unknown[d.Src.String()]++
		return
	}
	es := v.exporters[idx]
	info, recs, err := v.dec.Decode(es.name, d.Payload)
	if err != nil {
		note(&es.decodeErrs, &es.decodeN, "datagrama %d: %v", es.pkt, err)
		es.pkt++
		return
	}
	es.datagrams++
	ex := v.exp.Exporters[idx]
	wantVer := uint16(10)
	if v.exp.Protocol == flow.V9.String() {
		wantVer = 9
	}
	if info.Version != wantVer {
		es.verErrs++
	}
	if info.ObservationID != ex.ObservationDomainID {
		es.odidErrs++
	}
	// Secuencia: v9 cuenta paquetes; IPFIX cuenta registros de datos.
	if es.seqStarted && info.Sequence != es.nextSeq {
		es.seqErrors++
	}
	es.seqStarted = true
	if info.Version == 9 {
		es.nextSeq = info.Sequence + 1
	} else {
		es.nextSeq = info.Sequence + uint32(info.DataRecords)
	}
	// Plantillas reenviadas periódicamente.
	if info.TemplateRecords > 0 {
		es.tmplSends++
		if es.lastTmpl >= 0 {
			es.maxGapPkts = max(es.maxGapPkts, es.pkt-es.lastTmpl)
			es.maxGapTime = max(es.maxGapTime, info.ExportTime.Sub(es.lastTmplAt))
		}
		es.lastTmpl, es.lastTmplAt = es.pkt, info.ExportTime
	}
	for _, t := range info.Templates {
		if old, ok := es.templates[t.ID]; ok && !fieldsEqual(old, t.Fields) {
			es.tmplChange++
		}
		es.templates[t.ID] = t.Fields
	}
	es.missing += info.MissingTemplate
	if info.ExportTime.Before(es.lastExport) {
		es.exportBack++
	}
	es.lastExport = info.ExportTime
	es.pkt++

	for i := range recs {
		r := &recs[i]
		v.sanity(es, r, info)
		at := es.att.Attribute(r)
		v.natCount(es, r, at)
		es.tally.Add(r, at)
		v.acc.Add(es.name, r, at)
	}
}

func fieldsEqual(a, b [][2]uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// natCount sigue la traducción NAT de docs/traffic-model.md §4.4: subidas
// con postNATSrc pública y bajadas con dst = IP pública del NAT.
func (v *Verifier) natCount(es *exporterState, r *flow.Record, at signals.Attribution) {
	switch at.Rule {
	case expect.RuleUploadSrc:
		if p := r.PostNATSrc; p.IsValid() && !p.IsUnspecified() && p != r.SrcIP {
			es.natUp++
			if len(es.natPool) > 0 && !es.natPool[p] {
				es.natOutside++
			}
		}
	case expect.RuleDownloadPostNATDst:
		es.natDown++
		if len(es.natPool) > 0 && !es.natPool[r.DstIP] {
			es.natOutside++
		}
	}
}

func (v *Verifier) sanity(es *exporterState, r *flow.Record, info PacketInfo) {
	switch {
	case !r.SrcIP.IsValid() || !r.DstIP.IsValid():
		note(&es.sanity, &es.sanityN, "registro sin direcciones")
	case r.Start.IsZero() || r.End.IsZero():
		note(&es.sanity, &es.sanityN, "registro sin tiempos %s→%s", r.SrcIP, r.DstIP)
	case r.End.Before(r.Start):
		note(&es.sanity, &es.sanityN, "fin antes que inicio %s→%s", r.SrcIP, r.DstIP)
	case r.End.After(info.ExportTime.Add(time.Second)):
		note(&es.sanity, &es.sanityN, "flujo %s→%s termina después de exportarse", r.SrcIP, r.DstIP)
	case v.activeMax > 0 && r.End.Sub(r.Start) > v.activeMax:
		note(&es.sanity, &es.sanityN, "flujo %s→%s dura %s (> active timeout)", r.SrcIP, r.DstIP, r.End.Sub(r.Start))
	case r.Packets == 0 || r.Bytes < r.Packets:
		note(&es.sanity, &es.sanityN, "contadores incoherentes %s→%s: %d B / %d pkts", r.SrcIP, r.DstIP, r.Bytes, r.Packets)
	case r.Proto != flow.ProtoTCP && r.TCPFlags != 0:
		note(&es.sanity, &es.sanityN, "TCP flags en protocolo %d", r.Proto)
	case !r.IsV6() && r.FlowLabel != 0:
		note(&es.sanity, &es.sanityN, "flow label en IPv4")
	}
}

// Check es una comprobación del informe.
type Check struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// Report es el resultado de la verificación.
type Report struct {
	Scenario string  `json:"scenario"`
	Protocol string  `json:"protocol"`
	OK       bool    `json:"ok"`
	Checks   []Check `json:"checks"`
}

// Write imprime el informe legible.
func (r *Report) Write(w io.Writer) error {
	for _, c := range r.Checks {
		st := "OK  "
		switch {
		case c.Skipped:
			st = "SKIP"
		case !c.OK:
			st = "FAIL"
		}
		line := fmt.Sprintf("%s %s", st, c.Name)
		if c.Detail != "" {
			line += ": " + c.Detail
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return fmt.Errorf("informe: %w", err)
		}
	}
	res := "RESULTADO: OK"
	if !r.OK {
		res = "RESULTADO: FALLO"
	}
	if _, err := fmt.Fprintf(w, "%s (%s/%s, %d comprobaciones)\n", res, r.Scenario, r.Protocol, len(r.Checks)); err != nil {
		return fmt.Errorf("informe: %w", err)
	}
	return nil
}

func (r *Report) add(name string, ok bool, format string, args ...any) {
	r.Checks = append(r.Checks, Check{Name: name, OK: ok, Detail: fmt.Sprintf(format, args...)})
}

func (r *Report) skip(name, detail string) {
	r.Checks = append(r.Checks, Check{Name: name, OK: true, Skipped: true, Detail: detail})
}

func within(got, want uint64, tol float64) bool {
	if got == want {
		return true
	}
	if tol == 0 || got > want {
		return false
	}
	return float64(want-got) <= tol*float64(want)
}

// Report compara lo recibido con expected.json.
func (v *Verifier) Report() *Report {
	e := v.exp
	rep := &Report{Scenario: e.Scenario, Protocol: e.Protocol}
	if len(v.unknown) > 0 {
		var ks []string
		for k, n := range v.unknown {
			ks = append(ks, fmt.Sprintf("%s (%d)", k, n))
		}
		sort.Strings(ks)
		rep.add("origen", false, "datagramas de orígenes no declarados: %s", strings.Join(ks, ", "))
	}
	lossy := false
	for i, es := range v.exporters {
		ex := e.Exporters[i]
		p := es.name + ": "
		got := expect.Exporter{}
		es.tally.Fill(&got)
		got.Totals.Datagrams, got.Totals.TemplateSends = es.datagrams, es.tmplSends
		lost := got.Totals.Datagrams < ex.Totals.Datagrams
		lossy = lossy || lost
		tol := v.opt.Tolerance

		rep.add(p+"decodificación", es.decodeN == 0 && es.datagrams > 0, "%d datagramas decodificados, %d errores %v", es.datagrams, es.decodeN, es.decodeErrs)
		rep.add(p+"versión y dominio", es.verErrs == 0 && es.odidErrs == 0, "%s, observation domain %d (versión errónea: %d, dominio erróneo: %d)", e.Protocol, ex.ObservationDomainID, es.verErrs, es.odidErrs)
		rep.add(p+"secuencia", es.seqErrors == 0 || (tol > 0 && lost), "%d saltos de secuencia", es.seqErrors)
		rep.add(p+"plantillas antes de datos", es.missing == 0, "%d sets de datos sin plantilla", es.missing)
		maxGapT := time.Duration(e.Export.TemplateTimeoutSeconds*float64(time.Second)) + time.Second
		okTmpl := es.tmplSends > 0 && es.maxGapPkts <= int64(e.Export.TemplateRefreshPackets) && es.maxGapTime <= maxGapT
		if tol > 0 && lost {
			okTmpl = es.tmplSends > 0
		}
		rep.add(p+"refresco de plantillas", okTmpl, "%d envíos; hueco máx. %d paquetes (límite %d) y %s (límite %s)",
			es.tmplSends, es.maxGapPkts, e.Export.TemplateRefreshPackets, es.maxGapTime, maxGapT)
		rep.add(p+"tiempo de exportación", es.exportBack == 0, "%d retrocesos de exportTime", es.exportBack)
		rep.add(p+"coherencia de registros", es.sanityN == 0, "%d problemas %v", es.sanityN, es.sanity)

		t, w := got.Totals, ex.Totals
		rep.add(p+"totales", within(t.Datagrams, w.Datagrams, tol) && within(t.TemplateSends, w.TemplateSends, tol) &&
			within(t.DataRecords, w.DataRecords, tol) && within(t.RecordsV4, w.RecordsV4, tol) &&
			within(t.RecordsV6, w.RecordsV6, tol) && within(t.Bytes, w.Bytes, tol) && within(t.Packets, w.Packets, tol),
			"datagramas %d/%d, plantillas %d/%d, registros %d/%d (v4 %d/%d, v6 %d/%d), bytes %d/%d, paquetes %d/%d",
			t.Datagrams, w.Datagrams, t.TemplateSends, w.TemplateSends, t.DataRecords, w.DataRecords,
			t.RecordsV4, w.RecordsV4, t.RecordsV6, w.RecordsV6, t.Bytes, w.Bytes, t.Packets, w.Packets)

		if lost {
			for _, n := range []string{"atribución por estado", "clientes", "fuera de prefijos"} {
				rep.skip(p+n, fmt.Sprintf("pérdida UDP (%d/%d datagramas)", t.Datagrams, w.Datagrams))
			}
		} else {
			rep.add(p+"atribución por estado", mapsEqual(got.ByStatus, ex.ByStatus), "%s (esperado %s)", fmtStatus(got.ByStatus), fmtStatus(ex.ByStatus))
			rep.add(p+"atribución por regla (§4.4)", mapsEqual(got.ByRule, ex.ByRule), "%s (esperado %s)", fmtStatus(got.ByRule), fmtStatus(ex.ByRule))
			ok, detail := clientsEqual(got.Clients, ex.Clients)
			rep.add(p+"clientes", ok, "%s", detail)
			ok, detail = unattributedEqual(got.Unattributed, ex.Unattributed)
			rep.add(p+"fuera de prefijos", ok, "%s", detail)
		}
		v.templateCheck(rep, p, es, ex.Templates)
		v.natCheck(rep, p, got.Clients)
		v.natRuleCheck(rep, p, es)
		v.ipv6Check(rep, p, got.Clients, ex.Clients)
	}
	v.signalChecks(rep, lossy)
	rep.OK = true
	for _, c := range rep.Checks {
		rep.OK = rep.OK && c.OK
	}
	return rep
}

func fmtStatus(m map[string]uint64) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = fmt.Sprintf("%s=%d", k, m[k])
	}
	return strings.Join(parts, " ")
}

func mapsEqual(a, b map[string]uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func clientsEqual(got, want []expect.Client) (bool, string) {
	gm := map[string]expect.Client{}
	for _, c := range got {
		gm[c.Key] = c
	}
	var diffs []string
	v6 := 0
	for _, w := range want {
		if w.Family == 6 {
			v6++
		}
		g, ok := gm[w.Key]
		if !ok {
			diffs = append(diffs, "falta "+w.Key)
			continue
		}
		if g.Records != w.Records || g.BytesUp != w.BytesUp || g.BytesDown != w.BytesDown || g.PacketsUp != w.PacketsUp || g.PacketsDown != w.PacketsDown {
			diffs = append(diffs, fmt.Sprintf("%s: %d reg %d/%d B (esperado %d reg %d/%d B)", w.Key, g.Records, g.BytesUp, g.BytesDown, w.Records, w.BytesUp, w.BytesDown))
		}
		delete(gm, w.Key)
	}
	for k := range gm {
		diffs = append(diffs, "sobra "+k)
	}
	sort.Strings(diffs)
	if len(diffs) > 0 {
		if len(diffs) > maxMessages {
			diffs = append(diffs[:maxMessages], fmt.Sprintf("… (%d diferencias)", len(diffs)))
		}
		return false, strings.Join(diffs, "; ")
	}
	return true, fmt.Sprintf("%d clientes (%d IPv4, %d IPv6) con registros y bytes exactos", len(want), len(want)-v6, v6)
}

func unattributedEqual(got, want []expect.Unattributed) (bool, string) {
	if len(got) != len(want) {
		return false, fmt.Sprintf("%d IPs fuera de prefijos (esperadas %d)", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			return false, fmt.Sprintf("%s:%d (esperado %s:%d)", got[i].IP, got[i].Records, want[i].IP, want[i].Records)
		}
	}
	return true, fmt.Sprintf("%d IPs fuera de prefijos", len(got))
}

// natCheck: con NAT en el router principal (D12), las IPv4 de cliente deben
// ser privadas/CGNAT en subida y bajada (docs/vendors/mikrotik.md §2.4).
func (v *Verifier) natCheck(rep *Report, p string, clients []expect.Client) {
	if !v.exp.NAT {
		rep.skip(p+"NAT: IPs de cliente privadas", "escenario sin NAT")
		return
	}
	var public []string
	n := 0
	for _, c := range clients {
		if c.Family != 4 {
			continue
		}
		n++
		ip, err := netip.ParseAddr(c.Key)
		if err != nil || !flow.IsClientPrivate(ip) {
			public = append(public, c.Key)
		}
	}
	rep.add(p+"NAT: IPs de cliente privadas", len(public) == 0, "%d clientes IPv4, %d públicos %v", n, len(public), public)
}

// templateCheck compara las plantillas decodificadas con las declaradas
// (ID, orden de IEs y longitudes).
func (v *Verifier) templateCheck(rep *Report, p string, es *exporterState, want []expect.Template) {
	if len(want) == 0 {
		rep.skip(p+"plantillas", "expected.json no declara plantillas")
		return
	}
	var diffs []string
	for _, w := range want {
		g, ok := es.templates[w.ID]
		switch {
		case !ok:
			diffs = append(diffs, fmt.Sprintf("falta la plantilla %d", w.ID))
		case !fieldsEqual(g, w.Fields):
			diffs = append(diffs, fmt.Sprintf("plantilla %d con %d campos distintos de los %d esperados", w.ID, len(g), len(w.Fields)))
		}
	}
	if len(es.templates) != len(want) {
		diffs = append(diffs, fmt.Sprintf("%d plantillas recibidas, %d esperadas", len(es.templates), len(want)))
	}
	if es.tmplChange > 0 {
		diffs = append(diffs, fmt.Sprintf("%d redefiniciones con campos distintos", es.tmplChange))
	}
	ids := make([]string, 0, len(want))
	for _, w := range want {
		ids = append(ids, fmt.Sprintf("%d (%d campos)", w.ID, len(w.Fields)))
	}
	detail := "idénticas: " + strings.Join(ids, ", ")
	if len(diffs) > 0 {
		detail = strings.Join(diffs, "; ")
	}
	rep.add(p+"plantillas", len(diffs) == 0, "%s", detail)
}

// natRuleCheck: con NAT en el router y campos NAT (docs/traffic-model.md
// §4.4) la bajada se atribuye por IE 226 y la IP pública es del pool.
func (v *Verifier) natRuleCheck(rep *Report, p string, es *exporterState) {
	name := p + "NAT: bajada por postNATDst (IE 226)"
	if !v.exp.NAT || !v.exp.NATFields {
		rep.skip(name, "escenario sin NAT o sin campos NAT")
		return
	}
	rep.add(name, es.natDown > 0 && es.natUp > 0 && es.natOutside == 0,
		"%d subidas con postNATSrc pública, %d bajadas atribuidas por IE 226, %d con IP pública fuera de nat_ips",
		es.natUp, es.natDown, es.natOutside)
}

func (v *Verifier) ipv6Check(rep *Report, p string, got, want []expect.Client) {
	if !v.exp.IPv6 {
		rep.skip(p+"IPv6: prefijos delegados", "IPv6 desactivado")
		return
	}
	wantV6 := 0
	for _, c := range want {
		if c.Family == 6 {
			wantV6++
		}
	}
	if wantV6 == 0 {
		rep.skip(p+"IPv6: prefijos delegados", "el escenario no tiene clientes IPv6 en este nodo")
		return
	}
	n := 0
	for _, c := range got {
		if c.Family == 6 {
			if pf, err := netip.ParsePrefix(c.Key); err == nil && pf.Bits() <= 64 {
				n++
			}
		}
	}
	rep.add(p+"IPv6: prefijos delegados", n > 0, "%d clientes IPv6 identificados por prefijo delegado", n)
}

func detailJSON(d map[string]any) string {
	b, err := json.Marshal(d)
	if err != nil {
		return fmt.Sprint(d)
	}
	return string(b)
}

func (v *Verifier) signalChecks(rep *Report, lossy bool) {
	got := v.acc.Results()
	gm := map[string]signals.Signal{}
	for _, s := range got {
		gm[s.Key()] = s
	}
	wm := map[string]signals.Signal{}
	for _, s := range v.exp.Signals {
		wm[s.Key()] = s
	}
	if lossy {
		missing := 0
		for k := range wm {
			if _, ok := gm[k]; !ok {
				missing++
			}
		}
		rep.add("señales", missing == 0, "%d de %d señales esperadas presentes (comparación de detalle omitida por pérdida UDP)", len(wm)-missing, len(wm))
	} else {
		var diffs []string
		for k, w := range wm {
			g, ok := gm[k]
			switch {
			case !ok:
				diffs = append(diffs, "falta "+k)
			case detailJSON(g.Detail) != detailJSON(w.Detail):
				diffs = append(diffs, fmt.Sprintf("%s: %s (esperado %s)", k, detailJSON(g.Detail), detailJSON(w.Detail)))
			}
		}
		for k := range gm {
			if _, ok := wm[k]; !ok {
				diffs = append(diffs, "inesperada "+k)
			}
		}
		sort.Strings(diffs)
		var names []string
		for _, s := range v.exp.Signals {
			names = append(names, s.Client+":"+s.Name)
		}
		detail := fmt.Sprintf("%d señales idénticas a las esperadas %v", len(wm), names)
		if len(wm) == 0 {
			detail = "ninguna señal, como se esperaba"
		}
		if len(diffs) > 0 {
			detail = strings.Join(diffs, "; ")
		}
		rep.add("señales", len(diffs) == 0, "%s", detail)
	}
	var fails []string
	for _, f := range v.exp.Findings {
		for _, sn := range f.Signals {
			if _, ok := gm[f.Exporter+"|"+f.Client+"|"+sn]; !ok {
				fails = append(fails, fmt.Sprintf("%s %s %s sin señal %s", f.Exporter, f.Client, f.Kind, sn))
			}
		}
	}
	var kinds []string
	for _, f := range v.exp.Findings {
		kinds = append(kinds, f.Client+":"+f.Kind+"/"+f.Severity)
	}
	detail := fmt.Sprintf("%d hallazgos esperados sustentados %v", len(v.exp.Findings), kinds)
	if len(v.exp.Findings) == 0 {
		detail = "sin hallazgos esperados"
	}
	if len(fails) > 0 {
		detail = strings.Join(fails, "; ")
	}
	rep.add("hallazgos", len(fails) == 0, "%s", detail)
}
