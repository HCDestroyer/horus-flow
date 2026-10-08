// Package scenarios contiene los escenarios del simulador de flujos
// (contrato C10). Cada YAML describe nodos, poblaciones de clientes, sus
// comportamientos y las señales/hallazgos que promete; el formato está
// documentado en tools/flowsim/README.md.
package scenarios

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

// FS son los escenarios embebidos en los binarios.
//
//go:embed *.yaml
var FS embed.FS

// Names devuelve los nombres de los escenarios embebidos, ordenados.
func Names() []string {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), ".yaml"); ok {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
