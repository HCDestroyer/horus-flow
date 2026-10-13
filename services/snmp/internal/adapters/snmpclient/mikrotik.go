package snmpclient

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// OIDs usados (docs/vendors/mikrotik.md §3.2).
const (
	OIDSysDescr        = ".1.3.6.1.2.1.1.1.0"
	OIDSysUpTime       = ".1.3.6.1.2.1.1.3.0"
	OIDHrProcessorLoad = ".1.3.6.1.2.1.25.3.3.1.2"
	OIDHrStorageTable  = ".1.3.6.1.2.1.25.2.3.1"
	OIDHrStorageRAM    = ".1.3.6.1.2.1.25.2.1.2"
	OIDIfName          = ".1.3.6.1.2.1.31.1.1.1.1"
	OIDIfAlias         = ".1.3.6.1.2.1.31.1.1.1.18"
	OIDIfHighSpeed     = ".1.3.6.1.2.1.31.1.1.1.15"
	OIDIfType          = ".1.3.6.1.2.1.2.2.1.3"
	OIDIfAdminStatus   = ".1.3.6.1.2.1.2.2.1.7"
	OIDIfOperStatus    = ".1.3.6.1.2.1.2.2.1.8"
	OIDIfHCInOctets    = ".1.3.6.1.2.1.31.1.1.1.6"
	OIDIfHCOutOctets   = ".1.3.6.1.2.1.31.1.1.1.10"
	OIDIfHCInUcast     = ".1.3.6.1.2.1.31.1.1.1.7"
	OIDIfHCOutUcast    = ".1.3.6.1.2.1.31.1.1.1.11"
	OIDIfInErrors      = ".1.3.6.1.2.1.2.2.1.14"
	OIDIfOutErrors     = ".1.3.6.1.2.1.2.2.1.20"
	OIDIfInDiscards    = ".1.3.6.1.2.1.2.2.1.13"
	OIDIfOutDiscards   = ".1.3.6.1.2.1.2.2.1.19"

	OIDMtxrHlTemperature  = ".1.3.6.1.4.1.14988.1.1.3.10.0"
	OIDMtxrHlCPUTemp      = ".1.3.6.1.4.1.14988.1.1.3.11.0"
	OIDMtxrHlVoltage      = ".1.3.6.1.4.1.14988.1.1.3.8.0"
	OIDMtxrGaugeTable     = ".1.3.6.1.4.1.14988.1.1.3.100.1"
	OIDMtxrLicVersion     = ".1.3.6.1.4.1.14988.1.1.4.4.0"
	OIDMtxrSerialNumber   = ".1.3.6.1.4.1.14988.1.1.7.3.0"
	mtxrGaugeColumnName   = "2"
	mtxrGaugeColumnValue  = "3"
	mtxrGaugeColumnUnit   = "4"
	hrStorageColumnType   = "2"
	hrStorageColumnUnits  = "4"
	hrStorageColumnSize   = "5"
	hrStorageColumnUsed   = "6"
	interfacesPerGet      = 6
	countersPerInterface  = 9
	maxCPURowsConsidered  = 256
	gaugeUnitCelsius      = 1
	gaugeUnitRPM          = 2
	gaugeUnitDeciVolt     = 3
	gaugeUnitDeciAmp      = 4
	gaugeUnitDeciWatt     = 5
	gaugeUnitStatus       = 6
	healthScaleTenths     = 10.0
	sysDescrRouterOSMarks = "RouterOS "
)

// System es el bloque de sistema (cada ciclo).
type System struct {
	SysDescr    string
	UptimeTicks *uint64 // centésimas de segundo
	Model       *string
}

// Resources es CPU y memoria (cada ciclo).
type Resources struct {
	CPULoads         []int
	MemoryUsedBytes  *uint64
	MemoryTotalBytes *uint64
}

// Health es la rama de salud (cada HORUS_SNMP_HEALTH_INTERVAL).
type Health struct {
	TemperatureCelsius    *float64
	CPUTemperatureCelsius *float64
	Voltage               *float64
	Firmware              *string
	SerialNumber          *string
	Sensors               []api.Sensor
}

// Discovered es una fila de IF-MIB en el descubrimiento.
type Discovered struct {
	IfIndex       int
	Name          string
	Alias         string
	Type          int
	HighSpeedMbps uint64
	AdminUp       *bool
	OperStatus    *int
}

