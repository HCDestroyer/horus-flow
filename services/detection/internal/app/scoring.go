package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/engine"
)

// Eventos del scoring (docs/events.md §8.8).
const (
	EventKindSuggested = "horus.detection.customer.kind_suggested"
	EventScoreChanged  = "horus.detection.score.changed"
	// stateScoringDay es la clave de engine_state con el último día puntuado.
	stateScoringDay = "scoring:last_day"
	// MaxScoringRange acota la serie de GET /customers/{id}/scoring.
	MaxScoringRange = 400 * 24 * time.Hour
)

// ScoringRow son las características agregadas de un cliente en la ventana
// (flows.client_security_1h).
type ScoringRow struct {
	Key           engine.ClientKey
	Site          uuid.UUID
	UpBytes       uint64
	DownBytes     uint64
	BusinessBytes uint64
	ActiveHours   int
	InboundPorts  uint64
	RemoteASNs    uint64
}

// ScoringCustomer es el cliente de dim.customer que se puntúa.
type ScoringCustomer struct {
	ID         uuid.UUID
	Kind       string
	KindSource string
	Site       uuid.UUID
}

// ScoreRecord es una fila de detection.customer_scores_1d (ClickHouse).
type ScoreRecord struct {
	CustomerID      uuid.UUID
	Key             engine.ClientKey
	Site            uuid.UUID
	Day             time.Time
	Model           string
	CommercialScore int
	RawScore        int
	Confidence      float64
	KindOnDay       string
	SuggestedKind   string
	Level           string
	DynamicPool     bool
	Reasons         []string
	ComputedAt      time.Time
}

// ScorePoint es un punto de la serie diaria de un cliente.
type ScorePoint struct {
	Day             time.Time
	CommercialScore int
	Confidence      float64
	Level           string
	SuggestedKind   string
	KindOnDay       string
}

// ScoringSource es la fuente ClickHouse del scoring (adaptador
// clickhouse.Reader): características, clientes y la serie diaria.
type ScoringSource interface {
	ScoringFeatures(ctx context.Context, tenant uuid.UUID, from, to time.Time, tz string, p domain.ScoringParams) ([]ScoringRow, error)
	InboundPeers(ctx context.Context, tenant uuid.UUID, from, to time.Time, keys []engine.ClientKey) (map[engine.ClientKey]uint64, error)
	ScoringCustomers(ctx context.Context, tenant uuid.UUID) (map[engine.ClientKey]ScoringCustomer, error)
	Timezone(ctx context.Context, tenant uuid.UUID) (string, error)
	InsertScores(ctx context.Context, tenant uuid.UUID, rows []ScoreRecord) error
	ScoreSeries(ctx context.Context, tenant, realm uuid.UUID, ip netip.Addr, from time.Time) ([]ScorePoint, error)
}

// DynamicPool indica si la IP de un cliente está en un prefijo dinámico.
type DynamicPool func(tenant, site uuid.UUID, ip netip.Addr) bool

// ErrNoScoring indica un módulo sin fuente de scoring (sin ClickHouse).
var ErrNoScoring = errors.New("detection: scoring source not configured")

// ScoringReport resume una pasada del scoring de un tenant.
type ScoringReport struct {
	Skipped      bool
	Candidates   int // clientes con tráfico en la ventana
	Scored       int
	Insufficient int // sin el mínimo de horas activas
	Unknown      int // sin cliente en dim.customer
	Published    int // kind_suggested emitidos
	ScoreChanged int // score.changed emitidos
}

// SetScoring configura la fuente del scoring y el detector de pools dinámicos.
func (s *Service) SetScoring(src ScoringSource, pool DynamicPool) { s.scoring, s.pool = src, pool }

