package httpx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
)

// TestMetricsMiddlewareAllowsWebSocket (I1-26): el middleware de métricas
// envuelve el ResponseWriter; un WebSocket detrás de él debe poder hacer
// Hijack (antes respondía 501) y la petición se cuenta como 101.
func TestMetricsMiddlewareAllowsWebSocket(t *testing.T) {
	m, err := httpx.NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	h := m.Middleware("gateway")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		_ = c.Write(r.Context(), websocket.MessageText, []byte("hola"))
		_ = c.Close(websocket.StatusNormalClosure, "")
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, res, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		code := 0
		if res != nil {
			code = res.StatusCode
		}
		t.Fatalf("dial: %v (HTTP %d)", err, code)
	}
	defer func() { _ = c.CloseNow() }()
	if _, msg, err := c.Read(ctx); err != nil || string(msg) != "hola" {
		t.Fatalf("read = %q, %v", msg, err)
	}
}