// Counters son los contadores de una interfaz sondeada.
type Counters struct {
	IfIndex    int
	OperStatus *int
	domain.Counters
}

// ReadSystem lee sysDescr y sysUpTime.
func ReadSystem(ctx context.Context, t Transport) (System, error) {
	vars, err := t.Get(ctx, []string{OIDSysDescr, OIDSysUpTime})
	if err != nil {
		return System{}, err
	}
	m := index(vars)
	var s System
	if d, ok := m[OIDSysDescr].(string); ok {
		s.SysDescr = d
		if model := ParseModel(d); model != "" {
			s.Model = &model
		}
	}
	if u, ok := asUint(m[OIDSysUpTime]); ok {
		s.UptimeTicks = &u
	}
	return s, nil
}

// ParseModel extrae el modelo de sysDescr ("RouterOS CCR2116-12G-4S+").
func ParseModel(sysDescr string) string {
	d := strings.TrimSpace(sysDescr)
	if i := strings.Index(d, sysDescrRouterOSMarks); i >= 0 {
		d = strings.TrimSpace(d[i+len(sysDescrRouterOSMarks):])
	}
	if f := strings.Fields(d); len(f) > 0 {
		return f[0]
	}
	return ""
}

// ReadResources lee hrProcessorLoad (todas las filas) y la fila RAM de
// hrStorageTable.
func ReadResources(ctx context.Context, t Transport) (Resources, error) {
	var r Resources
	loads, err := t.Walk(ctx, OIDHrProcessorLoad)
	if err != nil {
		return r, err
	}
	for _, v := range loads {
		if n, ok := asInt(v.Value); ok && len(r.CPULoads) < maxCPURowsConsidered {
			r.CPULoads = append(r.CPULoads, int(n))
		}
	}
	rows, err := t.Walk(ctx, OIDHrStorageTable)
	if err != nil {
		return r, err
	}
	type storage struct {
		typ         string
		units, size *uint64
		used        *uint64
	}
	byIdx := map[string]*storage{}
	for _, v := range rows {
		col, idx, ok := column(OIDHrStorageTable, v.OID)
		if !ok {
			continue
		}
		s := byIdx[idx]
		if s == nil {
			s = &storage{}
			byIdx[idx] = s
		}
		switch col {
		case hrStorageColumnType:
			s.typ, _ = v.Value.(string)
		case hrStorageColumnUnits:
			if n, ok := asUint(v.Value); ok {
				s.units = &n
			}
		case hrStorageColumnSize:
			if n, ok := asUint(v.Value); ok {
				s.size = &n
			}
		case hrStorageColumnUsed:
			if n, ok := asUint(v.Value); ok {
				s.used = &n
			}
		}
	}
	for _, s := range byIdx {
		if s.typ != OIDHrStorageRAM && s.typ != strings.TrimPrefix(OIDHrStorageRAM, ".") {
			continue
		}
		if s.units == nil || s.size == nil || s.used == nil {
			continue
		}
		total, used := *s.size**s.units, *s.used**s.units
		r.MemoryTotalBytes, r.MemoryUsedBytes = &total, &used
		break
	}
	return r, nil
}

