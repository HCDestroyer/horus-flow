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

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
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
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func runCLI(t *testing.T, c *clock, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), args, &out, &errb, c.now)
	return code, out.String(), errb.String()
}

// TestEndToEndWithFixtures recorre el ciclo completo sin Internet: descarga
// desde fixtures, snapshot, consulta, fuente caída y fuente corrupta.
func TestEndToEndWithFixtures(t *testing.T) {
	root := repoRoot(t)
	cfg := filepath.Join(root, "tests", "fixtures", "feeds", "feeds.yaml")
	fx := t.TempDir()
	copyDir(t, filepath.Join(root, "tests", "fixtures", "feeds"), fx)
	data := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 8, 4, 0, 0, 0, time.UTC)}
	base := []string{"-config", cfg, "-data-dir", data, "-fixtures", fx}

	// Las fuentes aprobadas en D20 ("yes") se procesan sin --allow-unverified;
	// la fuente "no" (FireHOL) nunca se descarga, ni con --allow-unverified.
	code, out, errOut := runCLI(t, c, append([]string{"sync"}, base...)...)
	if code != 0 {
		t.Fatalf("sync: code=%d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "firehol-level1     skipped") || !strings.Contains(out, "snapshot de reputación v1") ||
		strings.Contains(out, "sin verificar") {
		t.Fatalf("sync:\n%s", out)
	}
	if code, out, _ := runCLI(t, c, append([]string{"fetch", "-allow-unverified", "-only", "firehol-level1"}, base...)...); code != 0 ||
		!strings.Contains(out, "licencia sin uso comercial permitido") {
		t.Fatalf("firehol con -allow-unverified: %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(data, "datasets", "firehol-level1")); !os.IsNotExist(err) {
		t.Fatal("se escribió la fuente sin uso comercial")
	}

	code, out, _ = runCLI(t, c, "lookup", "-data-dir", data, "192.0.2.10", "198.51.100.99")
	if code != 0 || !strings.Contains(out, "fuente=abusech-feodo categoría=botnet_cc confianza=90 fecha=2026-09-01 puerto=443") ||
		!strings.Contains(out, "198.51.100.99\t198.51.100.0/25\tfuente=spamhaus-drop-v4 categoría=blocklist") {
		t.Fatalf("lookup:\n%s", out)
	}

	// Segunda ronda: feodo cae (sin fixture) y threatfox llega corrupto.
	c.t = c.t.Add(2 * time.Hour)
	if err := os.Remove(filepath.Join(fx, "abusech-feodo.csv")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fx, "abusech-threatfox.csv"), []byte("<html>502 Bad Gateway</html>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runCLI(t, c, append([]string{"sync"}, base...)...)
	if code != 1 || !strings.Contains(out, "abusech-feodo      failed") || !strings.Contains(out, "abusech-threatfox  failed") ||
		!strings.Contains(out, "snapshot de reputación v2") {
		t.Fatalf("sync con fallos: code=%d\n%s", code, out)
	}
	// El snapshot v2 sigue teniendo los C2 de la última versión válida.
	code, out, _ = runCLI(t, c, "lookup", "-data-dir", data, "192.0.2.10", "198.51.100.50")
	if code != 0 || !strings.Contains(out, "snapshot v2") || !strings.Contains(out, "fuente=abusech-feodo") ||
		!strings.Contains(out, "fuente=abusech-threatfox categoría=botnet_cc confianza=75") {
		t.Fatalf("lookup tras fallos:\n%s", out)
	}
	// Métricas: antigüedad de 2 h y fallos consecutivos para las fuentes caídas.
	code, out, _ = runCLI(t, c, "status", "-config", cfg, "-data-dir", data)
	if code != 0 || !strings.Contains(out, `horus_dataset_age_seconds{kind="reputation",source="abusech-feodo"} 7200`) ||
		!strings.Contains(out, `horus_dataset_consecutive_failures{kind="reputation",source="abusech-threatfox"} 1`) ||
		!strings.Contains(out, `horus_dataset_age_seconds{kind="reputation",source="tor-exit"} 0`) {
		t.Fatalf("status:\n%s", out)
	}
}

func TestDefaultConfigAndUsage(t *testing.T) {
	c := &clock{t: time.Now()}
	code, out, _ := runCLI(t, c, "sources")
	if code != 0 || !strings.Contains(out, "abusech-feodo") || !strings.Contains(out, "firehol-level1") {
		t.Fatalf("sources con la configuración embebida: %d\n%s", code, out)
	}
	if code, _, _ := runCLI(t, c); code != 2 {
		t.Fatal("sin subcomando debería salir con 2")
	}
	if code, _, _ := runCLI(t, c, "nope"); code != 2 {
		t.Fatal("subcomando desconocido debería salir con 2")
	}
	if code, _, _ := runCLI(t, c, "build", "-data-dir", t.TempDir()); code != 1 {
		t.Fatal("build sin datasets debería fallar y no publicar")
	}
	if code, _, _ := runCLI(t, c, "lookup", "-data-dir", t.TempDir(), "192.0.2.1"); code != 1 {
		t.Fatal("lookup sin snapshot debería fallar")
	}
	if code, _, _ := runCLI(t, c, "fetch", "-only", "nope", "-data-dir", t.TempDir()); code != 1 {
		t.Fatal("-only con fuente desconocida debería fallar")
	}
}
