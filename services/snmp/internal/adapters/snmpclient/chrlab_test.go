//go:build chrlab

package snmpclient

// Prueba contra un RouterOS REAL: el CHR del laboratorio (infrastructure/lab/chr, `make lab-up`).
// No corre en CI: necesita el laboratorio levantado y la etiqueta `chrlab`.
//
//	HORUS_CHR_SNMP=10.255.3.17 HORUS_CHR_SECRETS=$PWD/.lab/secrets.env \
//	go test -tags chrlab -run TestCHRLab -v ./services/snmp/internal/adapters/snmpclient/
//
// Usa el transporte real (gosnmp, SNMPv3 authPriv SHA1+AES, como el script de alta) y el ciclo
// completo del adaptador, incluido un GET de 6 interfaces × 9 columnas (54 varbinds) con ifIndex
// inexistentes.

import (
	"context"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCHRLab(t *testing.T) {
	host, secrets := os.Getenv("HORUS_CHR_SNMP"), os.Getenv("HORUS_CHR_SECRETS")
	if host == "" || secrets == "" {
		t.Skip("HORUS_CHR_SNMP y HORUS_CHR_SECRETS no definidos (laboratorio CHR apagado)")
	}
	raw, err := os.ReadFile(secrets)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, l := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			env[k] = strings.TrimSpace(v)
		}
	}
	ctx := context.Background()
	d := GoSNMP{Timeout: 5 * time.Second, Retries: 1}
	tr, err := d.Dial(ctx, netip.MustParseAddr(host), Credential{Username: "horus-lab", AuthProtocol: "sha1",
		AuthPassword: env["SNMP_AUTH_PASS"], PrivProtocol: "aes128", PrivPassword: env["SNMP_PRIV_PASS"]})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()
	sys, err := ReadSystem(ctx, tr)
	if err != nil || sys.Model == nil || sys.UptimeTicks == nil {
		t.Fatalf("sistema: %+v %v", sys, err)
	}
	res, err := ReadResources(ctx, tr)
	if err != nil || len(res.CPULoads) == 0 || res.MemoryTotalBytes == nil {
		t.Fatalf("recursos: %+v %v", res, err)
	}
	h, err := ReadHealth(ctx, tr)
	if err != nil || h.Firmware == nil {
		t.Fatalf("salud: %+v %v", h, err)
	}
	disc, err := Discover(ctx, tr)
	if err != nil || len(disc) == 0 {
		t.Fatalf("descubrimiento: %+v %v", disc, err)
	}
	idx := []int{}
	for _, x := range disc {
		idx = append(idx, x.IfIndex)
	}
	for len(idx) < interfacesPerGet {
		idx = append(idx, 9000+len(idx)) // inexistentes: noSuchObject por varbind
	}
	ctrs, err := ReadCounters(ctx, tr, idx[:interfacesPerGet], time.Now(), *sys.UptimeTicks)
	if err != nil || len(ctrs) != interfacesPerGet {
		t.Fatalf("contadores (GET de %d varbinds): %+v %v", interfacesPerGet*countersPerInterface, ctrs, err)
	}
	t.Logf("RouterOS %s · %s · CPU %v · RAM %d/%d · %d interfaces · %+v", *h.Firmware, *sys.Model, res.CPULoads,
		*res.MemoryUsedBytes, *res.MemoryTotalBytes, len(disc), ctrs[0])
	// Credenciales incorrectas. Comportamiento REAL de RouterOS (7.12 y 7.23.8): solo la clave de
	// autenticación incorrecta recibe respuesta (Report) → ErrAuth (señal snmp = auth_failed). Con
	// la clave de cifrado incorrecta o un usuario desconocido el router descarta en silencio y el
	// resultado es un timeout, indistinguible de un router inalcanzable.
	wantAuth := map[string]bool{"auth": true}
	for name, c := range map[string]Credential{
		"auth":    {Username: "horus-lab", AuthProtocol: "sha1", AuthPassword: "claveincorrecta", PrivProtocol: "aes128", PrivPassword: env["SNMP_PRIV_PASS"]},
		"priv":    {Username: "horus-lab", AuthProtocol: "sha1", AuthPassword: env["SNMP_AUTH_PASS"], PrivProtocol: "aes128", PrivPassword: "claveincorrecta"},
		"usuario": {Username: "nadie", AuthProtocol: "sha1", AuthPassword: "claveincorrecta", PrivProtocol: "aes128", PrivPassword: "claveincorrecta"},
	} {
		bad, err := d.Dial(ctx, netip.MustParseAddr(host), c)
		if err == nil {
			_, err = ReadSystem(ctx, bad)
			_ = bad.Close()
		}
		t.Logf("credencial incorrecta (%s): %v", name, err)
		if err == nil || IsAuth(err) != wantAuth[name] {
			t.Errorf("credencial incorrecta (%s): ErrAuth esperado=%v, obtengo %v", name, wantAuth[name], err)
		}
	}
}
