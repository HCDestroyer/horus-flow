// Package iptrie ofrece una tabla inmutable de prefijos IPv4/IPv6 con búsqueda
// por coincidencia del prefijo más largo (LPM).
//
// La tabla se construye con un Builder y se "congela" en un arreglo ordenado de
// intervalos disjuntos: cada intervalo apunta al prefijo más específico que lo
// cubre y cada prefijo a su padre (el siguiente prefijo menos específico que lo
// contiene). Una búsqueda es una búsqueda binaria (O(log n), sin asignaciones),
// lo que cumple el objetivo de < 1 µs de mediana del snapshot de reputación
// (docs/backlog/increment-0.md, I0-17) con millones de prefijos y una huella de
// memoria compacta.
//
// No contiene lógica de dominio: los valores son genéricos.
package iptrie

import (
	"cmp"
	"fmt"
	"iter"
	"net/netip"
	"slices"
	"sort"
)

// Builder acumula prefijos antes de construir una Table. No es seguro para uso
// concurrente.
type Builder[V any] struct {
	m     map[netip.Prefix]V
	merge func(old, new V) V
}

// NewBuilder crea un Builder. merge decide qué valor queda cuando el mismo
// prefijo se inserta más de una vez; si es nil, gana el último.
func NewBuilder[V any](merge func(old, new V) V) *Builder[V] {
	return &Builder[V]{m: make(map[netip.Prefix]V), merge: merge}
}

// Insert añade p (se normaliza a su dirección de red; las IPv4 mapeadas en
// IPv6 se tratan como IPv4).
func (b *Builder[V]) Insert(p netip.Prefix, v V) error {
	p, err := Normalize(p)
	if err != nil {
		return err
	}
	if old, ok := b.m[p]; ok && b.merge != nil {
		v = b.merge(old, v)
	}
	b.m[p] = v
	return nil
}

// Len devuelve el número de prefijos distintos insertados.
func (b *Builder[V]) Len() int { return len(b.m) }

// Normalize valida p, convierte una IPv4 mapeada en IPv6 a IPv4 y devuelve el
// prefijo con los bits de host a cero.
func Normalize(p netip.Prefix) (netip.Prefix, error) {
	if !p.IsValid() {
		return netip.Prefix{}, fmt.Errorf("iptrie: prefijo inválido %q", p)
	}
	a := p.Addr()
	if a.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("iptrie: prefijo con zona %q", p)
	}
	if a.Is4In6() {
		if p.Bits() < 96 {
			return netip.Prefix{}, fmt.Errorf("iptrie: prefijo IPv4 mapeado demasiado corto %q", p)
		}
		p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
	}
	return p.Masked(), nil
}

type entry[V any] struct {
	prefix netip.Prefix
	value  V
	parent int32
}

type family struct {
	starts  []u128   // inicios de intervalo (IPv6)
	starts4 []uint32 // inicios de intervalo (IPv4): 4 bytes por intervalo, mejor localidad de caché
	idx     []int32  // índice en Table.entries, -1 si el intervalo no está cubierto
}

// Table es una tabla inmutable de prefijos. Es segura para lecturas
// concurrentes.
type Table[V any] struct {
	entries []entry[V]
	v4, v6  family
}

// Build congela los prefijos acumulados en una Table. El Builder puede seguir
// usándose después.
func (b *Builder[V]) Build() *Table[V] {
	t := &Table[V]{entries: make([]entry[V], 0, len(b.m))}
	for p, v := range b.m {
		t.entries = append(t.entries, entry[V]{prefix: p, value: v, parent: -1})
	}
	slices.SortFunc(t.entries, func(x, y entry[V]) int { return comparePrefix(x.prefix, y.prefix) })
	split := sort.Search(len(t.entries), func(i int) bool { return !t.entries[i].prefix.Addr().Is4() })
	t.v4 = t.buildFamily(0, split, 32)
	t.v4.starts4 = make([]uint32, len(t.v4.starts))
	for i, u := range t.v4.starts {
		t.v4.starts4[i] = uint32(u.lo)
	}
	t.v4.starts = nil
	t.v6 = t.buildFamily(split, len(t.entries), 128)
	return t
}

// comparePrefix ordena por familia (IPv4 primero), dirección de red y longitud
// ascendente (el prefijo que contiene va antes que los contenidos).
func comparePrefix(a, b netip.Prefix) int {
	if c := a.Addr().Compare(b.Addr()); c != 0 {
		return c
	}
	return cmp.Compare(a.Bits(), b.Bits())
}

