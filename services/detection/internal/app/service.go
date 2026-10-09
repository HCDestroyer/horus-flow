// Package app contiene los casos de uso de detection: la capa de hallazgos
// del motor (engine.Sink: deduplicación, silencio, reincidencia, estado de
// seguridad y eventos por outbox) y la API del contrato
// (packages/schemas/openapi/v0/detection.yaml): listado y detalle de
// hallazgos, transiciones, evidencia auditada, resumen para widgets y
// allowlist del ISP.
package app

import (
	"context"
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
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/engine"
)

// Permisos (services/auth/internal/domain/permissions.v0.yaml).
const (
	PermRead      = "security.findings.read"
	PermManage    = "security.findings.manage"
	PermEvidence  = "security.evidence.read"
	PermCustomers = "customers.read"
)

// Códigos de error del contrato.
const (
	CodeFindingNotFound     = "FINDING_NOT_FOUND"
	CodeFindingStateInvalid = "FINDING_STATE_INVALID"
)

// EvidenceRow es un flujo de evidencia (sin payloads).
type EvidenceRow struct {
	TS         time.Time
	RemoteIP   netip.Addr
	RemotePort uint16
	RemoteASN  uint32
	Protocol   uint8
	TCPFlags   uint8
	Direction  string
	Bytes      uint64
	Packets    uint64
	Reputation string
}

// WatchedPort es una fila del widget watched_ports.
type WatchedPort struct {
	Port      uint16
	Protocol  uint8
	Customers uint64
	Flows     uint64
}

// Flows es la lectura de ClickHouse que necesita la API (puede ser nil).
type Flows interface {
	Evidence(ctx context.Context, tenant, realm uuid.UUID, client netip.Prefix, from, to time.Time, offset, limit int) ([]EvidenceRow, error)
	WatchedPorts(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID, from, to time.Time) ([]WatchedPort, error)
	Customers(ctx context.Context, tenant uuid.UUID, keys []engine.ClientKey) (map[engine.ClientKey]engine.Customer, error)
	CustomerKnown(ctx context.Context, tenant, customer uuid.UUID) (bool, error)
}

// Registry anota los tenants que usan detection (planificador del motor).
type Registry interface {
	RegisterTenant(ctx context.Context, tenant uuid.UUID) error
}

// Service implementa los casos de uso.
type Service struct {
	db       *pgdb.DB
	flows    Flows
	audit    authapi.AuditRecorder
	cursor   *pagination.Codec
	registry Registry
	now      func() time.Time
	log      *slog.Logger
}

// Options del servicio.
type Options struct {
	Flows    Flows
	Audit    authapi.AuditRecorder
	Cursor   *pagination.Codec
	Registry Registry
	Now      func() time.Time
	Logger   *slog.Logger
}

