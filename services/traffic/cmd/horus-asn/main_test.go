package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no se encontró la raíz del repositorio")
		}
		dir = parent
	}
}

func runCLI(t *testing.T, now time.Time, args ...string) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), args, &out, &errb, func() time.Time { return now })
	return code, out.String() + errb.String()
}

// TestEndToEndWithFixtures: consolidación BGP > iptoasn > RIR + PeeringDB,
// control de diff > 5 % y conservación ante fuente corrupta, sin Internet.
func TestEndToEndWithFixtures(t *testing.T) {
	root := repoRoot(t)
	cfg := filepath.Join(root, "tests", "fixtures", "datasets", "datasets.yaml")
	fx := t.TempDir()
	src := filepath.Join(root, "tests", "fixtures", "datasets")
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fx, e.Name()), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data := t.TempDir()
	now := time.Date(2026, 10, 8, 4, 0, 0, 0, time.UTC)
	base := []string{"-config", cfg, "-data-dir", data, "-fixtures", fx}

	// 1. Arranque solo con iptoasn.
	code, out := runCLI(t, now, append([]string{"sync", "-only", "iptoasn-v4,iptoasn-v6"}, base...)...)
	if code != 0 || !strings.Contains(out, "snapshot IP→ASN v1") || strings.Contains(out, "ris-rrc00") {
		t.Fatalf("sync iptoasn: %d\n%s", code, out)
	}
	code, out = runCLI(t, now, "lookup", "-data-dir", data, "192.0.2.1")
	if code != 0 || !strings.Contains(out, "AS64496 país=ES") || !strings.Contains(out, "fuente=iptoasn-v4") {
		t.Fatalf("lookup v1:\n%s", out)
	}

	// 2. Con BGP, RIR y PeeringDB (aprobadas para uso comercial, D20; sin
	// -allow-unverified) el cambio supera el 5 %: no se publica. CAIDA
	// sigue sin descargarse.
	now = now.Add(time.Hour)
	code, out = runCLI(t, now, append([]string{"sync"}, base...)...)
	if code != 3 || !strings.Contains(out, "caida-as2org  skipped") || !strings.Contains(out, "requiere revisión") || strings.Contains(out, "snapshot IP→ASN v2") {
		t.Fatalf("sync con diff grande: %d\n%s", code, out)
	}
	// 3. Aceptado explícitamente: se publica v2 con la consolidación.
	code, out = runCLI(t, now, append([]string{"build", "-accept-large-diff"}, base...)...)
	if code != 0 || !strings.Contains(out, "snapshot IP→ASN v2") {
		t.Fatalf("build aceptado: %d\n%s", code, out)
	}
	code, out = runCLI(t, now, "lookup", "-data-dir", data, "192.0.2.1", "198.51.100.10", "198.51.100.100", "2001:db8::1", "8.8.8.8")
	for _, want := range []string{
		"192.0.2.1\t192.0.2.0/24\tAS64500 país=ES org=\"Example BGP Origin\" tipo=enterprise fuente=ris-rrc00", // BGP gana a iptoasn; país del RIR
		"198.51.100.10\t198.51.100.0/26\tAS64501", // más específico de BGP
		"198.51.100.100\t198.51.100.0/25\tAS64497 país=MX org=\"Example Content\" tipo=content fuente=iptoasn-v4",
		"2001:db8::1\t2001:db8::/48\tAS64499 país=DE",
		"8.8.8.8\tsin atribución",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lookup v2 sin %q:\n%s", want, out)
		}
	}
	if code != 0 {
		t.Fatalf("lookup v2: %d", code)
	}

	// 4. RIS llega truncado: se rechaza, se conserva el anterior y el
	// snapshot reconstruido no cambia (diff 0 %).
	now = now.Add(time.Hour)
	trunc, err := os.ReadFile(filepath.Join(src, "corrupt", "ris-truncated.mrt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(fx, "ris-rrc00.mrt.gz")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fx, "ris-rrc00.mrt"), trunc, 0o600); err != nil {
		t.Fatal(err)
	}
	code, out = runCLI(t, now, append([]string{"sync"}, base...)...)
	if code != 1 || !strings.Contains(out, "ris-rrc00     failed") || !strings.Contains(out, "(0.0 %)") || !strings.Contains(out, "snapshot IP→ASN v3") {
		t.Fatalf("sync con RIS truncado: %d\n%s", code, out)
	}
	code, out = runCLI(t, now, "status", "-config", cfg, "-data-dir", data)
	if code != 0 || !strings.Contains(out, `horus_dataset_consecutive_failures{kind="asn",source="ris-rrc00"} 1`) ||
		!strings.Contains(out, `horus_dataset_age_seconds{kind="asn",source="ris-rrc00"} 3600`) ||
		strings.Contains(out, "caida-as2org") {
		t.Fatalf("status:\n%s", out)
	}
}

func TestEmbeddedConfig(t *testing.T) {
	code, out := runCLI(t, time.Now(), "sources")
	if code != 0 || !strings.Contains(out, "iptoasn-v4") || !strings.Contains(out, "PDDL-1.0") || !strings.Contains(out, "caida-as2org") {
		t.Fatalf("sources: %d\n%s", code, out)
	}
	if code, _ := runCLI(t, time.Now(), "build", "-data-dir", t.TempDir()); code != 1 {
		t.Fatal("build sin datasets debería fallar")
	}
	if code, _ := runCLI(t, time.Now()); code != 2 {
		t.Fatal("sin subcomando: 2")
	}
}
