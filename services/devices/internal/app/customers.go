package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// Permisos de clientes (C7).
const (
	PermCustomersRead      = "customers.read"
	PermCustomersUpdate    = "customers.update"
	PermCustomersKindWrite = "customers.kind.write"
)

// CustomerQuery es un listado keyset de clientes.
type CustomerQuery struct {
	SortCol  string // last_seen | first_seen | kind_changed_at
	Desc     bool
	AfterKey string
	AfterID  uuid.UUID
	Limit    int
	// Sites: alcance del permiso (nil = todo el tenant).
	Sites []uuid.UUID

	Q                   string
	SiteIDs             []uuid.UUID
	RealmIDs            []uuid.UUID
	PrefixIDs           []uuid.UUID
	Statuses            []string
	Kinds               []string
	KindSources         []string
	SecurityStates      []string
	KindLocked          *bool
	CommercialSuspected *bool
	HasOpenFindings     *bool
	LastSeenGTE         *time.Time
	LastSeenLTE         *time.Time
	// Contains: clientes cuya clave contiene esta dirección (lookup por IP).
	Contains *netip.Prefix
	// Within: clientes dentro de este prefijo (lookup por prefijo).
	Within *netip.Prefix
}

// CustomerStats son los conteos de GET /customers/stats.
type CustomerStats struct {
	Total, Active, NewToday int
	ByKind                  map[string]int
	ByKindSource            map[string]int
	ByStatus                map[string]int
	BySecurityState         map[string]int
	BySite                  []SiteCount
}

// SiteCount son los clientes activos de un nodo.
type SiteCount struct {
	SiteID           uuid.UUID
	Active           int
	WithOpenFindings int
}

// NewCustomer es una IP vista por primera vez (lote first_seen).
type NewCustomer struct {
	Address        netip.Addr
	ClientPrefixID *uuid.UUID
	FirstSeen      time.Time
}

// SeenCustomer es una IP activa en el resumen horario.
type SeenCustomer struct {
	Address  netip.Addr
	LastSeen time.Time
}

// CustomerEvents construye los eventos de un cliente cambiado.
type CustomerEvents func(c *domain.Customer) []outbox.Event

// CustomerStore es el repositorio de clientes (siempre dentro del tenant).
type CustomerStore interface {
	ListCustomers(ctx context.Context, t pgdb.TenantID, q CustomerQuery) ([]domain.Customer, error)
	CustomerStats(ctx context.Context, t pgdb.TenantID, sites []uuid.UUID, site *uuid.UUID, dayStart time.Time) (*CustomerStats, error)
	GetCustomer(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Customer, error)
	// UpdateCustomer guarda c si su versión sigue siendo expect, con la entrada
	// de historial opcional y los eventos.
	UpdateCustomer(ctx context.Context, t pgdb.TenantID, c *domain.Customer, expect int, change *domain.KindChange, ev CustomerEvents) error
	ListKindChanges(ctx context.Context, t pgdb.TenantID, customerID uuid.UUID, afterAt *time.Time, afterID uuid.UUID, limit int) ([]domain.KindChange, error)
	// DiscoverCustomers crea de forma idempotente los clientes del lote y
	// devuelve los nuevos.
	DiscoverCustomers(ctx context.Context, t pgdb.TenantID, realm uuid.UUID, clients []NewCustomer, ev CustomerEvents) ([]domain.Customer, error)
	// TouchCustomers actualiza last_seen y reactiva los inactivos.
	TouchCustomers(ctx context.Context, t pgdb.TenantID, realm uuid.UUID, seen []SeenCustomer, ev CustomerEvents) (int, error)
	// InactivateIdle pasa a inactive los activos con last_seen < before.
	InactivateIdle(ctx context.Context, t pgdb.TenantID, before time.Time, ev CustomerEvents) (int, error)
	// PurgeExpired borra los clientes (y su historial) con last_seen < before.
	PurgeExpired(ctx context.Context, t pgdb.TenantID, before time.Time, ev CustomerEvents) (int, error)
	// CustomerTenants devuelve los tenants con clientes (job de ciclo de vida).
	CustomerTenants(ctx context.Context) ([]uuid.UUID, error)
}

