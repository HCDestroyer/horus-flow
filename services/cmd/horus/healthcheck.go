package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
)

// healthcheck implementa `horus healthcheck [--live] [--timeout=2s]`: el
// healthcheck de los contenedores horus-* (compose, Dockerfile). Consulta
// /readyz (o /healthz con --live) del puerto de administración del proceso
// que ya corre (HORUS_ADMIN_ADDR) sin arrancar roles. Sale con 0 si responde
// 200 y con 1 si no.
func healthcheck(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("horus healthcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	live := fs.Bool("live", false, "check /healthz (liveness) instead of /readyz")
	timeout := fs.Duration("timeout", 2*time.Second, "HTTP timeout")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	cfg, err := config.Load[config.Common](environ)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "horus healthcheck: %v\n", err)
		return exitUsage
	}
	url := adminURL(cfg.AdminAddr)
	if *live {
		url += "/healthz"
	} else {
		url += "/readyz"
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "horus healthcheck: %v\n", err)
		return exitFailure
	}
	resp, err := (&http.Client{Timeout: *timeout}).Do(req)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "horus healthcheck: %v\n", err)
		return exitFailure
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		_, _ = fmt.Fprintf(stderr, "horus healthcheck: %s -> %d\n", url, resp.StatusCode)
		return exitFailure
	}
	_, _ = fmt.Fprintf(stdout, "ok\n")
	return exitOK
}

// adminURL convierte HORUS_ADMIN_ADDR (":8081", "0.0.0.0:8081",
// "127.0.0.1:9") en la URL de loopback a consultar.
func adminURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = "", strings.TrimPrefix(addr, ":")
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}
