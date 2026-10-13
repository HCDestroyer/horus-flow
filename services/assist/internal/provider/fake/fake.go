// Package fake es un proveedor de IA de guion para tests: devuelve los turnos
// indicados en orden y registra lo que recibe.
package fake

import (
	"context"
	"errors"
	"sync"

	"github.com/hcdestroyer/horus-flow/services/assist/internal/provider"
)

// Provider es un proveedor con guion.
type Provider struct {
	ModelName string
	Turns     []provider.Turn
	// Err, si no es nil, lo devuelve Next en el turno ErrAt (0-based).
	Err   error
	ErrAt int
	// PingErr lo devuelve Ping.
	PingErr error

	mu       sync.Mutex
	Requests []provider.Request
	// Results son los lotes de resultados recibidos (uno por AddToolResults).
	Results [][]provider.ToolResult
	calls   int
}

// Name implementa provider.Provider.
func (p *Provider) Name() string { return "fake" }

// Model implementa provider.Provider.
func (p *Provider) Model() string {
	if p.ModelName == "" {
		return "fake-model"
	}
	return p.ModelName
}

// Ping implementa provider.Provider.
func (p *Provider) Ping(context.Context) error { return p.PingErr }

// Start implementa provider.Provider.
func (p *Provider) Start(req provider.Request) provider.Conversation {
	p.mu.Lock()
	p.Requests = append(p.Requests, req)
	p.mu.Unlock()
	return &conv{p: p}
}

// Calls devuelve cuántas veces se llamó a Next.
func (p *Provider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type conv struct{ p *Provider }

func (c *conv) Next(ctx context.Context) (provider.Turn, error) {
	c.p.mu.Lock()
	defer c.p.mu.Unlock()
	i := c.p.calls
	c.p.calls++
	if err := ctx.Err(); err != nil {
		return provider.Turn{}, err
	}
	if c.p.Err != nil && i == c.p.ErrAt {
		return provider.Turn{}, c.p.Err
	}
	if len(c.p.Turns) == 0 {
		return provider.Turn{}, errors.New("fake: no turns")
	}
	if i >= len(c.p.Turns) {
		i = len(c.p.Turns) - 1 // repite el último (para probar el tope de iteraciones)
	}
	return c.p.Turns[i], nil
}

func (c *conv) AddToolResults(results []provider.ToolResult) {
	c.p.mu.Lock()
	defer c.p.mu.Unlock()
	c.p.Results = append(c.p.Results, results)
}
