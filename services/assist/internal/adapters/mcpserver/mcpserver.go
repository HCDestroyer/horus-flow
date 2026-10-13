// Package mcpserver publica el registro de herramientas del asistente como un
// servidor MCP (Model Context Protocol) sobre Streamable HTTP en
// POST /api/v1/assist/mcp (docs/assist.md §MCP), para Claude Desktop u otros
// clientes MCP.
//
// Autenticación: `Authorization: Bearer hmcp_…` (token MCP creado en
// Administración › Inteligencia artificial; solo se guarda su hash). Cada
// petición es sin estado: se construye un servidor MCP ligado a la identidad
// del token y las herramientas leen la API como su usuario (token delegado de
// auth, 2 min), con el mismo enmascarado y auditoría (feature "mcp").
package mcpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/app"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/mask"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/prompt"
)

// Path es la ruta del servidor MCP.
const Path = "/api/v1/assist/mcp"

// Version del servidor que se anuncia a los clientes.
const Version = "1.0.0"

type ctxKey struct{}

// Server sirve el endpoint MCP.
type Server struct {
	svc    *app.Service
	logger *slog.Logger
	h      http.Handler
}

// New crea el servidor.
func New(svc *app.Service, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	s := &Server{svc: svc, logger: logger}
	s.h = mcp.NewStreamableHTTPHandler(s.serverFor, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, Logger: logger, MaxRequestBodyBytes: 1 << 20,
	})
	return s
}

// Mount registra la ruta.
func (s *Server) Mount(r *httpx.ServiceMux) {
	r.Handle("POST /api/v1/assist/mcp", s)
}

// ServeHTTP autentica el token MCP y delega en el handler Streamable HTTP.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw := authz.BearerToken(r)
	if raw == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="horus-mcp"`)
		problem.Std(w, r, http.StatusUnauthorized, problem.CodeUnauthenticated)
		return
	}
	sess, err := s.svc.AuthenticateMCP(r.Context(), raw)
	switch {
	case errors.Is(err, app.ErrMCPUnavailable):
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable,
			problem.WithDetail("El servidor MCP necesita el rol auth en el mismo proceso."))
		return
	case err != nil:
		if !errors.Is(err, app.ErrMCPUnauthenticated) {
			s.logger.ErrorContext(r.Context(), "assist: mcp auth failed", slog.Any("error", err))
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="horus-mcp", error="invalid_token"`)
		problem.Std(w, r, http.StatusUnauthorized, problem.CodeUnauthenticated)
		return
	}
	ctx := context.WithValue(r.Context(), ctxKey{}, sess)
	ctx = observability.WithTenant(ctx, sess.TenantID.String())
	s.h.ServeHTTP(w, r.WithContext(ctx))
}

// serverFor construye el servidor MCP de una petición (sin estado), ligado a
// la identidad del token.
func (s *Server) serverFor(r *http.Request) *mcp.Server {
	sess, _ := r.Context().Value(ctxKey{}).(*app.MCPSession)
	srv := mcp.NewServer(&mcp.Implementation{Name: "horus-flow", Title: "Horus Flow", Version: Version},
		&mcp.ServerOptions{Instructions: instructions, Logger: s.logger})
	if sess == nil {
		return srv
	}
	masker := mask.New()
	for _, spec := range s.svc.Registry().Specs() {
		name := spec.Name
		srv.AddTool(&mcp.Tool{Name: name, Description: spec.Description, InputSchema: spec.Schema,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)}},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				out, err := s.svc.MCPCall(ctx, sess, masker, name, req.Params.Arguments)
				if err != nil {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil
			})
	}
	return srv
}

func ptr[T any](v T) *T { return &v }

// instructions resume para el cliente MCP las reglas del prompt del asistente.
var instructions = "Herramientas de solo lectura de Horus Flow (monitoreo de un ISP). Los datos de clientes llegan " +
	"seudonimizados. Horus nunca escribe en los routers. Al responder, sigue estas reglas:\n\n" + prompt.System
