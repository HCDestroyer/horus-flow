// Package loopback implementa tools.Reader llamando en proceso a la API REST
// de Horus como el usuario del análisis (docs/assist.md §Cómo llegan las
// herramientas a los datos): la petición GET lleva el Bearer del usuario (o
// el token delegado del servidor MCP) y atraviesa el borde del gateway y el
// handler del módulo dueño, de modo que permisos, alcances por nodo, RLS,
// visibilidad de datos personales y auditoría de accesos se aplican igual que
// a una petición del navegador. Sin red ni credenciales propias.
package loopback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hcdestroyer/horus-flow/services/assist/internal/tools"
)

// maxBody acota la respuesta leída de la API (protege la memoria).
const maxBody = 2 << 20

// Reader lee la API del proceso con un Bearer.
type Reader struct {
	h       http.Handler
	token   string
	timeout time.Duration
}

// New crea un lector que llama a h con token.
func New(h http.Handler, token string) *Reader {
	return &Reader{h: h, token: token, timeout: 20 * time.Second}
}

// Get implementa tools.Reader.
func (r *Reader) Get(ctx context.Context, path string, query url.Values) (any, error) {
	if !strings.HasPrefix(path, "/api/v1/") || strings.Contains(path, "..") {
		return nil, fmt.Errorf("loopback: invalid path %q", path)
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("loopback: %w", err)
	}
	req.RemoteAddr = "127.0.0.1:0"
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "horus-assist")
	rec := &recorder{header: http.Header{}}
	r.h.ServeHTTP(rec, req)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}
	if status < 200 || status > 299 {
		var p struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(rec.body.Bytes(), &p)
		return nil, &tools.HTTPError{Status: status, Code: p.Code}
	}
	if rec.overflow {
		return nil, &tools.HTTPError{Status: http.StatusBadGateway, Code: "RESPONSE_TOO_LARGE"}
	}
	var out any
	if err := json.Unmarshal(rec.body.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("loopback: decode %s: %w", path, err)
	}
	return out, nil
}

// recorder es un http.ResponseWriter en memoria con tope de tamaño.
type recorder struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (w *recorder) Header() http.Header { return w.header }

func (w *recorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

func (w *recorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len()+len(b) > maxBody {
		w.overflow = true
		return len(b), nil
	}
	return w.body.Write(b)
}
