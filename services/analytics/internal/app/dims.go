package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/chread"
)

// DurableNames: consumidor analytics-names (streams.yaml consumers_i1,
// docs/events.md §4.5) que proyecta nodos, routers y clientes de devices en
// dim.site, dim.router y dim.customer (analytics es el escritor único de dim.*).
// Sin él la ficha de un cliente responde 404 en su tráfico y las tablas no
// muestran nombres de nodo ni alias.
const DurableNames = "analytics-names"

// NamesSubjects son los filtros del consumidor.
var NamesSubjects = []string{"horus.devices.router.>", "horus.devices.site.>", "horus.devices.customer.>"}

// DimStore escribe y lee las dimensiones (chread.Reader).
type DimStore interface {
	InsertDims(ctx context.Context, table string, rows [][]any) error
	Query(ctx context.Context, tenant uuid.UUID, sql string, args ...any) (chread.Rows, context.CancelFunc, error)
}

// ErrDimsUnavailable: ClickHouse aún no está abierto (se reintenta).
var ErrDimsUnavailable = errors.New("analytics: dimensions store unavailable")

// DimProjector aplica los eventos de devices a dim.*.
type DimProjector struct {
	Store func() DimStore
}

type siteData struct {
	ID        uuid.UUID  `json:"id"`
	Version   uint32     `json:"version"`
	Name      string     `json:"name"`
	ParentID  *uuid.UUID `json:"parent_id"`
	Timezone  *string    `json:"timezone"`
	DeletedAt *string    `json:"deleted_at"`
}

type routerData struct {
	ID          uuid.UUID `json:"id"`
	Version     uint32    `json:"version"`
	SiteID      uuid.UUID `json:"site_id"`
	Name        string    `json:"name"`
	DisplayName *string   `json:"display_name"`
	IsPrimary   bool      `json:"is_primary"`
	DeletedAt   *string   `json:"deleted_at"`
}

// customerData: los eventos completos (discovered, updated, kind_changed,
// reactivated) traen realm_id, address y site_id; reset, inactivated y purged
// solo lo que cambia y se completan con la fila vigente.
type customerData struct {
	ID         uuid.UUID  `json:"id"`
	Version    uint32     `json:"version"`
	Address    *string    `json:"address"`
	RealmID    *uuid.UUID `json:"realm_id"`
	SiteID     *uuid.UUID `json:"site_id"`
	Kind       *string    `json:"kind"`
	KindSource *string    `json:"kind_source"`
	Alias      *string    `json:"alias"`
	Status     *string    `json:"status"`
	ResetAt    *time.Time `json:"reset_at"`
	raw        map[string]json.RawMessage
}

func (c customerData) has(field string) bool { _, ok := c.raw[field]; return ok }

// Handle implementa natsx.Handler.
func (p DimProjector) Handle(ctx context.Context, m *natsx.Message) error {
	if m.Envelope == nil || m.Tenant == uuid.Nil {
		return nil
	}
	typ := m.Envelope.Type
	kind, action, ok := splitType(typ)
	if !ok {
		return nil
	}
	store := p.Store()
	if store == nil {
		return ErrDimsUnavailable
	}
	switch kind {
	case "site":
		var d siteData
		if err := json.Unmarshal(m.Envelope.Data, &d); err != nil || d.ID == uuid.Nil {
			return natsx.Permanent(fmt.Errorf("%s: %v", typ, err))
		}
		parent, tz := uuid.Nil, ""
		if d.ParentID != nil {
			parent = *d.ParentID
		}
		if d.Timezone != nil {
			tz = *d.Timezone
		}
		return store.InsertDims(ctx, "dim.site (tenant_id, site_id, name, parent_id, timezone, version, deleted)",
			[][]any{{m.Tenant, d.ID, d.Name, parent, tz, d.Version, flag(action == "deleted" || d.DeletedAt != nil)}})
	case "router":
		var d routerData
		if err := json.Unmarshal(m.Envelope.Data, &d); err != nil || d.ID == uuid.Nil {
			return natsx.Permanent(fmt.Errorf("%s: %v", typ, err))
		}
		name := d.Name
		if d.DisplayName != nil && strings.TrimSpace(*d.DisplayName) != "" {
			name = *d.DisplayName
		}
		return store.InsertDims(ctx, "dim.router (tenant_id, router_id, site_id, name, is_primary, version, deleted)",
			[][]any{{m.Tenant, d.ID, d.SiteID, name, flag(d.IsPrimary), d.Version, flag(action == "deleted" || d.DeletedAt != nil)}})
	case "customer":
		return p.customer(ctx, store, m.Tenant, typ, action, m.Envelope.Data)
	}
	return nil
}

