package app

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/traffic/api/catalog"
)

// Dimensiones de GET /analytics/traffic/top.
const (
	DimCustomers     = "customers"
	DimServices      = "services"
	DimCategories    = "categories"
	DimOrganizations = "organizations"
	DimASNs          = "asns"
)

// Scope es el ámbito de una consulta: ISP (Sites vacío), nodos o cliente.
type Scope struct {
	Tenant uuid.UUID
	// Sites limita a esos nodos (vacío = todo el ISP; nil tras aplicar ACL = todos).
	Sites []uuid.UUID
	// Customer limita a un cliente (realm + dirección canónica).
	Customer *CustomerKey
}

// CustomerKey identifica a un cliente en ClickHouse (ClickHouse no guarda customer_id).
type CustomerKey struct {
	ID      uuid.UUID
	RealmID uuid.UUID
	Address netip.Addr
	SiteID  uuid.UUID
	Alias   *string
	Kind    string
	ResetAt *time.Time
}

// TopRow es una fila del top.
type TopRow struct {
	Key       string
	Label     string
	Down, Up  uint64
	Share     float64
	Customer  *CustomerKey
	CustomerIP netip.Addr
	Site      uuid.UUID
}

// TopResult es el resultado del top.
type TopResult struct {
	Dimension string
	Rows      []TopRow
	OthersDown, OthersUp uint64
	TotalDown, TotalUp   uint64
}

// TopQuery es la petición de un top.
type TopQuery struct {
	Scope     Scope
	Dimension string
	Direction string // both | download | upload
	N         int
	Range     Range
}

func table(prefix string, r Range) string {
	g := Pick(r, 0)
	return "flows." + prefix + "_" + g.Suffix
}

// siteFilter añade el filtro por nodos.
func siteFilter(sites []uuid.UUID, args *[]any) string {
	if len(sites) == 0 {
		return ""
	}
	*args = append(*args, sites)
	return " AND site_id IN ?"
}

type agg struct {
	key      string
	down, up uint64
	site     uuid.UUID
	realm    uuid.UUID
	ip       netip.Addr
}

// Top calcula el top N + Otros (I1-08 criterio 1).
func (s *Service) Top(ctx context.Context, q TopQuery) (*TopResult, error) {
	if q.N < 1 || q.N > 10 {
		q.N = 10
	}
	if q.Direction == "" {
		q.Direction = "both"
	}
	var aggs []agg
	var err error
	switch q.Dimension {
	case DimServices, DimCategories:
		aggs, err = s.byColumn(ctx, q, "service_id")
		if err == nil && q.Dimension == DimCategories {
			aggs = s.toCategories(aggs)
		}
	case DimOrganizations, DimASNs:
		aggs, err = s.byColumn(ctx, q, "remote_asn")
		if err == nil && q.Dimension == DimOrganizations {
			aggs = s.toOrganizations(aggs)
		}
	case DimCustomers:
		aggs, err = s.byCustomer(ctx, q)
	default:
		return nil, fmt.Errorf("%w: dimension %q", ErrBadRequest, q.Dimension)
	}
	if err != nil {
		return nil, err
	}
	res := &TopResult{Dimension: q.Dimension}
	weight := func(a agg) uint64 {
		switch q.Direction {
		case "download":
			return a.down
		case "upload":
			return a.up
		}
		return a.down + a.up
	}
	sort.Slice(aggs, func(i, j int) bool {
		wi, wj := weight(aggs[i]), weight(aggs[j])
		if wi != wj {
			return wi > wj
		}
		return aggs[i].key < aggs[j].key
	})
	var total uint64
	for _, a := range aggs {
		res.TotalDown += a.down
		res.TotalUp += a.up
		total += weight(a)
	}
	for _, a := range aggs {
		// Lo no clasificado (clave vacía) y lo que no cabe en N va a "Otros".
		if a.key == "" || len(res.Rows) >= q.N || weight(a) == 0 {
			res.OthersDown += a.down
			res.OthersUp += a.up
			continue
		}
		row := TopRow{Key: a.key, Label: s.label(q.Dimension, a.key), Down: a.down, Up: a.up, Site: a.site, CustomerIP: a.ip}
		if total > 0 {
			row.Share = float64(weight(a)) / float64(total)
		}
		res.Rows = append(res.Rows, row)
	}
	if q.Dimension == DimCustomers {
		s.attachCustomers(ctx, q.Scope.Tenant, res.Rows, aggs)
	}
	return res, nil
}