// Customers implementa los casos de uso de clientes (I1-06).
type Customers struct {
	store  CustomerStore
	cursor *pagination.Codec
	audit  func() (authapi.AuditRecorder, bool)
	now    func() time.Time
	logger *slog.Logger
	// InactivityDays y RetentionMonths del ciclo de vida.
	InactivityDays  int
	RetentionMonths int
}

// NewCustomers crea el servicio.
func NewCustomers(store CustomerStore, cursor *pagination.Codec, audit func() (authapi.AuditRecorder, bool),
	now func() time.Time, logger *slog.Logger,
) *Customers {
	if now == nil {
		now = time.Now
	}
	if cursor == nil {
		cursor = pagination.NewCodec(nil)
	}
	if audit == nil {
		audit = func() (authapi.AuditRecorder, bool) { return nil, false }
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Customers{store: store, cursor: cursor, audit: audit, now: now, logger: logger,
		InactivityDays: domain.DefaultInactivityDays, RetentionMonths: domain.DefaultRetentionMonths}
}

func (s *Customers) ts() time.Time { return s.now().UTC() }

// ---------------------------------------------------------------- eventos

const evPrefix = "horus.devices.customer."

// CustomerData es el estado del cliente en los eventos (sin notas).
func CustomerData(c *domain.Customer) map[string]any {
	return map[string]any{
		"id": c.ID, "version": c.Version, "address": c.AddressString(), "realm_id": c.RealmID, "site_id": c.SiteID,
		"client_prefix_id": c.ClientPrefixID, "kind": c.Kind, "kind_source": c.KindSource, "kind_locked": c.KindLocked,
		"kind_confidence": c.KindConfidence, "commercial_use_suspected": c.CommercialUseSuspected, "status": c.Status,
		"alias": c.Alias, "first_seen": fmtTS(c.FirstSeen), "last_seen": fmtTS(c.LastSeen), "reset_at": fmtTSPtr(c.ResetAt),
	}
}

func fmtTS(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func fmtTSPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return fmtTS(*t)
}

func customerEvent(p *authz.Principal, t pgdb.TenantID, name string, c *domain.Customer, at time.Time, data map[string]any) outbox.Event {
	tid := t.UUID()
	actor := outbox.ActorFrom(p)
	if p == nil {
		actor = outbox.Actor{Type: "service", ID: "svc:devices"}
	}
	return outbox.Event{Type: evPrefix + name, Source: "horus/devices", TenantID: &tid, AggregateType: "customer",
		AggregateID: c.ID, AggregateVersion: c.Version, Actor: actor, OccurredAt: at, Data: data}
}

func lifecycleActor() *authz.Principal { return nil }

// ---------------------------------------------------------------- lectura

var customerSorts = map[string]struct {
	col  string
	desc bool
}{
	"": {"last_seen", true}, "last_seen": {"last_seen", false}, "-last_seen": {"last_seen", true},
	"first_seen": {"first_seen", false}, "-first_seen": {"first_seen", true},
	"kind_changed_at": {"kind_changed_at", false}, "-kind_changed_at": {"kind_changed_at", true},
}

func csv(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func enumList(field, v string, allowed []string) ([]string, error) {
	vals := csv(v)
	for _, x := range vals {
		if !slices.Contains(allowed, x) {
			return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, field+": valor no permitido")
		}
	}
	return vals, nil
}

func boolParam(field, v string) (*bool, error) {
	if v == "" {
		return nil, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, field+": booleano")
	}
	return &b, nil
}

func timeParam(field, v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, field+": fecha RFC 3339")
	}
	return &t, nil
}

