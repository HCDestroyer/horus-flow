package datasets

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// La cabecera secreta de una fuente se envía a su host y no se reenvía si
// una redirección lleva a otro.
func TestHTTPFetcherSourceHeader(t *testing.T) {
	var other string
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		other = r.Header.Get("Auth-Key")
		_, _ = io.WriteString(w, "192.0.2.1\n")
	}))
	defer b.Close()
	var got []string
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Auth-Key"))
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/away":
			http.Redirect(w, r, b.URL+"/list", http.StatusFound)
		default:
			_, _ = io.WriteString(w, "192.0.2.2\n")
		}
	}))
	defer a.Close()
	f := NewHTTPFetcher("test", 5*time.Second)
	src := Source{ID: "x", URL: a.URL + "/same", Header: http.Header{"Auth-Key": {"k1"}}}
	rc, err := f.Fetch(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !strings.Contains(string(body), "192.0.2.2") || len(got) != 2 || got[0] != "k1" || got[1] != "k1" {
		t.Fatalf("mismo host: %q %v", body, got)
	}
	src.URL = a.URL + "/away"
	rc, err = f.Fetch(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	if other != "" {
		t.Fatalf("la cabecera llegó a otro host: %q", other)
	}
}
