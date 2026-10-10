package app

import (
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestSetReadBuffer: se concede lo pedido hasta net.core.rmem_max (o más con
// CAP_NET_ADMIN).
func TestSetReadBuffer(t *testing.T) {
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	got := setReadBuffer(c, 1<<20)
	if got <= 0 {
		t.Skip("SO_RCVBUF not readable on this platform")
	}
	limit := 1 << 20
	if b, err := os.ReadFile("/proc/sys/net/core/rmem_max"); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && v < limit {
			limit = v
		}
	}
	if got < limit/2 {
		t.Fatalf("granted %d bytes, want ≈ %d", got, limit)
	}
}
