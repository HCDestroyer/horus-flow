// Command horus es el binario único de Horus Flow (ADR-0025).
//
// Qué módulos ejecuta cada proceso se elige con HORUS_ROLES. En este esqueleto
// (historia I0-01) solo muestra la versión, los roles disponibles y los roles
// seleccionados; el registro real de módulos, configuración, logs, métricas y
// health checks llega con I0-04.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// version se inyecta en el build con -ldflags "-X main.version=...".
var version = "dev"

// availableRoles son los roles del binario según ADR-0025 y docs/services.md.
var availableRoles = []string{
	"gateway",
	"auth",
	"devices",
	"wireguard",
	"snmp",
	"traffic",
	"detection",
	"alerts",
	"analytics",
	"reporting",
	"ingester",
	"jobs",
	"collector",
	"wg-agent",
}

// parseRoles interpreta el valor de HORUS_ROLES: lista separada por comas,
// "all" (o vacío) para todos los roles. Devuelve error ante un rol desconocido.
func parseRoles(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "all" {
		return append([]string(nil), availableRoles...), nil
	}
	known := make(map[string]bool, len(availableRoles))
	for _, r := range availableRoles {
		known[r] = true
	}
	seen := make(map[string]bool)
	var roles []string
	for _, part := range strings.Split(raw, ",") {
		r := strings.TrimSpace(part)
		if r == "" || seen[r] {
			continue
		}
		if !known[r] {
			return nil, fmt.Errorf("rol desconocido %q", r)
		}
		seen[r] = true
		roles = append(roles, r)
	}
	if len(roles) == 0 {
		return nil, fmt.Errorf("HORUS_ROLES no contiene ningún rol")
	}
	return roles, nil
}

func run(w io.Writer, rolesEnv string) error {
	roles, err := parseRoles(rolesEnv)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "horus %s\nroles disponibles: %s\nroles seleccionados (HORUS_ROLES): %s\n",
		version, strings.Join(availableRoles, ", "), strings.Join(roles, ", "))
	if err != nil {
		return fmt.Errorf("escribir salida: %w", err)
	}
	return nil
}

func main() {
	if err := run(os.Stdout, os.Getenv("HORUS_ROLES")); err != nil {
		fmt.Fprintf(os.Stderr, "horus: %v\n", err)
		os.Exit(2)
	}
}
