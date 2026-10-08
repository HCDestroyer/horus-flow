package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAdminURL(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		":8081":          "http://127.0.0.1:8081",
		"0.0.0.0:9000":   "http://127.0.0.1:9000",
		"127.0.0.1:7":    "http://127.0.0.1:7",
		"[::]:8081":      "http://127.0.0.1:8081",
		"10.0.0.5:18081": "http://10.0.0.5:18081",
	} {
		if got := adminURL(in); got != want {
			t.Errorf("adminURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHealthcheckSubcommand(t *testing.T) {
	t.Parallel()
	var ready atomic.Bool
	ready.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/healthz":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/readyz" && ready.Load():
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	env := []string{"HORUS_ADMIN_ADDR=" + addr}
	run1 := func(args ...string) int {
		var out bytes.Buffer
		return run(context.Background(), append([]string{"healthcheck"}, args...), env, &out, io.Discard, roleCatalog)
	}
	if code := run1(); code != exitOK {
		t.Fatalf("ready: exit %d", code)
	}
	ready.Store(false)
	if code := run1(); code != exitFailure {
		t.Fatalf("not ready: exit %d", code)
	}
	if code := run1("--live"); code != exitOK {
		t.Fatalf("live: exit %d", code)
	}
	srv.Close()
	if code := run1("--timeout=200ms"); code != exitFailure {
		t.Fatalf("down: exit %d", code)
	}
	if code := run1("--bogus"); code != exitUsage {
		t.Fatalf("bad flag: exit %d", code)
	}
}
