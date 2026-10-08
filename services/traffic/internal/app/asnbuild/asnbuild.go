// Package asnbuild orquesta el dataset de plataforma prefijo → ASN →
// organización (docs/traffic-model.md §5): descarga y validación de cada
// fuente con conservación de la última versión válida, consolidación
// (BGP > iptoasn > RIR), diff contra el snapshot anterior (> 5 % ⇒ revisión) y
// publicación del snapshot.
package asnbuild

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sort"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/iptrie"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/asn"
)

// SourceKind es el valor de `kind` de estas fuentes en datasets.yaml.
const SourceKind = "asn"

// ReviewThreshold es la fracción de prefijos cambiados a partir de la cual un
// snapshot nuevo requiere revisión antes de publicarse.
const ReviewThreshold = 0.05

// Role indica qué aporta una fuente a la consolidación.
type Role int

const (
	RoleBGP      Role = iota + 1 // prefijo → ASN de origen observado en BGP (prioridad 1)
	RoleIPToASN                  // prefijo → ASN (prioridad 2)
	RoleRegistry                 // prefijo/ASN → país (RIR: validación y relleno)
	RoleOrgInfo                  // ASN → organización y tipo de red (PeeringDB)
)

// CountryBlock es un bloque delegado por un RIR a un país.
type CountryBlock struct {
	Prefix  netip.Prefix
	Country string
}

// SourceData es la salida normalizada de una fuente.
type SourceData struct {
	Role      Role
	Routes    []asn.Entry
	ASInfo    map[uint32]asn.ASInfo
	Countries []CountryBlock
	Lines     int
	Invalid   int
	Ignored   int
}

// Entries devuelve el número de registros útiles.
func (d *SourceData) Entries() int { return len(d.Routes) + len(d.ASInfo) + len(d.Countries) }

// ParseFunc interpreta el archivo de una fuente (implementado por
// adapters/asnsources).
type ParseFunc func(path string, src datasets.Source) (*SourceData, error)

