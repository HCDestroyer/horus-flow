package datasets

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"
)

func TestEgressGuardAllowPrivate(t *testing.T) {
	t.Parallel()
	strict := &EgressGuard{}
	lab := &EgressGuard{AllowPrivate: true, AllowHTTP: true}
	for _, a := range []string{"10.0.0.5", "127.0.0.1", "100.64.1.1", "fd00::1"} {
		if err := strict.CheckAddr(netip.MustParseAddr(a)); !errors.Is(err, ErrEgressDenied) {
			t.Errorf("estricto %s: %v", a, err)
		}
		if err := lab.CheckAddr(netip.MustParseAddr(a)); err != nil {
			t.Errorf("laboratorio %s: %v", a, err)
		}
	}
	for _, a := range []string{"169.254.169.254", "fe80::1", "224.0.0.1", "0.0.0.0"} {
		if err := lab.CheckAddr(netip.MustParseAddr(a)); !errors.Is(err, ErrEgressDenied) {
			t.Errorf("laboratorio %s debe seguir prohibida: %v", a, err)
		}
	}
	u, _ := url.Parse("http://127.0.0.1/x")
	if err := strict.CheckURL(context.Background(), u); !errors.Is(err, ErrEgressDenied) {
		t.Errorf("http estricto: %v", err)
	}
	if err := lab.CheckURL(context.Background(), u); err != nil {
		t.Errorf("http laboratorio: %v", err)
	}
}

func TestEgressGuardDo(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if _, err := (&EgressGuard{}).Do(http.DefaultClient, req); !errors.Is(err, ErrEgressDenied) {
		t.Fatalf("loopback sin permiso: %v", err)
	}
	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	res, err := (&EgressGuard{AllowPrivate: true, AllowHTTP: true}).Do(&http.Client{Transport: tr}, req2)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatalf("status %d", res.StatusCode)
	}
}
