package reporting

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// CSV del reporte: un .zip con un CSV por sección. Formato pensado para
// abrir con doble clic en Excel/LibreOffice en español:
//   - UTF-8 con BOM (Excel detecta la codificación),
//   - separador `;` (la coma es el separador decimal en es-*),
//   - decimales con coma, enteros y bytes sin separador de miles,
//   - fechas dd/mm/aaaa HH:mm en la zona del ISP (columna de zona en resumen.csv),
//   - textos que empiezan por = + - @ (alias del ISP) llevan un apóstrofo
//     delante para que la hoja de cálculo no los evalúe (inyección de fórmulas).
// Una sección sin datos (fuente no disponible) contiene solo la fila `nota`.

const csvBOM = "\xef\xbb\xbf"

// CSVSeparator es el separador de campos de los CSV del reporte.
const CSVSeparator = ';'

// CSVFiles son los archivos del zip, en orden.
var CSVFiles = []string{"resumen.csv", "hallazgos_por_tipo.csv", "hallazgos_por_severidad.csv", "clientes_con_hallazgos.csv",
	"hallazgos_criticos_altos.csv", "estado_seguridad_clientes.csv", "disponibilidad_routers.csv", "trafico_diario.csv", "avisos_enviados.csv"}

// RenderCSV escribe el zip de CSV del reporte en w.
func RenderCSV(w io.Writer, d *Data) error {
	zw := zip.NewWriter(w)
	loc := d.Period.Loc
	mod := d.GeneratedAt
	if mod.IsZero() {
		mod = time.Now()
	}
	for _, name := range CSVFiles {
		rows := csvSection(name, d, loc)
		var buf bytes.Buffer
		buf.WriteString(csvBOM)
		cw := csv.NewWriter(&buf)
		cw.Comma = CSVSeparator
		cw.UseCRLF = true
		for _, r := range rows {
			for i := range r {
				r[i] = csvSafe(r[i])
			}
			if err := cw.Write(r); err != nil {
				return fmt.Errorf("reporting: csv %s: %w", name, err)
			}
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return fmt.Errorf("reporting: csv %s: %w", name, err)
		}
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: mod})
		if err != nil {
			return fmt.Errorf("reporting: zip %s: %w", name, err)
		}
		if _, err := fw.Write(buf.Bytes()); err != nil {
			return fmt.Errorf("reporting: zip %s: %w", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("reporting: zip: %w", err)
	}
	return nil
}

// csvSafe neutraliza textos que una hoja de cálculo interpretaría como fórmula.
func csvSafe(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		// Números negativos legítimos ("-3") no los produce el reporte; un
		// alias "-x" sí puede: se protege todo.
		return "'" + s
	}
	return s
}

func note(n string) [][]string { return [][]string{{"nota", n}} }

func pct(r *float64) string {
	if r == nil {
		return ""
	}
	return decimal(*r*100, 3)
}

func itoa(n int) string      { return strconv.Itoa(n) }
func utoa(n uint64) string   { return strconv.FormatUint(n, 10) }
func deref(s *string) string { return strings.TrimSpace(ptrOr(s, "")) }

func ptrOr(s *string, def string) string {
	if s == nil {
		return def
	}
	return *s
}