// NewService crea el servicio.
func NewService(db *pgdb.DB, o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Cursor == nil {
		o.Cursor = pagination.NewCodec(nil)
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &Service{db: db, flows: o.Flows, audit: o.Audit, cursor: o.Cursor, registry: o.Registry, now: o.Now, log: o.Logger}
}

// scope devuelve principal, tenant y los nodos a los que se limita perm.
func (s *Service) scope(ctx context.Context, perm string) (*authz.Principal, pgdb.TenantID, []uuid.UUID, error) {
	p := authz.FromContext(ctx)
	tid, err := authz.TenantOf(ctx)
	if err != nil {
		return nil, pgdb.TenantID{}, nil, apperr.Forbidden(problem.CodeTokenScopeInvalid, "")
	}
	if !p.Has(perm) {
		return nil, pgdb.TenantID{}, nil, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	if s.registry != nil {
		if err := s.registry.RegisterTenant(ctx, tid); err != nil {
			s.log.WarnContext(ctx, "tenant registry", "err", err)
		}
	}
	var sites []uuid.UUID
	if !p.TenantWide(perm) {
		sites = p.SiteScopes(perm)
		if sites == nil {
			sites = []uuid.UUID{}
		}
	}
	return p, pgdb.TenantID(tid), sites, nil
}

func actorID(p *authz.Principal) *uuid.UUID {
	if p == nil || p.UserID == uuid.Nil {
		return nil
	}
	id := p.UserID
	return &id
}

// Page es una página de hallazgos con su vista.
type Page struct {
	Data []map[string]any
	Page pagination.Page
}

func splitList(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, x := range strings.Split(v, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func badFilter(msg string) error {
	return apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, msg)
}

var (
	validStates     = []string{domain.StateOpen, domain.StateAcknowledged, domain.StateResolved, domain.StateFalsePositive}
	validSeverities = []string{domain.SeverityLow, domain.SeverityMedium, domain.SeverityHigh, domain.SeverityCritical}
	validSorts      = []string{"", "-severity", "-last_seen_at", "-opened_at", "opened_at"}
)

// List es GET /findings y GET /customers/{id}/findings.
func (s *Service) List(ctx context.Context, qv url.Values, customer *uuid.UUID) (*Page, error) {
	p, t, allowed, err := s.scope(ctx, PermRead)
	if err != nil {
		return nil, err
	}
	req, err := pagination.ParseRequest(qv)
	if err != nil {
		code := problem.CodeValidationFailed
		if errors.Is(err, pagination.ErrInvalidCursor) {
			code = problem.CodeInvalidCursor
		}
		return nil, apperr.New(apperr.KindBadRequest, code, "limit debe estar entre 1 y 200")
	}
	f := postgres.ListFilter{Kinds: splitList(qv.Get("kind")), Severities: splitList(qv.Get("severity")), States: splitList(qv.Get("state")),
		CustomerID: customer, AllowedSites: allowed, Sort: qv.Get("sort"), Limit: req.Limit}
	for _, st := range f.States {
		if !slices.Contains(validStates, st) {
			return nil, badFilter("state: valores permitidos open, acknowledged, resolved, false_positive")
		}
	}
	for _, sv := range f.Severities {
		if !slices.Contains(validSeverities, sv) {
			return nil, badFilter("severity: valores permitidos low, medium, high, critical")
		}
	}
	if !slices.Contains(validSorts, f.Sort) {
		return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidSortField, "")
	}
	if v := qv.Get("customer_id"); v != "" && customer == nil {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, badFilter("customer_id: UUID no válido")
		}
		f.CustomerID = &id
	}
	for _, v := range splitList(qv.Get("site_id")) {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, badFilter("site_id: UUID no válido")
		}
		f.Sites = append(f.Sites, id)
	}
	if v := qv.Get("opened_at_gte"); v != "" {
		at, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil, badFilter("opened_at_gte: fecha RFC 3339")
		}
		f.OpenedGTE = &at
	}
	custKey := ""
	if customer != nil {
		custKey = customer.String()
	}
	fk := pagination.FilterKey(qv.Get("kind"), qv.Get("severity"), qv.Get("state"), qv.Get("customer_id"), qv.Get("site_id"),
		qv.Get("opened_at_gte"), custKey)
	if req.Cursor != "" {
		cur, err := s.cursor.Decode(req.Cursor, "offset"+f.Sort, fk, t.String())
		if err != nil {
			return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		if f.Offset, err = strconv.Atoi(cur.Keys[0]); err != nil || f.Offset < 0 {
			return nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
	}
	var rows []domain.Finding
	err = s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		if customer != nil {
			known, err := postgres.KnownCustomer(ctx, tx, *customer)
			if err != nil {
				return err
			}
			if !known && s.flows != nil {
				// Cliente sin historial de seguridad: existe si devices lo proyectó en dim.customer.
				known, _ = s.flows.CustomerKnown(ctx, t.UUID(), *customer)
			}
			if !known {
				return apperr.NotFound("CUSTOMER_NOT_FOUND")
			}
		}
		rows, err = postgres.List(ctx, tx, f)
		return err
	})
	if err != nil {
		return nil, err
	}
	page := pagination.Page{Limit: req.Limit}
	if len(rows) > req.Limit {
		rows = rows[:req.Limit]
		next := s.cursor.Encode(pagination.Cursor{Keys: []string{strconv.Itoa(f.Offset + req.Limit)}, Sort: "offset" + f.Sort, Filter: fk, Tenant: t.String()})
		page.NextCursor, page.HasMore = &next, true
	}
	views := s.views(ctx, p, t, rows)
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		out = append(out, Document(&rows[i], views[i]))
	}
	return &Page{Data: out, Page: page}, nil
}