// customerQuery interpreta filtros, orden y cursor.
func (s *Customers) customerQuery(p *authz.Principal, t pgdb.TenantID, qv url.Values, extra ...string) (CustomerQuery, pagination.Request, string, error) {
	req, err := pagination.ParseRequest(qv)
	if err != nil {
		code := problem.CodeValidationFailed
		if errors.Is(err, pagination.ErrInvalidCursor) {
			code = problem.CodeInvalidCursor
		}
		return CustomerQuery{}, req, "", apperr.New(apperr.KindBadRequest, code, "limit debe estar entre 1 y 200")
	}
	so, ok := customerSorts[qv.Get("sort")]
	if !ok {
		return CustomerQuery{}, req, "", apperr.New(apperr.KindBadRequest, problem.CodeInvalidSortField, "")
	}
	q := CustomerQuery{SortCol: so.col, Desc: so.desc, Limit: req.Limit + 1, Sites: sitesFor(p, PermCustomersRead)}
	q.Q = strings.TrimSpace(qv.Get("q"))
	if len(q.Q) > 128 {
		return q, req, "", apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "q: máximo 128")
	}
	var errs []error
	var e error
	q.SiteIDs, e = parseUUIDList("site_id", qv.Get("site_id"))
	errs = append(errs, e)
	q.RealmIDs, e = parseUUIDList("realm_id", qv.Get("realm_id"))
	errs = append(errs, e)
	q.PrefixIDs, e = parseUUIDList("client_prefix_id", qv.Get("client_prefix_id"))
	errs = append(errs, e)
	q.Statuses, e = enumList("status", qv.Get("status"), domain.CustomerStatuses)
	errs = append(errs, e)
	q.Kinds, e = enumList("kind", qv.Get("kind"), domain.CustomerKinds)
	errs = append(errs, e)
	q.KindSources, e = enumList("kind_source", qv.Get("kind_source"), domain.CustomerKindSources)
	errs = append(errs, e)
	q.SecurityStates, e = enumList("security_state", qv.Get("security_state"), domain.SecurityStates)
	errs = append(errs, e)
	q.KindLocked, e = boolParam("kind_locked", qv.Get("kind_locked"))
	errs = append(errs, e)
	q.CommercialSuspected, e = boolParam("commercial_use_suspected", qv.Get("commercial_use_suspected"))
	errs = append(errs, e)
	q.HasOpenFindings, e = boolParam("has_open_findings", qv.Get("has_open_findings"))
	errs = append(errs, e)
	q.LastSeenGTE, e = timeParam("last_seen_gte", qv.Get("last_seen_gte"))
	errs = append(errs, e)
	q.LastSeenLTE, e = timeParam("last_seen_lte", qv.Get("last_seen_lte"))
	errs = append(errs, e)
	for _, e := range errs {
		if e != nil {
			return q, req, "", e
		}
	}
	parts := append([]string{q.Q, qv.Get("site_id"), qv.Get("realm_id"), qv.Get("client_prefix_id"), qv.Get("status"), qv.Get("kind"),
		qv.Get("kind_source"), qv.Get("security_state"), qv.Get("kind_locked"), qv.Get("commercial_use_suspected"),
		qv.Get("has_open_findings"), qv.Get("last_seen_gte"), qv.Get("last_seen_lte")}, extra...)
	fk := pagination.FilterKey(parts...)
	if req.Cursor != "" {
		cur, err := s.cursor.Decode(req.Cursor, so.col+fmt.Sprint(so.desc), fk, t.String())
		if err != nil || len(cur.Keys) != 2 {
			return q, req, "", apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		id, err := uuid.Parse(cur.Keys[1])
		if err != nil {
			return q, req, "", apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		q.AfterKey, q.AfterID = cur.Keys[0], id
	}
	return q, req, fk, nil
}

// SortKey devuelve la clave keyset de c para la columna col.
func SortKey(col string, c *domain.Customer) string {
	switch col {
	case "first_seen":
		return c.FirstSeen.UTC().Format(time.RFC3339Nano)
	case "kind_changed_at":
		if c.KindChangedAt == nil {
			return time.Unix(0, 0).UTC().Format(time.RFC3339Nano)
		}
		return c.KindChangedAt.UTC().Format(time.RFC3339Nano)
	}
	return c.LastSeen.UTC().Format(time.RFC3339Nano)
}

func (s *Customers) page(t pgdb.TenantID, q CustomerQuery, req pagination.Request, fk string, rows []domain.Customer) Page[domain.Customer] {
	pg := pagination.Page{Limit: req.Limit}
	if len(rows) > req.Limit {
		rows = rows[:req.Limit]
		last := &rows[len(rows)-1]
		next := s.cursor.Encode(pagination.Cursor{Keys: []string{SortKey(q.SortCol, last), last.ID.String()},
			Sort: q.SortCol + fmt.Sprint(q.Desc), Filter: fk, Tenant: t.String()})
		pg.NextCursor, pg.HasMore = &next, true
	}
	if rows == nil {
		rows = []domain.Customer{}
	}
	return Page[domain.Customer]{Data: rows, Page: pg}
}

// List es GET /customers.
func (s *Customers) List(ctx context.Context, qv url.Values) (Page[domain.Customer], error) {
	p, t, err := scope(ctx, PermCustomersRead)
	if err != nil {
		return Page[domain.Customer]{}, err
	}
	q, req, fk, err := s.customerQuery(p, t, qv)
	if err != nil {
		return Page[domain.Customer]{}, err
	}
	rows, err := s.store.ListCustomers(ctx, t, q)
	if err != nil {
		return Page[domain.Customer]{}, err
	}
	return s.page(t, q, req, fk, rows), nil
}

// LookupInput es el cuerpo de POST /customers/lookup.
type LookupInput struct {
	Address *string    `json:"address"`
	Prefix  *string    `json:"prefix"`
	RealmID *uuid.UUID `json:"realm_id"`
	SiteID  *uuid.UUID `json:"site_id"`
}

// Lookup es POST /customers/lookup (la IP viaja en el cuerpo, nunca en la URL).
func (s *Customers) Lookup(ctx context.Context, qv url.Values, in LookupInput) (Page[domain.Customer], error) {
	p, t, err := scope(ctx, PermCustomersRead)
	if err != nil {
		return Page[domain.Customer]{}, err
	}
	lq := url.Values{"limit": qv["limit"], "cursor": qv["cursor"]}
	var key string
	var contains, within *netip.Prefix
	switch {
	case in.Address != nil && in.Prefix == nil && in.RealmID == nil:
		a, err := netip.ParseAddr(strings.TrimSpace(*in.Address))
		if err != nil {
			return Page[domain.Customer]{}, apperr.Validation(apperr.Field("address", "INVALID_FORMAT", "IP no válida."))
		}
		pp := netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen())
		contains, key = &pp, "a:"+pp.String()
	case in.Prefix != nil && in.Address == nil:
		pp, err := netip.ParsePrefix(strings.TrimSpace(*in.Prefix))
		if err != nil {
			return Page[domain.Customer]{}, apperr.Validation(apperr.Field("prefix", "INVALID_FORMAT", "Prefijo CIDR no válido."))
		}
		pp = pp.Masked()
		within, key = &pp, "p:"+pp.String()
	default:
		return Page[domain.Customer]{}, apperr.Validation(apperr.Field("address", "REQUIRED", "Indique address o prefix (no ambos)."))
	}
	q, req, fk, err := s.customerQuery(p, t, lq, key, fmt.Sprint(in.RealmID, in.SiteID))
	if err != nil {
		return Page[domain.Customer]{}, err
	}
	q.Contains, q.Within = contains, within
	if in.RealmID != nil {
		q.RealmIDs = []uuid.UUID{*in.RealmID}
	}
	if in.SiteID != nil {
		q.SiteIDs = []uuid.UUID{*in.SiteID}
	}
	rows, err := s.store.ListCustomers(ctx, t, q)
	if err != nil {
		return Page[domain.Customer]{}, err
	}
	return s.page(t, q, req, fk, rows), nil
}

