package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"strings"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowstate"
)

// KnownClientsConsumer es el nombre de la instantánea del conjunto de claves
// conocidas (y del antiguo durable ingester-known-clients, events.md §4.5):
// se mantiene con customer.discovered (y reactivated) y se olvidan con
// customer.purged.
const KnownClientsConsumer = "ingester-known-clients"

type customerData struct {
	Address string    `json:"address"`
	RealmID uuid.UUID `json:"realm_id"`
}

// HandleCustomerEvent aplica un evento horus.devices.customer.* al descubridor.
func (d *Discovery) HandleCustomerEvent(data []byte) error {
	var env flowbus.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	if env.TenantID == nil {
		return errors.New("customer event without tenant")
	}
	tenant, err := uuid.Parse(*env.TenantID)
	if err != nil {
		return err
	}
	var c customerData
	if err := json.Unmarshal(env.Data, &c); err != nil {
		return err
	}
	addr, err := netip.ParseAddr(strings.Split(c.Address, "/")[0])
	if err != nil || c.RealmID == uuid.Nil {
		return errors.New("customer event without address/realm")
	}
	switch {
	case strings.HasSuffix(env.Type, ".purged"):
		d.Forget(c.RealmID, addr)
	default:
		d.LoadKnown([]flowinv.CustomerKey{{TenantID: tenant, RealmID: c.RealmID, Address: addr}})
	}
	return nil
}

type knownRealm struct {
	Tenant uuid.UUID    `json:"tenant"`
	Addrs  []netip.Addr `json:"addrs"`
}

// KnownSnapshot serializa el conjunto de claves conocidas por realm.
func (d *Discovery) KnownSnapshot() (json.RawMessage, error) {
	d.mu.Lock()
	out := make(map[uuid.UUID]knownRealm, len(d.realms))
	for id, r := range d.realms {
		if len(r.known) == 0 {
			continue
		}
		kr := knownRealm{Tenant: r.tenant, Addrs: make([]netip.Addr, 0, len(r.known))}
		for a := range r.known {
			kr.Addrs = append(kr.Addrs, a)
		}
		out[id] = kr
	}
	d.mu.Unlock()
	return json.Marshal(out)
}

// RestoreKnown añade las claves de una instantánea.
func (d *Discovery) RestoreKnown(b json.RawMessage) error {
	var in map[uuid.UUID]knownRealm
	if err := json.Unmarshal(b, &in); err != nil {
		return err
	}
	keys := make([]flowinv.CustomerKey, 0)
	for realm, kr := range in {
		for _, a := range kr.Addrs {
			keys = append(keys, flowinv.CustomerKey{TenantID: kr.Tenant, RealmID: realm, Address: a})
		}
	}
	d.LoadKnown(keys)
	return nil
}

// KnownClientsSubjects son los eventos de clientes de DEVICES_EVENTS.
var KnownClientsSubjects = []string{"horus.devices.customer.discovered.>", "horus.devices.customer.reactivated.>",
	"horus.devices.customer.purged.>"}

// RunKnownClients mantiene el conjunto de clientes conocidos con
// DEVICES_EVENTS (devices puede no estar desplegado: entonces el conjunto se
// nutre del inventario y del TTL). Antes era el durable
// ingester-known-clients con el conjunto en memoria: tras un reinicio solo
// llegaban los eventos no confirmados y se reenviaban first_seen de todos
// los clientes ya conocidos. Ahora se reconstruye al arrancar
// (packages/go/flowstate): instantánea en flows_state (states puede ser nil)
// y lectura del stream desde la secuencia siguiente; el durable antiguo se
// borra. ready (puede ser nil) se cierra al alcanzar el final del stream.
func (d *Discovery) RunKnownClients(ctx context.Context, js jetstream.JetStream, states flowstate.Store, log *slog.Logger, ready chan<- struct{}) {
	r := &flowstate.Replay{JS: js, Stream: flowbus.StreamDevices, Filters: KnownClientsSubjects, Name: KnownClientsConsumer,
		Legacy: KnownClientsConsumer, Log: log, Snapshot: d.KnownSnapshot, Restore: d.RestoreKnown,
		Apply: func(_ string, data []byte) (bool, error) {
			if err := d.HandleCustomerEvent(data); err != nil {
				return false, err
			}
			return true, nil
		}}
	if states != nil {
		r.Store = states
	}
	r.Run(ctx, ready)
}
