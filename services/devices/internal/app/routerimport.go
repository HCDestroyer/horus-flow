package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// Errores del lector de RouterOS.
var (
	ErrRouterUnreachable = errors.New("devices: router unreachable")
	ErrRouterAuth        = errors.New("devices: router rejected credentials")
	ErrRouterFingerprint = errors.New("devices: router TLS fingerprint changed")
)

// ReadTarget es el router a leer por el túnel.
type ReadTarget struct {
	TunnelIP string
	User     string
	Password string
	Pinned   string
}

// RouterReader lee (solo lectura) lo necesario para proponer prefijos.
// Devuelve la huella TLS observada aunque falle.
type RouterReader interface {
	Read(ctx context.Context, t ReadTarget) (domain.RouterFacts, string, error)
}

// ImportStore es el repositorio de la importación.
type ImportStore interface {
	GetRouter(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Router, error)
	GetSite(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Site, error)
	ListPrefixes(ctx context.Context, t pgdb.TenantID, q ListQuery) ([]domain.ClientPrefix, error)
	// CreatePrefixes crea todos o ninguno (solape → ErrOverlap).
	CreatePrefixes(ctx context.Context, t pgdb.TenantID, ps []*domain.ClientPrefix, ev Events[domain.ClientPrefix]) error
	RecordCredentialUse(ctx context.Context, t pgdb.TenantID, id uuid.UUID, result string, fingerprint *string, at time.Time) error
	Emit(ctx context.Context, evs []outbox.Event) error
}

// Importer implementa I1-28: propuesta de prefijos leída del MikroTik y
// confirmación por lotes.
type Importer struct {
	store   ImportStore
	creds   *Onboarding
	reader  RouterReader
	exclude []netip.Prefix
	audit   func() (authapi.AuditRecorder, bool)
	now     func() time.Time
	logger  *slog.Logger
}

