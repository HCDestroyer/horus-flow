package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/catalog"
)

// Permisos de las reglas ASN propias (permissions.yaml).
const (
	PermSettingsRead   = "settings.read"
	PermSettingsManage = "settings.manage"
)

// ErrASNTaken: ya hay una regla viva para ese ASN en el tenant.
var ErrASNTaken = errors.New("devices: asn rule exists")

var (
	asnRuleCreateFields = []string{"asn", "name", "category", "service", "note"}
	asnRulePatchFields  = []string{"name", "category", "service", "note"}
)

// AsnRuleStore es el repositorio de las reglas ASN (siempre dentro del tenant).
type AsnRuleStore interface {
	ListAsnRules(ctx context.Context, t pgdb.TenantID) ([]domain.AsnRule, error)
	GetAsnRule(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.AsnRule, error)
	CreateAsnRule(ctx context.Context, t pgdb.TenantID, r *domain.AsnRule, ev Events[domain.AsnRule]) error
	UpdateAsnRule(ctx context.Context, t pgdb.TenantID, r *domain.AsnRule, expect int, ev Events[domain.AsnRule]) error
	DeleteAsnRule(ctx context.Context, t pgdb.TenantID, id uuid.UUID, expect int, ev Events[domain.AsnRule]) error
}

// AsnRules gestiona los ASN propios del ISP (Administración › ASN propios):
// nombre, categoría y servicio por ASN que flows aplica al tráfico nuevo. Cada
// cambio publica horus.devices.asn_rule.* por el outbox; flows y analytics
// los proyectan (packages/go/flowinv).
type AsnRules struct {
	store   AsnRuleStore
	catalog func() *catalog.Catalog
	now     func() time.Time
}

// NewAsnRules crea el servicio; cat nil = catálogo semilla de plataforma.
func NewAsnRules(store AsnRuleStore, cat func() *catalog.Catalog, now func() time.Time) *AsnRules {
	if now == nil {
		now = time.Now
	}
	if cat == nil {
		seed, _ := catalog.Seed()
		cat = func() *catalog.Catalog { return seed }
	}
	return &AsnRules{store: store, catalog: cat, now: now}
}

// AsnRuleEventData es el payload horus.events.devices.v1.AsnRule.
func AsnRuleEventData(r *domain.AsnRule, changed []string) map[string]any {
	if changed == nil {
		changed = []string{}
	}
	return map[string]any{
		"id": r.ID.String(), "version": r.Version, "asn": int64(r.ASN), "name": r.Name, "category": r.Category,
		"service": r.Service, "created_at": r.CreatedAt.UTC().Format(outbox.TimeFormat), "deleted_at": tsOrNil(r.DeletedAt),
		"changed_fields": changed,
	}
}

// CatalogOption es una categoría o servicio del catálogo admitido en una regla.
type CatalogOption struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Category string `json:"category,omitempty"`
}

// Catalog devuelve las categorías y servicios del catálogo vigente (GET /asn-rules/catalog).
func (a *AsnRules) Catalog(ctx context.Context) (cats, svcs []CatalogOption, err error) {
	if _, _, err := scope(ctx, PermSettingsRead); err != nil {
		return nil, nil, err
	}
	c := a.catalog()
	cats, svcs = make([]CatalogOption, 0, len(c.Def.Categories)), make([]CatalogOption, 0, len(c.Def.Services))
	for _, k := range c.Def.Categories {
		cats = append(cats, CatalogOption{Slug: k.Slug, Name: k.Name})
	}
	for _, s := range c.Def.Services {
		svcs = append(svcs, CatalogOption{Slug: s.Slug, Name: s.Name, Category: s.Category})
	}
	slices.SortFunc(svcs, func(x, y CatalogOption) int { return strings.Compare(x.Name, y.Name) })
	return cats, svcs, nil
}

