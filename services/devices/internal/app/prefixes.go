package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

var (
	prefixCreateFields = []string{"prefix", "role", "assignment_mode", "default_kind", "ipv6_client_len", "source", "note"}
	prefixPatchFields  = []string{"role", "assignment_mode", "default_kind", "ipv6_client_len", "note"}
)

// PrefixEventData es el payload horus.events.devices.v1.ClientPrefix.
func PrefixEventData(p *domain.ClientPrefix, changed []string) map[string]any {
	if changed == nil {
		changed = []string{}
	}
	return map[string]any{
		"id": p.ID.String(), "version": p.Version, "site_id": p.SiteID.String(), "realm_id": p.RealmID.String(),
		"prefix": p.Prefix.String(), "role": p.Role, "assignment_mode": p.AssignmentMode, "default_kind": p.DefaultKind,
		"ipv6_client_len": p.IPv6ClientLen, "source": p.Source, "confirmed": p.Confirmed,
		"created_at": p.CreatedAt.UTC().Format(outbox.TimeFormat), "deleted_at": tsOrNil(p.DeletedAt), "changed_fields": changed,
	}
}

func applyPrefix(f *fieldErrs, p *domain.ClientPrefix, fields Fields, create bool) {
	if create {
		unknownFields(f, fields, prefixCreateFields...)
		raw, ok := fields["prefix"]
		var s string
		if !ok || json.Unmarshal(raw, &s) != nil {
			f.add("prefix", "REQUIRED", "prefix (CIDR) es obligatorio.")
		} else if pfx, ok := domain.ParseCanonicalPrefix(s); !ok {
			f.add("prefix", "INVALID_CIDR", "CIDR IPv4/IPv6 válido y sin bits de host (p. ej. 10.20.0.0/24).")
		} else {
			p.Prefix = pfx
		}
		if _, ok := fields["role"]; !ok {
			f.add("role", "REQUIRED", "role es obligatorio.")
		}
		if v, ok := f.enum(fields, "source", domain.PrefixSources); ok {
			p.Source = v
		}
	} else {
		unknownFields(f, fields, prefixPatchFields...)
	}
	if v, ok := f.enum(fields, "role", domain.PrefixRoles); ok {
		p.Role = v
	}
	if v, ok := f.enum(fields, "assignment_mode", domain.AssignmentModes); ok {
		p.AssignmentMode = v
	}
	if v, ok := f.enum(fields, "default_kind", domain.CustomerKinds); ok {
		p.DefaultKind = v
	}
	if v, ok := f.str(fields, "note", 300, true); ok {
		p.Note = v
	}
	if raw, ok := fields["ipv6_client_len"]; ok {
		if isNull(raw) {
			p.IPv6ClientLen = nil
		} else {
			var n int
			if json.Unmarshal(raw, &n) != nil || !slices.Contains(domain.IPv6ClientLens, n) {
				f.add("ipv6_client_len", "INVALID_VALUE", "Valores permitidos: 48, 56, 60, 64.")
			} else {
				p.IPv6ClientLen = &n
			}
		}
	}
	if p.Prefix.IsValid() {
		switch {
		case p.Prefix.Addr().Is4() && p.IPv6ClientLen != nil:
			f.add("ipv6_client_len", "INVALID_VALUE", "Solo aplica a prefijos IPv6.")
		case p.Prefix.Addr().Is6() && p.IPv6ClientLen == nil:
			if _, explicit := fields["ipv6_client_len"]; explicit && !create {
				f.add("ipv6_client_len", "REQUIRED", "Obligatorio en prefijos IPv6.")
			} else {
				n := 64
				p.IPv6ClientLen = &n
			}
		}
		if p.IPv6ClientLen != nil && *p.IPv6ClientLen < p.Prefix.Bits() {
			f.add("ipv6_client_len", "INVALID_VALUE", "Debe ser ≥ la longitud del prefijo.")
		}
	}
}

func (s *Service) siteForPrefixes(ctx context.Context, perm string, siteID uuid.UUID) (*domain.Site, error) {
	p, t, err := scope(ctx, perm)
	if err != nil {
		return nil, err
	}
	site, err := s.store.GetSite(ctx, t, siteID)
	if err != nil || !siteAllowed(p, perm, siteID) {
		return nil, apperr.NotFound(domain.CodeSiteNotFound)
	}
	return site, nil
}

// ListPrefixes lista los prefijos de un nodo (vacío = modo descubrimiento).
func (s *Service) ListPrefixes(ctx context.Context, siteID uuid.UUID, qv url.Values) (*Page[domain.ClientPrefix], error) {
	if _, err := s.siteForPrefixes(ctx, "sites.read", siteID); err != nil {
		return nil, err
	}
	_, t, _ := scope(ctx, "sites.read")
	qv = url.Values{"limit": qv["limit"], "cursor": qv["cursor"], "role": qv["role"]}
	lq, req, fk, err := s.listQuery(t, qv, siteID.String(), qv.Get("role"))
	if err != nil {
		return nil, err
	}
	lq.SiteID, lq.Role = siteID, qv.Get("role")
	rows, err := s.store.ListPrefixes(ctx, t, lq)
	if err != nil {
		return nil, err
	}
	page := finish(s, t, lq, req, fk, rows, func(x domain.ClientPrefix) (string, uuid.UUID) {
		return sortKeyOf(lq.SortCol, "", x.CreatedAt), x.ID
	})
	return &page, nil
}

