package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/detection/api/customfeeds"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

// Fuentes de reputación de plataforma (D20; /platform/reputation/sources de
// packages/schemas/openapi/v0/platform.yaml): el superadministrador ve el
// catálogo base con su estado de carga, lo activa o desactiva, y da de alta,
// edita, borra y recarga listas personalizadas. Las fuentes viven en
// detection.reputation_source y feedsync las lee en cada sincronización
// (ReputationSources implementa feedsync.Registry).

// PermReputationSources es el permiso de plataforma de estas rutas.
const PermReputationSources = "platform.reputation_sources.manage"

// Códigos de error del contrato.
const (
	CodeSourceNotFound      = "REPUTATION_SOURCE_NOT_FOUND"
	CodeSourceReadOnly      = "REPUTATION_SOURCE_READ_ONLY"
	CodeSourceURLNotAllowed = "REPUTATION_SOURCE_URL_NOT_ALLOWED"
)

// Acciones de auditoría (x-audited).
const (
	AuditSourceCreated   = "platform.reputation_source.created"
	AuditSourceUpdated   = "platform.reputation_source.updated"
	AuditSourceDeleted   = "platform.reputation_source.deleted"
	AuditSourceRefreshed = "platform.reputation_source.refresh_requested"
)

// Límites del contrato (ReputationSourceCreate).
const (
	minFrequency       = 15 * time.Minute
	maxFrequency       = 7 * 24 * time.Hour
	defaultSourceBytes = 64 << 20
	minSourceBytes     = 1 << 10
	maxSourceBytes     = 256 << 20
)

var (
	sourceKeyRe   = regexp.MustCompile(`^[a-z][a-z0-9_]{2,47}$`)
	frequencyRe   = regexp.MustCompile(`^([1-9][0-9]*)(m|h|d)$`)
	headerNameRe  = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	sourceFormats = []string{"netset", "abusech-feodo-csv", "abusech-threatfox-csv", "spamhaus-drop-json"}
	// Categoría del contrato → categoría de reputation (inversa de sourceCategory).
	contractCategory = map[string]reputation.Category{"botnet_cc": reputation.CategoryBotnetCC, "scanner": reputation.CategoryScanner,
		"malware_dist": reputation.CategoryMalware, "mining_pool": reputation.CategoryMining, "proxy_vpn": reputation.CategoryProxy,
		"tor_exit": reputation.CategoryTor, "blocklist": reputation.CategoryBlocklist}
	// catalogNS es el espacio de nombres de los UUID (v5) de las fuentes del catálogo.
	catalogNS = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://horus-flow/detection/reputation-source"))
)

// SourcesPolicy es la validación de las listas personalizadas de la API de
// plataforma: la de customfeeds con la frecuencia mínima del contrato (15 min).
func SourcesPolicy() customfeeds.Policy {
	p := customfeeds.DefaultPolicy()
	p.MinFrequency = minFrequency
	return p
}

// SourceRepo es la persistencia de las fuentes (adapters/postgres).
type SourceRepo interface {
	ListReputationSources(ctx context.Context) ([]domain.ReputationSource, error)
	GetReputationSource(ctx context.Context, id uuid.UUID) (*domain.ReputationSource, error)
	InsertReputationSource(ctx context.Context, r *domain.ReputationSource) error
	UpdateReputationSource(ctx context.Context, r *domain.ReputationSource, expect int) error
	DeleteReputationSource(ctx context.Context, id uuid.UUID, expect int) error
	SyncCatalog(ctx context.Context, catalog []domain.ReputationSource) error
}

// SourceSync es la sincronización de feeds del rol (feedsync.Scheduler).
type SourceSync interface {
	Catalog() ([]datasets.Source, error)
	LoadState(id string) (datasets.State, error)
	Refresh(id string)
	Pending(id string) bool
	Invalidate()
}

// SourcesOptions configura ReputationSources.
type SourcesOptions struct {
	Repo SourceRepo
	Sync SourceSync
	// Sealer cifra la cabecera de autenticación; nil = no se admiten
	// fuentes con autenticación (falta la KEK).
	Sealer *envelope.Sealer
	// Guard comprueba que la URL apunte a una dirección pública (también tras
	// resolver DNS con Guard.Resolver o net.DefaultResolver).
	Guard *datasets.EgressGuard
	// Audit registra las acciones (Service.RecordPlatformAudit).
	Audit  func(ctx context.Context, e authapi.AuditEntry)
	Now    func() time.Time
	Logger *slog.Logger
}

// ReputationSources implementa la API de plataforma de fuentes de reputación.
type ReputationSources struct {
	o         SourcesOptions
	catalogMu sync.Mutex
	catalogOK bool
}

// NewReputationSources crea el servicio.
func NewReputationSources(o SourcesOptions) *ReputationSources {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Guard == nil {
		o.Guard = &datasets.EgressGuard{}
	}
	if o.Audit == nil {
		o.Audit = func(context.Context, authapi.AuditEntry) {}
	}
	return &ReputationSources{o: o}
}