func csvSection(name string, d *Data, loc *time.Location) [][]string {
	sec := d.Security
	switch name {
	case "resumen.csv":
		y, w := d.Period.ISOWeek()
		rows := [][]string{{"campo", "valor"},
			{"isp", d.TenantName},
			{"reporte", "Seguridad semanal"},
			{"semana_iso", fmt.Sprintf("%d-W%02d", y, w)},
			{"desde", FmtDateTime(d.Period.From, loc)},
			{"hasta", FmtDateTime(d.Period.To, loc)},
			{"zona_horaria", loc.String()},
			{"generado", FmtDateTime(d.GeneratedAt, loc)},
		}
		if sec != nil {
			mttr := ""
			if sec.MTTRSeconds != nil {
				mttr = strconv.FormatInt(int64(*sec.MTTRSeconds), 10)
			}
			rows = append(rows,
				[]string{"hallazgos_abiertos", itoa(sec.Totals.Opened)},
				[]string{"hallazgos_resueltos", itoa(sec.Totals.Resolved)},
				[]string{"hallazgos_falsos_positivos", itoa(sec.Totals.FalsePositive)},
				[]string{"hallazgos_activos_ahora", itoa(sec.ActiveNow)},
				[]string{"mttr_segundos", mttr},
				[]string{"mttr_muestras", itoa(sec.MTTRSamples)},
				[]string{"contactos_reputacion", itoa(sec.ReputationHits)},
				[]string{"clientes_contacto_reputacion", itoa(sec.ReputationCustomers)},
			)
		} else {
			rows = append(rows, []string{"nota_seguridad", d.SecurityNote})
		}
		if d.Availability != nil {
			rows = append(rows, []string{"disponibilidad_media_pct", pct(d.Availability.Overall)})
		}
		if t := d.Traffic; t != nil {
			rows = append(rows,
				[]string{"bytes_bajada", utoa(t.DownBytes)}, []string{"bytes_subida", utoa(t.UpBytes)},
				[]string{"bytes_bajada_periodo_anterior", utoa(t.PrevDownBytes)}, []string{"bytes_subida_periodo_anterior", utoa(t.PrevUpBytes)})
		}
		return rows
	case "hallazgos_por_tipo.csv":
		if sec == nil {
			return note(d.SecurityNote)
		}
		rows := [][]string{{"tipo", "clave", "abiertos", "resueltos", "falsos_positivos"}}
		for _, k := range sec.ByKind {
			rows = append(rows, []string{KindLabel(k.Kind), k.Kind, itoa(k.Opened), itoa(k.Resolved), itoa(k.FalsePositive)})
		}
		return rows
	case "hallazgos_por_severidad.csv":
		if sec == nil {
			return note(d.SecurityNote)
		}
		rows := [][]string{{"severidad", "clave", "abiertos", "resueltos", "falsos_positivos"}}
		for _, s := range sec.BySeverity {
			rows = append(rows, []string{SeverityLabel(s.Severity), s.Severity, itoa(s.Opened), itoa(s.Resolved), itoa(s.FalsePositive)})
		}
		return rows
	case "clientes_con_hallazgos.csv":
		if sec == nil {
			return note(d.SecurityNote)
		}
		rows := [][]string{{"alias", "ip", "nodo", "hallazgos_activos", "severidad_maxima", "tipo_principal", "estado_seguridad"}}
		for _, c := range sec.TopCustomers {
			rows = append(rows, []string{deref(c.Alias), c.Address, c.Site, itoa(c.OpenFindings), SeverityLabel(c.MaxSeverity),
				KindLabel(c.TopKind), SecurityLabel(c.SecurityState)})
		}
		return rows
	case "hallazgos_criticos_altos.csv":
		if sec == nil {
			return note(d.SecurityNote)
		}
		rows := [][]string{{"id", "severidad", "estado", "tipo", "resumen", "alias", "ip", "nodo", "abierto", "ultima_actividad"}}
		for _, f := range sec.OpenCriticalHigh {
			rows = append(rows, []string{f.ID.String(), SeverityLabel(f.Severity), StateLabel(f.State), KindLabel(f.Kind), f.Summary,
				deref(f.Alias), f.Address, f.Site, FmtDateTime(f.OpenedAt, loc), FmtDateTime(f.LastSeenAt, loc)})
		}
		return rows
	case "estado_seguridad_clientes.csv":
		if sec == nil {
			return note(d.SecurityNote)
		}
		rows := [][]string{{"estado", "clave", "clientes"}}
		for _, s := range SecurityStates {
			rows = append(rows, []string{SecurityLabel(s), s, itoa(sec.SecurityStates[s])})
		}
		return rows
	case "disponibilidad_routers.csv":
		if d.Availability == nil {
			return note(d.AvailabilityNote)
		}
		rows := [][]string{{"router", "nodo", "disponibilidad_icmp_pct", "disponibilidad_snmp_pct", "sondeos"}}
		for _, r := range d.Availability.Routers {
			rows = append(rows, []string{r.Name, r.Site, pct(r.ICMPRatio), pct(r.SNMPRatio), utoa(r.Samples)})
		}
		return rows
	case "trafico_diario.csv":
		if d.Traffic == nil {
			return note(d.TrafficNote)
		}
		rows := [][]string{{"dia", "bytes_bajada", "bytes_subida"}}
		for _, v := range d.Traffic.Daily {
			if !v.HasSamples {
				rows = append(rows, []string{FmtDate(v.Day, loc), "", ""})
				continue
			}
			rows = append(rows, []string{FmtDate(v.Day, loc), utoa(v.DownBytes), utoa(v.UpBytes)})
		}
		return rows
	case "avisos_enviados.csv":
		if d.Alerts == nil {
			return note(d.AlertsNote)
		}
		rows := [][]string{{"canal", "evento", "clave_evento", "enviados", "fallidos"}}
		for _, a := range d.Alerts {
			rows = append(rows, []string{ChannelLabel(a.ChannelKind), EventLabel(a.EventType), a.EventType, itoa(a.Sent), itoa(a.Failed)})
		}
		return rows
	}
	return nil
}
