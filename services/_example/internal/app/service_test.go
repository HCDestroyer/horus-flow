package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hcdestroyer/horus-flow/services/_example/internal/app"
	"github.com/hcdestroyer/horus-flow/services/_example/internal/domain"
)

func TestServiceHello(t *testing.T) {
	t.Parallel()
	svc := app.NewService("hola")
	got, err := svc.Hello(context.Background(), " ana ")
	if err != nil || got != "hola, ana" {
		t.Fatalf("Hello = %q, %v", got, err)
	}
	if _, err := svc.Hello(context.Background(), " "); !errors.Is(err, domain.ErrInvalidName) {
		t.Fatalf("err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Hello(ctx, "ana"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
