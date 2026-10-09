package analytics

import (
	"context"
	"os"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	collectorapi "github.com/hcdestroyer/horus-flow/services/collector/api"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/catalog"
)

// startExtras conecta el estado de exportadores (NATS KV), carga el
// catálogo vigente y escribe sus dimensiones en ClickHouse (dim.*).
func (m *mod) startExtras(ctx context.Context) error {
	var cur atomic.Pointer[catalog.Catalog]
	if c, err := catalog.Seed(); err == nil {
		cur.Store(c)
	}
	load := func(ctx context.Context) {
		if m.cfg.CatalogSnapshotDir == "" {
			return
		}
		_, path, err := datasets.SnapshotDir{Root: m.cfg.CatalogSnapshotDir}.Latest()
		if err != nil {
			return
		}
		f, err := os.Open(path) //nolint:gosec // directorio de configuración
		if err != nil {
			return
		}
		defer func() { _ = f.Close() }()
		if c, err := catalog.ReadSnapshot(f); err == nil && (cur.Load() == nil || c.Version() != cur.Load().Version()) {
			cur.Store(c)
			m.writeDims(ctx, c)
		}
	}
	load(ctx)
	m.svc.Catalog = cur.Load
	m.writeDims(ctx, cur.Load())
	m.loops = append(m.loops, func(ctx context.Context) {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				load(ctx)
			}
		}
	})
	if m.cfg.NATSURL != "" {
		_, js, err := flowbus.Connect(m.cfg.NATSURL, "horus-analytics")
		if err != nil {
			m.log.WarnContext(ctx, "analytics: NATS unavailable: exporters_status shows pending", "error", err)
			return nil
		}
		kv, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: flowbus.ExporterStateBucket,
			Description: "Estado de los exportadores de flujos (I1-09)", History: 1, Storage: jetstream.FileStorage})
		if err != nil {
			m.log.WarnContext(ctx, "analytics: exporter state bucket unavailable", "error", err)
			return nil
		}
		m.widgets.States = collectorapi.KVStates{KV: kv}
	}
	return nil
}

// writeDims escribe dim.category, dim.service, dim.organization y dim.asn
// del catálogo (analytics es el escritor único de dim.*, database.md §4).
func (m *mod) writeDims(ctx context.Context, c *catalog.Catalog) {
	r := m.q.get()
	if r == nil || c == nil {
		return
	}
	v := c.Version()
	var cats, svcs, orgs, asns [][]any
	for _, k := range c.Def.Categories {
		cats = append(cats, []any{catalog.ID("category", k.Slug), k.Name, v})
	}
	for _, s := range c.Def.Services {
		svcs = append(svcs, []any{catalog.ID("service", s.Slug), s.Name, catalog.ID("category", s.Category), v})
	}
	for _, o := range c.Def.Organizations {
		country := o.Country
		if len(country) != 2 {
			country = ""
		}
		orgs = append(orgs, []any{catalog.ID("org", o.Slug), o.Name, country, v})
		for _, a := range o.ASNs {
			asns = append(asns, []any{a, catalog.ID("org", o.Slug), o.Name, v})
		}
	}
	for table, rows := range map[string][][]any{
		"dim.category (category_id, name, catalog_version)":            cats,
		"dim.service (service_id, name, category_id, catalog_version)": svcs,
		"dim.organization (org_id, name, country, catalog_version)":    orgs,
		"dim.asn (asn, org_id, name, catalog_version)":                 asns,
	} {
		if err := r.InsertDims(ctx, table, rows); err != nil {
			m.log.WarnContext(ctx, "analytics: catalog dimensions not written", "table", table, "error", err)
		}
	}
}
