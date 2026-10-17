package routeros

// Fixtures REALES grabados con chrlab_test.go contra el CHR del laboratorio (infrastructure/lab/chr)
// por el túnel WireGuard con el usuario de solo lectura `horus`: RouterOS 7.12 (mínima, D15) y
// 7.23.8 (última long-term al grabar). Datos del laboratorio (RFC 5737/3849/2544), sin secretos.

import (
	"context"
	"math"
	"testing"

	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

func TestReadFactsRealCHR(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		dir, version string
		dhcp         domain.DHCPv6Server
		linkReuse    bool
	}{
		// 7.12 no tiene `prefix-pool`: su `address-pool` es el pool de prefijos delegados.
		{"chr-7.12", "7.12", domain.DHCPv6Server{Name: "dhcp6-lan", Interface: "ether3-lan", PrefixPool: "v6-pd"}, false},
		// 7.23.8: `prefix-pool` (PD) y `address-pool` (IA_NA) separados.
		{"chr-7.23.8", "7.23.8", domain.DHCPv6Server{Name: "dhcp6-lan", Interface: "ether3-lan", PrefixPool: "v6-pd", AddressPool: "v6-na"}, true},
	} {
		t.Run(tc.version, func(t *testing.T) {
			t.Parallel()
			f, err := client(serve(t, tc.dir), "").ReadFacts(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if f.Version != tc.version {
				t.Fatalf("versión %q", f.Version)
			}
			if len(f.IPPools) != 2 || f.IPPools[0].Ranges != "10.20.0.100-10.20.0.200,10.20.1.0/24" {
				t.Fatalf("pools IPv4: %+v", f.IPPools)
			}
			if len(f.IPv6Pools) < 2 || f.IPv6Pools[0].Name != "v6-pd" || f.IPv6Pools[0].PrefixLength != 56 {
				t.Fatalf("pools IPv6: %+v", f.IPv6Pools)
			}
			var plan *domain.PPPProfile
			for i := range f.PPPProfiles {
				if f.PPPProfiles[i].Name == "plan-50M" {
					plan = &f.PPPProfiles[i]
				}
			}
			if plan == nil || plan.PDPool != "v6-pd" || plan.LinkPool != "v6-enlace" || plan.LinkReuse != tc.linkReuse {
				t.Fatalf("perfil PPP: %+v", f.PPPProfiles)
			}
			if len(f.DHCPv6Servers) != 1 || f.DHCPv6Servers[0] != tc.dhcp {
				t.Fatalf("servidor DHCPv6: %+v", f.DHCPv6Servers)
			}
			if len(f.Addresses) < 5 {
				t.Fatalf("direcciones: %+v", f.Addresses)
			}
		})
	}
}

func TestReadNetwatchRealCHR(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		dir     string
		gwLoss  float64
		gwSent  int
		gwRecv  int
		gwAvg   float64
		gwJit   float64
		gwStdev float64
	}{
		{"chr-7.12", 0, 10, 10, 1.079, 0.891, 0.306},
		// Con pérdida parcial inducida en el laboratorio: "loss-percent":"20", sigue "up".
		{"chr-7.23.8", 20, 10, 8, 2.314, 3.764, 1.004},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			t.Parallel()
			fs := serve(t, tc.dir)
			rd := Reader{BaseURL: func(string) string { return fs.URL }}
			probes, err := rd.ReadNetwatch(context.Background(), app.ReadTarget{TunnelIP: "10.255.3.17", User: "horus", Password: "clave-ro", Pinned: fs.fingerprint()})
			if err != nil || len(probes) != 4 {
				t.Fatalf("%v %+v", err, probes)
			}
			gw := probes[0]
			if gw.Comment != "horus-uplink;if=ether2-wan;ref=gw" || gw.Type != "icmp" || gw.Status != "up" || gw.Disabled ||
				*gw.SentCount != tc.gwSent || *gw.ResponseCount != tc.gwRecv || *gw.LossPercent != tc.gwLoss ||
				math.Abs(*gw.RTTAvgMs-tc.gwAvg) > 1e-9 || math.Abs(*gw.RTTJitterMs-tc.gwJit) > 1e-9 || math.Abs(*gw.RTTStdevMs-tc.gwStdev) > 1e-9 ||
				gw.RTTMinMs == nil || gw.RTTMaxMs == nil || *gw.RTTMinMs > *gw.RTTAvgMs || *gw.RTTMaxMs < *gw.RTTAvgMs {
				t.Fatalf("puerta de enlace: %+v", gw)
			}
			// Destino que no responde: "down", 100 % de pérdida y sin RTT (RouterOS no publica rtt-*).
			down := probes[1]
			if down.Status != "down" || *down.LossPercent != 100 || *down.ResponseCount != 0 || down.RTTAvgMs != nil || down.RTTJitterMs != nil {
				t.Fatalf("caída: %+v", down)
			}
			// Sonda deshabilitada: "disabled":"true" (solo aparece cuando es true), "unknown", sin contadores.
			off := probes[3]
			if !off.Disabled || off.Status != "unknown" || off.SentCount != nil || off.LossPercent != nil {
				t.Fatalf("deshabilitada: %+v", off)
			}
			if fs.writes.Load() != 0 {
				t.Fatal("escrituras hacia el router")
			}
		})
	}
}
