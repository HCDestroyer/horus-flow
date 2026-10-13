// Package app contiene los casos de uso del asistente de IA (docs/assist.md):
// configuración del proveedor por ISP (clave de API write-only y cifrada),
// prueba de conexión, análisis con herramientas de solo lectura sobre datos
// enmascarados, límite diario por ISP, auditoría de cada consulta y tokens del
// servidor MCP.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/agent"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/grounding"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/mask"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/prompt"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/provider"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/tools"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

// Permisos.
const (
	PermSettingsRead   = "settings.read"
	PermSettingsManage = "settings.manage"
	PermFindingsRead   = "security.findings.read"
	PermDevicesRead    = "devices.read"
	// PermMCPTokens gestiona los tokens MCP (permiso existente de API tokens del ISP).
	PermMCPTokens = "api_tokens.manage"
)

// Códigos de error del contrato (assist.yaml).
const (
	CodeDisabled            = "ASSIST_DISABLED"
	CodeNotConfigured       = "ASSIST_NOT_CONFIGURED"
	CodeLimitReached        = "ASSIST_LIMIT_REACHED"
	CodeProviderUnavailable = "ASSIST_PROVIDER_UNAVAILABLE"
	CodeRefused             = "ASSIST_REFUSED"
	CodeIncomplete          = "ASSIST_INCOMPLETE"
	CodeContextNotFound     = "ASSIST_CONTEXT_NOT_FOUND"
	// CodeContextUnavailable: la API de Horus no pudo servir el contexto (otro módulo caído o degradado).
	CodeContextUnavailable = "ASSIST_CONTEXT_UNAVAILABLE"
	// CodeStoreUnavailable: el almacén del asistente (PostgreSQL) no responde.
	CodeStoreUnavailable    = "ASSIST_STORE_UNAVAILABLE"
	CodeKeyStoreUnavailable = "ASSIST_KEY_STORE_UNAVAILABLE"
	CodeTokenNotFound       = "MCP_TOKEN_NOT_FOUND"
)

// ErrNotFound lo devuelve el Store cuando no hay fila.
var ErrNotFound = errors.New("assist: not found")

// Store es la persistencia (assist.*).
type Store interface {
	GetSettings(ctx context.Context, tenant uuid.UUID) (*domain.Settings, error)
	PutSettings(ctx context.Context, s *domain.Settings) (*domain.Settings, error)
	RecordAudit(ctx context.Context, e *domain.AuditEntry) error
	CountRequests(ctx context.Context, tenant uuid.UUID, since time.Time) (int, error)
	ListAudit(ctx context.Context, tenant uuid.UUID, limit int, before *AuditCursor) ([]domain.AuditEntry, error)
	CreateMCPToken(ctx context.Context, t *domain.MCPToken) error
	ListMCPTokens(ctx context.Context, tenant uuid.UUID) ([]domain.MCPToken, error)
	RevokeMCPToken(ctx context.Context, tenant, id uuid.UUID, at time.Time) error
	// MCPTokenByHash busca un token por su hash sin tenant (rol de plataforma).
	MCPTokenByHash(ctx context.Context, hash []byte) (*domain.MCPToken, error)
	TouchMCPToken(ctx context.Context, id uuid.UUID, at time.Time) error
}

// AuditCursor pagina la auditoría (created_at, id descendentes).
type AuditCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// Sealer cifra las claves de API (envelope.Sealer).
type Sealer interface {
	Seal(plaintext, aad []byte) (ciphertext, dekWrapped []byte, err error)
	Open(ciphertext, dekWrapped, aad []byte) ([]byte, error)
	KEKID() string
}

// ProviderConfig es lo necesario para construir un proveedor.
type ProviderConfig struct {
	Provider string
	Model    string
	BaseURL  string
	APIKey   string //nolint:gosec // secreto en memoria; nunca se serializa
	Effort   string
}

// ProviderFactory construye el adaptador del proveedor.
type ProviderFactory func(c ProviderConfig) (provider.Provider, error)

// ReaderFactory devuelve un lector de la API de Horus que actúa como p.
type ReaderFactory func(p *authz.Principal) tools.Reader

// Defaults es la configuración de plataforma (entorno).
type Defaults struct {
	Enabled  bool
	Provider string
	Model    string
	BaseURL  string
	APIKey   string //nolint:gosec // secreto en memoria; nunca se serializa
	Effort   string
	Limit    int
}

