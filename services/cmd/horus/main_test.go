package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/testkit"
)

func TestResolveRoles(t *testing.T) {
	t.Parallel()
	all := publicRoles(roleCatalog)
	if slices.Contains(all, "example") || len(all) < 14 || all[len(all)-1] != "gateway" {
		t.Fatalf("roles públicos = %v", all)
	}
	tests := []struct {
		name    string
		in      []string
		want    []string
		wantErr bool
	}{
		{name: "vacío = todos", in: nil, want: all},
		{name: "all", in: []string{"all"}, want: all},
		{name: "orden del catálogo", in: []string{"gateway", " devices"}, want: []string{"devices", "gateway"}},
		{name: "duplicados", in: []string{"collector", "collector"}, want: []string{"collector"}},
		{name: "oculto explícito", in: []string{"example"}, want: []string{"example"}},
		{name: "solo comas", in: []string{"", " "}, want: all},
		{name: "desconocido", in: []string{"gateway", "foo"}, wantErr: true},
	}
	withHidden, err := resolveRoles([]string{"all", "example"}, roleCatalog)
	if err != nil || len(withHidden) != len(all)+1 || !slices.Contains(roleNames(withHidden), "example") ||
		withHidden[len(withHidden)-1].name != "gateway" {
		t.Fatalf("all + example = %v, %v", roleNames(withHidden), err)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveRoles(tc.in, roleCatalog)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && !slices.Equal(roleNames(got), tc.want) {
				t.Fatalf("got %v, want %v", roleNames(got), tc.want)
			}
		})
	}
}

func TestUsageErrors(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		args    []string
		environ []string
		code    int
		stdout  string
	}{
		"version":      {args: []string{"--version"}, code: exitOK, stdout: "horus dev"},
		"help":         {args: []string{"-h"}, code: exitOK},
		"bad flag":     {args: []string{"--nope"}, code: exitUsage},
		"bad config":   {environ: []string{"HORUS_ENV=qa"}, code: exitUsage},
		"unknown role": {args: []string{"--roles=foo"}, code: exitUsage},
		"bad level":    {environ: []string{"HORUS_LOG_LEVEL=loud"}, code: exitUsage},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var out, errOut bytes.Buffer
			code := run(context.Background(), tc.args, tc.environ, &out, &errOut, roleCatalog)
			if code != tc.code {
				t.Fatalf("code = %d, want %d (stderr %q)", code, tc.code, errOut.String())
			}
			if !strings.Contains(out.String(), tc.stdout) {
				t.Fatalf("stdout = %q", out.String())
			}
		})
	}
}

// freeAddr reserva un puerto libre de loopback y lo libera para el proceso.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

type proc struct {
	admin, api string
	logs       *testkit.LogBuffer
	cancel     context.CancelFunc
	exit       chan int
}

func start(t *testing.T, catalog []roleSpec, roles string, extraEnv ...string) *proc {
	t.Helper()
	p := &proc{admin: freeAddr(t), api: freeAddr(t), logs: &testkit.LogBuffer{}, exit: make(chan int, 1)}
	environ := append([]string{
		"HORUS_ADMIN_ADDR=" + p.admin, "HORUS_HTTP_ADDR=" + p.api, "HORUS_SHUTDOWN_DELAY=0s",
		"HORUS_SHUTDOWN_TIMEOUT=5s", "HORUS_PROCESS=horus-test",
	}, extraEnv...)
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go func() { p.exit <- run(ctx, []string{"--roles=" + roles}, environ, p.logs, io.Discard, catalog) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-p.exit:
		case <-time.After(10 * time.Second):
			t.Error("el proceso no terminó")
		}
	})
	testkit.Eventually(t, 5*time.Second, func() bool {
		code, _ := httpGet("http://" + p.admin + "/healthz")
		return code == http.StatusOK
	}, "admin /healthz")
	return p
}

func (p *proc) stop(t *testing.T) int {
	t.Helper()
	p.cancel()
	select {
	case code := <-p.exit:
		p.exit <- code // para el Cleanup
		return code
	case <-time.After(10 * time.Second):
		t.Fatal("el proceso no terminó")
		return -1
	}
}