// views decide el bloque customer y el render de comandos (customers.read,
// nunca kioscos) y resuelve alias/tipo en dim.customer si está disponible.
func (s *Service) views(ctx context.Context, p *authz.Principal, t pgdb.TenantID, rows []domain.Finding) []View {
	out := make([]View, len(rows))
	if p == nil || p.Type == authz.TypeKiosk || !p.Has(PermCustomers) {
		return out
	}
	custs := map[engine.ClientKey]engine.Customer{}
	if s.flows != nil && len(rows) > 0 {
		keys := make([]engine.ClientKey, 0, len(rows))
		for _, f := range rows {
			keys = append(keys, engine.ClientKey{Realm: f.RealmID, IP: f.Address.Addr().Unmap()})
		}
		if got, err := s.flows.Customers(ctx, t.UUID(), keys); err == nil {
			custs = got
		}
	}
	for i, f := range rows {
		addr := f.Address.Addr().Unmap().String()
		if f.Address.Addr().Is6() && !f.Address.Addr().Is4In6() {
			addr = f.Address.String()
		}
		cv := &CustomerView{Address: addr, Kind: f.CustomerKind}
		if c, ok := custs[engine.ClientKey{Realm: f.RealmID, IP: f.Address.Addr().Unmap()}]; ok {
			cv.Alias, cv.Kind = c.Alias, c.Kind
		}
		out[i] = View{Customer: cv, Render: true}
	}
	return out
}

func (s *Service) load(ctx context.Context, perm string, id uuid.UUID) (*authz.Principal, pgdb.TenantID, *domain.Finding, error) {
	p, t, allowed, err := s.scope(ctx, perm)
	if err != nil {
		return nil, t, nil, err
	}
	var f *domain.Finding
	err = s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		f, err = postgres.Get(ctx, tx, id, false)
		return err
	})
	if errors.Is(err, postgres.ErrNotFound) || (err == nil && allowed != nil && !slices.Contains(allowed, f.SiteID)) {
		return nil, t, nil, apperr.NotFound(CodeFindingNotFound)
	}
	return p, t, f, err
}

// Get es GET /findings/{id}.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (map[string]any, int, error) {
	p, t, f, err := s.load(ctx, PermRead, id)
	if err != nil {
		return nil, 0, err
	}
	return Document(f, s.views(ctx, p, t, []domain.Finding{*f})[0]), f.Version, nil
}

// Transition es el cuerpo de acknowledge/resolve/mark-false-positive.
type Transition struct {
	Comment      *string  `json:"comment"`
	ActionsTaken []string `json:"actions_taken"`
	SilenceDays  *int     `json:"silence_days"`
}

