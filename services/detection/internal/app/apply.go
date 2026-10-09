package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/engine"
)

var _ engine.Sink = (*Service)(nil)

// Tipos de evento (packages/events/catalog/detection.yaml).
const (
	EventOpened        = "horus.detection.finding.opened"
	EventUpdated       = "horus.detection.finding.updated"
	EventResolved      = "horus.detection.finding.resolved"
	EventSecurityState = "horus.detection.customer.security_state_changed"
	EventAudit         = "horus.detection.audit.recorded"
	source             = "horus/detection"
	schema             = "detection"
)

var serviceActor = outbox.Actor{Type: "service", ID: "svc:detection"}

func findingEvent(typ string, f *domain.Finding, actor outbox.Actor, at time.Time) outbox.Event {
	tid := f.TenantID
	// Payload = C8 sin datos personales: customer null y rendered_* null (D11, events.md §5.7).
	return outbox.Event{Type: typ, Source: source, TenantID: &tid, AggregateType: "finding", AggregateID: f.ID,
		AggregateVersion: f.Version, Actor: actor, OccurredAt: at, Data: Document(f, View{})}
}

// Params implementa engine.Sink: valores por defecto + overrides del ISP.
func (s *Service) Params(ctx context.Context, tenant uuid.UUID) (domain.Params, error) {
	p := domain.DefaultParams()
	var cfg map[string]struct {
		Enabled bool
		Params  json.RawMessage
	}
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		var err error
		cfg, err = postgres.DetectorConfig(ctx, tx)
		return err
	})
	if err != nil {
		return p, err
	}
	for name, c := range cfg {
		if err := p.Merge(name, c.Params); err != nil {
			s.log.WarnContext(ctx, "invalid detector config ignored", "detector", name, "err", err)
			continue
		}
		p.SetEnabled(name, c.Enabled)
	}
	return p, nil
}

// Allowlist implementa engine.Sink.
func (s *Service) Allowlist(ctx context.Context, tenant uuid.UUID) ([]engine.AllowEntry, error) {
	var rows []postgres.AllowEntry
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		var err error
		rows, err = postgres.ListAllow(ctx, tx, 0, 10000)
		return err
	})
	out := make([]engine.AllowEntry, 0, len(rows))
	for _, r := range rows {
		e := engine.AllowEntry{Prefix: r.Prefix, Kinds: r.Kinds}
		if r.ASN != nil {
			a := uint32(*r.ASN) //nolint:gosec // CHECK 1..2^32-1
			e.ASN = &a
		}
		out = append(out, e)
	}
	return out, err
}

// State implementa engine.Sink.
func (s *Service) State(ctx context.Context, tenant uuid.UUID, key string) (string, error) {
	var v string
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		var err error
		v, err = postgres.GetState(ctx, tx, key)
		return err
	})
	return v, err
}

// SetState implementa engine.Sink.
func (s *Service) SetState(ctx context.Context, tenant uuid.UUID, key, value string) error {
	return s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		return postgres.SetState(ctx, tx, pgdb.TenantID(tenant), key, value, s.now().UTC())
	})
}

func clientPrefix(a netip.Addr) netip.Prefix {
	a = a.Unmap()
	if a.Is4() {
		return netip.PrefixFrom(a, 32)
	}
	// D22: el cliente IPv6 es el prefijo delegado (/64 por defecto; flows_raw
	// guarda la dirección base del prefijo).
	return netip.PrefixFrom(a, 64).Masked()
}

func intPtr(v uint32) *int {
	if v == 0 {
		return nil
	}
	n := int(v)
	return &n
}

