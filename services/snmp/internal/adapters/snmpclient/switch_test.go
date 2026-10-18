package snmpclient

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Switches con fixtures SINTÉTICOS (cabecera de cada fichero): Huawei serie S (S5735, VRP V200R021) con
// HUAWEI-ENTITY-EXTENT-MIB y LLDP-MIB, y SwOS (CSS326) con MIB limitadas. Cuando haya capturas reales se
// añaden aquí con los mismos asertos.

func TestHuaweiS5735Synthetic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := loadWalk(t, "huawei-s5735-v200-synthetic.snmpwalk")
	a := ForDevice("huawei", PlatformVRP)
	sys, err := a.ReadSystem(ctx, f)
	if err != nil || sys.Model == nil || *sys.Model != "S5735-L48T4X-A1" {
		t.Fatalf("modelo de la serie S (primera línea de sysDescr): %+v %v", sys, err)
	}
	res, err := a.ReadResources(ctx, f)
	if err != nil || len(res.CPULoads) != 1 || res.CPULoads[0] != 12 || res.MemoryTotalBytes == nil || *res.MemoryTotalBytes != 1024<<20 ||
		*res.MemoryUsedBytes != 1024<<20*45/100 {
		t.Fatalf("CPU/memoria de la MPU (única tarjeta con CPU): %+v %v", res, err)
	}
	h, err := a.ReadHealth(ctx, f)
	if err != nil || h.TemperatureCelsius == nil || *h.TemperatureCelsius != 41 || h.Firmware == nil || *h.Firmware != "V200R021C10SPC600" ||
		h.SerialNumber == nil || *h.SerialNumber != "2102SYNTHS5735" {
		t.Fatalf("salud: %+v %v", h, err)
	}
	disc, err := Discover(ctx, f)
	if err != nil || len(disc) != 11 {
		t.Fatalf("descubrimiento: %d %v", len(disc), err)
	}
	nb, err := ReadNeighbors(ctx, f, disc)
	if err != nil || len(nb) != 3 {
		t.Fatalf("vecinos LLDP: %+v %v", nb, err)
	}
	byPort := map[int]Neighbor{}
	for _, n := range nb {
		byPort[n.LocalIfIndex] = n
	}
	up := byPort[54]
	if up.SysName != "rt-core-1" || up.PortID != "sfp-sfpplus1" || up.ChassisID != "48:8f:5a:11:22:33" || up.MgmtAddress != "10.255.3.17" ||
		up.LocalPort != "XGigabitEthernet0/0/1" || up.Source != "lldp" {
		t.Fatalf("vecino del uplink: %+v", up)
	}
	// PortID de subtipo macAddress(3) en bruto → hexadecimal.
	if ap := byPort[6]; ap.SysName != "ap-torre-norte" || ap.PortID != "dc:2c:6e:44:55:66" || ap.PortDesc != "eth0" {
		t.Fatalf("vecino con PortID MAC: %+v", ap)
	}
	if lag := byPort[60]; lag.SysName != "sw-acceso-2.isp.example" || lag.MgmtAddress != "10.20.0.6" {
		t.Fatalf("vecino del Eth-Trunk (puerto local por lldpLocPortNum = ifIndex): %+v", lag)
	}
	ctrs, err := ReadCounters(ctx, f, []int{9, 54}, time.Now(), *sys.UptimeTicks)
	if err != nil || len(ctrs) != 2 || ctrs[0].InErrors != 17 || ctrs[0].InDiscards != 4 || ctrs[1].InOctets != 9876543210987 {
		t.Fatalf("contadores: %+v %v", ctrs, err)
	}
}

func TestSwOSSynthetic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := loadWalk(t, "swos-css326-synthetic.snmpwalk")
	a := ForDevice("mikrotik", PlatformSwOS)
	sys, err := a.ReadSystem(ctx, f)
	if err != nil || sys.Model == nil || *sys.Model != "CSS326-24G-2S+" {
		t.Fatalf("modelo SwOS: %+v %v", sys, err)
	}
	// Sin HOST-RESOURCES-MIB: CPU y memoria null, sin error (degradación).
	if res, err := a.ReadResources(ctx, f); err != nil || res.CPULoads != nil || res.MemoryTotalBytes != nil {
		t.Fatalf("recursos: %+v %v", res, err)
	}
	h, err := a.ReadHealth(ctx, f)
	if err != nil || h.TemperatureCelsius == nil || *h.TemperatureCelsius != 39.5 || h.Voltage == nil || *h.Voltage != 24 {
		t.Fatalf("salud MIKROTIK-MIB: %+v %v", h, err)
	}
	disc, err := Discover(ctx, f)
	if err != nil || len(disc) != 26 {
		t.Fatalf("descubrimiento: %d %v", len(disc), err)
	}
	// Sin LLDP ni tabla de vecinos: lista vacía, sin error.
	if nb, err := ReadNeighbors(ctx, f, disc); err != nil || len(nb) != 0 {
		t.Fatalf("vecinos SwOS: %+v %v", nb, err)
	}
	if ForDevice("mikrotik", PlatformRouterOS).Vendor() != VendorMikroTik || ForDevice("huawei", "").Vendor() != VendorHuawei {
		t.Fatal("ForDevice")
	}
}