func (t *Table[V]) buildFamily(from, to, width int) family {
	var f family
	emit := func(start u128, idx int32) {
		if n := len(f.idx); n > 0 && f.idx[n-1] == idx {
			return
		}
		f.starts = append(f.starts, start)
		f.idx = append(f.idx, idx)
	}
	maxAddr := hostMask(width, 0)
	end := func(i int32) u128 {
		p := t.entries[i].prefix
		return fromAddr(p.Addr()).or(hostMask(width, p.Bits()))
	}
	var (
		stack     []int32
		cursor    u128
		exhausted bool // el cursor pasó de la última dirección de la familia
	)
	closeTop := func() {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		e := end(top)
		if exhausted || e.less(cursor) {
			return
		}
		emit(cursor, top)
		if e == maxAddr {
			exhausted = true
			return
		}
		cursor = e.addOne()
	}
	for i := from; i < to; i++ {
		s := fromAddr(t.entries[i].prefix.Addr())
		for len(stack) > 0 && end(stack[len(stack)-1]).less(s) {
			closeTop()
		}
		parent := int32(-1)
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		if cursor.less(s) {
			emit(cursor, parent)
		}
		t.entries[i].parent = parent
		stack = append(stack, int32(i))
		cursor = s
	}
	for len(stack) > 0 {
		closeTop()
	}
	if !exhausted {
		emit(cursor, -1)
	}
	return f
}

// Len devuelve el número de prefijos de la tabla.
func (t *Table[V]) Len() int { return len(t.entries) }

// Segments devuelve el número de intervalos disjuntos (IPv4 + IPv6); sirve para
// estimar memoria.
func (t *Table[V]) Segments() int { return len(t.v4.idx) + len(t.v6.idx) }

func (t *Table[V]) find(a netip.Addr) int32 {
	if !a.IsValid() {
		return -1
	}
	a = a.Unmap()
	if a.Is4() {
		b := a.As4()
		u := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
		starts := t.v4.starts4
		lo, hi := 0, len(starts)
		for lo < hi {
			m := int(uint(lo+hi) >> 1)
			if u < starts[m] {
				hi = m
			} else {
				lo = m + 1
			}
		}
		if lo == 0 {
			return -1
		}
		return t.v4.idx[lo-1]
	}
	f := &t.v6
	u := fromAddr(a)
	// Primer intervalo cuyo inicio es > u; el anterior contiene a u.
	lo, hi := 0, len(f.starts)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if s := f.starts[m]; u.hi < s.hi || (u.hi == s.hi && u.lo < s.lo) {
			hi = m
		} else {
			lo = m + 1
		}
	}
	if lo == 0 {
		return -1
	}
	return f.idx[lo-1]
}

// Lookup devuelve el prefijo más específico que contiene a y su valor.
func (t *Table[V]) Lookup(a netip.Addr) (netip.Prefix, V, bool) {
	i := t.find(a)
	if i < 0 {
		var zero V
		return netip.Prefix{}, zero, false
	}
	e := &t.entries[i]
	return e.prefix, e.value, true
}

// Covering recorre todos los prefijos que contienen a, del más específico al
// menos específico.
func (t *Table[V]) Covering(a netip.Addr) iter.Seq2[netip.Prefix, V] {
	return func(yield func(netip.Prefix, V) bool) {
		for i := t.find(a); i >= 0; i = t.entries[i].parent {
			if !yield(t.entries[i].prefix, t.entries[i].value) {
				return
			}
		}
	}
}

// CoveringPrefix devuelve el prefijo más específico de la tabla que contiene
// por completo a p (igual o menos específico que p).
func (t *Table[V]) CoveringPrefix(p netip.Prefix) (netip.Prefix, V, bool) {
	p, err := Normalize(p)
	if err == nil {
		for i := t.find(p.Addr()); i >= 0; i = t.entries[i].parent {
			if t.entries[i].prefix.Bits() <= p.Bits() {
				return t.entries[i].prefix, t.entries[i].value, true
			}
		}
	}
	var zero V
	return netip.Prefix{}, zero, false
}

// All recorre los prefijos en orden determinista (IPv4 antes que IPv6, por
// dirección y longitud ascendente).
func (t *Table[V]) All() iter.Seq2[netip.Prefix, V] {
	return func(yield func(netip.Prefix, V) bool) {
		for i := range t.entries {
			if !yield(t.entries[i].prefix, t.entries[i].value) {
				return
			}
		}
	}
}
