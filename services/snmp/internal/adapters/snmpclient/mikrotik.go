package snmpclient

import (
	"context"
	"sort"
	"strings"

	"github.com/hcdestroyer/horus-flow/services/snmp/api"
)

// OIDs de MikroTik (docs/vendors/mikrotik.md §3.2): HOST-RESOURCES-MIB para
// CPU y memoria y MIKROTIK-MIB para la salud.
const (
	OIDMtxrHlTemperature  = ".1.3.6.1.4.1.14988.1.1.3.10.0"
	OIDMtxrHlCPUTemp      = ".1.3.6.1.4.1.14988.1.1.3.11.0"
	OIDMtxrHlVoltage      = ".1.3.6.1.4.1.14988.1.1.3.8.0"
	OIDMtxrGaugeTable     = ".1.3.6.1.4.1.14988.1.1.3.100.1"
	OIDMtxrLicVersion     = ".1.3.6.1.4.1.14988.1.1.4.4.0"
	OIDMtxrSerialNumber   = ".1.3.6.1.4.1.14988.1.1.7.3.0"
	mtxrGaugeColumnName   = "2"
	mtxrGaugeColumnValue  = "3"
	mtxrGaugeColumnUnit   = "4"
	gaugeUnitCelsius      = 1
	gaugeUnitRPM          = 2
	gaugeUnitDeciVolt     = 3
	gaugeUnitDeciAmp      = 4
	gaugeUnitDeciWatt     = 5
	gaugeUnitStatus       = 6
	sysDescrRouterOSMarks = "RouterOS "
)

// ReadSystem lee sysDescr y sysUpTime (modelo de RouterOS).
func ReadSystem(ctx context.Context, t Transport) (System, error) {
	return readSystem(ctx, t, ParseModel)
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
