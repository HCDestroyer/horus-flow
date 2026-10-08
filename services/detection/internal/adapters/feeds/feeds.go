// Package feeds interpreta los formatos de los feeds de reputación declarados
// en config/feeds.yaml y los normaliza a reputation.Entry.
//
// Cada parser es estricto con la estructura (cabeceras, contadores de
// registros, metadatos de cierre) para detectar archivos corruptos, truncados
// o páginas de error servidas como datos: un archivo rechazado nunca reemplaza
// la versión vigente (I0-17, criterio 3).
package feeds

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
)

var (
	// ErrEmpty: el archivo no contiene ninguna entrada válida.
	ErrEmpty = errors.New("feed vacío")
	// ErrCorrupt: estructura inválida, truncada o demasiadas líneas inválidas.
	ErrCorrupt = errors.New("feed corrupto")
	// ErrDangerous: la lista incluye entradas peligrosas (0.0.0.0/0, prefijos
	// demasiado amplios, rangos privados o protegidos) y la política las
	// rechaza, o cubre demasiado espacio de direcciones.
	ErrDangerous = errors.New("lista peligrosa")
	// ErrTooLarge: más entradas que las permitidas por la fuente.
	ErrTooLarge = errors.New("lista demasiado grande")
)

// Result es la salida de un parser.
type Result struct {
	Entries []reputation.Entry
	Lines   int // registros de datos leídos (sin comentarios)
	Invalid int // registros descartados por inválidos
	Ignored int // registros válidos pero fuera de alcance (p. ej. IOC de dominio)
	// Dropped: entradas peligrosas descartadas con aviso (ListPolicy en modo
	// DangerWarn).
	Dropped int
	// Dangerous cuenta las entradas peligrosas por motivo (en cualquier modo).
	Dangerous map[Danger]int
	// Warnings resume lo descartado para el informe del operador.
	Warnings []string
}

// Parser interpreta el contenido plano de un feed. fetchedAt se usa como fecha
// cuando la fuente no da la suya.
type Parser func(r io.Reader, src datasets.Source, fetchedAt time.Time) (*Result, error)

var parsers = map[string]Parser{
	"abusech-feodo-csv":     parseFeodo,
	"abusech-threatfox-csv": parseThreatFox,
	"spamhaus-drop-txt":     parseDropTxt,
	"spamhaus-drop-json":    parseDropJSON,
	"netset":                parseNetset,
	FormatIPList:            parseIPList,
	FormatCSV:               parseCSV,
}

