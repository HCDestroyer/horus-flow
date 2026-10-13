package loopback

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hcdestroyer/horus-flow/services/assist/internal/tools"
)

func TestGetForwardsBearerAndDecodes(t *testing.T) {
	t.Parallel()
	var got *http.Request
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		switch r.URL.Path {
		case "/api/v1/findings/1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"1","kind":"scanning"}`))
		case "/api/v1/big":
			_, _ = w.Write([]byte(strings.Repeat("x", maxBody+10)))
		default:
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"PERMISSION_DENIED","status":403}`))
		}
	})
	r := New(h, "user-jwt")
	v, err := r.Get(context.Background(), "/api/v1/findings/1", url.Values{"limit": {"5"}})
	if err != nil {
		t.Fatal(err)
	}
	if v.(map[string]any)["kind"] != "scanning" {
		t.Fatalf("v = %v", v)
	}
	if got.Header.Get("Authorization") != "Bearer user-jwt" || got.Method != http.MethodGet || got.URL.RawQuery != "limit=5" {
		t.Fatalf("petición = %v %v %q", got.Method, got.Header, got.URL.RawQuery)
	}
	_, err = r.Get(context.Background(), "/api/v1/customers/1", nil)
	var he *tools.HTTPError
	if !errors.As(err, &he) || he.Status != 403 || he.Code != "PERMISSION_DENIED" {
		t.Fatalf("err = %v", err)
	}
	if _, err := r.Get(context.Background(), "/api/v1/big", nil); !errors.As(err, &he) || he.Code != "RESPONSE_TOO_LARGE" {
		t.Fatalf("respuesta grande: %v", err)
	}
	for _, bad := range []string{"/internal/x", "/api/v1/../metrics", "http://evil/api/v1/x"} {
		if _, err := r.Get(context.Background(), bad, nil); err == nil {
			t.Errorf("ruta %q aceptada", bad)
		}
	}
}
