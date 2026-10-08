// Package httpx contiene los bloques HTTP comunes de `horus`: servidor con
// apagado ordenado, middleware (request_id, recuperación de pánicos,
// métricas RED), el mux de la API compartido por los roles y el handler del
// puerto de administración.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/lifecycle"
)

// ServerOptions ajusta los timeouts de [Server]. Los valores 0 usan los
// valores por defecto indicados.
type ServerOptions struct {
	ReadHeaderTimeout time.Duration // 5 s
	ReadTimeout       time.Duration // 30 s
	WriteTimeout      time.Duration // 60 s
	IdleTimeout       time.Duration // 120 s
}

// Server es un http.Server con arranque en dos fases (Start abre el socket y
// falla rápido si el puerto está ocupado; Run vigila errores de Serve) y
// apagado ordenado en Stop (espera a las peticiones en curso hasta el plazo
// del contexto).
type Server struct {
	name   string
	srv    *http.Server
	logger *slog.Logger

	ln       net.Listener
	serveErr chan error
}

// NewServer crea un servidor que escuchará en addr.
func NewServer(name, addr string, h http.Handler, logger *slog.Logger, opts ServerOptions) *Server {
	def := func(v, d time.Duration) time.Duration {
		if v > 0 {
			return v
		}
		return d
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Server{
		name:   name,
		logger: logger,
		srv: &http.Server{
			Addr:              addr,
			Handler:           h,
			ReadHeaderTimeout: def(opts.ReadHeaderTimeout, 5*time.Second),
			ReadTimeout:       def(opts.ReadTimeout, 30*time.Second),
			WriteTimeout:      def(opts.WriteTimeout, 60*time.Second),
			IdleTimeout:       def(opts.IdleTimeout, 120*time.Second),
			ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		},
		serveErr: make(chan error, 1),
	}
}

// Addr devuelve la dirección real de escucha (útil con ":0"); nil antes de Start.
func (s *Server) Addr() net.Addr {
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// Start abre el socket y empieza a servir en segundo plano.
func (s *Server) Start(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.srv.Addr)
	if err != nil {
		return fmt.Errorf("listen %s on %s: %w", s.name, s.srv.Addr, err)
	}
	s.ln = ln
	s.srv.BaseContext = func(net.Listener) context.Context { return context.WithoutCancel(ctx) }
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.serveErr <- err
		}
		close(s.serveErr)
	}()
	s.logger.InfoContext(ctx, "http server listening", slog.String("server", s.name), slog.String("addr", ln.Addr().String()))
	return nil
}

// Run espera hasta que ctx se cancela (devuelve nil; el servidor sigue
// sirviendo hasta Stop) o hasta que Serve falla.
func (s *Server) Run(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return nil
	case err, ok := <-s.serveErr:
		if ok && err != nil {
			return fmt.Errorf("serve %s: %w", s.name, err)
		}
		return nil
	}
}

// Stop deja de aceptar conexiones y espera a las peticiones en curso hasta
// el plazo de ctx; al agotarse, cierra las conexiones restantes.
func (s *Server) Stop(ctx context.Context) error {
	if s.ln == nil {
		return nil
	}
	if err := s.srv.Shutdown(ctx); err != nil {
		_ = s.srv.Close() // forzar el cierre tras agotar el plazo
		return fmt.Errorf("shutdown %s: %w", s.name, err)
	}
	s.logger.InfoContext(ctx, "http server stopped", slog.String("server", s.name))
	return nil
}

// Hook adapta el servidor al ciclo de vida.
func (s *Server) Hook() lifecycle.Hook {
	return lifecycle.Hook{Name: "http:" + s.name, Start: s.Start, Run: s.Run, Stop: s.Stop}
}
