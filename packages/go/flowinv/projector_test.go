package flowinv

import (
	"context"
	"encoding/json"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus/flowbustest"
	"github.com/hcdestroyer/horus-flow/packages/go/flowstate"
)

func event(t *testing.T, typ string, tenant uuid.UUID, version int, data any) []byte {
	t.Helper()
	d, _ := json.Marshal(data)
	b, _ := json.Marshal(map[string]any{"type": typ, "tenant_id": tenant.String(), "aggregate_version": version, "data": json.RawMessage(d)})
	return b
}

func TestProjectorFromDevicesEvents(t *testing.T) {
	url := flowbustest.URL(t)
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "DEVICES_EVENTS", Subjects: []string{"horus.devices.>"}}); err != nil {
		t.Fatal(err)
	}
	tenant, site, router, realm, prefix := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	pub := func(subject string, body []byte) {
		if _, err := js.Publish(ctx, subject, body); err != nil {
			t.Fatal(err)
		}
	}
	pub("horus.devices.site.created."+site.String(), event(t, "horus.devices.site.created", tenant, 1, map[string]any{"id": site, "name": "Centro"}))
	pub("horus.devices.router.created."+router.String(), event(t, "horus.devices.router.created", tenant, 1,
		map[string]any{"id": router, "site_id": site, "name": "rt-centro", "admin_state": "active", "tunnel_address": nil}))
	// El túnel llega después (enrolamiento WireGuard): versión 2.
	pub("horus.devices.router.updated."+router.String(), event(t, "horus.devices.router.updated", tenant, 2,
		map[string]any{"id": router, "site_id": site, "name": "rt-centro", "admin_state": "active", "tunnel_address": "10.255.3.17/32"}))
	// Reentrega de la v1: no debe borrar el túnel.
	pub("horus.devices.router.created."+router.String(), event(t, "horus.devices.router.created", tenant, 1,
		map[string]any{"id": router, "site_id": site, "name": "rt-centro", "admin_state": "active", "tunnel_address": nil}))
	pub("horus.devices.realm.updated."+realm.String(), event(t, "horus.devices.realm.updated", tenant, 1,
		map[string]any{"id": realm, "kind": "node_private", "site_id": site, "name": "Centro (privado)"}))
	pub("horus.devices.client_prefix.created."+prefix.String(), event(t, "horus.devices.client_prefix.created", tenant, 1,
		map[string]any{"id": prefix, "site_id": site, "realm_id": realm, "prefix": "10.20.0.0/24", "role": "customers", "ipv6_client_len": nil}))

	store := NewStore(nil)
	p := NewProjector(store, Data{}, nil)
	ready := make(chan struct{})
	go p.Run(ctx, js, "flows-inventory-test", ready)
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("projector not ready")
	}
	s := store.Load()
	e, ok := s.Exporter(netip.MustParseAddr("10.255.3.17"))
	if !ok || e.RouterID != router || e.SiteName != "Centro" {
		t.Fatalf("exporter %+v", e)
	}
	if m, ok := s.Lookup(tenant, site, netip.MustParseAddr("10.20.0.9")); !ok || m.Prefix.RealmID != realm {
		t.Fatal("prefix not projected")
	}
	// Reinicio del proceso: un proyector nuevo (memoria vacía) con el mismo
	// nombre relee todo el stream y recupera routers y prefijos.
	store2 := NewStore(nil)
	ready2 := make(chan struct{})
	go NewProjector(store2, Data{}, nil).Run(ctx, js, "flows-inventory-test", ready2)
	select {
	case <-ready2:
	case <-ctx.Done():
		t.Fatal("restarted projector not ready")
	}
	if _, ok := store2.Load().Lookup(tenant, site, netip.MustParseAddr("10.20.0.9")); !ok {
		t.Fatal("prefix lost after restart")
	}
	if _, ok := store2.Load().Exporter(netip.MustParseAddr("10.255.3.17")); !ok {
		t.Fatal("exporter lost after restart")
	}
	// Borrado del prefijo: deja de atribuir.
	pub("horus.devices.client_prefix.deleted."+prefix.String(), event(t, "horus.devices.client_prefix.deleted", tenant, 2,
		map[string]any{"id": prefix, "site_id": site, "realm_id": realm, "prefix": "10.20.0.0/24", "role": "customers"}))
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, ok := store.Load().Lookup(tenant, site, netip.MustParseAddr("10.20.0.9")); !ok {
			return
		}
	}
	t.Fatal("deleted prefix still active")
}

// TestProjectorSurvivesStreamRetention (D23): el inventario se reconstruye
// tras un reinicio aunque los eventos ya no estén en DEVICES_EVENTS
// (max_age de 30 días): la instantánea de flows_state conserva routers y
// prefijos, y los eventos posteriores a ella se siguen aplicando.
func TestProjectorSurvivesStreamRetention(t *testing.T) {
	url := flowbustest.URL(t)
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stream, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "DEVICES_EVENTS", Subjects: []string{"horus.devices.>"}})
	if err != nil {
		t.Fatal(err)
	}
	states, err := flowstate.Open(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	tenant, site, router, realm, prefix, prefix2 := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	pub := func(subject string, body []byte) {
		if _, err := js.Publish(ctx, subject, body); err != nil {
			t.Fatal(err)
		}
	}
	pub("horus.devices.router.created."+router.String(), event(t, "horus.devices.router.created", tenant, 1,
		map[string]any{"id": router, "site_id": site, "name": "rt", "admin_state": "active", "tunnel_address": "10.255.3.17/32"}))
	pub("horus.devices.realm.updated."+realm.String(), event(t, "horus.devices.realm.updated", tenant, 1,
		map[string]any{"id": realm, "kind": "node_private", "site_id": site, "name": "privado"}))
	pub("horus.devices.client_prefix.created."+prefix.String(), event(t, "horus.devices.client_prefix.created", tenant, 1,
		map[string]any{"id": prefix, "site_id": site, "realm_id": realm, "prefix": "10.20.0.0/24", "role": "customers"}))

	run := func() (*Store, func()) {
		store := NewStore(nil)
		pctx, pcancel := context.WithCancel(ctx)
		ready := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			NewProjector(store, Data{}, nil).RunWithStore(pctx, js, states, "flows-inventory-test", ready)
		}()
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("projector not ready")
		}
		return store, func() { pcancel(); <-done }
	}
	_, stop := run()
	stop() // guarda la instantánea al parar

	// Retención: el stream pierde todo lo anterior; llega un prefijo nuevo.
	if err := stream.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	pub("horus.devices.client_prefix.created."+prefix2.String(), event(t, "horus.devices.client_prefix.created", tenant, 1,
		map[string]any{"id": prefix2, "site_id": site, "realm_id": realm, "prefix": "10.30.0.0/24", "role": "customers"}))

	store, stop := run()
	defer stop()
	s := store.Load()
	if _, ok := s.Exporter(netip.MustParseAddr("10.255.3.17")); !ok {
		t.Fatal("exporter lost after restart + retention")
	}
	if _, ok := s.Lookup(tenant, site, netip.MustParseAddr("10.20.0.9")); !ok {
		t.Fatal("old prefix lost after restart + retention")
	}
	if _, ok := s.Lookup(tenant, site, netip.MustParseAddr("10.30.0.9")); !ok {
		t.Fatal("event after the snapshot not applied")
	}
}
