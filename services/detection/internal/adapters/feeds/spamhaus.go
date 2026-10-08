package feeds

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
)

// parseDropTxt interpreta Spamhaus DROP en texto ("prefijo ; SBLnnn", con
// comentarios ';'). Exige la cabecera "Spamhaus" para no aceptar una página
// de error con líneas que parezcan prefijos.
func parseDropTxt(r io.Reader, src datasets.Source, fetchedAt time.Time) (*Result, error) {
	res := &Result{}
	sawHeader := false
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ";") {
			if strings.Contains(line, "Spamhaus") {
				sawHeader = true
			}
			continue
		}
		res.Lines++
		cidr, sbl, _ := strings.Cut(line, ";")
		p, ok := parsePrefixOrAddr(cidr)
		if !ok {
			res.Invalid++
			continue
		}
		ind := baseIndicator(src, reputation.CategoryBlocklist, fetchedAt)
		ind.Reference = strings.TrimSpace(sbl)
		finish(&ind, src, fetchedAt)
		res.Entries = append(res.Entries, reputation.Entry{Prefix: p, Indicator: ind})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if !sawHeader {
		return nil, fmt.Errorf("%w: falta la cabecera de Spamhaus", ErrCorrupt)
	}
	return res, nil
}

type dropJSON struct {
	Type    string `json:"type"`
	CIDR    string `json:"cidr"`
	SBLID   string `json:"sblid"`
	RIR     string `json:"rir"`
	Records *int   `json:"records"`
}

// parseDropJSON interpreta Spamhaus DROP en NDJSON (drop_v4.json,
// drop_v6.json). El último registro es de metadatos y declara cuántos hay:
// si falta o no cuadra, el archivo está truncado.
func parseDropJSON(r io.Reader, src datasets.Source, fetchedAt time.Time) (*Result, error) {
	res := &Result{}
	declared := -1
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec dropJSON
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			res.Lines++
			res.Invalid++
			continue
		}
		if rec.Type == "metadata" {
			if rec.Records == nil {
				return nil, fmt.Errorf("%w: metadatos sin número de registros", ErrCorrupt)
			}
			declared = *rec.Records
			continue
		}
		res.Lines++
		p, ok := parsePrefixOrAddr(rec.CIDR)
		if !ok {
			res.Invalid++
			continue
		}
		ind := baseIndicator(src, reputation.CategoryBlocklist, fetchedAt)
		ind.Reference = rec.SBLID
		finish(&ind, src, fetchedAt)
		res.Entries = append(res.Entries, reputation.Entry{Prefix: p, Indicator: ind})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if declared < 0 {
		return nil, fmt.Errorf("%w: falta el registro de metadatos (¿descarga truncada?)", ErrCorrupt)
	}
	if declared != res.Lines {
		return nil, fmt.Errorf("%w: los metadatos declaran %d registros y hay %d", ErrCorrupt, declared, res.Lines)
	}
	return res, nil
}

// parseNetset interpreta listas de una IP o CIDR por línea con comentarios
// '#' (FireHOL .netset/.ipset, lista de salida de Tor…). La categoría sale de
// la declaración de la fuente.
func parseNetset(r io.Reader, src datasets.Source, fetchedAt time.Time) (*Result, error) {
	res := &Result{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		res.Lines++
		field := strings.Fields(line)[0]
		p, ok := parsePrefixOrAddr(field)
		if !ok {
			res.Invalid++
			continue
		}
		ind := baseIndicator(src, reputation.CategoryOther, fetchedAt)
		finish(&ind, src, fetchedAt)
		res.Entries = append(res.Entries, reputation.Entry{Prefix: p, Indicator: ind})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return res, nil
}
