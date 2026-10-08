package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

var (
	routerCreateFields = []string{"site_id", "name", "display_name", "is_primary", "vendor", "model", "routeros_version", "tags"}
	routerPatchFields  = []string{"name", "display_name", "model", "routeros_version", "admin_state", "tags"}
)

// RouterEventData es el payload horus.events.devices.v1.Router.
func RouterEventData(r *domain.Router, changed []string) map[string]any {
	if changed == nil {
		changed = []string{}
	}
	return map[string]any{
		"id": r.ID.String(), "version": r.Version, "site_id": r.SiteID.String(), "name": r.Hostname, "display_name": r.DisplayName,
		"is_primary": r.IsPrimary, "vendor": r.Vendor, "model": r.Model, "routeros_version": r.RouterOSVersion,
		"routeros_version_supported": r.RouterOSVersionOK, "admin_state": r.AdminState, "onboarding_state": r.OnboardingState,
		"tunnel_address": r.TunnelAddress, "tags": r.Tags, "created_at": r.CreatedAt.UTC().Format(outbox.TimeFormat),
		"updated_at": r.UpdatedAt.UTC().Format(outbox.TimeFormat), "deleted_at": tsOrNil(r.DeletedAt), "changed_fields": changed,
	}
}

func applyRouter(f *fieldErrs, r *domain.Router, fields Fields, create bool) {
	if create {
		unknownFields(f, fields, routerCreateFields...)
	} else {
		unknownFields(f, fields, routerPatchFields...)
	}
	if v, ok := f.str(fields, "name", 120, false); ok && v != nil {
		if *v == "" {
			f.add("name", "REQUIRED", "Nombre obligatorio.")
		}
		r.Hostname = *v
	} else if create && !ok {
		f.add("name", "REQUIRED", "Nombre obligatorio.")
	}
	if v, ok := f.str(fields, "display_name", 120, true); ok {
		r.DisplayName = v
	}
	if v, ok := f.str(fields, "model", 120, true); ok {
		r.Model = v
	}
	if v, ok := f.enum(fields, "vendor", []string{"mikrotik"}); ok {
		r.Vendor = v
	}
	if v, ok := f.enum(fields, "admin_state", domain.AdminStates); ok {
		r.AdminState = v
	}
	if raw, ok := fields["is_primary"]; ok {
		var b bool
		if json.Unmarshal(raw, &b) != nil {
			f.add("is_primary", "INVALID_TYPE", "Debe ser booleano.")
		}
		r.IsPrimary = b
	}
	if v, ok := f.str(fields, "routeros_version", 32, true); ok {
		r.RouterOSVersion = v
		r.RouterOSVersionOK = nil
		if v != nil {
			if !domain.ValidRouterOSVersion(*v) {
				f.add("routeros_version", "ROUTEROS_VERSION_INVALID", "Solo RouterOS 7.x (D15), p. ej. 7.16.1.")
			} else {
				ok := domain.RouterOSSupported(*v)
				r.RouterOSVersionOK = &ok
			}
		}
	}
	if v, ok := f.tags(fields); ok {
		r.Tags = v
	}
}

// ListRouters lista los routers.
func (s *Service) ListRouters(ctx context.Context, qv url.Values) (*Page[domain.Router], error) {
	p, t, err := scope(ctx, "devices.read")
	if err != nil {
		return nil, err
	}
	lq, req, fk, err := s.listQuery(t, qv, qv.Get("site_id"), qv.Get("onboarding_state"), qv.Get("is_primary"))
	if err != nil {
		return nil, err
	}
	if lq.SiteIDs, err = parseUUIDList("site_id", qv.Get("site_id")); err != nil {
		return nil, err
	}
	lq.OnboardingState = qv.Get("onboarding_state")
	if v := qv.Get("is_primary"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "is_primary: booleano")
		}
		lq.IsPrimary = &b
	}
	lq.Sites = sitesFor(p, "devices.read")
	rows, err := s.store.ListRouters(ctx, t, lq)
	if err != nil {
		return nil, err
	}
	page := finish(s, t, lq, req, fk, rows, func(x domain.Router) (string, uuid.UUID) {
		return sortKeyOf(lq.SortCol, x.Hostname, x.CreatedAt), x.ID
	})
	if req.IncludeTotal {
		lq.AfterKey = ""
		n, err := s.store.CountRouters(ctx, t, lq)
		if err != nil {
			return nil, err
		}
		page.Page.Total = &n
	}
	return &page, nil
}

func (s *Service) routerInScope(ctx context.Context, perm string, id uuid.UUID) (*domain.Router, error) {
	p, t, err := scope(ctx, perm)
	if err != nil {
		return nil, err
	}
	r, err := s.store.GetRouter(ctx, t, id)
	if err != nil || !siteAllowed(p, perm, r.SiteID) {
		return nil, apperr.NotFound(domain.CodeRouterNotFound)
	}
	return r, nil
}