// List devuelve las reglas del tenant ordenadas por ASN.
func (a *AsnRules) List(ctx context.Context) ([]domain.AsnRule, error) {
	_, t, err := scope(ctx, PermSettingsRead)
	if err != nil {
		return nil, err
	}
	rows, err := a.store.ListAsnRules(ctx, t)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(x, y domain.AsnRule) int { return int(int64(x.ASN) - int64(y.ASN)) })
	if rows == nil {
		rows = []domain.AsnRule{}
	}
	return rows, nil
}

// apply valida y aplica los campos de un alta o de un merge patch.
func (a *AsnRules) apply(f *fieldErrs, r *domain.AsnRule, fields Fields, create bool) {
	if create {
		unknownFields(f, fields, asnRuleCreateFields...)
		raw, ok := fields["asn"]
		var n float64
		switch {
		case !ok || isNull(raw):
			f.add("asn", "REQUIRED", "Indica el número de ASN.")
		case json.Unmarshal(raw, &n) != nil || n != float64(int64(n)) || n < 1 || n > domain.MaxASN:
			f.add("asn", "OUT_OF_RANGE", "El ASN es un número entre 1 y 4294967295.")
		default:
			r.ASN = uint32(n)
		}
		if _, ok := fields["name"]; !ok {
			f.add("name", "REQUIRED", "Campo obligatorio.")
		}
		_, hasCat := fields["category"]
		if raw, hasSvc := fields["service"]; !hasCat && (!hasSvc || isNull(raw)) {
			f.add("category", "REQUIRED", "Elige una categoría (o un servicio del catálogo).")
		}
	} else {
		unknownFields(f, fields, asnRulePatchFields...)
	}
	if v, ok := f.str(fields, "name", domain.MaxAsnRuleName, false); ok && v != nil {
		if name := strings.TrimSpace(*v); name == "" {
			f.add("name", "REQUIRED", "El nombre no puede quedar vacío.")
		} else {
			r.Name = name
		}
	}
	c := a.catalog()
	if v, ok := f.str(fields, "category", domain.MaxAsnRuleSlug, false); ok && v != nil {
		if !slices.ContainsFunc(c.Def.Categories, func(k catalog.Category) bool { return k.Slug == *v }) {
			f.add("category", "INVALID_VALUE", "Categoría desconocida en el catálogo.")
		} else {
			r.Category = *v
		}
	}
	if v, ok := f.str(fields, "service", domain.MaxAsnRuleSlug, true); ok {
		switch {
		case v == nil || strings.TrimSpace(*v) == "":
			r.Service = nil
		case !slices.ContainsFunc(c.Def.Services, func(s catalog.Service) bool { return s.Slug == *v }):
			f.add("service", "INVALID_VALUE", "Servicio desconocido en el catálogo.")
		default:
			s := *v
			r.Service = &s
		}
	}
	// Los agregados de ClickHouse solo guardan service_id: con un servicio del
	// catálogo, la categoría de la regla es la de ese servicio (si no, los tops
	// por categoría contarían el tráfico en otra).
	if r.Service != nil {
		if i := slices.IndexFunc(c.Def.Services, func(s catalog.Service) bool { return s.Slug == *r.Service }); i >= 0 {
			want := c.Def.Services[i].Category
			if _, explicit := fields["category"]; explicit && r.Category != want && !f.has("category") {
				f.add("category", "INVALID_VALUE", "Con un servicio del catálogo la categoría es la del servicio ("+want+").")
			}
			r.Category = want
		}
	}
	if v, ok := f.str(fields, "note", domain.MaxAsnRuleNote, true); ok {
		if v != nil && strings.TrimSpace(*v) == "" {
			v = nil
		}
		r.Note = v
	}
}

