// Package flowstate reconstruye al arrancar el estado en memoria que un rol
// de flows deriva de un stream de eventos (D23: ningún estado vive solo en
// memoria).
//
// El patrón que falló en flowinv (un durable con el estado en memoria: tras
// reiniciar solo llegaba lo no confirmado) se sustituye por:
//
//  1. una instantánea del estado con la secuencia del último evento aplicado,
//     guardada en el Object Store `flows_state` de JetStream (sobrevive a la
//     retención del stream: DEVICES_EVENTS caduca a los 30 días y un router
//     dado de alta hace 31 ya no estaría en el stream);
//  2. al arrancar se restaura la instantánea y se lee el stream con un
//     consumidor efímero sin acks desde la secuencia siguiente (sin
//     instantánea, desde el principio);
//  3. mientras corre, la instantánea se reescribe cuando cambia el estado
//     (como mucho cada SaveEvery) y al alcanzar el final del stream.
//
// Apply debe ser idempotente: tras un corte entre la instantánea y el
// siguiente guardado se reaplican eventos ya vistos.
package flowstate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Bucket es el Object Store de las instantáneas de estado de flows.
const Bucket = "flows_state"

// Store guarda instantáneas por nombre.
type Store interface {
	// Load devuelve la instantánea o (nil, nil) si no existe.
	Load(ctx context.Context, name string) ([]byte, error)
	Save(ctx context.Context, name string, b []byte) error
}

// ObjectStore adapta un jetstream.ObjectStore.
type ObjectStore struct{ OS jetstream.ObjectStore }

// Open crea (o abre) el Object Store de instantáneas.
func Open(ctx context.Context, js jetstream.JetStream) (*ObjectStore, error) {
	os, err := js.CreateOrUpdateObjectStore(ctx, jetstream.ObjectStoreConfig{Bucket: Bucket,
		Description: "Instantáneas del estado de flows reconstruible tras un reinicio (D23)",
		Storage:     jetstream.FileStorage})
	if err != nil {
		return nil, fmt.Errorf("flowstate: object store %s: %w", Bucket, err)
	}
	return &ObjectStore{OS: os}, nil
}

// Load implementa Store.
func (o *ObjectStore) Load(ctx context.Context, name string) ([]byte, error) {
	r, err := o.OS.Get(ctx, name)
	if errors.Is(err, jetstream.ErrObjectNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}

// Save implementa Store.
func (o *ObjectStore) Save(ctx context.Context, name string, b []byte) error {
	_, err := o.OS.Put(ctx, jetstream.ObjectMeta{Name: name}, bytes.NewReader(b))
	return err
}

// MemStore es un Store en memoria (tests).
type MemStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

// Load implementa Store.
func (s *MemStore) Load(_ context.Context, name string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.m[name]...), nil
}

// Save implementa Store.
func (s *MemStore) Save(_ context.Context, name string, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string][]byte{}
	}
	s.m[name] = append([]byte(nil), b...)
	return nil
}

// snapshot es el formato guardado.
type snapshot struct {
	Version int             `json:"version"`
	Stream  string          `json:"stream"`
	Seq     uint64          `json:"seq"`
	SavedAt time.Time       `json:"saved_at"`
	State   json.RawMessage `json:"state"`
}

// Replay mantiene un estado derivado de un stream.
type Replay struct {
	JS      jetstream.JetStream
	Stream  string
	Filters []string
	// Store guarda la instantánea (nil = siempre se relee el stream entero).
	Store Store
	// Name es el nombre de la instantánea (uno por rol y estado).
	Name string
	// Apply aplica un evento; changed indica si el estado cambió.
	Apply func(subject string, data []byte) (changed bool, err error)
	// Snapshot serializa el estado actual; Restore lo sustituye.
	Snapshot func() (json.RawMessage, error)
	Restore  func(json.RawMessage) error
	// Changed se llama tras aplicar un bloque con cambios (y tras Restore).
	Changed func()
	// SaveEvery acota la frecuencia de guardado (30 s por defecto).
	SaveEvery time.Duration
	// Retry es la espera si el stream aún no existe (30 s).
	Retry time.Duration
	Log   *slog.Logger
	// Legacy es el durable del patrón antiguo, que se borra si existe.
	Legacy string

	mu      sync.Mutex
	seq     uint64
	dirty   bool
	savedAt time.Time
}

// Seq devuelve la secuencia del último evento aplicado.
func (r *Replay) Seq() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seq
}

func (r *Replay) log() *slog.Logger {
	if r.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return r.Log
}

// restore carga la instantánea si la hay.
func (r *Replay) restore(ctx context.Context) {
	if r.Store == nil || r.Restore == nil {
		return
	}
	b, err := r.Store.Load(ctx, r.Name)
	if err != nil {
		r.log().Warn("state snapshot not loaded: replaying the whole stream", "name", r.Name, "error", err)
		return
	}
	if len(b) == 0 {
		return
	}
	var s snapshot
	if err := json.Unmarshal(b, &s); err != nil || s.Stream != r.Stream {
		r.log().Warn("state snapshot ignored: replaying the whole stream", "name", r.Name, "error", err)
		return
	}
	if err := r.Restore(s.State); err != nil {
		r.log().Warn("state snapshot rejected: replaying the whole stream", "name", r.Name, "error", err)
		return
	}
	r.mu.Lock()
	r.seq = s.Seq
	r.savedAt = s.SavedAt
	r.mu.Unlock()
	if r.Changed != nil {
		r.Changed()
	}
	r.log().Info("state restored from snapshot", "name", r.Name, "stream", r.Stream, "seq", s.Seq, "saved_at", s.SavedAt)
}

