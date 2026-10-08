// Package selftest ejecuta la prueba sin router de `make sim-verify`: genera
// cada escenario en memoria, lo serializa (hfsim o pcap), lo vuelve a leer, lo
// decodifica con goflow2 y lo compara con su expected.json. También verifica
// y regenera los fixtures versionados para detectar cambios no intencionados.
package selftest

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/capture"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/expect"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/flow"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/sim"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/verify"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/scenarios"
)

// FixtureSeed es la semilla de los fixtures versionados.
const FixtureSeed = 1

// Case es una combinación a probar.
type Case struct {
	Scenario  string
	Protocol  flow.Protocol
	NAT       *bool
	IPv6      *bool
	NATFields bool
	Format    capture.Format
}

// Name describe el caso.
func (c Case) Name() string {
	n := c.Scenario + "/" + c.Protocol.String()
	if c.NAT != nil {
		n += fmt.Sprintf(" nat=%t", *c.NAT)
	}
	if c.IPv6 != nil {
		n += fmt.Sprintf(" ipv6=%t", *c.IPv6)
	}
	if c.NATFields {
		n += " nat-fields"
	}
	if c.Format == capture.FormatPcap {
		n += " pcap"
	}
	return n
}

func boolp(b bool) *bool { return &b }

// DefaultCases son todos los escenarios en IPFIX y v9, más variantes sin NAT,
// sin IPv6, con campos NAT y a través de pcap.
func DefaultCases() []Case {
	var cs []Case
	for _, n := range scenarios.Names() {
		for _, p := range []flow.Protocol{flow.IPFIX, flow.V9} {
			cs = append(cs, Case{Scenario: n, Protocol: p, Format: capture.FormatHFSim})
		}
	}
	return append(cs,
		Case{Scenario: "normal", Protocol: flow.IPFIX, NAT: boolp(false), Format: capture.FormatHFSim},
		Case{Scenario: "out_of_prefix", Protocol: flow.V9, NAT: boolp(false), Format: capture.FormatHFSim},
		Case{Scenario: "normal", Protocol: flow.V9, IPv6: boolp(false), Format: capture.FormatHFSim},
		Case{Scenario: "c2", Protocol: flow.IPFIX, NATFields: true, Format: capture.FormatPcap},
		Case{Scenario: "scan", Protocol: flow.V9, Format: capture.FormatPcap},
	)
}

// Generate ejecuta un caso con los parámetros de fixture y devuelve la
// captura serializada y su expected.json.
func Generate(ctx context.Context, c Case) ([]byte, *expect.Expected, error) {
	sc, err := sim.Load(c.Scenario)
	if err != nil {
		return nil, nil, err
	}
	var buf bytes.Buffer
	w, err := capture.NewWriter(&buf, c.Format)
	if err != nil {
		return nil, nil, err
	}
	exp, err := sim.Run(ctx, sc, sim.Options{
		Seed: FixtureSeed, Protocol: c.Protocol, Fixture: true, NAT: c.NAT, IPv6: c.IPv6, NATFields: c.NATFields,
	}, func(d capture.Datagram, _ int) error { return w.Write(d) })
	if err != nil {
		return nil, nil, err
	}
	if err := w.Flush(); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), exp, nil
}

// roundTrip serializa y vuelve a leer expected.json, como lo verá un consumidor.
func roundTrip(e *expect.Expected) (*expect.Expected, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("expected: %w", err)
	}
	var out expect.Expected
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("expected: %w", err)
	}
	return &out, nil
}

// VerifyCapture decodifica una captura y la compara con expected.
func VerifyCapture(r io.Reader, e *expect.Expected, opt verify.Options) (*verify.Report, error) {
	v, err := verify.New(e, opt)
	if err != nil {
		return nil, err
	}
	if err := capture.Read(r, func(d capture.Datagram) error { v.Feed(d); return nil }); err != nil {
		return nil, err
	}
	return v.Report(), nil
}

// RunCase genera y verifica un caso.
func RunCase(ctx context.Context, c Case) (*verify.Report, error) {
	data, exp, err := Generate(ctx, c)
	if err != nil {
		return nil, err
	}
	exp, err = roundTrip(exp)
	if err != nil {
		return nil, err
	}
	return VerifyCapture(bytes.NewReader(data), exp, verify.Options{})
}

// Run ejecuta los casos y escribe un resumen; verbose imprime cada
// comprobación. Devuelve true si todo pasa.
func Run(ctx context.Context, w io.Writer, cases []Case, verbose bool) (bool, error) {
	ok := true
	for _, c := range cases {
		rep, err := RunCase(ctx, c)
		if err != nil {
			ok = false
			if _, werr := fmt.Fprintf(w, "FAIL %-40s %v\n", c.Name(), err); werr != nil {
				return false, fmt.Errorf("selftest: %w", werr)
			}
			continue
		}
		if err := summarize(w, c.Name(), rep, verbose); err != nil {
			return false, err
		}
		ok = ok && rep.OK
	}
	return ok, nil
}

