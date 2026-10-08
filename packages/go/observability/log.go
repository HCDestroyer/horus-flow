// Package observability reúne la telemetría de plataforma común a todos los
// roles de `horus` (docs/observability.md): logger slog JSON con los campos
// obligatorios, registro Prometheus con horus_build_info, validación de
// labels y el tipo Secret para no filtrar secretos en logs.
package observability

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
)

// Claves de log obligatorias (docs/observability.md §3.1).
const (
	KeyTimestamp = "ts"
	KeyService   = "service"
	KeyProcess   = "process"
	KeyRole      = "role"
	KeyTraceID   = "trace_id"
	KeySpanID    = "span_id"
	KeyRequestID = "request_id"
	KeyTenantID  = "tenant_id"
	KeyVersion   = "version"
	KeyEnv       = "env"
	KeyError     = "error"
)

// TimeFormat es RFC 3339 con milisegundos y Z.
const TimeFormat = "2006-01-02T15:04:05.000Z07:00"

// LogConfig configura [NewLogger].
type LogConfig struct {
	// Level: debug, info, warn o error.
	Level string
	// Format: json (por defecto) o text.
	Format string
	// Service es el valor de service cuando la línea no pertenece a un rol.
	Service string
	// Process, Version y Env se añaden a todas las líneas si no están vacíos.
	Process string
	Version string
	Env     string
}

// NewLogger crea el logger de proceso. Devuelve también el LevelVar para
// poder cambiar el nivel en caliente.
func NewLogger(w io.Writer, c LogConfig) (*slog.Logger, *slog.LevelVar, error) {
	lv := new(slog.LevelVar)
	if c.Level != "" {
		var l slog.Level
		if err := l.UnmarshalText([]byte(c.Level)); err != nil {
			return nil, nil, fmt.Errorf("log level %q: %w", c.Level, err)
		}
		lv.Set(l)
	}
	opts := &slog.HandlerOptions{Level: lv, ReplaceAttr: replaceAttr}
	var inner slog.Handler
	switch strings.ToLower(c.Format) {
	case "", "json":
		inner = slog.NewJSONHandler(w, opts)
	case "text":
		inner = slog.NewTextHandler(w, opts)
	default:
		return nil, nil, fmt.Errorf("log format %q: want json or text", c.Format)
	}
	var static []slog.Attr
	for _, a := range []slog.Attr{
		slog.String(KeyProcess, c.Process),
		slog.String(KeyVersion, c.Version),
		slog.String(KeyEnv, c.Env),
	} {
		if a.Value.String() != "" {
			static = append(static, a)
		}
	}
	h := NewContextHandler(inner.WithAttrs(static), c.Service)
	return slog.New(h), lv, nil
}

// ForRole devuelve un logger hijo para un rol: service y role = rol.
func ForRole(l *slog.Logger, role string) *slog.Logger {
	return l.With(slog.String(KeyService, role), slog.String(KeyRole, role))
}

func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch a.Key {
	case slog.TimeKey:
		if t, ok := a.Value.Any().(time.Time); ok {
			return slog.String(KeyTimestamp, t.UTC().Format(TimeFormat))
		}
	case slog.LevelKey:
		if l, ok := a.Value.Any().(slog.Level); ok {
			return slog.String(slog.LevelKey, strings.ToLower(l.String()))
		}
	}
	return a
}

// ContextHandler envuelve otro handler y garantiza en cada línea los campos
// obligatorios service, role, trace_id, span_id, request_id y tenant_id,
// tomándolos del contexto (o vacíos). Si el logger ya fijó alguno con With,
// se respeta el valor fijado.
//
// Limitación: tras WithGroup los campos de contexto quedan dentro del grupo;
// los roles no deben usar WithGroup en el logger raíz.
type ContextHandler struct {
	inner          slog.Handler
	defaultService string
	bound          map[string]bool
}

// NewContextHandler crea un [ContextHandler]; defaultService es el service
// para líneas sin rol.
func NewContextHandler(inner slog.Handler, defaultService string) *ContextHandler {
	return &ContextHandler{inner: inner, defaultService: defaultService, bound: map[string]bool{}}
}

// Enabled implementa slog.Handler.
func (h *ContextHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

// Handle implementa slog.Handler.
func (h *ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	role := RoleFrom(ctx)
	traceID, spanID := TraceFrom(ctx)
	add := func(key, val string) {
		if !h.bound[key] {
			r.AddAttrs(slog.String(key, val))
		}
	}
	service := h.defaultService
	if role != "" {
		service = role
	}
	add(KeyService, service)
	add(KeyRole, role)
	add(KeyTraceID, traceID)
	add(KeySpanID, spanID)
	add(KeyRequestID, RequestIDFrom(ctx))
	add(KeyTenantID, TenantFrom(ctx))
	if err := h.inner.Handle(ctx, r); err != nil {
		return fmt.Errorf("log handler: %w", err)
	}
	return nil
}

// WithAttrs implementa slog.Handler.
func (h *ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	bound := make(map[string]bool, len(h.bound)+len(attrs))
	for k := range h.bound {
		bound[k] = true
	}
	for _, a := range attrs {
		bound[a.Key] = true
	}
	return &ContextHandler{inner: h.inner.WithAttrs(attrs), defaultService: h.defaultService, bound: bound}
}

// WithGroup implementa slog.Handler.
func (h *ContextHandler) WithGroup(name string) slog.Handler {
	return &ContextHandler{inner: h.inner.WithGroup(name), defaultService: h.defaultService, bound: h.bound}
}
