// Package config carga la configuración tipada de Horus desde variables de
// entorno (12-factor, docs/conventions.md §2.3).
//
// Reglas:
//   - Solo variables de entorno con prefijo HORUS_; sin archivos obligatorios.
//   - Toda variable admite la variante HORUS_<CLAVE>_FILE: si existe, se lee el
//     archivo indicado (pensado para secretos de Docker/Kubernetes) y su
//     contenido tiene prioridad sobre HORUS_<CLAVE>.
//   - Las estructuras usan las etiquetas de github.com/caarlos0/env/v11
//     (env, envDefault, required, envSeparator) y pueden implementar
//     [Validator] para validaciones propias.
//   - Los errores nunca incluyen el contenido de un archivo _FILE.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/caarlos0/env/v11"
)

// FileSuffix es el sufijo de las variables que apuntan a un archivo con el valor.
const FileSuffix = "_FILE"

// Prefix es el prefijo de las variables de Horus. Solo las variables
// HORUS_*_FILE se resuelven como archivos; el resto del entorno (p. ej.
// PIP_CONFIG_FILE) se ignora.
const Prefix = "HORUS_"

// Validator lo implementan las estructuras de configuración que necesitan
// reglas propias tras el parseo (rangos, enumerados, combinaciones).
type Validator interface {
	Validate() error
}

// ReadFileFunc lee el contenido de un archivo _FILE. Se inyecta en tests.
type ReadFileFunc func(path string) ([]byte, error)

// Load crea un T, lo rellena desde environ (formato "CLAVE=valor", como
// os.Environ) y lo valida.
func Load[T any](environ []string) (T, error) {
	var cfg T
	if err := Parse(&cfg, environ); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Parse rellena dst (puntero a struct) desde environ resolviendo las
// variables _FILE con os.ReadFile y llama a Validate si dst implementa
// [Validator].
func Parse(dst any, environ []string) error {
	return ParseWith(dst, environ, os.ReadFile)
}

// ParseWith es Parse con un lector de archivos inyectable.
func ParseWith(dst any, environ []string, readFile ReadFileFunc) error {
	vars, err := Resolve(environ, readFile)
	if err != nil {
		return err
	}
	if err := env.ParseWithOptions(dst, env.Options{Environment: vars}); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if v, ok := dst.(Validator); ok {
		if err := v.Validate(); err != nil {
			return fmt.Errorf("config: %w", err)
		}
	}
	return nil
}

// Resolve convierte environ en un mapa y aplica las variables _FILE: para
// cada HORUS_CLAVE_FILE=ruta se lee el archivo y su contenido (sin el salto
// de línea final) sustituye a HORUS_CLAVE. Una ruta vacía se ignora.
func Resolve(environ []string, readFile ReadFileFunc) (map[string]string, error) {
	vars := make(map[string]string, len(environ))
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		vars[k] = v
	}
	var errs []error
	resolved := map[string]string{}
	for k, path := range vars {
		base, isFile := strings.CutSuffix(k, FileSuffix)
		if !isFile || !strings.HasPrefix(base, Prefix) || base == Prefix || path == "" {
			continue
		}
		b, err := readFile(path)
		if err != nil {
			// Se nombra la variable, nunca el contenido.
			errs = append(errs, fmt.Errorf("config: read %s: %w", k, err))
			continue
		}
		resolved[base] = strings.TrimRight(string(b), "\r\n")
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	for k, v := range resolved {
		vars[k] = v
	}
	return vars, nil
}