func (s *ReputationSources) now() time.Time { return s.o.Now().UTC().Truncate(time.Microsecond) }

// SourceDoc es ReputationSource del contrato (sin secretos).
type SourceDoc struct {
	ID                  uuid.UUID `json:"id"`
	NumericID           int       `json:"numeric_id"`
	Key                 string    `json:"key"`
	Origin              string    `json:"origin"`
	Name                string    `json:"name"`
	URL                 string    `json:"url"`
	Format              string    `json:"format"`
	Frequency           string    `json:"frequency"`
	Category            string    `json:"category"`
	Confidence          int       `json:"confidence"`
	Enabled             bool      `json:"enabled"`
	License             *string   `json:"license"`
	Notes               *string   `json:"notes"`
	CommercialUse       string    `json:"commercial_use"`
	HasAuth             bool      `json:"has_auth"`
	Status              string    `json:"status"`
	Entries             int       `json:"entries"`
	LastAttemptAt       *string   `json:"last_attempt_at"`
	LastSuccessAt       *string   `json:"last_success_at"`
	LastError           *string   `json:"last_error"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	Version             int       `json:"version"`
	CreatedBy           *string   `json:"created_by"`
	CreatedAt           string    `json:"created_at"`
	UpdatedAt           string    `json:"updated_at"`
}

func nonZeroTS(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	v := ts(t)
	return &v
}

func (s *ReputationSources) doc(r *domain.ReputationSource) SourceDoc {
	d := SourceDoc{ID: r.ID, NumericID: r.NumericID, Key: r.Key, Origin: r.Origin, Name: r.Name, URL: r.URL, Format: r.Format,
		Frequency: r.Frequency, Category: r.Category, Confidence: r.Confidence, Enabled: r.Enabled, License: r.License, Notes: r.Notes,
		CommercialUse: "approved", HasAuth: r.HasAuth(), Version: r.Version, CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt)}
	if r.Origin == domain.OriginCustom {
		d.CommercialUse = "operator_acknowledged"
	}
	if r.CreatedBy != nil {
		v := r.CreatedBy.String()
		d.CreatedBy = &v
	}
	var st datasets.State
	if s.o.Sync != nil {
		var err error
		if st, err = s.o.Sync.LoadState(r.DatasetID); err != nil {
			s.o.Logger.Warn("reputation source state", "source", r.DatasetID, "err", err)
		}
	}
	if st.Current != nil {
		d.Entries = st.Current.Entries
	}
	d.LastAttemptAt, d.LastSuccessAt, d.ConsecutiveFailures = nonZeroTS(st.LastAttempt), nonZeroTS(st.LastSuccess), st.ConsecutiveFailures
	if st.ConsecutiveFailures > 0 && st.LastError != "" {
		e := st.LastError
		d.LastError = &e
	}
	switch {
	case !r.Enabled:
		d.Status = "disabled"
	case s.o.Sync != nil && s.o.Sync.Pending(r.DatasetID), st.LastAttempt.IsZero():
		d.Status = "pending"
	case st.ConsecutiveFailures > 0:
		d.Status = "failing"
	default:
		d.Status = "ok"
	}
	return d
}

// platform comprueba el permiso (el guard ya exige token de plataforma).
func platform(ctx context.Context) (*authz.Principal, error) {
	p := authz.FromContext(ctx)
	if p == nil || p.Scope != authz.ScopePlatform {
		return nil, apperr.Forbidden(problem.CodeTokenScopeInvalid, "")
	}
	if !p.Has(PermReputationSources) {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	return p, nil
}

// FormatFrequency escribe una duración como en el contrato (7d, 6h, 15m).
func FormatFrequency(d time.Duration) string {
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d >= time.Hour && d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	default:
		return strconv.Itoa(max(int(d/time.Minute), 1)) + "m"
	}
}

// ParseFrequency interpreta una frecuencia del contrato.
func ParseFrequency(v string) (time.Duration, bool) {
	m := frequencyRe.FindStringSubmatch(v)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n > 100000 {
		return 0, false
	}
	unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
	return time.Duration(n) * unit, true
}

// catalogRow convierte una fuente de feeds.yaml en su fila (ok = false si
// no se gestiona por la API: sin uso comercial aprobado o clave inválida).
func catalogRow(src datasets.Source, now time.Time) (domain.ReputationSource, bool) {
	key := strings.ReplaceAll(src.ID, "-", "_")
	if src.IsCustom() || src.CommercialUse != datasets.CommercialYes || !sourceKeyRe.MatchString(key) {
		return domain.ReputationSource{}, false
	}
	cat := sourceCategory[src.Category]
	if cat == "" {
		cat = "blocklist"
	}
	maxBytes := src.MaxBytes
	if maxBytes <= 0 {
		maxBytes = datasets.DefaultMaxBytes
	}
	r := domain.ReputationSource{ID: uuid.NewSHA1(catalogNS, []byte(src.ID)), NumericID: int(reputation.SourceID(src.ID)), Key: key,
		DatasetID: src.ID, Origin: domain.OriginCatalog, Name: cmpOr(src.Name, src.ID), URL: src.URL, Format: src.Format,
		Frequency: FormatFrequency(time.Duration(src.Frequency)), Category: cat, Confidence: min(max(src.Confidence, 0), 100),
		Enabled: src.IsEnabled(), License: optStr(src.License), Notes: optStr(strings.TrimSpace(src.Notes)),
		MinEntries: max(src.MinEntries, 0), MaxBytes: maxBytes, CreatedAt: now, UpdatedAt: now, Version: 1}
	return r, true
}

func cmpOr(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func optStr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// SyncCatalog concilia las filas del catálogo con la declaración de feeds
// (al arrancar y antes de la primera petición).
func (s *ReputationSources) SyncCatalog(ctx context.Context) error {
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	if s.catalogOK {
		return nil
	}
	if s.o.Sync == nil {
		s.catalogOK = true
		return nil
	}
	srcs, err := s.o.Sync.Catalog()
	if err != nil {
		return err
	}
	now := s.now()
	rows := make([]domain.ReputationSource, 0, len(srcs))
	for _, src := range srcs {
		if r, ok := catalogRow(src, now); ok {
			rows = append(rows, r)
		}
	}
	if err := s.o.Repo.SyncCatalog(ctx, rows); err != nil {
		return err
	}
	s.catalogOK = true
	return nil
}

func (s *ReputationSources) begin(ctx context.Context) (*authz.Principal, error) {
	p, err := platform(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.SyncCatalog(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// SourceFilter son los filtros de GET /platform/reputation/sources.
type SourceFilter struct {
	Origin  string
	Enabled *bool
}

// ParseSourceFilter lee origin y enabled de la query.
func ParseSourceFilter(q url.Values) (SourceFilter, error) {
	var f SourceFilter
	if v := q.Get("origin"); v != "" {
		if v != domain.OriginCatalog && v != domain.OriginCustom {
			return f, apperr.Validation(apperr.Field("origin", "INVALID", "catalog o custom"))
		}
		f.Origin = v
	}
	if v := q.Get("enabled"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return f, apperr.Validation(apperr.Field("enabled", "INVALID", "true o false"))
		}
		f.Enabled = &b
	}
	return f, nil
}

// List devuelve las fuentes con su estado de carga.
func (s *ReputationSources) List(ctx context.Context, f SourceFilter) ([]SourceDoc, error) {
	if _, err := s.begin(ctx); err != nil {
		return nil, err
	}
	rows, err := s.o.Repo.ListReputationSources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SourceDoc, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		if (f.Origin != "" && r.Origin != f.Origin) || (f.Enabled != nil && r.Enabled != *f.Enabled) {
			continue
		}
		out = append(out, s.doc(r))
	}
	return out, nil
}

func (s *ReputationSources) get(ctx context.Context, id uuid.UUID) (*domain.ReputationSource, error) {
	r, err := s.o.Repo.GetReputationSource(ctx, id)
	if errors.Is(err, domain.ErrSourceNotFound) {
		return nil, apperr.NotFound(CodeSourceNotFound)
	}
	return r, err
}

// Get devuelve una fuente.
func (s *ReputationSources) Get(ctx context.Context, id uuid.UUID) (SourceDoc, error) {
	if _, err := s.begin(ctx); err != nil {
		return SourceDoc{}, err
	}
	r, err := s.get(ctx, id)
	if err != nil {
		return SourceDoc{}, err
	}
	return s.doc(r), nil
}

// SourceCreate es ReputationSourceCreate.
type SourceCreate struct {
	Key               string  `json:"key"`
	Name              string  `json:"name"`
	URL               string  `json:"url"`
	Format            string  `json:"format"`
	Frequency         string  `json:"frequency"`
	Category          string  `json:"category"`
	Confidence        *int    `json:"confidence"`
	Enabled           *bool   `json:"enabled"`
	License           *string `json:"license"`
	Notes             *string `json:"notes"`
	MinEntries        *int    `json:"min_entries"`
	MaxBytes          *int64  `json:"max_bytes"`
	AuthHeaderName    *string `json:"auth_header_name"`
	AuthHeaderValue   *string `json:"auth_header_value"`
	TermsAcknowledged *bool   `json:"terms_acknowledged"`
}

// ReqMeta son los datos de la petición para la auditoría.
type ReqMeta = AuditMeta

type fieldErrs []problem.FieldError

func (f *fieldErrs) add(field, code, msg string) { *f = append(*f, apperr.Field(field, code, msg)) }

// validateDef comprueba los campos editables de r (contrato) y lo que exige
// feedsync para cargarla (customfeeds.Validate).
func validateDef(r *domain.ReputationSource, errs *fieldErrs) {
	if n := utf8.RuneCountInString(strings.TrimSpace(r.Name)); n == 0 || n > 120 {
		errs.add("name", "INVALID", "Entre 1 y 120 caracteres")
	}
	if len(r.URL) > 2048 || !strings.HasPrefix(r.URL, "https://") {
		errs.add("url", "INVALID", "Una URL https:// de hasta 2048 caracteres")
	} else if u, err := url.Parse(r.URL); err != nil || u.Host == "" {
		errs.add("url", "INVALID", "URL ilegible")
	}
	if !contains(sourceFormats, r.Format) {
		errs.add("format", "INVALID", "Formato no soportado ("+strings.Join(sourceFormats, ", ")+")")
	}
	if d, ok := ParseFrequency(r.Frequency); !ok || d < minFrequency || d > maxFrequency {
		errs.add("frequency", "INVALID", "Entre 15 min y 7 días, p. ej. 1h, 6h o 24h")
	}
	if _, ok := contractCategory[r.Category]; !ok {
		errs.add("category", "INVALID", "Categoría desconocida")
	}
	if r.Confidence < 0 || r.Confidence > 100 {
		errs.add("confidence", "INVALID", "Entre 0 y 100")
	}
	if r.License != nil && utf8.RuneCountInString(*r.License) > 200 {
		errs.add("license", "INVALID", "Hasta 200 caracteres")
	}
	if r.Notes != nil && utf8.RuneCountInString(*r.Notes) > 1000 {
		errs.add("notes", "INVALID", "Hasta 1000 caracteres")
	}
	if r.MinEntries < 0 {
		errs.add("min_entries", "INVALID", "No puede ser negativo")
	}
	if r.MaxBytes < minSourceBytes || r.MaxBytes > maxSourceBytes {
		errs.add("max_bytes", "INVALID", fmt.Sprintf("Entre %d y %d", minSourceBytes, maxSourceBytes))
	}
	if r.AuthHeaderName != nil && !headerNameRe.MatchString(*r.AuthHeaderName) {
		errs.add("auth_header_name", "INVALID", "Letras, dígitos y guiones (hasta 64)")
	}
	if len(*errs) > 0 || r.Origin != domain.OriginCustom {
		return
	}
	// Lo que feedsync volverá a comprobar al cargarla: si no pasa, la fuente
	// se quedaría fuera del snapshot sin avisar.
	var ve *customfeeds.ValidationError
	if err := customfeeds.Validate(SourceSpec(r, nil), SourcesPolicy()); errors.As(err, &ve) {
		for _, p := range ve.Problems {
			field := p.Field
			switch field {
			case "id":
				field = "key"
			case "url":
				continue // lo cubre checkURL (REPUTATION_SOURCE_URL_NOT_ALLOWED)
			}
			errs.add(field, "INVALID", p.Message)
		}
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// checkURL aplica la política de destinos (D20): sin credenciales ni
// fragmento, host público y distinto de la instalación, también tras
// resolver DNS. Un nombre que no resuelve ahora no se rechaza (lo vuelve a
// comprobar el descargador en cada carga).
func (s *ReputationSources) checkURL(ctx context.Context, r *domain.ReputationSource) error {
	notAllowed := func(msg string) error {
		return &apperr.Error{Kind: apperr.KindInvalid, Code: CodeSourceURLNotAllowed, Detail: msg,
			Fields: []problem.FieldError{apperr.Field("url", "URL_NOT_ALLOWED", msg)}}
	}
	var ve *customfeeds.ValidationError
	if err := customfeeds.Validate(SourceSpec(r, nil), SourcesPolicy()); errors.As(err, &ve) {
		for _, p := range ve.Problems {
			if p.Field == "url" {
				return notAllowed("Usa una URL https pública, sin credenciales (" + p.Message + ")")
			}
		}
	}
	u, err := url.Parse(r.URL)
	if err != nil {
		return notAllowed("URL ilegible")
	}
	host := u.Hostname()
	if a, err := netip.ParseAddr(host); err == nil {
		if err := s.o.Guard.CheckAddr(a); err != nil {
			return notAllowed("El destino no es una dirección pública")
		}
		return nil
	}
	var res datasets.Resolver = net.DefaultResolver
	if s.o.Guard.Resolver != nil {
		res = s.o.Guard.Resolver
	}
	lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := res.LookupNetIP(lctx, "ip", host)
	if err != nil {
		s.o.Logger.InfoContext(ctx, "reputation source: host not resolvable now", "host", host, "err", err)
		return nil
	}
	for _, a := range addrs {
		if err := s.o.Guard.CheckAddr(a); err != nil {
			return notAllowed("El nombre resuelve a una dirección no pública o de la instalación")
		}
	}
	return nil
}

// SourceSpec convierte una fuente personalizada en la definición que carga
// feedsync (header = cabecera de autenticación descifrada, o nil).
func SourceSpec(r *domain.ReputationSource, header http.Header) customfeeds.SourceSpec {
	freq, _ := ParseFrequency(r.Frequency)
	on := r.Enabled
	spec := customfeeds.SourceSpec{ID: r.DatasetID, Name: r.Name, URL: r.URL, Format: r.Format,
		Category: string(contractCategory[r.Category]), Confidence: max(r.Confidence, 1), Frequency: datasets.Duration(freq),
		MaxBytes: r.MaxBytes, MinEntries: r.MinEntries, Enabled: &on, UpdatedAt: r.UpdatedAt, Header: header}
	if r.License != nil {
		spec.License = *r.License
	}
	if r.Notes != nil {
		spec.Notes = *r.Notes
	}
	if r.CreatedBy != nil {
		spec.CreatedBy = r.CreatedBy.String()
	}
	return spec
}

func authAAD(id uuid.UUID) []byte { return []byte("reputation_source:" + id.String()) }

func (s *ReputationSources) seal(r *domain.ReputationSource, value string) error {
	if s.o.Sealer == nil {
		return apperr.Validation(apperr.Field("auth_header_value", "UNAVAILABLE",
			"La instalación no tiene clave de cifrado para credenciales de feeds (HORUS_DETECTION_KEK_FILE)"))
	}
	ct, dek, err := s.o.Sealer.Seal([]byte(value), authAAD(r.ID))
	if err != nil {
		return err
	}
	r.AuthSecret = &domain.SealedSecret{Ciphertext: ct, DEK: dek, KEKID: s.o.Sealer.KEKID()}
	return nil
}

func actorUUID(p *authz.Principal) *uuid.UUID {
	if id, err := uuid.Parse(p.Subject); err == nil {
		return &id
	}
	return nil
}

// Create da de alta una lista personalizada y encola su primera carga.
func (s *ReputationSources) Create(ctx context.Context, in SourceCreate, meta ReqMeta) (SourceDoc, error) {
	p, err := s.begin(ctx)
	if err != nil {
		return SourceDoc{}, err
	}
	var errs fieldErrs
	if !sourceKeyRe.MatchString(in.Key) {
		errs.add("key", "INVALID", "Solo minúsculas, números y guion bajo (3–48), empezando por letra")
	}
	for f, v := range map[string]string{"name": in.Name, "url": in.URL, "format": in.Format, "frequency": in.Frequency, "category": in.Category} {
		if strings.TrimSpace(v) == "" {
			errs.add(f, "REQUIRED", "Obligatorio")
		}
	}
	if in.Confidence == nil {
		errs.add("confidence", "REQUIRED", "Obligatorio")
	}
	if in.TermsAcknowledged == nil || !*in.TermsAcknowledged {
		errs.add("terms_acknowledged", "REQUIRED", "Confirma que los términos de la lista permiten su uso")
	}
	if len(errs) > 0 {
		return SourceDoc{}, apperr.Validation(errs...)
	}
	now := s.now()
	r := &domain.ReputationSource{ID: uuid.Must(uuid.NewV7()), Key: in.Key, DatasetID: customfeeds.IDPrefix + in.Key,
		Origin: domain.OriginCustom, Name: strings.TrimSpace(in.Name), URL: strings.TrimSpace(in.URL), Format: in.Format,
		Frequency: in.Frequency, Category: in.Category, Confidence: *in.Confidence, Enabled: in.Enabled == nil || *in.Enabled,
		License: nullIfBlank(in.License), Notes: nullIfBlank(in.Notes), MinEntries: 1, MaxBytes: defaultSourceBytes,
		TermsAcknowledgedAt: &now, CreatedBy: actorUUID(p), CreatedAt: now, UpdatedAt: now, Version: 1}
	r.NumericID = int(reputation.SourceID(r.DatasetID))
	if in.MinEntries != nil {
		r.MinEntries = *in.MinEntries
	}
	if in.MaxBytes != nil {
		r.MaxBytes = *in.MaxBytes
	}
	name, value := nullIfBlank(in.AuthHeaderName), nullIfBlank(in.AuthHeaderValue)
	switch {
	case name != nil && value == nil:
		errs.add("auth_header_value", "REQUIRED", "Indica el valor de la cabecera")
	case name == nil && value != nil:
		errs.add("auth_header_name", "REQUIRED", "Indica el nombre de la cabecera")
	}
	if value != nil && len(*value) > 512 {
		errs.add("auth_header_value", "INVALID", "Hasta 512 caracteres")
	}
	r.AuthHeaderName = name
	validateDef(r, &errs)
	if len(errs) > 0 {
		return SourceDoc{}, apperr.Validation(errs...)
	}
	if err := s.checkURL(ctx, r); err != nil {
		return SourceDoc{}, err
	}
	if value != nil {
		if err := s.seal(r, *value); err != nil {
			return SourceDoc{}, err
		}
	}
	if err := s.o.Repo.InsertReputationSource(ctx, r); err != nil {
		if errors.Is(err, domain.ErrSourceExists) {
			msg := "Ya existe una fuente con esa clave (o con su mismo identificador numérico): elige otra"
			return SourceDoc{}, &apperr.Error{Kind: apperr.KindConflict, Code: problem.CodeAlreadyExists, Detail: msg,
				Fields: []problem.FieldError{apperr.Field("key", problem.CodeAlreadyExists, msg)}}
		}
		return SourceDoc{}, err
	}
	s.audit(ctx, p, AuditSourceCreated, r, meta, map[string]any{"key": r.Key, "name": r.Name, "url": r.URL, "format": r.Format,
		"frequency": r.Frequency, "category": r.Category, "confidence": r.Confidence, "enabled": r.Enabled, "has_auth": r.HasAuth(),
		"terms_acknowledged": true})
	if r.Enabled && s.o.Sync != nil {
		s.o.Sync.Refresh(r.DatasetID)
	}
	return s.doc(r), nil
}

func nullIfBlank(v *string) *string {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil
	}
	t := strings.TrimSpace(*v)
	return &t
}

// SourcePatch es ReputationSourcePatch (JSON merge patch: un campo ausente
// no cambia; null borra los anulables).
type SourcePatch map[string]json.RawMessage

var patchFields = map[string]bool{"name": true, "url": true, "format": true, "frequency": true, "category": true, "confidence": true,
	"enabled": true, "license": true, "notes": true, "min_entries": true, "max_bytes": true, "auth_header_name": true,
	"auth_header_value": true}

func (p SourcePatch) isNull(k string) bool { return string(p[k]) == "null" }

func (p SourcePatch) str(k string, errs *fieldErrs, nullable bool) (val *string, set bool) {
	raw, ok := p[k]
	if !ok {
		return nil, false
	}
	if p.isNull(k) {
		if !nullable {
			errs.add(k, "INVALID_TYPE", "No puede ser null")
		}
		return nil, true
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		errs.add(k, "INVALID_TYPE", "Debe ser texto")
		return nil, true
	}
	return &v, true
}

func (p SourcePatch) num(k string, errs *fieldErrs) (*int64, bool) {
	raw, ok := p[k]
	if !ok {
		return nil, false
	}
	var v int64
	if err := json.Unmarshal(raw, &v); err != nil || p.isNull(k) {
		errs.add(k, "INVALID_TYPE", "Debe ser un número entero")
		return nil, true
	}
	return &v, true
}

// Update aplica un merge patch (del catálogo solo `enabled`).
func (s *ReputationSources) Update(ctx context.Context, id uuid.UUID, ifMatch int, patch SourcePatch, meta ReqMeta) (SourceDoc, error) {
	p, err := s.begin(ctx)
	if err != nil {
		return SourceDoc{}, err
	}
	cur, err := s.get(ctx, id)
	if err != nil {
		return SourceDoc{}, err
	}
	if ifMatch != cur.Version {
		return SourceDoc{}, apperr.PreconditionFailed(s.doc(cur))
	}
	var errs fieldErrs
	for k := range patch {
		if !patchFields[k] {
			errs.add(k, "UNKNOWN_FIELD", "Campo no permitido")
		}
	}
	if len(errs) > 0 {
		return SourceDoc{}, apperr.Validation(errs...)
	}
	if cur.Origin == domain.OriginCatalog {
		for k := range patch {
			if k != "enabled" {
				return SourceDoc{}, apperr.New(apperr.KindInvalid, CodeSourceReadOnly,
					"Del catálogo base solo se puede activar o desactivar")
			}
		}
	}
	next := *cur
	changes := map[string]any{}
	if v, set := patch.str("name", &errs, false); set && v != nil {
		next.Name = strings.TrimSpace(*v)
	}
	if v, set := patch.str("url", &errs, false); set && v != nil {
		next.URL = strings.TrimSpace(*v)
	}
	if v, set := patch.str("format", &errs, false); set && v != nil {
		next.Format = *v
	}
	if v, set := patch.str("frequency", &errs, false); set && v != nil {
		next.Frequency = *v
	}
	if v, set := patch.str("category", &errs, false); set && v != nil {
		next.Category = *v
	}
	if v, set := patch.str("license", &errs, true); set {
		next.License = nullIfBlank(v)
	}
	if v, set := patch.str("notes", &errs, true); set {
		next.Notes = nullIfBlank(v)
	}
	if v, set := patch.num("confidence", &errs); set && v != nil {
		next.Confidence = int(*v)
	}
	if v, set := patch.num("min_entries", &errs); set && v != nil {
		next.MinEntries = int(*v)
	}
	if v, set := patch.num("max_bytes", &errs); set && v != nil {
		next.MaxBytes = *v
	}
	if raw, ok := patch["enabled"]; ok {
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil || patch.isNull("enabled") {
			errs.add("enabled", "INVALID_TYPE", "Debe ser true o false")
		} else {
			next.Enabled = b
		}
	}
	// Autenticación: name null (o value null) la quita; value nuevo la
	// sustituye; cambiar solo el nombre conserva el valor (se vuelve a sellar
	// con el mismo AAD, ligado al id).
	authChanged := false
	hName, nameSet := patch.str("auth_header_name", &errs, true)
	hValue, valueSet := patch.str("auth_header_value", &errs, true)
	if valueSet && hValue != nil && len(*hValue) > 512 {
		errs.add("auth_header_value", "INVALID", "Hasta 512 caracteres")
	}
	if len(errs) > 0 {
		return SourceDoc{}, apperr.Validation(errs...)
	}
	switch {
	case (nameSet && nullIfBlank(hName) == nil) || (valueSet && nullIfBlank(hValue) == nil):
		authChanged = cur.HasAuth() || cur.AuthHeaderName != nil
		next.AuthHeaderName, next.AuthSecret = nil, nil
	case valueSet:
		if nameSet {
			next.AuthHeaderName = nullIfBlank(hName)
		}
		if next.AuthHeaderName == nil {
			errs.add("auth_header_name", "REQUIRED", "Indica el nombre de la cabecera")
			break
		}
		if err := s.seal(&next, strings.TrimSpace(*hValue)); err != nil {
			return SourceDoc{}, err
		}
		authChanged = true
	case nameSet:
		if !cur.HasAuth() {
			errs.add("auth_header_value", "REQUIRED", "Indica el valor de la cabecera")
			break
		}
		next.AuthHeaderName = nullIfBlank(hName)
		authChanged = next.AuthHeaderName == nil || cur.AuthHeaderName == nil || *next.AuthHeaderName != *cur.AuthHeaderName
	}
	if next.Origin == domain.OriginCustom { // del catálogo solo cambia enabled
		validateDef(&next, &errs)
	}
	if len(errs) > 0 {
		return SourceDoc{}, apperr.Validation(errs...)
	}
	if next.URL != cur.URL {
		if err := s.checkURL(ctx, &next); err != nil {
			return SourceDoc{}, err
		}
	}
	diff := func(k string, a, b any) {
		if fmt.Sprint(a) != fmt.Sprint(b) {
			changes[k] = map[string]any{"from": a, "to": b}
		}
	}
	diff("name", cur.Name, next.Name)
	diff("url", cur.URL, next.URL)
	diff("format", cur.Format, next.Format)
	diff("frequency", cur.Frequency, next.Frequency)
	diff("category", cur.Category, next.Category)
	diff("confidence", cur.Confidence, next.Confidence)
	diff("enabled", cur.Enabled, next.Enabled)
	diff("license", deref(cur.License), deref(next.License))
	diff("notes", deref(cur.Notes), deref(next.Notes))
	diff("min_entries", cur.MinEntries, next.MinEntries)
	diff("max_bytes", cur.MaxBytes, next.MaxBytes)
	if authChanged {
		changes["auth"] = map[string]any{"from": cur.HasAuth(), "to": next.HasAuth()} // nunca el valor
	}
	if len(changes) == 0 {
		return s.doc(cur), nil
	}
	next.Version, next.UpdatedAt = cur.Version+1, s.now()
	if err := s.o.Repo.UpdateReputationSource(ctx, &next, cur.Version); err != nil {
		return SourceDoc{}, s.writeErr(ctx, id, err)
	}
	s.audit(ctx, p, AuditSourceUpdated, &next, meta, changes)
	if s.o.Sync != nil {
		_, refetch := changes["url"]
		_, fmtChanged := changes["format"]
		switch {
		case next.Enabled && (refetch || fmtChanged || authChanged || !cur.Enabled):
			s.o.Sync.Refresh(next.DatasetID)
			s.o.Sync.Invalidate()
		default:
			s.o.Sync.Invalidate() // categoría, confianza o activación: recompilar el snapshot
		}
	}
	return s.doc(&next), nil
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// writeErr traduce los errores de escritura condicionada.
func (s *ReputationSources) writeErr(ctx context.Context, id uuid.UUID, err error) error {
	switch {
	case errors.Is(err, domain.ErrSourceNotFound):
		return apperr.NotFound(CodeSourceNotFound)
	case errors.Is(err, domain.ErrSourceStale):
		if cur, gerr := s.get(ctx, id); gerr == nil {
			return apperr.PreconditionFailed(s.doc(cur))
		}
		return apperr.PreconditionFailed(map[string]any{})
	}
	return err
}

// Delete borra una lista personalizada (sus indicadores salen del siguiente
// snapshot). Las del catálogo no se borran: se desactivan.
func (s *ReputationSources) Delete(ctx context.Context, id uuid.UUID, ifMatch int, meta ReqMeta) error {
	p, err := s.begin(ctx)
	if err != nil {
		return err
	}
	cur, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	if cur.Origin == domain.OriginCatalog {
		return apperr.New(apperr.KindInvalid, CodeSourceReadOnly, "Las fuentes del catálogo base no se borran: desactívalas")
	}
	if ifMatch != cur.Version {
		return apperr.PreconditionFailed(s.doc(cur))
	}
	if err := s.o.Repo.DeleteReputationSource(ctx, id, cur.Version); err != nil {
		return s.writeErr(ctx, id, err)
	}
	s.audit(ctx, p, AuditSourceDeleted, cur, meta, map[string]any{"key": cur.Key, "name": cur.Name, "url": cur.URL})
	if s.o.Sync != nil {
		s.o.Sync.Invalidate()
	}
	return nil
}

// Refresh encola una carga inmediata (asíncrona; 202).
func (s *ReputationSources) Refresh(ctx context.Context, id uuid.UUID, meta ReqMeta) (SourceDoc, error) {
	p, err := s.begin(ctx)
	if err != nil {
		return SourceDoc{}, err
	}
	cur, err := s.get(ctx, id)
	if err != nil {
		return SourceDoc{}, err
	}
	if !cur.Enabled {
		return SourceDoc{}, apperr.New(apperr.KindInvalid, problem.CodeValidationFailed, "La fuente está desactivada: actívala para cargarla")
	}
	if s.o.Sync == nil {
		return SourceDoc{}, apperr.New(apperr.KindUnavailable, problem.CodeServiceUnavailable, "La sincronización de feeds no está disponible")
	}
	s.o.Sync.Refresh(cur.DatasetID)
	s.audit(ctx, p, AuditSourceRefreshed, cur, meta, map[string]any{})
	return s.doc(cur), nil
}

func (s *ReputationSources) audit(ctx context.Context, p *authz.Principal, action string, r *domain.ReputationSource, meta ReqMeta,
	changes map[string]any) {
	changes["key"] = r.Key
	s.o.Audit(ctx, authapi.AuditEntry{OccurredAt: s.now(), ActorType: p.Type, ActorID: p.Subject, ViaPlatform: p.ViaPlatform,
		Action: action, ResourceType: "reputation_source", ResourceID: r.ID.String(), Scope: "platform", Outcome: "success",
		IP: meta.IP, UserAgent: meta.UserAgent, RequestID: meta.RequestID, Changes: changes})
}

// CustomSources implementa feedsync.Registry: las listas personalizadas con
// su cabecera de autenticación descifrada (solo en memoria).
func (s *ReputationSources) CustomSources(ctx context.Context) ([]customfeeds.SourceSpec, error) {
	rows, err := s.o.Repo.ListReputationSources(ctx)
	if err != nil {
		return nil, err
	}
	var out []customfeeds.SourceSpec
	for i := range rows {
		r := &rows[i]
		if r.Origin != domain.OriginCustom {
			continue
		}
		var h http.Header
		if r.HasAuth() {
			if s.o.Sealer == nil {
				s.o.Logger.WarnContext(ctx, "reputation source auth cannot be opened: no KEK", "source", r.DatasetID)
			} else if v, err := s.o.Sealer.Open(r.AuthSecret.Ciphertext, r.AuthSecret.DEK, authAAD(r.ID)); err != nil {
				s.o.Logger.WarnContext(ctx, "reputation source auth cannot be opened", "source", r.DatasetID, "err", err)
			} else {
				h = http.Header{}
				h.Set(*r.AuthHeaderName, string(v))
			}
		}
		out = append(out, SourceSpec(r, h))
	}
	return out, nil
}

// CatalogEnabled implementa feedsync.Registry.
func (s *ReputationSources) CatalogEnabled(ctx context.Context) (map[string]bool, error) {
	rows, err := s.o.Repo.ListReputationSources(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, r := range rows {
		if r.Origin == domain.OriginCatalog {
			out[r.DatasetID] = r.Enabled
		}
	}
	return out, nil
}

// RecordPlatformAudit registra una acción de plataforma: en proceso con auth
// si está local; si no, por el evento horus.detection.audit.recorded.
func (s *Service) RecordPlatformAudit(ctx context.Context, e authapi.AuditEntry) {
	if e.OccurredAt.IsZero() {
		e.OccurredAt = s.now().UTC()
	}
	if s.audit != nil {
		if err := s.audit.Record(ctx, e); err != nil {
			s.log.ErrorContext(ctx, "audit record failed", slog.String("action", e.Action), slog.Any("error", err))
		}
		return
	}
	if s.db == nil {
		return
	}
	id := uuid.Must(uuid.NewV7())
	actor := outbox.Actor{Type: e.ActorType, ID: e.ActorID, ViaPlatform: e.ViaPlatform}
	changes := e.Changes
	if changes == nil {
		changes = map[string]any{}
	}
	data := map[string]any{"id": id, "tenant_id": nil, "occurred_at": ts(e.OccurredAt), "source_service": "detection", "actor": actor,
		"ip": nilIfEmpty(e.IP), "user_agent": nilIfEmpty(e.UserAgent), "action": e.Action, "resource_type": e.ResourceType,
		"resource_id": e.ResourceID, "scope": e.Scope, "outcome": e.Outcome, "reason": nil, "changes": changes,
		"request_id": nilIfEmpty(e.RequestID), "trace_id": nil}
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		return outbox.Insert(ctx, tx, schema, outbox.Event{ID: id, Type: EventAudit, Source: source, AggregateType: "audit",
			AggregateID: id, AggregateVersion: 1, Actor: actor, OccurredAt: e.OccurredAt, Data: data})
	})
	if err != nil {
		s.log.ErrorContext(ctx, "audit outbox failed", slog.String("action", e.Action), slog.Any("error", err))
	}
}

func nilIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}
