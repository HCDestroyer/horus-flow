package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/mask"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/tools"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

// TokenPrefix identifica los tokens MCP de Horus (ayuda a los escáneres de secretos).
const TokenPrefix = "hmcp_"

// MCPTokenView es un token MCP sin su valor.
type MCPTokenView struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	Prefix     string    `json:"prefix"`
	UserID     uuid.UUID `json:"user_id"`
	CreatedAt  string    `json:"created_at"`
	ExpiresAt  string    `json:"expires_at"`
	LastUsedAt *string   `json:"last_used_at"`
	RevokedAt  *string   `json:"revoked_at"`
	Active     bool      `json:"active"`
}

// MCPTokenIssued es la respuesta de POST /assist/mcp-tokens (el valor se muestra una vez).
type MCPTokenIssued struct {
	MCPTokenView
	Token string `json:"token"`
}

func ts(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func tsPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := ts(*t)
	return &s
}

func (s *Service) tokenView(t *domain.MCPToken) MCPTokenView {
	return MCPTokenView{ID: t.ID, Name: t.Name, Prefix: t.Prefix, UserID: t.UserID, CreatedAt: ts(t.CreatedAt),
		ExpiresAt: ts(t.ExpiresAt), LastUsedAt: tsPtr(t.LastUsedAt), RevokedAt: tsPtr(t.RevokedAt), Active: t.Active(s.now())}
}

func hashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// CreateMCPToken es POST /assist/mcp-tokens: el token actúa como quien lo crea
// (con los permisos de lectura que tenga en cada momento).
func (s *Service) CreateMCPToken(ctx context.Context, name string) (*MCPTokenIssued, error) {
	p, err := principal(ctx, PermMCPTokens)
	if err != nil {
		return nil, err
	}
	if p.UserID == uuid.Nil || p.ViaPlatform {
		return nil, apperr.Forbidden("PERMISSION_DENIED", "Solo un miembro del ISP puede crear tokens MCP a su nombre.")
	}
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 80 {
		return nil, apperr.Validation(apperr.Field("name", "INVALID_VALUE", "Entre 1 y 80 caracteres."))
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	raw := TokenPrefix + base64.RawURLEncoding.EncodeToString(buf)
	now := s.now()
	t := &domain.MCPToken{ID: uuid.Must(uuid.NewV7()), TenantID: p.TenantID, UserID: p.UserID, Name: name, Hash: hashToken(raw),
		Prefix: raw[:len(TokenPrefix)+4], CreatedAt: now, ExpiresAt: now.Add(s.o.MCPTokenTTL)}
	if err := s.o.Store.CreateMCPToken(ctx, t); err != nil {
		return nil, err
	}
	s.auditChange(ctx, p, "assist.mcp_token.created", "assist_mcp_token", t.ID.String(), map[string]any{"name": name, "prefix": t.Prefix})
	return &MCPTokenIssued{MCPTokenView: s.tokenView(t), Token: raw}, nil
}

// ListMCPTokens es GET /assist/mcp-tokens.
func (s *Service) ListMCPTokens(ctx context.Context) ([]MCPTokenView, error) {
	p, err := principal(ctx, PermMCPTokens)
	if err != nil {
		return nil, err
	}
	list, err := s.o.Store.ListMCPTokens(ctx, p.TenantID)
	if err != nil {
		return nil, err
	}
	out := make([]MCPTokenView, 0, len(list))
	for i := range list {
		out = append(out, s.tokenView(&list[i]))
	}
	return out, nil
}

// RevokeMCPToken es DELETE /assist/mcp-tokens/{token_id}.
func (s *Service) RevokeMCPToken(ctx context.Context, id uuid.UUID) error {
	p, err := principal(ctx, PermMCPTokens)
	if err != nil {
		return err
	}
	if err := s.o.Store.RevokeMCPToken(ctx, p.TenantID, id, s.now()); err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound(CodeTokenNotFound)
		}
		return err
	}
	s.auditChange(ctx, p, "assist.mcp_token.revoked", "assist_mcp_token", id.String(), nil)
	return nil
}

// MCPSession es la identidad de una petición MCP autenticada.
type MCPSession struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
	TokenID  uuid.UUID
}