// ReadHealth lee temperatura, voltaje, versión, serie y la tabla de
// sensores. Los OIDs ausentes dejan nil; solo un error de transporte falla.
func ReadHealth(ctx context.Context, t Transport) (Health, error) {
	var h Health
	vars, err := t.Get(ctx, []string{OIDMtxrHlTemperature, OIDMtxrHlCPUTemp, OIDMtxrHlVoltage, OIDMtxrLicVersion, OIDMtxrSerialNumber})
	if err != nil {
		return h, err
	}
	m := index(vars)
	h.TemperatureCelsius = tenths(m[OIDMtxrHlTemperature])
	h.CPUTemperatureCelsius = tenths(m[OIDMtxrHlCPUTemp])
	h.Voltage = tenths(m[OIDMtxrHlVoltage])
	if s, ok := m[OIDMtxrLicVersion].(string); ok && s != "" {
		h.Firmware = &s
	}
	if s, ok := m[OIDMtxrSerialNumber].(string); ok && s != "" {
		h.SerialNumber = &s
	}
	rows, err := t.Walk(ctx, OIDMtxrGaugeTable)
	if err != nil {
		return h, err
	}
	type gauge struct {
		name  string
		value *int64
		unit  *int64
	}
	byIdx := map[string]*gauge{}
	var order []string
	for _, v := range rows {
		col, idx, ok := column(OIDMtxrGaugeTable, v.OID)
		if !ok {
			continue
		}
		g := byIdx[idx]
		if g == nil {
			g = &gauge{}
			byIdx[idx] = g
			order = append(order, idx)
		}
		switch col {
		case mtxrGaugeColumnName:
			g.name, _ = v.Value.(string)
		case mtxrGaugeColumnValue:
			if n, ok := asInt(v.Value); ok {
				g.value = &n
			}
		case mtxrGaugeColumnUnit:
			if n, ok := asInt(v.Value); ok {
				g.unit = &n
			}
		}
	}
	sort.Slice(order, func(i, j int) bool { return numLess(order[i], order[j]) })
	for _, idx := range order {
		g := byIdx[idx]
		if g.name == "" || g.value == nil {
			continue
		}
		s := api.Sensor{Name: g.name, Value: float64(*g.value)}
		if g.unit != nil {
			switch *g.unit {
			case gaugeUnitCelsius:
				s.Unit = str("C")
			case gaugeUnitRPM:
				s.Unit = str("rpm")
			case gaugeUnitDeciVolt:
				s.Unit, s.Value = str("V"), s.Value/healthScaleTenths
			case gaugeUnitDeciAmp:
				s.Unit, s.Value = str("A"), s.Value/healthScaleTenths
			case gaugeUnitDeciWatt:
				s.Unit, s.Value = str("W"), s.Value/healthScaleTenths
			case gaugeUnitStatus:
				s.Unit = str("status")
			}
		}
		h.Sensors = append(h.Sensors, s)
		// RouterOS 7 expone la salud como tabla: si faltan los OIDs fijos, se toman de aquí.
		switch {
		case h.TemperatureCelsius == nil && s.Unit != nil && *s.Unit == "C" && (g.name == "temperature" || g.name == "board-temperature"):
			h.TemperatureCelsius = f64(s.Value)
		case h.CPUTemperatureCelsius == nil && s.Unit != nil && *s.Unit == "C" && g.name == "cpu-temperature":
			h.CPUTemperatureCelsius = f64(s.Value)
		case h.Voltage == nil && s.Unit != nil && *s.Unit == "V" && g.name == "voltage":
			h.Voltage = f64(s.Value)
		}
	}
	return h, nil
}