// Stats es GET /customers/stats (sin IPs; también para kioscos).
func (s *Customers) Stats(ctx context.Context, qv url.Values) (*CustomerStats, time.Time, error) {
	p := authz.FromContext(ctx)
	tid, err := authz.TenantOf(ctx)
	if err != nil {
		return nil, time.Time{}, apperr.Forbidden(problem.CodeTokenScopeInvalid, "")
	}
	var sites []uuid.UUID
	if p.Type != authz.TypeKiosk {
		if !p.Has(PermCustomersRead) {
			return nil, time.Time{}, apperr.Forbidden(problem.CodePermissionDenied, "")
		}
		sites = sitesFor(p, PermCustomersRead)
	}
	var site *uuid.UUID
	if v := qv.Get("site_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, time.Time{}, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "site_id: UUID no válido")
		}
		site = &id
	}
	now := s.ts()
	st, err := s.store.CustomerStats(ctx, pgdb.TenantID(tid), sites, site, now.Truncate(24*time.Hour))
	return st, now, err
}

func (s *Customers) recordAudit(ctx context.Context, t pgdb.TenantID, action string, c *domain.Customer, changes map[string]any) {
	p := authz.FromContext(ctx)
	rec, ok := s.audit()
	if !ok || p == nil {
		s.logger.WarnContext(ctx, "customer audit not recorded: auth not local", slog.String("action", action))
		return
	}
	if err := rec.Record(ctx, authapi.AuditEntry{TenantID: t.UUID(), OccurredAt: s.ts(), ActorType: p.Type, ActorID: p.Subject,
		ViaPlatform: p.ViaPlatform, Action: action, ResourceType: "customer", ResourceID: c.ID.String(), Scope: "tenant",
		Outcome: "success", Changes: changes}); err != nil {
		s.logger.ErrorContext(ctx, "customer audit failed", slog.String("action", action), slog.Any("error", err))
	}
}