func summarize(w io.Writer, name string, rep *verify.Report, verbose bool) error {
	st := "OK  "
	if !rep.OK {
		st = "FAIL"
	}
	var fails []string
	for _, c := range rep.Checks {
		if !c.OK {
			fails = append(fails, c.Name+": "+c.Detail)
		}
	}
	line := fmt.Sprintf("%s %-40s %d comprobaciones", st, name, len(rep.Checks))
	if len(fails) > 0 {
		line += "\n     " + strings.Join(fails, "\n     ")
	}
	if _, err := fmt.Fprintln(w, line); err != nil {
		return fmt.Errorf("selftest: %w", err)
	}
	if verbose {
		return rep.Write(w)
	}
	return nil
}

// FixturePaths devuelve las rutas de captura y expected de un fixture.
func FixturePaths(dir, scenario string, p flow.Protocol) (capturePath, expectedPath string) {
	base := filepath.Join(dir, scenario, p.String())
	return base + ".hfsim.gz", base + ".expected.json"
}

// WriteFixtures regenera los fixtures de todos los escenarios en dir.
func WriteFixtures(ctx context.Context, dir string, w io.Writer) error {
	for _, n := range scenarios.Names() {
		if err := os.MkdirAll(filepath.Join(dir, n), 0o750); err != nil {
			return fmt.Errorf("fixtures: %w", err)
		}
		for _, p := range []flow.Protocol{flow.IPFIX, flow.V9} {
			data, exp, err := Generate(ctx, Case{Scenario: n, Protocol: p, Format: capture.FormatHFSim})
			if err != nil {
				return err
			}
			cp, ep := FixturePaths(dir, n, p)
			if err := writeGzip(cp, data); err != nil {
				return err
			}
			if err := exp.Save(ep); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "fixture %s (%d registros, %d datagramas)\n", cp, sumRecords(exp), sumDatagrams(exp)); err != nil {
				return fmt.Errorf("fixtures: %w", err)
			}
		}
	}
	return nil
}

func sumRecords(e *expect.Expected) (n uint64) {
	for _, x := range e.Exporters {
		n += x.Totals.DataRecords
	}
	return n
}

func sumDatagrams(e *expect.Expected) (n uint64) {
	for _, x := range e.Exporters {
		n += x.Totals.Datagrams
	}
	return n
}

// writeGzip escribe datos comprimidos sin nombre ni fecha en la cabecera.
func writeGzip(path string, data []byte) error {
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return fmt.Errorf("fixtures: %w", err)
	}
	if _, err := gz.Write(data); err != nil {
		return fmt.Errorf("fixtures: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("fixtures: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil { //nolint:gosec // fixture no sensible
		return fmt.Errorf("fixtures: %w", err)
	}
	return nil
}

func readGzip(path string) ([]byte, error) {
	fh, err := os.Open(path) //nolint:gosec // ruta interna de fixtures
	if err != nil {
		return nil, fmt.Errorf("fixtures: %w", err)
	}
	defer func() { _ = fh.Close() }()
	gz, err := gzip.NewReader(fh)
	if err != nil {
		return nil, fmt.Errorf("fixtures %s: %w", path, err)
	}
	b, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("fixtures %s: %w", path, err)
	}
	return b, nil
}

// CheckFixtures verifica cada fixture contra su expected.json y comprueba que
// regenerarlo con la misma semilla da exactamente los mismos bytes.
func CheckFixtures(ctx context.Context, dir string, w io.Writer, verbose bool) (bool, error) {
	ok := true
	found := 0
	names := scenarios.Names()
	sort.Strings(names)
	for _, n := range names {
		for _, p := range []flow.Protocol{flow.IPFIX, flow.V9} {
			cp, ep := FixturePaths(dir, n, p)
			label := "fixture " + n + "/" + p.String()
			exp, err := expect.Load(ep)
			if errors.Is(err, os.ErrNotExist) {
				ok = false
				if _, werr := fmt.Fprintf(w, "FAIL %-40s falta %s (regenera con: go run ./tools/flowsim/cmd/flowsim -fixtures-dir %s)\n", label, ep, dir); werr != nil {
					return false, fmt.Errorf("fixtures: %w", werr)
				}
				continue
			}
			if err != nil {
				return false, err
			}
			found++
			stored, err := readGzip(cp)
			if err != nil {
				return false, err
			}
			rep, err := VerifyCapture(bytes.NewReader(stored), exp, verify.Options{})
			if err != nil {
				return false, err
			}
			regen, gexp, err := Generate(ctx, Case{Scenario: n, Protocol: p, Format: capture.FormatHFSim})
			if err != nil {
				return false, err
			}
			same := bytes.Equal(regen, stored)
			b1, _ := json.Marshal(gexp)
			b2, _ := json.Marshal(exp)
			sameExp := bytes.Equal(b1, b2)
			rep.Checks = append(rep.Checks, verify.Check{
				Name: "regeneración determinista (semilla 1)", OK: same && sameExp,
				Detail: fmt.Sprintf("captura idéntica: %t, expected idéntico: %t", same, sameExp),
			})
			if !same || !sameExp {
				rep.OK = false
			}
			if err := summarize(w, label, rep, verbose); err != nil {
				return false, err
			}
			ok = ok && rep.OK
		}
	}
	return ok && found > 0, nil
}
