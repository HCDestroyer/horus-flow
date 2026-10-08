package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

var siteFields = []string{"name", "code", "kind", "parent_id", "address", "latitude", "longitude", "timezone", "tags"}

func tsOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(outbox.TimeFormat)
}

// SiteEventData es el payload horus.events.devices.v1.Site.
func SiteEventData(s *domain.Site, changed []string) map[string]any {
	if changed == nil {
		changed = []string{}
	}
	return map[string]any{
		"id": s.ID.String(), "version": s.Version, "name": s.Name, "code": s.Code, "kind": s.Kind, "parent_id": s.ParentID,
		"address": s.Address, "latitude": s.Latitude, "longitude": s.Longitude, "timezone": s.Timezone, "tags": s.Tags,
		"private_realm_id": s.PrivateRealmID.String(), "created_at": s.CreatedAt.UTC().Format(outbox.TimeFormat),
		"updated_at": s.UpdatedAt.UTC().Format(outbox.TimeFormat), "deleted_at": tsOrNil(s.DeletedAt), "changed_fields": changed,
	}
}

func applySite(f *fieldErrs, s *domain.Site, fields Fields, create bool) {
	unknownFields(f, fields, siteFields...)
	if v, ok := f.str(fields, "name", 120, false); ok && v != nil {
		if *v == "" {
			f.add("name", "REQUIRED", "Nombre obligatorio.")
		}
		s.Name = *v
	} else if create && !ok {
		f.add("name", "REQUIRED", "Nombre obligatorio.")
	}
	if v, ok := f.str(fields, "code", 32, true); ok {
		s.Code = v
	}
	if v, ok := f.enum(fields, "kind", domain.SiteKinds); ok {
		s.Kind = v
	}
	if raw, ok := fields["parent_id"]; ok {
		if isNull(raw) {
			s.ParentID = nil
		} else {
			var id uuid.UUID
			if json.Unmarshal(raw, &id) != nil {
				f.add("parent_id", "INVALID_UUID", "UUID no válido.")
			} else if id == s.ID {
				f.add("parent_id", "INVALID_VALUE", "Un nodo no puede ser su propio padre.")
			} else {
				s.ParentID = &id
			}
		}
	}
	if v, ok := f.str(fields, "address", 300, true); ok {
		s.Address = v
	}
	if v, ok := f.number(fields, "latitude", -90, 90); ok {
		s.Latitude = v
	}
	if v, ok := f.number(fields, "longitude", -180, 180); ok {
		s.Longitude = v
	}
	if v, ok := f.str(fields, "timezone", 64, true); ok {
		if v != nil {
			if _, err := time.LoadLocation(*v); err != nil || *v == "" {
				f.add("timezone", "INVALID_TIMEZONE", "Zona horaria IANA no válida.")
			}
		}
		s.Timezone = v
	}
	if v, ok := f.tags(fields); ok {
		s.Tags = v
	}
}

// ListSites lista los nodos.
func (s *Service) ListSites(ctx context.Context, qv url.Values) (*Page[domain.Site], error) {
	p, t, err := scope(ctx, "sites.read")
	if err != nil {
		return nil, err
	}
	lq, req, fk, err := s.listQuery(t, qv, qv.Get("kind"), qv.Get("parent_id"))
	if err != nil {
		return nil, err
	}
	lq.Kind = qv.Get("kind")
	if v := qv.Get("parent_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "parent_id: UUID no válido")
		}
		lq.ParentID = &id
	}
	lq.Sites = sitesFor(p, "sites.read")
	rows, err := s.store.ListSites(ctx, t, lq)
	if err != nil {
		return nil, err
	}
	page := finish(s, t, lq, req, fk, rows, func(x domain.Site) (string, uuid.UUID) {
		return sortKeyOf(lq.SortCol, x.Name, x.CreatedAt), x.ID
	})
	if req.IncludeTotal {
		lq.AfterKey = ""
		n, err := s.store.CountSites(ctx, t, lq)
		if err != nil {
			return nil, err
		}
		page.Page.Total = &n
	}
	return &page, nil
}

// GetSite devuelve un nodo del tenant (404 si no existe, es de otro ISP o
// está fuera del alcance).
func (s *Service) GetSite(ctx context.Context, id uuid.UUID) (*domain.Site, error) {
	p, t, err := scope(ctx, "sites.read")
	if err != nil {
		return nil, err
	}
	site, err := s.store.GetSite(ctx, t, id)
	if err != nil {
		return nil, mapStoreErr(err, domain.CodeSiteNotFound)
	}
	if !siteAllowed(p, "sites.read", site.ID) {
		return nil, apperr.NotFound(domain.CodeSiteNotFound)
	}
	return site, nil
}