func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, x := range b {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// Apply implementa engine.Sink: deduplica por (cliente, kind, objetivo
// principal); actualiza el hallazgo activo (finding.updated), respeta el
// silencio de un falso positivo, abre uno nuevo enlazado al resuelto
// (reincidente) y recalcula el estado de seguridad del cliente.
func (s *Service) Apply(ctx context.Context, tenant uuid.UUID, cands []domain.Candidate, now time.Time) (engine.ApplyResult, error) {
	var res engine.ApplyResult
	t := pgdb.TenantID(tenant)
	now = now.UTC()
	touched := map[uuid.UUID]uuid.UUID{} // cliente → nodo
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		for i := range cands {
			c := &cands[i]
			act, err := postgres.Active(ctx, tx, c.CustomerID, c.Kind, c.Target)
			if err != nil && !errors.Is(err, postgres.ErrNotFound) {
				return err
			}
			if act != nil {
				if !c.LastSeen.After(act.LastSeenAt) {
					res.Unchanged++
					continue
				}
				expect := act.Version
				mergeOccurrence(act, c, now)
				if err := postgres.Update(ctx, tx, act, expect); err != nil {
					return err
				}
				if err := outbox.Insert(ctx, tx, schema, findingEvent(EventUpdated, act, serviceActor, now)); err != nil {
					return err
				}
				res.Updated++
				touched[act.CustomerID] = act.SiteID
				continue
			}
			prev, err := postgres.LastClosed(ctx, tx, c.CustomerID, c.Kind, c.Target)
			if err != nil && !errors.Is(err, postgres.ErrNotFound) {
				return err
			}
			if prev != nil {
				if !c.LastSeen.After(prev.LastSeenAt) {
					res.Unchanged++ // misma ocurrencia que ya estaba en el hallazgo cerrado
					continue
				}
				if prev.State == domain.StateFalsePositive && prev.SilenceUntil != nil && now.Before(*prev.SilenceUntil) {
					res.Silenced++
					continue
				}
			}
			f := newFinding(tenant, c, now)
			if prev != nil && prev.State == domain.StateResolved {
				id := prev.ID
				f.PreviousFindingID = &id
				f.Reasons = append(f.Reasons, domain.Reason{Code: "recurrence", Detail: "Reincidente: el patrón vuelve tras un hallazgo resuelto el " +
					prev.ResolvedAt.UTC().Format("2006-01-02"), Weight: domain.W(0), Data: map[string]any{"previous_finding_id": prev.ID.String()}})
			}
			if err := postgres.Insert(ctx, tx, f); err != nil {
				return err
			}
			if err := outbox.Insert(ctx, tx, schema, findingEvent(EventOpened, f, serviceActor, now)); err != nil {
				return err
			}
			res.Opened++
			touched[f.CustomerID] = f.SiteID
		}
		for cust, site := range touched {
			if err := s.refreshSecurity(ctx, tx, t, cust, site, now); err != nil {
				return err
			}
		}
		return nil
	})
	return res, err
}

func newFinding(tenant uuid.UUID, c *domain.Candidate, now time.Time) *domain.Finding {
	signals := c.Signals
	if signals == nil {
		signals = []string{}
	}
	return &domain.Finding{
		ID: uuid.Must(uuid.NewV7()), TenantID: tenant, Version: 1, State: domain.StateOpen, Kind: c.Kind, Category: domain.CategorySecurity,
		Severity: c.Severity, Confidence: c.Confidence, CustomerID: c.CustomerID, RealmID: c.RealmID, SiteID: c.SiteID, RouterID: c.RouterID,
		Address: clientPrefix(c.Client), CustomerKind: kindOr(c.CustomerKind), Target: c.Target, Signals: signals, Summary: c.Summary,
		Reasons: c.Reasons, Evidence: c.Evidence, WindowFrom: c.WindowFrom.UTC(), WindowTo: c.WindowTo.UTC(), FirstSeenAt: c.FirstSeen.UTC(),
		LastSeenAt: c.LastSeen.UTC(), OpenedAt: now, UpdatedAt: now, Occurrences: 1, RuleVersion: c.RuleVersion,
		ReputationSnapshotVersion: c.ReputationVersion, MinSamplingRate: intPtr(c.MinSamplingRate), SamplingReducedConfidence: c.SamplingLow,
	}
}

func kindOr(k string) string {
	if k == "" {
		return "residential"
	}
	return k
}

