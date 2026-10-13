package mask

import (
	"encoding/json"
	"strings"
	"testing"
)

func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func encode(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCoarse(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"10.20.30.40":      "10.20.x.x",
		"::ffff:10.1.2.3":  "10.1.x.x",
		"2001:db8:1:2::5":  "2001:db8:x:x::",
		"10.20.0.•••":      "10.20.x.x",
		"2001:db8:abcd::•": "2001:db8:x:x",
	}
	for in, want := range cases {
		if got := Coarse(in); got != want {
			t.Errorf("Coarse(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCustomerIPStableAndResolvable(t *testing.T) {
	t.Parallel()
	m := New()
	a := m.CustomerIP("10.20.30.40")
	b := m.CustomerIP("10.20.30.41")
	if a != "Cliente 1 (10.20.x.x)" || b != "Cliente 2 (10.20.x.x)" {
		t.Fatalf("seudónimos = %q, %q", a, b)
	}
	if again := m.CustomerIP("10.20.30.40/32"); again != a {
		t.Fatalf("la misma IP (como /32) debe dar el mismo seudónimo: %q", again)
	}
	if ip, ok := m.Resolve("cliente 2"); !ok || ip != "10.20.30.41" {
		t.Fatalf("Resolve = %q %v", ip, ok)
	}
	if ip, ok := m.Resolve("Cliente 1 (10.20.x.x)"); !ok || ip != "10.20.30.40" {
		t.Fatalf("Resolve con red = %q %v", ip, ok)
	}
	if _, ok := m.Resolve("Cliente 9"); ok {
		t.Fatal("un seudónimo desconocido no se resuelve")
	}
	if m.Count() != 2 {
		t.Fatalf("Count = %d", m.Count())
	}
}

func TestValueMixedFinding(t *testing.T) {
	t.Parallel()
	m := New()
	in := decode(t, `{
		"id": "f1", "kind": "c2_contact", "tenant_id": "t1",
		"customer": {"customer_id": "c1", "address": "100.70.1.25", "alias": "Juan Pérez", "name": "Casa Juan",
			"kind": "residential", "note": "debe 2 meses"},
		"summary": {"text": "Contacto con C2 desde 100.70.1.25 hacia 203.0.113.9:443"},
		"primary_target": {"type": "remote_ip", "value": "203.0.113.9"},
		"recommended_actions": [{"routeros": {"commands": ["/ip firewall address-list add list=x address=customer_address"],
			"rendered_commands": ["/ip firewall address-list add list=x address=100.70.1.25"]}}],
		"acknowledged_by": "u1", "email": "juan@example.com"
	}`)
	out := encode(t, m.Value(in, Mixed))
	for _, leak := range []string{"100.70.1.25", "Juan", "Casa", "debe 2 meses", "juan@example.com", "rendered_commands", "acknowledged_by"} {
		if strings.Contains(out, leak) {
			t.Errorf("fuga de %q en %s", leak, out)
		}
	}
	for _, keep := range []string{"Cliente 1 (100.70.x.x)", "203.0.113.9", "c2_contact", "customer_address", "residential", `"customer_id":"c1"`} {
		if !strings.Contains(out, keep) {
			t.Errorf("falta %q en %s", keep, out)
		}
	}
	// No modifica la entrada.
	if !strings.Contains(encode(t, in), "Juan Pérez") {
		t.Error("Value modificó la entrada")
	}
}

func TestValueCustomerScopeMasksAnyIP(t *testing.T) {
	t.Parallel()
	m := New()
	in := decode(t, `{"id": "c1", "address": "192.168.5.7", "label": "Oficina", "description": "router en 192.168.5.1",
		"aliases": [{"alias": "x"}], "kind": "commercial", "detail": {"notes": "n", "seen_from": "192.168.5.7"}}`)
	out := encode(t, m.Value(in, Customer))
	for _, leak := range []string{"192.168.5.7", "192.168.5.1", "Oficina", "aliases", `"notes"`} {
		if strings.Contains(out, leak) {
			t.Errorf("fuga de %q en %s", leak, out)
		}
	}
	if !strings.Contains(out, "Cliente 1 (192.168.x.x)") || !strings.Contains(out, "commercial") {
		t.Errorf("salida = %s", out)
	}
}

func TestValueInfraKeepsTunnelAddresses(t *testing.T) {
	t.Parallel()
	m := New()
	in := decode(t, `{"router_id": "r1", "address": "10.255.3.17/32", "exporter_ip": "10.255.3.17", "public_key": "abc=",
		"state": "silent", "endpoint": "198.51.100.4:13231"}`)
	out := encode(t, m.Value(in, Infra))
	if !strings.Contains(out, "10.255.3.17/32") || !strings.Contains(out, "silent") || strings.Contains(out, "abc=") {
		t.Errorf("salida = %s", out)
	}
	if m.Count() != 0 {
		t.Errorf("Infra no debe seudonimizar: %d", m.Count())
	}
}

func TestKnownIPsReplacedEverywhere(t *testing.T) {
	t.Parallel()
	m := New()
	_ = m.Value(decode(t, `{"customer": {"address": "100.70.1.25"}}`), Mixed)
	// Una IP de cliente ya conocida que aparece luego en otro documento (incluso Infra) se reemplaza.
	out := encode(t, m.Value(decode(t, `{"note_free": "flujo interno 100.70.1.25 -> 8.8.8.8", "x": ["100.70.1.25"]}`), Infra))
	if strings.Contains(out, "100.70.1.25") || !strings.Contains(out, "8.8.8.8") {
		t.Errorf("salida = %s", out)
	}
	if got := m.Text("El cliente 100.70.1.25 hizo 1200 conexiones"); strings.Contains(got, "100.70.1.25") {
		t.Errorf("Text = %q", got)
	}
}

func TestMaskedAddressFromHorus(t *testing.T) {
	t.Parallel()
	m := New()
	out := encode(t, m.Value(decode(t, `{"customer": {"address": "10.20.0.•••", "address_masked": true}}`), Mixed))
	if !strings.Contains(out, "Cliente 1 (10.20.x.x)") {
		t.Errorf("salida = %s", out)
	}
}
