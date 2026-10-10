// Package app es la lógica del collector: identifica el exportador por su IP
// de túnel, decodifica, mide la secuencia, filtra (túnel, excluded), agrupa
// en lotes por router y los publica sin bloquear la recepción UDP.
package app

import (
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
	"github.com/hcdestroyer/horus-flow/services/collector/internal/decode"
)

func uintString(v uint64) string { return strconv.FormatUint(v, 10) }

// Datagram es un datagrama recibido.
type Datagram struct {
	Src     netip.AddrPort
	Payload []byte
	At      time.Time
}

// Worker procesa los datagramas de un subconjunto de exportadores (el
// reparto por IP mantiene las plantillas y la secuencia en un solo hilo).
type Worker struct {
	inv     *flowinv.Store
	dec     *decode.Decoder
	seq     map[seqKey]*seqTracker
	batcher *Batcher
	states  *States
	m       *Metrics
}

type seqKey struct {
	ip   netip.Addr
	odid uint32
	ver  uint16
}

// NewWorker crea un trabajador.
func NewWorker(inv *flowinv.Store, b *Batcher, st *States, m *Metrics, pendingTTL time.Duration) *Worker {
	return &Worker{inv: inv, dec: decode.New(decode.Options{PendingTTL: pendingTTL}),
		seq: map[seqKey]*seqTracker{}, batcher: b, states: st, m: m}
}

// Handle procesa un datagrama: las tres etapas seguidas en este hilo
// (HORUS_COLLECTOR_DECODE_WORKERS=1). Con más hilos, el motor ejecuta prepare
// y post en serie por carril y decode en el pool (parallel.go).
func (w *Worker) Handle(d Datagram) {
	j := w.prepare(d)
	if j == nil {
		return
	}
	j.decode(w.m)
	w.post(j)
}

// dgram es un datagrama en curso entre etapas.
type dgram struct {
	d    Datagram
	ip   netip.Addr
	inv  *flowinv.Snapshot
	exp  *flowinv.Exporter
	plan *decode.Plan
	res  decode.Result
	err  error
	// total son los registros decodificados (antes de filtrar); recs los que
	// pasan los filtros de túnel y excluded.
	total int
	recs  []flowpb.FlowRecord
	done  chan struct{}
}

// prepare (serie, por exportador): identifica el exportador y hace la parte
// de la decodificación que toca estado (plantillas, retenidos, opciones).
// Devuelve nil si el datagrama no sigue (exportador desconocido).
func (w *Worker) prepare(d Datagram) *dgram {
	inv := w.inv.Load()
	ip := d.Src.Addr().Unmap()
	exp, ok := inv.Exporter(ip)
	if !ok {
		w.m.Dropped.WithLabelValues(DropUnknownExporter).Inc()
		w.states.Unregistered(ip, d.At)
		return nil
	}
	return &dgram{d: d, ip: ip, inv: inv, exp: exp, plan: w.dec.Prepare(ip.String(), d.Payload)}
}

// decode (cualquier hilo): decodifica los conjuntos de datos y filtra
// registros del túnel y de prefijos excluded. Solo lee datos inmutables (la
// plantilla, el datagrama, la instantánea del inventario).
func (j *dgram) decode(m *Metrics) {
	for _, job := range j.plan.Jobs {
		job.Run()
	}
	j.res, j.err = j.plan.Finish()
	j.total = len(j.res.Records)
	sampling := j.plan.Sampling
	recs := j.res.Records[:0]
	for i := range j.res.Records {
		r := &j.res.Records[i]
		if r.SrcIP == j.ip || r.DstIP == j.ip {
			m.Dropped.WithLabelValues(DropTunnel).Inc()
			continue
		}
		if j.inv.ExcludedFor(j.exp.SiteID, r.SrcIP) || j.inv.ExcludedFor(j.exp.SiteID, r.DstIP) {
			m.Dropped.WithLabelValues(DropExcluded).Inc()
			continue
		}
		r.ExporterIP = j.ip
		if r.SamplingRate == 0 && sampling > 1 {
			r.SamplingRate = sampling
		}
		recs = append(recs, *r)
	}
	j.recs = recs
}

