package clientip

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestIP(t *testing.T) {
	r, err := New([]string{"172.31.250.0/24", "192.168.10.5"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, remote, xff, want string
	}{
		{"sin proxy", "203.0.113.7:5555", "", "203.0.113.7"},
		{"cliente directo con XFF falso: se ignora", "203.0.113.7:5555", "10.0.0.1", "203.0.113.7"},
		{"Traefik sin proxy externo", "172.31.250.10:4000", "198.51.100.20", "198.51.100.20"},
		{"proxy externo + Traefik", "172.31.250.10:4000", "198.51.100.20, 192.168.10.5", "198.51.100.20"},
		{"falsificado a la izquierda", "172.31.250.10:4000", "1.2.3.4, 198.51.100.20, 192.168.10.5", "198.51.100.20"},
		{"falsificado desde IP no confiable que pasa por Traefik", "172.31.250.10:4000", "1.2.3.4, 203.0.113.9", "203.0.113.9"},
		{"toda la cadena de confianza", "172.31.250.10:4000", "192.168.10.5", "192.168.10.5"},
		{"sin XFF desde Traefik", "172.31.250.10:4000", "", "172.31.250.10"},
		{"IPv6 y puertos", "172.31.250.10:4000", "[2001:db8::1]:443", "2001:db8::1"},
		{"salto ilegible corta la cadena", "172.31.250.10:4000", "1.2.3.4, basura, 192.168.10.5", "192.168.10.5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = c.remote
			if c.xff != "" {
				req.Header.Set("X-Forwarded-For", c.xff)
			}
			if got := r.IP(req).String(); got != c.want {
				t.Fatalf("IP = %s, quiero %s", got, c.want)
			}
		})
	}
}

func TestProto(t *testing.T) {
	r, _ := Parse("172.31.250.0/24")
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-Proto", "HTTPS, http")
	req.RemoteAddr = "203.0.113.7:1"
	if got := r.Proto(req); got != "" {
		t.Fatalf("Proto desde IP no confiable = %q", got)
	}
	req.RemoteAddr = "172.31.250.2:1"
	if got := r.Proto(req); got != "https" {
		t.Fatalf("Proto = %q", got)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse("10.0.0.0/33"); err == nil {
		t.Fatal("CIDR inválido aceptado")
	}
	if _, err := Parse("nada"); err == nil {
		t.Fatal("texto aceptado")
	}
	r, err := Parse(" ,10.0.0.0/8 ")
	if err != nil || !r.Trusted(mustAddr("10.1.2.3")) {
		t.Fatal("lista con huecos")
	}
}

func TestDefaultEmpty(t *testing.T) {
	SetDefault(&Resolver{})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "172.31.250.10:1"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := String(req); got != "172.31.250.10" {
		t.Fatalf("sin redes de confianza = %s", got)
	}
}

func mustAddr(s string) netip.Addr { return parseHop(s) }
