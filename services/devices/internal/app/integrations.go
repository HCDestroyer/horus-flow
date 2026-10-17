package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/packages/go/webhooksig"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// Permisos y códigos de las integraciones (ADR-0030).
const (
	CodeIntegrationNotConfig  = "INTEGRATION_NOT_CONFIGURED"
	CodeIntegrationUnavailble = "INTEGRATION_UNAVAILABLE"
	argosBatch                = 500
	webhookSecretGrace        = 24 * time.Hour
	webhookInboxRetention     = 7 * 24 * time.Hour
)

// Errores del cliente de ArgosISP (los envuelve el adaptador argos).
var (
	ErrArgosUnavailable = errors.New("argos: unavailable")
	ErrArgosAuth        = errors.New("argos: auth failed")
	ErrArgosForbidden   = errors.New("argos: forbidden")
	ErrArgosUnexpected  = errors.New("argos: unexpected response")
	ErrArgosNotAllowed  = errors.New("argos: destination not allowed")
	// ErrWebhookRejected: conector desconocido o apagado, o firma inválida.
	ErrWebhookUnknown  = errors.New("argos webhook: unknown connector")
	ErrWebhookRejected = errors.New("argos webhook: invalid signature")
	// errArgosSkipped: conector apagado o en espera (no es un fallo).
	errArgosSkipped = errors.New("argos: connector inactive or backing off")
)

// ArgosResult es la resolución de una IP en ArgosISP.
type ArgosResult struct {
	IP         string
	Resolution domain.Resolution
}

// ArgosResolver resuelve IPs en una instancia de ArgosISP.
type ArgosResolver interface {
	// Resolve devuelve un resultado por IP (mismo orden) y el modo usado
	// (batch | fallback).
	Resolve(ctx context.Context, ips []string) ([]ArgosResult, string, error)
}

// ArgosDialer construye el cliente de un ISP (con su límite de 10 req/s).
type ArgosDialer func(tenant uuid.UUID, baseURL, token string) ArgosResolver

// ArgosSyncState es el estado de la última sincronización del conector.
type ArgosSyncState struct {
	At           time.Time
	Status       string // ok | degraded | failing
	Error        *string
	Resolver     *string
	Failures     int
	BackoffUntil *time.Time
}

// ServiceWindow es un periodo en que un servicio de ArgosISP tuvo la IP de un cliente.
type ServiceWindow struct {
	ServiceID  int64
	CustomerID uuid.UUID
	RealmID    uuid.UUID
	SiteID     uuid.UUID
	Address    netip.Prefix
	From       time.Time
	To         *time.Time
}

// ArgosWebhook es un webhook recibido pendiente de procesar.
type ArgosWebhook struct {
	TenantID uuid.UUID
	ID       string
	Event    string
	Payload  []byte
	Attempts int
}

// IntegrationStore es el repositorio del conector y de la identidad externa.
type IntegrationStore interface {
	GetArgosConnector(ctx context.Context, t pgdb.TenantID) (*domain.ArgosConnector, error)
	ArgosConnectorByID(ctx context.Context, id uuid.UUID) (*domain.ArgosConnector, error)
	SaveArgosConnector(ctx context.Context, t pgdb.TenantID, c *domain.ArgosConnector, expect int) error
	RecordArgosSync(ctx context.Context, t pgdb.TenantID, st ArgosSyncState) error
	ActiveArgosTenants(ctx context.Context) ([]uuid.UUID, error)
	IdentityAt(ctx context.Context, t pgdb.TenantID, customerID uuid.UUID, at time.Time) (*domain.ExternalIdentity, error)
	IdentityHistory(ctx context.Context, t pgdb.TenantID, customerID uuid.UUID, limit int) ([]domain.ExternalIdentity, error)
	CurrentIdentities(ctx context.Context, t pgdb.TenantID, customerIDs []uuid.UUID) (map[uuid.UUID]domain.ExternalIdentity, error)
	CustomerSync(ctx context.Context, t pgdb.TenantID, customerID uuid.UUID) (*time.Time, *string, error)
	ApplyIdentityPlan(ctx context.Context, t pgdb.TenantID, customerID uuid.UUID, plan domain.IdentityPlan, status string, now time.Time,
		alias func(c *domain.Customer, current *domain.ExternalIdentity) bool, ev CustomerEvents) error
	MarkIdentitiesStale(ctx context.Context, t pgdb.TenantID, customerIDs []uuid.UUID, now time.Time) error
	SweepCandidates(ctx context.Context, t pgdb.TenantID, before time.Time, limit int) ([]domain.Customer, error)
	ServiceWindows(ctx context.Context, t pgdb.TenantID, serviceIDs []int64, from, to time.Time) ([]ServiceWindow, error)
	CustomersByExternal(ctx context.Context, t pgdb.TenantID, serviceID, customerID *int64) ([]domain.Customer, error)
	CountIdentities(ctx context.Context, t pgdb.TenantID) (current, stale int, err error)
	InsertArgosWebhook(ctx context.Context, t pgdb.TenantID, id, event string, payload []byte) (bool, error)
	PendingArgosWebhooks(ctx context.Context, limit int) ([]ArgosWebhook, error)
	FinishArgosWebhook(ctx context.Context, t pgdb.TenantID, id string, done bool) error
	PurgeArgosWebhooks(ctx context.Context, before time.Time) (int, error)
	// CustomersContaining devuelve, por dirección, el cliente que la contiene
	// visto más recientemente (sites limita al alcance; nil = todo el ISP).
	CustomersContaining(ctx context.Context, t pgdb.TenantID, addrs []netip.Addr, sites []uuid.UUID) (map[netip.Addr]domain.Customer, error)
	GetCustomer(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Customer, error)
}