// mergeOccurrence añade una ocurrencia al hallazgo activo: contador, última
// vez, razones y evidencia de la ocurrencia nueva; severidad y confianza no
// bajan mientras siga abierto.
func mergeOccurrence(f *domain.Finding, c *domain.Candidate, now time.Time) {
	f.Occurrences++
	f.LastSeenAt = c.LastSeen.UTC()
	if c.FirstSeen.Before(f.FirstSeenAt) {
		f.FirstSeenAt = c.FirstSeen.UTC()
	}
	f.WindowFrom, f.WindowTo = c.WindowFrom.UTC(), c.WindowTo.UTC()
	if domain.SeverityRank(c.Severity) > domain.SeverityRank(f.Severity) {
		f.Severity = c.Severity
	}
	f.Confidence = max(f.Confidence, c.Confidence)
	f.Signals = union(f.Signals, c.Signals)
	f.Summary, f.Reasons, f.Evidence, f.RuleVersion = c.Summary, c.Reasons, c.Evidence, c.RuleVersion
	if c.ReputationVersion != nil {
		f.ReputationSnapshotVersion = c.ReputationVersion
	}
	if c.MinSamplingRate > 0 {
		f.MinSamplingRate = intPtr(c.MinSamplingRate)
	}
	f.SamplingReducedConfidence = c.SamplingLow
	if c.RouterID != uuid.Nil {
		f.RouterID = c.RouterID
	}
	if c.CustomerKind != "" {
		f.CustomerKind = c.CustomerKind
	}
	f.Version++
	f.UpdatedAt = now
}

// refreshSecurity recalcula el estado de seguridad del cliente y emite
// security_state_changed si cambia (D5, D18).
func (s *Service) refreshSecurity(ctx context.Context, tx pgx.Tx, t pgdb.TenantID, customer, site uuid.UUID, now time.Time) error {
	active, err := postgres.ActiveOfCustomer(ctx, tx, customer)
	if err != nil {
		return err
	}
	last, err := postgres.LastResolvedAt(ctx, tx, customer)
	if err != nil {
		return err
	}
	st := domain.DeriveSecurityState(active, last, now)
	cur, err := postgres.GetSecurity(ctx, tx, customer)
	if err != nil && !errors.Is(err, postgres.ErrNotFound) {
		return err
	}
	prev := domain.SecurityClean
	row := postgres.SecurityRow{CustomerID: customer, SiteID: site, State: st.State, Version: 1, Since: now}
	if cur != nil {
		prev = cur.State
		row.Version, row.Since = cur.Version, cur.Since
		if cur.State != st.State {
			row.Version, row.Since = cur.Version+1, now
		}
	} else if st.State == domain.SecurityClean {
		return nil
	}
	if err := postgres.PutSecurity(ctx, tx, t, row, st, now); err != nil {
		return fmt.Errorf("security state: %w", err)
	}
	if cur != nil && cur.State == st.State {
		return nil
	}
	tid := t.UUID()
	siteStr := site.String()
	return outbox.Insert(ctx, tx, schema, outbox.Event{Type: EventSecurityState, Source: source, TenantID: &tid,
		AggregateType: "customer_security", AggregateID: customer, AggregateVersion: row.Version, Actor: serviceActor, OccurredAt: now,
		Data: map[string]any{"customer_id": customer, "version": row.Version, "previous_state": prev, "state": st.State,
			"open_findings": st.OpenFindings, "top_kind": st.TopKind, "max_severity": st.MaxSeverity, "since": ts(now),
			"model_version": domain.SecurityModel, "site_id": &siteStr}})
}

// Expire implementa engine.Sink: cierra (auto_expired) los hallazgos sin
// ocurrencias en idle.
func (s *Service) Expire(ctx context.Context, tenant uuid.UUID, idle time.Duration, now time.Time) (int, error) {
	t := pgdb.TenantID(tenant)
	n := 0
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		list, err := postgres.Idle(ctx, tx, now.Add(-idle))
		if err != nil {
			return err
		}
		touched := map[uuid.UUID]uuid.UUID{}
		for i := range list {
			f := &list[i]
			expect := f.Version
			if err := f.AutoExpire(now.UTC()); err != nil {
				continue
			}
			if err := postgres.Update(ctx, tx, f, expect); err != nil {
				return err
			}
			if err := outbox.Insert(ctx, tx, schema, findingEvent(EventResolved, f, serviceActor, now.UTC())); err != nil {
				return err
			}
			touched[f.CustomerID] = f.SiteID
			n++
		}
		for c, site := range touched {
			if err := s.refreshSecurity(ctx, tx, t, c, site, now.UTC()); err != nil {
				return err
			}
		}
		return nil
	})
	return n, err
}
