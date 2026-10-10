package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// HeaderTraceParent es la cabecera W3C Trace Context (HTTP y NATS,
// docs/observability.md §4.2).
const HeaderTraceParent = "traceparent"

// NewTraceID devuelve un trace-id W3C aleatorio (32 hex, nunca todo ceros).
func NewTraceID() string { return randomHex(16) }

// NewSpanID devuelve un span-id W3C aleatorio (16 hex, nunca todo ceros).
func NewSpanID() string { return randomHex(8) }

func randomHex(n int) string {
	b := make([]byte, n)
	for {
		_, _ = rand.Read(b)
		for _, c := range b {
			if c != 0 {
				return hex.EncodeToString(b)
			}
		}
	}
}

// FormatTraceParent compone `00-<trace_id>-<span_id>-01` (muestreado).
func FormatTraceParent(traceID, spanID string) string {
	return "00-" + traceID + "-" + spanID + "-01"
}

// ParseTraceParent valida una cabecera traceparent W3C (versión 00) y
// devuelve trace-id y span-id (parent-id). ok = false si no es válida.
func ParseTraceParent(s string) (traceID, spanID string, ok bool) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, "-")
	if len(parts) < 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return "", "", false
	}
	if !isLowerHex(parts[1]) || !isLowerHex(parts[2]) || !isLowerHex(parts[3]) {
		return "", "", false
	}
	if strings.Trim(parts[1], "0") == "" || strings.Trim(parts[2], "0") == "" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func isLowerHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// TraceParentFrom devuelve el traceparent del contexto ("" si no hay traza).
func TraceParentFrom(ctx context.Context) string {
	tid, sid := TraceFrom(ctx)
	if tid == "" || sid == "" {
		return ""
	}
	return FormatTraceParent(tid, sid)
}

// ContinueTrace devuelve un contexto con un span hijo de traceparent (mismo
// trace_id, span_id nuevo) o, si traceparent no es válido, con una traza
// nueva. Lo usan el servidor HTTP y los consumidores NATS.
func ContinueTrace(ctx context.Context, traceparent string) context.Context {
	tid, _, ok := ParseTraceParent(traceparent)
	if !ok {
		tid = NewTraceID()
	}
	return WithTrace(ctx, tid, NewSpanID())
}

// EnsureTrace devuelve ctx si ya tiene traza o un contexto con una traza
// nueva (trabajos de fondo: ticks del motor, relay, despachadores).
func EnsureTrace(ctx context.Context) context.Context {
	if tid, _ := TraceFrom(ctx); tid != "" {
		return ctx
	}
	return WithTrace(ctx, NewTraceID(), NewSpanID())
}