// Integrations implementa el conector ArgosISP (identidad con vigencia,
// receptor de webhooks y barrido), la consulta 360 por IP y el consumo por
// lotes (docs/integrations/argosisp.md).
type Integrations struct {
	store        IntegrationStore
	sealer       *envelope.Sealer
	dial         ArgosDialer
	audit        func() (authapi.AuditRecorder, bool)
	baseURL      string
	allowPrivate bool
	now          func() time.Time
	logger       *slog.Logger
	// Lectores de otros módulos (module.Services; nil = no disponible).
	Readers Readers

	queue chan resolveJob
	kick  chan struct{}
	locks sync.Map // tenant → *sync.Mutex (una resolución a la vez por ISP)
}

type resolveJob struct {
	tenant pgdb.TenantID
	ids    []uuid.UUID
}

// IntegrationsOptions configura el servicio.
type IntegrationsOptions struct {
	Store   IntegrationStore
	Sealer  *envelope.Sealer
	Dial    ArgosDialer
	Audit   func() (authapi.AuditRecorder, bool)
	BaseURL string
	// AllowPrivate (HORUS_INTEGRATIONS_ALLOW_PRIVATE, laboratorios): URLs http y destinos privados.
	AllowPrivate bool
	Now          func() time.Time
	Logger       *slog.Logger
	Readers      Readers
}

// NewIntegrations crea el servicio.
func NewIntegrations(o IntegrationsOptions) *Integrations {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Audit == nil {
		o.Audit = func() (authapi.AuditRecorder, bool) { return nil, false }
	}
	return &Integrations{store: o.Store, sealer: o.Sealer, dial: o.Dial, audit: o.Audit, baseURL: strings.TrimRight(o.BaseURL, "/"),
		allowPrivate: o.AllowPrivate, now: o.Now, logger: o.Logger, Readers: o.Readers,
		queue: make(chan resolveJob, 256), kick: make(chan struct{}, 1)}
}

// ts es el instante actual en UTC truncado a milisegundos (la precisión de
// la API): una vigencia leída y devuelta como `at` cae dentro de sí misma.
func (s *Integrations) ts() time.Time { return s.now().UTC().Truncate(time.Millisecond) }

func (s *Integrations) record(ctx context.Context, t pgdb.TenantID, action, resType, resID string, changes map[string]any) {
	p := authz.FromContext(ctx)
	rec, ok := s.audit()
	if !ok {
		s.logger.WarnContext(ctx, "integration audit not recorded: auth not local", slog.String("action", action))
		return
	}
	e := authapi.AuditEntry{TenantID: t.UUID(), OccurredAt: s.ts(), ActorType: "service", ActorID: "svc:devices", Action: action,
		ResourceType: resType, ResourceID: resID, Scope: "tenant", Outcome: "success", Changes: changes}
	if p != nil {
		e.ActorType, e.ActorID, e.ViaPlatform = p.Type, p.Subject, p.ViaPlatform
	}
	if e.Changes == nil {
		e.Changes = map[string]any{}
	}
	if err := rec.Record(ctx, e); err != nil {
		s.logger.ErrorContext(ctx, "integration audit failed", slog.String("action", action), slog.Any("error", err))
	}
}

// ---------------------------------------------------------------- conector

// ConnectorView es ArgosIntegration del contrato.
type ConnectorView struct {
	Connector  *domain.ArgosConnector
	Identities int
	Stale      int
}

// WebhookURL es la URL del receptor de este conector.
func (s *Integrations) WebhookURL(c *domain.ArgosConnector) *string {
	if c == nil {
		return nil
	}
	u := s.baseURL + "/api/v1/integrations/argosisp/webhook/" + c.ID.String()
	return &u
}

func connAAD(c *domain.ArgosConnector, what string) []byte {
	return []byte("devices_argosisp:" + what + ":" + c.TenantID.String() + ":" + c.ID.String())
}

// GetConnector es GET /integrations/argosisp.
func (s *Integrations) GetConnector(ctx context.Context) (*ConnectorView, error) {
	_, t, err := scope(ctx, PermSettingsRead)
	if err != nil {
		return nil, err
	}
	return s.view(ctx, t)
}

