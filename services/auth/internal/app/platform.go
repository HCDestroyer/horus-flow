package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

var (
	slugRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
	countryRe = regexp.MustCompile(`^[A-Z]{2}$`)
)

// TenantInput es el cuerpo de POST /platform/tenants (TenantCreate).
type TenantInput struct {
	Slug              string         `json:"slug"`
	Name              string         `json:"name"`
	Country           string         `json:"country"`
	Timezone          string         `json:"timezone"`
	Quotas            map[string]any `json:"quotas"`
	Settings          map[string]any `json:"settings"`
	InitialAdminEmail string         `json:"initial_admin_email"`
}

var quotaKeys = map[string]int64{"max_routers": 1, "max_flows_per_second": 100, "max_customers": 1}

func validateQuotas(field string, q map[string]any) []problem.FieldError {
	var errs []problem.FieldError
	for k, v := range q {
		minV, ok := quotaKeys[k]
		n, isNum := v.(float64)
		switch {
		case !ok:
			errs = append(errs, apperr.Field(field+"."+k, "UNKNOWN_FIELD", "Cuota desconocida."))
		case !isNum || n != float64(int64(n)) || int64(n) < minV:
			errs = append(errs, apperr.Field(field+"."+k, "INVALID_VALUE", "Debe ser un entero ≥ "+strconv.FormatInt(minV, 10)+"."))
		}
	}
	return errs
}

func validateSettings(field string, st map[string]any) []problem.FieldError {
	var errs []problem.FieldError
	intRange := func(k string, lo, hi float64) {
		if v, ok := st[k]; ok {
			n, isNum := v.(float64)
			if !isNum || n != float64(int64(n)) || n < lo || n > hi {
				errs = append(errs, apperr.Field(field+"."+k, "INVALID_VALUE", "Fuera de rango."))
			}
		}
	}
	for k := range st {
		switch k {
		case "customer_inactivity_days", "customer_default_kind", "kind_auto_apply_min_confidence", "retention_overrides":
		default:
			errs = append(errs, apperr.Field(field+"."+k, "UNKNOWN_FIELD", "Ajuste desconocido."))
		}
	}
	intRange("customer_inactivity_days", 1, 365)
	intRange("kind_auto_apply_min_confidence", 50, 100)
	if v, ok := st["customer_default_kind"]; ok {
		if s, _ := v.(string); s != "residential" && s != "commercial" && s != "unknown" {
			errs = append(errs, apperr.Field(field+".customer_default_kind", "INVALID_VALUE", "Valor no permitido."))
		}
	}
	if v, ok := st["retention_overrides"]; ok {
		if _, isObj := v.(map[string]any); !isObj {
			errs = append(errs, apperr.Field(field+".retention_overrides", "INVALID_VALUE", "Debe ser un objeto."))
		}
	}
	return errs
}

