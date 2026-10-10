package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/services/collector/internal/decode"
)

// Estado del collector que sobrevive a un reinicio (D23), en el bucket KV
// flow_collector_state:
//
//   - tpl.<ip>.<odid>.<versión>.<id>: plantillas IPFIX/v9 de cada exportador.
//     Al arrancar se reinstalan (si no superan TemplateTTL) y los datos que
//     llegan antes de que el router reenvíe la plantilla se decodifican en
//     vez de quedar retenidos o descartarse.
//   - seq.<ip>.<odid>.<versión>: la secuencia esperada de cada dominio de
//     observación. El primer datagrama tras el arranque mide lo que el
//     exportador envió mientras el collector estaba caído
//     (horus_collector_downtime_lost_records_total y data_gap con reason
//     collector_down), sin contarlo como pérdida del exportador.
//
// Se escribe en segundo plano (la decodificación nunca espera al KV): las
// plantillas cuando cambian (o cada 10 min para renovar su antigüedad) y las
// secuencias como mucho cada segundo.

// CollectorStateBucket es el bucket KV del estado del collector.
const CollectorStateBucket = "flow_collector_state"

// StateKV es lo que el collector necesita del bucket.
type StateKV interface {
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	// List devuelve las claves con el prefijo y sus valores.
	List(ctx context.Context, prefix string) (map[string][]byte, error)
}

// JetStreamStateKV adapta jetstream.KeyValue.
type JetStreamStateKV struct{ KV jetstream.KeyValue }

// Put implementa StateKV.
func (k JetStreamStateKV) Put(ctx context.Context, key string, v []byte) error {
	_, err := k.KV.Put(ctx, key, v)
	return err
}

// Delete implementa StateKV.
func (k JetStreamStateKV) Delete(ctx context.Context, key string) error {
	err := k.KV.Delete(ctx, key)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil
	}
	return err
}

// List implementa StateKV.
func (k JetStreamStateKV) List(ctx context.Context, prefix string) (map[string][]byte, error) {
	keys, err := k.KV.ListKeysFiltered(ctx, prefix+">")
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for key := range keys.Keys() {
		e, err := k.KV.Get(ctx, key)
		if err != nil {
			continue
		}
		out[key] = e.Value()
	}
	return out, nil
}

type persistedTemplate struct {
	Fields  []decode.Field `json:"fields"`
	Scope   int            `json:"scope"`
	Options bool           `json:"options"`
	At      time.Time      `json:"at"`
}

type persistedSeq struct {
	Next    uint32    `json:"next"`
	AvgRecs float64   `json:"avg_recs"`
	At      time.Time `json:"at"`
}

func ipKey(a netip.Addr) string { b := a.Unmap().As16(); return hex.EncodeToString(b[:]) }

func parseIPKey(s string) (netip.Addr, bool) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 {
		return netip.Addr{}, false
	}
	return netip.AddrFrom16([16]byte(b)).Unmap(), true
}

func tplKey(ip netip.Addr, odid uint32, ver, id uint16) string {
	return fmt.Sprintf("tpl.%s.%d.%d.%d", ipKey(ip), odid, ver, id)
}

func seqKeyName(k seqKey) string { return fmt.Sprintf("seq.%s.%d.%d", ipKey(k.ip), k.odid, k.ver) }

// persister escribe en segundo plano la última versión de cada clave.
type persister struct {
	kv  StateKV
	log *slog.Logger

	mu      sync.Mutex
	pending map[string][]byte // nil = borrar
	wake    chan struct{}
}

func newPersister(kv StateKV, log *slog.Logger) *persister {
	return &persister{kv: kv, log: log, pending: map[string][]byte{}, wake: make(chan struct{}, 1)}
}

func (p *persister) put(key string, v []byte) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.pending[key] = v
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *persister) flush(ctx context.Context) {
	p.mu.Lock()
	batch := p.pending
	p.pending = map[string][]byte{}
	p.mu.Unlock()
	for k, v := range batch {
		var err error
		if v == nil {
			err = p.kv.Delete(ctx, k)
		} else {
			err = p.kv.Put(ctx, k, v)
		}
		if err != nil {
			p.mu.Lock()
			if _, newer := p.pending[k]; !newer {
				p.pending[k] = v // reintento en la siguiente vuelta
			}
			p.mu.Unlock()
		}
	}
}

// run escribe lo pendiente hasta que ctx se cancela (y una vez más al final).
func (p *persister) run(ctx context.Context) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			p.flush(fctx)
			cancel()
			return
		case <-t.C:
		case <-p.wake:
		}
		p.flush(ctx)
	}
}

