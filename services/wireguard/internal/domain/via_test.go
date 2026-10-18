package domain

import (
	"errors"
	"net/netip"
	"testing"
)

func TestMapInto(t *testing.T) {
	t.Parallel()
	target, mapped := netip.MustParsePrefix("192.168.88.0/28"), netip.MustParsePrefix("10.255.1.16/28")
	got, ok := MapInto(target, mapped, netip.MustParseAddr("192.168.88.2"))
	if !ok || got != netip.MustParseAddr("10.255.1.18") {
		t.Fatalf("MapInto = %v %v", got, ok)
	}
	if _, ok := MapInto(target, mapped, netip.MustParseAddr("192.168.88.20")); ok {
		t.Fatal("una IP fuera de la red no se mapea")
	}
	if _, ok := MapInto(target, netip.MustParsePrefix("10.255.1.0/27"), netip.MustParseAddr("192.168.88.2")); ok {
		t.Fatal("bloques de distinto tamaño no se mapean")
	}
}

// Las direcciones Horus salen de la IPAM de túneles: un bloque alineado sin
// ninguna dirección usada, fuera de la red de servicios y de la red/broadcast
// del pool.
func TestNextFreeBlock(t *testing.T) {
	t.Parallel()
	pool, svc := netip.MustParsePrefix("10.255.0.0/16"), netip.MustParsePrefix("10.255.0.0/24")
	used := map[netip.Addr]bool{netip.MustParseAddr("10.255.1.1"): true}
	blk, err := NextFreeBlock(pool, svc, used, 28)
	if err != nil || blk != netip.MustParsePrefix("10.255.1.16/28") {
		t.Fatalf("bloque = %v, %v", blk, err)
	}
	one, err := NextFreeBlock(pool, svc, used, 32)
	if err != nil || one != netip.MustParsePrefix("10.255.1.0/32") {
		t.Fatalf("/32 = %v, %v", one, err)
	}
	small := netip.MustParsePrefix("10.200.0.0/29")
	if _, err := NextFreeBlock(small, svc, nil, 29); !errors.Is(err, ErrPoolExhausted) {
		t.Fatal("un bloque del tamaño del pool no cabe (red y broadcast)")
	}
	full := map[netip.Addr]bool{}
	for a := netip.MustParseAddr("10.200.0.1"); a.Less(netip.MustParseAddr("10.200.0.7")); a = a.Next() {
		full[a] = true
	}
	if _, err := NextFreeBlock(small, svc, full, 32); !errors.Is(err, ErrPoolExhausted) {
		t.Fatal("pool lleno")
	}
}
