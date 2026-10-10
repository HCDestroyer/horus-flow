package script

import (
	"errors"
	"flag"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "regenera los golden de testdata/")

const testCA = `-----BEGIN CERTIFICATE-----
MIIBfzCCASWgAwIBAgIUTESTTESTTESTTESTTESTTESTTESTwCgYIKoZIzj0EAwIw
FTETMBEGA1UEAwwKMjAzLjAuMTEzLjEwMB4XDTI2MTAwOTAwMDAwMFoXDTM2MTAw
-----END CERTIFICATE-----`

func values(tpl string) Values {
	return Values{
		Template: tpl, RouterID: "0192e333-0000-7000-8000-000000000033", RouterName: "rt-centro \"1\"$x",
		RouterWGIP: netip.MustParseAddr("10.255.3.17"), HubPublicKey: "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=",
		WGEndpoint: "horus.example.net", WGPort: 51820, ServicesCIDR: netip.MustParsePrefix("10.255.0.0/24"),
		CollectorIP: netip.MustParseAddr("10.255.0.1"), APIUser: "horus", APIPassword: "Api9pass9word9Api9pass9w",
		SNMPUser: "horus-00000033", SNMPAuthPass: "Auth9pass9word9Auth9pass", SNMPPrivPass: "Priv9pass9word9Priv9pass",
		NTPServer: "pool.ntp.org", EnrollURL: "https://horus.example.net/api/v1/enroll/wireguard",
		Token: "q1w2e3r4t5y6u7i8o9p0a1s2d3f4g5h6j7k8l9z0x1c", CacheEntries: "256k", AccessMode: "domain",
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (ejecute go test -update)", err)
	}
	if string(want) != got {
		t.Fatalf("%s distinto del golden; diferencias:\n%s", name, diff(string(want), got))
	}
}

func diff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	var out []string
	for i := range max(len(al), len(bl)) {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			out = append(out, "- "+x, "+ "+y)
		}
	}
	return strings.Join(out, "\n")
}

var placeholderRe = regexp.MustCompile(`<[A-Z_]+>|<…>|\{\{`)

func checkCommon(t *testing.T, s string) {
	t.Helper()
	if m := placeholderRe.FindString(s); m != "" {
		t.Fatalf("placeholder sin resolver: %q", m)
	}
	for _, must := range []string{
		"persistent-keepalive=25s", "check-certificate=yes", "nat-dst-address=yes", "nat-src-address=yes",
		"version=ipfix", "packet-sampling=no", "security=private", "group=horus-ro", "!write", "!sensitive",
		`"Content-Type: application/json"`, "/api/v1/enroll/wireguard", "src-address=10.255.3.17",
		"allowed-address=10.255.0.0/24", "address=10.255.3.17/32",
	} {
		if !strings.Contains(s, must) {
			t.Errorf("falta %q", must)
		}
	}
	if strings.Contains(s, "check-certificate=no") || strings.Contains(s, "private-key=") {
		t.Fatal("el script desactiva la verificación TLS o lleva una clave privada")
	}
	// Todo lo que crea lleva comment="horus", salvo el destino de Traffic Flow:
	// `/ip traffic-flow target add` no admite comment en el router real del PO
	// (I1-27); el script inverso lo encuentra por destino, puerto y origen.
	if strings.Contains(s, "/ip traffic-flow target add") && strings.Contains(s[strings.Index(s, "/ip traffic-flow target add"):strings.Index(s, "/ip traffic-flow ipfix set")], "comment=") {
		t.Error("/ip traffic-flow target add no debe llevar comment (falla en RouterOS real)")
	}
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "/") && strings.Contains(l, " add ") && !strings.HasPrefix(l, "/file add") && !strings.HasPrefix(l, "/certificate add") &&
			!strings.HasPrefix(l, "/ip traffic-flow target add") {
			if !strings.Contains(l+continuation(s, l), `comment="horus`) {
				t.Errorf("línea sin comment=\"horus\": %s", l)
			}
		}
	}
}