func (s *Integrations) view(ctx context.Context, t pgdb.TenantID) (*ConnectorView, error) {
	c, err := s.store.GetArgosConnector(ctx, t)
	if errors.Is(err, domain.ErrNotFound) {
		return &ConnectorView{}, nil
	}
	if err != nil {
		return nil, err
	}
	cur, stale, err := s.store.CountIdentities(ctx, t)
	if err != nil {
		return nil, err
	}
	return &ConnectorView{Connector: c, Identities: cur, Stale: stale}, nil
}

// ConnectorInput es ArgosIntegrationInput.
type ConnectorInput struct {
	BaseURL *string `json:"base_url"`
	Enabled *bool   `json:"enabled"`
	Token   *string `json:"token"`
}

// ValidateBaseURL comprueba la URL base de ArgosISP (https, sin credenciales;
// http solo en modo laboratorio).
func ValidateBaseURL(raw string, allowHTTP bool) (string, bool) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(raw) > 2048 {
		return "", false
	}
	if u.Scheme != "https" && (!allowHTTP || u.Scheme != "http") {
		return "", false
	}
	return raw, true
}

// PutConnector es PUT /integrations/argosisp (token write-only, cifrado).
func (s *Integrations) PutConnector(ctx context.Context, in ConnectorInput) (*ConnectorView, error) {
	_, t, err := scope(ctx, PermSettingsManage)
	if err != nil {
		return nil, err
	}
	var fe fieldErrs
	var base string
	if in.BaseURL == nil {
		fe.add("base_url", "REQUIRED", "Obligatoria.")
	} else if v, ok := ValidateBaseURL(*in.BaseURL, s.allowPrivate); !ok {
		fe.add("base_url", "INVALID_FORMAT", "URL https de ArgosISP (con /api/v1), sin credenciales.")
	} else {
		base = v
	}
	if in.Enabled == nil {
		fe.add("enabled", "REQUIRED", "Obligatorio.")
	}
	if in.Token != nil {
		if n := len(strings.TrimSpace(*in.Token)); n < 8 || n > 512 {
			fe.add("token", "INVALID_VALUE", "Token atk_… de ArgosISP (8–512 caracteres).")
		}
	}
	if err := fe.err(); err != nil {
		return nil, err
	}
	cur, err := s.store.GetArgosConnector(ctx, t)
	expect := 0
	switch {
	case errors.Is(err, domain.ErrNotFound):
		cur = &domain.ArgosConnector{ID: uuid.Must(uuid.NewV7()), TenantID: t.UUID()}
	case err != nil:
		return nil, err
	default:
		expect = cur.Version
	}
	changed := []string{}
	if cur.BaseURL != base {
		changed = append(changed, "base_url")
	}
	if cur.Enabled != *in.Enabled {
		changed = append(changed, "enabled")
	}
	cur.BaseURL, cur.Enabled = base, *in.Enabled
	kek := s.sealer.KEKID()
	if in.Token != nil {
		ct, dek, err := s.sealer.Seal([]byte(strings.TrimSpace(*in.Token)), connAAD(cur, "token"))
		if err != nil {
			return nil, fmt.Errorf("devices: seal argos token: %w", err)
		}
		cur.TokenCiphertext, cur.TokenDEK, cur.KEKID = ct, dek, &kek
		changed = append(changed, "token")
	}
	if !cur.HasSecret() {
		// El receptor genera el secreto del webhook en la primera configuración.
		sec, err := webhooksig.NewSecret()
		if err != nil {
			return nil, err
		}
		ct, dek, err := s.sealer.Seal([]byte(sec), connAAD(cur, "webhook"))
		if err != nil {
			return nil, fmt.Errorf("devices: seal webhook secret: %w", err)
		}
		cur.SecretCiphertext, cur.SecretDEK, cur.KEKID = ct, dek, &kek
	}
	if len(changed) > 0 {
		cur.Failures, cur.BackoffUntil = 0, nil // configuración nueva: se reintenta enseguida
	}
	if err := s.store.SaveArgosConnector(ctx, t, cur, expect); err != nil {
		if errors.Is(err, ErrVersionChanged) {
			return nil, apperr.Conflict(problem.CodeConflict, "El conector cambió mientras se guardaba; vuelva a intentarlo.")
		}
		return nil, err
	}
	s.record(ctx, t, "integrations.argosisp.updated", "integration", cur.ID.String(), map[string]any{"fields": changed, "enabled": cur.Enabled})
	return s.view(ctx, t)
}