// RunScoring puntúa a los clientes de tenant una vez al día (o siempre con
// force): calcula las características de la ventana, la probabilidad
// comercial explicada, aplica la histéresis y publica kind_suggested al
// cambiar la sugerencia y score.changed al moverse la puntuación. Los
// clientes con tipo fijado a mano también se puntúan (su sugerencia se
// muestra); quien decide no cambiarles el tipo es devices.
func (s *Service) RunScoring(ctx context.Context, tenant uuid.UUID, now time.Time, force bool) (ScoringReport, error) {
	var rep ScoringReport
	if s.scoring == nil {
		return rep, ErrNoScoring
	}
	params, err := s.Params(ctx, tenant)
	if err != nil {
		return rep, err
	}
	p := params.Scoring
	now = now.UTC()
	day := now.Truncate(24 * time.Hour)
	dayKey := day.Format(time.DateOnly)
	if !p.Enabled {
		rep.Skipped = true
		return rep, nil
	}
	if !force {
		last, err := s.State(ctx, tenant, stateScoringDay)
		if err != nil {
			return rep, err
		}
		if last == dayKey {
			rep.Skipped = true
			return rep, nil
		}
	}
	to := now.Truncate(time.Hour)
	from := to.Add(-p.Window.D())
	tz := "UTC"
	if v, err := s.scoring.Timezone(ctx, tenant); err == nil && v != "" {
		if _, lerr := time.LoadLocation(v); lerr == nil {
			tz = v
		}
	}
	rows, err := s.scoring.ScoringFeatures(ctx, tenant, from, to, tz, p)
	if err != nil {
		return rep, err
	}
	rep.Candidates = len(rows)
	custs, err := s.scoring.ScoringCustomers(ctx, tenant)
	if err != nil {
		return rep, err
	}
	var keys []engine.ClientKey
	for _, r := range rows {
		if r.InboundPorts > 0 {
			keys = append(keys, r.Key)
		}
	}
	peers := map[engine.ClientKey]uint64{}
	if len(keys) > 0 {
		if peers, err = s.scoring.InboundPeers(ctx, tenant, from, to, keys); err != nil {
			return rep, err
		}
	}
	windowHours := int(p.Window.D() / time.Hour)
	inputs := make(map[engine.ClientKey]domain.ScoringInput, len(rows))
	var residentialUp []uint64
	for _, r := range rows {
		in := domain.ScoringInput{UpBytes: r.UpBytes, DownBytes: r.DownBytes, ActiveHours: r.ActiveHours, WindowHours: windowHours,
			BusinessBytes: r.BusinessBytes, InboundPorts: r.InboundPorts, InboundPeers: peers[r.Key], RemoteASNs: r.RemoteASNs}
		inputs[r.Key] = in
		if c, ok := custs[r.Key]; ok && c.Kind != domain.KindCommercial && p.Enough(in) {
			residentialUp = append(residentialUp, r.UpBytes)
		}
	}
	median := domain.MedianUp(residentialUp, p.MinResidentialPeers)

	type scored struct {
		row   ScoringRow
		cust  ScoringCustomer
		score domain.Score
	}
	var list []scored
	for _, r := range rows {
		c, ok := custs[r.Key]
		if !ok {
			rep.Unknown++
			continue
		}
		in := inputs[r.Key]
		if !p.Enough(in) {
			rep.Insufficient++
			continue
		}
		in.ResidentialMedianUp = median
		site := r.Site
		if c.Site != uuid.Nil {
			site = c.Site
		}
		in.DynamicPool = s.pool != nil && s.pool(tenant, site, r.Key.IP)
		r.Site = site
		list = append(list, scored{row: r, cust: c, score: p.Score(in)})
	}
	rep.Scored = len(list)

	t := pgdb.TenantID(tenant)
	records := make([]ScoreRecord, 0, len(list))
	err = s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		rep.Published, rep.ScoreChanged = 0, 0
		records = records[:0]
		states, err := postgres.ScoringStates(ctx, tx)
		if err != nil {
			return err
		}
		for _, sc := range list {
			prev := states[sc.cust.ID]
			st, events, err := s.nextScoring(t, p, prev, sc.row, sc.cust, sc.score, from, to, now)
			if err != nil {
				return err
			}
			if err := postgres.PutScoring(ctx, tx, t, st); err != nil {
				return err
			}
			for _, ev := range events {
				if err := outbox.Insert(ctx, tx, schema, ev); err != nil {
					return err
				}
				if ev.Type == EventKindSuggested {
					rep.Published++
				} else {
					rep.ScoreChanged++
				}
			}
			records = append(records, ScoreRecord{CustomerID: sc.cust.ID, Key: sc.row.Key, Site: sc.row.Site, Day: day,
				Model: sc.score.Model, CommercialScore: sc.score.CommercialScore, RawScore: int(math.Round(sc.score.Raw * 100)),
				Confidence: sc.score.Confidence, KindOnDay: kindOr(sc.cust.Kind), SuggestedKind: sc.score.Kind, Level: sc.score.Level,
				DynamicPool: st.DynamicPool, Reasons: sc.score.ReasonCodes(), ComputedAt: now})
		}
		return postgres.SetState(ctx, tx, t, stateScoringDay, dayKey, now)
	})
	if err != nil {
		return rep, err
	}
	if len(records) > 0 {
		if err := s.scoring.InsertScores(ctx, tenant, records); err != nil {
			// La serie diaria es secundaria: el estado y los eventos ya están guardados.
			s.log.WarnContext(ctx, "detection: scoring series not written", "tenant", tenant.String(), "err", err)
		}
	}
	return rep, nil
}

