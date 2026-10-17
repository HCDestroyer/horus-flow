package snmpclient

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// OIDs de Huawei VRP (docs/vendors/huawei.md §3). HUAWEI-ENTITY-EXTENT-MIB
// (1.3.6.1.4.1.2011.5.25.31), hwEntityStateTable indexada por
// entPhysicalIndex de ENTITY-MIB: la CPU, la memoria y la temperatura se
// publican por entidad física (chasis, tarjetas MPU/LPU/SFU, puertos…), así
// que hay que elegir las entidades correctas con entPhysicalClass.
const (
	OIDHwEntityStateTable = ".1.3.6.1.4.1.2011.5.25.31.1.1.1.1"
	hwColumnStandby       = "3"  // hwEntityStandbyStatus: notSupported(1) hotStandby(2) coldStandby(3) providingService(4)
	hwColumnCPUUsage      = "5"  // hwEntityCpuUsage, Integer32 0..100 (%)
	hwColumnMemUsage      = "7"  // hwEntityMemUsage, Integer32 0..100 (%)
	hwColumnMemSize       = "9"  // hwEntityMemSize, Integer32 (bytes; desborda > 2 GiB)
	hwColumnTemperature   = "11" // hwEntityTemperature, Integer32 (°C)
	hwColumnVoltage       = "13" // hwEntityVoltage, Integer32 (mV)
	hwColumnMemSizeMega   = "19" // hwEntityMemSizeMega, Integer32 (MiB)

	hwStandbyHot    = 2
	hwStandbyCold   = 3
	entClassChassis = 3 // PhysicalClass chassis(3) (RFC 6933)
	entClassModule  = 9 // PhysicalClass module(9): tarjetas (MPU, LPU, SFU, SRU…)
	mebibyte        = 1 << 20
	milliPerUnit    = 1000.0
)

// Huawei es el adaptador de VRP (NE/AR/CX con VRP V5 y V8): IF-MIB estándar
// + ENTITY-MIB + HUAWEI-ENTITY-EXTENT-MIB. HOST-RESOURCES-MIB no se usa: VRP
// no la implementa de forma general (docs/vendors/huawei.md §3.1).
type Huawei struct{}

var _ Adapter = Huawei{}

// Vendor implementa Adapter.
func (Huawei) Vendor() string { return VendorHuawei }

// ReadSystem implementa Adapter (modelo desde sysDescr de VRP).
func (Huawei) ReadSystem(ctx context.Context, t Transport) (System, error) {
	return readSystem(ctx, t, ParseHuaweiModel)
}

var (
	// "VRP (R) software, Version 8.180 (NE40E V800R011C10SPC100)"
	vrpVersionRe = regexp.MustCompile(`Version\s+([0-9.]+)\s*\(([^()]+?)\s+(V[0-9]{3}R[0-9A-Z]+)\)`)
	// Tarjeta de control principal por nombre (entPhysicalName): MPU (NE40E,
	// NetEngine 8000), SRU/MCU (AR), RPU/SRP, "mainboard".
	hwMainBoardRe = regexp.MustCompile(`(?i)(^|[^a-z])(mpu|sru|mcu|rpu|srp|main ?board)([^a-z]|$)`)
)

// ParseHuaweiModel extrae el modelo de sysDescr de VRP: la línea "HUAWEI
// NE40E-X8A" (o "Huawei AR2240 Router") y, si no está, el producto entre
// paréntesis de la línea de versión ("(NE40E V800R011C10SPC100)").
func ParseHuaweiModel(sysDescr string) string {
	for _, line := range splitLines(sysDescr) {
		f := strings.Fields(line)
		if len(f) < 2 || !strings.EqualFold(f[0], "huawei") {
			continue
		}
		switch strings.ToLower(f[1]) {
		case "versatile", "technologies", "tech":
			continue
		}
		return f[1]
	}
	if m := vrpVersionRe.FindStringSubmatch(sysDescr); m != nil {
		return m[2]
	}
	return ""
}

// ParseVRPVersion devuelve la versión de software de VRP de sysDescr
// ("V800R011C10SPC100"; "" si no se reconoce).
func ParseVRPVersion(sysDescr string) string {
	if m := vrpVersionRe.FindStringSubmatch(sysDescr); m != nil {
		return m[3]
	}
	return ""
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, `\r\n`, "\n") // snmpwalk sin -Oa escapa CR/LF
	s = strings.ReplaceAll(s, `\n`, "\n")
	return strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' })
}

// hwEntity es una fila combinada de entPhysicalTable + hwEntityStateTable.
type hwEntity struct {
	index       int
	class       int
	name        string
	software    string
	serial      string
	standby     int64
	cpu         *int64
	memUsage    *int64
	memSize     *int64
	memSizeMega *int64
	temperature *int64
	voltage     *int64
}