// RotateWebhookSecret es POST /integrations/argosisp/webhook-secret: secreto
// nuevo (mostrado una vez); el anterior vale 24 h más.
func (s *Integrations) RotateWebhookSecret(ctx context.Context) (string, string, error) {
	_, t, err := scope(ctx, PermSettingsManage)
	if err != nil {
		return "", "", err
	}
	c, err := s.store.GetArgosConnector(ctx, t)
	if errors.Is(err, domain.ErrNotFound) {
		return "", "", apperr.Conflict(CodeIntegrationNotConfig, "Configure primero el conector ArgosISP.")
	}
	if err != nil {
		return "", "", err
	}
	sec, err := webhooksig.NewSecret()
	if err != nil {
		return "", "", err
	}
	ct, dek, err := s.sealer.Seal([]byte(sec), connAAD(c, "webhook"))
	if err != nil {
		return "", "", fmt.Errorf("devices: seal webhook secret: %w", err)
	}
	if c.HasSecret() {
		until := s.ts().Add(webhookSecretGrace)
		c.PrevSecretCiphertext, c.PrevSecretDEK, c.PrevSecretUntil = c.SecretCiphertext, c.SecretDEK, &until
	}
	kek := s.sealer.KEKID()
	c.SecretCiphertext, c.SecretDEK, c.KEKID = ct, dek, &kek
	if err := s.store.SaveArgosConnector(ctx, t, c, c.Version); err != nil {
		if errors.Is(err, ErrVersionChanged) {
			return "", "", apperr.Conflict(problem.CodeConflict, "El conector cambió; vuelva a intentarlo.")
		}
		return "", "", err
	}
	s.record(ctx, t, "integrations.argosisp.webhook_secret.rotated", "integration", c.ID.String(), nil)
	return *s.WebhookURL(c), sec, nil
}

// ConnectionTest es ArgosConnectionTest.
type ConnectionTest struct {
	OK        bool
	TestedAt  time.Time
	LatencyMS *int
	Resolver  *string
	ErrorCode *string
	Error     *string
}

func testFail(code, msg string) ConnectionTest { return ConnectionTest{ErrorCode: &code, Error: &msg} }

// TestConnector es POST /integrations/argosisp/connection-test: resuelve una
// IP de documentación (RFC 5737) sin cambiar nada.
func (s *Integrations) TestConnector(ctx context.Context) (ConnectionTest, error) {
	_, t, err := scope(ctx, PermSettingsManage)
	if err != nil {
		return ConnectionTest{}, err
	}
	c, err := s.store.GetArgosConnector(ctx, t)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && !c.HasToken()) {
		return ConnectionTest{}, apperr.Conflict(CodeIntegrationNotConfig, "Configure la URL y el token de ArgosISP.")
	}
	if err != nil {
		return ConnectionTest{}, err
	}
	tok, err := s.sealer.Open(c.TokenCiphertext, c.TokenDEK, connAAD(c, "token"))
	if err != nil {
		return ConnectionTest{}, fmt.Errorf("devices: open argos token: %w", err)
	}
	start := time.Now()
	_, mode, err := s.dial(c.TenantID, c.BaseURL, string(tok)).Resolve(ctx, []string{"192.0.2.1"})
	ms := int(time.Since(start).Milliseconds())
	var res ConnectionTest
	switch {
	case err == nil:
		res = ConnectionTest{OK: true, Resolver: &mode}
	case errors.Is(err, ErrArgosAuth):
		res = testFail("auth_failed", "ArgosISP rechazó el token.")
	case errors.Is(err, ErrArgosForbidden):
		res = testFail("forbidden", "El token no tiene los alcances ip.resolver, clientes.ver y servicios.ver.")
	case errors.Is(err, ErrArgosNotAllowed):
		res = testFail("not_allowed", "Destino no permitido (solo https y direcciones públicas).")
	case errors.Is(err, context.DeadlineExceeded):
		res = testFail("timeout", "ArgosISP no respondió a tiempo.")
	case errors.Is(err, ErrArgosUnavailable):
		res = testFail("connect_failed", "No se pudo conectar con ArgosISP.")
	default:
		res = testFail("unexpected_response", "Respuesta inesperada de ArgosISP.")
	}
	res.TestedAt, res.LatencyMS = s.ts(), &ms
	s.record(ctx, t, "integrations.argosisp.connection_test", "integration", c.ID.String(), map[string]any{"ok": res.OK})
	return res, nil
}

// ---------------------------------------------------------------- identidad

// IdentityView es CustomerExternalIdentity.
type IdentityView struct {
	CustomerID       uuid.UUID
	ConnectorEnabled bool
	Current          *domain.ExternalIdentity
	History          []domain.ExternalIdentity
	LastCheckedAt    *time.Time
	LastCheckStatus  *string
}

func (s *Integrations) identityView(ctx context.Context, t pgdb.TenantID, c *domain.Customer) (*IdentityView, error) {
	v := &IdentityView{CustomerID: c.ID}
	if conn, err := s.store.GetArgosConnector(ctx, t); err == nil {
		v.ConnectorEnabled = conn.Active()
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	hist, err := s.store.IdentityHistory(ctx, t, c.ID, 50)
	if err != nil {
		return nil, err
	}
	if hist == nil {
		hist = []domain.ExternalIdentity{}
	}
	v.History = hist
	for i := range hist {
		if hist[i].Current() {
			cur := hist[i]
			v.Current = &cur
			break
		}
	}
	v.LastCheckedAt, v.LastCheckStatus, err = s.store.CustomerSync(ctx, t, c.ID)
	return v, err
}

func (s *Integrations) customer(ctx context.Context, perm string, id uuid.UUID) (*authz.Principal, pgdb.TenantID, *domain.Customer, error) {
	p, t, err := scope(ctx, perm)
	if err != nil {
		return nil, t, nil, err
	}
	c, err := s.store.GetCustomer(ctx, t, id)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && !siteAllowed(p, perm, c.SiteID)) {
		return nil, t, nil, apperr.NotFound(domain.CodeCustomerNotFound)
	}
	return p, t, c, err
}

