// Package httpapi es el adaptador REST del módulo.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/services/_example/internal/domain"
)

// Greeter es el puerto que consume el handler.
type Greeter interface {
	Hello(ctx context.Context, name string) (string, error)
}

// Handler sirve las rutas del módulo.
type Handler struct {
	svc    Greeter
	logger *slog.Logger
}

// NewHandler crea el handler.
func NewHandler(svc Greeter, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// Mount registra las rutas en la API del proceso.
func (h *Handler) Mount(r *httpx.ServiceMux) {
	r.HandleFunc("GET /api/v1/example/hello/{name}", h.hello)
}

func (h *Handler) hello(w http.ResponseWriter, r *http.Request) {
	msg, err := h.svc.Hello(r.Context(), r.PathValue("name"))
	switch {
	case errors.Is(err, domain.ErrInvalidName):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
		return
	case err != nil:
		h.logger.ErrorContext(r.Context(), "hello failed", slog.Any("error", err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v) // el cliente puede haber cerrado
}