// Formats devuelve los formatos soportados, ordenados.
func Formats() []string {
	out := make([]string, 0, len(parsers))
	for k := range parsers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Supports indica si existe parser para format.
func Supports(format string) bool { _, ok := parsers[format]; return ok }

// Parse descomprime si hace falta, interpreta r con el parser del formato de
// src y aplica la política de listas peligrosas que corresponde a la fuente
// (PolicyFor) y los controles de calidad comunes.
func Parse(r io.Reader, src datasets.Source, fetchedAt time.Time) (*Result, error) {
	return ParseWithPolicy(r, src, fetchedAt, PolicyFor(src))
}

// ParseWithPolicy es Parse con una política de listas peligrosas explícita
// (p. ej. con los prefijos propios del ISP como protegidos).
func ParseWithPolicy(r io.Reader, src datasets.Source, fetchedAt time.Time, pol ListPolicy) (*Result, error) {
	p, ok := parsers[src.Format]
	if !ok {
		return nil, fmt.Errorf("formato de feed %q no soportado (%s)", src.Format, strings.Join(Formats(), ", "))
	}
	plain, err := datasets.Decompress(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	defer func() { _ = plain.Close() }()
	res, err := p(plain, src, fetchedAt.UTC())
	if err != nil {
		return nil, err
	}
	if err := pol.apply(res); err != nil {
		return nil, err
	}
	if src.MaxEntries > 0 && len(res.Entries) > src.MaxEntries {
		return nil, fmt.Errorf("%w: %d entradas, máximo %d (max_entries)", ErrTooLarge, len(res.Entries), src.MaxEntries)
	}
	if err := checkQuality(res); err != nil {
		return nil, err
	}
	return res, nil
}

// ParseFile es Parse sobre un archivo.
func ParseFile(path string, src datasets.Source, fetchedAt time.Time) (*Result, error) {
	f, err := os.Open(path) //nolint:gosec // ruta del almacén local de datasets
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return Parse(f, src, fetchedAt)
}

// maxInvalidPct es el porcentaje de registros inválidos tolerado (además de
// un margen absoluto de 2) antes de considerar el archivo corrupto.
const maxInvalidPct = 5

func checkQuality(res *Result) error {
	if res.Invalid > 2 && res.Invalid*100 > res.Lines*maxInvalidPct {
		return fmt.Errorf("%w: %d de %d registros inválidos", ErrCorrupt, res.Invalid, res.Lines)
	}
	// Las entradas peligrosas descartadas con aviso también cuentan: una
	// lista llena de rangos privados o enormes no es fiable aunque se pida
	// solo avisar.
	if bad := res.Invalid + res.Dropped; bad > 2 && bad*100 > res.Lines*maxInvalidPct {
		return fmt.Errorf("%w: %d de %d registros inválidos o peligrosos", ErrDangerous, bad, res.Lines)
	}
	if len(res.Entries) == 0 {
		return fmt.Errorf("%w (%d registros, %d inválidos)", ErrEmpty, res.Lines, res.Invalid)
	}
	return nil
}

// parsePrefix acepta "1.2.3.4", "1.2.3.0/24" o IPv6 equivalentes (las IPv4
// mapeadas en IPv6 se normalizan a IPv4). Solo comprueba la sintaxis: si el
// prefijo es peligroso (demasiado amplio, privado, reservado) lo decide
// ListPolicy después de interpretar el archivo entero.
func parsePrefix(s string) (netip.Prefix, bool) {
	s = strings.TrimSpace(s)
	var p netip.Prefix
	if strings.Contains(s, "/") {
		var err error
		if p, err = netip.ParsePrefix(s); err != nil {
			return netip.Prefix{}, false
		}
	} else {
		a, err := netip.ParseAddr(s)
		if err != nil || a.Zone() != "" {
			return netip.Prefix{}, false
		}
		a = a.Unmap()
		p = netip.PrefixFrom(a, a.BitLen())
	}
	if p.Addr().Is4In6() {
		if p.Bits() < 96 {
			return netip.Prefix{}, false
		}
		p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
	}
	p = p.Masked()
	return p, p.IsValid()
}

// hostPrefix devuelve el prefijo /32 o /128 de una IP (sin zona).
func hostPrefix(a netip.Addr) (netip.Prefix, bool) {
	if !a.IsValid() || a.Zone() != "" {
		return netip.Prefix{}, false
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), true
}

// baseIndicator rellena los campos comunes desde la declaración de la fuente.
func baseIndicator(src datasets.Source, fallback reputation.Category, fetchedAt time.Time) reputation.Indicator {
	cat := reputation.Category(src.Category)
	if cat == "" {
		cat = fallback
	}
	conf := src.Confidence
	if conf == 0 {
		conf = 50
	}
	return reputation.Indicator{Source: src.ID, Category: cat, Confidence: uint8(conf), FirstSeen: fetchedAt} //nolint:gosec // 0–100 validado
}

// finish calcula la caducidad con el TTL de la fuente.
func finish(ind *reputation.Indicator, src datasets.Source, fetchedAt time.Time) {
	if src.TTL <= 0 {
		return
	}
	ref := fetchedAt
	if ind.LastSeen.After(ref) {
		ref = ind.LastSeen
	}
	ind.ExpiresAt = ref.Add(time.Duration(src.TTL))
}

// EntriesFromFile implementa feedsync.ParseFunc.
func EntriesFromFile(path string, src datasets.Source, fetchedAt time.Time) ([]reputation.Entry, []string, error) {
	res, err := ParseFile(path, src, fetchedAt)
	if err != nil {
		return nil, nil, err
	}
	return res.Entries, res.Warnings, nil
}