func validTimezone(tz string) bool {
	if tz == "" || len(tz) > 64 {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

func (s *Service) actor(p *authz.Principal) Actor {
	return Actor{Type: p.Type, ID: p.Subject, SID: p.SessionID.String(), ViaPlatform: p.ViaPlatform}
}

func tenantEventData(t domain.Tenant, changed []string) map[string]any {
	if changed == nil {
		changed = []string{}
	}
	return map[string]any{
		"id": t.ID.String(), "version": t.Version, "slug": t.Slug, "name": t.Name, "status": t.Status,
		"country": t.Country, "timezone": t.Timezone, "quotas": t.Quotas,
		"created_at": t.CreatedAt.UTC().Format(time.RFC3339Nano), "updated_at": t.UpdatedAt.UTC().Format(time.RFC3339Nano),
		"changed_fields": changed,
	}
}

// CreateTenant da de alta un ISP y su primer tenant_admin: si el email ya es
// de un usuario, la membresía queda activa; si no, se crea el usuario
// pendiente con la membresía invitada (la aceptación llega en I1).
func (s *Service) CreateTenant(ctx context.Context, p *authz.Principal, in TenantInput, requestIP string) (*domain.Tenant, error) {
	var errs []problem.FieldError
	in.Slug = strings.TrimSpace(in.Slug)
	if !slugRe.MatchString(in.Slug) {
		errs = append(errs, apperr.Field("slug", "INVALID_FORMAT", "Minúsculas, dígitos y guiones (2–63)."))
	}
	if n := len([]rune(strings.TrimSpace(in.Name))); n == 0 || n > 120 {
		errs = append(errs, apperr.Field("name", "REQUIRED", "Nombre obligatorio (máx. 120)."))
	}
	if !countryRe.MatchString(in.Country) {
		errs = append(errs, apperr.Field("country", "INVALID_FORMAT", "Código ISO-3166 de 2 letras."))
	}
	if !validTimezone(in.Timezone) {
		errs = append(errs, apperr.Field("timezone", "INVALID_TIMEZONE", "Zona horaria IANA no válida."))
	}
	addr, err := mail.ParseAddress(in.InitialAdminEmail)
	if err != nil || addr.Address != strings.TrimSpace(in.InitialAdminEmail) {
		errs = append(errs, apperr.Field("initial_admin_email", "INVALID_EMAIL", "Email no válido."))
	}
	errs = append(errs, validateQuotas("quotas", in.Quotas)...)
	errs = append(errs, validateSettings("settings", in.Settings)...)
	if len(errs) > 0 {
		return nil, apperr.Validation(errs...)
	}
	now := s.now()
	creator := p.UserID
	t := domain.Tenant{
		ID: uuid.Must(uuid.NewV7()), Slug: in.Slug, Name: strings.TrimSpace(in.Name), Status: domain.TenantActive,
		Country: in.Country, Timezone: in.Timezone, Quotas: orEmpty(in.Quotas), Settings: orEmpty(in.Settings),
		SupportAccessPolicy: "notify", CreatedBy: &creator, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	admin, _ := s.o.Catalog.TenantRole(domain.RoleTenantAdmin)
	actor := s.actor(p)
	nt := NewTenant{
		Tenant: t, AdminEmail: addr.Address, AdminRole: admin.ID, Actor: actor,
		Events: func(t domain.Tenant, membershipID, userID uuid.UUID, status string) []OutboxEvent {
			tid := t.ID
			return []OutboxEvent{
				{ID: uuid.Must(uuid.NewV7()), Type: "horus.auth.tenant.created", TenantID: &tid, AggregateType: "tenant",
					AggregateID: t.ID, AggregateVersion: 1, Actor: actor, OccurredAt: now, Data: tenantEventData(t, nil)},
				{ID: uuid.Must(uuid.NewV7()), Type: "horus.auth.membership.granted", TenantID: &tid, AggregateType: "membership",
					AggregateID: membershipID, AggregateVersion: 1, Actor: actor, OccurredAt: now, Data: map[string]any{
						"id": membershipID.String(), "user_id": userID.String(), "version": 1, "status": status,
						"assignments":          []map[string]any{{"role_id": admin.ID.String(), "scope": "tenant"}},
						"previous_assignments": []map[string]any{},
					}},
			}
		},
	}
	if err := s.store.CreateTenant(ctx, nt); err != nil {
		if errors.Is(err, ErrSlugTaken) {
			return nil, apperr.Conflict(problem.CodeAlreadyExists, "Ya existe un ISP con ese slug.")
		}
		return nil, err
	}
	for _, tenant := range []uuid.UUID{uuid.Nil, t.ID} {
		_ = s.Record(ctx, api.AuditEntry{
			TenantID: tenant, ActorType: p.Type, ActorID: p.Subject, ViaPlatform: tenant != uuid.Nil,
			Action: "platform.tenant.created", ResourceType: "tenant", ResourceID: t.ID.String(), Outcome: "success",
			IP: requestIP, Changes: map[string]any{"slug": t.Slug},
		})
	}
	t.Members = 1
	return &t, nil
}

// ErrSlugTaken lo devuelve el repositorio si el slug ya existe.
var ErrSlugTaken = errors.New("auth: tenant slug taken")

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// GetTenant devuelve un ISP.
func (s *Service) GetTenant(ctx context.Context, id uuid.UUID) (*domain.Tenant, error) {
	t, err := s.store.Tenant(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, apperr.NotFound(problem.CodeTenantNotFound)
	}
	return t, err
}

// TenantPage es una página de ISP.
type TenantPage struct {
	Data []domain.Tenant
	Page pagination.Page
}

// ListTenants lista los ISP (keyset por created_at, id).
func (s *Service) ListTenants(ctx context.Context, codec *pagination.Codec, req pagination.Request, q, status string) (*TenantPage, error) {
	f := TenantFilter{Q: strings.TrimSpace(q), Status: status, Limit: req.Limit + 1}
	fk := pagination.FilterKey(f.Q, f.Status)
	if req.Cursor != "" {
		cur, err := codec.Decode(req.Cursor, "created_at", fk, "")
		if err != nil || len(cur.Keys) != 2 {
			return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		ts, err1 := time.Parse(time.RFC3339Nano, cur.Keys[0])
		id, err2 := uuid.Parse(cur.Keys[1])
		if err1 != nil || err2 != nil {
			return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		f.AfterCreated, f.AfterID = &ts, id
	}
	rows, err := s.store.ListTenants(ctx, f)
	if err != nil {
		return nil, err
	}
	page := pagination.Page{Limit: req.Limit}
	if len(rows) > req.Limit {
		rows = rows[:req.Limit]
		last := rows[len(rows)-1]
		next := codec.Encode(pagination.Cursor{Keys: []string{last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID.String()}, Sort: "created_at", Filter: fk})
		page.NextCursor, page.HasMore = &next, true
	}
	if req.IncludeTotal {
		f.AfterCreated = nil
		n, err := s.store.CountTenants(ctx, f)
		if err != nil {
			return nil, err
		}
		page.Total = &n
	}
	if rows == nil {
		rows = []domain.Tenant{}
	}
	return &TenantPage{Data: rows, Page: page}, nil
}

// UpdateTenant aplica un JSON Merge Patch (TenantPatch) con If-Match.
func (s *Service) UpdateTenant(ctx context.Context, p *authz.Principal, id uuid.UUID, ifMatch int, patch map[string]json.RawMessage, requestIP string) (*domain.Tenant, error) {
	t, err := s.GetTenant(ctx, id)
	if err != nil {
		return nil, err
	}
	if ifMatch != t.Version {
		return nil, apperr.PreconditionFailed(t)
	}
	var errs []problem.FieldError
	var changed []string
	for k, raw := range patch {
		switch k {
		case "name":
			var v string
			if json.Unmarshal(raw, &v) != nil || strings.TrimSpace(v) == "" || len([]rune(v)) > 120 {
				errs = append(errs, apperr.Field("name", "INVALID_VALUE", "Nombre obligatorio (máx. 120)."))
				continue
			}
			t.Name = strings.TrimSpace(v)
		case "country":
			var v string
			if json.Unmarshal(raw, &v) != nil || !countryRe.MatchString(v) {
				errs = append(errs, apperr.Field("country", "INVALID_FORMAT", "Código ISO-3166 de 2 letras."))
				continue
			}
			t.Country = v
		case "timezone":
			var v string
			if json.Unmarshal(raw, &v) != nil || !validTimezone(v) {
				errs = append(errs, apperr.Field("timezone", "INVALID_TIMEZONE", "Zona horaria IANA no válida."))
				continue
			}
			t.Timezone = v
		case "support_access_policy":
			var v string
			if json.Unmarshal(raw, &v) != nil || (v != "notify" && v != "require_approval" && v != "deny") {
				errs = append(errs, apperr.Field(k, "INVALID_VALUE", "Valor no permitido."))
				continue
			}
			t.SupportAccessPolicy = v
		case "quotas", "settings":
			var m map[string]any
			if json.Unmarshal(raw, &m) != nil {
				errs = append(errs, apperr.Field(k, "INVALID_VALUE", "Debe ser un objeto."))
				continue
			}
			target := t.Quotas
			if k == "settings" {
				target = t.Settings
			}
			merged := mergePatch(target, m)
			if k == "quotas" {
				errs = append(errs, validateQuotas(k, merged)...)
				t.Quotas = merged
			} else {
				errs = append(errs, validateSettings(k, merged)...)
				t.Settings = merged
			}
		default:
			errs = append(errs, apperr.Field(k, "UNKNOWN_FIELD", "Campo no editable."))
			continue
		}
		changed = append(changed, k)
	}
	if len(errs) > 0 {
		return nil, apperr.Validation(errs...)
	}
	if len(changed) == 0 {
		return t, nil
	}
	now := s.now()
	t.UpdatedAt = now
	actor := s.actor(p)
	ok, err := s.store.UpdateTenant(ctx, t, ifMatch, func(nt domain.Tenant) OutboxEvent {
		tid := nt.ID
		return OutboxEvent{ID: uuid.Must(uuid.NewV7()), Type: "horus.auth.tenant.updated", TenantID: &tid, AggregateType: "tenant",
			AggregateID: nt.ID, AggregateVersion: nt.Version, Actor: actor, OccurredAt: now, Data: tenantEventData(nt, changed)}
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		cur, err := s.GetTenant(ctx, id)
		if err != nil {
			return nil, err
		}
		return nil, apperr.PreconditionFailed(cur)
	}
	_ = s.Record(ctx, api.AuditEntry{ActorType: p.Type, ActorID: p.Subject, Action: "platform.tenant.updated",
		ResourceType: "tenant", ResourceID: id.String(), IP: requestIP, Changes: map[string]any{"fields": changed}})
	return s.GetTenant(ctx, id)
}

// SetTenantStatus es POST /platform/tenants/{id}/suspend (suspend = true,
// con motivo) y /resume. Idempotente: suspender un ISP ya suspendido (o
// reactivar uno activo) devuelve su estado sin emitir nada. Un ISP
// suspendido no obtiene tokens nuevos (TENANT_SUSPENDED).
func (s *Service) SetTenantStatus(ctx context.Context, p *authz.Principal, id uuid.UUID, suspend bool, reason string, pauseIngest bool,
	requestIP string,
) (*domain.Tenant, error) {
	want, typ, action := domain.TenantActive, "horus.auth.tenant.resumed", "platform.tenant.resumed"
	if suspend {
		want, typ, action = domain.TenantSuspended, "horus.auth.tenant.suspended", "platform.tenant.suspended"
		reason = strings.TrimSpace(reason)
		if reason == "" || len([]rune(reason)) > 300 {
			return nil, apperr.Validation(apperr.Field("reason", "INVALID_VALUE", "Motivo obligatorio (máx. 300)."))
		}
	} else {
		reason, pauseIngest = "", false
	}
	t, err := s.GetTenant(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.Status == want {
		return t, nil
	}
	if t.Status != domain.TenantActive && t.Status != domain.TenantSuspended {
		return nil, apperr.Conflict(problem.CodeConflict, "El ISP está en baja ("+t.Status+").")
	}
	prev := t.Status
	now := s.now()
	actor := s.actor(p)
	ok, err := s.store.SetTenantStatus(ctx, id, prev, want, now, func(nt domain.Tenant) OutboxEvent {
		tid := nt.ID
		var why any
		if reason != "" {
			why = reason
		}
		return OutboxEvent{ID: uuid.Must(uuid.NewV7()), Type: typ, TenantID: &tid, AggregateType: "tenant", AggregateID: nt.ID,
			AggregateVersion: nt.Version, Actor: actor, OccurredAt: now, Data: map[string]any{"id": nt.ID.String(), "version": nt.Version,
				"previous_status": prev, "status": want, "reason": why, "pause_ingest": pauseIngest,
				"changed_at": now.UTC().Format("2006-01-02T15:04:05.000Z")}}
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apperr.Conflict(problem.CodeConflict, "El estado del ISP cambió a la vez; vuelva a intentarlo.")
	}
	changes := map[string]any{"previous_status": prev, "status": want}
	if reason != "" {
		changes["reason"] = reason
	}
	_ = s.Record(ctx, api.AuditEntry{ActorType: p.Type, ActorID: p.Subject, Action: action, ResourceType: "tenant",
		ResourceID: id.String(), IP: requestIP, Changes: changes})
	return s.GetTenant(ctx, id)
}

// mergePatch aplica RFC 7396 a un objeto (null borra la clave).
func mergePatch(target, patch map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range target {
		out[k] = v
	}
	for k, v := range patch {
		if v == nil {
			delete(out, k)
			continue
		}
		if pm, ok := v.(map[string]any); ok {
			tm, _ := out[k].(map[string]any)
			out[k] = mergePatch(tm, pm)
			continue
		}
		out[k] = v
	}
	return out
}
