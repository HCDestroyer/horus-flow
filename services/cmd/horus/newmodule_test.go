package main

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/archtest"
)

// TestNewModuleScript ejecuta scripts/new-module.sh sobre una copia del repo
// y comprueba que el módulo generado compila, pasa sus tests y queda
// registrado como rol (criterio de aceptación 1 de I0-04). Lento: se omite
// con -short.
func TestNewModuleScript(t *testing.T) {
	if testing.Short() {
		t.Skip("lento: compila una copia del repositorio")
	}
	for _, tool := range []string{"bash", "go", "gofmt"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s no disponible", tool)
		}
	}
	root, err := archtest.FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	for _, p := range []string{"go.mod", "go.sum", "packages/go", "services", "scripts/new-module.sh"} {
		copyTree(t, filepath.Join(root, p), filepath.Join(dst, p))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	sh := func(name string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = dst
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return string(out)
	}
	sh("bash", "scripts/new-module.sh", "demo")

	roles, err := os.ReadFile(filepath.Join(dst, "services/cmd/horus/roles.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"github.com/hcdestroyer/horus-flow/services/demo"`, "{name: demo.Role, factory: demo.Register}"} {
		if !strings.Contains(string(roles), want) {
			t.Fatalf("roles.go sin %q", want)
		}
	}
	sh("go", "vet", "./services/demo/...", "./services/cmd/horus")
	sh("go", "test", "-count=1", "./services/demo/...")
	out := sh("go", "run", "./services/cmd/horus", "--version")
	if !strings.Contains(out, "demo") {
		t.Fatalf("--version no lista el rol demo: %s", out)
	}
	// Segunda ejecución: debe negarse a sobrescribir.
	cmd := exec.CommandContext(ctx, "bash", "scripts/new-module.sh", "demo")
	cmd.Dir = dst
	if err := cmd.Run(); err == nil {
		t.Fatal("new-module.sh debe fallar si el módulo ya existe")
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("copy %s: %v", src, err)
	}
}
