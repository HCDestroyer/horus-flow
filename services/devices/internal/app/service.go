package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// Service implementa los casos de uso.
type Service struct {
	store  Store
	cursor *pagination.Codec
	now    func() time.Time
}

// NewService crea el servicio.
func NewService(store Store, cursor *pagination.Codec, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	if cursor == nil {
		cursor = pagination.NewCodec(nil)
	}
	return &Service{store: store, cursor: cursor, now: now}
}

// Fields es un cuerpo JSON por campos (alta o JSON Merge Patch).
type Fields map[string]json.RawMessage

// Page es una página de resultados.
type Page[T any] struct {
	Data []T
	Page pagination.Page
}

// scope devuelve el tenant y el alcance por nodos del permiso perm.
func scope(ctx context.Context, perm string) (*authz.Principal, pgdb.TenantID, error) {
	p := authz.FromContext(ctx)
	tid, err := authz.TenantOf(ctx)
	if err != nil {
		return nil, pgdb.TenantID{}, apperr.Forbidden(problem.CodeTokenScopeInvalid, "")
	}
	if !p.Has(perm) {
		return nil, pgdb.TenantID{}, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	return p, pgdb.TenantID(tid), nil
}

// sitesFor devuelve los nodos a los que se limita perm (nil = todo el tenant).
func sitesFor(p *authz.Principal, perm string) []uuid.UUID {
	if p.TenantWide(perm) {
		return nil
	}
	s := p.SiteScopes(perm)
	if s == nil {
		s = []uuid.UUID{}
	}
	return s
}

func siteAllowed(p *authz.Principal, perm string, site uuid.UUID) bool {
	return p.HasScope(perm, "site:"+site.String())
}

func (s *Service) ts() time.Time { return s.now().UTC() }

func event(p *authz.Principal, t pgdb.TenantID, typ, aggType string, id uuid.UUID, version int, at time.Time, data any) outbox.Event {
	tid := t.UUID()
	return outbox.Event{
		Type: typ, Source: "horus/devices", TenantID: &tid, AggregateType: aggType, AggregateID: id,
		AggregateVersion: version, Actor: outbox.ActorFrom(p), OccurredAt: at, Data: data,
	}
}

// ---------------------------------------------------------------- listados

var sorts = map[string]struct {
	col  string
	desc bool
}{
	"": {"created_at", false}, "created_at": {"created_at", false}, "-created_at": {"created_at", true},
	"name": {"name", false}, "-name": {"name", true},
}

// listQuery interpreta paginación, orden y q comunes.
func (s *Service) listQuery(t pgdb.TenantID, qv url.Values, filterParts ...string) (ListQuery, pagination.Request, string, error) {
	req, err := pagination.ParseRequest(qv)
	if err != nil {
		code := problem.CodeValidationFailed
		if errors.Is(err, pagination.ErrInvalidCursor) {
			code = problem.CodeInvalidCursor
		}
		return ListQuery{}, req, "", apperr.New(apperr.KindBadRequest, code, "limit debe estar entre 1 y 200")
	}
	sortKey := qv.Get("sort")
	so, ok := sorts[sortKey]
	if !ok {
		return ListQuery{}, req, "", apperr.New(apperr.KindBadRequest, problem.CodeInvalidSortField, "")
	}
	q := strings.TrimSpace(qv.Get("q"))
	if len(q) > 128 {
		return ListQuery{}, req, "", apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "q: máximo 128")
	}
	lq := ListQuery{Q: q, SortCol: so.col, Desc: so.desc, Limit: req.Limit + 1}
	fk := pagination.FilterKey(append([]string{q}, filterParts...)...)
	if req.Cursor != "" {
		cur, err := s.cursor.Decode(req.Cursor, so.col+fmt.Sprint(so.desc), fk, t.String())
		if err != nil || len(cur.Keys) != 2 {
			return ListQuery{}, req, "", apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		id, err := uuid.Parse(cur.Keys[1])
		if err != nil {
			return ListQuery{}, req, "", apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		lq.AfterKey, lq.AfterID = cur.Keys[0], id
	}
	return lq, req, fk, nil
}

func finish[T any](s *Service, t pgdb.TenantID, lq ListQuery, req pagination.Request, fk string, rows []T, key func(T) (string, uuid.UUID)) Page[T] {
	page := pagination.Page{Limit: req.Limit}
	if len(rows) > req.Limit {
		rows = rows[:req.Limit]
		k, id := key(rows[len(rows)-1])
		next := s.cursor.Encode(pagination.Cursor{Keys: []string{k, id.String()}, Sort: lq.SortCol + fmt.Sprint(lq.Desc), Filter: fk, Tenant: t.String()})
		page.NextCursor, page.HasMore = &next, true
	}
	if rows == nil {
		rows = []T{}
	}
	return Page[T]{Data: rows, Page: page}
}

func sortKeyOf(col, name string, created time.Time) string {
	if col == "name" {
		return name
	}
	return created.UTC().Format(time.RFC3339Nano)
}

func parseUUIDList(field, v string) ([]uuid.UUID, error) {
	if v == "" {
		return nil, nil
	}
	var out []uuid.UUID
	for _, part := range strings.Split(v, ",") {
		id, err := uuid.Parse(strings.TrimSpace(part))
		if err != nil {
			return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, field+": UUID no válido")
		}
		out = append(out, id)
	}
	return out, nil
}

// ---------------------------------------------------------------- campos

type fieldErrs []problem.FieldError

func (f *fieldErrs) add(field, code, msg string) { *f = append(*f, apperr.Field(field, code, msg)) }

func isNull(raw json.RawMessage) bool { return strings.TrimSpace(string(raw)) == "null" }

func (f *fieldErrs) str(fields Fields, key string, maxLen int, nullable bool) (val *string, present bool) {
	raw, ok := fields[key]
	if !ok {
		return nil, false
	}
	if isNull(raw) {
		if !nullable {
			f.add(key, "REQUIRED", "No puede ser null.")
		}
		return nil, true
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		f.add(key, "INVALID_TYPE", "Debe ser texto.")
		return nil, true
	}
	if len([]rune(s)) > maxLen {
		f.add(key, "TOO_LONG", fmt.Sprintf("Máximo %d caracteres.", maxLen))
		return nil, true
	}
	return &s, true
}

func (f *fieldErrs) enum(fields Fields, key string, allowed []string) (string, bool) {
	raw, ok := fields[key]
	if !ok {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil || !slices.Contains(allowed, s) {
		f.add(key, "INVALID_VALUE", "Valores permitidos: "+strings.Join(allowed, ", ")+".")
		return "", false
	}
	return s, true
}

func (f *fieldErrs) tags(fields Fields) ([]string, bool) {
	raw, ok := fields["tags"]
	if !ok {
		return nil, false
	}
	var t []string
	if json.Unmarshal(raw, &t) != nil || len(t) > 50 {
		f.add("tags", "INVALID_VALUE", "Lista de textos (máx. 50).")
		return nil, false
	}
	if t == nil {
		t = []string{}
	}
	return t, true
}

func (f *fieldErrs) number(fields Fields, key string, lo, hi float64) (val *float64, present bool) {
	raw, ok := fields[key]
	if !ok {
		return nil, false
	}
	if isNull(raw) {
		return nil, true
	}
	var n float64
	if json.Unmarshal(raw, &n) != nil || n < lo || n > hi {
		f.add(key, "OUT_OF_RANGE", fmt.Sprintf("Debe estar entre %v y %v.", lo, hi))
		return nil, true
	}
	return &n, true
}

func unknownFields(f *fieldErrs, fields Fields, allowed ...string) {
	for k := range fields {
		if !slices.Contains(allowed, k) {
			f.add(k, "UNKNOWN_FIELD", "Campo no permitido.")
		}
	}
}

func (f fieldErrs) err() error {
	if len(f) == 0 {
		return nil
	}
	slices.SortFunc(f, func(a, b problem.FieldError) int { return strings.Compare(a.Field, b.Field) })
	return apperr.Validation(f...)
}

func changedFields(fields Fields) []string {
	out := make([]string, 0, len(fields))
	for k := range fields {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// mapConflict traduce errores del repositorio.
func mapStoreErr(err error, notFoundCode string) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return apperr.NotFound(notFoundCode)
	case errors.Is(err, ErrCodeTaken):
		return apperr.Conflict(problem.CodeAlreadyExists, "Ya existe un nodo con ese código.")
	case errors.Is(err, ErrHostnameTaken):
		return apperr.Conflict(problem.CodeAlreadyExists, "Ya existe un router con ese nombre en el ISP.")
	case errors.Is(err, ErrPrimaryExists):
		return apperr.Conflict(domain.CodeRouterPrimaryExists,
			"El nodo ya tiene un router principal (D6: uno por nodo). Registre este como no principal o dé de baja el actual.")
	case errors.Is(err, ErrSiteNotEmpty):
		return apperr.Conflict(domain.CodeSiteNotEmpty, "El nodo tiene routers: délos de baja primero.")
	case errors.Is(err, ErrOverlap):
		return apperr.Conflict(domain.CodeClientPrefixOverlap, "El prefijo se solapa con otro del mismo realm.")
	}
	return err
}