// NewImporter crea el servicio. exclude son los rangos de túneles de Horus.
func NewImporter(store ImportStore, creds *Onboarding, reader RouterReader, exclude []netip.Prefix,
	audit func() (authapi.AuditRecorder, bool), now func() time.Time, logger *slog.Logger,
) *Importer {
	if now == nil {
		now = time.Now
	}
	if audit == nil {
		audit = func() (authapi.AuditRecorder, bool) { return nil, false }
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Importer{store: store, creds: creds, reader: reader, exclude: exclude, audit: audit, now: now, logger: logger}
}

// Preview es PrefixImportPreview.
type Preview struct {
	RouterID       uuid.UUID
	ReadAt         time.Time
	Version        string
	TLSFingerprint string
	Items          []domain.ImportItem
}

func (im *Importer) record(ctx context.Context, p *authz.Principal, t pgdb.TenantID, action, rid string, changes map[string]any) {
	e := authapi.AuditEntry{TenantID: t.UUID(), OccurredAt: im.now().UTC(), ActorType: p.Type, ActorID: p.Subject, ViaPlatform: p.ViaPlatform,
		Action: action, ResourceType: "router", ResourceID: rid, Scope: "tenant", Outcome: "success", Changes: changes}
	if rec, ok := im.audit(); ok {
		if err := rec.Record(ctx, e); err != nil {
			im.logger.ErrorContext(ctx, "audit failed", slog.String("action", action), slog.Any("error", err))
		}
		return
	}
	id := uuid.Must(uuid.NewV7())
	tid := t.UUID()
	actor := outbox.ActorFrom(p)
	ev := outbox.Event{ID: id, Type: "horus.devices.audit.recorded", Source: "horus/devices", TenantID: &tid, AggregateType: "audit",
		AggregateID: id, AggregateVersion: 1, Actor: actor, OccurredAt: e.OccurredAt, Data: map[string]any{
			"id": id.String(), "tenant_id": tid.String(), "occurred_at": e.OccurredAt.Format(outbox.TimeFormat), "source_service": "devices",
			"actor": actor, "ip": nil, "user_agent": nil, "action": action, "resource_type": "router", "resource_id": rid, "scope": "tenant",
			"outcome": "success", "reason": nil, "changes": changes, "request_id": nil, "trace_id": nil,
		}}
	if err := im.store.Emit(ctx, []outbox.Event{ev}); err != nil {
		im.logger.ErrorContext(ctx, "audit outbox failed", slog.String("action", action), slog.Any("error", err))
	}
}

// PreviewImport lee el router por el túnel y propone prefijos sin aplicar
// nada (I1-28 criterios 1, 3 y 4).
func (im *Importer) PreviewImport(ctx context.Context, routerID uuid.UUID, acceptFingerprint *string) (*Preview, error) {
	p, t, err := scope(ctx, "sites.update")
	if err != nil {
		return nil, err
	}
	r, err := im.store.GetRouter(ctx, t, routerID)
	if err != nil || !siteAllowed(p, "sites.update", r.SiteID) {
		return nil, apperr.NotFound(domain.CodeRouterNotFound)
	}
	if r.TunnelAddress == nil {
		return nil, apperr.New(apperr.KindBadGateway, domain.CodeRouterUnreachable, "El router aún no tiene túnel con Horus: pegue el script de alta.")
	}
	cred, password, err := im.creds.APICredential(ctx, t, r.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, apperr.Conflict(problem.CodeConflict, "No hay credencial de la API de RouterOS: genere el script de alta.")
	}
	if err != nil {
		return nil, err
	}
	pinned := ""
	if cred.TLSFingerprintSHA256 != nil {
		pinned = *cred.TLSFingerprintSHA256
	}
	target := ReadTarget{TunnelIP: *r.TunnelAddress, User: cred.Username, Password: password, Pinned: pinned}
	accepting := acceptFingerprint != nil && strings.TrimSpace(*acceptFingerprint) != ""
	if accepting {
		target.Pinned = strings.ToUpper(strings.TrimSpace(*acceptFingerprint))
	}
	now := im.now().UTC()
	facts, observed, err := im.reader.Read(ctx, target)
	result := func(res string, fp *string) {
		if err := im.store.RecordCredentialUse(ctx, t, cred.ID, res, fp, now); err != nil {
			im.logger.WarnContext(ctx, "record credential use failed", slog.Any("error", err))
		}
	}
	switch {
	case errors.Is(err, ErrRouterFingerprint):
		result("tls_fingerprint_changed", nil)
		return nil, apperr.Conflict(domain.CodeTLSFingerprintChange,
			fmt.Sprintf("La huella TLS del router cambió (nueva: %s). Si lo esperaba, confírmela con accept_new_tls_fingerprint.", observed))
	case errors.Is(err, ErrRouterAuth):
		result("auth_failed", nil)
		return nil, apperr.New(apperr.KindBadGateway, domain.CodeRouterUnreachable, "El router rechazó el usuario de solo lectura de Horus.")
	case err != nil:
		result("unreachable", nil)
		return nil, apperr.New(apperr.KindBadGateway, domain.CodeRouterUnreachable, "El router no responde por el túnel.")
	}
	fp := observed
	result("ok", &fp)
	switch {
	case pinned == "":
		im.record(ctx, p, t, "devices.router.tls_fingerprint_pinned", r.ID.String(), map[string]any{"tls_fingerprint_sha256": fp})
	case accepting && !strings.EqualFold(pinned, fp):
		im.record(ctx, p, t, "devices.router.tls_fingerprint_accepted", r.ID.String(),
			map[string]any{"previous": pinned, "tls_fingerprint_sha256": fp})
	}
	existing, err := im.store.ListPrefixes(ctx, t, ListQuery{SiteID: r.SiteID, Limit: 10000, SortCol: "created_at"})
	if err != nil {
		return nil, err
	}
	return &Preview{RouterID: r.ID, ReadAt: now, Version: facts.Version, TLSFingerprint: fp, Items: domain.BuildImport(facts, existing, im.exclude)}, nil
}

// BatchCreatePrefixes crea una selección de prefijos (importación o modo
// descubrimiento) todo o nada y publica client_prefix.created por cada uno.
func (im *Importer) BatchCreatePrefixes(ctx context.Context, siteID uuid.UUID, items []Fields) ([]*domain.ClientPrefix, error) {
	pr, t, err := scope(ctx, "sites.update")
	if err != nil {
		return nil, err
	}
	if _, err := im.store.GetSite(ctx, t, siteID); err != nil || !siteAllowed(pr, "sites.update", siteID) {
		return nil, apperr.NotFound(domain.CodeSiteNotFound)
	}
	if len(items) == 0 || len(items) > 500 {
		return nil, apperr.Validation(apperr.Field("items", "OUT_OF_RANGE", "Entre 1 y 500 prefijos."))
	}
	now := im.now().UTC()
	out := make([]*domain.ClientPrefix, 0, len(items))
	var all fieldErrs
	for i, fields := range items {
		cp := &domain.ClientPrefix{ID: uuid.Must(uuid.NewV7()), TenantID: t.UUID(), SiteID: siteID, DefaultKind: "residential",
			AssignmentMode: "unknown", Source: "manual", Confirmed: true, CreatedAt: now, UpdatedAt: now, Version: 1}
		var f fieldErrs
		applyPrefix(&f, cp, fields, true)
		for _, e := range f {
			e.Field = fmt.Sprintf("items[%d].%s", i, e.Field)
			all = append(all, e)
		}
		cp.RealmKind = domain.RealmKindFor(cp.Prefix)
		out = append(out, cp)
	}
	if err := all.err(); err != nil {
		return nil, err
	}
	err = im.store.CreatePrefixes(ctx, t, out, func(v *domain.ClientPrefix) []outbox.Event {
		return []outbox.Event{event(pr, t, "horus.devices.client_prefix.created", "client_prefix", v.ID, v.Version, now, PrefixEventData(v, nil))}
	})
	if err != nil {
		return nil, mapStoreErr(err, domain.CodeSiteNotFound)
	}
	return out, nil
}

// DecodeItems interpreta {"items": [...]} como campos por elemento.
func DecodeItems(raw json.RawMessage) ([]Fields, error) {
	var items []Fields
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, apperr.Validation(apperr.Field("items", "INVALID_TYPE", "Lista de prefijos."))
	}
	return items, nil
}
