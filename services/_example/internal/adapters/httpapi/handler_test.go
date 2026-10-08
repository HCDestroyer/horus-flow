package httpapi_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/services/_example/internal/adapters/httpapi"
	"github.com/hcdestroyer/horus-flow/services/_example/internal/app"
)

type failingGreeter struct{}

func (failingGreeter) Hello(context.Context, string) (string, error) { return "", errors.New("down") }

func serve(t *testing.T, g httpapi.Greeter, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := httpx.NewMux(nil, nil)
	httpapi.NewHandler(g, slog.New(slog.DiscardHandler)).Mount(mux.ForService("example"))
	rec := httptest.NewRecorder()
	mux.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHello(t *testing.T) {
	t.Parallel()
	svc := app.NewService("hola")
	if rec := serve(t, svc, "/api/v1/example/hello/ana"); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"hola, ana"`) {
		t.Fatalf("200: %d %s", rec.Code, rec.Body.String())
	}
	if rec := serve(t, svc, "/api/v1/example/hello/"+strings.Repeat("x", 65)); rec.Code != http.StatusBadRequest {
		t.Fatalf("400: %d", rec.Code)
	}
	if rec := serve(t, failingGreeter{}, "/api/v1/example/hello/ana"); rec.Code != http.StatusInternalServerError ||
		strings.Contains(rec.Body.String(), "down") {
		t.Fatalf("500: %d %s", rec.Code, rec.Body.String())
	}
}