// Options del servicio.
type Options struct {
	Store     Store
	Sealer    Sealer // nil = no se pueden guardar claves por ISP
	Providers ProviderFactory
	Readers   ReaderFactory
	Registry  *tools.Registry
	Defaults  Defaults
	Audit     authapi.AuditRecorder // auditoría de cambios de configuración (opcional)
	// Delegation emite tokens para el servidor MCP (auth en el mismo proceso).
	Delegation      authapi.Delegator
	MaxIterations   int
	RequestTimeout  time.Duration
	MaxOutputTokens int64
	MCPTokenTTL     time.Duration
	Now             func() time.Time
	Logger          *slog.Logger
}

// Service implementa los casos de uso.
type Service struct {
	o Options
}

// New crea el servicio.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.MaxIterations <= 0 {
		o.MaxIterations = 8
	}
	if o.RequestTimeout <= 0 {
		o.RequestTimeout = 120 * time.Second
	}
	if o.MaxOutputTokens <= 0 {
		o.MaxOutputTokens = 16000
	}
	if o.MCPTokenTTL <= 0 {
		o.MCPTokenTTL = 90 * 24 * time.Hour
	}
	if o.Defaults.Provider == "" {
		o.Defaults.Provider = domain.ProviderAnthropic
	}
	if o.Defaults.Model == "" {
		o.Defaults.Model = domain.DefaultModels[o.Defaults.Provider]
	}
	if o.Defaults.Effort == "" {
		o.Defaults.Effort = "medium"
	}
	if o.Defaults.Limit <= 0 {
		o.Defaults.Limit = 100
	}
	return &Service{o: o}
}

// Registry devuelve el registro de herramientas.
func (s *Service) Registry() *tools.Registry { return s.o.Registry }

func (s *Service) now() time.Time { return s.o.Now().UTC() }

