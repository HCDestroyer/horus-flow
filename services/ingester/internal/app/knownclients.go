package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
)

// KnownClientsConsumer es el durable ingester-known-clients (events.md §4.5):
// mantiene el conjunto de claves conocidas con customer.discovered (y
// reactivated/updated) y las olvida con customer.purged.
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

// RunKnownClients consume DEVICES_EVENTS si existe (devices puede no estar
// desplegado: entonces el conjunto se nutre del inventario y del TTL).
func (d *Discovery) RunKnownClients(ctx context.Context, js jetstream.JetStream, log *slog.Logger) {
	for {
		cons, err := js.CreateOrUpdateConsumer(ctx, flowbus.StreamDevices, jetstream.ConsumerConfig{
			Durable: KnownClientsConsumer, AckPolicy: jetstream.AckExplicitPolicy, DeliverPolicy: jetstream.DeliverAllPolicy,
			FilterSubjects: []string{"horus.devices.customer.discovered.>", "horus.devices.customer.reactivated.>",
				"horus.devices.customer.purged.>"},
			AckWait: 30 * time.Second, MaxDeliver: 8, MaxAckPending: 64,
		})
		if err == nil {
			it, err := cons.Messages()
			if err == nil {
				go func() { <-ctx.Done(); it.Stop() }()
				for {
					m, err := it.Next()
					if err != nil {
						break
					}
					if err := d.HandleCustomerEvent(m.Data()); err != nil {
						log.Warn("customer event ignored", "subject", m.Subject(), "error", err)
						_ = m.Term()
						continue
					}
					_ = m.Ack()
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second): // el stream aún no existe: reintentar
		}
	}
}
