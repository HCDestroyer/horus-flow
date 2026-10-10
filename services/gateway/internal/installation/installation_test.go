package installation

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/clientip"
)

func codes(a Access) map[string]Warning {
	m := map[string]Warning{}
	for _, w := range a.Warnings {
		m[w.Code] = w
	}
	return m
}

func TestExternalProxyWarnings(t *testing.T) {
	res, err := clientip.Parse("172.31.250.0/24,192.168.1.10")
	if err != nil {
		t.Fatal(err)
	}
	in := New(Options{PublicBaseURL: "https://horus.isp.net", TLSMode: TLSExternal, Resolver: res, Metrics: prometheus.NewRegistry()})
	h := in.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	do := func(xff, proto string) {
		r := httptest.NewRequest("GET", "/api/v1/system/status", nil)
		r.RemoteAddr = "172.31.250.5:4000" // Traefik
		r.Header.Set("X-Forwarded-For", xff)
		r.Header.Set("X-Forwarded-Proto", proto)
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	do("198.51.100.7, 192.168.1.10", "https") // por el proxy: limpio
	a := in.Access()
	if len(codes(a)) != 0 || a.TLS.Mode != "external" || a.TLS.HSTS || a.AccessMode != "domain" || a.Host != "horus.isp.net" {
		t.Fatalf("acceso limpio = %+v", a)
	}
	do("203.0.113.66", "http") // saltándose el proxy (Traefik descartó sus cabeceras)
	do("198.51.100.7, 192.168.1.10", "http")
	c := codes(in.Access())
	if c[WarnProxyUntrusted].Severity != "critical" || c[WarnProxyProtoHTTP].Severity != "warning" {
		t.Fatalf("avisos = %+v", c)
	}
}

func TestNotExternalIsNoop(t *testing.T) {
	in := New(Options{PublicBaseURL: "https://203.0.113.10", TLSMode: "self_signed"})
	called := false
	h := in.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if !called {
		t.Fatal("no llama al siguiente")
	}
	a := in.Access()
	if a.AccessMode != "ip_only" || codes(a)[WarnIPOnly].Code == "" || codes(a)[WarnSelfSigned].Code == "" {
		t.Fatalf("ip_only = %+v", a)
	}
}

func TestSelfSignedFingerprint(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "203.0.113.10"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(10 * 24 * time.Hour), IPAddresses: []net.IP{net.ParseIP("203.0.113.10")}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "public.crt")
	if err := os.WriteFile(f, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(Options{PublicBaseURL: "https://203.0.113.10", AccessMode: "ip_only", TLSMode: "self_signed", CertFile: f}).Access()
	if a.TLS.FingerprintSHA256 == nil || len(*a.TLS.FingerprintSHA256) != 95 || a.TLS.NotAfter == nil {
		t.Fatalf("tls = %+v", a.TLS)
	}
	if codes(a)[WarnCertExpiring].Code == "" {
		t.Fatalf("sin aviso de caducidad: %+v", a.Warnings)
	}
}
