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
)

// parseIPToASN interpreta ip2asn-v4/v6/combined.tsv de iptoasn.com:
//
//	range_start \t range_end \t AS_number \t country_code \t AS_description
//
// Los rangos con AS 0 ("Not routed") se ignoran. Cada rango se descompone en
// prefijos CIDR.
func parseIPToASN(r io.Reader, src datasets.Source) (*Result, error) {
	res := &Result{ASInfo: map[uint32]asn.ASInfo{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		res.Lines++
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			res.Invalid++
			continue
		}
		first, err1 := netip.ParseAddr(f[0])
		last, err2 := netip.ParseAddr(f[1])
		n, err3 := strconv.ParseUint(f[2], 10, 32)
		if err1 != nil || err2 != nil || err3 != nil {
			res.Invalid++
			continue
		}
		if n == 0 {
			res.Ignored++
			continue
		}
		prefixes, err := iptrie.RangeToPrefixes(first, last)
		if err != nil {
			res.Invalid++
			continue
		}
		cc := normCountry(f[3])
		for _, p := range prefixes {
			if !routable(p) {
				continue
			}
			res.Routes = append(res.Routes, asn.Entry{Prefix: p, Route: asn.Route{ASN: uint32(n), Country: cc, Source: src.ID}})
		}
		if desc := strings.TrimSpace(f[4]); desc != "" && desc != "Not routed" {
			if _, ok := res.ASInfo[uint32(n)]; !ok {
				res.ASInfo[uint32(n)] = asn.ASInfo{Name: desc, Country: cc, Source: src.ID}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return res, nil
}

// normCountry devuelve un código ISO alfa-2 en mayúsculas o "" (iptoasn usa
// "None" y los RIR "ZZ" o vacío para desconocido).
func normCountry(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) != 2 || s == "ZZ" || s[0] < 'A' || s[0] > 'Z' || s[1] < 'A' || s[1] > 'Z' {
		return ""
	}
	return s
}