// Errores de autenticación MCP.
var (
	ErrMCPUnauthenticated = errors.New("assist: mcp unauthenticated")
	ErrMCPUnavailable     = errors.New("assist: mcp unavailable")
)

// AuthenticateMCP valida un token MCP (Authorization: Bearer hmcp_…).
func (s *Service) AuthenticateMCP(ctx context.Context, raw string) (*MCPSession, error) {
	if s.o.Delegation == nil {
		return nil, ErrMCPUnavailable
	}
	if !strings.HasPrefix(raw, TokenPrefix) || len(raw) > 200 {
		return nil, ErrMCPUnauthenticated
	}
	t, err := s.o.Store.MCPTokenByHash(ctx, hashToken(raw))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrMCPUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	if !t.Active(now) {
		return nil, ErrMCPUnauthenticated
	}
	if t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) > time.Minute {
		if err := s.o.Store.TouchMCPToken(ctx, t.ID, now); err != nil {
			s.o.Logger.WarnContext(ctx, "assist: mcp token touch failed", "err", err)
		}
	}
	return &MCPSession{TenantID: t.TenantID, UserID: t.UserID, TokenID: t.ID}, nil
}

// mcpPrincipal obtiene de auth un token delegado (2 min, solo los permisos de
// lectura de las herramientas ∩ los actuales del usuario) y lo convierte en
// el principal con el que el lector llama a la API.
func (s *Service) mcpPrincipal(ctx context.Context, sess *MCPSession) (*authz.Principal, error) {
	tok, err := s.o.Delegation.DelegatedToken(ctx, authapi.DelegationRequest{
		UserID: sess.UserID, TenantID: sess.TenantID, Permissions: s.o.Registry.Permissions(), Purpose: "assist-mcp",
	})
	if errors.Is(err, authapi.ErrDelegationDenied) {
		return nil, ErrMCPUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	return &authz.Principal{Type: authz.TypeService, Subject: sess.UserID.String(), Scope: authz.ScopeTenant,
		TenantID: sess.TenantID, Raw: tok}, nil
}

// MCPEnabled indica si el asistente está activo para el ISP de la sesión.
func (s *Service) MCPEnabled(ctx context.Context, sess *MCPSession) (bool, error) {
	st, err := s.effective(ctx, sess.TenantID)
	if err != nil {
		return false, err
	}
	return st.Enabled, nil
}

// MCPCall ejecuta una herramienta para el servidor MCP: mismo registro,
// mismo enmascarado y auditoría (feature "mcp"). No cuenta para el límite
// diario (no hay llamada al proveedor de IA de Horus), pero exige el
// asistente activado en el ISP.
func (s *Service) MCPCall(ctx context.Context, sess *MCPSession, m *mask.Masker, name string, args []byte) (string, error) {
	start := s.o.Now()
	entry := &domain.AuditEntry{TenantID: sess.TenantID, UserID: &sess.UserID, Feature: domain.FeatureMCP, ContextID: name,
		Provider: "mcp"}
	enabled, err := s.MCPEnabled(ctx, sess)
	if err != nil {
		return "", err
	}
	if !enabled {
		entry.Outcome, entry.ErrorCode = domain.OutcomeDenied, CodeDisabled
		s.record(ctx, entry)
		return "", tools.Public("El asistente de IA no está activado en este ISP.")
	}
	p, err := s.mcpPrincipal(ctx, sess)
	if err != nil {
		entry.Outcome, entry.ErrorCode = domain.OutcomeDenied, "DELEGATION_DENIED"
		s.record(ctx, entry)
		return "", tools.Public("El token ya no puede actuar en nombre de su usuario.")
	}
	out, cerr := s.o.Registry.Call(ctx, tools.Env{Reader: s.o.Readers(p), Masker: m}, name, args)
	entry.DurationMS = int(s.o.Now().Sub(start).Milliseconds())
	entry.ToolCalls = []domain.ToolUse{{Name: name, Count: 1}}
	entry.Outcome = domain.OutcomeSuccess
	if cerr != nil {
		entry.ToolCalls[0].Errors = 1
		entry.Outcome, entry.ErrorCode = domain.OutcomeError, "TOOL_ERROR"
	}
	s.record(ctx, entry)
	return out, cerr
}
