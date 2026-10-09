package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// Consumidores de clientes (docs/events.md §4.5; streams.yaml consumers_i1).
const (
	DurableFirstSeen = "devices-client-first-seen"
	DurableActivity  = "devices-client-activity"
)

// firstSeenData es el payload de horus.flows.client.first_seen (JSON).
type firstSeenData struct {
	BatchID  string    `json:"batch_id"`
	RealmID  uuid.UUID `json:"realm_id"`
	RouterID string    `json:"router_id"`
	Clients  []struct {
		Address        string     `json:"address"`
		ClientPrefixID *uuid.UUID `json:"client_prefix_id"`
		FirstSeen      time.Time  `json:"first_seen"`
	} `json:"clients"`
}

// HandleFirstSeen da de alta de forma idempotente los clientes de un lote
// first_seen y emite customer.discovered por cada uno nuevo (I1-06 criterio 1).
func (s *Customers) HandleFirstSeen(ctx context.Context, m *natsx.Message) error {
	if m.Envelope == nil || m.Envelope.Type != "horus.flows.client.first_seen" {
		return nil
	}
	var d firstSeenData
	if err := json.Unmarshal(m.Envelope.Data, &d); err != nil {
		return natsx.Permanent(fmt.Errorf("first_seen: %w", err))
	}
	if d.RealmID == uuid.Nil {
		return natsx.Permanent(errors.New("first_seen: realm_id missing"))
	}
	clients := make([]NewCustomer, 0, len(d.Clients))
	for _, c := range d.Clients {
		a, _, ok := domain.ParseCustomerAddress(c.Address)
		if !ok {
			s.logger.WarnContext(ctx, "first_seen: invalid address skipped", slog.String("batch_id", d.BatchID))
			continue
		}
		fs := c.FirstSeen
		if fs.IsZero() {
			fs = m.Envelope.Time
		}
		clients = append(clients, NewCustomer{Address: a, ClientPrefixID: c.ClientPrefixID, FirstSeen: fs.UTC()})
	}
	return s.Discover(ctx, pgdb.TenantID(m.Tenant), d.RealmID, clients)
}

// Discover crea los clientes nuevos (idempotente por (tenant, realm, address)).
func (s *Customers) Discover(ctx context.Context, t pgdb.TenantID, realm uuid.UUID, clients []NewCustomer) error {
	if len(clients) == 0 {
		return nil
	}
	now := s.ts()
	created, err := s.store.DiscoverCustomers(ctx, t, realm, clients, func(c *domain.Customer) []outbox.Event {
		return []outbox.Event{customerEvent(nil, t, "discovered", c, now, CustomerData(c))}
	})
	if err != nil {
		return err
	}
	if len(created) > 0 {
		s.logger.DebugContext(ctx, "customers discovered", slog.Int("count", len(created)))
	}
	return nil
}

// HandleActivity aplica un resumen horario (Protobuf; JSON en pruebas):
// last_seen y reactivación de inactivos (I1-06 criterio 3).
func (s *Customers) HandleActivity(ctx context.Context, m *natsx.Message) error {
	realm, seen, err := decodeActivity(m)
	if err != nil {
		return natsx.Permanent(err)
	}
	return s.Touch(ctx, pgdb.TenantID(m.Tenant), realm, seen)
}

// Touch actualiza last_seen y reactiva.
func (s *Customers) Touch(ctx context.Context, t pgdb.TenantID, realm uuid.UUID, seen []SeenCustomer) error {
	if len(seen) == 0 {
		return nil
	}
	now := s.ts()
	_, err := s.store.TouchCustomers(ctx, t, realm, seen, func(c *domain.Customer) []outbox.Event {
		return []outbox.Event{customerEvent(nil, t, "reactivated", c, now, map[string]any{
			"id": c.ID, "version": c.Version, "address": c.AddressString(), "status": c.Status, "last_seen": fmtTS(c.LastSeen),
		})}
	})
	return err
}

type activityJSON struct {
	RealmID uuid.UUID `json:"realm_id"`
	Clients []struct {
		Address  string    `json:"address"`
		LastSeen time.Time `json:"last_seen"`
	} `json:"clients"`
}

func decodeActivity(m *natsx.Message) (uuid.UUID, []SeenCustomer, error) {
	var out []SeenCustomer
	ct := m.Header.Get(natsx.HeaderContentType)
	if len(ct) >= 16 && ct[:16] == "application/json" {
		var d activityJSON
		body := m.Data
		var wrapped struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(body, &wrapped) == nil && len(wrapped.Data) > 0 {
			body = wrapped.Data
		}
		if err := json.Unmarshal(body, &d); err != nil {
			return uuid.Nil, nil, fmt.Errorf("activity_summary: %w", err)
		}
		for _, c := range d.Clients {
			if a, _, ok := domain.ParseCustomerAddress(c.Address); ok {
				out = append(out, SeenCustomer{Address: a, LastSeen: c.LastSeen.UTC()})
			}
		}
		return d.RealmID, out, nil
	}
	pb, err := flowpb.UnmarshalClientActivitySummary(m.Data)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("activity_summary: %w", err)
	}
	realm, err := uuid.Parse(pb.RealmID)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("activity_summary: realm_id: %w", err)
	}
	for _, c := range pb.Clients {
		a, _, ok := domain.ParseCustomerAddress(c.Address)
		if !ok {
			continue
		}
		out = append(out, SeenCustomer{Address: a, LastSeen: c.LastSeen.UTC()})
	}
	return realm, out, nil
}

// RunLifecycle aplica el ciclo de vida a todos los tenants: inactivación
// (sin tráfico InactivityDays) y purga por retención (RetentionMonths).
func (s *Customers) RunLifecycle(ctx context.Context) error {
	tenants, err := s.store.CustomerTenants(ctx)
	if err != nil {
		return err
	}
	now := s.ts()
	var errs []error
	for _, tid := range tenants {
		t := pgdb.TenantID(tid)
		n, err := s.store.InactivateIdle(ctx, t, domain.InactiveBefore(now, s.InactivityDays), func(c *domain.Customer) []outbox.Event {
			ev := customerEvent(lifecycleActor(), t, "inactivated", c, now, map[string]any{
				"id": c.ID, "version": c.Version, "address": c.AddressString(), "status": c.Status, "last_seen": fmtTS(c.LastSeen),
				"reason": "no_traffic",
			})
			ev.Actor = outbox.Actor{Type: "system", ID: "system:devices-lifecycle"}
			return []outbox.Event{ev}
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		p, err := s.store.PurgeExpired(ctx, t, domain.PurgeBefore(now, s.RetentionMonths), func(c *domain.Customer) []outbox.Event {
			ev := customerEvent(lifecycleActor(), t, "purged", c, now, map[string]any{"id": c.ID, "version": c.Version, "reason": "retention"})
			ev.Actor = outbox.Actor{Type: "system", ID: "system:devices-lifecycle"}
			return []outbox.Event{ev}
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if n > 0 || p > 0 {
			s.logger.InfoContext(ctx, "customer lifecycle", slog.String("tenant_id", tid.String()), slog.Int("inactivated", n), slog.Int("purged", p))
		}
	}
	return errors.Join(errs...)
}

// RunLifecycleDaily ejecuta RunLifecycle al arrancar y cada 24 h.
func (s *Customers) RunLifecycleDaily(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 24 * time.Hour
	}
	for {
		if err := s.RunLifecycle(ctx); err != nil && ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "customer lifecycle failed", slog.Any("error", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
