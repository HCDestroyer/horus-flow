// Package flowpause mantiene el conjunto de ISP con la ingesta de flujos en
// pausa: `POST /platform/tenants/{id}/suspend` con `pause_ingest` (por
// defecto true) publica horus.auth.tenant.suspended y `/resume`
// horus.auth.tenant.resumed (AUTH_EVENTS, docs/events.md). collector e
// ingester descartan y cuentan los flujos de un ISP en pausa y reanudan solos.
//
// El conjunto se reconstruye tras un reinicio (D23) con packages/go/flowstate:
// instantánea en flows_state + AUTH_EVENTS desde la secuencia siguiente.
package flowpause

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/flowstate"
)

// Subjects de AUTH_EVENTS que se aplican.
var Subjects = []string{"horus.auth.tenant.suspended.>", "horus.auth.tenant.resumed.>"}

type state struct {
	Paused  bool `json:"paused"`
	Version int  `json:"version"`
}

// Set es el conjunto de ISP en pausa; seguro para uso concurrente.
type Set struct {
	mu sync.RWMutex
	m  map[uuid.UUID]state
}

// New crea un conjunto vacío.
func New() *Set { return &Set{m: map[uuid.UUID]state{}} }

// Paused dice si la ingesta del ISP está en pausa (nil = nunca).
func (s *Set) Paused(tenant uuid.UUID) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.m[tenant].Paused
}

// PausedString es Paused con el UUID en texto (cabecera Horus-Tenant).
func (s *Set) PausedString(tenant string) bool {
	if s == nil {
		return false
	}
	id, err := uuid.Parse(tenant)
	return err == nil && s.Paused(id)
}

type envelope struct {
	Type             string `json:"type"`
	AggregateVersion int    `json:"aggregate_version"`
	Data             struct {
		ID          uuid.UUID `json:"id"`
		Version     int       `json:"version"`
		PauseIngest *bool     `json:"pause_ingest"`
	} `json:"data"`
}

// Apply aplica un evento suspended/resumed (idempotente por versión).
func (s *Set) Apply(body []byte) (bool, error) {
	var e envelope
	if err := json.Unmarshal(body, &e); err != nil {
		return false, err
	}
	if e.Data.ID == uuid.Nil {
		return false, errors.New("flowpause: event without data.id")
	}
	v := e.Data.Version
	if v == 0 {
		v = e.AggregateVersion
	}
	var paused bool
	switch {
	case strings.HasSuffix(e.Type, ".suspended"):
		paused = e.Data.PauseIngest == nil || *e.Data.PauseIngest
	case strings.HasSuffix(e.Type, ".resumed"):
		paused = false
	default:
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.m[e.Data.ID]
	if ok && v > 0 && v < cur.Version {
		return false, nil // reentrega o desorden
	}
	s.m[e.Data.ID] = state{Paused: paused, Version: v}
	return !ok || cur.Paused != paused, nil
}

// Snapshot serializa el conjunto.
func (s *Set) Snapshot() (json.RawMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.Marshal(s.m)
}

// Restore sustituye el conjunto.
func (s *Set) Restore(b json.RawMessage) error {
	m := map[uuid.UUID]state{}
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	s.mu.Lock()
	s.m = m
	s.mu.Unlock()
	return nil
}

// Run mantiene el conjunto con AUTH_EVENTS hasta que ctx se cancela. name es
// el nombre de la instantánea (uno por rol); states puede ser nil.
func (s *Set) Run(ctx context.Context, js jetstream.JetStream, states flowstate.Store, name string, log *slog.Logger, ready chan<- struct{}) {
	r := &flowstate.Replay{JS: js, Stream: "AUTH_EVENTS", Filters: Subjects, Name: name, Log: log,
		Apply:    func(_ string, data []byte) (bool, error) { return s.Apply(data) },
		Snapshot: s.Snapshot, Restore: s.Restore,
		Changed: func() {
			if log != nil {
				s.mu.RLock()
				n := 0
				for _, st := range s.m {
					if st.Paused {
						n++
					}
				}
				s.mu.RUnlock()
				log.Info("flows ingest pause set updated", "paused_tenants", n)
			}
		}}
	if states != nil {
		r.Store = states
	}
	r.Run(ctx, ready)
}
