package feeds

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/iptrie"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
)

// Formatos genéricos pensados para las listas personalizadas del superadmin
// (D20); también sirven para fuentes del catálogo.
const (
	// FormatIPList: texto plano con una IP, CIDR, rango "a-b" o ip:puerto por
	// línea; comentarios con '#', ';' o '//' (de línea completa o al final).
	FormatIPList = "ip-list"
	// FormatCSV: CSV con la IP/CIDR en una columna configurable
	// (Source.CSV).
	FormatCSV = "csv"
)

// maxRangePrefixes limita los prefijos que genera un rango "a-b" (un rango
// arbitrario de IPv6 puede dar hasta 254).
const maxRangePrefixes = 64

// parseToken interpreta una IP, un CIDR, un rango "a-b" o "ip:puerto" /
// "[ipv6]:puerto". Solo comprueba la sintaxis.
func parseToken(s string) ([]netip.Prefix, uint16, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, 0, false
	}
	if first, last, ok := strings.Cut(s, "-"); ok && !strings.Contains(s, "/") {
		a, err1 := netip.ParseAddr(strings.TrimSpace(first))
		b, err2 := netip.ParseAddr(strings.TrimSpace(last))
		if err1 != nil || err2 != nil || a.Zone() != "" || b.Zone() != "" {
			return nil, 0, false
		}
		ps, err := iptrie.RangeToPrefixes(a.Unmap(), b.Unmap())
		if err != nil || len(ps) == 0 || len(ps) > maxRangePrefixes {
			return nil, 0, false
		}
		return ps, 0, true
	}
	if p, ok := parsePrefix(s); ok {
		return []netip.Prefix{p}, 0, true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil && ap.Port() != 0 {
		if hp, ok := hostPrefix(ap.Addr()); ok {
			return []netip.Prefix{hp}, ap.Port(), true
		}
	}
	return nil, 0, false
}

// addToken añade a res las entradas de un registro con la IP/CIDR en tok.
func addToken(res *Result, tok string, src datasets.Source, fetchedAt time.Time) {
	res.Lines++
	ps, port, ok := parseToken(tok)
	if !ok {
		res.Invalid++
		return
	}
	for _, p := range ps {
		ind := baseIndicator(src, reputation.CategoryOther, fetchedAt)
		ind.Port = port
		finish(&ind, src, fetchedAt)
		res.Entries = append(res.Entries, reputation.Entry{Prefix: p, Indicator: ind})
	}
}

// stripComment quita un comentario '#', ';' o '//' de la línea.
func stripComment(line string) string {
	cut := len(line)
	for _, marker := range []string{"#", ";", "//"} {
		if i := strings.Index(line, marker); i >= 0 && i < cut {
			cut = i
		}
	}
	return line[:cut]
}

// parseIPList interpreta FormatIPList. Del contenido de cada línea solo se
// usa el primer campo (separado por espacios, tabuladores o comas), salvo
// los rangos escritos "a - b".
func parseIPList(r io.Reader, src datasets.Source, fetchedAt time.Time) (*Result, error) {
	res := &Result{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			line, first = strings.TrimPrefix(line, "\uFEFF"), false
		}
		line = strings.TrimSpace(stripComment(line))
		if line == "" {
			continue
		}
		fields := strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == '\t' || r == ',' })
		tok := fields[0]
		if len(fields) >= 3 && fields[1] == "-" {
			tok = fields[0] + "-" + fields[2]
		}
		addToken(res, tok, src, fetchedAt)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return res, nil
}

// errCSVOptions es un error de configuración de la fuente, no del contenido.
var errCSVOptions = errors.New("opciones csv inválidas")

// csvSettings son las CSVOptions con los valores por defecto resueltos.
type csvSettings struct {
	delim   rune
	comment rune // 0 = sin comentarios
	index   int  // 0-based; -1 si se busca por nombre en la cabecera
	name    string
	header  bool
}

// CheckCSVOptions valida las opciones del formato csv (también lo usa la
// validación de fuentes personalizadas).
func CheckCSVOptions(o *datasets.CSVOptions) error {
	_, err := csvSettingsFrom(o)
	return err
}

func csvSettingsFrom(o *datasets.CSVOptions) (csvSettings, error) {
	if o == nil || strings.TrimSpace(o.Column) == "" {
		return csvSettings{}, fmt.Errorf("%w: falta csv.column", errCSVOptions)
	}
	st := csvSettings{delim: ',', comment: '#', index: -1}
	if o.Delimiter != "" {
		r, n := utf8.DecodeRuneInString(o.Delimiter)
		if n != len(o.Delimiter) || !strings.ContainsRune(",;\t|", r) {
			return csvSettings{}, fmt.Errorf("%w: delimiter %q (uno de , ; | o tabulador)", errCSVOptions, o.Delimiter)
		}
		st.delim = r
	}
	switch o.Comment {
	case "":
	case "-":
		st.comment = 0
	default:
		r, n := utf8.DecodeRuneInString(o.Comment)
		if n != len(o.Comment) || !strings.ContainsRune("#;/!%", r) || r == st.delim {
			return csvSettings{}, fmt.Errorf("%w: comment %q (un carácter de # ; / ! %%, o \"-\")", errCSVOptions, o.Comment)
		}
		st.comment = r
	}
	col := strings.TrimSpace(o.Column)
	if n, err := strconv.Atoi(col); err == nil {
		if n < 1 || n > 256 {
			return csvSettings{}, fmt.Errorf("%w: column %d fuera de 1–256", errCSVOptions, n)
		}
		st.index = n - 1
	} else {
		st.name = strings.ToLower(col)
		st.header = true
	}
	if o.Header != nil {
		st.header = *o.Header
	}
	if st.index < 0 && !st.header {
		return csvSettings{}, fmt.Errorf("%w: column por nombre exige header", errCSVOptions)
	}
	return st, nil
}

// parseCSV interpreta FormatCSV.
func parseCSV(r io.Reader, src datasets.Source, fetchedAt time.Time) (*Result, error) {
	st, err := csvSettingsFrom(src.CSV)
	if err != nil {
		return nil, err
	}
	cr := csv.NewReader(r)
	cr.Comma, cr.Comment = st.delim, st.comment
	cr.FieldsPerRecord, cr.LazyQuotes, cr.TrimLeadingSpace, cr.ReuseRecord = -1, true, true, true
	res := &Result{}
	needHeader := st.header
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue
		}
		if needHeader {
			needHeader = false
			if st.index < 0 {
				for i, h := range rec {
					if strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF"))) == st.name {
						st.index = i
						break
					}
				}
				if st.index < 0 {
					return nil, fmt.Errorf("%w: la cabecera no tiene la columna %q", ErrCorrupt, st.name)
				}
			}
			continue
		}
		if st.index >= len(rec) {
			res.Lines++
			res.Invalid++
			continue
		}
		addToken(res, rec[st.index], src, fetchedAt)
	}
	if needHeader {
		return nil, fmt.Errorf("%w: falta la cabecera", ErrEmpty)
	}
	return res, nil
}
