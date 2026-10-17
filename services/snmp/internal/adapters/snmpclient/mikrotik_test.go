package snmpclient

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
)

// fake es un Transport sobre un mapa OID → valor (fixture de RouterOS 7).
type fake struct {
	vars map[string]any
	gets [][]string
	err  error
}

func (f *fake) Get(_ context.Context, oids []string) ([]Var, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.gets = append(f.gets, oids)
	out := make([]Var, 0, len(oids))
	for _, o := range oids {
		out = append(out, Var{OID: o, Value: f.vars[o]}) // ausente = nil (noSuchObject)
	}
	return out, nil
}

func (f *fake) Walk(_ context.Context, root string) ([]Var, error) {
	if f.err != nil {
		return nil, f.err
	}
	var keys []string
	for k := range f.vars {
		if strings.HasPrefix(k, root+".") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]Var, 0, len(keys))
	for _, k := range keys {
		out = append(out, Var{OID: k, Value: f.vars[k]})
	}
	return out, nil
}

func (f *fake) Close() error { return nil }

// routerOS7 es un CCR2116 con 16 núcleos, RAM, salud por tabla y tres
// interfaces (sfp uplink, bridge, pppoe dinámica).
func routerOS7() *fake {
	v := map[string]any{
		OIDSysDescr: "RouterOS CCR2116-12G-4S+", OIDSysUpTime: uint64(123456789),
		OIDMtxrHlTemperature: int64(470), OIDMtxrHlCPUTemp: int64(615), OIDMtxrLicVersion: "7.16.2", OIDMtxrSerialNumber: "HEX12345",
		// hrStorage: fila 65536 RAM, fila 131072 disco.
		OIDHrStorageTable + ".2.65536": OIDHrStorageRAM, OIDHrStorageTable + ".4.65536": uint64(1024), OIDHrStorageTable + ".5.65536": uint64(2097152), OIDHrStorageTable + ".6.65536": uint64(1258291),
		OIDHrStorageTable + ".2.131072": ".1.3.6.1.2.1.25.2.1.4", OIDHrStorageTable + ".4.131072": uint64(4096), OIDHrStorageTable + ".5.131072": uint64(32768), OIDHrStorageTable + ".6.131072": uint64(1000),
		// gauge table: temperatura (C), fan (rpm), voltaje (dV), psu (status)
		OIDMtxrGaugeTable + ".2.1": "temperature", OIDMtxrGaugeTable + ".3.1": int64(47), OIDMtxrGaugeTable + ".4.1": int64(1),
		OIDMtxrGaugeTable + ".2.2": "fan1-speed", OIDMtxrGaugeTable + ".3.2": int64(4200), OIDMtxrGaugeTable + ".4.2": int64(2),
		OIDMtxrGaugeTable + ".2.3": "voltage", OIDMtxrGaugeTable + ".3.3": int64(241), OIDMtxrGaugeTable + ".4.3": int64(3),
		OIDMtxrGaugeTable + ".2.4": "psu1-state", OIDMtxrGaugeTable + ".3.4": int64(1), OIDMtxrGaugeTable + ".4.4": int64(6),
		// interfaces
		OIDIfName + ".1": "ether1", OIDIfType + ".1": int64(6), OIDIfHighSpeed + ".1": uint64(1000), OIDIfAdminStatus + ".1": int64(1), OIDIfOperStatus + ".1": int64(2), OIDIfAlias + ".1": "",
		OIDIfName + ".7": "sfp-sfpplus1", OIDIfType + ".7": int64(6), OIDIfHighSpeed + ".7": uint64(10000), OIDIfAdminStatus + ".7": int64(1), OIDIfOperStatus + ".7": int64(1), OIDIfAlias + ".7": "UPLINK-TRANSIT-A",
		OIDIfName + ".20": "bridge", OIDIfType + ".20": int64(209), OIDIfHighSpeed + ".20": uint64(0), OIDIfAdminStatus + ".20": int64(1), OIDIfOperStatus + ".20": int64(1),
		OIDIfName + ".3001": "<pppoe-juan>", OIDIfType + ".3001": int64(23), OIDIfHighSpeed + ".3001": uint64(0), OIDIfAdminStatus + ".3001": int64(1), OIDIfOperStatus + ".3001": int64(1),
		// contadores ifIndex 7
		OIDIfHCInOctets + ".7": uint64(918273645512), OIDIfHCOutOctets + ".7": uint64(123456789012), OIDIfHCInUcast + ".7": uint64(100), OIDIfHCOutUcast + ".7": uint64(50),
		OIDIfInErrors + ".7": uint64(1), OIDIfOutErrors + ".7": uint64(0), OIDIfInDiscards + ".7": uint64(12), OIDIfOutDiscards + ".7": uint64(0),
	}
	for i := 1; i <= 16; i++ {
		load := int64(10)
		if i == 3 {
			load = 90 // Traffic Flow carga un núcleo
		}
		v[OIDHrProcessorLoad+"."+itoa(i)] = load
	}
	return &fake{vars: v}
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestReadRouterOS7(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := routerOS7()
	sys, err := ReadSystem(ctx, f)
	if err != nil || sys.Model == nil || *sys.Model != "CCR2116-12G-4S+" || sys.UptimeTicks == nil || *sys.UptimeTicks != 123456789 {
		t.Fatalf("sistema: %+v %v", sys, err)
	}
	res, err := ReadResources(ctx, f)
	if err != nil || len(res.CPULoads) != 16 || res.MemoryTotalBytes == nil || *res.MemoryTotalBytes != 2097152*1024 || *res.MemoryUsedBytes != 1258291*1024 {
		t.Fatalf("recursos: %+v %v", res, err)
	}
	h, err := ReadHealth(ctx, f)
	if err != nil || *h.TemperatureCelsius != 47 || *h.CPUTemperatureCelsius != 61.5 || *h.Firmware != "7.16.2" || *h.SerialNumber != "HEX12345" {
		t.Fatalf("salud: %+v %v", h, err)
	}
	if h.Voltage == nil || *h.Voltage != 24.1 {
		t.Fatalf("voltaje ausente en OID fijo: debe salir de la tabla: %v", h.Voltage)
	}
	if len(h.Sensors) != 4 || h.Sensors[1].Name != "fan1-speed" || *h.Sensors[1].Unit != "rpm" || h.Sensors[2].Value != 24.1 || *h.Sensors[3].Unit != "status" {
		t.Fatalf("sensores: %+v", h.Sensors)
	}
	disc, err := Discover(ctx, f)
	if err != nil || len(disc) != 4 {
		t.Fatalf("descubrimiento: %+v %v", disc, err)
	}
	if disc[1].IfIndex != 7 || disc[1].Alias != "UPLINK-TRANSIT-A" || disc[1].HighSpeedMbps != 10000 || *disc[1].OperStatus != 1 || !*disc[1].AdminUp {
		t.Fatalf("sfp: %+v", disc[1])
	}
	if disc[3].Name != "<pppoe-juan>" || disc[3].Type != 23 {
		t.Fatalf("pppoe: %+v", disc[3])
	}
	at := time.Now()
	ctrs, err := ReadCounters(ctx, f, []int{7, 20}, at, 123456789)
	if err != nil || len(ctrs) != 2 || ctrs[0].InOctets != 918273645512 || ctrs[0].InDiscards != 12 || *ctrs[0].OperStatus != 1 || ctrs[0].Uptime != 123456789 {
		t.Fatalf("contadores: %+v %v", ctrs, err)
	}
	if ctrs[1].OperStatus == nil || *ctrs[1].OperStatus != 1 || ctrs[1].InOctets != 0 {
		t.Fatalf("ifIndex sin contadores HC = ceros (no error) con su oper_status: %+v", ctrs[1])
	}
	// Nunca un walk de ifTable para contadores: solo GET de los ifIndex pedidos.
	if len(f.gets) == 0 || len(f.gets[len(f.gets)-1]) != 2*countersPerInterface {
		t.Fatalf("GET de contadores: %v", f.gets)
	}
}

func TestMissingOIDsAreNull(t *testing.T) {
	t.Parallel()
	f := &fake{vars: map[string]any{OIDSysDescr: "RouterOS hAP ac^2", OIDSysUpTime: uint64(100)}}
	h, err := ReadHealth(context.Background(), f)
	if err != nil || h.TemperatureCelsius != nil || h.Voltage != nil || h.Firmware != nil || len(h.Sensors) != 0 {
		t.Fatalf("modelo sin sensores: %+v %v", h, err)
	}
	res, err := ReadResources(context.Background(), f)
	if err != nil || res.MemoryTotalBytes != nil || len(res.CPULoads) != 0 {
		t.Fatalf("sin hr tables: %+v %v", res, err)
	}
	if ParseModel("RouterOS hAP ac^2") != "hAP" || ParseModel("") != "" {
		t.Fatal("ParseModel")
	}
}

func TestClassifyAuth(t *testing.T) {
	t.Parallel()
	// El último es el error real de gosnmp con RouterOS 7.12/7.23.8 y la clave de autenticación mal.
	for _, e := range []error{gosnmp.ErrUnknownUsername, gosnmp.ErrWrongDigest, gosnmp.ErrDecryption,
		errors.New("incoming packet is not authentic, discarding")} {
		if !IsAuth(classify(e)) {
			t.Errorf("%v debe ser ErrAuth", e)
		}
	}
	if IsAuth(classify(errors.New("request timeout"))) {
		t.Error("timeout no es auth")
	}
	if _, err := authProtocol("sha256"); err != nil {
		t.Error(err)
	}
	if _, err := privProtocol("rc4"); err == nil {
		t.Error("priv desconocido debe fallar")
	}
	v := convert(gosnmp.SnmpPDU{Name: ".1", Type: gosnmp.NoSuchObject})
	if v.Value != nil {
		t.Error("noSuchObject = nil")
	}
	v = convert(gosnmp.SnmpPDU{Name: ".1", Type: gosnmp.Counter64, Value: uint64(5)})
	if v.Value.(uint64) != 5 {
		t.Error("Counter64")
	}
}
