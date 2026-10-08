package httpx_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

func TestServerDrainsInFlightRequestsOnStop(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	release := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "done")
	})
	srv := httpx.NewServer("api", "127.0.0.1:0", h, nil, httpx.ServerOptions{})
	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatal(err)
	}
	type result struct {
		body string
		err  error
	}
	res := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + srv.Addr().String() + "/") //nolint:noctx // test
		if err != nil {
			res <- result{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		res <- result{body: string(b), err: err}
	}()
	<-entered
	stopped := make(chan error, 1)
	go func() {
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		stopped <- srv.Stop(sctx)
	}()
	select {
	case err := <-stopped:
		t.Fatalf("Stop devolvió antes de terminar la petición en curso: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if r := <-res; r.err != nil || r.body != "done" {
		t.Fatalf("petición en curso = %+v", r)
	}
	if err := <-stopped; err != nil {
		t.Fatalf("Stop: %v", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := srv.Run(runCtx); err != nil {
		t.Fatalf("Run tras Stop: %v", err)
	}
}

func TestServerStopTimeoutForcesClose(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	srv := httpx.NewServer("api", "127.0.0.1:0", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-release
	}), nil, httpx.ServerOptions{})
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	go func() {
		resp, err := http.Get("http://" + srv.Addr().String() + "/") //nolint:noctx // test
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := srv.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop = %v", err)
	}
}

func TestServerStartFailsOnBusyPort(t *testing.T) {
	t.Parallel()
	a := httpx.NewServer("a", "127.0.0.1:0", http.NotFoundHandler(), nil, httpx.ServerOptions{})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.Stop(context.Background()) //nolint:errcheck // test
	b := httpx.NewServer("b", a.Addr().String(), http.NotFoundHandler(), nil, httpx.ServerOptions{})
	if err := b.Start(context.Background()); err == nil {
		t.Fatal("esperaba error de puerto ocupado")
	}
	if b.Addr() != nil {
		t.Fatal("Addr debe ser nil si no arrancó")
	}
	if err := b.Stop(context.Background()); err != nil {
		t.Fatalf("Stop sin Start: %v", err)
	}
	h := a.Hook()
	if h.Name != "http:a" || h.Start == nil || h.Run == nil || h.Stop == nil {
		t.Fatalf("hook = %+v", h)
	}
}

func TestRequestID(t *testing.T) {
	t.Parallel()
	var seen string
	h := httpx.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = observability.RequestIDFrom(r.Context())
	}))
	cases := map[string]bool{"abc-123": true, "": false, "bad id\n": false, strings.Repeat("a", 65): false}
	for in, keep := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if in != "" {
			req.Header.Set(httpx.HeaderRequestID, in)
		}
		h.ServeHTTP(rec, req)
		got := rec.Header().Get(httpx.HeaderRequestID)
		if got != seen || got == "" {
			t.Fatalf("%q: header %q, ctx %q", in, got, seen)
		}
		if keep != (got == in) {
			t.Fatalf("%q: got %q, keep=%v", in, got, keep)
		}
	}
}

func TestRecover(t *testing.T) {
	t.Parallel()
	h := httpx.Recover(observabilityDiscard())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	abort := httpx.Recover(observabilityDiscard())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if v := recover(); v != http.ErrAbortHandler { //nolint:errorlint // identidad
			t.Fatalf("ErrAbortHandler debe repropagarse, got %v", v)
		}
	}()
	abort.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestMuxRoutesWithRoleAndMetrics(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	m, err := httpx.NewMetrics(reg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := httpx.NewMetrics(reg); err == nil {
		t.Fatal("registrar dos veces debe fallar")
	}
	mux := httpx.NewMux(m, nil)
	var role string
	mux.ForService("devices").HandleFunc("GET /api/v1/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		role = observability.RoleFrom(r.Context())
		w.WriteHeader(http.StatusCreated)
	})
	mux.ForService("devices").HandleFunc("POST /api/v1/devices", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("x"))
	})
	mux.ForService("auth").HandleFunc("/api/v1/auth/any", func(http.ResponseWriter, *http.Request) {})
	if mux.Routes() != 3 {
		t.Fatalf("Routes = %d", mux.Routes())
	}
	h := mux.Handler()
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/devices/42", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/devices", nil),
		httptest.NewRequest("PROPFIND", "/api/v1/auth/any", nil),
	} {
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if role != "devices" {
		t.Fatalf("role en contexto = %q", role)
	}
	if got := testutil.ToFloat64(m.Requests().WithLabelValues("devices", "GET", "GET /api/v1/devices/{id}", "201")); got != 1 {
		t.Fatalf("requests GET = %v", got)
	}
	if got := testutil.ToFloat64(m.Requests().WithLabelValues("devices", "POST", "POST /api/v1/devices", "200")); got != 1 {
		t.Fatalf("requests POST = %v", got)
	}
	if got := testutil.ToFloat64(m.Requests().WithLabelValues("auth", "OTHER", "/api/v1/auth/any", "200")); got != 1 {
		t.Fatalf("requests OTHER = %v", got)
	}
	// La métrica nunca lleva el id real.
	n, err := testutil.GatherAndCount(reg, "http_server_requests_total")
	if err != nil || n != 3 {
		t.Fatalf("series = %d, %v", n, err)
	}
}

func TestMuxWithoutMetrics(t *testing.T) {
	t.Parallel()
	mux := httpx.NewMux(nil, nil)
	mux.ForService("x").HandleFunc("GET /x", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	rec := httptest.NewRecorder()
	mux.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestAdminHandler(t *testing.T) {
	t.Parallel()
	hr := health.NewRegistry(health.Options{Service: "horus"})
	hr.Role("auth").SetState(health.StateRunning)
	reg := observability.NewRegistry(observability.BuildInfo{Service: "horus", Version: "dev"})
	for _, pprofOn := range []bool{false, true} {
		h := httpx.AdminHandler(httpx.AdminOptions{Health: hr, Gatherer: reg, Pprof: pprofOn})
		for path, want := range map[string]int{"/healthz": 200, "/readyz": 200, "/metrics": 200} {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != want {
				t.Fatalf("%s = %d", path, rec.Code)
			}
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
		if (rec.Code == http.StatusOK) != pprofOn {
			t.Fatalf("pprof=%v code=%d", pprofOn, rec.Code)
		}
	}
}

func observabilityDiscard() *slog.Logger { return slog.New(slog.DiscardHandler) }