// customerRow es una fila de dim.customer.
type customerRow struct {
	realm, site      uuid.UUID
	address          netip.Addr
	kind, kindSource string
	alias            *string
	status           string
	resetAt          *time.Time
}

func (p DimProjector) customer(ctx context.Context, store DimStore, tenant uuid.UUID, typ, action string, data json.RawMessage) error {
	var d customerData
	if err := json.Unmarshal(data, &d); err != nil || d.ID == uuid.Nil {
		return natsx.Permanent(fmt.Errorf("%s: %v", typ, err))
	}
	_ = json.Unmarshal(data, &d.raw)
	var row customerRow
	if d.RealmID != nil && d.SiteID != nil && d.Address != nil {
		a, err := netip.ParseAddr(strings.Split(*d.Address, "/")[0])
		if err != nil {
			return natsx.Permanent(fmt.Errorf("%s: address: %v", typ, err))
		}
		row = customerRow{realm: *d.RealmID, site: *d.SiteID, address: a, kind: "residential", kindSource: "default", status: "active"}
	} else {
		cur, found, err := currentCustomer(ctx, store, tenant, d.ID)
		if err != nil {
			return err
		}
		if !found {
			return nil // nunca se proyectó (anterior a la retención del stream): nada que cambiar
		}
		row = cur
	}
	if d.Kind != nil {
		row.kind = *d.Kind
	}
	if d.KindSource != nil {
		row.kindSource = *d.KindSource
	}
	if d.has("alias") {
		row.alias = d.Alias
	}
	if d.Status != nil {
		row.status = *d.Status
	}
	if d.has("reset_at") {
		row.resetAt = d.ResetAt
	}
	var alias, reset any
	if row.alias != nil {
		alias = *row.alias
	}
	if row.resetAt != nil {
		reset = row.resetAt.UTC()
	}
	return store.InsertDims(ctx,
		"dim.customer (tenant_id, realm_id, address, customer_id, site_id, kind, kind_source, alias, status, reset_at, version, deleted)",
		[][]any{{tenant, row.realm, netip.AddrFrom16(row.address.As16()), d.ID, row.site, row.kind, row.kindSource, alias,
			row.status, reset, d.Version, flag(action == "purged")}})
}

func currentCustomer(ctx context.Context, store DimStore, tenant, id uuid.UUID) (customerRow, bool, error) {
	rows, cancel, err := store.Query(ctx, tenant, `SELECT realm_id, address, site_id, kind, kind_source, alias, status, reset_at
		FROM dim.customer FINAL WHERE tenant_id = ? AND customer_id = ? AND deleted = 0 LIMIT 1`, tenant, id)
	if err != nil {
		return customerRow{}, false, err
	}
	defer cancel()
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return customerRow{}, false, nil
	}
	var r customerRow
	if err := rows.Scan(&r.realm, &r.address, &r.site, &r.kind, &r.kindSource, &r.alias, &r.status, &r.resetAt); err != nil {
		return customerRow{}, false, err
	}
	r.address = r.address.Unmap()
	return r, true, nil
}

// splitType: "horus.devices.customer.reset" → ("customer", "reset").
func splitType(t string) (kind, action string, ok bool) {
	parts := strings.Split(t, ".")
	if len(parts) != 4 || parts[0] != "horus" || parts[1] != "devices" {
		return "", "", false
	}
	return parts[2], parts[3], true
}

func flag(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}
