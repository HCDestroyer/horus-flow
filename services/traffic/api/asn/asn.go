// Package asn es el contrato público del snapshot de prefijos IP → ASN de
// origen → organización de `mod:traffic` (docs/traffic-model.md §5). El
// catálogo de clasificación (I3/I4) y el ingester lo usan para atribuir la IP
// remota de cada flujo a un sistema autónomo y su organización.
package asn

import (
	"fmt"
	"io"
	"maps"
	"net/netip"
	"slices"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/iptrie"
)

// Kind es el tipo de snapshot en el contenedor de datasets.
const Kind = "asn"

// PayloadFormat es la versión del formato del payload.
const PayloadFormat = 1

// Route es la atribución de un prefijo.
type Route struct {
	ASN     uint32 // ASN de origen
	Country string // ISO 3166-1 alfa-2 si se conoce
	Source  string // id de la fuente que ganó la consolidación (BGP > iptoasn > RIR)
}

// Entry asocia un prefijo a su atribución.
type Entry struct {
	Prefix netip.Prefix
	Route
}

// ASInfo describe un sistema autónomo.
type ASInfo struct {
	Name        string // organización / descripción
	Country     string
	NetworkType string // tipo de red según PeeringDB (asn.network_type), vacío si se desconoce
	Source      string // fuente del nombre
}

// Result es la respuesta de Lookup.
type Result struct {
	Prefix netip.Prefix
	Route
	AS ASInfo
}

// Snapshot es un conjunto inmutable de atribuciones; seguro para lecturas
// concurrentes.
type Snapshot struct {
	Meta  datasets.SnapshotMeta
	table *iptrie.Table[Route]
	asns  map[uint32]ASInfo
}

// NewSnapshot construye un snapshot. Si un prefijo se repite gana la
// primera aparición (el llamador ordena por prioridad de fuente).
func NewSnapshot(meta datasets.SnapshotMeta, routes []Entry, asns map[uint32]ASInfo) (*Snapshot, error) {
	b := iptrie.NewBuilder(func(old, _ Route) Route { return old })
	for _, e := range routes {
		if e.ASN == 0 {
			return nil, fmt.Errorf("asn: prefijo %s con ASN 0", e.Prefix)
		}
		if err := b.Insert(e.Prefix, e.Route); err != nil {
			return nil, err
		}
	}
	if asns == nil {
		asns = map[uint32]ASInfo{}
	}
	t := b.Build()
	meta.Kind, meta.PayloadFormat, meta.Entries = Kind, PayloadFormat, t.Len()
	return &Snapshot{Meta: meta, table: t, asns: asns}, nil
}

// Len devuelve el número de prefijos.
func (s *Snapshot) Len() int { return s.table.Len() }

// ASCount devuelve el número de sistemas autónomos con información.
func (s *Snapshot) ASCount() int { return len(s.asns) }

// Lookup devuelve la atribución del prefijo más específico que contiene a.
func (s *Snapshot) Lookup(a netip.Addr) (Result, bool) {
	p, r, ok := s.table.Lookup(a)
	if !ok {
		return Result{}, false
	}
	return Result{Prefix: p, Route: r, AS: s.asns[r.ASN]}, true
}

// AS devuelve la información de un ASN.
func (s *Snapshot) AS(n uint32) (ASInfo, bool) {
	i, ok := s.asns[n]
	return i, ok
}

// Payload codifica el snapshot (formato PayloadFormat):
//
//	uvarint nº de prefijos; por prefijo: prefijo | asn | country | source
//	uvarint nº de ASN;      por ASN (ascendente): asn | name | country | network_type | source
func (s *Snapshot) Payload() []byte {
	var e datasets.Enc
	e.Uvarint(uint64(s.table.Len()))
	for p, r := range s.table.All() {
		e.Prefix(p)
		e.Uvarint(uint64(r.ASN))
		e.String(r.Country)
		e.String(r.Source)
	}
	keys := slices.Sorted(maps.Keys(s.asns))
	e.Uvarint(uint64(len(keys)))
	for _, k := range keys {
		i := s.asns[k]
		e.Uvarint(uint64(k))
		e.String(i.Name)
		e.String(i.Country)
		e.String(i.NetworkType)
		e.String(i.Source)
	}
	return e.Bytes()
}