// nextScoring calcula la fila nueva de customer_scoring y los eventos que
// provoca (kind_suggested tras la histéresis; score.changed si la puntuación
// se mueve al menos ScoreChangeDelta puntos o es la primera).
func (s *Service) nextScoring(t pgdb.TenantID, p domain.ScoringParams, prev *postgres.ScoringState, row ScoringRow,
	cust ScoringCustomer, sc domain.Score, from, to, now time.Time,
) (*postgres.ScoringState, []outbox.Event, error) {
	feats, err := json.Marshal(sc.Features)
	if err != nil {
		return nil, nil, err
	}
	reasons, err := json.Marshal(sc.Reasons)
	if err != nil {
		return nil, nil, err
	}
	st := &postgres.ScoringState{CustomerID: cust.ID, RealmID: row.Key.Realm, SiteID: row.Site, Address: clientPrefix(row.Key.IP),
		Model: sc.Model, ComputedAt: now, WindowFrom: from, WindowTo: to, CommercialScore: sc.CommercialScore,
		RawScore: int(math.Round(sc.Raw * 100)), Level: sc.Level, Confidence: sc.Confidence, DynamicPool: hasReason(sc, domain.ReasonDynamicPool),
		CustomerKind: kindOr(cust.Kind), KindLocked: cust.KindSource == "manual", Features: feats, Reasons: reasons, Version: 1, UpdatedAt: now}
	var hs domain.SuggestionState
	if prev != nil {
		st.Version = prev.Version
		st.Published, st.PublishedAt = prev.Published, prev.PublishedAt
		if prev.Published != nil {
			hs.Published = *prev.Published
		}
		if prev.Pending != nil && prev.PendingSince != nil {
			hs.Pending, hs.PendingSince = *prev.Pending, *prev.PendingSince
		}
	}
	next, publish := p.Advance(hs, sc.Level, now)
	st.Pending, st.PendingSince = nil, nil
	if next.Pending != "" {
		lvl, since := next.Pending, next.PendingSince
		st.Pending, st.PendingSince = &lvl, &since
	}
	if st.Published == nil || *st.Published != next.Published {
		lvl := next.Published
		st.Published = &lvl
	}
	tid := t.UUID()
	site := row.Site.String()
	var events []outbox.Event
	if publish {
		st.Version++
		at := now
		st.PublishedAt = &at
		previous := domain.KindOf(hs.Published)
		if hs.Published == "" {
			previous = domain.KindResidential
		}
		events = append(events, outbox.Event{Type: EventKindSuggested, Source: source, TenantID: &tid, AggregateType: "customer_scoring",
			AggregateID: cust.ID, AggregateVersion: st.Version, Actor: serviceActor, OccurredAt: now,
			Data: map[string]any{"customer_id": cust.ID, "version": st.Version, "model_version": sc.Model, "previous_kind": previous,
				"kind": sc.Kind, "level": sc.Level, "confidence": sc.Confidence, "scores": sc.Scores(), "commercial_score": sc.CommercialScore,
				"commercial_use_suspected": sc.Level == domain.LevelSuspected, "apply_threshold": p.CommercialThreshold,
				"reasons": sc.Reasons, "dynamic_pool": st.DynamicPool, "window_from": ts(from), "window_to": ts(to),
				"score_ref": scoreRef(sc.Model, now), "site_id": &site}})
	}
	if prev == nil || absInt(prev.CommercialScore-sc.CommercialScore) >= max(p.ScoreChangeDelta, 1) || prev.Level != sc.Level {
		var previous *int
		if prev != nil {
			v := prev.CommercialScore
			previous = &v
		}
		events = append(events, outbox.Event{Type: EventScoreChanged, Source: source, TenantID: &tid, AggregateType: "customer_scoring",
			AggregateID: cust.ID, AggregateVersion: st.Version, Actor: serviceActor, OccurredAt: now,
			Data: map[string]any{"customer_id": cust.ID, "model_version": sc.Model, "commercial_score": sc.CommercialScore,
				"previous_score": previous, "level": sc.Level, "confidence": sc.Confidence, "day": now.Format(time.DateOnly),
				"window_from": ts(from), "window_to": ts(to), "site_id": &site}})
	}
	return st, events, nil
}

