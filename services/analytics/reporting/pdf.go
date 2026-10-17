package reporting

import (
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

// PDF del reporte: A4 vertical, imprimible, identidad Horus en tema claro
// (.claude/skills/horus-identity): un único color de marca (teal 700),
// estados siempre con marca + texto + color (nunca solo color), series de
// gráficos con la paleta categórica (nunca colores de estado), fuente
// estándar Helvetica (sin incrustar archivos; texto en cp1252, que cubre el
// español), cifras con coma decimal y fechas dd/mm/aaaa HH:mm en la zona del ISP.

type rgb struct{ r, g, b int }

var (
	colPrimary   = rgb{0x0f, 0x76, 0x6e} // teal 700
	colPrimaryBg = rgb{0xf0, 0xfd, 0xfa} // teal 50
	colText      = rgb{0x18, 0x18, 0x1b}
	colMuted     = rgb{0x52, 0x52, 0x5b}
	colBorder    = rgb{0xe4, 0xe4, 0xe7}
	colZebra     = rgb{0xfa, 0xfa, 0xfa}
	colHeaderBg  = rgb{0xf4, 0xf4, 0xf5}
	colSuccess   = rgb{0x16, 0x65, 0x34}
	colInfo      = rgb{0x1d, 0x4e, 0xd8}
	colWarning   = rgb{0x92, 0x40, 0x0e}
	colError     = rgb{0xb9, 0x1c, 0x1c}
	colNeutral   = rgb{0x52, 0x52, 0x5b}
	// Paleta categórica (claro) de horus-identity: ranuras 1 y 2.
	colSeries1 = rgb{0x2a, 0x78, 0xd6}
	colSeries2 = rgb{0xeb, 0x68, 0x34}
)

// severityTone es el color (y la marca) de una severidad: crítica y alta en
// error, media en aviso, baja en neutro (estados.md de horus-identity).
func severityTone(s string) rgb {
	switch s {
	case "critical", "high":
		return colError
	case "medium":
		return colWarning
	case "info":
		return colInfo
	}
	return colNeutral
}

func securityTone(s string) rgb {
	switch s {
	case "infected":
		return colError
	case "suspected":
		return colWarning
	case "mitigated":
		return colInfo
	case "clean":
		return colSuccess
	}
	return colNeutral
}

const (
	pageW    = 210.0
	pageH    = 297.0
	margin   = 16.0
	contentW = pageW - 2*margin
	bottom   = 18.0
)

type pdfDoc struct {
	f   *fpdf.Fpdf
	tr  func(string) string
	loc *time.Location
}

func (p *pdfDoc) color(c rgb)                     { p.f.SetTextColor(c.r, c.g, c.b) }
func (p *pdfDoc) fill(c rgb)                      { p.f.SetFillColor(c.r, c.g, c.b) }
func (p *pdfDoc) draw(c rgb)                      { p.f.SetDrawColor(c.r, c.g, c.b) }
func (p *pdfDoc) font(style string, size float64) { p.f.SetFont("Helvetica", style, size) }

// text escribe s (UTF-8) traducido a cp1252.
func (p *pdfDoc) cell(w, h float64, s, border string, ln int, align string, fill bool) {
	p.f.CellFormat(w, h, p.tr(s), border, ln, align, fill, 0, "")
}

// fit recorta s con "…" para que quepa en w mm con la fuente actual.
func (p *pdfDoc) fit(s string, w float64) string {
	s = strings.Join(strings.Fields(s), " ")
	if p.f.GetStringWidth(p.tr(s)) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && p.f.GetStringWidth(p.tr(string(r)+"…")) > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// ensure añade página si no caben h mm más.
func (p *pdfDoc) ensure(h float64) bool {
	if p.f.GetY()+h > pageH-bottom {
		p.f.AddPage()
		return true
	}
	return false
}

func (p *pdfDoc) heading(n int, title string) {
	p.ensure(24)
	p.f.Ln(4)
	y := p.f.GetY()
	p.fill(colPrimary)
	p.f.Rect(margin, y+1, 1.6, 6.5, "F")
	p.f.SetX(margin + 4)
	p.font("B", 14)
	p.color(colText)
	p.cell(contentW-4, 8.5, fmt.Sprintf("%d. %s", n, title), "", 1, "L", false)
	p.f.Ln(1.5)
}

func (p *pdfDoc) paragraph(s string) {
	p.font("", 9.5)
	p.color(colMuted)
	p.f.MultiCell(contentW, 4.8, p.tr(s), "", "L", false)
	p.f.Ln(1.5)
}

// notice es la caja de "sin datos" de una sección (hueco explícito, nunca ceros).
func (p *pdfDoc) notice(s string) {
	p.ensure(14)
	p.fill(colHeaderBg)
	p.draw(colBorder)
	p.font("", 9.5)
	p.color(colMuted)
	p.f.SetX(margin)
	p.f.MultiCell(contentW, 6, p.tr("Sin datos — "+s), "1", "L", true)
	p.f.Ln(2)
}

// kpi dibuja una fila de indicadores (etiqueta pequeña, valor grande).
type kpi struct {
	label, value, hint string
}

func (p *pdfDoc) kpis(items []kpi) {
	if len(items) == 0 {
		return
	}
	p.ensure(24)
	gap := 3.0
	w := (contentW - gap*float64(len(items)-1)) / float64(len(items))
	y := p.f.GetY()
	for i, it := range items {
		x := margin + float64(i)*(w+gap)
		p.fill(rgb{0xff, 0xff, 0xff})
		p.draw(colBorder)
		p.f.RoundedRect(x, y, w, 21, 2.5, "1234", "FD")
		p.f.SetXY(x+3, y+2.5)
		p.font("", 8)
		p.color(colMuted)
		p.cell(w-6, 4, p.fit(it.label, w-6), "", 2, "L", false)
		p.f.SetX(x + 3)
		p.font("B", 15)
		p.color(colText)
		p.cell(w-6, 8, p.fit(it.value, w-6), "", 2, "L", false)
		if it.hint != "" {
			p.f.SetX(x + 3)
			p.font("", 7.5)
			p.color(colMuted)
			p.cell(w-6, 4, p.fit(it.hint, w-6), "", 0, "L", false)
		}
	}
	p.f.SetXY(margin, y+24)
}

// col es una columna de tabla.
type col struct {
	title string
	w     float64 // fracción del ancho útil
	align string
	mono  bool
}

// cellv es una celda: texto y, opcionalmente, un tono (estado con marca + texto).
type cellv struct {
	text string
	tone *rgb
}

func plain(s string) cellv        { return cellv{text: s} }
func toned(s string, c rgb) cellv { return cellv{text: s, tone: &c} }

func (p *pdfDoc) table(cols []col, rows [][]cellv, empty string) {
	const hh, rh = 7.0, 6.2
	header := func() {
		p.fill(colHeaderBg)
		p.draw(colBorder)
		p.font("B", 8)
		p.color(colMuted)
		p.f.SetX(margin)
		for _, c := range cols {
			w := c.w * contentW
			p.cell(w, hh, " "+p.fit(c.title, w-2), "B", 0, alignOf(c.align), true)
		}
		p.f.Ln(hh)
	}
	p.ensure(hh + rh*2)
	header()
	if len(rows) == 0 {
		p.font("I", 8.5)
		p.color(colMuted)
		p.f.SetX(margin)
		p.cell(contentW, rh+1, " "+empty, "B", 1, "L", false)
		p.f.Ln(2)
		return
	}
	for i, r := range rows {
		if p.ensure(rh) {
			header()
		}
		zebra := i%2 == 1
		if zebra {
			p.fill(colZebra)
		}
		p.f.SetX(margin)
		for j, c := range cols {
			w := c.w * contentW
			v := cellv{}
			if j < len(r) {
				v = r[j]
			}
			if c.mono {
				p.f.SetFont("Courier", "", 8)
			} else {
				p.font("", 8.5)
			}
			x, y := p.f.GetXY()
			if v.tone != nil {
				// Marca (cuadrado de color) + texto del color: el estado no
				// depende solo del color.
				if zebra {
					p.f.Rect(x, y, w, rh, "F")
				}
				p.font("B", 8.5)
				txt := p.fit(v.text, w-6)
				// En columnas numéricas (alineadas a la derecha) la marca va
				// pegada al número, no al borde izquierdo.
				mx := x + 1.5
				if c.align == "R" {
					mx = x + w - 1.2 - p.f.GetStringWidth(p.tr(txt)) - 3.4
				}
				p.fill(*v.tone)
				p.f.Rect(mx, y+rh/2-1.2, 2.4, 2.4, "F")
				p.color(*v.tone)
				p.f.SetXY(mx+3.1, y)
				p.cell(x+w-mx-3.1, rh, txt+" ", "", 0, "L", false)
				p.f.SetXY(x+w, y)
				if zebra {
					p.fill(colZebra)
				}
				continue
			}
			p.color(colText)
			p.cell(w, rh, " "+p.fit(v.text, w-2)+" ", "", 0, alignOf(c.align), zebra)
		}
		p.f.Ln(rh)
	}
	p.draw(colBorder)
	p.f.Line(margin, p.f.GetY(), margin+contentW, p.f.GetY())
	p.f.Ln(3)
}

func alignOf(a string) string {
	if a == "" {
		return "L"
	}
	return a
}

// hbars es un gráfico de barras horizontales de una serie (un solo color de
// la paleta categórica; la etiqueta y el valor van escritos).
func (p *pdfDoc) hbars(title string, labels []string, values []float64, fmtv func(float64) string) {
	if len(labels) == 0 {
		return
	}
	rowH := 6.0
	p.ensure(10 + rowH*float64(len(labels)))
	p.font("B", 9)
	p.color(colText)
	p.f.SetX(margin)
	p.cell(contentW, 6, title, "", 1, "L", false)
	maxV := 0.0
	for _, v := range values {
		maxV = math.Max(maxV, v)
	}
	labelW, valueW := 42.0, 22.0
	barW := contentW - labelW - valueW - 4
	for i, l := range labels {
		y := p.f.GetY()
		p.font("", 8.5)
		p.color(colMuted)
		p.f.SetX(margin)
		p.cell(labelW, rowH, p.fit(l, labelW-1), "", 0, "L", false)
		p.fill(colHeaderBg)
		p.f.Rect(margin+labelW, y+1.2, barW, rowH-2.4, "F")
		if maxV > 0 && values[i] > 0 {
			p.fill(colSeries1)
			p.f.Rect(margin+labelW, y+1.2, math.Max(0.6, barW*values[i]/maxV), rowH-2.4, "F")
		}
		p.f.SetXY(margin+labelW+barW+2, y)
		p.color(colText)
		p.font("B", 8.5)
		p.cell(valueW, rowH, fmtv(values[i]), "", 1, "R", false)
	}
	p.f.Ln(3)
}

// vbars2 es un gráfico de columnas agrupadas de dos series (bajada y subida
// por día) con leyenda; los días sin datos se marcan como hueco.
func (p *pdfDoc) vbars2(title string, cats []string, a, b []float64, has []bool, nameA, nameB string, fmtv func(float64) string) {
	h := 52.0
	p.ensure(h + 22)
	p.font("B", 9)
	p.color(colText)
	p.f.SetX(margin)
	p.cell(contentW, 6, title, "", 1, "L", false)
	// Leyenda.
	y := p.f.GetY()
	for i, s := range []struct {
		n string
		c rgb
	}{{nameA, colSeries1}, {nameB, colSeries2}} {
		x := margin + float64(i)*40
		p.fill(s.c)
		p.f.Rect(x, y+1.3, 3, 3, "F")
		p.f.SetXY(x+4.5, y)
		p.font("", 8)
		p.color(colMuted)
		p.cell(34, 5.5, s.n, "", 0, "L", false)
	}
	p.f.SetXY(margin, y+7)
	top := p.f.GetY()
	axisW := 20.0
	plotX, plotW := margin+axisW, contentW-axisW
	maxV := 0.0
	for i := range cats {
		maxV = math.Max(maxV, math.Max(a[i], b[i]))
	}
	// Rejilla y eje (0, mitad, máximo).
	p.draw(colBorder)
	p.font("", 7)
	p.color(colMuted)
	for _, frac := range []float64{0, 0.5, 1} {
		gy := top + h - h*frac
		p.f.Line(plotX, gy, plotX+plotW, gy)
		p.f.SetXY(margin, gy-2)
		if maxV > 0 {
			p.cell(axisW-2, 4, fmtv(maxV*frac), "", 0, "R", false)
		}
	}
	n := float64(len(cats))
	slot := plotW / math.Max(n, 1)
	bw := math.Min(7, slot*0.32)
	for i, c := range cats {
		x0 := plotX + float64(i)*slot + slot/2
		if !has[i] {
			p.font("I", 7)
			p.color(colMuted)
			p.f.SetXY(x0-slot/2, top+h-6)
			p.cell(slot, 4, "sin datos", "", 0, "C", false)
		} else if maxV > 0 {
			ha := h * a[i] / maxV
			hb := h * b[i] / maxV
			p.fill(colSeries1)
			p.f.Rect(x0-bw-0.4, top+h-ha, bw, ha, "F")
			p.fill(colSeries2)
			p.f.Rect(x0+0.4, top+h-hb, bw, hb, "F")
		}
		p.font("", 7.5)
		p.color(colMuted)
		p.f.SetXY(x0-slot/2, top+h+1)
		p.cell(slot, 4, c, "", 0, "C", false)
	}
	p.f.SetXY(margin, top+h+8)
}

// RenderPDF escribe el PDF del reporte en w.
func RenderPDF(w io.Writer, d *Data) error {
	loc := d.Period.Loc
	if loc == nil {
		loc = time.UTC
	}
	f := fpdf.New("P", "mm", "A4", "")
	p := &pdfDoc{f: f, tr: f.UnicodeTranslatorFromDescriptor(""), loc: loc}
	year, week := d.Period.ISOWeek()
	f.SetTitle(fmt.Sprintf("Reporte de seguridad semanal · %s · semana %d de %d", d.TenantName, week, year), true)
	f.SetAuthor("Horus Flow", true)
	f.SetCreator("Horus Flow (rol reporting)", true)
	f.SetSubject("Reporte de seguridad semanal del ISP", true)
	f.SetCreationDate(d.GeneratedAt)
	f.SetModificationDate(d.GeneratedAt)
	f.SetCatalogSort(true) // salida reproducible (orden estable de fuentes y recursos)
	f.SetMargins(margin, margin, margin)
	f.SetAutoPageBreak(true, bottom)
	f.AliasNbPages("{nb}")
	f.SetFooterFunc(func() {
		if f.PageNo() == 1 {
			return
		}
		f.SetY(-12)
		p.draw(colBorder)
		f.Line(margin, f.GetY()-1, margin+contentW, f.GetY()-1)
		p.font("", 7.5)
		p.color(colMuted)
		p.cell(contentW*0.75, 5, p.fit(fmt.Sprintf("Horus Flow · Seguridad semanal · %s · %s", d.TenantName, FmtPeriod(d.Period)), contentW*0.75), "", 0, "L", false)
		p.cell(contentW*0.25, 5, fmt.Sprintf("Página %d de {nb}", f.PageNo()), "", 0, "R", false)
	})

	cover(p, d, week, year)
	f.AddPage()
	sections(p, d)
	if err := f.Output(w); err != nil {
		return fmt.Errorf("reporting: pdf: %w", err)
	}
	return nil
}

func cover(p *pdfDoc, d *Data, week, year int) {
	f := p.f
	f.AddPage()
	loc := p.loc
	// Banda de marca.
	p.fill(colPrimary)
	f.Rect(0, 0, pageW, 46, "F")
	// Marca: cuadrado redondeado blanco con el "ojo" en teal (logo-mark simplificado).
	f.SetFillColor(255, 255, 255)
	f.RoundedRect(margin, 14, 16, 16, 3.5, "1234", "F")
	p.fill(colPrimary)
	f.Ellipse(margin+8, 22, 5.2, 2.9, 0, "F")
	f.SetFillColor(255, 255, 255)
	f.Circle(margin+8, 22, 1.7, "F")
	f.SetTextColor(255, 255, 255)
	f.SetXY(margin+21, 15)
	p.font("B", 18)
	p.cell(100, 8, "Horus Flow", "", 2, "L", false)
	p.font("", 10)
	p.cell(120, 6, "Reporte de seguridad semanal", "", 0, "L", false)

	f.SetXY(margin, 62)
	p.font("", 10)
	p.color(colMuted)
	p.cell(contentW, 6, "ISP", "", 1, "L", false)
	p.font("B", 24)
	p.color(colText)
	f.SetX(margin)
	f.MultiCell(contentW, 11, p.tr(d.TenantName), "", "L", false)
	f.Ln(4)
	p.font("B", 13)
	p.color(colPrimary)
	f.SetX(margin)
	p.cell(contentW, 7, fmt.Sprintf("Semana %d de %d", week, year), "", 1, "L", false)
	p.font("", 10.5)
	p.color(colText)
	f.SetX(margin)
	p.cell(contentW, 6, "Periodo: "+FmtPeriod(d.Period), "", 1, "L", false)
	f.SetX(margin)
	p.color(colMuted)
	p.cell(contentW, 6, fmt.Sprintf("Zona horaria: %s · Generado el %s", loc.String(), FmtDateTime(d.GeneratedAt, loc)), "", 1, "L", false)
	f.Ln(8)

	// Indicadores principales.
	var items []kpi
	if s := d.Security; s != nil {
		mttr := "—"
		if s.MTTRSeconds != nil {
			mttr = FmtDuration(*s.MTTRSeconds)
		}
		items = append(items,
			kpi{"Hallazgos abiertos", FmtInt(s.Totals.Opened), "en el periodo"},
			kpi{"Resueltos", FmtInt(s.Totals.Resolved), fmt.Sprintf("%s falsos positivos", FmtInt(s.Totals.FalsePositive))},
			kpi{"Activos ahora", FmtInt(s.ActiveNow), "abiertos o reconocidos"},
			kpi{"Tiempo medio resolución", mttr, fmt.Sprintf("%s resueltos", FmtInt(s.MTTRSamples))})
	}
	if items != nil {
		p.kpis(items)
	}
	var items2 []kpi
	if a := d.Availability; a != nil {
		items2 = append(items2, kpi{"Disponibilidad media", FmtPercent(a.Overall, 2), fmt.Sprintf("%d routers", len(a.Routers))})
	}
	if t := d.Traffic; t != nil {
		items2 = append(items2, kpi{"Tráfico de bajada", FmtBytes(t.DownBytes), FmtChange(t.DownBytes, t.PrevDownBytes) + " vs. periodo anterior"},
			kpi{"Tráfico de subida", FmtBytes(t.UpBytes), FmtChange(t.UpBytes, t.PrevUpBytes) + " vs. periodo anterior"})
	}
	if s := d.Security; s != nil {
		items2 = append(items2, kpi{"Contactos de reputación", FmtInt(s.ReputationHits), fmt.Sprintf("%s clientes", FmtInt(s.ReputationCustomers))})
	}
	if items2 != nil {
		p.kpis(items2)
	}

	// Contenido.
	f.Ln(6)
	p.font("B", 11)
	p.color(colText)
	f.SetX(margin)
	p.cell(contentW, 7, "Contenido", "", 1, "L", false)
	p.font("", 10)
	p.color(colMuted)
	for i, t := range sectionTitles {
		f.SetX(margin)
		p.cell(contentW, 6, fmt.Sprintf("%d. %s", i+1, t), "", 1, "L", false)
	}
	// Nota de confidencialidad.
	f.SetXY(margin, pageH-38)
	p.fill(colPrimaryBg)
	p.draw(colBorder)
	p.font("", 8.5)
	p.color(colMuted)
	f.MultiCell(contentW, 4.6, p.tr("Documento confidencial del ISP. Contiene direcciones IP y alias de clientes: "+
		"no lo reenvíe fuera de la organización. Generado por Horus Flow a partir de los hallazgos de detección, "+
		"el sondeo SNMP/ICMP de los routers y los agregados de flujos."), "1", "L", true)
}

var sectionTitles = []string{
	"Resumen de hallazgos",
	"Hallazgos por tipo",
	"Clientes con más hallazgos activos",
	"Hallazgos críticos y altos activos",
	"Estado de seguridad de los clientes",
	"Disponibilidad de routers",
	"Contexto de tráfico",
	"Avisos enviados",
}

func sections(p *pdfDoc, d *Data) {
	loc := p.loc
	s := d.Security

	// 1. Resumen.
	p.heading(1, sectionTitles[0])
	if s == nil {
		p.notice(d.SecurityNote)
	} else {
		p.paragraph("Hallazgos abiertos, resueltos y marcados como falso positivo durante el periodo, por severidad. " +
			"\"Activos ahora\" cuenta los hallazgos abiertos o reconocidos al generar el reporte.")
		var rows [][]cellv
		labels := make([]string, 0, len(s.BySeverity))
		values := make([]float64, 0, len(s.BySeverity))
		for _, r := range s.BySeverity {
			rows = append(rows, []cellv{toned(SeverityLabel(r.Severity), severityTone(r.Severity)), plain(FmtInt(r.Opened)),
				plain(FmtInt(r.Resolved)), plain(FmtInt(r.FalsePositive))})
			labels = append(labels, SeverityLabel(r.Severity))
			values = append(values, float64(r.Opened))
		}
		rows = append(rows, []cellv{plain("Total"), plain(FmtInt(s.Totals.Opened)), plain(FmtInt(s.Totals.Resolved)),
			plain(FmtInt(s.Totals.FalsePositive))})
		p.table([]col{{"Severidad", 0.34, "L", false}, {"Abiertos", 0.22, "R", false}, {"Resueltos", 0.22, "R", false},
			{"Falsos positivos", 0.22, "R", false}}, rows, "Sin hallazgos en el periodo.")
		mttr := "sin hallazgos resueltos en el periodo"
		if s.MTTRSeconds != nil {
			mttr = fmt.Sprintf("%s (media de %s hallazgos resueltos)", FmtDuration(*s.MTTRSeconds), FmtInt(s.MTTRSamples))
		}
		p.paragraph("Tiempo medio de resolución (apertura a resolución): " + mttr + ".")
		p.hbars("Hallazgos abiertos en el periodo por severidad", labels, values, func(v float64) string { return FmtInt(int(v)) })
	}

	// 2. Por tipo.
	p.heading(2, sectionTitles[1])
	if s == nil {
		p.notice(d.SecurityNote)
	} else {
		var rows [][]cellv
		for _, k := range s.ByKind {
			rows = append(rows, []cellv{plain(KindLabel(k.Kind)), plain(FmtInt(k.Opened)), plain(FmtInt(k.Resolved)), plain(FmtInt(k.FalsePositive))})
		}
		p.table([]col{{"Tipo de hallazgo", 0.46, "L", false}, {"Abiertos", 0.18, "R", false}, {"Resueltos", 0.18, "R", false},
			{"Falsos positivos", 0.18, "R", false}}, rows, "Sin hallazgos en el periodo.")
	}

	// 3. Clientes.
	p.heading(3, sectionTitles[2])
	if s == nil {
		p.notice(d.SecurityNote)
	} else {
		p.paragraph("Hasta 10 clientes con más hallazgos activos al generar el reporte. Se muestra el alias del cliente o, si no tiene, su dirección IP.")
		var rows [][]cellv
		for _, c := range s.TopCustomers {
			name := c.Address
			if a := deref(c.Alias); a != "" {
				name = a
			}
			rows = append(rows, []cellv{plain(name), plain(c.Address), plain(c.Site), plain(FmtInt(c.OpenFindings)),
				toned(SeverityLabel(c.MaxSeverity), severityTone(c.MaxSeverity)), toned(SecurityLabel(c.SecurityState), securityTone(c.SecurityState))})
		}
		p.table([]col{{"Cliente", 0.24, "L", false}, {"IP", 0.2, "L", true}, {"Nodo", 0.18, "L", false}, {"Activos", 0.1, "R", false},
			{"Severidad máx.", 0.14, "L", false}, {"Estado", 0.14, "L", false}}, rows, "Ningún cliente con hallazgos activos.")
	}

	// 4. Críticos y altos.
	p.heading(4, sectionTitles[3])
	if s == nil {
		p.notice(d.SecurityNote)
	} else {
		p.paragraph("Hallazgos de severidad crítica o alta que siguen abiertos o reconocidos (hasta 50, más graves y recientes primero).")
		var rows [][]cellv
		for _, f := range s.OpenCriticalHigh {
			name := f.Address
			if a := deref(f.Alias); a != "" {
				name = a + " (" + f.Address + ")"
			}
			rows = append(rows, []cellv{toned(SeverityLabel(f.Severity), severityTone(f.Severity)), plain(KindLabel(f.Kind)), plain(name),
				plain(f.Site), plain(FmtDateTime(f.OpenedAt, loc)), plain(StateLabel(f.State))})
		}
		p.table([]col{{"Severidad", 0.12, "L", false}, {"Tipo", 0.26, "L", false}, {"Cliente", 0.24, "L", false}, {"Nodo", 0.14, "L", false},
			{"Abierto", 0.14, "L", false}, {"Estado", 0.10, "L", false}}, rows, "Sin hallazgos críticos ni altos activos.")
	}

	// 5. Estado de seguridad.
	p.heading(5, sectionTitles[4])
	if s == nil {
		p.notice(d.SecurityNote)
	} else {
		var rows [][]cellv
		for _, st := range SecurityStates {
			rows = append(rows, []cellv{toned(SecurityLabel(st), securityTone(st)), plain(FmtInt(s.SecurityStates[st]))})
		}
		p.table([]col{{"Estado de seguridad", 0.6, "L", false}, {"Clientes", 0.4, "R", false}}, rows, "")
		p.paragraph(fmt.Sprintf("Contactos con destinos de listas de reputación (C2 de botnets y otras listas) abiertos en el periodo: %s hallazgos en %s clientes.",
			FmtInt(s.ReputationHits), FmtInt(s.ReputationCustomers)))
	}

	// 6. Disponibilidad.
	p.heading(6, sectionTitles[5])
	if a := d.Availability; a == nil {
		p.notice(d.AvailabilityNote)
	} else {
		p.paragraph(fmt.Sprintf("Proporción de sondeos ICMP y SNMP correctos de cada router en el periodo (agregados horarios). Media ponderada: %s. "+
			"Peor disponibilidad primero; \"—\" = sin sondeos en el periodo.", FmtPercent(a.Overall, 2)))
		var rows [][]cellv
		for _, r := range a.Routers {
			icmp := plain(FmtPercent(r.ICMPRatio, 2))
			if r.ICMPRatio != nil {
				switch {
				case *r.ICMPRatio < 0.95:
					icmp = toned(FmtPercent(r.ICMPRatio, 2), colError)
				case *r.ICMPRatio < 0.99:
					icmp = toned(FmtPercent(r.ICMPRatio, 2), colWarning)
				}
			}
			rows = append(rows, []cellv{plain(r.Name), plain(r.Site), icmp, plain(FmtPercent(r.SNMPRatio, 2)), plain(FmtInt(int(r.Samples)))})
		}
		p.table([]col{{"Router", 0.3, "L", false}, {"Nodo", 0.22, "L", false}, {"Disponibilidad ICMP", 0.18, "R", false},
			{"Respuesta SNMP", 0.16, "R", false}, {"Sondeos", 0.14, "R", false}}, rows, "Sin routers registrados.")
	}

	// 7. Tráfico.
	p.heading(7, sectionTitles[6])
	if t := d.Traffic; t == nil {
		p.notice(d.TrafficNote)
	} else {
		p.table([]col{{"Sentido", 0.25, "L", false}, {"Periodo", 0.25, "R", false}, {"Periodo anterior", 0.25, "R", false}, {"Variación", 0.25, "R", false}},
			[][]cellv{
				{plain("Bajada"), plain(FmtBytes(t.DownBytes)), plain(FmtBytes(t.PrevDownBytes)), plain(FmtChange(t.DownBytes, t.PrevDownBytes))},
				{plain("Subida"), plain(FmtBytes(t.UpBytes)), plain(FmtBytes(t.PrevUpBytes)), plain(FmtChange(t.UpBytes, t.PrevUpBytes))},
			}, "")
		if len(t.Daily) > 0 && len(t.Daily) <= 31 {
			cats := make([]string, len(t.Daily))
			a := make([]float64, len(t.Daily))
			b := make([]float64, len(t.Daily))
			has := make([]bool, len(t.Daily))
			for i, v := range t.Daily {
				cats[i] = v.Day.Format("02/01")
				a[i], b[i], has[i] = float64(v.DownBytes), float64(v.UpBytes), v.HasSamples
			}
			if len(t.Daily) > 10 {
				for i := range cats {
					if i%3 != 0 {
						cats[i] = ""
					}
				}
			}
			p.vbars2("Volumen diario (bytes, SI decimal)", cats, a, b, has, "Bajada", "Subida", func(v float64) string { return FmtBytes(uint64(v)) })
		}
	}

	// 8. Avisos.
	p.heading(8, sectionTitles[7])
	if d.Alerts == nil {
		p.notice(d.AlertsNote)
	} else {
		var rows [][]cellv
		for _, a := range d.Alerts {
			failed := plain(FmtInt(a.Failed))
			if a.Failed > 0 {
				failed = toned(FmtInt(a.Failed), colError)
			}
			rows = append(rows, []cellv{plain(ChannelLabel(a.ChannelKind)), plain(EventLabel(a.EventType)), plain(FmtInt(a.Sent)), failed})
		}
		p.table([]col{{"Canal", 0.2, "L", false}, {"Evento", 0.44, "L", false}, {"Enviados", 0.18, "R", false}, {"Fallidos", 0.18, "R", false}},
			rows, "No se enviaron avisos en el periodo.")
	}
}
