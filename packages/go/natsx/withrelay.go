package natsx

import (
	"context"
	"log/slog"
	"sync"

	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
)

// WithRelay envuelve el módulo m para que, mientras corre, el relay publique
// el outbox de schema en el bus compartido del proceso (HORUS_NATS_URL). Sin
// NATS configurado devuelve m tal cual: los eventos esperan en PostgreSQL.
// Conserva Start/Stop de m.
func WithRelay(ctx context.Context, deps module.Deps, m module.Module, db *pgdb.DB, schema string) (module.Module, error) {
	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	bus, err := Shared(ctx, deps.Services, deps.Environ, logger)
	if err != nil {
		return nil, err
	}
	if bus == nil {
		logger.WarnContext(ctx, "outbox relay inactive: HORUS_NATS_URL not set", slog.String("schema", schema))
		return m, nil
	}
	return &relayed{inner: m, relay: &Relay{DB: db, Schema: schema, JS: bus.JS, Logger: logger}}, nil
}

type relayed struct {
	inner module.Module
	relay *Relay
}

func (r *relayed) Start(ctx context.Context) error {
	if s, ok := r.inner.(module.Starter); ok {
		return s.Start(ctx) //nolint:wrapcheck // error del módulo
	}
	return nil
}

func (r *relayed) Run(ctx context.Context) error {
	rctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _ = r.relay.Run(rctx) }()
	err := r.inner.Run(ctx)
	cancel()
	wg.Wait()
	return err //nolint:wrapcheck // error del módulo
}

func (r *relayed) Stop(ctx context.Context) error {
	if s, ok := r.inner.(module.Stopper); ok {
		return s.Stop(ctx) //nolint:wrapcheck // error del módulo
	}
	return nil
}
