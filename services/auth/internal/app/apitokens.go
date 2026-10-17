package app

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// Permiso y códigos de los API tokens.
const (
	PermAPITokensManage   = "api_tokens.manage"
	CodeAPITokenNotFound  = "API_TOKEN_NOT_FOUND"
	apiTokenExchangeTTL   = 2 * time.Minute
	apiTokenCacheMargin   = 30 * time.Second
	apiTokenTouchInterval = time.Minute
)

// APITokenStore es el repositorio de API tokens.
type APITokenStore interface {
	CreateAPIToken(ctx context.Context, t *domain.APIToken) error
	ListAPITokens(ctx context.Context, tenant uuid.UUID) ([]domain.APIToken, error)
	// RevokeAPIToken marca el token revocado y lo devuelve (domain.ErrNotFound
	// si no existe en el tenant o ya estaba revocado).
	RevokeAPIToken(ctx context.Context, tenant, id, by uuid.UUID, now time.Time) (*domain.APIToken, error)
	// APITokenByHash busca un token por su hash (sin tenant: canje en el borde).
	APITokenByHash(ctx context.Context, hash []byte) (*domain.APIToken, error)
	TouchAPIToken(ctx context.Context, id uuid.UUID, now time.Time, ip string) error
	UserByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
	Memberships(ctx context.Context, userID uuid.UUID) ([]domain.Membership, error)
}

// APITokens implementa los API tokens de integración (docs/security.md §5.4)
// y api.APITokenExchanger para el gateway.
type APITokens struct {
	store   APITokenStore
	signer  *authz.Signer
	catalog *domain.Catalog
	audit   api.AuditRecorder
	now     func() time.Time
	logger  *slog.Logger

	mu    sync.Mutex
	cache map[string]cachedExchange
}

type cachedExchange struct {
	token   string
	tokenID uuid.UUID
	exp     time.Time
}

// NewAPITokens crea el servicio. signer emite los access tokens canjeados
// (vida corta: 2 min).
func NewAPITokens(store APITokenStore, signer *authz.Signer, catalog *domain.Catalog, audit api.AuditRecorder,
	now func() time.Time, logger *slog.Logger,
) *APITokens {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &APITokens{store: store, signer: signer, catalog: catalog, audit: audit, now: now, logger: logger,
		cache: map[string]cachedExchange{}}
}

