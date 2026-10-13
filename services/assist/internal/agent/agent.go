// Package agent es el bucle de herramientas del asistente (docs/assist.md):
// pide al modelo, ejecuta TODAS las herramientas que solicite en un turno
// (en paralelo, de solo lectura) y devuelve sus resultados en un único
// mensaje, hasta que el modelo responde sin herramientas o se agota el tope
// de iteraciones.
package agent

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/hcdestroyer/horus-flow/services/assist/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/provider"
)

// Executor ejecuta una herramienta y devuelve su resultado (texto/JSON) o un
// error apto para el modelo.
type Executor func(ctx context.Context, call provider.ToolCall) (string, error)

// Errores del bucle.
var (
	// ErrRefused: el modelo rechazó la petición (stop_reason refusal).
	ErrRefused = errors.New("agent: refused by the model")
	// ErrMaxIterations: el modelo siguió pidiendo herramientas tras el tope.
	ErrMaxIterations = errors.New("agent: max iterations reached")
	// ErrEmpty: el modelo terminó sin texto.
	ErrEmpty = errors.New("agent: empty answer")
)

// Options del bucle.
type Options struct {
	MaxIterations int
	// MaxParallel acota las herramientas simultáneas de un turno.
	MaxParallel int
}

// Result es el resultado de un análisis.
type Result struct {
	Markdown        string
	Usage           provider.Usage
	ToolUses        []domain.ToolUse
	Iterations      int
	Model           string
	RefusalCategory string
	// Truncated indica que la respuesta se cortó por max_tokens.
	Truncated bool
}

// Run conduce la conversación. Devuelve el resultado parcial (uso y
// herramientas) también cuando falla, para la auditoría.
func Run(ctx context.Context, conv provider.Conversation, exec Executor, o Options) (Result, error) {
	if o.MaxIterations <= 0 {
		o.MaxIterations = 8
	}
	if o.MaxParallel <= 0 {
		o.MaxParallel = 4
	}
	var res Result
	uses := map[string]*domain.ToolUse{}
	for i := 0; i < o.MaxIterations; i++ {
		res.Iterations = i + 1
		turn, err := conv.Next(ctx)
		res.Usage.Add(turn.Usage)
		if turn.Model != "" {
			res.Model = turn.Model
		}
		if err != nil {
			res.ToolUses = sortedUses(uses)
			return res, err
		}
		// El rechazo se comprueba antes de leer el contenido.
		if turn.StopReason == provider.StopRefusal {
			res.RefusalCategory = turn.RefusalCategory
			res.ToolUses = sortedUses(uses)
			return res, ErrRefused
		}
		if len(turn.ToolCalls) == 0 {
			res.Markdown = strings.TrimSpace(turn.Text)
			res.Truncated = turn.StopReason == provider.StopMaxTokens
			res.ToolUses = sortedUses(uses)
			if res.Markdown == "" {
				return res, ErrEmpty
			}
			return res, nil
		}
		results := runTools(ctx, turn.ToolCalls, exec, o.MaxParallel)
		for j, c := range turn.ToolCalls {
			u := uses[c.Name]
			if u == nil {
				u = &domain.ToolUse{Name: c.Name}
				uses[c.Name] = u
			}
			u.Count++
			if results[j].IsError {
				u.Errors++
			}
		}
		conv.AddToolResults(results)
		if err := ctx.Err(); err != nil {
			res.ToolUses = sortedUses(uses)
			return res, err
		}
	}
	res.ToolUses = sortedUses(uses)
	return res, ErrMaxIterations
}

// runTools ejecuta las llamadas de un turno en paralelo (acotado) y devuelve
// los resultados en el mismo orden.
func runTools(ctx context.Context, calls []provider.ToolCall, exec Executor, parallel int) []provider.ToolResult {
	out := make([]provider.ToolResult, len(calls))
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() {
				if r := recover(); r != nil {
					out[i] = provider.ToolResult{CallID: c.ID, Content: "Error interno de la herramienta.", IsError: true}
				}
				<-sem
				wg.Done()
			}()
			content, err := exec(ctx, c)
			if err != nil {
				out[i] = provider.ToolResult{CallID: c.ID, Content: err.Error(), IsError: true}
				return
			}
			out[i] = provider.ToolResult{CallID: c.ID, Content: content}
		}()
	}
	wg.Wait()
	return out
}

func sortedUses(m map[string]*domain.ToolUse) []domain.ToolUse {
	out := make([]domain.ToolUse, 0, len(m))
	for _, u := range m {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