func hasReason(s domain.Score, code string) bool {
	for _, r := range s.Reasons {
		if r.Code == code {
			return true
		}
	}
	return false
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// scoreRef es la referencia del scoring en el historial de tipo de devices
// (modelo + día de detection.customer_scores_1d).
func scoreRef(model string, at time.Time) string { return model + "@" + at.UTC().Format(time.DateOnly) }

// ---------------------------------------------------------------- API

// parseRange interpreta ?range=90d (también 12h o 2w).
func parseRange(v string) (time.Duration, error) {
	if v == "" {
		return 0, nil
	}
	unit := v[len(v)-1]
	n, err := strconv.Atoi(v[:len(v)-1])
	if err != nil || n <= 0 {
		return 0, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "range: formato <n>d, <n>w o <n>h")
	}
	var d time.Duration
	switch unit {
	case 'h':
		d = time.Duration(n) * time.Hour
	case 'd':
		d = time.Duration(n) * 24 * time.Hour
	case 'w':
		d = time.Duration(n) * 7 * 24 * time.Hour
	default:
		return 0, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "range: formato <n>d, <n>w o <n>h")
	}
	if d > MaxScoringRange {
		return 0, apperr.New(apperr.KindBadRequest, "TIME_RANGE_TOO_LARGE", "range: máximo 400d")
	}
	return d, nil
}

