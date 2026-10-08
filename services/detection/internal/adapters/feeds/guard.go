package feeds

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
)

// Danger es el motivo por el que una entrada de una lista es peligrosa: si
// entrara en el snapshot marcaría como sospechoso tráfico legítimo (en el
// peor caso, todo).
type Danger string

const (
	// DangerDefaultRoute: 0.0.0.0/0 o ::/0.
	DangerDefaultRoute Danger = "default_route"
	// DangerReserved: solapa un rango privado, CGNAT, loopback, enlace local,
	// multicast u otro reservado (lo que verían todos los suscriptores de un
	// ISP detrás de CGNAT, por ejemplo).
	DangerReserved Danger = "reserved"
	// DangerTooBroad: prefijo más corto que el mínimo de la política.
	DangerTooBroad Danger = "too_broad"
	// DangerProtected: solapa un prefijo protegido (p. ej. los del propio ISP).
	DangerProtected Danger = "protected"
)

// Action es lo que hace la política con una entrada peligrosa.
type Action string

const (
	// ActionInvalid descarta la entrada y la cuenta como registro inválido
	// (comportamiento de las fuentes del catálogo desde I0-17: muchas
	// entradas inválidas hacen que el archivo se considere corrupto).
	ActionInvalid Action = "invalid"
	// ActionWarn descarta la entrada y deja un aviso en Result.Warnings.
	ActionWarn Action = "warn"
	// ActionReject rechaza la lista entera (se conserva la versión vigente).
	ActionReject Action = "reject"
)

// ListPolicy es la protección ante listas peligrosas. Se aplica después de
// interpretar el archivo, con todas las entradas a la vista.
type ListPolicy struct {
	// MinBitsV4/MinBitsV6: longitud mínima de prefijo aceptada.
	MinBitsV4, MinBitsV6 int
	// OnDangerous: acción ante entradas reservadas, demasiado amplias o
	// protegidas. Una ruta por defecto rechaza siempre la lista salvo con
	// ActionInvalid.
	OnDangerous Action
	// MaxAddressesV4: direcciones IPv4 distintas que puede cubrir la lista
	// entera (0 = sin límite). Superarlo rechaza la lista siempre.
	MaxAddressesV4 uint64
	// Protected: prefijos que ninguna lista puede marcar.
	Protected []netip.Prefix
}

// Límites por defecto.
const (
	// Catálogo (I0-17): un /4 o un ::/8 marcarían casi todo el tráfico.
	catalogMinBitsV4 = 8
	catalogMinBitsV6 = 16
	// Listas personalizadas: más estrictas. Un /12 IPv4 ya son ~1 M de
	// direcciones; en IPv6 nada más amplio que la asignación típica de un
	// LIR (/32).
	CustomMinBitsV4 = 12
	CustomMinBitsV6 = 32
	// CustomMaxAddressesV4 equivale a un /8 entero.
	CustomMaxAddressesV4 uint64 = 1 << 24
)

// CatalogPolicy es la política de las fuentes del catálogo embebido: las
// entradas peligrosas cuentan como inválidas, como en I0-17.
func CatalogPolicy() ListPolicy {
	return ListPolicy{MinBitsV4: catalogMinBitsV4, MinBitsV6: catalogMinBitsV6, OnDangerous: ActionInvalid}
}

// CustomPolicy es la política de las listas personalizadas (D20).
// onDangerous es Source.OnDangerous: "warn" descarta y avisa; cualquier otro
// valor (por defecto) rechaza la lista.
func CustomPolicy(onDangerous string) ListPolicy {
	act := ActionReject
	if onDangerous == datasets.DangerWarn {
		act = ActionWarn
	}
	return ListPolicy{
		MinBitsV4: CustomMinBitsV4, MinBitsV6: CustomMinBitsV6,
		OnDangerous: act, MaxAddressesV4: CustomMaxAddressesV4,
	}
}

// PolicyFor devuelve la política que corresponde a la fuente.
func PolicyFor(src datasets.Source) ListPolicy {
	if src.IsCustom() {
		return CustomPolicy(src.OnDangerous)
	}
	pol := CatalogPolicy()
	switch src.OnDangerous {
	case datasets.DangerReject:
		pol.OnDangerous = ActionReject
	case datasets.DangerWarn:
		pol.OnDangerous = ActionWarn
	}
	return pol
}

