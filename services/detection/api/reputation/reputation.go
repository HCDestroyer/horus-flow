// Package reputation es el contrato público del snapshot de reputación de
// `mod:detection` (docs/traffic-model.md §11, ADR-0024): tipos de indicador,
// lectura/escritura del snapshot versionado y búsqueda por IP en memoria.
//
// El ingester carga el snapshot publicado (NATS Object Store
// `reputation-snapshots`, copia en store/catalog/reputation/) y marca cada flujo
// cuya IP remota coincide. Una coincidencia es un hecho, no un veredicto: la
// decisión (hallazgo) la toma `detection` correlacionando señales.
package reputation

import (
	"fmt"
	"hash/fnv"
	"io"
	"iter"
	"net/netip"
	"slices"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/iptrie"
)

// Kind es el tipo de snapshot en el contenedor de datasets.
const Kind = "reputation"

// PayloadFormat es la versión del formato del payload; cambia si cambia la
// codificación de Entry.
const PayloadFormat = 1

// Category clasifica un indicador (docs/traffic-model.md §8 y §11).
type Category string

const (
	CategoryBotnetCC  Category = "botnet_cc" // servidor de mando y control de botnet
	CategoryMalware   Category = "malware"   // distribución de malware / payload
	CategorySpam      Category = "spam"      // fuente de spam
	CategoryScanner   Category = "scanner"   // escáner / fuerza bruta
	CategoryBlocklist Category = "blocklist" // redes secuestradas o criminales (p. ej. Spamhaus DROP)
	CategoryTor       Category = "tor"       // nodo de salida Tor
	CategoryProxy     Category = "proxy"     // proxy abierto / residencial
	CategoryMining    Category = "mining"    // pool de minería
	CategoryOther     Category = "other"
)

// Valid indica si c es una categoría conocida.
func (c Category) Valid() bool {
	switch c {
	case CategoryBotnetCC, CategoryMalware, CategorySpam, CategoryScanner, CategoryBlocklist,
		CategoryTor, CategoryProxy, CategoryMining, CategoryOther:
		return true
	}
	return false
}

// Indicator es la información de una fuente sobre un prefijo.
type Indicator struct {
	Source     string    // id de la fuente (config/feeds.yaml)
	Category   Category  //
	Confidence uint8     // 0–100
	FirstSeen  time.Time // fecha en que la fuente lo listó por primera vez (o de descarga si no la da)
	LastSeen   time.Time // última vez visto activo según la fuente (cero si no la da)
	ExpiresAt  time.Time // caducidad calculada con el TTL de la fuente (cero = sin caducidad)
	Port       uint16    // puerto del indicador si la fuente lo da (C2 ip:port)
	Threat     string    // familia de malware o etiqueta (p. ej. "QakBot", "SBL123456")
	Reference  string    // id del indicador en la fuente
}

// Entry asocia un prefijo (o una IP como /32 o /128) a un indicador.
type Entry struct {
	Prefix netip.Prefix
	Indicator
}

// Hit es una coincidencia de Lookup.
type Hit struct {
	Prefix netip.Prefix
	Indicator
}

// Snapshot es un conjunto inmutable de indicadores indexado por prefijo.
// Seguro para lecturas concurrentes.
type Snapshot struct {
	Meta  datasets.SnapshotMeta
	table *iptrie.Table[[]Indicator]
}

// sameIndicator decide si dos indicadores son el mismo hecho de la misma
// fuente (se fusionan en vez de duplicarse).
func sameIndicator(a, b Indicator) bool {
	return a.Source == b.Source && a.Category == b.Category && a.Port == b.Port && a.Threat == b.Threat
}

func mergeIndicators(old, add []Indicator) []Indicator {
	for _, n := range add {
		merged := false
		for i := range old {
			if sameIndicator(old[i], n) {
				o := &old[i]
				if !n.FirstSeen.IsZero() && (o.FirstSeen.IsZero() || n.FirstSeen.Before(o.FirstSeen)) {
					o.FirstSeen = n.FirstSeen
				}
				if n.LastSeen.After(o.LastSeen) {
					o.LastSeen = n.LastSeen
				}
				if n.ExpiresAt.After(o.ExpiresAt) || n.ExpiresAt.IsZero() {
					o.ExpiresAt = n.ExpiresAt
				}
				o.Confidence = max(o.Confidence, n.Confidence)
				merged = true
				break
			}
		}
		if !merged {
			old = append(old, n)
		}
	}
	return old
}

// NewSnapshot construye un snapshot. meta.Kind y meta.PayloadFormat se fijan
// aquí y meta.Entries se recalcula (prefijos distintos).
func NewSnapshot(meta datasets.SnapshotMeta, entries []Entry) (*Snapshot, error) {
	b := iptrie.NewBuilder(mergeIndicators)
	for _, e := range entries {
		if !e.Category.Valid() {
			return nil, fmt.Errorf("reputation: categoría desconocida %q (%s)", e.Category, e.Source)
		}
		if err := b.Insert(e.Prefix, []Indicator{e.Indicator}); err != nil {
			return nil, err
		}
	}
	t := b.Build()
	// Orden estable de los indicadores de cada prefijo.
	for _, inds := range t.All() {
		slices.SortFunc(inds, func(x, y Indicator) int {
			if c := cmpStr(x.Source, y.Source); c != 0 {
				return c
			}
			if c := cmpStr(string(x.Category), string(y.Category)); c != 0 {
				return c
			}
			if x.Port != y.Port {
				return int(x.Port) - int(y.Port)
			}
			return cmpStr(x.Threat, y.Threat)
		})
	}
	meta.Kind, meta.PayloadFormat, meta.Entries = Kind, PayloadFormat, t.Len()
	return &Snapshot{Meta: meta, table: t}, nil
}