// APITokenInput es el cuerpo de POST /api-tokens.
type APITokenInput struct {
	Name        string     `json:"name"`
	Template    *string    `json:"template"`
	Permissions []string   `json:"permissions"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

// APITokenIssued es el token recién creado con su valor (se muestra una vez).
type APITokenIssued struct {
	Token domain.APIToken
	Raw   string
}

func apiTokenScope(ctx context.Context) (*authz.Principal, uuid.UUID, error) {
	p := authz.FromContext(ctx)
	tid, err := authz.TenantOf(ctx)
	if err != nil || p.Type == authz.TypeKiosk {
		return nil, uuid.Nil, apperr.Forbidden(problem.CodeTokenScopeInvalid, "")
	}
	if !p.Has(PermAPITokensManage) {
		return nil, uuid.Nil, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	return p, tid, nil
}

func (s *APITokens) record(ctx context.Context, p *authz.Principal, tenant uuid.UUID, action string, id uuid.UUID, changes map[string]any) {
	if s.audit == nil {
		s.logger.WarnContext(ctx, "api token audit not recorded", slog.String("action", action))
		return
	}
	if err := s.audit.Record(ctx, api.AuditEntry{TenantID: tenant, OccurredAt: s.now().UTC(), ActorType: p.Type, ActorID: p.Subject,
		ViaPlatform: p.ViaPlatform, Action: action, ResourceType: "api_token", ResourceID: id.String(), Scope: "tenant",
		Outcome: "success", Changes: changes}); err != nil {
		s.logger.ErrorContext(ctx, "api token audit failed", slog.String("action", action), slog.Any("error", err))
	}
}

// Create es POST /api-tokens: el token lleva un subconjunto de los permisos
// que el creador tiene ahora en el ISP (por plantilla o lista explícita).
func (s *APITokens) Create(ctx context.Context, in APITokenInput) (*APITokenIssued, error) {
	p, tid, err := apiTokenScope(ctx)
	if err != nil {
		return nil, err
	}
	if p.Type != authz.TypeUser || p.UserID == uuid.Nil || p.ViaPlatform {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "Solo un miembro del ISP puede crear API tokens a su nombre.")
	}
	var errs []problem.FieldError
	name := strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 80 {
		errs = append(errs, apperr.Field("name", "INVALID_VALUE", "Entre 1 y 80 caracteres."))
	}
	perms := slices.Clone(in.Permissions)
	var template *string
	if in.Template != nil {
		tpl, ok := domain.APITokenTemplateByKey(*in.Template)
		if !ok {
			errs = append(errs, apperr.Field("template", "INVALID_VALUE", "Plantilla desconocida."))
		} else {
			if len(perms) == 0 {
				perms = slices.Clone(tpl.Permissions)
			}
			k := tpl.Key
			template = &k
		}
	}
	slices.Sort(perms)
	perms = slices.Compact(perms)
	if len(perms) == 0 {
		errs = append(errs, apperr.Field("permissions", "REQUIRED", "Indique una plantilla o al menos un permiso."))
	}
	if len(perms) > 64 {
		errs = append(errs, apperr.Field("permissions", "TOO_MANY", "Máximo 64 permisos."))
	}
	for _, k := range perms {
		def, ok := s.catalog.Permission(k)
		switch {
		case !ok || def.Family != "tenant":
			errs = append(errs, apperr.Field("permissions", "INVALID_VALUE", "Permiso desconocido: "+k+"."))
		case k == PermAPITokensManage:
			errs = append(errs, apperr.Field("permissions", "NOT_ALLOWED", "Un API token no puede gestionar API tokens."))
		case !p.Has(k):
			errs = append(errs, apperr.Field("permissions", "NOT_GRANTED", "No tiene el permiso "+k+": un token solo lleva permisos de quien lo crea."))
		}
	}
	now := s.now().UTC()
	exp := now.Add(domain.APITokenDefaultTTL)
	if in.ExpiresAt != nil {
		exp = in.ExpiresAt.UTC()
		switch {
		case exp.Before(now.Add(domain.APITokenMinTTL)):
			errs = append(errs, apperr.Field("expires_at", "OUT_OF_RANGE", "Al menos una hora desde ahora."))
		case exp.After(now.Add(domain.APITokenMaxTTL)):
			errs = append(errs, apperr.Field("expires_at", "OUT_OF_RANGE", "Máximo un año desde ahora."))
		}
	}
	if len(errs) > 0 {
		return nil, apperr.Validation(errs...)
	}
	raw, err := domain.GenerateAPIToken()
	if err != nil {
		return nil, err
	}
	t := domain.APIToken{ID: uuid.Must(uuid.NewV7()), TenantID: tid, UserID: p.UserID, Name: name,
		Prefix: raw[:len(domain.APITokenPrefix)+4], Hash: domain.HashAPIToken(raw), Permissions: perms, Template: template,
		CreatedAt: now, ExpiresAt: exp}
	if err := s.store.CreateAPIToken(ctx, &t); err != nil {
		return nil, err
	}
	s.record(ctx, p, tid, "auth.api_token.created", t.ID, map[string]any{"name": name, "prefix": t.Prefix,
		"permissions": perms, "template": template, "expires_at": exp.Format(time.RFC3339)})
	return &APITokenIssued{Token: t, Raw: raw}, nil
}

// List es GET /api-tokens (todos los del ISP, sin valor ni hash).
func (s *APITokens) List(ctx context.Context) ([]domain.APIToken, error) {
	_, tid, err := apiTokenScope(ctx)
	if err != nil {
		return nil, err
	}
	return s.store.ListAPITokens(ctx, tid)
}

// Revoke es DELETE /api-tokens/{id}: deja de servir de inmediato (se borra
// su canje de la caché).
func (s *APITokens) Revoke(ctx context.Context, id uuid.UUID) error {
	p, tid, err := apiTokenScope(ctx)
	if err != nil {
		return err
	}
	by := p.UserID
	t, err := s.store.RevokeAPIToken(ctx, tid, id, by, s.now().UTC())
	if errors.Is(err, domain.ErrNotFound) {
		return apperr.NotFound(CodeAPITokenNotFound)
	}
	if err != nil {
		return err
	}
	s.forget(t.ID)
	s.record(ctx, p, tid, "auth.api_token.revoked", t.ID, map[string]any{"name": t.Name, "prefix": t.Prefix})
	return nil
}

func (s *APITokens) forget(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.cache {
		if v.tokenID == id {
			delete(s.cache, k)
		}
	}
}

// ExchangeAPIToken implementa api.APITokenExchanger.
func (s *APITokens) ExchangeAPIToken(ctx context.Context, raw, ip string) (string, error) {
	if !domain.LooksLikeAPIToken(raw) {
		return "", api.ErrAPITokenInvalid
	}
	hash := domain.HashAPIToken(raw)
	key := hex.EncodeToString(hash)
	now := s.now()
	s.mu.Lock()
	if c, ok := s.cache[key]; ok && now.Before(c.exp) {
		s.mu.Unlock()
		return c.token, nil
	}
	delete(s.cache, key)
	if len(s.cache) > 10000 { // tope de memoria: se vacía (los canjes se rehacen)
		s.cache = map[string]cachedExchange{}
	}
	s.mu.Unlock()

	t, err := s.store.APITokenByHash(ctx, hash)
	if errors.Is(err, domain.ErrNotFound) {
		return "", api.ErrAPITokenInvalid
	}
	if err != nil {
		return "", err
	}
	if !t.Active(now) {
		return "", api.ErrAPITokenInvalid
	}
	perms, err := s.currentPermissions(ctx, t)
	if err != nil {
		return "", err
	}
	c := authz.Claims{Typ: authz.TypeService, Scope: authz.ScopeTenant, TID: t.TenantID.String(), Perms: perms,
		Via: authz.ViaAPIToken, APITokenID: t.ID.String()}
	c.Subject = t.UserID.String()
	c.ID = fmt.Sprintf("api_token:%s:%s", t.ID, uuid.Must(uuid.NewV7()))
	tok, exp, err := s.signer.Sign(c)
	if err != nil {
		return "", err
	}
	if t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) > apiTokenTouchInterval || (t.LastUsedIP != nil && *t.LastUsedIP != ip) {
		if err := s.store.TouchAPIToken(ctx, t.ID, now.UTC(), ip); err != nil {
			s.logger.WarnContext(ctx, "api token touch failed", slog.Any("error", err))
		}
	}
	s.mu.Lock()
	s.cache[key] = cachedExchange{token: tok, tokenID: t.ID, exp: exp.Add(-apiTokenCacheMargin)}
	s.mu.Unlock()
	return tok, nil
}

// currentPermissions comprueba que el creador sigue activo en el ISP y
// devuelve los permisos del token que conserva, con sus alcances actuales.
func (s *APITokens) currentPermissions(ctx context.Context, t *domain.APIToken) (map[string][]string, error) {
	u, err := s.store.UserByID(ctx, t.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, api.ErrAPITokenInvalid
	}
	if err != nil {
		return nil, err
	}
	if u.Status != domain.UserActive {
		return nil, api.ErrAPITokenInvalid
	}
	ms, err := s.store.Memberships(ctx, t.UserID)
	if err != nil {
		return nil, err
	}
	for i := range ms {
		m := &ms[i]
		if m.Tenant.ID != t.TenantID {
			continue
		}
		if m.Status != domain.MembershipActive || m.Tenant.Status != domain.TenantActive {
			return nil, api.ErrAPITokenInvalid
		}
		perms := domain.IntersectPermissions(t.Permissions, m.EffectivePermissions())
		if len(perms) == 0 {
			return nil, api.ErrAPITokenInvalid
		}
		return perms, nil
	}
	return nil, api.ErrAPITokenInvalid
}

// APITokenTTL es la vida del access token canjeado.
func APITokenTTL() time.Duration { return apiTokenExchangeTTL }
