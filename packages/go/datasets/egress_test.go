package datasets

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if a, ok := f[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
}

func addrs(ss ...string) []netip.Addr {
	out := make([]netip.Addr, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParseAddr(s)
	}
	return out
}

func TestEgressGuardURL(t *testing.T) {
	g := &EgressGuard{
		Deny: []netip.Prefix{netip.MustParsePrefix("203.0.113.10/32")}, // "la instalación"
		Resolver: fakeResolver{
			"good.example.net":  addrs("198.51.100.1", "2001:db8::1"),
			"evil.example.net":  addrs("10.0.0.1"),
			"mixed.example.net": addrs("198.51.100.2", "127.0.0.1"),
			"meta.example.net":  addrs("169.254.169.254"),
			"self.example.net":  addrs("203.0.113.10"),
			"cgnat.example.net": addrs("100.64.0.1"),
			"ula.example.net":   addrs("fd00::1"),
			"map.example.net":   addrs("::ffff:10.0.0.1"),
		},
	}
	ctx := context.Background()
	if err := g.CheckURL(ctx, mustURL(t, "https://good.example.net/x")); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{
		"http://good.example.net/x", "https://evil.example.net/", "https://mixed.example.net/", "https://meta.example.net/",
		"https://self.example.net/", "https://cgnat.example.net/", "https://ula.example.net/", "https://map.example.net/",
		"https://unknown.example.net/", "https://127.0.0.1/", "https://[::1]/", "https://203.0.113.10/",
	} {
		if err := g.CheckURL(ctx, mustURL(t, u)); !errors.Is(err, ErrEgressDenied) {
			t.Errorf("%s: %v", u, err)
		}
	}
	// Dirección real del socket (DNS rebinding) y redirecciones.
	if err := g.control("tcp", "127.0.0.1:443", nil); !errors.Is(err, ErrEgressDenied) {
		t.Errorf("control loopback: %v", err)
	}
	if err := g.control("tcp", "198.51.100.1:443", nil); err != nil {
		t.Errorf("control público: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "https://evil.example.net/", nil)
	if err := g.checkRedirect(req, []*http.Request{{}}); !errors.Is(err, ErrEgressDenied) {
		t.Errorf("redirección a privada: %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "http://good.example.net/", nil)
	if err := g.checkRedirect(req, nil); !errors.Is(err, ErrEgressDenied) {
		t.Errorf("redirección a http: %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "https://good.example.net/", nil)
	if err := g.checkRedirect(req, make([]*http.Request, 5)); !errors.Is(err, ErrEgressDenied) {
		t.Errorf("demasiadas redirecciones: %v", err)
	}
}

func TestHTTPFetcherGuardsCustomSources(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("192.0.2.1\n"))
	}))
	defer srv.Close()
	f := &HTTPFetcher{Client: srv.Client(), Guard: &EgressGuard{}}
	f.Client.Timeout = 5 * time.Second
	s := testSource("custom-x")
	s.URL = srv.URL + "/list.txt" // https://127.0.0.1:port
	// Una fuente del catálogo no pasa por el guardia.
	rc, err := f.Fetch(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	// Una personalizada hacia loopback se rechaza antes de conectar.
	s.Origin = OriginCustom
	if _, err := f.Fetch(context.Background(), s); !errors.Is(err, ErrEgressDenied) {
		t.Fatalf("personalizada a loopback: %v", err)
	}
	// Rebinding: la comprobación por DNS ve una IP pública, pero el socket
	// (resuelto por el sistema) va a loopback: lo corta el control del dialer.
	f.Guard.Resolver = fakeResolver{"localhost": addrs("198.51.100.1")}
	s.URL = "https://localhost:" + mustURL(t, srv.URL).Port() + "/list.txt"
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	_, err = f.Fetch(context.Background(), s)
	if !errors.Is(err, ErrEgressDenied) || !strings.Contains(err.Error(), "no es una dirección pública") {
		t.Fatalf("rebinding: %v", err)
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