// CreateSite da de alta un nodo con su realm privado.
func (s *Service) CreateSite(ctx context.Context, fields Fields) (*domain.Site, error) {
	p, t, err := scope(ctx, "sites.create")
	if err != nil {
		return nil, err
	}
	if !p.TenantWide("sites.create") {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "Crear nodos requiere el permiso en todo el ISP.")
	}
	now := s.ts()
	site := &domain.Site{ID: uuid.Must(uuid.NewV7()), TenantID: t.UUID(), Kind: "node", Tags: []string{},
		CreatedAt: now, UpdatedAt: now, Version: 1, PrivateRealmID: uuid.Must(uuid.NewV7())}
	var f fieldErrs
	applySite(&f, site, fields, true)
	if err := f.err(); err != nil {
		return nil, err
	}
	err = s.store.CreateSite(ctx, t, site, func(v *domain.Site) []outbox.Event {
		return []outbox.Event{event(p, t, "horus.devices.site.created", "site", v.ID, v.Version, now, SiteEventData(v, nil))}
	})
	if errors.Is(err, ErrRefNotFound) {
		return nil, apperr.Validation(apperr.Field("parent_id", problem.CodeNotFound, "El nodo padre no existe."))
	}
	if err != nil {
		return nil, mapStoreErr(err, domain.CodeSiteNotFound)
	}
	return site, nil
}

// UpdateSite aplica un JSON Merge Patch con If-Match.
func (s *Service) UpdateSite(ctx context.Context, id uuid.UUID, ifMatch int, fields Fields) (*domain.Site, error) {
	p, t, err := scope(ctx, "sites.update")
	if err != nil {
		return nil, err
	}
	site, err := s.store.GetSite(ctx, t, id)
	if err != nil || !siteAllowed(p, "sites.update", id) {
		return nil, apperr.NotFound(domain.CodeSiteNotFound)
	}
	if ifMatch != site.Version {
		return nil, apperr.PreconditionFailed(site)
	}
	var f fieldErrs
	applySite(&f, site, fields, false)
	if err := f.err(); err != nil {
		return nil, err
	}
	now := s.ts()
	site.UpdatedAt = now
	changed := changedFields(fields)
	err = s.store.UpdateSite(ctx, t, site, ifMatch, func(v *domain.Site) []outbox.Event {
		return []outbox.Event{event(p, t, "horus.devices.site.updated", "site", v.ID, v.Version, now, SiteEventData(v, changed))}
	})
	return s.afterWrite(ctx, err, func() (any, error) { return s.store.GetSite(ctx, t, id) }, domain.CodeSiteNotFound, site)
}

// afterWrite traduce el resultado de una escritura versionada.
func (s *Service) afterWrite(_ context.Context, err error, current func() (any, error), notFound string, ok *domain.Site) (*domain.Site, error) {
	if errors.Is(err, ErrVersionChanged) {
		cur, gerr := current()
		if gerr != nil {
			return nil, apperr.NotFound(notFound)
		}
		return nil, apperr.PreconditionFailed(cur)
	}
	if errors.Is(err, ErrRefNotFound) {
		return nil, apperr.Validation(apperr.Field("parent_id", problem.CodeNotFound, "El nodo padre no existe."))
	}
	if err != nil {
		return nil, mapStoreErr(err, notFound)
	}
	return ok, nil
}

// DeleteSite da de baja un nodo vacío (sin routers) con su realm y prefijos.
func (s *Service) DeleteSite(ctx context.Context, id uuid.UUID, ifMatch int) error {
	p, t, err := scope(ctx, "sites.delete")
	if err != nil {
		return err
	}
	site, err := s.store.GetSite(ctx, t, id)
	if err != nil || !siteAllowed(p, "sites.delete", id) {
		return apperr.NotFound(domain.CodeSiteNotFound)
	}
	if ifMatch != site.Version {
		return apperr.PreconditionFailed(site)
	}
	now := s.ts()
	err = s.store.DeleteSite(ctx, t, id, ifMatch, func(v *domain.Site, prefixes []domain.ClientPrefix) []outbox.Event {
		v.DeletedAt = &now
		evs := []outbox.Event{event(p, t, "horus.devices.site.deleted", "site", v.ID, v.Version, now, SiteEventData(v, nil))}
		for i := range prefixes {
			cp := &prefixes[i]
			cp.DeletedAt = &now
			evs = append(evs, event(p, t, "horus.devices.client_prefix.deleted", "client_prefix", cp.ID, cp.Version, now, PrefixEventData(cp, nil)))
		}
		return evs
	})
	_, err = s.afterWrite(ctx, err, func() (any, error) { return s.store.GetSite(ctx, t, id) }, domain.CodeSiteNotFound, site)
	return err
}