// CustomerScoring es GET /customers/{id}/scoring: último scoring del cliente
// con su explicación y, con ?range=, la serie diaria. Sin scoring todavía
// (o fuera del alcance de nodos del usuario) devuelve latest = null; un
// cliente desconocido en el tenant, 404 CUSTOMER_NOT_FOUND.
func (s *Service) CustomerScoring(ctx context.Context, customer uuid.UUID, qv url.Values) (map[string]any, error) {
	_, t, sites, err := s.scope(ctx, PermCustomers)
	if err != nil {
		return nil, err
	}
	rng, err := parseRange(strings.TrimSpace(qv.Get("range")))
	if err != nil {
		return nil, err
	}
	var st *postgres.ScoringState
	var params domain.Params
	err = s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		st, err = postgres.GetScoring(ctx, tx, customer)
		if !errors.Is(err, postgres.ErrNotFound) {
			return err
		}
		// Sin scoring: el cliente existe si tiene historial de seguridad o
		// devices lo proyectó en dim.customer (como /customers/{id}/findings).
		st = nil
		known, err := postgres.KnownCustomer(ctx, tx, customer)
		if err != nil {
			return err
		}
		if !known && s.flows != nil {
			known, _ = s.flows.CustomerKnown(ctx, t.UUID(), customer)
		}
		if !known {
			return apperr.NotFound("CUSTOMER_NOT_FOUND")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := map[string]any{"latest": nil, "series": []any{}}
	if st == nil || (sites != nil && !containsUUID(sites, st.SiteID)) {
		return out, nil
	}
	if params, err = s.Params(ctx, t.UUID()); err != nil {
		return nil, err
	}
	p := params.Scoring
	var feats []domain.Feature
	var reasons []domain.Reason
	if err := json.Unmarshal(st.Features, &feats); err != nil {
		return nil, fmt.Errorf("scoring features: %w", err)
	}
	if err := json.Unmarshal(st.Reasons, &reasons); err != nil {
		return nil, fmt.Errorf("scoring reasons: %w", err)
	}
	if feats == nil {
		feats = []domain.Feature{}
	}
	if reasons == nil {
		reasons = []domain.Reason{}
	}
	commercial := float64(st.CommercialScore) / 100
	var published, pending, pendingSince, publishedAt any
	if st.Published != nil {
		published = *st.Published
	}
	if st.PublishedAt != nil {
		publishedAt = ts(*st.PublishedAt)
	}
	if st.Pending != nil && st.PendingSince != nil {
		pending, pendingSince = *st.Pending, ts(*st.PendingSince)
	}
	out["latest"] = map[string]any{
		"customer_id": st.CustomerID, "model_version": st.Model, "computed_at": ts(st.ComputedAt),
		"window_from": ts(st.WindowFrom), "window_to": ts(st.WindowTo), "level": st.Level, "suggested_kind": domain.KindOf(st.Level),
		"commercial_score": st.CommercialScore, "raw_score": st.RawScore, "confidence": domain.Round2(st.Confidence),
		"scores":                   map[string]float64{"residential": domain.Round2(1 - commercial), "commercial": domain.Round2(commercial)},
		"commercial_use_suspected": st.Level == domain.LevelSuspected, "dynamic_pool": st.DynamicPool,
		"customer_kind": st.CustomerKind, "kind_locked": st.KindLocked, "published_level": published, "published_at": publishedAt,
		"pending_level": pending, "pending_since": pendingSince, "features": feats, "reasons": reasons,
		"thresholds": map[string]any{"commercial": p.CommercialThreshold, "suspected": p.SuspectedThreshold,
			"hysteresis_days": int(p.Hysteresis.D() / (24 * time.Hour))},
	}
	if rng > 0 && s.scoring != nil {
		pts, err := s.scoring.ScoreSeries(ctx, t.UUID(), st.RealmID, st.Address.Addr(), s.now().UTC().Add(-rng))
		if err != nil {
			s.log.WarnContext(ctx, "detection: scoring series unavailable", "err", err)
			out["series_unavailable"] = true
		} else {
			series := make([]any, 0, len(pts))
			for _, pt := range pts {
				series = append(series, map[string]any{"day": pt.Day.UTC().Format(time.DateOnly), "commercial_score": pt.CommercialScore,
					"confidence": domain.Round2(pt.Confidence), "level": pt.Level, "suggested_kind": pt.SuggestedKind, "kind": pt.KindOnDay})
			}
			out["series"] = series
		}
	}
	return out, nil
}

func containsUUID(list []uuid.UUID, id uuid.UUID) bool {
	for _, x := range list {
		if x == id {
			return true
		}
	}
	return false
}