// Transition aplica op (acknowledge | resolve | false_positive) con If-Match.
func (s *Service) Transition(ctx context.Context, id uuid.UUID, ifMatch int, op string, body Transition) (map[string]any, int, error) {
	p, t, allowed, err := s.scope(ctx, PermManage)
	if err != nil {
		return nil, 0, err
	}
	// Existencia antes que validación: un hallazgo de otro ISP es 404, no 422.
	if _, _, _, err := s.load(ctx, PermManage, id); err != nil {
		return nil, 0, err
	}
	if op == "false_positive" {
		var fe []problem.FieldError
		if body.Comment == nil || len([]rune(strings.TrimSpace(*body.Comment))) < 3 {
			fe = append(fe, apperr.Field("comment", "REQUIRED", "El comentario es obligatorio (mínimo 3 caracteres)."))
		} else if len([]rune(*body.Comment)) > 2000 {
			fe = append(fe, apperr.Field("comment", "TOO_LONG", "Máximo 2000 caracteres."))
		}
		if body.SilenceDays != nil && (*body.SilenceDays < 1 || *body.SilenceDays > 365) {
			fe = append(fe, apperr.Field("silence_days", "OUT_OF_RANGE", "Entre 1 y 365."))
		}
		if len(fe) > 0 {
			return nil, 0, apperr.Validation(fe...)
		}
	}
	if body.Comment != nil && len([]rune(*body.Comment)) > 2000 {
		return nil, 0, apperr.Validation(apperr.Field("comment", "TOO_LONG", "Máximo 2000 caracteres."))
	}
	now := s.now().UTC()
	actor := actorID(p)
	var f *domain.Finding
	err = s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		f, err = postgres.Get(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if allowed != nil && !slices.Contains(allowed, f.SiteID) {
			return postgres.ErrNotFound
		}
		if ifMatch != f.Version {
			return apperr.PreconditionFailed(Document(f, View{}))
		}
		expect := f.Version
		evType := EventResolved
		switch op {
		case "acknowledge":
			err = f.Acknowledge(actor, now)
			evType = EventUpdated
		case "resolve":
			err = f.Resolve(actor, body.Comment, body.ActionsTaken, now)
		default:
			days := 30
			if body.SilenceDays != nil {
				days = *body.SilenceDays
			}
			err = f.MarkFalsePositive(actor, strings.TrimSpace(*body.Comment), time.Duration(days)*24*time.Hour, now)
		}
		if errors.Is(err, domain.ErrStateInvalid) {
			return apperr.Conflict(CodeFindingStateInvalid, fmt.Sprintf("No se puede pasar de %s a ese estado.", f.State))
		}
		if err != nil {
			return err
		}
		if err := postgres.Update(ctx, tx, f, expect); err != nil {
			return err
		}
		if op != "acknowledge" {
			if err := postgres.InsertVerdict(ctx, tx, t, f, actor); err != nil {
				return err
			}
		}
		if err := outbox.Insert(ctx, tx, schema, findingEvent(evType, f, outbox.ActorFrom(p), now)); err != nil {
			return err
		}
		return s.refreshSecurity(ctx, tx, t, f.CustomerID, f.SiteID, now)
	})
	if errors.Is(err, postgres.ErrNotFound) {
		return nil, 0, apperr.NotFound(CodeFindingNotFound)
	}
	if err != nil {
		return nil, 0, err
	}
	return Document(f, s.views(ctx, p, t, []domain.Finding{*f})[0]), f.Version, nil
}

// AuditMeta son los datos de la petición para la auditoría.
type AuditMeta struct {
	IP, UserAgent, RequestID string
}

