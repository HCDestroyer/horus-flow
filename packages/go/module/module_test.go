package module_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/module"
)

func TestIdleReturnsOnCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- module.Idle().Run(ctx) }()
	select {
	case <-done:
		t.Fatal("Idle terminó antes de cancelar")
	case <-time.After(10 * time.Millisecond):
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Idle = %v", err)
	}
}

func TestFunc(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	var m module.Module = module.Func(func(context.Context) error { return boom })
	if err := m.Run(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}