// ExternalIdentity es GET /customers/{id}/external-identity.
func (s *Integrations) ExternalIdentity(ctx context.Context, id uuid.UUID) (*IdentityView, error) {
	_, t, c, err := s.customer(ctx, PermCustomersRead, id)
	if err != nil {
		return nil, err
	}
	return s.identityView(ctx, t, c)
}

// RefreshIdentity es POST /customers/{id}/external-identity/refresh.
func (s *Integrations) RefreshIdentity(ctx context.Context, id uuid.UUID) (*IdentityView, error) {
	_, t, c, err := s.customer(ctx, PermCustomersUpdate, id)
	if err != nil {
		return nil, err
	}
	conn, err := s.store.GetArgosConnector(ctx, t)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && !conn.Active()) {
		return nil, apperr.Conflict(CodeIntegrationNotConfig, "El conector ArgosISP no está activo.")
	}
	if err != nil {
		return nil, err
	}
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// A demanda no espera al backoff: si ArgosISP sigue caído, queda stale.
	if err := s.resolve(rctx, t, conn, []domain.Customer{*c}, true); err != nil && !errors.Is(err, ErrArgosUnavailable) &&
		!errors.Is(err, ErrArgosAuth) && !errors.Is(err, ErrArgosForbidden) && !errors.Is(err, ErrArgosNotAllowed) &&
		!errors.Is(err, ErrArgosUnexpected) && !errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	s.record(ctx, t, "integrations.argosisp.identity.refreshed", "customer", c.ID.String(), nil)
	return s.identityView(context.WithoutCancel(ctx), t, c)
}

func (s *Integrations) tenantLock(t pgdb.TenantID) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(t.UUID(), &sync.Mutex{})
	return v.(*sync.Mutex) //nolint:forcetypeassert // solo se guardan *sync.Mutex
}

// ResolveCustomers resuelve en ArgosISP los clientes dados (lotes de 500) si
// el conector del ISP está activo y no está en espera. Nunca bloquea la
// ingesta: lo llaman el barrido, los webhooks y el alta de clientes en
// segundo plano.
func (s *Integrations) ResolveCustomers(ctx context.Context, t pgdb.TenantID, cs []domain.Customer) error {
	if len(cs) == 0 {
		return nil
	}
	conn, err := s.store.GetArgosConnector(ctx, t)
	if errors.Is(err, domain.ErrNotFound) {
		return errArgosSkipped
	}
	if err != nil {
		return err
	}
	if !conn.Active() || conn.InBackoff(s.ts()) {
		return errArgosSkipped
	}
	return s.resolve(ctx, t, conn, cs, false)
}

// resolve llama a ArgosISP y aplica las vigencias; ante un fallo marca stale
// las identidades vigentes y aplica la espera exponencial (máx. 15 min).
func (s *Integrations) resolve(ctx context.Context, t pgdb.TenantID, conn *domain.ArgosConnector, cs []domain.Customer, onDemand bool) error {
	mu := s.tenantLock(t)
	mu.Lock()
	defer mu.Unlock()
	tok, err := s.sealer.Open(conn.TokenCiphertext, conn.TokenDEK, connAAD(conn, "token"))
	if err != nil {
		return fmt.Errorf("devices: open argos token: %w", err)
	}
	client := s.dial(conn.TenantID, conn.BaseURL, string(tok))
	var mode string
	for start := 0; start < len(cs); start += argosBatch {
		batch := cs[start:min(start+argosBatch, len(cs))]
		ips := make([]string, len(batch))
		ids := make([]uuid.UUID, len(batch))
		for i := range batch {
			ips[i], ids[i] = batch[i].Address.Addr().String(), batch[i].ID
		}
		res, m, err := client.Resolve(ctx, ips)
		if err != nil {
			now := s.ts()
			if serr := s.store.MarkIdentitiesStale(context.WithoutCancel(ctx), t, ids, now); serr != nil {
				s.logger.WarnContext(ctx, "argos: mark stale failed", slog.Any("error", serr))
			}
			conn.Failures++
			until := now.Add(domain.NextBackoff(conn.Failures))
			msg := argosErrText(err)
			_ = s.store.RecordArgosSync(context.WithoutCancel(ctx), t, ArgosSyncState{At: now, Status: "failing", Error: &msg,
				Failures: conn.Failures, BackoffUntil: &until})
			s.logger.WarnContext(ctx, "argos: resolution failed; identities kept as stale", slog.String("tenant_id", t.String()),
				slog.Int("customers", len(batch)), slog.Int("failures", conn.Failures), slog.Duration("backoff", until.Sub(now)),
				slog.Bool("on_demand", onDemand), slog.String("error", msg))
			return err
		}
		mode = m
		if err := s.apply(ctx, t, batch, res); err != nil {
			return err
		}
	}
	status := "ok"
	if mode == "fallback" {
		status = "degraded" // funciona, pero con el respaldo GET /ip/:ip (A1 pendiente en ArgosISP)
	}
	conn.Failures = 0
	return s.store.RecordArgosSync(context.WithoutCancel(ctx), t, ArgosSyncState{At: s.ts(), Status: status, Resolver: &mode})
}