// GetRouter devuelve un router.
func (s *Service) GetRouter(ctx context.Context, id uuid.UUID) (*domain.Router, error) {
	return s.routerInScope(ctx, "devices.read", id)
}

// CreateRouter registra un router "Pendiente de configurar" y publica
// horus.devices.router.created (lo consume wireguard).
func (s *Service) CreateRouter(ctx context.Context, fields Fields) (*domain.Router, error) {
	p, t, err := scope(ctx, "devices.create")
	if err != nil {
		return nil, err
	}
	now := s.ts()
	r := &domain.Router{ID: uuid.Must(uuid.NewV7()), TenantID: t.UUID(), Vendor: "mikrotik", IsPrimary: true,
		AdminState: "active", OnboardingState: "pending_configuration", Tags: []string{}, CreatedAt: now, UpdatedAt: now, Version: 1}
	var f fieldErrs
	raw, ok := fields["site_id"]
	if !ok || json.Unmarshal(raw, &r.SiteID) != nil {
		f.add("site_id", "REQUIRED", "site_id (UUID) es obligatorio.")
	}
	applyRouter(&f, r, fields, true)
	if err := f.err(); err != nil {
		return nil, err
	}
	if !siteAllowed(p, "devices.create", r.SiteID) {
		return nil, apperr.Validation(apperr.Field("site_id", problem.CodeNotFound, "El nodo no existe."))
	}
	err = s.store.CreateRouter(ctx, t, r, func(v *domain.Router) []outbox.Event {
		return []outbox.Event{event(p, t, "horus.devices.router.created", "router", v.ID, v.Version, now, RouterEventData(v, nil))}
	})
	if errors.Is(err, ErrRefNotFound) {
		return nil, apperr.Validation(apperr.Field("site_id", problem.CodeNotFound, "El nodo no existe."))
	}
	if err != nil {
		return nil, mapStoreErr(err, domain.CodeRouterNotFound)
	}
	return r, nil
}

// UpdateRouter aplica un JSON Merge Patch con If-Match.
func (s *Service) UpdateRouter(ctx context.Context, id uuid.UUID, ifMatch int, fields Fields) (*domain.Router, error) {
	r, err := s.routerInScope(ctx, "devices.update", id)
	if err != nil {
		return nil, err
	}
	p, t, _ := scope(ctx, "devices.update")
	if ifMatch != r.Version {
		return nil, apperr.PreconditionFailed(r)
	}
	var f fieldErrs
	applyRouter(&f, r, fields, false)
	if err := f.err(); err != nil {
		return nil, err
	}
	now := s.ts()
	r.UpdatedAt = now
	changed := changedFields(fields)
	err = s.store.UpdateRouter(ctx, t, r, ifMatch, func(v *domain.Router) []outbox.Event {
		return []outbox.Event{event(p, t, "horus.devices.router.updated", "router", v.ID, v.Version, now, RouterEventData(v, changed))}
	})
	if errors.Is(err, ErrVersionChanged) {
		cur, gerr := s.store.GetRouter(ctx, t, id)
		if gerr != nil {
			return nil, apperr.NotFound(domain.CodeRouterNotFound)
		}
		return nil, apperr.PreconditionFailed(cur)
	}
	if err != nil {
		return nil, mapStoreErr(err, domain.CodeRouterNotFound)
	}
	return r, nil
}

// DeleteRouter da de baja un router.
func (s *Service) DeleteRouter(ctx context.Context, id uuid.UUID, ifMatch int) error {
	r, err := s.routerInScope(ctx, "devices.delete", id)
	if err != nil {
		return err
	}
	p, t, _ := scope(ctx, "devices.delete")
	if ifMatch != r.Version {
		return apperr.PreconditionFailed(r)
	}
	now := s.ts()
	err = s.store.DeleteRouter(ctx, t, id, ifMatch, func(v *domain.Router) []outbox.Event {
		v.DeletedAt = &now
		return []outbox.Event{event(p, t, "horus.devices.router.deleted", "router", v.ID, v.Version, now, RouterEventData(v, nil))}
	})
	if errors.Is(err, ErrVersionChanged) {
		cur, gerr := s.store.GetRouter(ctx, t, id)
		if gerr != nil {
			return apperr.NotFound(domain.CodeRouterNotFound)
		}
		return apperr.PreconditionFailed(cur)
	}
	return mapStoreErrNil(err, domain.CodeRouterNotFound)
}

func mapStoreErrNil(err error, code string) error {
	if err == nil {
		return nil
	}
	return mapStoreErr(err, code)
}
