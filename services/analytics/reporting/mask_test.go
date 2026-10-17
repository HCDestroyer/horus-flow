package reporting

import (
	"bytes"
	"compress/zlib"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	secapi "github.com/hcdestroyer/horus-flow/services/detection/api/securityreport"
)

func TestMaskAddress(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"100.64.1.7":                "100.64.1.x",
		"100.64.1.7/32":             "100.64.1.x",
		"2001:db8:1:2::42":          "2001:db8:1…",
		"2001:db8::42":              "2001:db8:0…",
		"2001:db8:abcd:12::/56":     "2001:db8:abcd…",
		"[2001:db8:5::1]":           "2001:db8:5…",
		"::ffff:198.51.100.9":       "198.51.100.x",
		"fe80::1%eth0":              "fe80:0:0…",
		"no es una ip":              "IP enmascarada",
		"":                          "",
		"  203.0.113.200  ":         "203.0.113.x",
		"2001:0db8:0000:0000::0001": "2001:db8:0…",
	} {
		if got := MaskAddress(in); got != want {
			t.Errorf("MaskAddress(%q) = %q, quiero %q", in, got, want)
		}
	}
}

func TestMaskText(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Contacto con C2 conocido 203.0.113.50:443 (abuse.ch)":    "Contacto con C2 conocido 203.0.113.x:443 (abuse.ch)",
		"Ráfaga de 12.000 pps hacia 2001:db8:77::9 durante 5 min": "Ráfaga de 12.000 pps hacia 2001:db8:77… durante 5 min",
		"Beaconing a 2001:db8:1::5:443 cada 60 s":                 "Beaconing a 2001:db8:1… cada 60 s",
		"Escaneo a 1.234 destinos en 10 min":                      "Escaneo a 1.234 destinos en 10 min",
		"Visto a las 12:30 y 08:15:00":                            "Visto a las 12:30 y 08:15:00",
		"Desde [2001:db8:9::1]:8080":                              "Desde [2001:db8:9…]:8080",
	} {
		if got := MaskText(in); got != want {
			t.Errorf("MaskText(%q) = %q, quiero %q", in, got, want)
		}
	}
}

// Rastro de datos personales: IPs completas (v4 y v6) y alias de muestra.
var (
	fullIPv4 = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	fullIPv6 = regexp.MustCompile(`(?i)\b[0-9a-f]{1,4}(?::[0-9a-f]{0,4}){2,7}\b`)
)

func maskedSample(t *testing.T) (*Data, *Data, []string) {
	t.Helper()
	d := sampleData(t)
	// Un hallazgo más del mismo cliente (misma etiqueta) y otro con IPs y
	// alias en el resumen.
	d.Security.OpenCriticalHigh = append(d.Security.OpenCriticalHigh,
		secapi.FindingRow{ID: uuid.New(), Kind: "ddos_participation", Severity: "high", State: "open",
			Summary: "Ráfaga desde Ferretería Demo hacia 2001:db8:77::9 y 198.51.100.77", Address: "2001:db8::42",
			Alias: ptr("=HYPERLINK(\"http://x\")"), Site: "Nodo Norte", OpenedAt: d.Period.From, LastSeenAt: d.Period.From},
		secapi.FindingRow{ID: uuid.New(), Kind: "outbound_scanning", Severity: "high", State: "acknowledged",
			Summary: "Escaneo a 200 destinos", Address: "100.64.99.3", Site: "Nodo Sur", OpenedAt: d.Period.From, LastSeenAt: d.Period.From})
	secrets := []string{"Ferretería Demo", "HYPERLINK", "100.64.10.21", "2001:db8::42", "198.51.100.77", "2001:db8:77::9", "100.64.99.3"}
	return d, MaskData(d), secrets
}

func TestMaskDataPseudonyms(t *testing.T) {
	t.Parallel()
	d, m, _ := maskedSample(t)
	if d.Masked || !m.Masked || d.Security.TopCustomers[0].Address != "100.64.10.21" || *d.Security.TopCustomers[0].Alias != "Ferretería Demo" {
		t.Fatal("MaskData modificó el original")
	}
	tc, fr := m.Security.TopCustomers, m.Security.OpenCriticalHigh
	if *tc[0].Alias != "Cliente 1" || tc[0].Address != "100.64.10.x" || *tc[1].Alias != "Cliente 2" || tc[1].Address != "2001:db8:0…" {
		t.Fatalf("clientes = %+v / %+v", tc[0], tc[1])
	}
	// Mismo cliente → misma etiqueta en todas las secciones; uno nuevo → la siguiente.
	if *fr[0].Alias != "Cliente 1" || *fr[1].Alias != "Cliente 2" || *fr[2].Alias != "Cliente 3" || fr[2].Address != "100.64.99.x" {
		t.Fatalf("hallazgos = %q %q %q", *fr[0].Alias, *fr[1].Alias, *fr[2].Alias)
	}
	if got := fr[1].Summary; got != "Ráfaga desde Cliente 1 hacia 2001:db8:77… y 198.51.100.x" {
		t.Fatalf("resumen = %q", got)
	}
	// El resto del reporte queda igual.
	if m.TenantName != d.TenantName || m.Security.Totals != d.Security.Totals || m.Availability != d.Availability {
		t.Fatal("secciones sin datos personales alteradas")
	}
	// Sin datos de seguridad no hay nada que enmascarar.
	if n := MaskData(&Data{SecurityNote: "x"}); !n.Masked || n.Security != nil {
		t.Fatalf("sin seguridad = %+v", n)
	}
}

