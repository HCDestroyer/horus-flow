//go:build !linux

// Package linux implementa las interfaces de túnel del hub con netlink (solo Linux).
package linux

import (
	"errors"

	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/app"
)

// Options configura el adaptador.
type Options struct {
	Underlay  string
	GRESet    string
	SourceSet string
}

// Host no existe fuera de Linux.
type Host struct{ app.Links }

// Open falla fuera de Linux.
func Open(Options) (*Host, error) {
	return nil, errors.New("tunnelagent: netlink driver requires Linux")
}

// Close no hace nada.
func (*Host) Close() error { return nil }
