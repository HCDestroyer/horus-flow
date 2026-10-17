package domain

import (
	"bytes"
	"strings"
	"testing"
)

func TestConnectionCodeHashOnly(t *testing.T) {
	t.Parallel()
	a, err := GenerateConnectionCode()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := GenerateConnectionCode()
	if a == b || !LooksLikeConnectionCode(a) || len(a) != ConnectionCodeChars {
		t.Fatalf("códigos %q %q", a, b)
	}
	h := HashConnectionCode(a)
	if len(h) != 32 || bytes.Contains(h, []byte(a)) || bytes.Equal(h, HashConnectionCode(b)) {
		t.Fatal("solo el SHA-256 del código, distinto por código")
	}
	for _, bad := range []string{"", "corto", strings.Repeat("A", 42), strings.Repeat("*", 43)} {
		if LooksLikeConnectionCode(bad) {
			t.Errorf("%q no es un código", bad)
		}
	}
}

func TestParseConnectionLink(t *testing.T) {
	t.Parallel()
	code, _ := GenerateConnectionCode()
	ok := map[string]string{
		"https://panel.isp.example/conectar#" + code:                                    "https://panel.isp.example",
		"https://panel.isp.example/conectar/#" + code:                                   "https://panel.isp.example",
		"https://panel.isp.example/conectar?api=https%3A%2F%2Fapi.isp.example#" + code:  "https://api.isp.example",
		"https://panel.isp.example/conectar?api=https://api.isp.example/api/v1#" + code: "https://api.isp.example",
	}
	for raw, origin := range ok {
		got, valid := ParseConnectionLink(raw, false)
		if !valid || got.Code != code || got.APIOrigin != origin {
			t.Errorf("%s → %+v %v", raw, got, valid)
		}
	}
	bad := []string{
		"http://panel.isp.example/conectar#" + code,              // http sin laboratorio
		"https://user:pw@panel.isp.example/conectar#" + code,     // credenciales
		"https://panel.isp.example/otra#" + code,                 // otra ruta
		"https://panel.isp.example/conectar?codigo=" + code,      // código en la consulta
		"https://panel.isp.example/conectar?x=1#" + code,         // parámetros desconocidos
		"https://panel.isp.example/conectar?api=http://api.x#" + code,
		"https://panel.isp.example/conectar?api=https://api.x/otra#" + code,
		"https://panel.isp.example/conectar#corto",
		"javascript:alert(1)",
		"",
	}
	for _, raw := range bad {
		if _, valid := ParseConnectionLink(raw, false); valid {
			t.Errorf("%q debía rechazarse", raw)
		}
	}
	if got, valid := ParseConnectionLink("http://10.0.0.5:3000/conectar#"+code, true); !valid || got.APIOrigin != "http://10.0.0.5:3000" {
		t.Fatalf("laboratorio: %+v %v", got, valid)
	}
}

func TestParseOriginAndSSOEntryURL(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]string{"https://argos.example": "https://argos.example", "https://argos.example/": "https://argos.example",
		"https://argos.example:8443/api/v1": "https://argos.example:8443"} {
		if got, ok := ParseOrigin(raw, false); !ok || got != want {
			t.Errorf("%s → %s %v", raw, got, ok)
		}
	}
	for _, raw := range []string{"http://argos.example", "https://argos.example/otra", "https://a@argos.example", "https://argos.example?x=1", "argos.example"} {
		if _, ok := ParseOrigin(raw, false); ok {
			t.Errorf("%s debía rechazarse", raw)
		}
	}
	ticket := strings.Repeat("t", 43)
	if !ValidSSOEntryURL("https://panel.isp.example/sso/entrar#"+ticket, false) {
		t.Fatal("URL válida rechazada")
	}
	for _, raw := range []string{
		"https://panel.isp.example/sso/entrar?ticket=" + ticket, // ticket en la consulta (queda en registros)
		"https://panel.isp.example/otra#" + ticket,
		"http://panel.isp.example/sso/entrar#" + ticket,
		"javascript:alert(1)//sso/entrar#" + ticket,
		"https://u:p@panel.isp.example/sso/entrar#" + ticket,
		"https://panel.isp.example/sso/entrar#corto",
	} {
		if ValidSSOEntryURL(raw, false) {
			t.Errorf("%s debía rechazarse", raw)
		}
	}
}
