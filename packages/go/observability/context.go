package observability

import "context"

type ctxKey int

const (
	keyRole ctxKey = iota
	keyTenant
	keyRequestID
	keyTrace
)

type traceIDs struct{ traceID, spanID string }

// WithRole devuelve un contexto marcado con el rol que lo ejecuta. Los logs
// emitidos con ese contexto llevan role y service = rol.
func WithRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, keyRole, role)
}

// RoleFrom devuelve el rol del contexto o "".
func RoleFrom(ctx context.Context) string {
	v, _ := ctx.Value(keyRole).(string)
	return v
}

// WithTenant marca el contexto con el tenant (UUID del ISP) de la petición o
// mensaje en curso. Solo para correlación en logs: la autorización usa
// TenantScope (packages/go/authz), nunca este valor.
func WithTenant(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, keyTenant, tenantID)
}

// TenantFrom devuelve el tenant del contexto o "".
func TenantFrom(ctx context.Context) string {
	v, _ := ctx.Value(keyTenant).(string)
	return v
}

// WithRequestID marca el contexto con el request_id (X-Request-Id).
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyRequestID, id)
}

// RequestIDFrom devuelve el request_id del contexto o "".
func RequestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(keyRequestID).(string)
	return v
}

// WithTrace marca el contexto con los identificadores W3C de traza (32 hex)
// y span (16 hex). Cuando llegue OpenTelemetry (I0-18), el handler de logs
// los leerá también del span activo.
func WithTrace(ctx context.Context, traceID, spanID string) context.Context {
	return context.WithValue(ctx, keyTrace, traceIDs{traceID: traceID, spanID: spanID})
}

// TraceFrom devuelve trace_id y span_id del contexto (vacíos si no hay).
func TraceFrom(ctx context.Context) (traceID, spanID string) {
	v, _ := ctx.Value(keyTrace).(traceIDs)
	return v.traceID, v.spanID
}
