package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

// Cada petición lleva traza (continuada de un traceparent válido o nueva),
// la respuesta devuelve el traceparent del span del servidor y las rutas de
// un router marcan router_id en el contexto de los logs.
func TestTraceAndRouterContext(t *testing.T) {
	t.Parallel()
	mux := httpx.NewMux(nil, nil)
	var gotTrace, gotRouter string
	mux.ForService("devices").HandleFunc("GET /api/v1/routers/{router_id}", func(w http.ResponseWriter, r *http.Request) {
		gotTrace, _ = observability.TraceFrom(r.Context())
		gotRouter = observability.RouterFrom(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	h := mux.Handler()
	parent := observability.NewTraceID()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/routers/r-1", nil)
	req.Header.Set("traceparent", observability.FormatTraceParent(parent, observability.NewSpanID()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if gotTrace != parent || gotRouter != "r-1" {
		t.Fatalf("trace=%q router=%q", gotTrace, gotRouter)
	}
	if tid, _, ok := observability.ParseTraceParent(rec.Header().Get("traceparent")); !ok || tid != parent {
		t.Fatalf("response traceparent %q", rec.Header().Get("traceparent"))
	}
	// Sin traceparent (o inválido): traza nueva.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/routers/r-2", nil)
	req.Header.Set("traceparent", "basura")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if len(gotTrace) != 32 || gotTrace == parent {
		t.Fatalf("new trace = %q", gotTrace)
	}
}
