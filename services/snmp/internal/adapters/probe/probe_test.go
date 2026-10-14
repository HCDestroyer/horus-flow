package probe

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/icmp"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

func ptr[T any](v T) *T { return &v }

func addrPort(t *testing.T, raw string) (netip.Addr, int) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	ap := netip.MustParseAddrPort(u.Host)
	return ap.Addr(), int(ap.Port())
}

func TestTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	ap := netip.MustParseAddrPort(ln.Addr().String())
	p := &Prober{}
	o := p.Run(context.Background(), Request{Check: domain.Check{Type: domain.CheckTCP}, Addr: ap.Addr(), Port: int(ap.Port()), Timeout: time.Second})
	if o.State != domain.StateUp || o.LatencyMs == nil {
		t.Fatalf("tcp abierto: %+v", o)
	}
	_ = ln.Close()
	o = p.Run(context.Background(), Request{Check: domain.Check{Type: domain.CheckTCP}, Addr: ap.Addr(), Port: int(ap.Port()), Timeout: time.Second})
	if o.State != domain.StateDown || o.Reason != domain.ReasonRefused {
		t.Fatalf("tcp cerrado: %+v", o)
	}
}

func TestHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte("ok"))
		case "/slow":
			time.Sleep(300 * time.Millisecond)
		case "/moved":
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	a, port := addrPort(t, srv.URL)
	p := &Prober{}
	run := func(c domain.Check, timeout time.Duration) domain.CheckObservation {
		return p.Run(context.Background(), Request{Check: c, Addr: a, Port: port, Timeout: timeout})
	}
	if o := run(domain.Check{Type: domain.CheckHTTP, Path: ptr("/health")}, time.Second); o.State != domain.StateUp || o.Detail != "HTTP 200" {
		t.Fatalf("200: %+v", o)
	}
	if o := run(domain.Check{Type: domain.CheckHTTP, Path: ptr("/down")}, time.Second); o.State != domain.StateDown || o.Reason != domain.ReasonHTTPStatus {
		t.Fatalf("503: %+v", o)
	}
	// 503 aceptado por rango propio.
	if o := run(domain.Check{Type: domain.CheckHTTP, Path: ptr("/down"), ExpectedStatusMin: ptr(500), ExpectedStatusMax: ptr(503)}, time.Second); o.State != domain.StateUp {
		t.Fatalf("rango 500-503: %+v", o)
	}
	// Sin seguir redirecciones: 302 está en 200–399.
	if o := run(domain.Check{Type: domain.CheckHTTP, Path: ptr("/moved")}, time.Second); o.State != domain.StateUp || o.Detail != "HTTP 302" {
		t.Fatalf("302: %+v", o)
	}
	if o := run(domain.Check{Type: domain.CheckHTTP, Path: ptr("/slow")}, 100*time.Millisecond); o.State != domain.StateDown || o.Reason != domain.ReasonTimeout {
		t.Fatalf("timeout: %+v", o)
	}
}

func TestHTTPS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	a, port := addrPort(t, srv.URL)
	p := &Prober{}
	c := domain.Check{Type: domain.CheckHTTP, HTTPS: ptr(true)}
	if o := p.Run(context.Background(), Request{Check: c, Addr: a, Port: port, Timeout: time.Second}); o.State != domain.StateDown || o.Reason != domain.ReasonTLSError {
		t.Fatalf("certificado no confiable con tls_verify: %+v", o)
	}
	c.TLSVerify = ptr(false)
	if o := p.Run(context.Background(), Request{Check: c, Addr: a, Port: port, Timeout: time.Second}); o.State != domain.StateUp || o.Detail != "HTTP "+strconv.Itoa(http.StatusNoContent) {
		t.Fatalf("tls_verify=false: %+v", o)
	}
}

type fakePinger struct {
	res icmp.Result
	err error
}

func (f fakePinger) Ping(context.Context, netip.Addr, int, time.Duration) (icmp.Result, error) {
	return f.res, f.err
}

func TestICMP(t *testing.T) {
	c := domain.Check{Type: domain.CheckICMP}
	v4 := netip.MustParseAddr("192.0.2.20")
	if o := (&Prober{}).Run(context.Background(), Request{Check: c, Addr: v4}); o.State != domain.StateUnknown || o.Reason != domain.ReasonICMPOff {
		t.Fatalf("sin socket: %+v", o)
	}
	rtt := 12 * time.Millisecond
	p := &Prober{Pinger: fakePinger{res: icmp.Result{Sent: 3, Received: 2, AvgRTT: &rtt}}}
	if o := p.Run(context.Background(), Request{Check: c, Addr: v4}); o.State != domain.StateUp || *o.LatencyMs != 12 || o.Detail != "2/3 respuestas" {
		t.Fatalf("2/3: %+v", o)
	}
	p = &Prober{Pinger: fakePinger{res: icmp.Result{Sent: 3}}}
	if o := p.Run(context.Background(), Request{Check: c, Addr: v4}); o.State != domain.StateDown || o.Reason != domain.ReasonTimeout {
		t.Fatalf("0/3: %+v", o)
	}
	p = &Prober{Pinger: fakePinger{err: errors.New("no route")}}
	if o := p.Run(context.Background(), Request{Check: c, Addr: v4}); o.State != domain.StateDown || o.Reason != domain.ReasonUnreachable {
		t.Fatalf("error: %+v", o)
	}
	if o := p.Run(context.Background(), Request{Check: c, Addr: netip.MustParseAddr("2001:db8::1")}); o.State != domain.StateUnknown {
		t.Fatalf("ipv6: %+v", o)
	}
}