func (s *Customers) get(ctx context.Context, perm string, id uuid.UUID) (*authz.Principal, pgdb.TenantID, *domain.Customer, error) {
	p, t, err := scope(ctx, perm)
	if err != nil {
		return nil, t, nil, err
	}
	c, err := s.store.GetCustomer(ctx, t, id)
	if err != nil {
		return nil, t, nil, mapCustomerErr(err)
	}
	if !siteAllowed(p, perm, c.SiteID) {
		return nil, t, nil, apperr.NotFound(domain.CodeCustomerNotFound)
	}
	return p, t, c, nil
}

func mapCustomerErr(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return apperr.NotFound(domain.CodeCustomerNotFound)
	case errors.Is(err, ErrVersionChanged):
		return err
	}
	return err
}

// Get es GET /customers/{id} (acceso auditado).
func (s *Customers) Get(ctx context.Context, id uuid.UUID) (*domain.Customer, error) {
	_, t, c, err := s.get(ctx, PermCustomersRead, id)
	if err != nil {
		return nil, err
	}
	s.recordAudit(ctx, t, "customers.read", c, map[string]any{})
	return c, nil
}

// save aplica un cambio con concurrencia optimista y traduce el 412.
func (s *Customers) save(ctx context.Context, t pgdb.TenantID, c *domain.Customer, expect int, ch *domain.KindChange, ev CustomerEvents) error {
	if c.Version != expect {
		return apperr.PreconditionFailed(c)
	}
	err := s.store.UpdateCustomer(ctx, t, c, expect, ch, ev)
	if errors.Is(err, ErrVersionChanged) {
		cur, gerr := s.store.GetCustomer(ctx, t, c.ID)
		if gerr != nil {
			return mapCustomerErr(gerr)
		}
		return apperr.PreconditionFailed(cur)
	}
	return mapCustomerErr(err)
}

