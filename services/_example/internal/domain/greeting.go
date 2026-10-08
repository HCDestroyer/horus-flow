// Package domain contiene las reglas puras del módulo (sin E/S).
package domain

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// MaxNameLen es la longitud máxima de un nombre.
const MaxNameLen = 64

// ErrInvalidName indica un nombre vacío o demasiado largo.
var ErrInvalidName = errors.New("invalid name")

// Greeting compone el saludo para name.
func Greeting(greeting, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLen {
		return "", ErrInvalidName
	}
	return greeting + ", " + name, nil
}
