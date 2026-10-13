package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/services/assist/internal/provider"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/provider/fake"
)

func start(p *fake.Provider) provider.Conversation {
	return p.Start(provider.Request{System: "s", User: "u"})
}

func okExec(_ context.Context, c provider.ToolCall) (string, error) {
	return `{"tool":"` + c.Name + `"}`, nil
}

func TestRunSimpleAnswer(t *testing.T) {
	t.Parallel()
	p := &fake.Provider{Turns: []provider.Turn{{Text: "## Qué está pasando\nTodo bien", StopReason: provider.StopEnd,
		Usage: provider.Usage{InputTokens: 10, OutputTokens: 5}, Model: "m-1"}}}
	res, err := Run(context.Background(), start(p), okExec, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Markdown != "## Qué está pasando\nTodo bien" || res.Usage.InputTokens != 10 || res.Iterations != 1 || res.Model != "m-1" {
		t.Fatalf("res = %+v", res)
	}
	if len(res.ToolUses) != 0 {
		t.Fatalf("tool uses = %+v", res.ToolUses)
	}
}

func TestRunParallelToolsOneBatch(t *testing.T) {
	t.Parallel()
	p := &fake.Provider{Turns: []provider.Turn{
		{StopReason: provider.StopToolUse, Usage: provider.Usage{InputTokens: 100, OutputTokens: 20}, ToolCalls: []provider.ToolCall{
			{ID: "a", Name: "get_finding"}, {ID: "b", Name: "get_router"}, {ID: "c", Name: "get_finding"},
		}},
		{Text: "listo", StopReason: provider.StopEnd, Usage: provider.Usage{InputTokens: 300, OutputTokens: 50}},
	}}
	// Barrera: las tres llamadas deben estar en curso a la vez (paralelismo real).
	var arrived atomic.Int32
	release := make(chan struct{})
	exec := func(ctx context.Context, c provider.ToolCall) (string, error) {
		if arrived.Add(1) == 3 {
			close(release)
		}
		select {
		case <-release:
		case <-time.After(5 * time.Second):
			return "", errors.New("no se ejecutaron en paralelo")
		}
		return okExec(ctx, c)
	}
	res, err := Run(context.Background(), start(p), exec, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Results) != 1 || len(p.Results[0]) != 3 {
		t.Fatalf("todos los tool_result van en un único mensaje: %+v", p.Results)
	}
	for i, id := range []string{"a", "b", "c"} {
		if p.Results[0][i].CallID != id || p.Results[0][i].IsError {
			t.Fatalf("resultado %d = %+v", i, p.Results[0][i])
		}
	}
	if res.Usage.InputTokens != 400 || res.Usage.OutputTokens != 70 {
		t.Errorf("uso acumulado = %+v", res.Usage)
	}
	if len(res.ToolUses) != 2 || res.ToolUses[0].Name != "get_finding" || res.ToolUses[0].Count != 2 || res.ToolUses[1].Count != 1 {
		t.Errorf("tool uses = %+v", res.ToolUses)
	}
}

func TestRunToolErrorIsReturnedAsIsError(t *testing.T) {
	t.Parallel()
	p := &fake.Provider{Turns: []provider.Turn{
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{{ID: "x", Name: "get_router"}, {ID: "y", Name: "boom"}}},
		{Text: "sin datos del router", StopReason: provider.StopEnd},
	}}
	exec := func(_ context.Context, c provider.ToolCall) (string, error) {
		switch c.Name {
		case "boom":
			panic("fallo inesperado")
		default:
			return "", errors.New("El usuario no tiene permiso para consultar este dato.")
		}
	}
	res, err := Run(context.Background(), start(p), exec, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := p.Results[0]
	if !r[0].IsError || r[0].Content != "El usuario no tiene permiso para consultar este dato." || !r[1].IsError {
		t.Fatalf("results = %+v", r)
	}
	if res.ToolUses[1].Errors != 1 || res.ToolUses[0].Errors != 1 {
		t.Fatalf("errores contados = %+v", res.ToolUses)
	}
}

func TestRunMaxIterations(t *testing.T) {
	t.Parallel()
	p := &fake.Provider{Turns: []provider.Turn{{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{{ID: "1", Name: "list_open_findings"}},
		Usage: provider.Usage{InputTokens: 1}}}}
	res, err := Run(context.Background(), start(p), okExec, Options{MaxIterations: 3})
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("err = %v", err)
	}
	if p.Calls() != 3 || res.Iterations != 3 || res.Usage.InputTokens != 3 || res.ToolUses[0].Count != 3 {
		t.Fatalf("calls = %d res = %+v", p.Calls(), res)
	}
}

func TestRunRefusalBeforeContent(t *testing.T) {
	t.Parallel()
	p := &fake.Provider{Turns: []provider.Turn{{StopReason: provider.StopRefusal, RefusalCategory: "cyber", Text: "no",
		ToolCalls: []provider.ToolCall{{ID: "z", Name: "get_finding"}}}}}
	executed := false
	res, err := Run(context.Background(), start(p), func(context.Context, provider.ToolCall) (string, error) {
		executed = true
		return "", nil
	}, Options{})
	if !errors.Is(err, ErrRefused) || res.RefusalCategory != "cyber" || res.Markdown != "" || executed {
		t.Fatalf("err = %v res = %+v executed = %v", err, res, executed)
	}
}

func TestRunProviderErrorAndEmpty(t *testing.T) {
	t.Parallel()
	boom := provider.ErrUnavailable
	p := &fake.Provider{Turns: []provider.Turn{{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{{ID: "1", Name: "t"}},
		Usage: provider.Usage{InputTokens: 7}}}, Err: boom, ErrAt: 1}
	res, err := Run(context.Background(), start(p), okExec, Options{})
	if !errors.Is(err, boom) || res.Usage.InputTokens != 7 || len(res.ToolUses) != 1 {
		t.Fatalf("err = %v res = %+v", err, res)
	}
	p2 := &fake.Provider{Turns: []provider.Turn{{Text: "  ", StopReason: provider.StopEnd}}}
	if _, err := Run(context.Background(), start(p2), okExec, Options{}); !errors.Is(err, ErrEmpty) {
		t.Fatalf("vacío: %v", err)
	}
	p3 := &fake.Provider{Turns: []provider.Turn{{Text: "parcial", StopReason: provider.StopMaxTokens}}}
	if res, err := Run(context.Background(), start(p3), okExec, Options{}); err != nil || !res.Truncated {
		t.Fatalf("max_tokens: %v %+v", err, res)
	}
}

func TestRunContextCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &fake.Provider{Turns: []provider.Turn{{Text: "x", StopReason: provider.StopEnd}}}
	if _, err := Run(ctx, start(p), okExec, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