func httpGet(url string) (int, string) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return 0, err.Error()
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func readyz(t *testing.T, admin, query string) (int, health.Report) {
	t.Helper()
	code, body := httpGet("http://" + admin + "/readyz" + query)
	var rep health.Report
	if err := json.Unmarshal([]byte(body), &rep); err != nil {
		t.Fatalf("readyz no JSON (%d): %s", code, body)
	}
	return code, rep
}

// Criterios de aceptación 1 y 3 de I0-04 sobre el módulo de ejemplo.
func TestProcessHealthMetricsAndLogs(t *testing.T) {
	t.Parallel()
	deadDep := freeAddr(t) // puerto cerrado: dependencia que no responde
	p := start(t, roleCatalog, "example,collector", "HORUS_EXAMPLE_DEPENDENCY_ADDR="+deadDep)

	var rep health.Report
	testkit.Eventually(t, 5*time.Second, func() bool {
		var code int
		code, rep = readyz(t, p.admin, "")
		return code == http.StatusOK
	}, "readyz 200")
	if rep.Status != health.StatusDegraded || rep.Service != "horus-test" {
		t.Fatalf("readyz = %+v", rep)
	}
	ex := rep.Roles["example"]
	if ex.Status != health.StatusDegraded || !ex.Ready || ex.Checks["dependency"].Status != health.CheckFail {
		t.Fatalf("example = %+v", ex)
	}
	if c := rep.Roles["collector"]; c.Status != health.StatusOK || !c.Ready {
		t.Fatalf("collector = %+v", c)
	}
	if code, _ := readyz(t, p.admin, "?role=collector"); code != http.StatusOK {
		t.Fatalf("readyz?role=collector = %d", code)
	}
	if code, _ := httpGet("http://" + p.admin + "/readyz?role=auth"); code != http.StatusNotFound {
		t.Fatalf("readyz?role=auth (no activo) = %d", code)
	}
	if code, _ := httpGet("http://" + p.admin + "/healthz"); code != http.StatusOK {
		t.Fatalf("healthz = %d", code)
	}
	if code, body := httpGet("http://" + p.api + "/api/v1/example/hello/ana"); code != http.StatusOK ||
		!strings.Contains(body, "hola, ana") {
		t.Fatalf("hello = %d %s", code, body)
	}
	code, metrics := httpGet("http://" + p.admin + "/metrics")
	for _, want := range []string{
		`horus_build_info{`, `service="horus-test"`,
		`http_server_requests_total{code="200",method="GET",route="GET /api/v1/example/hello/{name}",service="example"} 1`,
	} {
		if code != http.StatusOK || !strings.Contains(metrics, want) {
			t.Fatalf("/metrics sin %q", want)
		}
	}
	if code, _ := httpGet("http://" + p.admin + "/debug/pprof/"); code != http.StatusNotFound {
		t.Fatalf("pprof debe estar desactivado por defecto: %d", code)
	}

	if code := p.stop(t); code != exitOK {
		t.Fatalf("exit = %d\n%s", code, p.logs.String())
	}
	started := p.logs.Find(t, "role started")
	if len(started) != 2 {
		t.Fatalf("role started = %v", started)
	}
	for _, rec := range p.logs.Records(t) {
		for _, k := range []string{"ts", "level", "msg", "service", "role", "trace_id", "span_id", "request_id", "tenant_id", "process", "version", "env"} {
			if _, ok := rec[k]; !ok {
				t.Fatalf("log sin %q: %v", k, rec)
			}
		}
	}
	if started[0]["role"] != "collector" || started[0]["service"] != "collector" || started[1]["role"] != "example" {
		t.Fatalf("orden/rol de arranque: %v", started)
	}
	if len(p.logs.Find(t, "shutdown complete")) != 1 {
		t.Fatal("falta 'shutdown complete'")
	}
}

// slowModule registra una ruta que bloquea hasta release.
func slowCatalog(entered chan<- struct{}, release <-chan struct{}) []roleSpec {
	return []roleSpec{{name: "slow", factory: func(_ context.Context, d module.Deps) (module.Module, error) {
		d.Routes.HandleFunc("GET /api/v1/slow", func(w http.ResponseWriter, _ *http.Request) {
			entered <- struct{}{}
			<-release
			_, _ = io.WriteString(w, "finished")
		})
		return module.Idle(), nil
	}}}
}