// principal devuelve el principal de tenant de la petición con perm.
func principal(ctx context.Context, perm string) (*authz.Principal, error) {
	p := authz.FromContext(ctx)
	if p == nil {
		return nil, apperr.New(apperr.KindUnauthorized, problem.CodeUnauthenticated, "")
	}
	if p.Scope != authz.ScopeTenant || p.TenantID == uuid.Nil {
		return nil, apperr.Forbidden(problem.CodeTokenScopeInvalid, "")
	}
	if perm != "" && !p.Has(perm) {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	return p, nil
}

func userOf(p *authz.Principal) *uuid.UUID {
	if p == nil || p.UserID == uuid.Nil {
		return nil
	}
	id := p.UserID
	return &id
}

func aad(tenant uuid.UUID) []byte { return []byte("assist_api_key:" + tenant.String()) }

// effective devuelve la configuración del ISP o la de plataforma.
func (s *Service) effective(ctx context.Context, tenant uuid.UUID) (*domain.Settings, error) {
	st, err := s.o.Store.GetSettings(ctx, tenant)
	if errors.Is(err, ErrNotFound) {
		d := s.o.Defaults
		return &domain.Settings{TenantID: tenant, Enabled: d.Enabled, Provider: d.Provider, Model: d.Model, BaseURL: d.BaseURL,
			Effort: d.Effort, DailyRequestLimit: d.Limit}, nil
	}
	if err != nil {
		return nil, s.storeErr(ctx, "settings", err)
	}
	return st, nil
}

// storeErr traduce un fallo del almacén a 503 con su código (nunca un 500
// sin pista) y lo registra con la operación.
func (s *Service) storeErr(ctx context.Context, op string, err error) error {
	if _, ok := apperr.As(err); ok {
		return err
	}
	s.o.Logger.ErrorContext(ctx, "assist: store unavailable", slog.String("op", op), slog.Any("error", err))
	return apperr.New(apperr.KindUnavailable, CodeStoreUnavailable, "El almacén del asistente no responde; inténtalo de nuevo en unos segundos.")
}

// platformKeyFor indica si la clave de plataforma sirve para provider.
func (s *Service) platformKeyFor(provider string) bool {
	return s.o.Defaults.APIKey != "" && provider == s.o.Defaults.Provider
}

// apiKey devuelve la clave usable de st (la del ISP o la de plataforma).
func (s *Service) apiKey(st *domain.Settings) (string, string, error) {
	if st.Sealed != nil {
		if s.o.Sealer == nil {
			return "", "", apperr.New(apperr.KindUnavailable, CodeKeyStoreUnavailable,
				"La clave del ISP no se puede descifrar: falta HORUS_ASSIST_KEK_FILE.")
		}
		pt, err := s.o.Sealer.Open(st.Sealed.Ciphertext, st.Sealed.DEKWrapped, aad(st.TenantID))
		if err != nil {
			return "", "", apperr.New(apperr.KindUnavailable, CodeKeyStoreUnavailable,
				"La clave del ISP no se puede descifrar (¿cambió la KEK?). Vuelve a guardarla.")
		}
		return string(pt), "tenant", nil
	}
	if s.platformKeyFor(st.Provider) {
		return s.o.Defaults.APIKey, "platform", nil
	}
	return "", "none", nil
}

// SettingsView es la respuesta de GET/PUT /assist/settings (sin la clave).
type SettingsView struct {
	Enabled           bool       `json:"enabled"`
	Provider          string     `json:"provider"`
	Model             string     `json:"model"`
	BaseURL           *string    `json:"base_url"`
	Effort            string     `json:"effort"`
	DailyRequestLimit int        `json:"daily_request_limit"`
	APIKeySet         bool       `json:"api_key_set"`
	APIKeySource      string     `json:"api_key_source"`
	Source            string     `json:"source"`
	UsageToday        int        `json:"usage_today"`
	KeyStoreAvailable bool       `json:"key_store_available"`
	PlatformDefault   PlatformDT `json:"platform_default"`
	MCPEnabled        bool       `json:"mcp_enabled"`
	UpdatedAt         *string    `json:"updated_at"`
	UpdatedBy         *uuid.UUID `json:"updated_by"`
	Version           int        `json:"version"`
}

// PlatformDT resume el valor por defecto de plataforma.
type PlatformDT struct {
	Enabled   bool   `json:"enabled"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	APIKeySet bool   `json:"api_key_set"`
}

func (s *Service) view(ctx context.Context, st *domain.Settings) (*SettingsView, error) {
	used, err := s.o.Store.CountRequests(ctx, st.TenantID, domain.StartOfDay(s.now()))
	if err != nil {
		return nil, err
	}
	v := &SettingsView{
		Enabled: st.Enabled, Provider: st.Provider, Model: st.Model, Effort: st.Effort, DailyRequestLimit: st.DailyRequestLimit,
		APIKeySet: st.Sealed != nil, UsageToday: used, KeyStoreAvailable: s.o.Sealer != nil, Version: st.Version,
		UpdatedBy: st.UpdatedBy, MCPEnabled: s.o.Delegation != nil,
		PlatformDefault: PlatformDT{Enabled: s.o.Defaults.Enabled, Provider: s.o.Defaults.Provider, Model: s.o.Defaults.Model,
			APIKeySet: s.o.Defaults.APIKey != ""},
	}
	switch {
	case st.Sealed != nil:
		v.APIKeySource = "tenant"
	case s.platformKeyFor(st.Provider):
		v.APIKeySource = "platform"
	default:
		v.APIKeySource = "none"
	}
	if st.BaseURL != "" {
		b := st.BaseURL
		v.BaseURL = &b
	}
	v.Source = "platform"
	if st.Stored {
		v.Source = "tenant"
		ts := st.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z")
		v.UpdatedAt = &ts
	}
	return v, nil
}

// Settings es GET /assist/settings.
func (s *Service) Settings(ctx context.Context) (*SettingsView, error) {
	p, err := principal(ctx, PermSettingsRead)
	if err != nil {
		return nil, err
	}
	st, err := s.effective(ctx, p.TenantID)
	if err != nil {
		return nil, err
	}
	return s.view(ctx, st)
}

// Status es lo mínimo que necesita la UI para mostrar u ocultar los botones
// (GET /assist/status: cualquier miembro del ISP).
type Status struct {
	Enabled   bool   `json:"enabled"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Remaining int    `json:"remaining_today"`
}

// Status es GET /assist/status.
func (s *Service) Status(ctx context.Context) (*Status, error) {
	p, err := principal(ctx, "")
	if err != nil {
		return nil, err
	}
	st, err := s.effective(ctx, p.TenantID)
	if err != nil {
		return nil, err
	}
	used, err := s.o.Store.CountRequests(ctx, p.TenantID, domain.StartOfDay(s.now()))
	if err != nil {
		return nil, err
	}
	enabled := st.Enabled
	if enabled && st.Provider == domain.ProviderAnthropic && st.Sealed == nil && !s.platformKeyFor(st.Provider) {
		enabled = false
	}
	return &Status{Enabled: enabled, Provider: st.Provider, Model: st.Model, Remaining: max(0, st.DailyRequestLimit-used)}, nil
}

func validationErr(err error) error {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		fe := make([]problem.FieldError, len(ve.Fields))
		for i, f := range ve.Fields {
			fe[i] = apperr.Field(f.Field, f.Code, f.Message)
		}
		return apperr.Validation(fe...)
	}
	return err
}

