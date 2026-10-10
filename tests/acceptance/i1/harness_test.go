//go:build acceptance

package i1

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/tests/acceptance/acceptkit"
)

// stage es un paso del informe: id, historias, criterio y dependencias.
type stage struct {
	ID        string
	Stories   string
	Criterion string
	Deps      []string
	Run       func(*world) error
}

// skipErr marca un paso como SKIP (con motivo) en vez de FAIL.
type skipErr struct{ why string }

func (s skipErr) Error() string { return s.why }

// expected es lo que usa la batería del expected.json del simulador
// (esquema horus.flowsim.expected/v1).
type expected struct {
	Scenario   string `json:"scenario"`
	Indicators []struct {
		IP     string `json:"ip"`
		Kind   string `json:"kind"`
		Source string `json:"source"`
	} `json:"indicators"`
	Findings []struct {
		Client   string `json:"client"`
		Kind     string `json:"kind"`
		Severity string `json:"severity"`
	} `json:"findings"`
	Exporters []struct {
		ExporterIP    string `json:"exporter_ip"`
		IPv6ClientLen int    `json:"ipv6_client_len"`
		Prefixes      struct {
			Customers      []string `json:"customers"`
			Infrastructure []string `json:"infrastructure"`
			Excluded       []string `json:"excluded"`
		} `json:"prefixes"`
		Totals struct {
			DataRecords int `json:"data_records"`
		} `json:"totals"`
		ByStatus     map[string]int `json:"by_status"`
		SequenceGaps uint64         `json:"sequence_gaps"`
		Clients      []struct {
			Key    string `json:"key"`
			Family int    `json:"family"`
		} `json:"clients"`
	} `json:"exporters"`
}

func loadExpected(path string) (*expected, error) {
	b, err := os.ReadFile(path) //nolint:gosec // fixture del repositorio
	if err != nil {
		return nil, err
	}
	var e expected
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(e.Exporters) != 1 {
		return nil, fmt.Errorf("%s: se esperaba un exportador", path)
	}
	return &e, nil
}

// node es un nodo con su router simulado: un escenario del simulador o la
// captura real de MikroTik.
type node struct {
	Name     string // escenario (normal, scan…) o "mikrotik-real"
	Fixture  string // captura (solo mikrotik-real)
	Exp      *expected
	ISP      *isp
	Site     string
	Router   string
	Index    int
	TunnelIP string
	Script   string
	Onb      onboarding
	done     chan error
	started  time.Time
	finished time.Time
}

// isp es un ISP de prueba.
type isp struct {
	Slug, ID string
}

// onboarding son los valores que el router toma del script de Horus.
type onboarding struct {
	TunnelIP, HubKey, Endpoint, Port, Services, Collector, Token string
}

// world es el estado compartido de la batería.
type world struct {
	t       *testing.T
	c       *acceptkit.Client
	s       *acceptkit.Session
	repo    string
	stateD  string
	suffix  string
	isps    map[string]*isp // demo, scan, c2, spam, dos_out, commercial, otro
	nodes   []*node
	byName  map[string]*node
	flowsim string
	replay  string
	simrtr  string
	reported map[string]string // resultado por paso
	feedAt  time.Time
	sendEnd time.Time
}

func (w *world) logf(f string, a ...any) { w.t.Logf(f, a...) }

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func repoRoot() string {
	d, _ := os.Getwd()
	for range 6 {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		d = filepath.Dir(d)
	}
	return "."
}

// --- Llamadas a la API -----------------------------------------------------------------------

func (w *world) token(scope string) string {
	tok, err := w.s.Token(scope)
	if err != nil {
		panic(fatal{err})
	}
	return tok
}

// fatal aborta el paso actual con un error (se recupera en runStage).
type fatal struct{ err error }

func failf(f string, a ...any) { panic(fatal{fmt.Errorf(f, a...)}) }

func (w *world) do(r acceptkit.Call) acceptkit.Resp {
	res, err := w.c.Do(r)
	if err != nil {
		panic(fatal{err})
	}
	return res
}

// expect hace la llamada y exige el estado; el mensaje describe qué se comprobaba.
func (w *world) expect(what string, r acceptkit.Call, status int) acceptkit.Resp {
	res := w.do(r)
	if res.Status != status {
		failf("%s: HTTP %d, se esperaba %d\n%s", what, res.Status, status, trunc(res.Raw))
	}
	w.logf("ok  %s (HTTP %d)", what, res.Status)
	return res
}

func trunc(b []byte) string {
	if len(b) > 1500 {
		return string(b[:1500]) + "…"
	}
	return string(b)
}

func get(tok, path string) acceptkit.Call {
	return acceptkit.Call{Method: http.MethodGet, Path: path, Token: tok}
}

func post(tok, path string, body any) acceptkit.Call {
	return acceptkit.Call{Method: http.MethodPost, Path: path, Token: tok, Body: body}
}

func idem(c acceptkit.Call) acceptkit.Call {
	if c.Header == nil {
		c.Header = map[string]string{}
	}
	c.Header["Idempotency-Key"] = uuid.NewString()
	return c
}

