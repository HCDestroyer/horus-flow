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