func TestMaskedCSVHasNoPersonalData(t *testing.T) {
	t.Parallel()
	_, m, secrets := maskedSample(t)
	var buf bytes.Buffer
	if err := RenderCSV(&buf, m); err != nil {
		t.Fatal(err)
	}
	files := unzip(t, buf.Bytes())
	var all strings.Builder
	for _, rows := range files {
		for _, r := range rows {
			all.WriteString(strings.Join(r, ";") + "\n")
		}
	}
	assertClean(t, "CSV", all.String(), secrets)
	cust := files["clientes_con_hallazgos.csv"]
	if cust[1][0] != "Cliente 1" || cust[1][1] != "100.64.10.x" {
		t.Fatalf("clientes = %v", cust)
	}
	sum := map[string]string{}
	for _, r := range files["resumen.csv"][1:] {
		sum[r[0]] = r[1]
	}
	if sum["datos_personales"] != "enmascarados" {
		t.Fatalf("resumen sin la marca de enmascarado: %v", sum)
	}
}

func TestMaskedPDFHasNoPersonalData(t *testing.T) {
	t.Parallel()
	full, m, secrets := maskedSample(t)
	text := func(d *Data) string {
		var buf bytes.Buffer
		if err := RenderPDF(&buf, d); err != nil {
			t.Fatal(err)
		}
		return pdfText(t, buf.Bytes())
	}
	// Control: el extractor ve los datos en la variante completa…
	if ft := text(full); !strings.Contains(ft, "100.64.10.21") || !strings.Contains(ft, "Ferretería Demo") {
		t.Fatal("el extractor de texto del PDF no ve los datos de la variante completa")
	}
	// …y no los ve en la enmascarada.
	mt := text(m)
	assertClean(t, "PDF", mt, secrets)
	if !strings.Contains(mt, "Cliente 1") || !strings.Contains(mt, "100.64.10.x") || !strings.Contains(mt, "Datos personales enmascarados") {
		t.Fatal("el PDF enmascarado no lleva etiquetas ni la nota")
	}
}

func assertClean(t *testing.T, what, s string, secrets []string) {
	t.Helper()
	for _, x := range secrets {
		if strings.Contains(s, x) {
			t.Errorf("%s enmascarado contiene %q", what, x)
		}
	}
	if ip := fullIPv4.FindString(s); ip != "" {
		t.Errorf("%s enmascarado contiene la IPv4 %q", what, ip)
	}
	// Una IPv6 enmascarada es el /48 seguido de "…"; cualquier otra cuenta como completa.
	for _, ix := range fullIPv6.FindAllStringIndex(s, -1) {
		if !strings.HasPrefix(s[ix[1]:], "…") {
			t.Errorf("%s enmascarado contiene la IPv6 %q", what, s[ix[0]:ix[1]])
		}
	}
}

