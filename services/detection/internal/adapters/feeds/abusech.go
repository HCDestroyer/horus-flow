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

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
)

// abuseCSV es un CSV de abuse.ch: comentarios con '#', la cabecera va
// comentada ("# "first_seen_utc",…") y puede haber un pie
// "# Number of entries: N".
type abuseCSV struct {
	cols     map[string]int
	records  [][]string
	declared int // -1 si no hay pie con el número de entradas
}

func readAbuseCSV(r io.Reader, required ...string) (*abuseCSV, error) {
	out := &abuseCSV{declared: -1}
	var data strings.Builder
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(line, "#") {
			body := strings.TrimSpace(strings.TrimPrefix(line, "#"))
			switch {
			case out.cols == nil && strings.Contains(body, "first_seen_utc"):
				cr := csv.NewReader(strings.NewReader(body))
				cr.TrimLeadingSpace = true
				hdr, err := cr.Read()
				if err != nil {
					return nil, fmt.Errorf("%w: cabecera ilegible: %v", ErrCorrupt, err)
				}
				out.cols = make(map[string]int, len(hdr))
				for i, h := range hdr {
					out.cols[strings.TrimSpace(h)] = i
				}
			case strings.HasPrefix(strings.ToLower(body), "number of entries:"):
				n, err := strconv.Atoi(strings.TrimSpace(body[len("number of entries:"):]))
				if err != nil {
					return nil, fmt.Errorf("%w: pie ilegible %q", ErrCorrupt, body)
				}
				out.declared = n
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		data.WriteString(line)
		data.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if out.cols == nil {
		return nil, fmt.Errorf("%w: no se encontró la cabecera (¿página de error o formato cambiado?)", ErrCorrupt)
	}
	for _, c := range required {
		if _, ok := out.cols[c]; !ok {
			return nil, fmt.Errorf("%w: falta la columna %q", ErrCorrupt, c)
		}
	}
	cr := csv.NewReader(strings.NewReader(data.String()))
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = false
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// Una comilla sin cerrar suele ser una descarga truncada.
			return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		out.records = append(out.records, rec)
	}
	if out.declared >= 0 && out.declared != len(out.records) {
		return nil, fmt.Errorf("%w: el pie declara %d entradas y hay %d", ErrCorrupt, out.declared, len(out.records))
	}
	return out, nil
}

func (a *abuseCSV) get(rec []string, col string) string {
	i, ok := a.cols[col]
	if !ok || i >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[i])
}

func parseAbuseTime(s string) time.Time {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t
		}
	}
	return time.Time{}
}

// parseFeodo interpreta la lista de C2 de abuse.ch Feodo Tracker
// (ipblocklist.csv). Un C2 "offline" baja 20 puntos de confianza.
func parseFeodo(r io.Reader, src datasets.Source, fetchedAt time.Time) (*Result, error) {
	c, err := readAbuseCSV(r, "first_seen_utc", "dst_ip", "dst_port")
	if err != nil {
		return nil, err
	}
	res := &Result{Lines: len(c.records)}
	for _, rec := range c.records {
		if len(rec) != len(c.cols) {
			res.Invalid++
			continue
		}
		a, err := netip.ParseAddr(c.get(rec, "dst_ip"))
		hp, ok := hostPrefix(a)
		if err != nil || !ok {
			res.Invalid++
			continue
		}
		port, err := strconv.ParseUint(c.get(rec, "dst_port"), 10, 16)
		if err != nil {
			res.Invalid++
			continue
		}
		ind := baseIndicator(src, reputation.CategoryBotnetCC, fetchedAt)
		if t := parseAbuseTime(c.get(rec, "first_seen_utc")); !t.IsZero() {
			ind.FirstSeen = t
		}
		ind.LastSeen = parseAbuseTime(c.get(rec, "last_online"))
		ind.Port = uint16(port)
		ind.Threat = c.get(rec, "malware")
		if strings.EqualFold(c.get(rec, "c2_status"), "offline") {
			ind.Confidence = uint8(max(int(ind.Confidence)-20, 0)) //nolint:gosec // 0–100
		}
		finish(&ind, src, fetchedAt)
		res.Entries = append(res.Entries, reputation.Entry{Prefix: hp, Indicator: ind})
	}
	return res, nil
}

// parseThreatFox interpreta la exportación CSV ip:port de abuse.ch ThreatFox.
// Solo se usan IOC "ip:port"; los de dominio/URL se ignoran (no son de flujo).
// La confianza es el mínimo entre la de la fuente y la del IOC.
func parseThreatFox(r io.Reader, src datasets.Source, fetchedAt time.Time) (*Result, error) {
	c, err := readAbuseCSV(r, "first_seen_utc", "ioc_value", "ioc_type", "threat_type")
	if err != nil {
		return nil, err
	}
	res := &Result{Lines: len(c.records)}
	for _, rec := range c.records {
		if len(rec) != len(c.cols) {
			res.Invalid++
			continue
		}
		if c.get(rec, "ioc_type") != "ip:port" {
			res.Ignored++
			continue
		}
		ap, err := netip.ParseAddrPort(c.get(rec, "ioc_value"))
		hp, ok := hostPrefix(ap.Addr())
		if err != nil || !ok {
			res.Invalid++
			continue
		}
		cat := reputation.CategoryMalware
		if c.get(rec, "threat_type") == "botnet_cc" {
			cat = reputation.CategoryBotnetCC
		}
		ind := baseIndicator(src, cat, fetchedAt)
		ind.Category = cat // ThreatFox mezcla C2 y distribución: manda el tipo del IOC
		if t := parseAbuseTime(c.get(rec, "first_seen_utc")); !t.IsZero() {
			ind.FirstSeen = t
		}
		ind.LastSeen = parseAbuseTime(c.get(rec, "last_seen_utc"))
		if v, err := strconv.Atoi(c.get(rec, "confidence_level")); err == nil && v >= 0 && v <= 100 {
			ind.Confidence = min(ind.Confidence, uint8(v)) //nolint:gosec // 0–100
		}
		ind.Port = ap.Port()
		ind.Threat = c.get(rec, "malware_printable")
		ind.Reference = c.get(rec, "ioc_id")
		finish(&ind, src, fetchedAt)
		res.Entries = append(res.Entries, reputation.Entry{Prefix: hp, Indicator: ind})
	}
	return res, nil
}
