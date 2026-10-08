// Package archtest verifica reglas de arquitectura del monorepo sobre las
// importaciones Go (docs/conventions.md §1, §2.1 y §5; ADR-0025).
//
// Reglas de importación:
//
//   - module-internal: un módulo services/<a> no importa services/<b>/internal/...
//     de otro módulo (b ≠ a). Tampoco services/cmd.
//   - module-api-only: un módulo services/<a> solo importa de otro módulo
//     services/<b> su contrato services/<b>/api/...; el paquete raíz de un
//     módulo (su Register) solo lo importa services/cmd.
//   - platform-no-services: packages/go/... no importa services/... (sin
//     lógica de dominio en las librerías de plataforma).
//   - domain-no-adapters: services/<a>/internal/domain/... no importa
//     .../internal/adapters/... .
//
// Se analizan también los archivos _test.go y las carpetas que empiezan por
// "_" (p. ej. services/_example); se ignoran testdata, vendor y las carpetas
// ocultas.
package archtest

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Nombres de regla.
const (
	RuleModuleInternal     = "module-internal"
	RuleModuleAPIOnly      = "module-api-only"
	RulePlatformNoServices = "platform-no-services"
	RuleDomainNoAdapters   = "domain-no-adapters"
)

// Violation es una importación que rompe una regla.
type Violation struct {
	Rule   string
	File   string // relativo a la raíz, con "/"
	Import string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s: %s imports %s", v.Rule, v.File, v.Import)
}

// Check recorre root (raíz del repositorio, donde está go.mod) y devuelve las
// violaciones ordenadas. modulePath es la ruta del módulo Go
// (github.com/hcdestroyer/horus-flow).
func Check(root, modulePath string) ([]Violation, error) {
	var out []Violation
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != root && (name == "testdata" || name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return fmt.Errorf("relative path of %s: %w", p, err)
		}
		rel = filepath.ToSlash(rel)
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		for _, imp := range f.Imports {
			ip, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			out = append(out, checkImport(rel, ip, modulePath)...)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("archtest: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Import < out[j].Import
	})
	return out, nil
}

// checkImport aplica las reglas a una importación ip hecha desde el archivo
// relFile (relativo a la raíz).
func checkImport(relFile, ip, modulePath string) []Violation {
	prefix := modulePath + "/"
	if !strings.HasPrefix(ip, prefix) {
		return nil
	}
	target := strings.TrimPrefix(ip, prefix) // p. ej. services/devices/internal/app
	src := path.Dir(relFile)                 // p. ej. services/auth/internal/app
	v := func(rule string) []Violation { return []Violation{{Rule: rule, File: relFile, Import: ip}} }

	srcMod, srcRest := splitModule(src)
	dstMod, dstRest := splitModule(target)

	if strings.HasPrefix(src, "packages/go/") || src == "packages/go" {
		if dstMod != "" {
			return v(RulePlatformNoServices)
		}
		return nil
	}
	if dstMod == "" || srcMod == "" {
		return nil
	}
	if srcMod == dstMod {
		if hasSegment(srcRest, "internal/domain") && hasSegment(dstRest, "internal/adapters") {
			return v(RuleDomainNoAdapters)
		}
		return nil
	}
	// Importación entre módulos distintos.
	switch {
	case dstRest == "internal" || strings.HasPrefix(dstRest, "internal/"):
		return v(RuleModuleInternal)
	case dstRest == "api" || strings.HasPrefix(dstRest, "api/"):
		return nil
	case srcMod == "cmd" && dstRest == "":
		// services/cmd compone los módulos a través de su Register.
		return nil
	default:
		return v(RuleModuleAPIOnly)
	}
}

// splitModule separa "services/<mod>/<resto>" en (mod, resto). Devuelve
// mod = "" si p no está bajo services/.
func splitModule(p string) (mod, rest string) {
	after, ok := strings.CutPrefix(p, "services/")
	if !ok {
		return "", ""
	}
	mod, rest, _ = strings.Cut(after, "/")
	return mod, rest
}

func hasSegment(p, seg string) bool {
	return p == seg || strings.HasPrefix(p, seg+"/")
}

// FindRepoRoot sube desde dir hasta encontrar go.mod y devuelve esa carpeta.
func FindRepoRoot(dir string) (string, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("archtest: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("archtest: go.mod not found above %s", dir)
		}
		d = parent
	}
}