// post (serie, en orden de llegada por exportador): secuencia y huecos por
// dominio de observación, estado del exportador y lotes.
func (w *Worker) post(j *dgram) {
	res, err, exp := j.res, j.err, j.exp
	if err != nil {
		w.m.Dropped.WithLabelValues(DropMalformed).Inc()
		if res.Header.Version == 0 {
			return
		}
	}
	w.m.Datagrams.WithLabelValues(strconv.Itoa(int(res.Header.Version))).Inc()
	w.m.ReceivedRecords.Add(float64(j.total))
	if res.DroppedNoTemplate > 0 {
		w.m.Dropped.WithLabelValues(DropNoTemplate).Add(float64(res.DroppedNoTemplate))
	}
	k := seqKey{ip: j.ip, odid: res.Header.ODID, ver: res.Header.Version}
	tr := w.seq[k]
	if tr == nil {
		tr = &seqTracker{}
		w.seq[k] = tr
	}
	var lost uint64
	if err == nil {
		lost = tr.observe(res.Header.Version == decode.VersionIPFIX, res.Header.Sequence, res.DataRecords+res.OptionsRecords)
	}
	rid := exp.RouterID.String()
	if lost > 0 {
		w.m.SeqGaps.WithLabelValues(rid).Inc()
		w.m.LostRecords.WithLabelValues(rid).Add(float64(lost))
	}
	skew := res.Header.ExportTime.Sub(j.d.At)
	w.m.ClockSkew.WithLabelValues(rid).Set(skew.Seconds())
	source := "ipfix"
	if res.Header.Version == decode.VersionV9 {
		source = "netflow_v9"
	}
	w.states.Observe(exp, Observation{At: j.d.At, Records: j.total, Lost: lost, Skew: skew,
		FlowSource: source, Sampling: j.plan.Sampling, Gap: lost > 0})
	if len(j.recs) > 0 {
		w.batcher.Add(exp, j.recs, j.d.At)
	}
}

// ---------------------------------------------------------------- lotes

type openBatch struct {
	fb     flowpb.FlowBatch
	opened time.Time
}

// Batcher agrupa registros por router en lotes de ≤ MaxRecords o ≤ MaxAge.
type Batcher struct {
	MaxRecords  int
	MaxAge      time.Duration
	CollectorID string
	pub         *Publisher

	mu   sync.Mutex
	open map[uuid.UUID]*openBatch
}

// NewBatcher crea el agrupador.
func NewBatcher(pub *Publisher, maxRecords int, maxAge time.Duration, collectorID string) *Batcher {
	return &Batcher{MaxRecords: maxRecords, MaxAge: maxAge, CollectorID: collectorID, pub: pub, open: map[uuid.UUID]*openBatch{}}
}

// Add añade registros del exportador exp.
func (b *Batcher) Add(exp *flowinv.Exporter, recs []flowpb.FlowRecord, at time.Time) {
	var ready []*openBatch
	b.mu.Lock()
	for len(recs) > 0 {
		ob := b.open[exp.RouterID]
		if ob == nil {
			ob = &openBatch{opened: at, fb: flowpb.FlowBatch{
				CollectorID: b.CollectorID, TenantID: exp.TenantID.String(), RouterID: exp.RouterID.String(),
				ExporterIP: exp.TunnelIP.Unmap(), SamplingRate: 1, ReceivedFrom: at,
				Records: make([]flowpb.FlowRecord, 0, b.MaxRecords)}}
			b.open[exp.RouterID] = ob
		}
		n := min(b.MaxRecords-len(ob.fb.Records), len(recs))
		ob.fb.Records = append(ob.fb.Records, recs[:n]...)
		ob.fb.ReceivedTo = at
		recs = recs[n:]
		if len(ob.fb.Records) >= b.MaxRecords {
			delete(b.open, exp.RouterID)
			ready = append(ready, ob)
		}
	}
	b.mu.Unlock()
	for _, ob := range ready {
		b.emit(ob)
	}
}

// Flush publica los lotes abiertos más viejos que MaxAge (todos si force).
func (b *Batcher) Flush(now time.Time, force bool) {
	var ready []*openBatch
	b.mu.Lock()
	for k, ob := range b.open {
		if force || now.Sub(ob.opened) >= b.MaxAge {
			delete(b.open, k)
			ready = append(ready, ob)
		}
	}
	b.mu.Unlock()
	for _, ob := range ready {
		b.emit(ob)
	}
}

func (b *Batcher) emit(ob *openBatch) {
	id, err := uuid.NewV7()
	if err != nil {
		id = uuid.New()
	}
	fb := &ob.fb
	fb.BatchID = id.String()
	for i := range fb.Records {
		fb.Records[i].BatchID = fb.BatchID
	}
	msg := flowbus.Telemetry{
		Subject: flowbus.SubjectBatchPrefix + fb.RouterID, Type: flowbus.TypeBatchReceived,
		Source: "horus/flows/collector", TenantID: fb.TenantID, MsgID: fb.BatchID,
		ContentType: flowpb.ContentTypeFlowBatch, Time: fb.ReceivedTo, Body: fb.Marshal(),
	}.Msg()
	b.pub.Enqueue(msg, len(fb.Records))
}
