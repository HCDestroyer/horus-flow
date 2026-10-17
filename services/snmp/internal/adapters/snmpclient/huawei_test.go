package snmpclient

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Fixtures SINTÉTICOS de Huawei VRP (testdata/huawei-*-synthetic.snmpwalk, generados a partir de la
// documentación oficial y las RFC; ver la cabecera de cada fichero y docs/vendors/huawei.md §9). Cuando haya
// una captura real basta con añadirla aquí con los mismos asertos.

func TestHuaweiNE40EV8Synthetic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := loadWalk(t, "huawei-ne40e-v8-synthetic.snmpwalk")
	a := ForVendor("huawei")
	if a.Vendor() != VendorHuawei {
		t.Fatalf("adaptador: %s", a.Vendor())
	}
	sys, err := a.ReadSystem(ctx, f)
	if err != nil || sys.Model == nil || *sys.Model != "NE40E-X8A" || sys.UptimeTicks == nil || *sys.UptimeTicks != 864000000 {
		t.Fatalf("sistema: %+v %v", sys, err)
	}
	res, err := a.ReadResources(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	// Una carga por tarjeta con CPU o memoria: MPU 9 (principal, primero), MPU 10, LPU 1, LPU 2; la SFU sin
	// CPU ni memoria, el chasis, las fuentes, ventiladores y puertos quedan fuera.
	if len(res.CPULoads) != 4 || res.CPULoads[0] != 23 || res.CPULoads[2] != 41 {
		t.Fatalf("CPU por tarjeta: %+v", res.CPULoads)
	}
	if res.MemoryTotalBytes == nil || *res.MemoryTotalBytes != 8192<<20 || res.MemoryUsedBytes == nil || *res.MemoryUsedBytes != 8192<<20*45/100 {
		t.Fatalf("memoria de la MPU principal (hwEntityMemSizeMega × hwEntityMemUsage): %+v", res)
	}
	h, err := a.ReadHealth(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if h.TemperatureCelsius == nil || *h.TemperatureCelsius != 42 || h.Voltage == nil || *h.Voltage != 12.03 || h.CPUTemperatureCelsius != nil {
		t.Fatalf("salud de la MPU: %+v", h)
	}
	if h.Firmware == nil || *h.Firmware != "V800R011C10SPC100" || h.SerialNumber == nil || *h.SerialNumber != "2102SYNTH00001" {
		t.Fatalf("versión (sysDescr) y serie (chasis): %+v", h)
	}
	// Sensores: temperatura de MPU 9, MPU 10, LPU 1, LPU 2, SFU 11, PWR 1 y voltaje de las dos MPU.
	if len(h.Sensors) != 8 || h.Sensors[0].Name != "MPU 9" || *h.Sensors[0].Unit != "C" || h.Sensors[1].Name != "MPU 9 voltage" {
		t.Fatalf("sensores: %+v", h.Sensors)
	}
	disc, err := Discover(ctx, f)
	if err != nil || len(disc) != 12 {
		t.Fatalf("descubrimiento: %d %v", len(disc), err)
	}
	monitored := map[string]bool{}
	for _, d := range disc {
		if a.Monitored(d.Type, d.Name) {
			monitored[d.Name] = true
		}
	}
	for _, want := range []string{"GigabitEthernet0/0/0", "GigabitEthernet1/0/0", "GigabitEthernet1/0/1", "100GE2/0/0",
		"GigabitEthernet1/0/1.100", "Eth-Trunk1", "Eth-Trunk1.200"} {
		if !monitored[want] {
			t.Errorf("%s debe sondearse", want)
		}
	}
	for _, not := range []string{"NULL0", "InLoopBack0", "LoopBack0", "Virtual-Ethernet1/0/0", "Virtual-Template1:0"} {
		if monitored[not] {
			t.Errorf("%s no debe sondearse", not)
		}
	}
	ctrs, err := ReadCounters(ctx, f, []int{10, 20, 13}, time.Now(), *sys.UptimeTicks)
	if err != nil || len(ctrs) != 3 || ctrs[0].InOctets != 918273645512 || ctrs[0].InErrors != 3 || ctrs[0].InDiscards != 15 ||
		ctrs[1].OutOctets != 4440000000000 || ctrs[2].InUcast != 200000000 {
		t.Fatalf("contadores HC, errores y descartes: %+v %v", ctrs, err)
	}
	pings, err := ReadPingResults(ctx, f, "horus")
	if err != nil || len(pings) != 3 {
		t.Fatalf("DISMAN-PING-MIB: %+v %v", pings, err)
	}
	byTest := map[string]PingResult{}
	for _, p := range pings {
		byTest[p.Test] = p
		if p.Source != PingSourceDisman || p.Owner != "horus" {
			t.Errorf("origen: %+v", p)
		}
	}
	gw := byTest["gw-GigabitEthernet1_0_0"]
	if gw.Target != "203.0.113.1" || *gw.Sent != 10 || *gw.Responses != 10 || *gw.LossPct != 0 || *gw.AvgRTTMs != 2 || *gw.MaxRTTMs != 4 ||
		gw.StdDevMs == nil || *gw.StdDevMs != 1 { // sqrt(50/10 - 2²) = 1
		t.Fatalf("puerta de enlace: %+v", gw)
	}
	if down := byTest["gw-100GE2_0_0"]; *down.LossPct != 100 || down.StdDevMs != nil {
		t.Fatalf("enlace caído: %+v", down)
	}
	if intl := byTest["intl-1.1.1.1"]; intl.Target != "1.1.1.1" || *intl.LossPct != 10 {
		t.Fatalf("internacional: %+v", intl)
	}
}

func TestHuaweiAR651V5SyntheticNQAMIB(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := loadWalk(t, "huawei-ar651-v5-synthetic.snmpwalk")
	a := ForVendor("Huawei")
	sys, err := a.ReadSystem(ctx, f)
	if err != nil || sys.Model == nil || *sys.Model != "AR651" {
		t.Fatalf("sistema: %+v %v", sys, err)
	}
	// La placa no se llama MPU/SRU: se elige la de más memoria; memoria en bytes (hwEntityMemSize).
	res, err := a.ReadResources(ctx, f)
	if err != nil || len(res.CPULoads) != 1 || res.CPULoads[0] != 12 || res.MemoryTotalBytes == nil || *res.MemoryTotalBytes != 1<<30 ||
		*res.MemoryUsedBytes != (1<<30)*61/100 {
		t.Fatalf("recursos: %+v %v", res, err)
	}
	h, err := a.ReadHealth(ctx, f)
	if err != nil || h.TemperatureCelsius == nil || *h.TemperatureCelsius != 47 || h.Voltage != nil || *h.Firmware != "V300R019C10SPC300" ||
		*h.SerialNumber != "21SYNTHAR651X" {
		t.Fatalf("salud: %+v %v", h, err)
	}
	disc, err := Discover(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range disc {
		want := d.Name == "GigabitEthernet0/0/8" || d.Name == "GigabitEthernet0/0/9" || d.Name == "Vlanif10"
		if a.Monitored(d.Type, d.Name) != want {
			t.Errorf("%s (tipo %d): monitored debe ser %v", d.Name, d.Type, want)
		}
	}
	// Sin DISMAN-PING-MIB: respaldo NQA-MIB (solo pruebas ICMP; pérdida = nqaResultsLostPacketRatio).
	pings, err := ReadPingResults(ctx, f, "horus")
	if err != nil || len(pings) != 1 {
		t.Fatalf("NQA-MIB: %+v %v", pings, err)
	}
	p := pings[0]
	if p.Source != PingSourceNQA || p.Test != "gw-GigabitEthernet0_0_8" || p.Target != "192.0.2.1" || *p.Sent != 10 || *p.Responses != 8 ||
		*p.LossPct != 20 || *p.AvgRTTMs != 5 || *p.MinRTTMs != 3 || p.StdDevMs != nil {
		t.Fatalf("resultado NQA: %+v", p)
	}
}

func TestHuaweiWithoutVendorMIBs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := &fake{vars: map[string]any{OIDSysDescr: "Huawei Versatile Routing Platform Software VRP (R) software, Version 8.200 (NetEngine 8000 V800R022C00)",
		OIDSysUpTime: uint64(1)}}
	a := Huawei{}
	sys, err := a.ReadSystem(ctx, f)
	if err != nil || sys.Model == nil || *sys.Model != "NetEngine 8000" {
		t.Fatalf("modelo desde la línea de versión: %+v %v", sys, err)
	}
	res, err := a.ReadResources(ctx, f)
	if err != nil || len(res.CPULoads) != 0 || res.MemoryTotalBytes != nil {
		t.Fatalf("sin hwEntityStateTable todo nil: %+v %v", res, err)
	}
	h, err := a.ReadHealth(ctx, f)
	if err != nil || h.TemperatureCelsius != nil || len(h.Sensors) != 0 || h.SerialNumber != nil {
		t.Fatalf("salud vacía: %+v %v", h, err)
	}
	pings, err := ReadPingResults(ctx, f, "horus")
	if err != nil || len(pings) != 0 {
		t.Fatalf("sin pruebas: %+v %v", pings, err)
	}
	boom := &fake{err: errors.New("timeout")}
	if _, err := a.ReadResources(ctx, boom); err == nil {
		t.Fatal("error de transporte")
	}
	if _, err := ReadPingResults(ctx, boom, "horus"); err == nil {
		t.Fatal("error de transporte")
	}
	if ParseHuaweiModel("") != "" || ParseVRPVersion("RouterOS CHR") != "" {
		t.Fatal("cadenas vacías")
	}
}

func TestForVendorDefaultsToMikroTik(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"", "mikrotik", "otro"} {
		if ForVendor(v).Vendor() != VendorMikroTik {
			t.Errorf("%q", v)
		}
	}
	// El adaptador MikroTik es exactamente el comportamiento anterior (mismos fixtures del CHR).
	f := loadWalk(t, "chr-7.23.8.snmpwalk")
	ctx := context.Background()
	got, err := ForVendor("mikrotik").ReadResources(ctx, f)
	want, err2 := ReadResources(ctx, f)
	if err != nil || err2 != nil || len(got.CPULoads) != len(want.CPULoads) || *got.MemoryTotalBytes != *want.MemoryTotalBytes {
		t.Fatalf("%+v %+v", got, want)
	}
	if mk := (MikroTik{}); !mk.Monitored(6, "ether1") || mk.Monitored(23, "<pppoe-x>") {
		t.Fatal("monitored MikroTik")
	}
}

func TestAdminStringIndex(t *testing.T) {
	t.Parallel()
	if encodeAdminString("horus") != "5.104.111.114.117.115" {
		t.Fatal(encodeAdminString("horus"))
	}
	names, rest, ok := decodeAdminStrings("5.104.111.114.117.115.2.103.119.0.0", 2)
	if !ok || names[0] != "horus" || names[1] != "gw" || len(rest) != 2 {
		t.Fatalf("%v %v %v", names, rest, ok)
	}
	for _, bad := range []string{"", "x", "3.1.2", "40.1", "1.999"} {
		if _, _, ok := decodeAdminStrings(bad, 1); ok {
			t.Errorf("%q debe fallar", bad)
		}
	}
	if inetAddress("\x20\x01\x0d\xb8\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x01") != "2001:db8::1" || inetAddress("x") != "" ||
		inetAddress(" 192.0.2.7") != "192.0.2.7" || inetAddress(int64(1)) != "" {
		t.Fatal("InetAddress")
	}
}
