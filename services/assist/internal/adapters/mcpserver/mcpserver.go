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
	"encoding/json"
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
	"github.com/hcdestroyer/horus-flow/services/assist/internal/tools"
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
		srv.AddTool(&mcp.Tool{Name: name, Description: spec.Description, InputSchema: spec.Schema, OutputSchema: envelopeSchema,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false), IdempotentHint: true}},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				out, err := s.svc.MCPCall(ctx, sess, masker, name, req.Params.Arguments)
				if err != nil {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
				}
				// El sobre va como texto y como contenido estructurado (SEP-2106).
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}, StructuredContent: json.RawMessage(out)}, nil
			})
	}
	// Prompts: el mismo mensaje anclado (datos primarios + catálogo) que usa la app.
	for _, pr := range prompts {
		pr := pr
		srv.AddPrompt(&mcp.Prompt{Name: pr.name, Title: pr.title, Description: pr.description,
			Arguments: []*mcp.PromptArgument{{Name: pr.arg, Description: pr.argDesc, Required: true}}},
			func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				user, err := s.svc.MCPPrompt(ctx, sess, masker, pr.feature, req.Params.Arguments[pr.arg])
				if err != nil {
					return nil, err
				}
				return &mcp.GetPromptResult{Description: pr.description, Messages: []*mcp.PromptMessage{
					{Role: "user", Content: &mcp.TextContent{Text: instructions + "\n\n---\n\n" + user}},
				}}, nil
			})
	}
	// Recursos de referencia: guía (prompt del asistente), glosario y catálogo RouterOS.
	for _, r := range resources {
		r := r
		srv.AddResource(&mcp.Resource{URI: r.uri, Name: r.name, Title: r.title, Description: r.description, MIMEType: "text/markdown"},
			func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: r.uri, MIMEType: "text/markdown", Text: r.text()}}}, nil
			})
	}
	return srv
}

func ptr[T any](v T) *T { return &v }

// envelopeSchema describe el sobre común de todas las herramientas (tools.Envelope).
var envelopeSchema = map[string]any{
	"type":     "object",
	"required": []string{"tool", "as_of", "facts", "missing", "units", "data"},
	"properties": map[string]any{
		"tool":      map[string]any{"type": "string"},
		"as_of":     map[string]any{"type": "string", "description": "Hora de la consulta (UTC, ISO 8601)."},
		"facts":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Hechos con valores exactos; cítalos como [F1], [F2]…"},
		"missing":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Lo que no se pudo obtener: no lo supongas."},
		"units":     map[string]any{"type": "string"},
		"data":      map[string]any{},
		"truncated": map[string]any{"type": "boolean"},
	},
}

type promptDef struct{ name, title, description, arg, argDesc, feature string }

var prompts = []promptDef{
	{name: "analizar_hallazgo", title: "Analizar un hallazgo", feature: "finding", arg: "finding_id", argDesc: "UUID del hallazgo.",
		description: "Análisis anclado de un hallazgo de seguridad: incluye los datos del hallazgo y su evidencia ya consultados (con hechos [F#]) y el catálogo de comandos RouterOS verificados."},
	{name: "diagnosticar_router", title: "Diagnosticar un router", feature: "router", arg: "router_id", argDesc: "UUID del router.",
		description: "Diagnóstico anclado de un router: incluye su ficha, túnel WireGuard y exportador ya consultados (con hechos [F#]) y el catálogo de comandos RouterOS de diagnóstico."},
}

type resourceDef struct {
	uri, name, title, description string
	text                          func() string
}

var resources = []resourceDef{
	{uri: "horus://guia", name: "guia", title: "Guía del asistente", description: "Contexto del dominio, reglas de veracidad y formato de respuesta.",
		text: func() string { return prompt.System }},
	{uri: "horus://glosario", name: "glosario", title: "Glosario de Horus Flow", description: "Términos y estados que devuelven las herramientas.",
		text: func() string { return prompt.Glossary }},
	{uri: "horus://routeros", name: "routeros", title: "Catálogo RouterOS verificado", description: "Plantillas de comandos RouterOS v7 con su deshacer; las únicas de escritura admitidas.",
		text: func() string { return tools.CatalogMarkdown() }},
}

// instructions resume para el cliente MCP las reglas del prompt del asistente.
var instructions = "Herramientas de solo lectura de Horus Flow (monitoreo de un ISP). Cada herramienta devuelve un sobre " +
	"{tool, as_of, facts[], missing[], units, data}: usa facts como fuente citable ([F1], [F2]…), trata missing como " +
	"ausente (no lo supongas) y no escribas cifras, IP, ASN ni puertos que no estén en el sobre. Los datos de clientes " +
	"llegan seudonimizados. Horus nunca escribe en los routers: los comandos RouterOS salen solo del catálogo " +
	"routeros_commands (recurso horus://routeros). Para un análisis completo usa los prompts analizar_hallazgo o " +
	"diagnosticar_router, que ya incluyen los datos. Reglas completas:\n\n" + prompt.System