// Criterio de aceptación 2 de I0-04: con SIGTERM y peticiones en curso, las
// termina dentro del plazo y sale con 0; /readyz pasa a 503 al empezar.
func TestGracefulShutdownDrainsInFlightRequests(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	p := start(t, slowCatalog(entered, release), "slow", "HORUS_SHUTDOWN_DELAY=200ms")
	testkit.Eventually(t, 5*time.Second, func() bool {
		code, _ := readyz(t, p.admin, "")
		return code == http.StatusOK
	}, "readyz 200")

	type result struct {
		code int
		body string
	}
	res := make(chan result, 1)
	go func() {
		code, body := httpGet("http://" + p.api + "/api/v1/slow")
		res <- result{code, body}
	}()
	<-entered
	p.cancel() // equivalente a SIGTERM: main cancela ctx con signal.NotifyContext

	testkit.Eventually(t, 2*time.Second, func() bool {
		code, rep := readyz(t, p.admin, "")
		return code == http.StatusServiceUnavailable && rep.Status == health.StatusDraining
	}, "readyz 503 draining")
	close(release)
	if r := <-res; r.code != http.StatusOK || r.body != "finished" {
		t.Fatalf("petición en curso = %+v", r)
	}
	if code := p.stop(t); code != exitOK {
		t.Fatalf("exit = %d\n%s", code, p.logs.String())
	}
}

func TestShutdownTimeoutExitsWithFailure(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	catalog := []roleSpec{{name: "stuck", factory: func(context.Context, module.Deps) (module.Module, error) {
		return module.Func(func(context.Context) error { <-release; return nil }), nil
	}}}
	p := start(t, catalog, "stuck", "HORUS_SHUTDOWN_TIMEOUT=50ms")
	if code := p.stop(t); code != exitFailure {
		t.Fatalf("exit = %d, want %d", code, exitFailure)
	}
}

type lifecycleModule struct {
	startErr error
	events   chan string
}

func (m *lifecycleModule) Start(context.Context) error { m.events <- "start"; return m.startErr }
func (m *lifecycleModule) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (m *lifecycleModule) Stop(context.Context) error {
	m.events <- "stop"
	return errors.New("stop failed")
}

func TestStarterStopperAndFailures(t *testing.T) {
	t.Parallel()
	mod := &lifecycleModule{events: make(chan string, 4)}
	catalog := []roleSpec{{name: "full", factory: func(context.Context, module.Deps) (module.Module, error) { return mod, nil }}}
	p := start(t, catalog, "full")
	if code := p.stop(t); code != exitFailure { // Stop devuelve error
		t.Fatalf("exit = %d", code)
	}
	if a, b := <-mod.events, <-mod.events; a != "start" || b != "stop" {
		t.Fatalf("eventos = %s, %s", a, b)
	}

	env := []string{"HORUS_ADMIN_ADDR=" + freeAddr(t), "HORUS_SHUTDOWN_DELAY=0s"}
	failing := &lifecycleModule{startErr: errors.New("no db"), events: make(chan string, 4)}
	catalog = []roleSpec{{name: "bad", factory: func(context.Context, module.Deps) (module.Module, error) { return failing, nil }}}
	if code := run(context.Background(), []string{"--roles=bad"}, env, io.Discard, io.Discard, catalog); code != exitFailure {
		t.Fatalf("Start con error: exit = %d", code)
	}
	catalog = []roleSpec{{name: "broken", factory: func(context.Context, module.Deps) (module.Module, error) {
		return nil, errors.New("bad wiring")
	}}}
	if code := run(context.Background(), []string{"--roles=broken"}, env, io.Discard, io.Discard, catalog); code != exitFailure {
		t.Fatalf("Register con error: exit = %d", code)
	}
}

func TestAllRolesStartAndBecomeReady(t *testing.T) {
	t.Parallel()
	p := start(t, roleCatalog, "all", "HORUS_PPROF_ENABLED=true")
	var rep health.Report
	testkit.Eventually(t, 5*time.Second, func() bool {
		var code int
		code, rep = readyz(t, p.admin, "")
		return code == http.StatusOK
	}, "readyz 200")
	if rep.Status != health.StatusOK || len(rep.Roles) != len(publicRoles(roleCatalog)) {
		t.Fatalf("readyz = %+v", rep)
	}
	if code, _ := httpGet("http://" + p.admin + "/debug/pprof/"); code != http.StatusOK {
		t.Fatalf("pprof = %d", code)
	}
	if code := p.stop(t); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
}