// Update es PATCH /customers/{id}: alias y notas (auditado sin el valor).
func (s *Customers) Update(ctx context.Context, id uuid.UUID, expect int, f Fields) (*domain.Customer, error) {
	p, t, c, err := s.get(ctx, PermCustomersUpdate, id)
	if err != nil {
		return nil, err
	}
	var fe fieldErrs
	unknownFields(&fe, f, "alias", "notes")
	alias, hasAlias := fe.str(f, "alias", 120, true)
	notes, hasNotes := fe.str(f, "notes", 2000, true)
	if err := fe.err(); err != nil {
		return nil, err
	}
	if c.Version != expect {
		return nil, apperr.PreconditionFailed(c)
	}
	if hasAlias {
		c.Alias = alias
		c.AliasSource = nil
		if alias != nil {
			src := "manual"
			c.AliasSource = &src
		}
	}
	if hasNotes {
		c.Notes = notes
	}
	changed := changedFields(f)
	now := s.ts()
	err = s.save(ctx, t, c, expect, nil, func(c *domain.Customer) []outbox.Event {
		d := CustomerData(c)
		d["changed_fields"] = changed
		return []outbox.Event{customerEvent(p, t, "updated", c, now, d)}
	})
	if err != nil {
		return nil, err
	}
	s.recordAudit(ctx, t, "customers.update", c, map[string]any{"fields": changed})
	return c, nil
}

// SetKindInput es el cuerpo de set-kind.
type SetKindInput struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

func validReason(r string) bool {
	n := len([]rune(strings.TrimSpace(r)))
	return n >= 3 && n <= 500
}

func actorUUID(p *authz.Principal) *uuid.UUID {
	if p == nil || p.UserID == uuid.Nil {
		return nil
	}
	id := p.UserID
	return &id
}

func changeData(ch *domain.KindChange) map[string]any {
	return map[string]any{"from_kind": ch.FromKind, "to_kind": ch.ToKind, "source": ch.Source, "model_ref": ch.ModelRef,
		"reasons": json.RawMessage(ch.Reasons), "manual_reason": ch.ManualReason, "causation_event_id": nil, "changed_at": fmtTS(ch.ChangedAt)}
}

// SetKind es POST /customers/{id}/set-kind (manual: kind_locked = true).
func (s *Customers) SetKind(ctx context.Context, id uuid.UUID, expect int, in SetKindInput) (*domain.Customer, error) {
	p, t, c, err := s.get(ctx, PermCustomersKindWrite, id)
	if err != nil {
		return nil, err
	}
	var fe fieldErrs
	if !slices.Contains(domain.CustomerKinds, in.Kind) {
		fe.add("kind", "INVALID_VALUE", "Valores permitidos: "+strings.Join(domain.CustomerKinds, ", ")+".")
	}
	if !validReason(in.Reason) {
		fe.add("reason", "REQUIRED", "El motivo es obligatorio (3–500 caracteres).")
	}
	if err := fe.err(); err != nil {
		return nil, err
	}
	if c.Version != expect {
		return nil, apperr.PreconditionFailed(c)
	}
	now := s.ts()
	ch := c.SetKind(in.Kind, strings.TrimSpace(in.Reason), actorUUID(p), now)
	if ch == nil {
		return c, nil
	}
	err = s.save(ctx, t, c, expect, ch, func(c *domain.Customer) []outbox.Event {
		d := CustomerData(c)
		d["change"] = changeData(ch)
		return []outbox.Event{customerEvent(p, t, "kind_changed", c, now, d)}
	})
	if err != nil {
		return nil, err
	}
	s.recordAudit(ctx, t, "customers.kind.write", c, map[string]any{"from_kind": ch.FromKind, "to_kind": ch.ToKind})
	return c, nil
}

