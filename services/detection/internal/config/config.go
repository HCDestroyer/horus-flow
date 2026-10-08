// Package config contiene la configuración de `mod:detection`. Por ahora solo
// la declaración de feeds de reputación (feeds.yaml, embebida en el binario).
package config

import (
	_ "embed"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
)

//go:embed feeds.yaml
var defaultFeeds []byte

// DefaultFeeds devuelve la declaración de feeds embebida.
func DefaultFeeds() (*datasets.Config, error) { return datasets.ParseConfig(defaultFeeds) }

// LoadFeeds lee la declaración de feeds de path, o la embebida si path es "".
func LoadFeeds(path string) (*datasets.Config, error) {
	if path == "" {
		return DefaultFeeds()
	}
	return datasets.LoadConfig(path)
}