// UpdateSettings es PUT /assist/settings.
func (s *Service) UpdateSettings(ctx context.Context, upd domain.Update) (*SettingsView, error) {
	p, err := principal(ctx, PermSettingsManage)
	if err != nil {
		return nil, err
	}
	upd.Normalize()
	cur, err := s.effective(ctx, p.TenantID)
	if err != nil {
		return nil, err
	}
	sealed := cur.Sealed
	// Una clave guardada es del proveedor para el que se guardó: al cambiar de
	// proveedor sin clave nueva se descarta (no se envía la de Claude a otro servidor).
	if upd.ClearAPIKey || (cur.Stored && cur.Provider != upd.Provider) {
		sealed = nil
	}
	if upd.APIKey != nil && s.o.Sealer == nil {
		return nil, apperr.New(apperr.KindUnavailable, CodeKeyStoreUnavailable,
			"No se pueden guardar claves por ISP: falta HORUS_ASSIST_KEK_FILE en el servidor.")
	}
	keyAvailable := sealed != nil || upd.APIKey != nil || s.platformKeyFor(upd.Provider)
	if err := upd.Validate(keyAvailable); err != nil {
		return nil, validationErr(err)
	}
	if upd.APIKey != nil {
		ct, dek, err := s.o.Sealer.Seal([]byte(*upd.APIKey), aad(p.TenantID))
		if err != nil {
			return nil, fmt.Errorf("assist: seal api key: %w", err)
		}
		sealed = &domain.SealedKey{Ciphertext: ct, DEKWrapped: dek, KEKID: s.o.Sealer.KEKID()}
	}
	base := ""
	if upd.BaseURL != nil && upd.Provider == domain.ProviderOpenAICompatible {
		base = *upd.BaseURL
	}
	next := &domain.Settings{
		TenantID: p.TenantID, Enabled: upd.Enabled, Provider: upd.Provider, Model: upd.Model, BaseURL: base, Effort: upd.Effort,
		DailyRequestLimit: upd.DailyRequestLimit, Sealed: sealed, UpdatedAt: s.now(), UpdatedBy: userOf(p),
	}
	saved, err := s.o.Store.PutSettings(ctx, next)
	if err != nil {
		return nil, err
	}
	s.auditChange(ctx, p, "assist.settings.updated", "assist_settings", p.TenantID.String(), map[string]any{
		"enabled": upd.Enabled, "provider": upd.Provider, "model": upd.Model, "effort": upd.Effort,
		"daily_request_limit": upd.DailyRequestLimit, "api_key_changed": upd.APIKey != nil || upd.ClearAPIKey,
	})
	return s.view(ctx, saved)
}

// auditChange registra un cambio de configuración en la auditoría del ISP (auth).
func (s *Service) auditChange(ctx context.Context, p *authz.Principal, action, rtype, rid string, changes map[string]any) {
	if s.o.Audit == nil {
		return
	}
	err := s.o.Audit.Record(ctx, authapi.AuditEntry{
		TenantID: p.TenantID, OccurredAt: s.now(), ActorType: "user", ActorID: p.ActorID(), ViaPlatform: p.ViaPlatform,
		Action: action, ResourceType: rtype, ResourceID: rid, Scope: "tenant", Outcome: "success", Changes: changes,
	})
	if err != nil {
		s.o.Logger.ErrorContext(ctx, "assist: audit record failed", slog.String("action", action), slog.Any("error", err))
	}
}

// buildProvider construye el adaptador de la configuración efectiva.
func (s *Service) buildProvider(st *domain.Settings) (provider.Provider, error) {
	key, _, err := s.apiKey(st)
	if err != nil {
		return nil, err
	}
	if st.Provider == domain.ProviderAnthropic && key == "" {
		return nil, apperr.Conflict(CodeNotConfigured, "Falta la clave de API de Claude (del ISP o de plataforma).")
	}
	prov, err := s.o.Providers(ProviderConfig{Provider: st.Provider, Model: st.Model, BaseURL: st.BaseURL, APIKey: key, Effort: st.Effort})
	if err != nil {
		if _, ok := apperr.As(err); ok {
			return nil, err
		}
		return nil, apperr.Conflict(CodeNotConfigured, "La configuración del proveedor de IA no es válida: "+err.Error()+". Revísala en Administración › Inteligencia artificial.")
	}
	return prov, nil
}

