//go:build acceptance

// Command pcapreplay reenvía por UDP los datagramas de una captura (pcap,
// pcapng o hfsim) con su ritmo original, desde la IP de origen indicada
// (la IP de túnel del router simulado). Lo usa la aceptación de I1 para
// pasar la captura real anonimizada de MikroTik (tests/fixtures/mikrotik-real)
// por el túnel WireGuard y el colector reales:
//
//	pcapreplay -in ipfix-nat-20s.pcapng -target 10.241.0.1:4739 -src 10.241.0.8
package main

import (
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/pcapread"
)

func main() {
	in := flag.String("in", "", "captura (pcap, pcapng, hfsim; .gz admitido)")
	target := flag.String("target", "", "colector host:puerto")
	src := flag.String("src", "", "IP de origen (vacío = la que elija el sistema)")
	speed := flag.Float64("speed", 1, "velocidad respecto al ritmo original (0 = sin pausas)")
	flag.Parse()
	if err := run(*in, *target, *src, *speed); err != nil {
		fmt.Fprintln(os.Stderr, "pcapreplay:", err)
		os.Exit(1)
	}
}

func run(in, target, src string, speed float64) error {
	if in == "" || target == "" {
		return fmt.Errorf("faltan -in y -target")
	}
	ds, err := pcapread.ReadFile(in)
	if err != nil {
		return err
	}
	dst, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return err
	}
	var laddr *net.UDPAddr
	if src != "" {
		a, err := netip.ParseAddr(src)
		if err != nil {
			return fmt.Errorf("-src: %w", err)
		}
		laddr = net.UDPAddrFromAddrPort(netip.AddrPortFrom(a, 0))
	}
	conn, err := net.DialUDP("udp", laddr, dst)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	start := time.Now()
	var bytes int
	for i, d := range ds {
		if speed > 0 && i > 0 {
			due := time.Duration(float64(d.Time.Sub(ds[0].Time)) / speed)
			if wait := due - time.Since(start); wait > 0 {
				time.Sleep(wait)
			}
		}
		if _, err := conn.Write(d.Payload); err != nil {
			return fmt.Errorf("datagrama %d: %w", i, err)
		}
		bytes += len(d.Payload)
	}
	fmt.Printf("pcapreplay: %d datagramas, %d bytes a %s en %s\n", len(ds), bytes, target, time.Since(start).Round(time.Millisecond))
	return nil
}