func argosErrText(err error) string {
	switch {
	case errors.Is(err, ErrArgosAuth):
		return "ArgosISP rechazó el token (401)."
	case errors.Is(err, ErrArgosForbidden):
		return "El token de ArgosISP no tiene los alcances necesarios (403)."
	case errors.Is(err, ErrArgosNotAllowed):
		return "Destino de ArgosISP no permitido."
	case errors.Is(err, context.DeadlineExceeded):
		return "ArgosISP no respondió a tiempo."
	case errors.Is(err, ErrArgosUnavailable):
		return "ArgosISP no responde."
	}
	return "Respuesta inesperada de ArgosISP."
}

// apply aplica las vigencias de un lote resuelto.
func (s *Integrations) apply(ctx context.Context, t pgdb.TenantID, batch []domain.Customer, res []ArgosResult) error {
	ids := make([]uuid.UUID, len(batch))
	for i := range batch {
		ids[i] = batch[i].ID
	}
	current, err := s.store.CurrentIdentities(ctx, t, ids)
	if err != nil {
		return err
	}
	now := s.ts()
	for i := range batch {
		c := &batch[i]
		var r domain.Resolution
		if i < len(res) {
			r = res[i].Resolution
		}
		var cur *domain.ExternalIdentity
		if x, ok := current[c.ID]; ok {
			cur = &x
		}
		plan := domain.PlanResolution(cur, r, now, t.UUID(), c.ID)
		status := domain.CheckNotFound
		if r.Found {
			status = domain.CheckFound
		}
		err := s.store.ApplyIdentityPlan(ctx, t, c.ID, plan, status, now,
			func(cust *domain.Customer, cur *domain.ExternalIdentity) bool {
				var name *string
				if cur != nil {
					name = cur.DisplayName
				}
				return cust.ExternalAlias(name)
			},
			func(cust *domain.Customer) []outbox.Event {
				d := CustomerData(cust)
				d["changed_fields"] = []string{"alias"}
				ev := customerEvent(nil, t, "updated", cust, now, d)
				ev.Actor = outbox.Actor{Type: "system", ID: "system:devices-argosisp"}
				return []outbox.Event{ev}
			})
		if errors.Is(err, domain.ErrNotFound) {
			continue // purgado entre la consulta y la resolución
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- disparadores

// EnqueueDiscovered encola la resolución de clientes recién descubiertos
// (customer.discovered). No bloquea: si la cola está llena, los resolverá el
// barrido.
func (s *Integrations) EnqueueDiscovered(t pgdb.TenantID, cs []domain.Customer) {
	if len(cs) == 0 {
		return
	}
	ids := make([]uuid.UUID, len(cs))
	for i := range cs {
		ids[i] = cs[i].ID
	}
	select {
	case s.queue <- resolveJob{tenant: t, ids: ids}:
	default:
		s.logger.Debug("argos: discovery queue full; the sweep will resolve them")
	}
}

// ReceiveWebhook valida y guarda un webhook de ArgosISP (deduplicado por
// X-Webhook-Id) y despierta al procesador. Rápido: no llama a ArgosISP.
func (s *Integrations) ReceiveWebhook(ctx context.Context, connectorID uuid.UUID, h http.Header, body []byte) error {
	c, err := s.store.ArgosConnectorByID(ctx, connectorID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && (!c.Enabled || !c.HasSecret())) {
		return ErrWebhookUnknown
	}
	if err != nil {
		return err
	}
	secrets := [][]byte{}
	if b, err := s.sealer.Open(c.SecretCiphertext, c.SecretDEK, connAAD(c, "webhook")); err == nil {
		secrets = append(secrets, b)
	}
	if len(c.PrevSecretCiphertext) > 0 && c.PrevSecretUntil != nil && s.ts().Before(*c.PrevSecretUntil) {
		if b, err := s.sealer.Open(c.PrevSecretCiphertext, c.PrevSecretDEK, connAAD(c, "webhook")); err == nil {
			secrets = append(secrets, b)
		}
	}
	id, err := webhooksig.Verify(h, body, s.now(), secrets...)
	if err != nil {
		return ErrWebhookRejected
	}
	var ev struct {
		Evento string `json:"evento"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return apperr.New(apperr.KindBadRequest, problem.CodeValidationFailed, "Cuerpo JSON no válido.")
	}
	if len(ev.Evento) > 64 {
		ev.Evento = ev.Evento[:64]
	}
	t := pgdb.TenantID(c.TenantID)
	if _, err := s.store.InsertArgosWebhook(ctx, t, id, ev.Evento, body); err != nil {
		return err
	}
	select {
	case s.kick <- struct{}{}:
	default:
	}
	return nil
}

// argosEvent es el cuerpo de un webhook de ArgosISP (contrato §2.3).
type argosEvent struct {
	ID      string `json:"id"`
	Evento  string `json:"evento"`
	Payload struct {
		ServicioID        *int64          `json:"servicio_id"`
		ClienteID         *int64          `json:"cliente_id"`
		IPv4              json.RawMessage `json:"ipv4"`
		PrefijoV6         *string         `json:"prefijo_v6"`
		PrefijoEnlaceV6   *string         `json:"prefijo_enlace_v6"`
		PrefijoDelegadoV6 *string         `json:"prefijo_delegado_v6"`
		Anterior          json.RawMessage `json:"anterior"`
	} `json:"payload"`
}

// addresses extrae las IPs (y la primera de cada prefijo) de un valor JSON
// que puede ser texto, lista u objeto (p. ej. "anterior").
func addresses(raw json.RawMessage, out map[netip.Addr]bool) {
	if len(raw) == 0 {
		return
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		addAddr(s, out)
		return
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		for _, e := range list {
			addresses(e, out)
		}
		return
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		for _, k := range []string{"ipv4", "prefijo_v6", "prefijo_enlace_v6", "prefijo_delegado_v6", "ip"} {
			addresses(obj[k], out)
		}
	}
}

func addAddr(s string, out map[netip.Addr]bool) {
	s = strings.TrimSpace(s)
	if a, err := netip.ParseAddr(s); err == nil {
		out[a.Unmap()] = true
	} else if p, err := netip.ParsePrefix(s); err == nil {
		out[p.Masked().Addr().Unmap()] = true
	}
}

// affected devuelve los clientes a re-resolver por un webhook.
func (s *Integrations) affected(ctx context.Context, t pgdb.TenantID, ev argosEvent) ([]domain.Customer, error) {
	byID := map[uuid.UUID]domain.Customer{}
	switch ev.Evento {
	case "asignacion.cambiada", "sesion.iniciada", "sesion.terminada", "servicio.estado_cambiado":
		if ev.Payload.ServicioID != nil {
			cs, err := s.store.CustomersByExternal(ctx, t, ev.Payload.ServicioID, nil)
			if err != nil {
				return nil, err
			}
			for _, c := range cs {
				byID[c.ID] = c
			}
		}
		addrs := map[netip.Addr]bool{}
		addresses(ev.Payload.IPv4, addrs)
		addresses(ev.Payload.Anterior, addrs)
		for _, p := range []*string{ev.Payload.PrefijoV6, ev.Payload.PrefijoEnlaceV6, ev.Payload.PrefijoDelegadoV6} {
			if p != nil {
				addAddr(*p, addrs)
			}
		}
		if len(addrs) > 0 {
			list := make([]netip.Addr, 0, len(addrs))
			for a := range addrs {
				list = append(list, a)
			}
			found, err := s.store.CustomersContaining(ctx, t, list, nil)
			if err != nil {
				return nil, err
			}
			for _, c := range found {
				byID[c.ID] = c
			}
		}
	case "cliente.actualizado":
		if ev.Payload.ClienteID != nil {
			cs, err := s.store.CustomersByExternal(ctx, t, nil, ev.Payload.ClienteID)
			if err != nil {
				return nil, err
			}
			for _, c := range cs {
				byID[c.ID] = c
			}
		}
	}
	out := make([]domain.Customer, 0, len(byID))
	for _, c := range byID {
		out = append(out, c)
	}
	return out, nil
}

// ProcessWebhooks procesa los webhooks pendientes (re-resolución de las IPs o
// servicios afectados). Si ArgosISP no responde, el webhook queda pendiente
// y se reintenta en la siguiente pasada (máx. 10 intentos).
func (s *Integrations) ProcessWebhooks(ctx context.Context) error {
	pending, err := s.store.PendingArgosWebhooks(ctx, 50)
	if err != nil {
		return err
	}
	for _, w := range pending {
		t := pgdb.TenantID(w.TenantID)
		var ev argosEvent
		done := true
		if json.Unmarshal(w.Payload, &ev) == nil {
			cs, err := s.affected(ctx, t, ev)
			if err != nil {
				return err
			}
			if err := s.ResolveCustomers(ctx, t, cs); err != nil && !errors.Is(err, errArgosSkipped) {
				done = false
			} else if errors.Is(err, errArgosSkipped) && len(cs) > 0 {
				done = false // en espera: se reintenta tras el backoff
				if c, cerr := s.store.GetArgosConnector(ctx, t); cerr == nil && !c.Active() {
					done = true // conector apagado: se descarta
				}
			}
		}
		if err := s.store.FinishArgosWebhook(ctx, t, w.ID, done); err != nil {
			return err
		}
	}
	return nil
}

// Sweep re-resuelve en cada ISP con conector activo los clientes activos sin
// resolución o con la última de más de 6 h (lotes de 500).
func (s *Integrations) Sweep(ctx context.Context) error {
	tenants, err := s.store.ActiveArgosTenants(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, tid := range tenants {
		t := pgdb.TenantID(tid)
		cs, err := s.store.SweepCandidates(ctx, t, s.ts().Add(-domain.IdentityMaxAge), argosBatch)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := s.ResolveCustomers(ctx, t, cs); err != nil && !errors.Is(err, errArgosSkipped) &&
			!errors.Is(err, ErrArgosUnavailable) && !errors.Is(err, ErrArgosAuth) && !errors.Is(err, ErrArgosForbidden) &&
			!errors.Is(err, ErrArgosNotAllowed) && !errors.Is(err, ErrArgosUnexpected) {
			errs = append(errs, err)
		}
	}
	if _, err := s.store.PurgeArgosWebhooks(ctx, s.ts().Add(-webhookInboxRetention)); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Run atiende la cola de clientes descubiertos, los webhooks y el barrido
// periódico hasta que ctx se cancela. Los fallos de ArgosISP nunca paran el
// módulo (degradación: identidad stale).
func (s *Integrations) Run(ctx context.Context, sweepEvery time.Duration) {
	if sweepEvery <= 0 {
		sweepEvery = 10 * time.Minute
	}
	sweep := time.NewTicker(sweepEvery)
	defer sweep.Stop()
	poll := time.NewTicker(30 * time.Second)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.queue:
			cs := make([]domain.Customer, 0, len(job.ids))
			for _, id := range job.ids {
				if c, err := s.store.GetCustomer(ctx, job.tenant, id); err == nil {
					cs = append(cs, *c)
				}
			}
			if err := s.ResolveCustomers(ctx, job.tenant, cs); err != nil && !errors.Is(err, errArgosSkipped) && ctx.Err() == nil {
				s.logger.DebugContext(ctx, "argos: discovered customers not resolved (the sweep will retry)", slog.Any("error", err))
			}
		case <-s.kick:
			s.processLogged(ctx)
		case <-poll.C:
			s.processLogged(ctx)
		case <-sweep.C:
			if err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
				s.logger.WarnContext(ctx, "argos sweep failed", slog.Any("error", err))
			}
		}
	}
}

func (s *Integrations) processLogged(ctx context.Context) {
	if err := s.ProcessWebhooks(ctx); err != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "argos webhooks processing failed", slog.Any("error", err))
	}
}

// TenantLimiter espacia las peticiones a ArgosISP de cada ISP (≤ rate por segundo).
type TenantLimiter struct {
	mu   sync.Mutex
	next map[uuid.UUID]time.Time
	gap  time.Duration
}

// NewTenantLimiter crea un limitador de perSecond peticiones por segundo y por ISP.
func NewTenantLimiter(perSecond int) *TenantLimiter {
	if perSecond <= 0 {
		perSecond = 10
	}
	return &TenantLimiter{next: map[uuid.UUID]time.Time{}, gap: time.Second / time.Duration(perSecond)}
}

// Wait espera el turno de tenant.
func (l *TenantLimiter) Wait(ctx context.Context, tenant uuid.UUID) error {
	l.mu.Lock()
	now := time.Now()
	at := l.next[tenant]
	if at.Before(now) {
		at = now
	}
	l.next[tenant] = at.Add(l.gap)
	l.mu.Unlock()
	if d := time.Until(at); d > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	return nil
}

// CustomerDirectory implementa devapi.CustomerDirectory (alerts: canal webhook).
type CustomerDirectory struct{ Store IntegrationStore }

// CustomerRef implementa devapi.CustomerDirectory.
func (d CustomerDirectory) CustomerRef(ctx context.Context, tenant, id uuid.UUID) (*devapi.CustomerRef, error) {
	t := pgdb.TenantID(tenant)
	c, err := d.Store.GetCustomer(ctx, t, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, devapi.ErrCustomerNotFound
	}
	if err != nil {
		return nil, err
	}
	ref := &devapi.CustomerRef{ID: c.ID, SiteID: c.SiteID, Address: c.AddressString(), Alias: c.Alias}
	cur, err := d.Store.CurrentIdentities(ctx, t, []uuid.UUID{id})
	if err != nil {
		return nil, err
	}
	if i, ok := cur[id]; ok {
		ref.External = &devapi.ExternalIDs{System: i.System, CustomerID: i.ExternalCustomerID, ServiceID: i.ExternalServiceID}
	}
	return ref, nil
}