// TestResult es la respuesta de POST /assist/settings/test.
type TestResult struct {
	OK        bool   `json:"ok"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	LatencyMS int    `json:"latency_ms"`
	ErrorCode string `json:"error_code,omitempty"`
	Message   string `json:"message"`
}

// TestConnection es POST /assist/settings/test: una petición mínima al
// proveedor configurado (no cuenta para el límite diario; se audita).
func (s *Service) TestConnection(ctx context.Context) (*TestResult, error) {
	p, err := principal(ctx, PermSettingsManage)
	if err != nil {
		return nil, err
	}
	st, err := s.effective(ctx, p.TenantID)
	if err != nil {
		return nil, err
	}
	prov, err := s.buildProvider(st)
	if err != nil {
		return nil, err
	}
	start := s.o.Now()
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	perr := prov.Ping(tctx)
	elapsed := int(s.o.Now().Sub(start).Milliseconds())
	res := &TestResult{OK: perr == nil, Provider: st.Provider, Model: st.Model, LatencyMS: elapsed, Message: "Conexión correcta."}
	entry := &domain.AuditEntry{TenantID: p.TenantID, UserID: userOf(p), Feature: domain.FeatureTest, Provider: st.Provider,
		Model: st.Model, DurationMS: elapsed, Outcome: domain.OutcomeSuccess}
	if perr != nil {
		res.ErrorCode = provider.Code(perr)
		res.Message = providerMessage(res.ErrorCode)
		entry.Outcome, entry.ErrorCode = domain.OutcomeError, res.ErrorCode
	}
	s.record(ctx, entry)
	return res, nil
}

func providerMessage(code string) string {
	switch code {
	case "PROVIDER_UNAUTHORIZED":
		return "El proveedor rechazó la clave de API."
	case "PROVIDER_RATE_LIMITED":
		return "El proveedor limitó las peticiones; inténtalo en unos minutos."
	case "PROVIDER_MODEL_NOT_FOUND":
		return "El proveedor no reconoce el modelo configurado."
	case "PROVIDER_BAD_REQUEST":
		return "El proveedor rechazó la petición (revisa el modelo y la URL)."
	case "PROVIDER_TIMEOUT":
		return "El proveedor no respondió a tiempo."
	default:
		return "No se pudo contactar con el proveedor."
	}
}

func (s *Service) record(ctx context.Context, e *domain.AuditEntry) {
	if e.ID == uuid.Nil {
		e.ID = uuid.Must(uuid.NewV7())
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = s.now()
	}
	if err := s.o.Store.RecordAudit(context.WithoutCancel(ctx), e); err != nil {
		s.o.Logger.ErrorContext(ctx, "assist: audit insert failed", slog.String("feature", e.Feature), slog.Any("error", err))
	}
}

// Analysis es la respuesta de POST /assist/analyses.
type Analysis struct {
	ID          uuid.UUID        `json:"id"`
	Feature     string           `json:"feature"`
	ContextID   string           `json:"context_id"`
	Markdown    string           `json:"markdown"`
	Provider    string           `json:"provider"`
	Model       string           `json:"model"`
	GeneratedAt string           `json:"generated_at"`
	ToolCalls   []domain.ToolUse `json:"tool_calls"`
	Usage       UsageView        `json:"usage"`
	DurationMS  int              `json:"duration_ms"`
	Truncated   bool             `json:"truncated"`
	Disclaimer  string           `json:"disclaimer"`
	// Grounding es la verificación de la respuesta contra los datos (docs/assist.md §Anclaje).
	Grounding grounding.Report `json:"grounding"`
}

// UsageView son los tokens de un análisis.
type UsageView struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// Disclaimer acompaña a toda respuesta generada.
const Disclaimer = "Generado por IA; revisa antes de aplicar. Horus no ejecuta nada en tus routers."

// AnalysisRequest es el cuerpo de POST /assist/analyses.
type AnalysisRequest struct {
	Feature   string `json:"feature"`
	ContextID string `json:"context_id"`
}

// featurePerm es el permiso de lectura del recurso de cada feature.
var featurePerm = map[string]string{domain.FeatureFinding: PermFindingsRead, domain.FeatureRouter: PermDevicesRead}

// featureProbe es la ruta que se lee (como el usuario) para comprobar que el contexto existe y es visible.
var featureProbe = map[string]string{domain.FeatureFinding: "/api/v1/findings/", domain.FeatureRouter: "/api/v1/routers/"}

// Analyze es POST /assist/analyses.
func (s *Service) Analyze(ctx context.Context, req AnalysisRequest) (*Analysis, error) {
	if !slices.Contains(domain.AnalysisFeatures, req.Feature) {
		return nil, apperr.Validation(apperr.Field("feature", "INVALID_VALUE", "Feature no admitida (finding o router)."))
	}
	if _, err := uuid.Parse(req.ContextID); err != nil {
		return nil, apperr.Validation(apperr.Field("context_id", "INVALID_VALUE", "Debe ser un UUID."))
	}
	p, err := principal(ctx, featurePerm[req.Feature])
	if err != nil {
		return nil, err
	}
	st, err := s.effective(ctx, p.TenantID)
	if err != nil {
		return nil, err
	}
	entry := &domain.AuditEntry{ID: uuid.Must(uuid.NewV7()), TenantID: p.TenantID, UserID: userOf(p), Feature: req.Feature,
		ContextID: req.ContextID, Provider: st.Provider, Model: st.Model}
	if !st.Enabled {
		return nil, apperr.Conflict(CodeDisabled, "El asistente de IA no está activado en este ISP (Administración › Inteligencia artificial).")
	}
	used, err := s.o.Store.CountRequests(ctx, p.TenantID, domain.StartOfDay(s.now()))
	if err != nil {
		return nil, s.storeErr(ctx, "count_requests", err)
	}
	if used >= st.DailyRequestLimit {
		entry.Outcome, entry.ErrorCode = domain.OutcomeLimited, CodeLimitReached
		s.record(ctx, entry)
		e := apperr.New(apperr.KindTooManyRequests, CodeLimitReached,
			fmt.Sprintf("Se alcanzó el límite de %d consultas de hoy en este ISP.", st.DailyRequestLimit))
		e.RetryAfter = int(domain.StartOfDay(s.now()).Add(24*time.Hour).Sub(s.now()).Seconds()) + 1
		return nil, e
	}
	if s.o.Readers == nil {
		return nil, apperr.New(apperr.KindUnavailable, problem.CodeServiceUnavailable, "El asistente no tiene acceso a la API en este proceso.")
	}
	prov, err := s.buildProvider(st)
	if err != nil {
		return nil, err
	}
	reader := s.o.Readers(p)
	// El contexto debe existir y ser visible para quien pide el análisis.
	if _, err := reader.Get(ctx, featureProbe[req.Feature]+req.ContextID, nil); err != nil {
		var he *tools.HTTPError
		switch {
		case errors.As(err, &he) && (he.Status == 404 || he.Status == 403):
			return nil, apperr.NotFound(CodeContextNotFound)
		case errors.As(err, &he):
			// 401, 429, 5xx…: el módulo dueño del contexto no lo sirvió; se devuelve su
			// estado y código (nunca un 500 sin explicación) y queda en el log.
			s.o.Logger.WarnContext(ctx, "assist: context probe failed", slog.String("feature", req.Feature),
				slog.Int("status", he.Status), slog.String("code", he.Code))
			return nil, apperr.New(apperr.KindBadGateway, CodeContextUnavailable,
				fmt.Sprintf("La API de Horus respondió %d %s al leer el contexto del análisis (%s).", he.Status, he.Code, featureProbe[req.Feature]))
		default:
			s.o.Logger.WarnContext(ctx, "assist: context probe failed", slog.String("feature", req.Feature), slog.Any("error", err))
			return nil, apperr.New(apperr.KindUnavailable, CodeContextUnavailable,
				"No se pudo leer el contexto del análisis en este proceso; revisa el registro de horus-app (assist: context probe failed).")
		}
	}
	env := tools.Env{Reader: reader, Masker: mask.New()}
	// Datos primarios ya consultados: el modelo los recibe en el primer mensaje
	// (anclaje) y cuentan como uso de herramienta en la auditoría.
	seeds, seedUses, evidence := s.seed(ctx, env, req.Feature, req.ContextID)
	user := prompt.User(req.Feature, req.ContextID, seeds, tools.CatalogMarkdown(prompt.Topics(req.Feature)...))
	return s.run(ctx, prov, env, entry, user, seedUses, evidence)
}

// seedTools son las herramientas primarias de cada feature.
var seedTools = map[string][]struct{ tool, arg string }{
	domain.FeatureFinding: {{"get_finding", "finding_id"}},
	domain.FeatureRouter:  {{"get_router", "router_id"}, {"get_exporter_status", "router_id"}},
}

// seed consulta las herramientas primarias; si una falla, el modelo la pedirá él.
func (s *Service) seed(ctx context.Context, env tools.Env, feature, contextID string) ([]prompt.Seed, []domain.ToolUse, []string) {
	var seeds []prompt.Seed
	var uses []domain.ToolUse
	var evidence []string
	for _, st := range seedTools[feature] {
		args, _ := json.Marshal(map[string]string{st.arg: contextID})
		out, err := s.o.Registry.Call(ctx, env, st.tool, args)
		u := domain.ToolUse{Name: st.tool, Count: 1}
		if err != nil {
			u.Errors = 1
			uses = append(uses, u)
			continue
		}
		uses = append(uses, u)
		seeds = append(seeds, prompt.Seed{Tool: st.tool, Envelope: out})
		evidence = append(evidence, out)
	}
	return seeds, uses, evidence
}

// run ejecuta el bucle del agente, audita y traduce el resultado.
// RepairIterations es el tope de pasos del turno de corrección.
const RepairIterations = 3

func (s *Service) run(ctx context.Context, prov provider.Provider, env tools.Env, entry *domain.AuditEntry, user string,
	seedUses []domain.ToolUse, evidence []string) (*Analysis, error) {
	start := s.o.Now()
	rctx, cancel := context.WithTimeout(ctx, s.o.RequestTimeout)
	defer cancel()
	specs := s.o.Registry.Specs()
	ts := make([]provider.ToolSpec, len(specs))
	for i, sp := range specs {
		ts[i] = provider.ToolSpec{Name: sp.Name, Description: sp.Description, Schema: sp.Schema}
	}
	conv := prov.Start(provider.Request{System: prompt.System, Tools: ts, User: user, MaxTokens: s.o.MaxOutputTokens})
	// Toda salida de herramienta es evidencia para la verificación.
	var evMu sync.Mutex
	evidence = append(evidence, prompt.System, user)
	exec := func(ctx context.Context, c provider.ToolCall) (string, error) {
		out, err := s.o.Registry.Call(ctx, env, c.Name, c.Input)
		if err == nil {
			evMu.Lock()
			evidence = append(evidence, out)
			evMu.Unlock()
		}
		return out, err
	}
	res, err := agent.Run(rctx, conv, exec, agent.Options{MaxIterations: s.o.MaxIterations})
	var report grounding.Report
	if err == nil {
		// Verificación contra los datos (sobre el texto ya enmascarado: ningún dato
		// personal llega al informe ni al turno de corrección).
		report = grounding.Check(env.Masker.Text(res.Markdown), evidence)
		if !report.OK() {
			conv.AddUserMessage(grounding.RepairMessage(report))
			res2, err2 := agent.Run(rctx, conv, exec, agent.Options{MaxIterations: RepairIterations})
			res.Usage.Add(res2.Usage)
			res.ToolUses = mergeUses(res.ToolUses, res2.ToolUses)
			res.Iterations += res2.Iterations
			if err2 == nil && res2.Markdown != "" {
				res.Markdown, res.Truncated = res2.Markdown, res2.Truncated
				if res2.Model != "" {
					res.Model = res2.Model
				}
				report = grounding.Check(env.Masker.Text(res.Markdown), evidence)
			}
			report.Repaired = true
		}
	}
	entry.DurationMS = int(s.o.Now().Sub(start).Milliseconds())
	entry.InputTokens, entry.OutputTokens = res.Usage.InputTokens, res.Usage.OutputTokens
	entry.ToolCalls = mergeUses(seedUses, res.ToolUses)
	if report.Status != "" && report.Status != grounding.Verified {
		s.o.Logger.InfoContext(ctx, "assist: answer not fully grounded", slog.String("status", string(report.Status)),
			slog.Int("unsupported", len(report.Unsupported)), slog.Int("commands", len(report.UnverifiedCommands)), slog.Bool("repaired", report.Repaired))
	}
	if res.Model != "" {
		entry.Model = res.Model
	}
	switch {
	case err == nil:
		entry.Outcome = domain.OutcomeSuccess
	case errors.Is(err, agent.ErrRefused):
		entry.Outcome, entry.ErrorCode = domain.OutcomeRefused, "REFUSED"
	case errors.Is(err, agent.ErrMaxIterations), errors.Is(err, agent.ErrEmpty):
		entry.Outcome, entry.ErrorCode = domain.OutcomeError, CodeIncomplete
	default:
		entry.Outcome, entry.ErrorCode = domain.OutcomeError, provider.Code(err)
	}
	s.record(ctx, entry)
	switch {
	case err == nil:
	case errors.Is(err, agent.ErrRefused):
		return nil, apperr.New(apperr.KindInvalid, CodeRefused,
			"El modelo declinó responder a esta consulta. Revisa el caso manualmente o prueba con otro modelo.")
	case errors.Is(err, agent.ErrMaxIterations), errors.Is(err, agent.ErrEmpty):
		return nil, apperr.New(apperr.KindBadGateway, CodeIncomplete,
			"El modelo no llegó a una respuesta dentro del límite de pasos. Inténtalo de nuevo.")
	default:
		code := provider.Code(err)
		s.o.Logger.WarnContext(ctx, "assist: provider error", slog.String("code", code), slog.Any("error", err))
		return nil, apperr.New(apperr.KindBadGateway, CodeProviderUnavailable, providerMessage(code))
	}
	markdown := env.Masker.Text(res.Markdown)
	if w := grounding.Warning(report); w != "" {
		markdown += "\n\n> ⚠ " + w
	}
	return &Analysis{
		ID: entry.ID, Feature: entry.Feature, ContextID: entry.ContextID, Markdown: markdown,
		Provider: prov.Name(), Model: entry.Model, GeneratedAt: s.now().Format("2006-01-02T15:04:05.000Z"),
		ToolCalls: entry.ToolCalls, Usage: UsageView{InputTokens: res.Usage.InputTokens, OutputTokens: res.Usage.OutputTokens},
		DurationMS: entry.DurationMS, Truncated: res.Truncated, Disclaimer: Disclaimer, Grounding: report,
	}, nil
}

// mergeUses suma los usos de herramienta por nombre (orden por nombre).
func mergeUses(a, b []domain.ToolUse) []domain.ToolUse {
	m := map[string]domain.ToolUse{}
	for _, list := range [][]domain.ToolUse{a, b} {
		for _, u := range list {
			x := m[u.Name]
			x.Name = u.Name
			x.Count += u.Count
			x.Errors += u.Errors
			m[u.Name] = x
		}
	}
	out := make([]domain.ToolUse, 0, len(m))
	for _, u := range m {
		out = append(out, u)
	}
	slices.SortFunc(out, func(x, y domain.ToolUse) int { return strings.Compare(x.Name, y.Name) })
	return out
}

// AuditItem es una fila de GET /assist/audit.
type AuditItem struct {
	ID           uuid.UUID        `json:"id"`
	UserID       *uuid.UUID       `json:"user_id"`
	Feature      string           `json:"feature"`
	ContextID    string           `json:"context_id"`
	Provider     string           `json:"provider"`
	Model        string           `json:"model"`
	InputTokens  int64            `json:"input_tokens"`
	OutputTokens int64            `json:"output_tokens"`
	ToolCalls    []domain.ToolUse `json:"tool_calls"`
	DurationMS   int              `json:"duration_ms"`
	Outcome      string           `json:"outcome"`
	ErrorCode    *string          `json:"error_code"`
	CreatedAt    string           `json:"created_at"`
}

// AuditPage es una página de auditoría.
type AuditPage struct {
	Data []AuditItem
	Next *AuditCursor
}

// Audit es GET /assist/audit.
func (s *Service) Audit(ctx context.Context, limit int, before *AuditCursor) (*AuditPage, error) {
	p, err := principal(ctx, PermSettingsRead)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.o.Store.ListAudit(ctx, p.TenantID, limit+1, before)
	if err != nil {
		return nil, err
	}
	page := &AuditPage{Data: []AuditItem{}}
	if len(rows) > limit {
		last := rows[limit-1]
		page.Next = &AuditCursor{CreatedAt: last.CreatedAt, ID: last.ID}
		rows = rows[:limit]
	}
	for _, r := range rows {
		it := AuditItem{ID: r.ID, UserID: r.UserID, Feature: r.Feature, ContextID: r.ContextID, Provider: r.Provider, Model: r.Model,
			InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, ToolCalls: r.ToolCalls, DurationMS: r.DurationMS,
			Outcome: r.Outcome, CreatedAt: r.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z")}
		if it.ToolCalls == nil {
			it.ToolCalls = []domain.ToolUse{}
		}
		if r.ErrorCode != "" {
			c := r.ErrorCode
			it.ErrorCode = &c
		}
		page.Data = append(page.Data, it)
	}
	return page, nil
}