// CreatePrefix declara un prefijo; el realm lo decide el servidor (privado
// → realm del nodo; público → realm público del ISP). Solape → 409.
func (s *Service) CreatePrefix(ctx context.Context, siteID uuid.UUID, fields Fields) (*domain.ClientPrefix, error) {
	if _, err := s.siteForPrefixes(ctx, "sites.update", siteID); err != nil {
		return nil, err
	}
	pr, t, _ := scope(ctx, "sites.update")
	now := s.ts()
	cp := &domain.ClientPrefix{ID: uuid.Must(uuid.NewV7()), TenantID: t.UUID(), SiteID: siteID, DefaultKind: "residential",
		AssignmentMode: "unknown", Source: "manual", Confirmed: true, CreatedAt: now, UpdatedAt: now, Version: 1}
	var f fieldErrs
	applyPrefix(&f, cp, fields, true)
	if err := f.err(); err != nil {
		return nil, err
	}
	cp.RealmKind = domain.RealmKindFor(cp.Prefix)
	err := s.store.CreatePrefix(ctx, t, cp, func(v *domain.ClientPrefix) []outbox.Event {
		return []outbox.Event{event(pr, t, "horus.devices.client_prefix.created", "client_prefix", v.ID, v.Version, now, PrefixEventData(v, nil))}
	})
	if err != nil {
		return nil, mapStoreErr(err, domain.CodeSiteNotFound)
	}
	return cp, nil
}

func (s *Service) prefixInScope(ctx context.Context, perm string, id uuid.UUID) (*domain.ClientPrefix, error) {
	p, t, err := scope(ctx, perm)
	if err != nil {
		return nil, err
	}
	cp, err := s.store.GetPrefix(ctx, t, id)
	if err != nil || !siteAllowed(p, perm, cp.SiteID) {
		return nil, apperr.NotFound(domain.CodeClientPrefixNotFound)
	}
	return cp, nil
}

// UpdatePrefix aplica un JSON Merge Patch con If-Match.
func (s *Service) UpdatePrefix(ctx context.Context, id uuid.UUID, ifMatch int, fields Fields) (*domain.ClientPrefix, error) {
	cp, err := s.prefixInScope(ctx, "sites.update", id)
	if err != nil {
		return nil, err
	}
	pr, t, _ := scope(ctx, "sites.update")
	if ifMatch != cp.Version {
		return nil, apperr.PreconditionFailed(cp)
	}
	var f fieldErrs
	applyPrefix(&f, cp, fields, false)
	if err := f.err(); err != nil {
		return nil, err
	}
	now := s.ts()
	cp.UpdatedAt = now
	changed := changedFields(fields)
	err = s.store.UpdatePrefix(ctx, t, cp, ifMatch, func(v *domain.ClientPrefix) []outbox.Event {
		return []outbox.Event{event(pr, t, "horus.devices.client_prefix.updated", "client_prefix", v.ID, v.Version, now, PrefixEventData(v, changed))}
	})
	if errors.Is(err, ErrVersionChanged) {
		cur, gerr := s.store.GetPrefix(ctx, t, id)
		if gerr != nil {
			return nil, apperr.NotFound(domain.CodeClientPrefixNotFound)
		}
		return nil, apperr.PreconditionFailed(cur)
	}
	if err != nil {
		return nil, mapStoreErr(err, domain.CodeClientPrefixNotFound)
	}
	return cp, nil
}

// DeletePrefix borra un prefijo (sus clientes pasan a inactive, prefix_removed).
func (s *Service) DeletePrefix(ctx context.Context, id uuid.UUID, ifMatch int) error {
	cp, err := s.prefixInScope(ctx, "sites.update", id)
	if err != nil {
		return err
	}
	pr, t, _ := scope(ctx, "sites.update")
	if ifMatch != cp.Version {
		return apperr.PreconditionFailed(cp)
	}
	now := s.ts()
	err = s.store.DeletePrefix(ctx, t, id, ifMatch, func(v *domain.ClientPrefix) []outbox.Event {
		v.DeletedAt = &now
		return []outbox.Event{event(pr, t, "horus.devices.client_prefix.deleted", "client_prefix", v.ID, v.Version, now, PrefixEventData(v, nil))}
	})
	if errors.Is(err, ErrVersionChanged) {
		cur, gerr := s.store.GetPrefix(ctx, t, id)
		if gerr != nil {
			return apperr.NotFound(domain.CodeClientPrefixNotFound)
		}
		return apperr.PreconditionFailed(cur)
	}
	return mapStoreErrNil(err, domain.CodeClientPrefixNotFound)
}
