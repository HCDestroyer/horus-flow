package asnsources

import (
	"bufio"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/iptrie"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/asn"
	"github.com/hcdestroyer/horus-flow/services/traffic/internal/app/asnbuild"
)

// maxASNBlock limita la expansión de un bloque de ASN delegado.
const maxASNBlock = 65536

// parseRIR interpreta las estadísticas delegadas (formato NRO, normal o
// extendido) de un RIR:
//
//	2|ripencc|20261007|123456|19830705|20261007|+0100      cabecera (versión|registro|serie|registros|…)
//	ripencc|*|ipv4|*|85000|summary                         resúmenes por tipo
//	ripencc|FR|ipv4|2.0.0.0|1048576|20100712|allocated|…   registros
//
// Se usan los registros allocated/assigned: país por bloque IPv4/IPv6 y por
// ASN. La cabecera y los resúmenes deben cuadrar con los registros leídos;
// si no, el archivo está truncado.
func parseRIR(r io.Reader, src datasets.Source) (*Result, error) {
	res := &Result{ASInfo: map[uint32]asn.ASInfo{}}
	declaredTotal := -1
	summary := map[string]int{}
	counted := map[string]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "|")
		if declaredTotal < 0 {
			// La primera línea útil es la cabecera de versión.
			if len(f) < 7 || !strings.HasPrefix(f[0], "2") {
				return nil, fmt.Errorf("%w: cabecera de estadísticas delegadas ausente o desconocida", ErrCorrupt)
			}
			n, err := strconv.Atoi(f[3])
			if err != nil || n < 0 {
				return nil, fmt.Errorf("%w: cabecera con número de registros inválido %q", ErrCorrupt, f[3])
			}
			declaredTotal = n
			continue
		}
		if len(f) >= 6 && f[1] == "*" && f[5] == "summary" {
			n, err := strconv.Atoi(f[4])
			if err != nil {
				return nil, fmt.Errorf("%w: resumen inválido %q", ErrCorrupt, line)
			}
			summary[f[2]] = n
			continue
		}
		res.Lines++
		if len(f) < 7 {
			res.Invalid++
			continue
		}
		typ, cc, start, value, status := f[2], normCountry(f[1]), f[3], f[4], f[6]
		counted[typ]++
		if status != "allocated" && status != "assigned" {
			res.Ignored++
			continue
		}
		switch typ {
		case "ipv4":
			a, err1 := netip.ParseAddr(start)
			n, err2 := strconv.ParseUint(value, 10, 64)
			if err1 != nil || err2 != nil || !a.Is4() {
				res.Invalid++
				continue
			}
			prefixes, err := iptrie.RangeFromCount(a, n)
			if err != nil {
				res.Invalid++
				continue
			}
			for _, p := range prefixes {
				if cc != "" {
					res.Countries = append(res.Countries, asnbuild.CountryBlock{Prefix: p, Country: cc})
				}
			}
		case "ipv6":
			a, err1 := netip.ParseAddr(start)
			bits, err2 := strconv.Atoi(value)
			if err1 != nil || err2 != nil || !a.Is6() || bits < 0 || bits > 128 {
				res.Invalid++
				continue
			}
			if cc != "" {
				res.Countries = append(res.Countries, asnbuild.CountryBlock{Prefix: netip.PrefixFrom(a, bits).Masked(), Country: cc})
			}
		case "asn":
			first, err1 := strconv.ParseUint(start, 10, 32)
			n, err2 := strconv.ParseUint(value, 10, 32)
			if err1 != nil || err2 != nil || n == 0 || n > maxASNBlock || first+n-1 > 0xFFFFFFFF {
				res.Invalid++
				continue
			}
			if cc == "" {
				continue
			}
			for a := first; a < first+n; a++ {
				res.ASInfo[uint32(a)] = asn.ASInfo{Country: cc, Source: src.ID} //nolint:gosec // rango validado
			}
		default:
			res.Invalid++
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if declaredTotal < 0 {
		return nil, fmt.Errorf("%w: archivo sin cabecera", ErrCorrupt)
	}
	if declaredTotal != res.Lines {
		return nil, fmt.Errorf("%w: la cabecera declara %d registros y hay %d", ErrCorrupt, declaredTotal, res.Lines)
	}
	for typ, n := range summary {
		if counted[typ] != n {
			return nil, fmt.Errorf("%w: el resumen de %s declara %d registros y hay %d", ErrCorrupt, typ, n, counted[typ])
		}
	}
	return res, nil
}
