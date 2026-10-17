package snmpclient

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Fixtures REALES: salida de `snmpwalk -On` (SNMPv3 authPriv por el túnel WireGuard) contra el CHR
// del laboratorio (infrastructure/lab/chr) con RouterOS 7.12 y 7.23.8 (última long-term al grabar),
// de exactamente los OIDs que usa este adaptador. Solo se anonimizó mtxrLicSoftwareId.

var walkLine = regexp.MustCompile(`^(\.[0-9.]+) = (?:([A-Za-z0-9-]+): )?(.*)$`)

// loadWalk convierte la salida de snmpwalk en el mapa OID → valor que entrega el transporte real
// (convert): INTEGER → int64, Counter/Gauge/Timeticks → uint64, STRING/OID → string.
func loadWalk(t *testing.T, name string) *fake {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	vars := map[string]any{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		m := walkLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		oid, typ, val := m[1], m[2], m[3]
		switch typ {
		case "INTEGER":
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				t.Fatalf("%s: %v", sc.Text(), err)
			}
			vars[oid] = n
		case "Counter32", "Counter64", "Gauge32":
			n, err := strconv.ParseUint(val, 10, 64)
			if err != nil {
				t.Fatalf("%s: %v", sc.Text(), err)
			}
			vars[oid] = n
		case "Timeticks": // "(36000) 0:06:00.00"
			n, err := strconv.ParseUint(strings.Trim(strings.Fields(val)[0], "()"), 10, 64)
			if err != nil {
				t.Fatalf("%s: %v", sc.Text(), err)
			}
			vars[oid] = n
		case "STRING":
			vars[oid] = strings.Trim(val, `"`)
		case "OID":
			vars[oid] = val
		case "Hex-STRING": // octetos en bruto (InetAddress), como los entrega convert
			var b []byte
			for _, h := range strings.Fields(val) {
				n, err := strconv.ParseUint(h, 16, 8)
				if err != nil {
					t.Fatalf("%s: %v", sc.Text(), err)
				}
				b = append(b, byte(n))
			}
			vars[oid] = string(b)
		case "":
			vars[oid] = strings.Trim(val, `"`) // cadena vacía ("")
		default:
			t.Fatalf("tipo SNMP no previsto en el fixture: %s", sc.Text())
		}
	}
	return &fake{vars: vars}
}

func TestReadRealCHRFixtures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		file, version, sysDescr string
		ifaces                  int
	}{
		// 7.12: sysDescr = "RouterOS <modelo>"; sin "lo" en IF-MIB.
		{"chr-7.12.snmpwalk", "7.12", "RouterOS CHR", 4},
		// 7.23.8: sysDescr añade versión y canal, e IF-MIB publica "lo" (ifType 1) en el ifIndex 1,
		// así que los ifIndex de las ethernet cambian respecto a 7.12.
		{"chr-7.23.8.snmpwalk", "7.23.8", "RouterOS CHR 7.23.8 (long-term)", 5},
	} {
		t.Run(tc.version, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := loadWalk(t, tc.file)
			sys, err := ReadSystem(ctx, f)
			if err != nil || sys.SysDescr != tc.sysDescr || sys.Model == nil || *sys.Model != "CHR" || sys.UptimeTicks == nil || *sys.UptimeTicks == 0 {
				t.Fatalf("sistema: %+v %v", sys, err)
			}
			res, err := ReadResources(ctx, f)
			if err != nil || len(res.CPULoads) != 2 || res.MemoryTotalBytes == nil || res.MemoryUsedBytes == nil ||
				*res.MemoryTotalBytes < 256<<20 || *res.MemoryUsedBytes == 0 || *res.MemoryUsedBytes > *res.MemoryTotalBytes {
				t.Fatalf("recursos (2 vCPU, RAM en hrStorageRam × 1024): %+v %v", res, err)
			}
			// El CHR no tiene sensores: sin OIDs fijos de temperatura/voltaje, sin mtxrGaugeTable y
			// sin número de serie. Todo nil, sin error; la versión sí está en mtxrLicVersion.
			h, err := ReadHealth(ctx, f)
			if err != nil || h.TemperatureCelsius != nil || h.CPUTemperatureCelsius != nil || h.Voltage != nil ||
				h.SerialNumber != nil || len(h.Sensors) != 0 || h.Firmware == nil || *h.Firmware != tc.version {
				t.Fatalf("salud: %+v %v", h, err)
			}
			disc, err := Discover(ctx, f)
			if err != nil || len(disc) != tc.ifaces {
				t.Fatalf("descubrimiento: %+v %v", disc, err)
			}
			byName := map[string]Discovered{}
			for _, d := range disc {
				byName[d.Name] = d
			}
			wan, ok := byName["ether2-wan"]
			// virtio no informa velocidad: ifHighSpeed = 0 (velocidad desconocida, no un error).
			if !ok || wan.Type != 6 || wan.HighSpeedMbps != 0 || wan.Alias != "horus-lab wan nat" || wan.AdminUp == nil || !*wan.AdminUp ||
				wan.OperStatus == nil || *wan.OperStatus != 1 {
				t.Fatalf("ether2-wan: %+v", wan)
			}
			if wg := byName["wg-horus"]; wg.Type != 1 { // WireGuard = other(1): nunca se sondea como enlace
				t.Fatalf("wg-horus: %+v", wg)
			}
			ctrs, err := ReadCounters(ctx, f, []int{wan.IfIndex, 9999}, time.Now(), *sys.UptimeTicks)
			if err != nil || len(ctrs) != 2 || ctrs[0].InOctets == 0 || ctrs[0].OutOctets == 0 || ctrs[0].InUcast == 0 ||
				ctrs[0].OperStatus == nil || *ctrs[0].OperStatus != 1 {
				t.Fatalf("contadores HC de la WAN: %+v %v", ctrs, err)
			}
			// ifIndex inexistente: RouterOS responde noSuchObject en cada varbind → ceros y sin estado.
			if ctrs[1].InOctets != 0 || ctrs[1].OperStatus != nil {
				t.Fatalf("ifIndex inexistente: %+v", ctrs[1])
			}
		})
	}
}