// Discover recorre IF-MIB (walks GETBULK de ifName, ifAlias, ifType,
// ifHighSpeed, ifAdminStatus, ifOperStatus) y devuelve las filas por ifIndex.
func Discover(ctx context.Context, t Transport) ([]Discovered, error) {
	byIdx := map[int]*Discovered{}
	get := func(idx int) *Discovered {
		d := byIdx[idx]
		if d == nil {
			d = &Discovered{IfIndex: idx}
			byIdx[idx] = d
		}
		return d
	}
	names, err := t.Walk(ctx, OIDIfName)
	if err != nil {
		return nil, err
	}
	for _, v := range names {
		if idx, ok := lastIndex(OIDIfName, v.OID); ok {
			get(idx).Name, _ = v.Value.(string)
		}
	}
	for _, c := range []struct {
		root  string
		apply func(d *Discovered, v any)
	}{
		{OIDIfAlias, func(d *Discovered, v any) { d.Alias, _ = v.(string) }},
		{OIDIfType, func(d *Discovered, v any) {
			if n, ok := asInt(v); ok {
				d.Type = int(n)
			}
		}},
		{OIDIfHighSpeed, func(d *Discovered, v any) {
			if n, ok := asUint(v); ok {
				d.HighSpeedMbps = n
			}
		}},
		{OIDIfAdminStatus, func(d *Discovered, v any) {
			if n, ok := asInt(v); ok {
				up := n == 1
				d.AdminUp = &up
			}
		}},
		{OIDIfOperStatus, func(d *Discovered, v any) {
			if n, ok := asInt(v); ok {
				st := int(n)
				d.OperStatus = &st
			}
		}},
	} {
		rows, err := t.Walk(ctx, c.root)
		if err != nil {
			return nil, err
		}
		for _, v := range rows {
			if idx, ok := lastIndex(c.root, v.OID); ok {
				if d, known := byIdx[idx]; known {
					c.apply(d, v.Value)
				}
			}
		}
	}
	out := make([]Discovered, 0, len(byIdx))
	for _, d := range byIdx {
		if d.Name != "" {
			out = append(out, *d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IfIndex < out[j].IfIndex })
	return out, nil
}

// ReadCounters hace GET de los contadores de los ifIndex indicados (nunca un
// walk de ifTable: en un concentrador PPPoE tiene miles de filas).
func ReadCounters(ctx context.Context, t Transport, ifIndexes []int, at time.Time, uptimeTicks uint64) ([]Counters, error) {
	var out []Counters
	cols := []string{OIDIfHCInOctets, OIDIfHCOutOctets, OIDIfHCInUcast, OIDIfHCOutUcast, OIDIfInErrors, OIDIfOutErrors,
		OIDIfInDiscards, OIDIfOutDiscards, OIDIfOperStatus}
	for start := 0; start < len(ifIndexes); start += interfacesPerGet {
		end := min(start+interfacesPerGet, len(ifIndexes))
		chunk := ifIndexes[start:end]
		oids := make([]string, 0, len(chunk)*countersPerInterface)
		for _, idx := range chunk {
			for _, c := range cols {
				oids = append(oids, c+"."+strconv.Itoa(idx))
			}
		}
		vars, err := t.Get(ctx, oids)
		if err != nil {
			return nil, err
		}
		m := index(vars)
		for _, idx := range chunk {
			suffix := "." + strconv.Itoa(idx)
			c := Counters{IfIndex: idx}
			c.At, c.Uptime = at, uptimeTicks
			c.InOctets, _ = asUint(m[OIDIfHCInOctets+suffix])
			c.OutOctets, _ = asUint(m[OIDIfHCOutOctets+suffix])
			c.InUcast, _ = asUint(m[OIDIfHCInUcast+suffix])
			c.OutUcast, _ = asUint(m[OIDIfHCOutUcast+suffix])
			c.InErrors, _ = asUint(m[OIDIfInErrors+suffix])
			c.OutErrors, _ = asUint(m[OIDIfOutErrors+suffix])
			c.InDiscards, _ = asUint(m[OIDIfInDiscards+suffix])
			c.OutDiscards, _ = asUint(m[OIDIfOutDiscards+suffix])
			if n, ok := asInt(m[OIDIfOperStatus+suffix]); ok {
				st := int(n)
				c.OperStatus = &st
			}
			out = append(out, c)
		}
	}
	return out, nil
}

// --- utilidades ---

func index(vars []Var) map[string]any {
	m := make(map[string]any, len(vars))
	for _, v := range vars {
		m[normOID(v.OID)] = v.Value
	}
	return m
}

func normOID(o string) string {
	if !strings.HasPrefix(o, ".") {
		return "." + o
	}
	return o
}

// column separa "<root>.<col>.<idx>" en columna e índice.
func column(root, oid string) (col, idx string, ok bool) {
	rest, ok := strings.CutPrefix(normOID(oid), root+".")
	if !ok {
		return "", "", false
	}
	col, idx, ok = strings.Cut(rest, ".")
	return col, idx, ok
}

func lastIndex(root, oid string) (int, bool) {
	rest, ok := strings.CutPrefix(normOID(oid), root+".")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil
}

func numLess(a, b string) bool {
	x, errA := strconv.Atoi(a)
	y, errB := strconv.Atoi(b)
	if errA == nil && errB == nil {
		return x < y
	}
	return a < b
}

func asUint(v any) (uint64, bool) {
	switch n := v.(type) {
	case uint64:
		return n, true
	case int64:
		if n >= 0 {
			return uint64(n), true
		}
	case int:
		if n >= 0 {
			return uint64(n), true
		}
	case uint:
		return uint64(n), true
	}
	return 0, false
}

func asInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case uint64:
		if n <= math.MaxInt64 {
			return int64(n), true
		}
	case uint:
		return int64(n), true //nolint:gosec // valores SNMP pequeños
	}
	return 0, false
}

func tenths(v any) *float64 {
	n, ok := asInt(v)
	if !ok {
		return nil
	}
	return f64(float64(n) / healthScaleTenths)
}

func f64(v float64) *float64 { return &v }
func str(s string) *string   { return &s }
