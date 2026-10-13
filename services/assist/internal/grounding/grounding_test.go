package grounding

import (
	"strings"
	"testing"
)

const evidence = `{"tool":"get_finding","facts":["Hallazgo 0192f000-0000-7000-8000-0000000000f1: tipo c2_contact, severidad high, confianza 92 %.",
"Destino 185.220.101.4 puerto 443/tcp (outbound): 143 flujos, 12.4 MB (12400000 bytes), AS15169, reputación c2."],
"data":{"finding":{"confidence":0.92,"customer":{"address":"Cliente 1 (10.20.x.x)"}},"evidence_flows":{"top":[{"remote":"185.220.101.4","remote_port":443,"remote_asn":15169,"flows":143,"bytes":12400000}]}}}`

func full(body string) string {
	return "## Qué está pasando\n" + body + "\n## Posibles causas\n-\n## Qué hacer\n-\n## Para deshacer\n-\n## Fuentes\n- get_finding [F1]"
}

func TestVerifiedAnswer(t *testing.T) {
	t.Parallel()
	ans := full("El Cliente 1 (10.20.x.x) contactó 185.220.101.4 por el puerto 443 (AS15169) con 143 flujos y 12,4 MB; confianza 92 %.\n" +
		"```routeros\n/ip firewall address-list add list=horus-revisar address=10.20.1.5 comment=\"horus f1\" timeout=1d\n/ip firewall connection print count-only where src-address~\"10.20.1.5\"\n```")
	r := Check(ans, []string{evidence})
	if !r.OK() {
		t.Fatalf("report = %+v", r)
	}
}

func TestUnsupportedDataAndCommands(t *testing.T) {
	t.Parallel()
	ans := full("Contactó 203.0.113.9 por el puerto 6667 (AS64496), 9,8 GB y 55 %.\n```\n/ip firewall filter add chain=input action=accept\n/interface ethernet disable ether1\n/system resource print\n```")
	r := Check(ans, []string{evidence})
	if r.Status != Partial {
		t.Fatalf("status = %s (%+v)", r.Status, r)
	}
	want := []string{"203.0.113.9", "55 %", "9,8 GB", "AS64496", "puerto 6667"}
	if strings.Join(r.Unsupported, ",") != strings.Join(want, ",") {
		t.Fatalf("unsupported = %v, quiero %v", r.Unsupported, want)
	}
	if len(r.UnverifiedCommands) != 2 || !strings.HasPrefix(r.UnverifiedCommands[0], "/ip firewall filter add chain=input") {
		t.Fatalf("commands = %v", r.UnverifiedCommands)
	}
	msg := RepairMessage(r)
	if !strings.Contains(msg, "203.0.113.9") || !strings.Contains(msg, "routeros_commands") {
		t.Fatalf("repair = %s", msg)
	}
	if w := Warning(r); !strings.Contains(w, "AS64496") || !strings.Contains(w, "2 comando") {
		t.Fatalf("warning = %s", w)
	}
}

func TestMissingSectionsAndNoEvidence(t *testing.T) {
	t.Parallel()
	r := Check("## Qué está pasando\nnada", nil)
	if r.Status != Unverified || len(r.MissingSections) != 4 || r.OK() {
		t.Fatalf("report = %+v", r)
	}
}

func TestSmallCountsAndPercentFromRatio(t *testing.T) {
	t.Parallel()
	// "3 pasos", "2 clientes" son conteos de redacción; 92 % sale de confidence 0.92.
	r := Check(full("Sigue 3 pasos; afecta a 2 clientes; confianza 92 %; 1 destino."), []string{`{"confidence":0.92}`})
	if len(r.Unsupported) != 0 {
		t.Fatalf("unsupported = %v", r.Unsupported)
	}
}

func TestNumberVariants(t *testing.T) {
	t.Parallel()
	set := numberSet("bytes 12400000 share 0.153 flows 1.234")
	for _, n := range []string{"12400000", "12.400.000", "12,400,000", "1234", "1.234", "15.3", "15,3"} {
		if !numberSupported(set, n, true) {
			t.Errorf("%s debería estar respaldado", n)
		}
	}
}
