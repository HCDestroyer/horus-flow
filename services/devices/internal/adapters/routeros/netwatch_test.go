package routeros

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
)

// Las sondas netwatch se leen solo con GET y con la huella fijada; sin
// huella no se abre conexión (en segundo plano nunca hay TOFU).
func TestReadNetwatchFixture(t *testing.T) {
	fs := serve(t, "7.20")
	ip := fs.Listener.Addr().String()
	rd := Reader{BaseURL: func(string) string { return fs.URL }}
	if _, err := rd.ReadNetwatch(context.Background(), app.ReadTarget{TunnelIP: ip, User: "horus", Password: "clave-ro"}); !errors.Is(err, app.ErrRouterFingerprint) {
		t.Fatalf("sin huella: %v", err)
	}
	if fs.gets.Load() != 0 {
		t.Fatal("conexión sin huella fijada")
	}
	probes, err := rd.ReadNetwatch(context.Background(), app.ReadTarget{TunnelIP: ip, User: "horus", Password: "clave-ro", Pinned: fs.fingerprint()})
	if err != nil || len(probes) != 3 {
		t.Fatalf("lectura: %v %+v", err, probes)
	}
	gw := probes[0]
	if gw.Host != "198.51.100.1" || gw.Type != "icmp" || gw.Status != "up" || *gw.SentCount != 10 || *gw.LossPercent != 10 ||
		math.Abs(*gw.RTTAvgMs-4.21) > 1e-9 || *gw.RTTMaxMs != 6 || math.Abs(*gw.RTTJitterMs-1.1) > 1e-9 || math.Abs(*gw.RTTStdevMs-0.62) > 1e-9 {
		t.Fatalf("puerta de enlace: %+v", gw)
	}
	intl := probes[1]
	if *intl.LossPercent != 0 || math.Abs(*intl.RTTAvgMs-52) > 1e-9 || intl.RTTMinMs != nil {
		t.Fatalf("referencia: %+v", intl)
	}
	if !probes[2].Disabled || probes[2].Type != "simple" || probes[2].LossPercent != nil {
		t.Fatalf("ajena: %+v", probes[2])
	}
	if fs.writes.Load() != 0 {
		t.Fatal("escrituras hacia el router")
	}
	if _, err := rd.ReadNetwatch(context.Background(), app.ReadTarget{TunnelIP: ip, User: "horus", Password: "otra", Pinned: fs.fingerprint()}); !errors.Is(err, app.ErrRouterAuth) {
		t.Fatalf("usuario rechazado: %v", err)
	}
	if _, err := rd.ReadNetwatch(context.Background(), app.ReadTarget{TunnelIP: ip, User: "horus", Password: "clave-ro", Pinned: "AA:BB"}); !errors.Is(err, app.ErrRouterFingerprint) {
		t.Fatalf("huella distinta: %v", err)
	}
}

func TestDurationMs(t *testing.T) {
	for in, want := range map[string]float64{"1ms487us": 1.487, "12ms": 12, "1s200ms": 1200, "350us": 0.35, "00:00:01.234": 1234, "1.5ms": 1.5, "2": 2000} {
		got := durMs(in)
		if got == nil || math.Abs(*got-want) > 1e-9 {
			t.Errorf("durMs(%q) = %v, quiero %v", in, got, want)
		}
	}
	for _, in := range []string{"", "abc", "5parsecs", "1:2"} {
		if durMs(in) != nil {
			t.Errorf("durMs(%q) debería ser nil", in)
		}
	}
}