func (e *hwEntity) memTotal() uint64 {
	switch {
	case e.memSizeMega != nil && *e.memSizeMega > 0:
		return uint64(*e.memSizeMega) * mebibyte //nolint:gosec // > 0
	case e.memSize != nil && *e.memSize > 0:
		return uint64(*e.memSize) //nolint:gosec // > 0
	}
	return 0
}

// hasCompute: la entidad tiene CPU o memoria propias (una tarjeta, no un
// puerto, un ventilador ni una fuente). VRP devuelve 0 en las demás.
func (e *hwEntity) hasCompute() bool {
	return (e.cpu != nil && *e.cpu > 0) || e.memTotal() > 0
}

// readEntities recorre las columnas indicadas de ENTITY-MIB y de
// hwEntityStateTable y las combina por entPhysicalIndex.
func readEntities(ctx context.Context, t Transport, entCols, hwCols []string) (map[int]*hwEntity, error) {
	out := map[int]*hwEntity{}
	get := func(idx string) *hwEntity {
		n, err := strconv.Atoi(idx)
		if err != nil {
			return nil
		}
		e := out[n]
		if e == nil {
			e = &hwEntity{index: n}
			out[n] = e
		}
		return e
	}
	for _, col := range entCols {
		root := OIDEntPhysicalTable + "." + col
		rows, err := t.Walk(ctx, root)
		if err != nil {
			return nil, err
		}
		for _, v := range rows {
			idx, ok := strings.CutPrefix(normOID(v.OID), root+".")
			if !ok {
				continue
			}
			e := get(idx)
			if e == nil {
				continue
			}
			s, _ := v.Value.(string)
			switch col {
			case entColumnClass:
				if n, ok := asInt(v.Value); ok {
					e.class = int(n)
				}
			case entColumnName:
				e.name = strings.TrimSpace(s)
			case entColumnSoftware:
				e.software = strings.TrimSpace(s)
			case entColumnSerial:
				e.serial = strings.TrimSpace(s)
			}
		}
	}
	for _, col := range hwCols {
		root := OIDHwEntityStateTable + "." + col
		rows, err := t.Walk(ctx, root)
		if err != nil {
			return nil, err
		}
		for _, v := range rows {
			idx, ok := strings.CutPrefix(normOID(v.OID), root+".")
			if !ok {
				continue
			}
			n, ok := asInt(v.Value)
			if !ok {
				continue
			}
			e := get(idx)
			if e == nil {
				continue
			}
			switch col {
			case hwColumnStandby:
				e.standby = n
			case hwColumnCPUUsage:
				e.cpu = &n
			case hwColumnMemUsage:
				e.memUsage = &n
			case hwColumnMemSize:
				e.memSize = &n
			case hwColumnMemSizeMega:
				e.memSizeMega = &n
			case hwColumnTemperature:
				e.temperature = &n
			case hwColumnVoltage:
				e.voltage = &n
			}
		}
	}
	return out, nil
}

// boards devuelve las tarjetas con CPU o memoria propias (clase module) en
// orden de entPhysicalIndex; si ninguna es module (equipos que publican la
// placa con otra clase), cualquier entidad con CPU o memoria.
func boards(ents map[int]*hwEntity) []*hwEntity {
	var mods, any []*hwEntity
	for _, e := range ents {
		if !e.hasCompute() {
			continue
		}
		any = append(any, e)
		if e.class == entClassModule {
			mods = append(mods, e)
		}
	}
	list := mods
	if len(list) == 0 {
		list = any
	}
	sort.Slice(list, func(i, j int) bool { return list[i].index < list[j].index })
	return list
}

// mainBoard elige la tarjeta de control principal: según la documentación
// de Huawei, la CPU del sistema es la de la MPU activa (hwEntityStandbyStatus
// distinto de hotStandby/coldStandby). Primero la tarjeta cuyo nombre la
// delata (MPU, SRU, MCU…) y que no esté de reserva; si no, la de más memoria
// que no esté de reserva; si todas lo están, la de más memoria.
func mainBoard(list []*hwEntity) *hwEntity {
	standby := func(e *hwEntity) bool { return e.standby == hwStandbyHot || e.standby == hwStandbyCold }
	for _, e := range list {
		if hwMainBoardRe.MatchString(e.name) && !standby(e) {
			return e
		}
	}
	var active []*hwEntity
	for _, e := range list {
		if !standby(e) {
			active = append(active, e)
		}
	}
	if len(active) > 0 {
		list = active
	}
	var best *hwEntity
	for _, e := range list {
		if best == nil || e.memTotal() > best.memTotal() {
			best = e
		}
	}
	return best
}