// EvidencePage es GET /findings/{id}/evidence.
func (s *Service) Evidence(ctx context.Context, id uuid.UUID, qv url.Values, meta AuditMeta) ([]map[string]any, pagination.Page, error) {
	var page pagination.Page
	p, t, f, err := s.load(ctx, PermEvidence, id)
	if err != nil {
		return nil, page, err
	}
	req, err := pagination.ParseRequest(qv)
	if err != nil {
		return nil, page, apperr.New(apperr.KindBadRequest, problem.CodeValidationFailed, "limit debe estar entre 1 y 200")
	}
	offset := 0
	if req.Cursor != "" {
		cur, err := s.cursor.Decode(req.Cursor, "evidence", id.String(), t.String())
		if err != nil {
			return nil, page, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		offset, _ = strconv.Atoi(cur.Keys[0])
	}
	if err := s.recordAudit(ctx, p, t, f, meta); err != nil {
		return nil, page, err
	}
	if s.flows == nil {
		return nil, page, apperr.New(apperr.KindUnavailable, problem.CodeServiceUnavailable, "ClickHouse no configurado")
	}
	from, to := f.FirstSeenAt.Add(-time.Minute), f.LastSeenAt.Add(time.Minute)
	if f.WindowFrom.Before(from) {
		from = f.WindowFrom
	}
	if f.WindowTo.After(to) {
		to = f.WindowTo
	}
	rows, err := s.flows.Evidence(ctx, t.UUID(), f.RealmID, f.Address, from, to, offset, req.Limit+1)
	if err != nil {
		return nil, page, apperr.New(apperr.KindUnavailable, problem.CodeServiceUnavailable, "No se pudo leer la evidencia")
	}
	page.Limit = req.Limit
	if len(rows) > req.Limit {
		rows = rows[:req.Limit]
		next := s.cursor.Encode(pagination.Cursor{Keys: []string{strconv.Itoa(offset + req.Limit)}, Sort: "evidence", Filter: id.String(), Tenant: t.String()})
		page.NextCursor, page.HasMore = &next, true
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		var asn, rep any
		if r.RemoteASN != 0 {
			asn = r.RemoteASN
		}
		if r.Reputation != "" && r.Reputation != "none" {
			rep = r.Reputation
		}
		out = append(out, map[string]any{"ts": ts(r.TS), "remote_ip": r.RemoteIP.Unmap().String(), "remote_port": r.RemotePort,
			"remote_asn": asn, "protocol": r.Protocol, "tcp_flags": r.TCPFlags, "direction": r.Direction,
			"bytes": strconv.FormatUint(r.Bytes, 10), "packets": strconv.FormatUint(r.Packets, 10), "reputation_category": rep})
	}
	return out, page, nil
}

// recordAudit audita el acceso a la evidencia (x-audited): en proceso con
// auth si está local; si no, por el evento horus.detection.audit.recorded.
func (s *Service) recordAudit(ctx context.Context, p *authz.Principal, t pgdb.TenantID, f *domain.Finding, meta AuditMeta) error {
	now := s.now().UTC()
	actor := outbox.ActorFrom(p)
	if s.audit != nil {
		return s.audit.Record(ctx, authapi.AuditEntry{TenantID: t.UUID(), OccurredAt: now, ActorType: actor.Type, ActorID: actor.ID,
			ViaPlatform: actor.ViaPlatform, Action: PermEvidence, ResourceType: "finding", ResourceID: f.ID.String(), Scope: "tenant",
			Outcome: "success", IP: meta.IP, UserAgent: meta.UserAgent, RequestID: meta.RequestID, Changes: map[string]any{}})
	}
	tid := t.UUID()
	id := uuid.Must(uuid.NewV7())
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		return outbox.Insert(ctx, tx, schema, outbox.Event{Type: EventAudit, Source: source, TenantID: &tid, AggregateType: "audit",
			AggregateID: id, AggregateVersion: 1, Actor: actor, OccurredAt: now,
			Data: map[string]any{"id": id, "tenant_id": tid, "occurred_at": ts(now), "source_service": "detection", "actor": actor,
				"ip": meta.IP, "user_agent": meta.UserAgent, "action": PermEvidence, "resource_type": "finding", "resource_id": f.ID,
				"scope": "tenant", "outcome": "success", "reason": nil, "changes": map[string]any{}, "request_id": meta.RequestID, "trace_id": nil}})
	})
}

