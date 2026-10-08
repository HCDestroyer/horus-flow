// Package asnsources interpreta las fuentes de prefijo → ASN → organización
// declaradas en datasets.yaml (docs/traffic-model.md §5): tablas BGP en MRT
// (RIPE RIS / RouteViews), iptoasn.com, estadísticas delegadas de los RIR y
// PeeringDB. Cada parser valida la estructura (cabeceras, contadores,
// registros truncados) para que un archivo corrupto nunca reemplace la
// versión vigente.
package asnsources

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"sort"
	"strings"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/traffic/internal/app/asnbuild"
)

var (
	// ErrEmpty: sin registros válidos.
	ErrEmpty = errors.New("dataset vacío")
	// ErrCorrupt: estructura inválida, truncada o demasiados registros inválidos.
	ErrCorrupt = errors.New("dataset corrupto")
)

// Result es la salida normalizada de un parser.
type Result = asnbuild.SourceData

type parser struct {
	role  asnbuild.Role
	parse func(r io.Reader, src datasets.Source) (*Result, error)
}

var parsers = map[string]parser{
	"mrt-table-dump-v2": {asnbuild.RoleBGP, parseMRT},
	"iptoasn-tsv":       {asnbuild.RoleIPToASN, parseIPToASN},
	"rir-delegated":     {asnbuild.RoleRegistry, parseRIR},
	"peeringdb-net":     {asnbuild.RoleOrgInfo, parsePeeringDB},
}

// Formats devuelve los formatos soportados.
func Formats() []string {
	out := make([]string, 0, len(parsers))
	for k := range parsers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// RoleOf devuelve el papel de un formato (0 si no se soporta).
func RoleOf(format string) asnbuild.Role { return parsers[format].role }

// Parse descomprime si hace falta e interpreta r según src.Format.
func Parse(r io.Reader, src datasets.Source) (*Result, error) {
	p, ok := parsers[src.Format]
	if !ok {
		return nil, fmt.Errorf("formato %q no soportado (%s)", src.Format, strings.Join(Formats(), ", "))
	}
	plain, err := datasets.Decompress(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	defer func() { _ = plain.Close() }()
	res, err := p.parse(plain, src)
	if err != nil {
		return nil, err
	}
	res.Role = p.role
	if res.Invalid > 2 && res.Invalid*100 > res.Lines*5 {
		return nil, fmt.Errorf("%w: %d de %d registros inválidos", ErrCorrupt, res.Invalid, res.Lines)
	}
	if res.Entries() == 0 {
		return nil, fmt.Errorf("%w (%d registros, %d inválidos)", ErrEmpty, res.Lines, res.Invalid)
	}
	return res, nil
}

// ParseFile es Parse sobre un archivo; implementa asnbuild.ParseFunc.
func ParseFile(path string, src datasets.Source) (*Result, error) {
	f, err := os.Open(path) //nolint:gosec // ruta del almacén local de datasets
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return Parse(f, src)
}

// routable descarta prefijos que no deben atribuirse (ruta por defecto,
// privados, documentación no, porque los fixtures la usan).
func routable(p netip.Prefix) bool {
	a := p.Addr()
	if !p.IsValid() || a.IsUnspecified() || a.IsLoopback() || a.IsMulticast() || a.IsLinkLocalUnicast() || a.IsPrivate() {
		return false
	}
	if a.Is4() {
		return p.Bits() >= 8
	}
	return p.Bits() >= 16
}
