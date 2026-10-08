// Package lifecycle orquesta el arranque ordenado y el apagado limpio de los
// componentes de un proceso `horus` (docs/conventions.md §2.5).
//
// Secuencia:
//
//  1. Start de cada Hook, en orden de registro y con StartTimeout. Si uno
//     falla, se ejecuta Stop de los ya arrancados en orden inverso.
//  2. Run de cada Hook en su propia goroutine, hasta que se cancela el
//     contexto padre (señal) o un Run devuelve error.
//  3. Apagado: OnDrain (p. ej. /readyz → 503), espera DrainDelay, cancela el
//     contexto de Run, espera a que todos los Run terminen y ejecuta Stop en
//     orden inverso. Todo el apagado está acotado por ShutdownTimeout.
//
// Run devuelve nil si el apagado fue limpio y a tiempo; el binario sale
// entonces con código 0.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ErrShutdownTimeout indica que el apagado superó ShutdownTimeout.
var ErrShutdownTimeout = errors.New("shutdown timeout exceeded")

// Hook es un componente gestionado. Todas las funciones son opcionales.
type Hook struct {
	// Name identifica el componente en logs y errores.
	Name string
	// Start inicializa de forma síncrona (abrir sockets, cargar cachés).
	Start func(ctx context.Context) error
	// Run trabaja hasta que ctx se cancela y entonces devuelve nil tras
	// terminar lo que tenga en curso. Devolver error antes provoca el apagado
	// de todo el proceso.
	Run func(ctx context.Context) error
	// Stop libera recursos; ctx lleva el plazo restante de apagado.
	Stop func(ctx context.Context) error
}

// Options configura un [Manager].
type Options struct {
	Logger          *slog.Logger
	StartTimeout    time.Duration // por hook; 0 = sin límite
	DrainDelay      time.Duration // espera tras OnDrain antes de cancelar Run
	ShutdownTimeout time.Duration // plazo total de apagado; 0 = sin límite
	// OnDrain se llama al empezar el apagado, antes de DrainDelay.
	OnDrain func()
}

// Manager ejecuta una lista ordenada de hooks.
type Manager struct {
	opts  Options
	hooks []Hook
}

// New crea un Manager.
func New(opts Options) *Manager {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Manager{opts: opts}
}

// Append añade hooks al final (arrancan después y paran antes que los ya
// añadidos).
func (m *Manager) Append(h ...Hook) { m.hooks = append(m.hooks, h...) }

// Run ejecuta el ciclo de vida completo hasta que ctx se cancela o un
// componente falla.
func (m *Manager) Run(ctx context.Context) error {
	log := m.opts.Logger
	started := 0
	var startErr error
	for _, h := range m.hooks {
		if h.Start != nil {
			if err := m.start(ctx, h); err != nil {
				startErr = fmt.Errorf("start %s: %w", h.Name, err)
				break
			}
		}
		started++
	}
	if startErr != nil {
		log.Error("startup failed", slog.Any("error", startErr))
		stopCtx, cancel := m.shutdownContext(context.WithoutCancel(ctx))
		defer cancel()
		return errors.Join(startErr, m.stopAll(stopCtx, started))
	}

	runCtx, cancelRun := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelRun()
	runErrs := make(chan error, len(m.hooks))
	var wg sync.WaitGroup
	for _, h := range m.hooks {
		if h.Run == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := h.Run(runCtx); err != nil {
				runErrs <- fmt.Errorf("run %s: %w", h.Name, err)
			}
		}()
	}
	allDone := make(chan struct{})
	go func() { wg.Wait(); close(allDone) }()

	var failure error
	select {
	case <-ctx.Done():
		log.Info("shutdown requested", slog.String("reason", context.Cause(ctx).Error()))
	case failure = <-runErrs:
		log.Error("component failed, shutting down", slog.Any("error", failure))
	}

	shutdownCtx, cancelShutdown := m.shutdownContext(context.WithoutCancel(ctx))
	defer cancelShutdown()
	if m.opts.OnDrain != nil {
		m.opts.OnDrain()
	}
	if failure == nil && m.opts.DrainDelay > 0 {
		t := time.NewTimer(m.opts.DrainDelay)
		select {
		case <-t.C:
		case <-shutdownCtx.Done():
			t.Stop()
		}
	}
	cancelRun()

	errs := []error{failure}
	select {
	case <-allDone:
	case <-shutdownCtx.Done():
		errs = append(errs, fmt.Errorf("waiting for components: %w", ErrShutdownTimeout))
	}
	// Errores de Run producidos durante el apagado.
drain:
	for {
		select {
		case err := <-runErrs:
			errs = append(errs, err)
		default:
			break drain
		}
	}
	errs = append(errs, m.stopAll(shutdownCtx, len(m.hooks)))
	err := errors.Join(errs...)
	if errors.Is(context.Cause(shutdownCtx), ErrShutdownTimeout) && !errors.Is(err, ErrShutdownTimeout) {
		err = errors.Join(err, ErrShutdownTimeout)
	}
	if err != nil {
		log.Error("shutdown finished with errors", slog.Any("error", err))
		return err
	}
	log.Info("shutdown complete")
	return nil
}

func (m *Manager) start(ctx context.Context, h Hook) error {
	sctx, cancel := ctx, context.CancelFunc(func() {})
	if m.opts.StartTimeout > 0 {
		sctx, cancel = context.WithTimeout(ctx, m.opts.StartTimeout)
	}
	defer cancel()
	return h.Start(sctx)
}

func (m *Manager) shutdownContext(parent context.Context) (context.Context, context.CancelFunc) {
	if m.opts.ShutdownTimeout > 0 {
		return context.WithTimeoutCause(parent, m.opts.ShutdownTimeout, ErrShutdownTimeout)
	}
	return context.WithCancel(parent)
}

// stopAll llama a Stop de los n primeros hooks en orden inverso.
func (m *Manager) stopAll(ctx context.Context, n int) error {
	var errs []error
	for i := n - 1; i >= 0; i-- {
		h := m.hooks[i]
		if h.Stop == nil {
			continue
		}
		if err := h.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop %s: %w", h.Name, err))
		}
	}
	return errors.Join(errs...)
}