// pdfText descomprime los flujos del PDF y devuelve el texto de los
// operadores de texto (cadenas literales en cp1252), concatenado.
func pdfText(t *testing.T, b []byte) string {
	t.Helper()
	var raw bytes.Buffer
	re := regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`)
	for _, m := range re.FindAllSubmatch(b, -1) {
		zr, err := zlib.NewReader(bytes.NewReader(m[1]))
		if err != nil {
			raw.Write(m[1])
			continue
		}
		data, _ := io.ReadAll(zr)
		_ = zr.Close()
		raw.Write(data)
	}
	lit := regexp.MustCompile(`\(((?:\\.|[^\\)])*)\)\s*Tj`)
	var out strings.Builder
	for _, m := range lit.FindAllSubmatch(raw.Bytes(), -1) {
		s := regexp.MustCompile(`\\(.)`).ReplaceAll(m[1], []byte("$1"))
		out.WriteString(cp1252(s))
		out.WriteByte('\n')
	}
	return out.String()
}

// cp1252 decodifica lo justo para las pruebas: ASCII, "…" y Latin-1 (á, é, í…).
func cp1252(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		switch {
		case c == 0x85:
			sb.WriteRune('…')
		case c < 0x80 || c >= 0xA0:
			sb.WriteRune(rune(c))
		default:
			sb.WriteRune('?')
		}
	}
	return sb.String()
}

func TestMaskedForPrincipals(t *testing.T) {
	t.Parallel()
	site := "site:" + uuid.NewString()
	mk := func(via string, perms map[string][]string) *authz.Principal {
		p := &authz.Principal{Type: authz.TypeUser, Scope: authz.ScopeTenant, Perms: perms}
		if via != "" {
			p.Type, p.Via = authz.TypeService, via
		}
		return p
	}
	for name, c := range map[string]struct {
		p      *authz.Principal
		masked bool
	}{
		"noc sin customers.read":       {mk("", map[string][]string{PermRead: {authz.ScopeAll}}), true},
		"analista con customers.read":  {mk("", map[string][]string{PermRead: {authz.ScopeAll}, PermCustomers: {authz.ScopeAll}}), false},
		"customers.read solo de nodo":  {mk("", map[string][]string{PermRead: {authz.ScopeAll}, PermCustomers: {site}}), true},
		"api token sin customers.read": {mk(authz.ViaAPIToken, map[string][]string{PermRead: {authz.ScopeAll}}), true},
		"api token con customers.read": {mk(authz.ViaAPIToken, map[string][]string{PermRead: {authz.ScopeAll}, PermCustomers: {authz.ScopeAll}}), false},
		"sin principal":                {nil, true},
	} {
		if got := MaskedFor(c.p); got != c.masked {
			t.Errorf("%s: MaskedFor = %v, quiero %v", name, got, c.masked)
		}
	}
}

func TestRunVariantSelection(t *testing.T) {
	t.Parallel()
	tenant, id := uuid.New(), uuid.New()
	r := &Run{ID: id, TenantID: tenant, Formats: []string{FormatPDF, FormatCSV}, PeriodFrom: time.Now(), PeriodTo: time.Now(), Files: []File{
		{Format: FormatPDF, ObjectKey: objectKey(tenant, id, FormatPDF, false), SizeBytes: 10},
		{Format: FormatCSV, ObjectKey: objectKey(tenant, id, FormatCSV, false), SizeBytes: 20},
		{Format: FormatPDF, ObjectKey: objectKey(tenant, id, FormatPDF, true), SizeBytes: 11, Masked: true},
		{Format: FormatCSV, ObjectKey: objectKey(tenant, id, FormatCSV, true), SizeBytes: 21, Masked: true},
	}}
	if f, ok := r.File(FormatPDF, true); !ok || !f.Masked || !strings.HasSuffix(f.ObjectKey, id.String()+"-masked.pdf") {
		t.Fatalf("pdf enmascarado = %+v", f)
	}
	if f, ok := r.File(FormatCSV, false); !ok || f.Masked || !strings.HasSuffix(f.ObjectKey, id.String()+"-csv.zip") {
		t.Fatalf("csv completo = %+v", f)
	}
	if r.SizeBytes() != 30 {
		t.Fatalf("size_bytes = %d (solo la variante completa)", r.SizeBytes())
	}
	full, masked := RunJSON(r, false), RunJSON(r, true)
	if full["personal_data_masked"] != false || full["size_bytes"] != int64(30) || masked["personal_data_masked"] != true || masked["size_bytes"] != int64(32) {
		t.Fatalf("RunJSON = %v / %v", full, masked)
	}
	// Ejecución anterior a la variante: sin archivo enmascarado (Download → 409, nunca la completa).
	old := &Run{Files: r.Files[:2]}
	if _, ok := old.File(FormatPDF, true); ok {
		t.Fatal("una ejecución antigua no tiene variante enmascarada")
	}
	// El evento solo lleva la variante completa (contrato ReportRun sin cambios).
	ev := runEvent(EventCompleted, r, time.Now())
	if files := ev.Data.(map[string]any)["files"].([]map[string]any); len(files) != 2 {
		t.Fatalf("evento con %d archivos", len(files))
	}
}

func TestNoticeMessageHasNoPersonalData(t *testing.T) {
	t.Parallel()
	d := sampleData(t)
	def := &Definition{Recipients: []string{"noc@isp.example"}, TenantSlug: "isp-demo"}
	r := &Run{ID: uuid.New(), PeriodFrom: d.Period.From, PeriodTo: d.Period.To, Timezone: d.Period.Loc.String(), Formats: Formats}
	m := NoticeMessage(def, r, d.TenantName, "https://horus.example.net")
	assertClean(t, "aviso", m.Subject+"\n"+m.Text, []string{"Ferretería Demo", "100.64.10.21", "2001:db8::42"})
}