// WriteTo serializa el snapshot en el contenedor versionado.
func (s *Snapshot) WriteTo(w io.Writer) (int64, error) {
	cw := &countWriter{w: w}
	err := datasets.EncodeSnapshot(cw, s.Meta, s.Payload())
	return cw.n, err
}

// Publish guarda el snapshot como nueva versión en d.
func (s *Snapshot) Publish(d datasets.SnapshotDir) (*datasets.SnapshotManifest, string, error) {
	m, path, err := d.Publish(s.Meta, s.Payload())
	if err == nil {
		s.Meta.Version = m.Version
	}
	return m, path, err
}

// ReadSnapshot lee y verifica un snapshot IP→ASN.
func ReadSnapshot(r io.Reader) (*Snapshot, error) {
	meta, payload, err := datasets.DecodeSnapshot(r, Kind)
	if err != nil {
		return nil, err
	}
	if meta.PayloadFormat != PayloadFormat {
		return nil, fmt.Errorf("%w: formato de payload %d no soportado", datasets.ErrCorruptSnapshot, meta.PayloadFormat)
	}
	d := datasets.NewDec(payload)
	n := d.Uvarint()
	if n > uint64(len(payload)) {
		return nil, fmt.Errorf("%w: número de prefijos imposible", datasets.ErrCorruptSnapshot)
	}
	routes := make([]Entry, 0, n)
	for i := uint64(0); i < n && d.Err() == nil; i++ {
		p := d.Prefix()
		a := d.Uvarint()
		r := Route{ASN: uint32(a), Country: d.String(), Source: d.String()} //nolint:gosec // validado abajo
		if a > 0xFFFFFFFF {
			return nil, fmt.Errorf("%w: ASN fuera de rango", datasets.ErrCorruptSnapshot)
		}
		routes = append(routes, Entry{Prefix: p, Route: r})
	}
	k := d.Uvarint()
	if k > uint64(len(payload)) {
		return nil, fmt.Errorf("%w: número de ASN imposible", datasets.ErrCorruptSnapshot)
	}
	asns := make(map[uint32]ASInfo, k)
	for i := uint64(0); i < k && d.Err() == nil; i++ {
		a := d.Uvarint()
		info := ASInfo{Name: d.String(), Country: d.String(), NetworkType: d.String(), Source: d.String()}
		if a > 0xFFFFFFFF {
			return nil, fmt.Errorf("%w: ASN fuera de rango", datasets.ErrCorruptSnapshot)
		}
		asns[uint32(a)] = info
	}
	if err := d.Err(); err != nil {
		return nil, err
	}
	if !d.Done() {
		return nil, datasets.ErrTrailing
	}
	s, err := NewSnapshot(meta, routes, asns)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", datasets.ErrCorruptSnapshot, err)
	}
	if s.Meta.Entries != meta.Entries {
		return nil, fmt.Errorf("%w: %d prefijos, la meta declara %d", datasets.ErrCorruptSnapshot, s.Meta.Entries, meta.Entries)
	}
	return s, nil
}

// DiffStats compara dos snapshots por prefijo.
type DiffStats struct {
	Added, Removed, Changed int
	Base                    int     // prefijos del snapshot anterior
	Ratio                   float64 // (añadidos + eliminados + cambiados) / max(Base, 1)
}

// Diff calcula los cambios de next respecto a prev (cambio = otro ASN de
// origen). Con prev nil todo cuenta como añadido y Ratio es 0.
func Diff(prev, next *Snapshot) DiffStats {
	var st DiffStats
	if prev == nil {
		st.Added = next.Len()
		return st
	}
	st.Base = prev.Len()
	old := make(map[netip.Prefix]uint32, prev.Len())
	for p, r := range prev.table.All() {
		old[p] = r.ASN
	}
	for p, r := range next.table.All() {
		a, ok := old[p]
		switch {
		case !ok:
			st.Added++
		case a != r.ASN:
			st.Changed++
		}
		delete(old, p)
	}
	st.Removed = len(old)
	st.Ratio = float64(st.Added+st.Removed+st.Changed) / float64(max(st.Base, 1))
	return st
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
