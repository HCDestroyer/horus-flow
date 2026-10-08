// Package app contiene los casos de uso del módulo; orquesta el dominio y
// los puertos (interfaces definidas aquí, implementadas en adapters).
package app

import (
	"context"
	"fmt"

	"github.com/hcdestroyer/horus-flow/services/_example/internal/domain"
)

// Service implementa los casos de uso.
type Service struct {
	greeting string
}

// NewService crea el servicio.
func NewService(greeting string) *Service {
	return &Service{greeting: greeting}
}

// Hello devuelve el saludo para name.
func (s *Service) Hello(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("hello: %w", err)
	}
	g, err := domain.Greeting(s.greeting, name)
	if err != nil {
		return "", fmt.Errorf("hello: %w", err)
	}
	return g, nil
}
