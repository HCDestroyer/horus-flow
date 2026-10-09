package realtime

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
)

func TestTopicsForType(t *testing.T) {
	id := uuid.NewString()
	for typ, want := range map[string]string{
		"horus.detection.finding.opened":                  "security",
		"horus.detection.customer.security_state_changed": "security",
		"horus.devices.customer.discovered":               "customers",
		"horus.devices.router.updated":                    "routers",
		"horus.flows.exporter.silent":                     "exporters",
		"horus.wireguard.peer.activated":                  "wireguard.peers",
		"horus.analytics.dashboard.updated":               "dashboard." + id,
		"horus.analytics.playlist.updated":                "dashboard.*",
		"horus.auth.session.revoked":                      "me",
	} {
		if got := TopicsForType(typ, id); !slices.Contains(got, want) {
			t.Errorf("%s → %v, quiero %s", typ, got, want)
		}
	}
	if TopicsForType("horus.devices.audit.recorded", id) != nil || TopicsForType("x", id) != nil {
		t.Error("auditoría o tipo inválido no debe ir a ningún topic")
	}
	if _, ok := LookupTopic("dashboard.no-uuid"); ok {
		t.Error("dashboard.<no uuid> aceptado")
	}
}

func TestStripPII(t *testing.T) {
	out := StripPII(json.RawMessage(`{"id":"x","address":"10.0.0.1","alias":"Ferretería","kind":"residential"}`), Topics["customers"].PII)
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if _, ok := m["address"]; ok || m["alias"] != nil || m["kind"] != "residential" {
		t.Fatalf("pii = %s", out)
	}
}

func TestTicketsSingleUseAndTTL(t *testing.T) {
	now := time.Now()
	tk := newTickets(func() time.Time { return now })
	p := &authz.Principal{Subject: "u"}
	ticket, exp := tk.Issue(p)
	if len(ticket) != 43 || exp.Sub(now) != TicketTTL {
		t.Fatalf("ticket %q exp %v", ticket, exp)
	}
	if got, ok := tk.Redeem(ticket); !ok || got != p {
		t.Fatal("primer canje")
	}
	if _, ok := tk.Redeem(ticket); ok {
		t.Fatal("segundo canje aceptado")
	}
	t2, _ := tk.Issue(p)
	now = now.Add(31 * time.Second)
	if _, ok := tk.Redeem(t2); ok {
		t.Fatal("ticket caducado aceptado")
	}
}

func TestRingResume(t *testing.T) {
	r := &ring{size: 3, window: time.Minute}
	now := time.Now()
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	for _, id := range ids {
		r.add(ringItem{id: id, at: now, topics: []string{"security"}})
	}
	items, ok := r.since(ids[1], now)
	if !ok || len(items) != 2 || items[0].id != ids[2] {
		t.Fatalf("since = %v %v", items, ok)
	}
	if _, ok := r.since(ids[0], now); ok {
		t.Fatal("fuera del tamaño de la ventana")
	}
	if _, ok := r.since(ids[2], now.Add(2*time.Minute)); ok {
		t.Fatal("fuera de la ventana temporal")
	}
}
