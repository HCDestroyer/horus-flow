package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Los textos de los hallazgos hablan de "señales compatibles con…", nunca
// de equipos "infectados" (ADR-0024; roadmap §3, I1 criterio 7). make
// accept-i1 lo comprueba sobre los hallazgos reales; esto lo frena antes.
func TestNoInfectedWordingInReasons(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f) //nolint:gosec // fuentes del paquete
		if err != nil {
			t.Fatal(err)
		}
		for i, l := range strings.Split(string(b), "\n") {
			if strings.Contains(strings.ToLower(l), "infectad") {
				t.Errorf("%s:%d: texto de hallazgo con «infectado»: %s", f, i+1, strings.TrimSpace(l))
			}
		}
	}
}