// continuation devuelve las líneas de continuación (\) de l.
func continuation(s, l string) string {
	i := strings.Index(s, l)
	rest := s[i:]
	var b strings.Builder
	for _, x := range strings.Split(rest, "\n") {
		b.WriteString(x)
		if !strings.HasSuffix(strings.TrimSpace(x), `\`) {
			break
		}
	}
	return b.String()
}

func TestOnboardingGolden(t *testing.T) {
	t.Parallel()
	for _, tpl := range []string{Template712, TemplateLongTerm} {
		s, err := Onboarding(values(tpl))
		if err != nil {
			t.Fatal(err)
		}
		checkCommon(t, s)
		if strings.Contains(s, "horus-ca") {
			t.Fatal("certificado importado con TLS de confianza pública")
		}
		if !strings.Contains(s, `# Router: rt-centro _1__x`) {
			t.Fatal("nombre del router sin sanear")
		}
		golden(t, "onboarding-"+tpl+".rsc", s)
	}
}

// D19: con TLS autofirmado (modo ip_only) el script importa el certificado
// público de la instalación y mantiene check-certificate=yes.
func TestOnboardingIPOnlyImportsCertificate(t *testing.T) {
	t.Parallel()
	v := values(Template712)
	v.AccessMode, v.CACertPEM, v.EnrollURL, v.WGEndpoint = "ip_only", testCA, "https://203.0.113.10/api/v1/enroll/wireguard", "203.0.113.10"
	s, err := Onboarding(v)
	if err != nil {
		t.Fatal(err)
	}
	checkCommon(t, s)
	if !strings.Contains(s, `/certificate import file-name=horus-ca.crt`) || !strings.Contains(s, `trusted=yes`) ||
		strings.Index(s, "/certificate import") > strings.Index(s, "    /tool fetch url=") {
		t.Fatal("el certificado no se importa antes del fetch")
	}
	if strings.Count(s, "\n-----") != 0 || !strings.Contains(s, `\n-----END CERTIFICATE-----\n"`) {
		t.Fatal("PEM no escapado en una sola cadena RouterOS")
	}
	golden(t, "onboarding-7.12-ip_only.rsc", s)
}

func TestDeprovisioningGolden(t *testing.T) {
	t.Parallel()
	s, err := Deprovisioning(DeprovisionValues{RouterID: "0192e333-0000-7000-8000-000000000033", RouterName: "rt-centro",
		RouterWGIP: netip.MustParseAddr("10.255.3.17"), CollectorIP: netip.MustParseAddr("10.255.0.1"), SNMPUser: "horus-00000033"})
	if err != nil {
		t.Fatal(err)
	}
	if placeholderRe.MatchString(s) {
		t.Fatal("placeholder sin resolver")
	}
	for _, l := range strings.Split(s, "\n") {
		// El destino de Traffic Flow no lleva comment: se limita por destino, puerto y origen.
		scoped := strings.Contains(l, "horus") || strings.Contains(l, "dst-address=10.255.0.1 && port=4739 && src-address=10.255.3.17")
		if strings.Contains(l, " remove ") && !scoped {
			t.Errorf("borrado que no se limita a lo de Horus: %s", l)
		}
	}
	if !strings.Contains(s, "dst-address=10.255.0.1 && port=4739") {
		t.Fatal("el target de Horus no se identifica")
	}
	golden(t, "deprovisioning.rsc", s)
}

func TestValidationRejectsInjection(t *testing.T) {
	t.Parallel()
	for name, mut := range map[string]func(*Values){
		"password":  func(v *Values) { v.APIPassword = `abc"; /system reset-configuration;"` },
		"ntp":       func(v *Values) { v.NTPServer = "pool.ntp.org; /user add" },
		"endpoint":  func(v *Values) { v.WGEndpoint = "a b" },
		"token":     func(v *Values) { v.Token = `x"` },
		"url":       func(v *Values) { v.EnrollURL = "http://horus.example.net/api/v1/enroll/wireguard" },
		"collector": func(v *Values) { v.CollectorIP = netip.MustParseAddr("192.0.2.1") },
		"template":  func(v *Values) { v.Template = "6.49" },
	} {
		v := values(Template712)
		mut(&v)
		if _, err := Onboarding(v); err == nil {
			t.Errorf("%s: valor peligroso aceptado", name)
		}
	}
}

func TestTemplateFor(t *testing.T) {
	t.Parallel()
	for v, want := range map[string]string{"": Template712, "7.12": Template712, "7.16.1": Template712, "7.18": TemplateLongTerm, "7.20.2": TemplateLongTerm} {
		if got, err := TemplateFor(v); err != nil || got != want {
			t.Errorf("%s → %s, %v", v, got, err)
		}
	}
	for _, v := range []string{"7.11", "7.1", "6.49.10", "8.0"} {
		if _, err := TemplateFor(v); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s aceptada", v)
		}
	}
}