var ranges = map[string]time.Duration{"15m": 15 * time.Minute, "1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour,
	"7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "90d": 90 * 24 * time.Hour}

// Summary es GET /security/summary (sin IPs; también para kioscos).
func (s *Service) Summary(ctx context.Context, qv url.Values) (map[string]any, error) {
	_, t, allowed, err := s.scope(ctx, PermRead)
	if err != nil {
		return nil, err
	}
	sites := allowed
	if v := qv.Get("site_id"); v != "" {
		var req []uuid.UUID
		for _, x := range splitList(v) {
			id, err := uuid.Parse(x)
			if err != nil {
				return nil, badFilter("site_id: UUID no válido")
			}
			if allowed == nil || slices.Contains(allowed, id) {
				req = append(req, id)
			}
		}
		if req == nil {
			req = []uuid.UUID{}
		}
		sites = req
	}
	rng := qv.Get("range")
	if rng == "" {
		rng = "1h"
	}
	d, ok := ranges[rng]
	if !ok {
		return nil, badFilter("range: 15m, 1h, 6h, 24h, 7d, 30d o 90d")
	}
	now := s.now().UTC()
	var sc *postgres.SummaryCounts
	err = s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		sc, err = postgres.Summary(ctx, tx, sites, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	bySite := make([]map[string]any, 0, len(sc.BySite))
	for _, x := range sc.BySite {
		bySite = append(bySite, map[string]any{"site_id": x.SiteID, "customers_with_signals": x.Customers, "open_findings": x.OpenFindings})
	}
	out := map[string]any{"open_by_severity": sc.OpenBySeverity, "new_last_24h": sc.NewLast24h, "affected_customers": sc.Affected,
		"by_signal": sc.BySignal, "by_kind": sc.ByKind, "by_security_state": sc.BySecurityState, "by_site": bySite,
		"generated_at": ts(now), "range": rng}
	wp := []map[string]any{}
	if s.flows != nil {
		if rows, err := s.flows.WatchedPorts(ctx, t.UUID(), sites, now.Add(-d), now); err == nil {
			for _, r := range rows {
				proto := "tcp"
				if r.Protocol == 17 {
					proto = "udp"
				}
				wp = append(wp, map[string]any{"port": r.Port, "protocol": proto, "customers": r.Customers, "flows": strconv.FormatUint(r.Flows, 10)})
			}
		} else {
			s.log.WarnContext(ctx, "watched ports unavailable", "err", err)
		}
	}
	out["watched_ports"] = wp
	return out, nil
}

// AllowView es una entrada de la allowlist en la API.
func allowJSON(a postgres.AllowEntry) map[string]any {
	var prefix, asn any
	if a.Prefix != nil {
		prefix = a.Prefix.String()
	}
	if a.ASN != nil {
		asn = *a.ASN
	}
	kinds := a.Kinds
	if kinds == nil {
		kinds = []string{}
	}
	return map[string]any{"id": a.ID, "tenant_id": a.TenantID, "prefix": prefix, "asn": asn, "kinds": kinds, "reason": a.Reason,
		"created_at": ts(a.CreatedAt), "created_by": a.CreatedBy}
}

// ListAllow es GET /reputation/allowlist.
func (s *Service) ListAllow(ctx context.Context, qv url.Values) ([]map[string]any, pagination.Page, error) {
	var page pagination.Page
	_, t, _, err := s.scope(ctx, PermRead)
	if err != nil {
		return nil, page, err
	}
	req, err := pagination.ParseRequest(qv)
	if err != nil {
		return nil, page, apperr.New(apperr.KindBadRequest, problem.CodeValidationFailed, "limit debe estar entre 1 y 200")
	}
	offset := 0
	if req.Cursor != "" {
		cur, err := s.cursor.Decode(req.Cursor, "allow", "", t.String())
		if err != nil {
			return nil, page, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		offset, _ = strconv.Atoi(cur.Keys[0])
	}
	var rows []postgres.AllowEntry
	err = s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		rows, err = postgres.ListAllow(ctx, tx, offset, req.Limit+1)
		return err
	})
	if err != nil {
		return nil, page, err
	}
	page.Limit = req.Limit
	if len(rows) > req.Limit {
		rows = rows[:req.Limit]
		next := s.cursor.Encode(pagination.Cursor{Keys: []string{strconv.Itoa(offset + req.Limit)}, Sort: "allow", Tenant: t.String()})
		page.NextCursor, page.HasMore = &next, true
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, allowJSON(r))
	}
	return out, page, nil
}