// ReadResources implementa Adapter: una carga de CPU por tarjeta con CPU
// (hwEntityCpuUsage; CPUPercent es la media y CPUMaxCorePercent la más
// cargada) y la memoria de la tarjeta de control principal
// (hwEntityMemSizeMega × hwEntityMemUsage). Sin hwEntityStateTable todo nil.
func (Huawei) ReadResources(ctx context.Context, t Transport) (Resources, error) {
	var r Resources
	ents, err := readEntities(ctx, t, []string{entColumnClass, entColumnName},
		[]string{hwColumnStandby, hwColumnCPUUsage, hwColumnMemUsage, hwColumnMemSize, hwColumnMemSizeMega})
	if err != nil {
		return r, err
	}
	list := boards(ents)
	main := mainBoard(list)
	if main != nil && main.cpu != nil {
		r.CPULoads = append(r.CPULoads, int(*main.cpu))
	}
	for _, e := range list {
		if e != main && e.cpu != nil && len(r.CPULoads) < maxCPURowsConsidered {
			r.CPULoads = append(r.CPULoads, int(*e.cpu))
		}
	}
	if main != nil && main.memUsage != nil {
		if total := main.memTotal(); total > 0 && *main.memUsage >= 0 && *main.memUsage <= 100 {
			used := total * uint64(*main.memUsage) / 100 //nolint:gosec // 0..100
			r.MemoryTotalBytes, r.MemoryUsedBytes = &total, &used
		}
	}
	return r, nil
}

// ReadHealth implementa Adapter: temperatura y voltaje de la tarjeta de
// control principal, un sensor por entidad con temperatura (y voltaje),
// versión de VRP (sysDescr; si no, entPhysicalSoftwareRev) y número de serie
// del chasis (si no, de la tarjeta principal).
func (Huawei) ReadHealth(ctx context.Context, t Transport) (Health, error) {
	var h Health
	vars, err := t.Get(ctx, []string{OIDSysDescr})
	if err != nil {
		return h, err
	}
	if d, ok := index(vars)[OIDSysDescr].(string); ok {
		if v := ParseVRPVersion(d); v != "" {
			h.Firmware = &v
		}
	}
	ents, err := readEntities(ctx, t, []string{entColumnClass, entColumnName, entColumnSoftware, entColumnSerial},
		[]string{hwColumnStandby, hwColumnCPUUsage, hwColumnMemSize, hwColumnMemSizeMega, hwColumnTemperature, hwColumnVoltage})
	if err != nil {
		return h, err
	}
	main := mainBoard(boards(ents))
	if main != nil {
		if main.temperature != nil && *main.temperature > 0 {
			h.TemperatureCelsius = f64(float64(*main.temperature))
		}
		if main.voltage != nil && *main.voltage > 0 {
			h.Voltage = f64(float64(*main.voltage) / milliPerUnit)
		}
		if h.Firmware == nil && main.software != "" {
			s := main.software
			h.Firmware = &s
		}
	}
	idx := make([]int, 0, len(ents))
	for i := range ents {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		e := ents[i]
		if e.class == entClassChassis && e.serial != "" && h.SerialNumber == nil {
			s := e.serial
			h.SerialNumber = &s
		}
		name := e.name
		if name == "" {
			name = "entity-" + strconv.Itoa(e.index)
		}
		if e.temperature != nil && *e.temperature > 0 {
			h.Sensors = append(h.Sensors, api.Sensor{Name: name, Value: float64(*e.temperature), Unit: str("C")})
		}
		if e.voltage != nil && *e.voltage > 0 {
			h.Sensors = append(h.Sensors, api.Sensor{Name: name + " voltage", Value: float64(*e.voltage) / milliPerUnit, Unit: str("V")})
		}
	}
	if h.SerialNumber == nil && main != nil && main.serial != "" {
		s := main.serial
		h.SerialNumber = &s
	}
	return h, nil
}

// Monitored implementa Adapter: puertos Ethernet (GigabitEthernet,
// XGigabitEthernet, 25GE, 100GE…), sus subinterfaces (GE0/1/0.100),
// Eth-Trunk y sus subinterfaces y Vlanif; nunca gestión (MEth), loopback,
// NULL, túneles, plantillas virtuales ni interfaces dinámicas por usuario
// (con ':' en el nombre, p. ej. Virtual-Template1:0).
func (Huawei) Monitored(ifType int, name string) bool {
	n := strings.TrimSpace(name)
	if n == "" || strings.Contains(n, ":") {
		return false
	}
	lower := strings.ToLower(n)
	for _, p := range []string{"null", "inloopback", "loopback", "meth", "virtual-", "tunnel", "dialer", "cellular", "aux",
		"console", "nve", "vbdif"} {
		if strings.HasPrefix(lower, p) {
			return false
		}
	}
	switch ifType {
	case domain.IfTypeEthernet, domain.IfTypeL2VLAN, domain.IfTypeL3IPVLAN, domain.IfTypeLAG, ifTypeGigabitEthernet,
		ifTypeFastEther, ifTypeFastEtherFX:
		return true
	case domain.IfTypePropVirtual:
		// VRP antiguos publican Eth-Trunk como propVirtual(53).
		return strings.HasPrefix(lower, "eth-trunk")
	}
	return false
}

// ifType IANA obsoletos que algunos VRP aún publican en puertos Ethernet.
const (
	ifTypeFastEther       = 62
	ifTypeFastEtherFX     = 69
	ifTypeGigabitEthernet = 117
)
