package realtime

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
)

// TicketTTL es la vida de un ticket (api.md §4.2).
const TicketTTL = 30 * time.Second

// tickets guarda `ws_ticket:<sha256(ticket)>` → principal con TTL y canje
// único (GETDEL). En v1 vive en memoria del gateway (una réplica); con varias
// réplicas se mueve a Valkey sin cambiar la interfaz.
type tickets struct {
	mu  sync.Mutex
	m   map[[32]byte]ticketEntry
	now func() time.Time
}

type ticketEntry struct {
	p   *authz.Principal
	exp time.Time
}

func newTickets(now func() time.Time) *tickets {
	return &tickets{m: map[[32]byte]ticketEntry{}, now: now}
}

// Issue emite un ticket de 256 bits para p.
func (t *tickets) Issue(p *authz.Principal) (string, time.Time) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	tk := base64.RawURLEncoding.EncodeToString(b)
	exp := t.now().Add(TicketTTL)
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, e := range t.m { // limpieza perezosa
		if t.now().After(e.exp) {
			delete(t.m, k)
		}
	}
	t.m[sha256.Sum256([]byte(tk))] = ticketEntry{p: p, exp: exp}
	return tk, exp
}

// Redeem canjea un ticket (un solo uso).
func (t *tickets) Redeem(tk string) (*authz.Principal, bool) {
	if tk == "" || len(tk) > 128 {
		return nil, false
	}
	k := sha256.Sum256([]byte(tk))
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.m[k]
	delete(t.m, k)
	if !ok || t.now().After(e.exp) {
		return nil, false
	}
	return e.p, true
}

// ring guarda los últimos eventos de un tenant para la reanudación.
type ring struct {
	mu     sync.Mutex
	items  []ringItem
	size   int
	window time.Duration
}

type ringItem struct {
	id     uuid.UUID
	at     time.Time
	topics []string
	ev     *outEvent
}

func (r *ring) add(it ringItem) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, it)
	if len(r.items) > r.size {
		r.items = r.items[len(r.items)-r.size:]
	}
}

// since devuelve los eventos posteriores a last para topic; ok = false si
// last no está en la ventana (el cliente debe pedir un snapshot REST).
func (r *ring) since(last uuid.UUID, now time.Time) ([]ringItem, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, it := range r.items {
		if it.id == last {
			if now.Sub(it.at) > r.window {
				return nil, false
			}
			return append([]ringItem(nil), r.items[i+1:]...), true
		}
	}
	return nil, false
}