// workerPersist es el estado de persistencia de un trabajador (solo lo toca
// su hilo serie: prepare para plantillas, post para secuencias).
type workerPersist struct {
	p        *persister
	tplAt    map[string]time.Time
	tplSig   map[string]string
	seqAt    map[seqKey]time.Time
	interval time.Duration
}

func (wp *workerPersist) template(src string, odid uint32, ver uint16, t *decode.Template, withdrawn bool) {
	ip, err := netip.ParseAddr(src)
	if err != nil {
		return
	}
	key := tplKey(ip, odid, ver, t.ID)
	if withdrawn {
		delete(wp.tplSig, key)
		delete(wp.tplAt, key)
		wp.p.put(key, nil)
		return
	}
	now := time.Now()
	sig := fmt.Sprint(t.Scope, t.Options, t.Fields)
	if wp.tplSig[key] == sig && now.Sub(wp.tplAt[key]) < 10*time.Minute {
		return
	}
	b, err := json.Marshal(persistedTemplate{Fields: t.Fields, Scope: t.Scope, Options: t.Options, At: now.UTC()})
	if err != nil {
		return
	}
	wp.tplSig[key], wp.tplAt[key] = sig, now
	wp.p.put(key, b)
}

func (wp *workerPersist) seq(k seqKey, tr *seqTracker, now time.Time) {
	if now.Sub(wp.seqAt[k]) < wp.interval {
		return
	}
	wp.seqAt[k] = now
	b, err := json.Marshal(persistedSeq{Next: tr.next, AvgRecs: tr.avgRecs, At: now.UTC()})
	if err == nil {
		wp.p.put(seqKeyName(k), b)
	}
}

// RestoreState reinstala plantillas y secuencias guardadas en los
// trabajadores (antes de Run). templateTTL descarta plantillas viejas.
func (e *Engine) RestoreState(ctx context.Context, kv StateKV, templateTTL time.Duration) (templates, seqs int, err error) {
	tpls, err := kv.List(ctx, "tpl.")
	if err != nil {
		return 0, 0, err
	}
	now := time.Now()
	for key, v := range tpls {
		parts := strings.Split(key, ".")
		if len(parts) != 5 {
			continue
		}
		ip, ok := parseIPKey(parts[1])
		odid, e1 := strconv.ParseUint(parts[2], 10, 32)
		ver, e2 := strconv.ParseUint(parts[3], 10, 16)
		id, e3 := strconv.ParseUint(parts[4], 10, 16)
		var pt persistedTemplate
		if !ok || e1 != nil || e2 != nil || e3 != nil || json.Unmarshal(v, &pt) != nil {
			continue
		}
		if templateTTL > 0 && now.Sub(pt.At) > templateTTL {
			continue
		}
		w := e.workerFor(ip)
		if w.dec.Install(ip.String(), uint32(odid), uint16(ver), decode.Template{ID: uint16(id), Fields: pt.Fields, //nolint:gosec // acotados por ParseUint
			Scope: pt.Scope, Options: pt.Options}) == nil {
			templates++
		}
	}
	sq, err := kv.List(ctx, "seq.")
	if err != nil {
		return templates, 0, err
	}
	for key, v := range sq {
		parts := strings.Split(key, ".")
		if len(parts) != 4 {
			continue
		}
		ip, ok := parseIPKey(parts[1])
		odid, e1 := strconv.ParseUint(parts[2], 10, 32)
		ver, e2 := strconv.ParseUint(parts[3], 10, 16)
		var ps persistedSeq
		if !ok || e1 != nil || e2 != nil || json.Unmarshal(v, &ps) != nil {
			continue
		}
		k := seqKey{ip: ip, odid: uint32(odid), ver: uint16(ver)} //nolint:gosec // acotados por ParseUint
		w := e.workerFor(ip)
		w.seq[k] = &seqTracker{init: true, next: ps.Next, avgRecs: ps.AvgRecs, restored: true, restoredAt: ps.At}
		seqs++
	}
	return templates, seqs, nil
}

// UsePersistence activa la escritura del estado en kv (antes de Run).
func (e *Engine) UsePersistence(kv StateKV) {
	e.persist = newPersister(kv, e.log)
	for _, w := range e.workers {
		wp := &workerPersist{p: e.persist, tplAt: map[string]time.Time{}, tplSig: map[string]string{},
			seqAt: map[seqKey]time.Time{}, interval: time.Second}
		w.persist = wp
		w.dec.SetOnTemplate(wp.template)
	}
}