// UnlockKind es POST /customers/{id}/unlock-kind.
func (s *Customers) UnlockKind(ctx context.Context, id uuid.UUID, expect int) (*domain.Customer, error) {
	p, t, c, err := s.get(ctx, PermCustomersKindWrite, id)
	if err != nil {
		return nil, err
	}
	if c.Version != expect {
		return nil, apperr.PreconditionFailed(c)
	}
	if !c.KindLocked {
		return c, nil
	}
	c.KindLocked = false
	if c.KindSource == "manual" {
		c.KindSource = "default" // ck_customer__manual_locked: manual exige bloqueo
	}
	now := s.ts()
	err = s.save(ctx, t, c, expect, nil, func(c *domain.Customer) []outbox.Event {
		d := CustomerData(c)
		d["changed_fields"] = []string{"kind_locked"}
		return []outbox.Event{customerEvent(p, t, "updated", c, now, d)}
	})
	if err != nil {
		return nil, err
	}
	s.recordAudit(ctx, t, "customers.kind.unlock", c, map[string]any{})
	return c, nil
}

// Reset es POST /customers/{id}/reset (customers.kind.write + customers.update).
func (s *Customers) Reset(ctx context.Context, id uuid.UUID, expect int, reason string) (*domain.Customer, error) {
	p, t, c, err := s.get(ctx, PermCustomersKindWrite, id)
	if err != nil {
		return nil, err
	}
	if !p.HasScope(PermCustomersUpdate, "site:"+c.SiteID.String()) {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "Reiniciar exige customers.kind.write y customers.update.")
	}
	if !validReason(reason) {
		return nil, apperr.Validation(apperr.Field("reason", "REQUIRED", "El motivo es obligatorio (3–500 caracteres)."))
	}
	if c.Version != expect {
		return nil, apperr.PreconditionFailed(c)
	}
	now := s.ts()
	reason = strings.TrimSpace(reason)
	ch := c.Reset(reason, actorUUID(p), now)
	err = s.save(ctx, t, c, expect, ch, func(c *domain.Customer) []outbox.Event {
		return []outbox.Event{customerEvent(p, t, "reset", c, now, map[string]any{
			"id": c.ID, "version": c.Version, "address": c.AddressString(), "kind": c.Kind, "kind_source": c.KindSource,
			"kind_locked": c.KindLocked, "alias": nil, "reset_at": fmtTS(now), "reason": reason,
		})}
	})
	if err != nil {
		return nil, err
	}
	s.recordAudit(ctx, t, "customers.reset", c, map[string]any{"to_kind": c.Kind})
	return c, nil
}

// KindHistory es GET /customers/{id}/kind-history (más reciente primero).
func (s *Customers) KindHistory(ctx context.Context, id uuid.UUID, qv url.Values) (Page[domain.KindChange], error) {
	_, t, c, err := s.get(ctx, PermCustomersRead, id)
	if err != nil {
		return Page[domain.KindChange]{}, err
	}
	req, err := pagination.ParseRequest(qv)
	if err != nil {
		return Page[domain.KindChange]{}, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
	}
	var afterAt *time.Time
	afterID := uuid.Nil
	fk := pagination.FilterKey(c.ID.String())
	if req.Cursor != "" {
		cur, err := s.cursor.Decode(req.Cursor, "changed_at", fk, t.String())
		if err != nil || len(cur.Keys) != 2 {
			return Page[domain.KindChange]{}, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		at, err1 := time.Parse(time.RFC3339Nano, cur.Keys[0])
		id, err2 := uuid.Parse(cur.Keys[1])
		if err1 != nil || err2 != nil {
			return Page[domain.KindChange]{}, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		afterAt, afterID = &at, id
	}
	rows, err := s.store.ListKindChanges(ctx, t, c.ID, afterAt, afterID, req.Limit+1)
	if err != nil {
		return Page[domain.KindChange]{}, err
	}
	pg := pagination.Page{Limit: req.Limit}
	if len(rows) > req.Limit {
		rows = rows[:req.Limit]
		last := rows[len(rows)-1]
		next := s.cursor.Encode(pagination.Cursor{Keys: []string{last.ChangedAt.UTC().Format(time.RFC3339Nano), last.ID.String()},
			Sort: "changed_at", Filter: fk, Tenant: t.String()})
		pg.NextCursor, pg.HasMore = &next, true
	}
	if rows == nil {
		rows = []domain.KindChange{}
	}
	return Page[domain.KindChange]{Data: rows, Page: pg}, nil
}
