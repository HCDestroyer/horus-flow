package archtest_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hcdestroyer/horus-flow/packages/go/archtest"
)

const modulePath = "github.com/hcdestroyer/horus-flow"

// TestRepositoryArchitecture es el test de arquitectura del monorepo: corre
// en cada `go test ./...` contra el código real.
func TestRepositoryArchitecture(t *testing.T) {
	t.Parallel()
	root, err := archtest.FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	violations, err := archtest.Check(root, modulePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range violations {
		t.Errorf("violación de arquitectura: %s", v)
	}
}

// Criterio de aceptación 4 de I0-04: un módulo que importa el internal/ de
// otro hace fallar el test.
func TestDetectsViolations(t *testing.T) {
	t.Parallel()
	got, err := archtest.Check(filepath.Join("testdata", "bad"), modulePath)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, v := range got {
		lines = append(lines, v.String())
	}
	want := []string{
		archtest.RulePlatformNoServices + ": packages/go/lib/lib.go imports " + modulePath + "/services/auth/api",
		archtest.RuleModuleInternal + ": services/auth/internal/app/app.go imports " + modulePath + "/services/devices/internal/app",
		archtest.RuleModuleAPIOnly + ": services/auth/internal/app/app_test.go imports " + modulePath + "/services/devices",
		archtest.RuleDomainNoAdapters + ": services/auth/internal/domain/d.go imports " + modulePath + "/services/auth/internal/adapters/postgres",
		archtest.RuleModuleInternal + ": services/cmd/horus/main.go imports " + modulePath + "/services/auth/internal/app",
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("violaciones:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestAllowedImports(t *testing.T) {
	t.Parallel()
	got, err := archtest.Check(filepath.Join("testdata", "good"), modulePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("falsos positivos: %v", got)
	}
}

func TestParseErrorAndMissingRoot(t *testing.T) {
	t.Parallel()
	broken := t.TempDir()
	dir := filepath.Join(broken, "services", "x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte("package x\n\nimport (\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := archtest.Check(broken, modulePath); err == nil {
		t.Fatal("esperaba error de parseo")
	}
	if _, err := archtest.Check(filepath.Join("testdata", "no-existe"), modulePath); err == nil {
		t.Fatal("esperaba error por raíz inexistente")
	}
}

func TestFindRepoRootFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "go.mod")); err == nil {
		t.Skip("hay un go.mod sobre el directorio temporal")
	}
	if _, err := archtest.FindRepoRoot(dir); err == nil {
		t.Fatal("esperaba error sin go.mod")
	}
}