// Service agrupa las dependencias.
type Service struct {
	Store           *datasets.Store
	Fetcher         datasets.Fetcher
	Parse           ParseFunc
	AllowUnverified bool
	Now             func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func filterKind(sources []datasets.Source) []datasets.Source {
	var out []datasets.Source
	for _, src := range sources {
		if src.Kind == SourceKind {
			out = append(out, src)
		}
	}
	return out
}

// Fetch descarga y valida las fuentes.
func (s *Service) Fetch(ctx context.Context, sources []datasets.Source, log *slog.Logger) []datasets.Result {
	r := &datasets.Runner{
		Store: s.Store, Fetcher: s.Fetcher, AllowUnverified: s.AllowUnverified, Now: s.now, Log: log,
		Validate: func(_ context.Context, src datasets.Source, path string) (int, error) {
			d, err := s.Parse(path, src)
			if err != nil {
				return 0, err
			}
			return d.Entries(), nil
		},
	}
	return r.Run(ctx, filterKind(sources))
}

// SourceReport resume la aportación de una fuente.
type SourceReport struct {
	SourceID string
	Used     bool
	Routes   int // prefijos aportados tras la consolidación
	Reason   string
}

// ErrNoRoutes indica que ninguna fuente con prefijo → ASN tenía versión
// válida: no se genera snapshot.
var ErrNoRoutes = errors.New("ninguna fuente prefijo→ASN tiene una versión válida")

// Build consolida las versiones vigentes de las fuentes en un snapshot.
//
//   - BGP (MRT): prefijo → origen; si varias fuentes BGP anuncian el mismo
//     prefijo, gana la primera declarada.
//   - iptoasn: solo prefijos que ningún prefijo BGP cubre por completo (los
//     más específicos de BGP siguen ganando por LPM).
//   - RIR: rellenan el país de los prefijos y ASN que no lo tienen.
//   - PeeringDB: nombre y tipo de red del ASN (prevalece sobre iptoasn).
func (s *Service) Build(sources []datasets.Source) (*asn.Snapshot, []SourceReport, error) {
	type loaded struct {
		src  datasets.Source
		m    *datasets.Manifest
		data *SourceData
	}
	var (
		all     []loaded
		reports []SourceReport
		idx     = map[string]int{}
	)
	for _, src := range filterKind(sources) {
		rep := SourceReport{SourceID: src.ID}
		idx[src.ID] = len(reports)
		if ok, reason := src.Allowed(s.AllowUnverified); !ok {
			rep.Reason = reason
			reports = append(reports, rep)
			continue
		}
		f, m, err := s.Store.OpenCurrent(src.ID)
		if err != nil {
			rep.Reason = err.Error()
			reports = append(reports, rep)
			continue
		}
		path := f.Name()
		_ = f.Close()
		d, err := s.Parse(path, src)
		if err != nil {
			rep.Reason = fmt.Sprintf("versión vigente ilegible: %v", err)
			reports = append(reports, rep)
			continue
		}
		rep.Used = true
		reports = append(reports, rep)
		all = append(all, loaded{src: src, m: m, data: d})
	}

	// 1. BGP.
	bgp := iptrie.NewBuilder(func(old, _ asn.Route) asn.Route { return old })
	var routes []asn.Entry
	seen := map[netip.Prefix]bool{}
	for _, l := range all {
		if l.data.Role != RoleBGP {
			continue
		}
		for _, e := range l.data.Routes {
			if seen[e.Prefix] {
				continue
			}
			seen[e.Prefix] = true
			_ = bgp.Insert(e.Prefix, e.Route)
			routes = append(routes, e)
			reports[idx[l.src.ID]].Routes++
		}
	}
	bgpTable := bgp.Build()
	// 2. iptoasn como respaldo.
	for _, l := range all {
		if l.data.Role != RoleIPToASN {
			continue
		}
		for _, e := range l.data.Routes {
			if seen[e.Prefix] {
				continue
			}
			if _, _, covered := bgpTable.CoveringPrefix(e.Prefix); covered {
				continue
			}
			seen[e.Prefix] = true
			routes = append(routes, e)
			reports[idx[l.src.ID]].Routes++
		}
	}
	if len(routes) == 0 {
		return nil, reports, ErrNoRoutes
	}
	// 3. País desde los RIR.
	cb := iptrie.NewBuilder[string](nil)
	asns := map[uint32]asn.ASInfo{}
	for _, l := range all {
		if l.data.Role != RoleRegistry {
			continue
		}
		for _, c := range l.data.Countries {
			_ = cb.Insert(c.Prefix, c.Country)
		}
		for n, info := range l.data.ASInfo {
			if _, ok := asns[n]; !ok {
				asns[n] = asn.ASInfo{Country: info.Country}
			}
		}
	}
	countries := cb.Build()
	for i := range routes {
		if routes[i].Country == "" {
			if _, cc, ok := countries.CoveringPrefix(routes[i].Prefix); ok {
				routes[i].Country = cc
			}
		}
	}
	// 4. Nombres: iptoasn y después PeeringDB (prevalece en nombre y tipo).
	for _, role := range []Role{RoleIPToASN, RoleOrgInfo} {
		for _, l := range all {
			if l.data.Role != role {
				continue
			}
			for n, info := range l.data.ASInfo {
				cur := asns[n]
				if info.Name != "" && (role == RoleOrgInfo || cur.Name == "") {
					cur.Name, cur.Source = info.Name, info.Source
				}
				if info.NetworkType != "" {
					cur.NetworkType = info.NetworkType
				}
				if cur.Country == "" {
					cur.Country = info.Country
				}
				asns[n] = cur
			}
		}
	}
	var refs []datasets.SourceRef
	for _, l := range all {
		refs = append(refs, datasets.RefFromManifest(l.src, l.m, l.data.Entries()))
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	snap, err := asn.NewSnapshot(datasets.SnapshotMeta{CreatedAt: s.now(), Sources: refs}, routes, asns)
	if err != nil {
		return nil, reports, err
	}
	return snap, reports, nil
}

// ErrNeedsReview indica que el snapshot nuevo cambia más del umbral respecto
// al anterior y no se publica sin confirmación.
var ErrNeedsReview = errors.New("el snapshot cambia más del 5 % de los prefijos: requiere revisión")

// Publish compara con el último snapshot publicado en d y publica el nuevo
// salvo que el cambio supere ReviewThreshold y no se haya aceptado.
func Publish(d datasets.SnapshotDir, snap *asn.Snapshot, acceptLargeDiff bool, readPrev func(path string) (*asn.Snapshot, error)) (*datasets.SnapshotManifest, string, asn.DiffStats, error) {
	var prev *asn.Snapshot
	if _, path, err := d.Latest(); err == nil {
		if prev, err = readPrev(path); err != nil {
			return nil, "", asn.DiffStats{}, fmt.Errorf("leer el snapshot anterior: %w", err)
		}
	} else if !errors.Is(err, datasets.ErrNotFound) {
		return nil, "", asn.DiffStats{}, err
	}
	diff := asn.Diff(prev, snap)
	if prev != nil && diff.Ratio > ReviewThreshold && !acceptLargeDiff {
		return nil, "", diff, ErrNeedsReview
	}
	m, path, err := snap.Publish(d)
	return m, path, diff, err
}