func cmpStr(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Len devuelve el número de prefijos distintos.
func (s *Snapshot) Len() int { return s.table.Len() }

// Lookup devuelve todos los indicadores de los prefijos que contienen a,
// del prefijo más específico al menos específico. Devuelve nil si no hay
// coincidencias. Asigna solo cuando hay coincidencia.
func (s *Snapshot) Lookup(a netip.Addr) []Hit {
	var out []Hit
	for p, inds := range s.table.Covering(a) {
		for _, ind := range inds {
			out = append(out, Hit{Prefix: p, Indicator: ind})
		}
	}
	return out
}

// Best devuelve el indicador más relevante para a: el prefijo más específico
// y, dentro de él, la mayor confianza. Sin asignaciones (camino caliente del
// ingester).
func (s *Snapshot) Best(a netip.Addr) (Hit, bool) {
	p, inds, ok := s.table.Lookup(a)
	if !ok || len(inds) == 0 {
		return Hit{}, false
	}
	best := 0
	for i := 1; i < len(inds); i++ {
		if inds[i].Confidence > inds[best].Confidence {
			best = i
		}
	}
	return Hit{Prefix: p, Indicator: inds[best]}, true
}

// WriteTo serializa el snapshot en el contenedor versionado de datasets.
func (s *Snapshot) WriteTo(w io.Writer) (int64, error) {
	cw := &countWriter{w: w}
	err := datasets.EncodeSnapshot(cw, s.Meta, s.Payload())
	return cw.n, err
}

// Payload codifica las entradas (formato PayloadFormat):
//
//	uvarint nº de prefijos
//	por prefijo: prefijo | uvarint nº de indicadores |
//	  por indicador: source | category | confidence | first_seen | last_seen |
//	  expires_at | port | threat | reference
//
// Las cadenas repetidas (fuente, categoría) ocupan poco tras zstd.
func (s *Snapshot) Payload() []byte {
	var e datasets.Enc
	e.Uvarint(uint64(s.table.Len()))
	for p, inds := range s.table.All() {
		e.Prefix(p)
		e.Uvarint(uint64(len(inds)))
		for _, ind := range inds {
			e.String(ind.Source)
			e.String(string(ind.Category))
			e.Uvarint(uint64(ind.Confidence))
			e.Time(ind.FirstSeen)
			e.Time(ind.LastSeen)
			e.Time(ind.ExpiresAt)
			e.Uvarint(uint64(ind.Port))
			e.String(ind.Threat)
			e.String(ind.Reference)
		}
	}
	return e.Bytes()
}

// Publish guarda el snapshot como nueva versión en un SnapshotDir y devuelve
// su manifiesto.
func (s *Snapshot) Publish(d datasets.SnapshotDir) (*datasets.SnapshotManifest, string, error) {
	m, path, err := d.Publish(s.Meta, s.Payload())
	if err == nil {
		s.Meta.Version = m.Version
	}
	return m, path, err
}

// ReadSnapshot lee y verifica un snapshot de reputación.
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
	entries := make([]Entry, 0, n)
	for i := uint64(0); i < n && d.Err() == nil; i++ {
		p := d.Prefix()
		k := d.Uvarint()
		if k > uint64(len(payload)) {
			return nil, fmt.Errorf("%w: número de indicadores imposible", datasets.ErrCorruptSnapshot)
		}
		for j := uint64(0); j < k && d.Err() == nil; j++ {
			ind := Indicator{
				Source:   d.String(),
				Category: Category(d.String()),
			}
			conf := d.Uvarint()
			ind.FirstSeen, ind.LastSeen, ind.ExpiresAt = d.Time(), d.Time(), d.Time()
			port := d.Uvarint()
			ind.Threat, ind.Reference = d.String(), d.String()
			if conf > 100 || port > 65535 {
				return nil, fmt.Errorf("%w: confianza o puerto fuera de rango", datasets.ErrCorruptSnapshot)
			}
			ind.Confidence, ind.Port = uint8(conf), uint16(port)
			entries = append(entries, Entry{Prefix: p, Indicator: ind})
		}
	}
	if err := d.Err(); err != nil {
		return nil, err
	}
	if !d.Done() {
		return nil, datasets.ErrTrailing
	}
	s, err := NewSnapshot(meta, entries)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", datasets.ErrCorruptSnapshot, err)
	}
	if s.Meta.Entries != meta.Entries {
		return nil, fmt.Errorf("%w: %d prefijos, la meta declara %d", datasets.ErrCorruptSnapshot, s.Meta.Entries, meta.Entries)
	}
	return s, nil
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

// SourceID convierte el id textual de una fuente en el UInt16 de
// flows_raw.reputation_source_id (FNV-1a, nunca 0). Es la misma función que
// usa el ingester al marcar la reputación en ingesta
// (services/ingester/internal/app.SourceID); detection la usa para resolver
// la fuente de un flujo marcado.
func SourceID(source string) uint16 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(source))
	return uint16(h.Sum32()%65535) + 1 //nolint:gosec // acotado a 1..65535
}

// All recorre todos los prefijos del snapshot con sus indicadores (barrido
// retroactivo de detection).
func (s *Snapshot) All() iter.Seq2[netip.Prefix, []Indicator] { return s.table.All() }