// Save guarda la instantánea ahora (si hay Store).
func (r *Replay) Save(ctx context.Context) error {
	if r.Store == nil || r.Snapshot == nil {
		return nil
	}
	r.mu.Lock()
	seq := r.seq
	r.mu.Unlock()
	st, err := r.Snapshot()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	b, err := json.Marshal(snapshot{Version: 1, Stream: r.Stream, Seq: seq, SavedAt: now, State: st})
	if err != nil {
		return err
	}
	if err := r.Store.Save(ctx, r.Name, b); err != nil {
		return err
	}
	r.mu.Lock()
	r.savedAt = now
	if r.seq == seq {
		r.dirty = false
	}
	r.mu.Unlock()
	return nil
}

func (r *Replay) maybeSave(ctx context.Context, force bool) {
	r.mu.Lock()
	due := r.dirty && (force || time.Since(r.savedAt) >= r.SaveEvery)
	r.mu.Unlock()
	if !due {
		return
	}
	if err := r.Save(ctx); err != nil {
		r.log().Warn("state snapshot not saved", "name", r.Name, "error", err)
	}
}

// Run restaura la instantánea y aplica el stream hasta que ctx se cancela.
// ready se cierra al alcanzar el final del stream por primera vez (o si el
// stream no existe).
func (r *Replay) Run(ctx context.Context, ready chan<- struct{}) {
	if r.SaveEvery <= 0 {
		r.SaveEvery = 30 * time.Second
	}
	if r.Retry <= 0 {
		r.Retry = 30 * time.Second
	}
	var once sync.Once
	markReady := func() {
		if ready != nil {
			once.Do(func() { close(ready) })
		}
	}
	r.restore(ctx)
	defer r.maybeSave(context.WithoutCancel(ctx), true)
	cleaned := r.Legacy == ""
	for ctx.Err() == nil {
		if !cleaned {
			if err := r.JS.DeleteConsumer(ctx, r.Stream, r.Legacy); err == nil || errors.Is(err, jetstream.ErrConsumerNotFound) {
				cleaned = true
			}
		}
		cons, err := r.consumer(ctx)
		if err != nil {
			markReady()
			select {
			case <-ctx.Done():
				return
			case <-time.After(r.Retry):
				continue
			}
		}
		r.consume(ctx, cons, markReady)
	}
}

// consumer crea el consumidor efímero desde la secuencia siguiente a la
// aplicada. Si el stream ya no tiene esa secuencia (retención) lo avisa: la
// instantánea conserva lo anterior y solo faltan los eventos caducados entre
// ella y el principio actual del stream.
func (r *Replay) consumer(ctx context.Context) (jetstream.Consumer, error) {
	st, err := r.JS.Stream(ctx, r.Stream)
	if err != nil {
		return nil, err
	}
	info := st.CachedInfo()
	seq := r.Seq()
	cfg := jetstream.ConsumerConfig{AckPolicy: jetstream.AckNonePolicy, FilterSubjects: r.Filters,
		InactiveThreshold: 5 * time.Minute, DeliverPolicy: jetstream.DeliverAllPolicy}
	switch {
	case seq == 0:
	case info != nil && seq > info.State.LastSeq:
		// El stream es más corto que la instantánea: se recreó (volumen de
		// NATS nuevo). Se conserva la instantánea y se relee entero.
		r.log().Warn("stream behind the state snapshot (recreated?): replaying it whole over the snapshot",
			"name", r.Name, "stream", r.Stream, "snapshot_seq", seq, "stream_last_seq", info.State.LastSeq)
		r.mu.Lock()
		r.seq = 0
		r.mu.Unlock()
	default:
		if info != nil && info.State.FirstSeq > seq+1 {
			r.log().Warn("events expired between the state snapshot and the stream start",
				"name", r.Name, "stream", r.Stream, "snapshot_seq", seq, "stream_first_seq", info.State.FirstSeq)
		}
		cfg.DeliverPolicy, cfg.OptStartSeq = jetstream.DeliverByStartSequencePolicy, seq+1
	}
	return r.JS.CreateConsumer(ctx, r.Stream, cfg)
}

func (r *Replay) consume(ctx context.Context, cons jetstream.Consumer, markReady func()) {
	for ctx.Err() == nil {
		batch, err := cons.FetchNoWait(256)
		if err != nil {
			return // se recrea desde la última secuencia aplicada
		}
		changed, n := false, 0
		for m := range batch.Messages() {
			n++
			md, err := m.Metadata()
			if err != nil {
				continue
			}
			c, err := r.Apply(m.Subject(), m.Data())
			if err != nil {
				r.log().Warn("event ignored by state replay", "name", r.Name, "subject", m.Subject(), "error", err)
			}
			r.mu.Lock()
			if md.Sequence.Stream > r.seq {
				r.seq = md.Sequence.Stream
			}
			if c {
				r.dirty = true
			}
			r.mu.Unlock()
			changed = changed || c
		}
		if changed && r.Changed != nil {
			r.Changed()
		}
		if n == 0 {
			markReady()
			r.maybeSave(ctx, false)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
}