// AllowInput es el cuerpo de POST /reputation/allowlist.
type AllowInput struct {
	Prefix *string  `json:"prefix"`
	ASN    *int64   `json:"asn"`
	Kinds  []string `json:"kinds"`
	Reason string   `json:"reason"`
}

// CreateAllow es POST /reputation/allowlist.
func (s *Service) CreateAllow(ctx context.Context, in AllowInput) (map[string]any, error) {
	p, t, _, err := s.scope(ctx, PermManage)
	if err != nil {
		return nil, err
	}
	var fe []problem.FieldError
	a := postgres.AllowEntry{ID: uuid.Must(uuid.NewV7()), TenantID: t.UUID(), Kinds: in.Kinds, Reason: strings.TrimSpace(in.Reason), CreatedAt: s.now().UTC()}
	if a.Kinds == nil {
		a.Kinds = []string{}
	}
	if (in.Prefix == nil) == (in.ASN == nil) {
		fe = append(fe, apperr.Field("prefix", "ONE_OF_REQUIRED", "Indique un prefijo o un ASN (solo uno)."))
	}
	if in.Prefix != nil {
		pf, err := netip.ParsePrefix(*in.Prefix)
		if err != nil {
			if ad, err2 := netip.ParseAddr(*in.Prefix); err2 == nil {
				pf, err = netip.PrefixFrom(ad.Unmap(), ad.Unmap().BitLen()), nil
			}
		}
		if err != nil {
			fe = append(fe, apperr.Field("prefix", "INVALID_VALUE", "CIDR no válido."))
		} else if pf.Bits() == 0 || (pf.Addr().Is4() && pf.Bits() < 8) {
			fe = append(fe, apperr.Field("prefix", "TOO_BROAD", "El prefijo es demasiado amplio."))
		} else {
			pf = pf.Masked()
			a.Prefix = &pf
		}
	}
	if in.ASN != nil {
		if *in.ASN < 1 || *in.ASN > 4294967295 {
			fe = append(fe, apperr.Field("asn", "OUT_OF_RANGE", "ASN entre 1 y 4294967295."))
		}
		a.ASN = in.ASN
	}
	if a.Reason == "" || len([]rune(a.Reason)) > 300 {
		fe = append(fe, apperr.Field("reason", "REQUIRED", "Motivo obligatorio (máximo 300 caracteres)."))
	}
	for _, k := range a.Kinds {
		if k == "" || len(k) > 64 || strings.Trim(k, "abcdefghijklmnopqrstuvwxyz0123456789_") != "" {
			fe = append(fe, apperr.Field("kinds", "INVALID_VALUE", "kind no válido."))
			break
		}
	}
	if p.UserID == uuid.Nil {
		fe = append(fe, apperr.Field("created_by", "INVALID_VALUE", "Solo un usuario puede crear entradas."))
	}
	if len(fe) > 0 {
		return nil, apperr.Validation(fe...)
	}
	a.CreatedBy = p.UserID
	if err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error { return postgres.InsertAllow(ctx, tx, a) }); err != nil {
		return nil, err
	}
	return allowJSON(a), nil
}

// DeleteAllow es DELETE /reputation/allowlist/{id}.
func (s *Service) DeleteAllow(ctx context.Context, id uuid.UUID) error {
	_, t, _, err := s.scope(ctx, PermManage)
	if err != nil {
		return err
	}
	err = s.db.TenantTx(ctx, t, func(tx pgx.Tx) error { return postgres.DeleteAllow(ctx, tx, id, s.now().UTC()) })
	if errors.Is(err, postgres.ErrNotFound) {
		return apperr.NotFound(problem.CodeNotFound)
	}
	return err
}
