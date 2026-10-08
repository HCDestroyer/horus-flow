// Package config contiene la configuración de `mod:traffic`. Por ahora solo
// la declaración de fuentes del dataset prefijo → ASN → organización
// (datasets.yaml, embebida en el binario).
package config

import (
	_ "embed"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
)

//go:embed datasets.yaml
var defaultDatasets []byte

// DefaultDatasets devuelve la declaración embebida.
func DefaultDatasets() (*datasets.Config, error) { return datasets.ParseConfig(defaultDatasets) }

// LoadDatasets lee la declaración de path, o la embebida si path es "".
func LoadDatasets(path string) (*datasets.Config, error) {
	if path == "" {
		return DefaultDatasets()
	}
	return datasets.LoadConfig(path)
}