// eventually repite f hasta que devuelva nil o venza d; devuelve el último error.
func eventually(d, every time.Duration, f func() error) error {
	deadline := time.Now().Add(d)
	for {
		err := f()
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(every)
	}
}

// --- Órdenes externas ------------------------------------------------------------------------

func (w *world) run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...) //nolint:gosec // órdenes de la batería
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

// --- Script de onboarding ------------------------------------------------------------------------

var (
	reAddr     = regexp.MustCompile(`/ip address add address=([0-9.]+)/32 interface=wg-horus`)
	reHub      = regexp.MustCompile(`public-key="([A-Za-z0-9+/]{43}=)"`)
	reEndpoint = regexp.MustCompile(`endpoint-address=(\S+) endpoint-port=(\d+)`)
	reServices = regexp.MustCompile(`allowed-address=([0-9./]+)`)
	reTarget   = regexp.MustCompile(`/ip traffic-flow target add dst-address=([0-9.]+) port=4739`)
	reToken    = regexp.MustCompile(`\\"token\\":\\"([A-Za-z0-9_-]{20,})\\"`)
)

// parseScript extrae del script lo que un router aplicaría.
func parseScript(s string) (onboarding, error) {
	var o onboarding
	find := func(re *regexp.Regexp, what string, dst ...*string) error {
		m := re.FindStringSubmatch(s)
		if m == nil {
			return fmt.Errorf("el script no contiene %s", what)
		}
		for i, d := range dst {
			*d = m[i+1]
		}
		return nil
	}
	return o, errors.Join(
		find(reAddr, "la IP de túnel (/ip address add … interface=wg-horus)", &o.TunnelIP),
		find(reHub, "la clave pública del hub", &o.HubKey),
		find(reEndpoint, "el endpoint del hub", &o.Endpoint, &o.Port),
		find(reServices, "la red de servicios (allowed-address)", &o.Services),
		find(reTarget, "el destino IPFIX (/ip traffic-flow target add)", &o.Collector),
		find(reToken, "el token de enrolamiento", &o.Token),
	)
}

// scriptLine devuelve la instrucción completa (con continuaciones \) que empieza por prefix.
func scriptLine(s, prefix string) string {
	sc := bufio.NewScanner(strings.NewReader(s))
	var b strings.Builder
	in := false
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if !in && strings.HasPrefix(l, prefix) {
			in = true
		}
		if in {
			b.WriteString(strings.TrimSuffix(l, "\\"))
			b.WriteByte(' ')
			if !strings.HasSuffix(l, "\\") {
				break
			}
		}
	}
	return b.String()
}

// --- Informe ----------------------------------------------------------------------------------

// runStages ejecuta los pasos en orden y escribe el informe (TSV) que
// scripts/accept/accept-i1.sh incorpora a su resumen.
func runStages(t *testing.T, w *world, stages []stage) {
	report := env("ACCEPT_REPORT", filepath.Join(w.stateD, "e2e-report.tsv"))
	f, err := os.Create(report) //nolint:gosec // informe de la batería
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	only := env("ACCEPT_E2E_STAGES", "")
	failed := false
	for _, st := range stages {
		res, note := "OK", ""
		start := time.Now()
		switch {
		case only != "" && !slices.Contains(strings.Split(only, ","), st.ID):
			res, note = "SKIP", "no está en ACCEPT_E2E_STAGES"
		default:
			for _, d := range st.Deps {
				if w.reported[d] != "OK" {
					res, note = "SKIP", fmt.Sprintf("depende de '%s' (%s)", d, w.reported[d])
					break
				}
			}
		}
		if res == "OK" {
			t.Logf("==> [%s] %s (%s)", st.ID, st.Criterion, st.Stories)
			err := w.runStage(st)
			var sk skipErr
			switch {
			case errors.As(err, &sk):
				res, note = "SKIP", sk.why
			case err != nil:
				res, note = "FAIL", err.Error()
				failed = true
			}
		}
		w.reported[st.ID] = res
		el := time.Since(start).Round(time.Second)
		if res == "FAIL" {
			t.Errorf("FALLO [%s] %s (historias %s)\n%s", st.ID, st.Criterion, st.Stories, note)
		} else {
			t.Logf("    %s [%s] en %s %s", res, st.ID, el, note)
		}
		oneLine := strings.Join(strings.Fields(note), " ")
		if len(oneLine) > 300 {
			oneLine = oneLine[:300] + "…"
		}
		_, _ = fmt.Fprintf(f, "%s\t%s\t%d\t%s\t%s\t%s\n", st.ID, res, int(el.Seconds()), st.Stories, st.Criterion, oneLine)
		_ = f.Sync()
	}
	if failed {
		t.Fail()
	}
}

func (w *world) runStage(st stage) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if f, ok := r.(fatal); ok {
				err = f.err
				return
			}
			panic(r)
		}
	}()
	return st.Run(w)
}