// byColumn agrega bytes por una columna de los agregados de nodo o de cliente.
func (s *Service) byColumn(ctx context.Context, q TopQuery, col string) ([]agg, error) {
	args := []any{q.Scope.Tenant, q.Range.From, q.Range.To}
	var sql string
	if c := q.Scope.Customer; c != nil {
		tbl := table("customer", q.Range)
		if col == "remote_asn" && strings.HasSuffix(tbl, "_5m") {
			tbl = "flows.customer_1h" // customer_5m no lleva remote_asn
		}
		args = append(args, c.RealmID, netip.AddrFrom16(c.Address.As16()))
		sql = fmt.Sprintf(`SELECT toString(%s) AS k, sumIf(bytes, direction = 'download') AS d, sumIf(bytes, direction IN ('upload', 'internal')) AS u
			FROM %s WHERE tenant_id = ? AND bucket >= ? AND bucket < ? AND realm_id = ? AND client_ip = ? GROUP BY k`, col, tbl)
	} else {
		sql = fmt.Sprintf(`SELECT toString(%s) AS k, sumIf(bytes, direction = 'download') AS d, sumIf(bytes, direction = 'upload') AS u
			FROM %s WHERE tenant_id = ? AND bucket >= ? AND bucket < ?%s GROUP BY k`, col, table("site", q.Range), siteFilter(q.Scope.Sites, &args))
	}
	rows, cancel, err := s.Q.Query(ctx, q.Scope.Tenant, sql, args...)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer func() { _ = rows.Close() }()
	var out []agg
	for rows.Next() {
		var a agg
		if err := rows.Scan(&a.key, &a.down, &a.up); err != nil {
			return nil, err
		}
		if a.key == uuid.Nil.String() || a.key == "0" {
			a.key = ""
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) byCustomer(ctx context.Context, q TopQuery) ([]agg, error) {
	args := []any{q.Scope.Tenant, q.Range.From, q.Range.To}
	tbl := table("customer", q.Range)
	sites := ""
	if !strings.HasSuffix(tbl, "_1d") {
		sites = siteFilter(q.Scope.Sites, &args)
	}
	rows, cancel, err := s.Q.Query(ctx, q.Scope.Tenant, fmt.Sprintf(`
		SELECT realm_id, client_ip, %s AS site, sumIf(bytes, direction = 'download') AS d,
		       sumIf(bytes, direction IN ('upload', 'internal')) AS u
		FROM %s WHERE tenant_id = ? AND bucket >= ? AND bucket < ?%s
		GROUP BY realm_id, client_ip%s`, map[bool]string{true: "any(site_id)", false: "toUUID('00000000-0000-0000-0000-000000000000')"}[!strings.HasSuffix(tbl, "_1d")],
		tbl, sites, ""), args...)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer func() { _ = rows.Close() }()
	var out []agg
	for rows.Next() {
		var a agg
		if err := rows.Scan(&a.realm, &a.ip, &a.site, &a.down, &a.up); err != nil {
			return nil, err
		}
		a.ip = a.ip.Unmap()
		a.key = a.realm.String() + "|" + a.ip.String()
		out = append(out, a)
	}
	return out, rows.Err()
}

// attachCustomers resuelve customer_id, alias y tipo con dim.customer (si
// devices los ha proyectado); si no, la clave es un UUIDv5 estable de la clave natural.
func (s *Service) attachCustomers(ctx context.Context, tenant uuid.UUID, rows []TopRow, aggs []agg) {
	if len(rows) == 0 {
		return
	}
	byKey := map[string]*TopRow{}
	var realms []uuid.UUID
	var ips []netip.Addr
	for i := range rows {
		parts := strings.SplitN(rows[i].Key, "|", 2)
		realm, _ := uuid.Parse(parts[0])
		ck := &CustomerKey{RealmID: realm, Address: rows[i].CustomerIP, SiteID: rows[i].Site, Kind: "residential",
			ID: uuid.NewSHA1(tenant, []byte(rows[i].Key))}
		rows[i].Customer = ck
		byKey[rows[i].Key] = &rows[i]
		realms = append(realms, realm)
		ips = append(ips, netip.AddrFrom16(rows[i].CustomerIP.As16()))
	}
	_ = aggs
	q, cancel, err := s.Q.Query(ctx, tenant, `SELECT realm_id, address, customer_id, alias, kind, site_id
		FROM dim.customer FINAL WHERE tenant_id = ? AND deleted = 0 AND realm_id IN ? AND address IN ?`, tenant, realms, ips)
	if err != nil {
		return
	}
	defer cancel()
	defer func() { _ = q.Close() }()
	for q.Next() {
		var realm, id, site uuid.UUID
		var addr netip.Addr
		var alias *string
		var kind string
		if q.Scan(&realm, &addr, &id, &alias, &kind, &site) != nil {
			continue
		}
		if r := byKey[realm.String()+"|"+addr.Unmap().String()]; r != nil {
			r.Customer.ID, r.Customer.Alias, r.Customer.Kind = id, alias, kind
			if r.Customer.SiteID == uuid.Nil {
				r.Customer.SiteID = site
			}
			r.Key = id.String()
		}
	}
}

func (s *Service) toCategories(in []agg) []agg {
	c := s.catalog()
	svcCat := map[string]string{}
	for _, sv := range c.Def.Services {
		svcCat[catalog.ID("service", sv.Slug).String()] = catalog.ID("category", sv.Category).String()
	}
	m := map[string]*agg{}
	for _, a := range in {
		k := svcCat[a.key]
		x := m[k]
		if x == nil {
			x = &agg{key: k}
			m[k] = x
		}
		x.down += a.down
		x.up += a.up
	}
	out := make([]agg, 0, len(m))
	for _, a := range m {
		out = append(out, *a)
	}
	return out
}

func (s *Service) toOrganizations(in []agg) []agg {
	c := s.catalog()
	m := map[string]*agg{}
	for _, a := range in {
		k := ""
		if n, err := strconv.ParseUint(a.key, 10, 32); err == nil && n > 0 {
			if o, ok := c.Organization(uint32(n)); ok {
				k = catalog.ID("org", o.Slug).String()
			} else {
				k = "AS" + a.key
			}
		}
		x := m[k]
		if x == nil {
			x = &agg{key: k}
			m[k] = x
		}
		x.down += a.down
		x.up += a.up
	}
	out := make([]agg, 0, len(m))
	for _, a := range m {
		out = append(out, *a)
	}
	return out
}

func (s *Service) catalog() *catalog.Catalog {
	if s.Catalog != nil {
		if c := s.Catalog(); c != nil {
			return c
		}
	}
	c, _ := catalog.Seed()
	return c
}

// label devuelve la etiqueta de una clave del top.
func (s *Service) label(dim, key string) string {
	c := s.catalog()
	switch dim {
	case DimServices:
		for _, sv := range c.Def.Services {
			if catalog.ID("service", sv.Slug).String() == key {
				return sv.Name
			}
		}
	case DimCategories:
		for _, k := range c.Def.Categories {
			if catalog.ID("category", k.Slug).String() == key {
				return k.Name
			}
		}
	case DimOrganizations:
		for _, o := range c.Def.Organizations {
			if catalog.ID("org", o.Slug).String() == key {
				return o.Name
			}
		}
		if n, ok := strings.CutPrefix(key, "AS"); ok {
			if v, err := strconv.ParseUint(n, 10, 32); err == nil {
				if name, ok := s.names().ASN(uint32(v)); ok {
					return name
				}
			}
		}
	case DimASNs:
		if v, err := strconv.ParseUint(key, 10, 32); err == nil {
			name := "AS" + key
			if n, ok := s.names().ASN(uint32(v)); ok {
				name += " · " + n
			} else if o, ok := c.Organization(uint32(v)); ok {
				name += " · " + o.Name
			}
			return name
		}
	case DimCustomers:
		parts := strings.SplitN(key, "|", 2)
		if len(parts) == 2 {
			return parts[1]
		}
	}
	return key
}
