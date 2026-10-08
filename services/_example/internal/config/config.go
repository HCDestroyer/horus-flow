// Package config define la configuración propia del módulo (HORUS_EXAMPLE_*).
package config

import (
	"errors"
	"strings"
)

// Config del módulo. Las variables comunes (HORUS_ENV, HORUS_LOG_LEVEL…)
// están en packages/go/config.Common.
type Config struct {
	// Greeting es el saludo que devuelve la ruta hello.
	Greeting string `env:"HORUS_EXAMPLE_GREETING" envDefault:"hola"`
	// DependencyAddr (host:puerto) es una dependencia opcional cuyo estado se
	// publica en /readyz como chequeo degradable.
	DependencyAddr string `env:"HORUS_EXAMPLE_DEPENDENCY_ADDR"`
}

// Validate implementa config.Validator.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Greeting) == "" {
		return errors.New("HORUS_EXAMPLE_GREETING must not be blank")
	}
	return nil
}
