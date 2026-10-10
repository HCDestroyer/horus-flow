package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestMaskerMasksClientAddressesOnly(t *testing.T) {
	t.Parallel()
	m := newMasker([]netip.Prefix{netip.MustParsePrefix("10.255.0.0/16"), netip.MustParsePrefix("172.31.250.0/24")})
	in := `{"ts":"2026-10-07T22:31:04.512Z","msg":"x","client":"100.64.12.7","dst":"203.0.113.9:443","v6":"2001:db8:12::7",` +
		`"pfx":"10.20.0.0/24","exporter":"10.255.3.17","app":"172.31.250.5:8081","lo":"127.0.0.1","mac":"aa:bb:cc:dd:ee:ff",` +
		`"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","id":"0192f1c2-7d1e-7c3a-9e55-1f2a3b4c5d6e","ver":"1.2.3","time":"22:31:04",` +
		`"email":"noc@isp.example","auth":"Bearer abcdefghijklmnop.qrs","password":"hunter22","again":"100.64.12.7"}`
	out := m.Text(in)
	for _, leak := range []string{"100.64.12.7", "203.0.113.9", "2001:db8:12::7", "10.20.0.0", "noc@isp.example", "abcdefghijklmnop", "hunter22"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q in %s", leak, out)
		}
	}
	for _, kept := range []string{"10.255.3.17", "172.31.250.5:8081", "127.0.0.1", "aa:bb:cc:dd:ee:ff", "4bf92f3577b34da6a3ce929d0e0e4736",
		"0192f1c2-7d1e-7c3a-9e55-1f2a3b4c5d6e", `"1.2.3"`, "22:31:04.512Z", `"22:31:04"`, ":443", "/24"} {
		if !strings.Contains(out, kept) {
			t.Errorf("lost %q in %s", kept, out)
		}
	}
	// Seudónimo estable: la misma IP, el mismo valor.
	pseudo := regexp.MustCompile(`\[ip:[0-9a-f]{8}\]`).FindAllString(out, -1)
	if len(pseudo) < 4 {
		t.Fatalf("pseudonyms: %v", pseudo)
	}
	var client, again string
	var doc map[string]string
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("masked JSON no longer valid: %v", err)
	}
	client, again = doc["client"], doc["again"]
	if client != again || !strings.HasPrefix(client, "[ip:") {
		t.Fatalf("pseudonym not stable: %q %q", client, again)
	}
}

// ipRe encuentra cualquier IPv4 en el paquete para el test de privacidad.
var ipRe = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)

// D23: `horus diagnose` produce el paquete sin datos de clientes aunque los
// logs que recoge los contengan (p. ej. un log de un componente de terceros).
func TestDiagnoseBundleHasNoClientData(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o750); err != nil {
		t.Fatal(err)
	}
	clientIPs := []string{"100.64.1.10", "198.51.100.23", "45.33.32.156"}
	var content strings.Builder
	for _, ip := range clientIPs {
		content.WriteString(`{"level":"warn","msg":"flow from client","client_ip":"` + ip + `","exporter":"10.255.0.9","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}` + "\n")
	}
	content.WriteString("traefik 2001:db8::1 - - GET /api/v1/customers/search?q=203.0.113.77 Authorization: Bearer secret-token-value\n")
	if err := os.WriteFile(filepath.Join(logs, "journal.log"), []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "bundle.tar.gz")
	env := []string{"HORUS_WG_TUNNEL_CIDRS=10.255.0.0/16", "HORUS_PROCESS=horus-app", "HORUS_POSTGRES_PASSWORD=pg-secret-value",
		"HORUS_SEED_ADMIN_EMAIL=root@isp.example"}
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"diagnose", "--output=" + out, "--docker=off", "--logs-dir=" + logs, "--admin-urls=",
		"--timeout=20s"}, env, io.Discard, &stderr, roleCatalog)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	files := readBundle(t, out)
	for _, want := range []string{"manifest.json", "README.txt", "config.json", "versions.json", "extra/journal.log"} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %s (have %v)", want, keys(files))
		}
	}
	for name, data := range files {
		s := string(data)
		for _, ip := range append(clientIPs, "203.0.113.77", "2001:db8::1") {
			if strings.Contains(s, ip) {
				t.Errorf("%s: client address %s leaked", name, ip)
			}
		}
		for _, secret := range []string{"pg-secret-value", "secret-token-value", "root@isp.example"} {
			if strings.Contains(s, secret) {
				t.Errorf("%s: secret %q leaked", name, secret)
			}
		}
		for _, ip := range ipRe.FindAllString(s, -1) {
			a, err := netip.ParseAddr(ip)
			if err == nil && !a.IsLoopback() && !netip.MustParsePrefix("10.255.0.0/16").Contains(a) && !strings.HasPrefix(name, "manifest") {
				// Solo pueden quedar direcciones de infraestructura (interfaces locales del host de test incluidas).
				if !localAddr(a) {
					t.Errorf("%s: unexpected address %s", name, ip)
				}
			}
		}
	}
	if !strings.Contains(string(files["extra/journal.log"]), "10.255.0.9") {
		t.Error("infrastructure (tunnel) address was masked")
	}
	var manifest struct {
		FormatVersion int               `json:"format_version"`
		Sections      map[string]string `json:"sections"`
		Masking       struct {
			Count int `json:"client_addresses_masked"`
		} `json:"masking"`
	}
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != DiagnoseFormatVersion || manifest.Sections["logs_dir"] != "ok" || manifest.Masking.Count < 5 ||
		!strings.Contains(manifest.Sections["postgres"], "not configured") {
		t.Fatalf("manifest = %+v", manifest)
	}
}

func localAddr(a netip.Addr) bool {
	for _, p := range infraPrefixes(nil, nil) {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func readBundle(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		out[strings.TrimPrefix(h.Name, "horus-diagnose/")] = b
	}
	return out
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