// Classify devuelve por qué p es peligroso para la política, o "" si no lo es.
func (pol ListPolicy) Classify(p netip.Prefix) Danger {
	if p.Bits() == 0 {
		return DangerDefaultRoute
	}
	if datasets.OverlapsReserved(p) {
		return DangerReserved
	}
	minBits := pol.MinBitsV6
	if p.Addr().Is4() {
		minBits = pol.MinBitsV4
	}
	if p.Bits() < minBits {
		return DangerTooBroad
	}
	for _, r := range pol.Protected {
		if r.Overlaps(p) {
			return DangerProtected
		}
	}
	return ""
}

const maxSamples = 3

// apply filtra res.Entries según la política.
func (pol ListPolicy) apply(res *Result) error {
	samples := map[Danger][]string{}
	kept := res.Entries[:0]
	for _, e := range res.Entries {
		d := pol.Classify(e.Prefix)
		if d == "" {
			kept = append(kept, e)
			continue
		}
		if res.Dangerous == nil {
			res.Dangerous = map[Danger]int{}
		}
		res.Dangerous[d]++
		if len(samples[d]) < maxSamples {
			samples[d] = append(samples[d], e.Prefix.String())
		}
	}
	res.Entries = kept
	if len(res.Dangerous) == 0 {
		return pol.checkCoverage(res.Entries)
	}
	dangers := make([]Danger, 0, len(res.Dangerous))
	for d := range res.Dangerous {
		dangers = append(dangers, d)
	}
	slices.Sort(dangers)
	describe := func(d Danger) string {
		return fmt.Sprintf("%d %s (p. ej. %s)", res.Dangerous[d], d, strings.Join(samples[d], ", "))
	}
	switch pol.OnDangerous {
	case ActionInvalid:
		for _, n := range res.Dangerous {
			res.Invalid += n
		}
	case ActionWarn:
		if res.Dangerous[DangerDefaultRoute] > 0 {
			return fmt.Errorf("%w: incluye una ruta por defecto: %s", ErrDangerous, describe(DangerDefaultRoute))
		}
		for _, d := range dangers {
			res.Dropped += res.Dangerous[d]
			res.Warnings = append(res.Warnings, "descartadas "+describe(d))
		}
	default: // ActionReject
		parts := make([]string, len(dangers))
		for i, d := range dangers {
			parts[i] = describe(d)
		}
		return fmt.Errorf("%w: %s", ErrDangerous, strings.Join(parts, "; "))
	}
	return pol.checkCoverage(res.Entries)
}

// checkCoverage rechaza la lista si cubre más direcciones IPv4 distintas que
// MaxAddressesV4 (p. ej. muchos /12 que por separado pasan el mínimo).
func (pol ListPolicy) checkCoverage(entries []reputation.Entry) error {
	if pol.MaxAddressesV4 == 0 {
		return nil
	}
	if n := coverageV4(entries); n > pol.MaxAddressesV4 {
		return fmt.Errorf("%w: cubre %d direcciones IPv4, máximo %d", ErrDangerous, n, pol.MaxAddressesV4)
	}
	return nil
}

// coverageV4 cuenta las direcciones IPv4 distintas cubiertas por entries.
func coverageV4(entries []reputation.Entry) uint64 {
	type span struct{ first, last uint64 }
	var spans []span
	for _, e := range entries {
		if !e.Prefix.Addr().Is4() {
			continue
		}
		b := e.Prefix.Addr().As4()
		first := uint64(b[0])<<24 | uint64(b[1])<<16 | uint64(b[2])<<8 | uint64(b[3])
		spans = append(spans, span{first, first + (uint64(1) << (32 - e.Prefix.Bits())) - 1})
	}
	slices.SortFunc(spans, func(a, b span) int { return cmp.Compare(a.first, b.first) })
	var total uint64
	var cur span
	open := false
	for _, s := range spans {
		switch {
		case !open:
			cur, open = s, true
		case s.first <= cur.last+1:
			cur.last = max(cur.last, s.last)
		default:
			total += cur.last - cur.first + 1
			cur = s
		}
	}
	if open {
		total += cur.last - cur.first + 1
	}
	return total
}
