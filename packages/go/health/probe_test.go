package health_test

import (
	"context"
	"net"
	"testing"

	"github.com/hcdestroyer/horus-flow/packages/go/health"
)

func TestDialProbe(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := health.DialProbe("tcp", addr)(context.Background()); err != nil {
		t.Fatalf("probe contra puerto abierto: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if err := health.DialProbe("tcp", addr)(context.Background()); err == nil {
		t.Fatal("probe contra puerto cerrado debería fallar")
	}
}