// Create da de alta una regla. Un ASN que ya tiene regla → 422 con error en `asn`.
func (a *AsnRules) Create(ctx context.Context, fields Fields) (*domain.AsnRule, error) {
	p, t, err := scope(ctx, PermSettingsManage)
	if err != nil {
		return nil, err
	}
	now := a.now().UTC()
	r := &domain.AsnRule{ID: uuid.Must(uuid.NewV7()), TenantID: t.UUID(), CreatedAt: now, UpdatedAt: now, Version: 1}
	if p != nil && p.UserID != uuid.Nil {
		uid := p.UserID
		r.CreatedBy = &uid
	}
	var f fieldErrs
	a.apply(&f, r, fields, true)
	if err := f.err(); err != nil {
		return nil, err
	}
	existing, err := a.store.ListAsnRules(ctx, t)
	if err != nil {
		return nil, err
	}
	if slices.ContainsFunc(existing, func(x domain.AsnRule) bool { return x.ASN == r.ASN }) {
		return nil, asnTaken()
	}
	if len(existing) >= domain.MaxAsnRulesPerTenant {
		return nil, apperr.New(apperr.KindInvalid, problem.CodeValidationFailed, "Máximo 1000 reglas ASN por ISP.")
	}
	err = a.store.CreateAsnRule(ctx, t, r, func(v *domain.AsnRule) []outbox.Event {
		return []outbox.Event{event(p, t, "horus.devices.asn_rule.created", "asn_rule", v.ID, v.Version, now, AsnRuleEventData(v, nil))}
	})
	if errors.Is(err, ErrASNTaken) {
		return nil, asnTaken()
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

func asnTaken() error {
	return apperr.Validation(apperr.Field("asn", "ALREADY_EXISTS", "Ya hay una regla para este ASN: edítala en la lista."))
}

func (a *AsnRules) get(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.AsnRule, error) {
	r, err := a.store.GetAsnRule(ctx, t, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, apperr.NotFound(domain.CodeAsnRuleNotFound)
		}
		return nil, err
	}
	return r, nil
}

// Update aplica un JSON Merge Patch con If-Match (el ASN no cambia).
func (a *AsnRules) Update(ctx context.Context, id uuid.UUID, ifMatch int, fields Fields) (*domain.AsnRule, error) {
	p, t, err := scope(ctx, PermSettingsManage)
	if err != nil {
		return nil, err
	}
	r, err := a.get(ctx, t, id)
	if err != nil {
		return nil, err
	}
	if ifMatch != r.Version {
		return nil, apperr.PreconditionFailed(r)
	}
	var f fieldErrs
	a.apply(&f, r, fields, false)
	if err := f.err(); err != nil {
		return nil, err
	}
	now := a.now().UTC()
	r.UpdatedAt = now
	changed := changedFields(fields)
	err = a.store.UpdateAsnRule(ctx, t, r, ifMatch, func(v *domain.AsnRule) []outbox.Event {
		return []outbox.Event{event(p, t, "horus.devices.asn_rule.updated", "asn_rule", v.ID, v.Version, now, AsnRuleEventData(v, changed))}
	})
	if errors.Is(err, ErrVersionChanged) {
		cur, gerr := a.get(ctx, t, id)
		if gerr != nil {
			return nil, gerr
		}
		return nil, apperr.PreconditionFailed(cur)
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// Delete da de baja una regla con If-Match.
func (a *AsnRules) Delete(ctx context.Context, id uuid.UUID, ifMatch int) error {
	p, t, err := scope(ctx, PermSettingsManage)
	if err != nil {
		return err
	}
	r, err := a.get(ctx, t, id)
	if err != nil {
		return err
	}
	if ifMatch != r.Version {
		return apperr.PreconditionFailed(r)
	}
	now := a.now().UTC()
	err = a.store.DeleteAsnRule(ctx, t, id, ifMatch, func(v *domain.AsnRule) []outbox.Event {
		v.DeletedAt = &now
		return []outbox.Event{event(p, t, "horus.devices.asn_rule.deleted", "asn_rule", v.ID, v.Version, now, AsnRuleEventData(v, nil))}
	})
	if errors.Is(err, ErrVersionChanged) {
		cur, gerr := a.get(ctx, t, id)
		if gerr != nil {
			return gerr
		}
		return apperr.PreconditionFailed(cur)
	}
	return err
}