// Tabla de vecinos de MIKROTIK-MIB (respaldo cuando no hay LLDP-MIB): identidad, IP y el ifIndex local.
func TestMtxrNeighborFallback(t *testing.T) {
	t.Parallel()
	f := &fake{vars: map[string]any{
		OIDMtxrNeighborTable + ".2.1": "10.255.3.17",
		OIDMtxrNeighborTable + ".3.1": "\x48\x8f\x5a\x11\x22\x33",
		OIDMtxrNeighborTable + ".5.1": "MikroTik",
		OIDMtxrNeighborTable + ".6.1": "rt-core-1",
		OIDMtxrNeighborTable + ".8.1": int64(2),
		OIDMtxrNeighborTable + ".2.2": "0.0.0.0",
		OIDMtxrNeighborTable + ".6.2": "huerfano",
		OIDMtxrNeighborTable + ".8.2": int64(99),
	}}
	nb, err := ReadNeighbors(context.Background(), f, []Discovered{{IfIndex: 2, Name: "sfp-sfpplus1"}})
	if err != nil || len(nb) != 2 {
		t.Fatalf("vecinos: %+v %v", nb, err)
	}
	if nb[1].SysName != "rt-core-1" || nb[1].LocalIfIndex != 2 || nb[1].LocalPort != "sfp-sfpplus1" || nb[1].MgmtAddress != "10.255.3.17" ||
		nb[1].ChassisID != "48:8f:5a:11:22:33" || nb[1].Source != "mndp" {
		t.Fatalf("vecino MNDP: %+v", nb[1])
	}
	if nb[0].LocalIfIndex != 0 || nb[0].MgmtAddress != "" {
		t.Fatalf("ifIndex desconocido e IP 0.0.0.0: %+v", nb[0])
	}
}

// Fixture REAL de RouterOS 7.23.8 (CHR del laboratorio) consultado como switch en modo directo con la
// configuración que genera Horus: RouterOS SÍ publica LLDP-MIB (lldpRemChassisId como texto "48:8F:…", no en
// bruto) y la misma vecindad en mtxrNeighborTable.
func TestRouterOSLLDPRealFixture(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := loadWalk(t, "chr-7.23.8-switch-lldp.snmpwalk")
	disc, err := Discover(ctx, f)
	if err != nil || len(disc) != 4 {
		t.Fatalf("descubrimiento: %+v %v", disc, err)
	}
	nb, err := ReadNeighbors(ctx, f, disc)
	if err != nil || len(nb) != 1 {
		t.Fatalf("vecinos: %+v %v", nb, err)
	}
	n := nb[0]
	if n.Source != "lldp" || n.SysName != "rt-core-1" || n.PortID != "sfp-sfpplus1" || n.PortDesc != "sfp-sfpplus1 uplink" ||
		n.ChassisID != "48:8F:5A:11:22:33" || n.MgmtAddress != "10.255.3.17" || n.LocalIfIndex != 3 || n.LocalPort != "ether2" {
		t.Fatalf("vecino LLDP real: %+v", n)
	}
	// Sin filas LLDP, el respaldo de MIKROTIK-MIB da la misma vecindad (identidad, IP, ifIndex local).
	for k := range f.vars {
		if strings.HasPrefix(k, OIDLldpRemTable) || strings.HasPrefix(k, OIDLldpRemManAddrTab) {
			delete(f.vars, k)
		}
	}
	nb, err = ReadNeighbors(ctx, f, disc)
	if err != nil || len(nb) != 1 || nb[0].Source != "mndp" || nb[0].SysName != "rt-core-1" || nb[0].LocalIfIndex != 3 ||
		nb[0].MgmtAddress != "10.255.3.17" || nb[0].ChassisID != "48:8f:5a:11:22:33" {
		t.Fatalf("respaldo mtxrNeighbor real: %+v %v", nb, err)
	}
}
