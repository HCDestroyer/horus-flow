//go:build integration

package itest

import (
	"context"
	"encoding/json"
	"net/netip"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/app"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/engine"
)

const gb = 1_000_000_000

// fakeScoring es una fuente de scoring en memoria (sin ClickHouse).
type fakeScoring struct {
	rows    []app.ScoringRow
	custs   map[engine.ClientKey]app.ScoringCustomer
	peers   map[engine.ClientKey]uint64
	written []app.ScoreRecord
}

func (f *fakeScoring) ScoringFeatures(context.Context, uuid.UUID, time.Time, time.Time, string, domain.ScoringParams) ([]app.ScoringRow, error) {
	return f.rows, nil
}

func (f *fakeScoring) InboundPeers(_ context.Context, _ uuid.UUID, _, _ time.Time, keys []engine.ClientKey) (map[engine.ClientKey]uint64, error) {
	out := map[engine.ClientKey]uint64{}
	for _, k := range keys {
		out[k] = f.peers[k]
	}
	return out, nil
}

func (f *fakeScoring) ScoringCustomers(context.Context, uuid.UUID) (map[engine.ClientKey]app.ScoringCustomer, error) {
	return f.custs, nil
}

func (f *fakeScoring) Timezone(context.Context, uuid.UUID) (string, error) {
	return "America/Mexico_City", nil
}

func (f *fakeScoring) InsertScores(_ context.Context, _ uuid.UUID, rows []app.ScoreRecord) error {
	f.written = append(f.written, rows...)
	return nil
}

func (f *fakeScoring) ScoreSeries(_ context.Context, _, realm uuid.UUID, ip netip.Addr, from time.Time) ([]app.ScorePoint, error) {
	var out []app.ScorePoint
	for _, r := range f.written {
		if r.Key.Realm == realm && r.Key.IP == ip && !r.Day.Before(from.Truncate(24*time.Hour)) {
			out = append(out, app.ScorePoint{Day: r.Day, CommercialScore: r.CommercialScore, Confidence: r.Confidence, Level: r.Level,
				SuggestedKind: r.SuggestedKind, KindOnDay: r.KindOnDay})
		}
	}
	return out, nil
}

type scoringWorld struct {
	*lifecycle
	src                                 *fakeScoring
	office, home, locked, dynamic, thin engine.ClientKey
	ids                                 map[engine.ClientKey]uuid.UUID
}

func newScoringWorld(t *testing.T) *scoringWorld {
	l := newLifecycle(t)
	realm := uuid.New()
	k := func(ip string) engine.ClientKey { return engine.ClientKey{Realm: realm, IP: netip.MustParseAddr(ip)} }
	w := &scoringWorld{lifecycle: l, office: k("10.20.0.10"), home: k("10.20.0.11"), locked: k("10.20.0.12"),
		dynamic: k("10.20.0.13"), thin: k("10.20.0.14"), ids: map[engine.ClientKey]uuid.UUID{}}
	officeRow := func(key engine.ClientKey) app.ScoringRow {
		return app.ScoringRow{Key: key, Site: l.site, UpBytes: 40 * gb, DownBytes: 50 * gb, BusinessBytes: 70 * gb, ActiveHours: 166,
			InboundPorts: 4, RemoteASNs: 320}
	}
	w.src = &fakeScoring{custs: map[engine.ClientKey]app.ScoringCustomer{}, peers: map[engine.ClientKey]uint64{}}
	w.src.rows = append(w.src.rows, officeRow(w.office), officeRow(w.locked), officeRow(w.dynamic),
		app.ScoringRow{Key: w.thin, Site: l.site, UpBytes: 40 * gb, DownBytes: 50 * gb, ActiveHours: 20, InboundPorts: 4})
	// Residenciales: dan la mediana de subida (≥ 10).
	for i := 0; i < 12; i++ {
		key := engine.ClientKey{Realm: realm, IP: netip.AddrFrom4([4]byte{10, 20, 1, byte(i + 1)})}
		if i == 0 {
			w.home = key
		}
		w.src.rows = append(w.src.rows, app.ScoringRow{Key: key, Site: l.site, UpBytes: 4 * gb, DownBytes: 60 * gb, BusinessBytes: 20 * gb,
			ActiveHours: 110, RemoteASNs: 40})
	}
	for _, r := range w.src.rows {
		id := uuid.New()
		w.ids[r.Key] = id
		c := app.ScoringCustomer{ID: id, Kind: "residential", KindSource: "default", Site: l.site}
		if r.Key == w.locked {
			c.KindSource = "manual"
		}
		w.src.custs[r.Key] = c
		w.src.peers[r.Key] = 240
	}
	l.svc.SetScoring(w.src, func(_, _ uuid.UUID, ip netip.Addr) bool { return ip == w.dynamic.IP })
	return w
}

func (w *scoringWorld) run(day int) app.ScoringReport {
	w.t.Helper()
	w.now = time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC).AddDate(0, 0, day)
	rep, err := w.svc.RunScoring(context.Background(), w.tenant, w.now, false)
	if err != nil {
		w.t.Fatal(err)
	}
	return rep
}

func (w *scoringWorld) events(typ string) []map[string]any {
	w.t.Helper()
	var out []map[string]any
	err := w.db.PlatformTx(context.Background(), func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT payload->'data' FROM detection.outbox WHERE payload->>'type' = $1 ORDER BY seq`, typ)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b []byte
			if err := rows.Scan(&b); err != nil {
				return err
			}
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return out
}

func TestScoringHysteresisAndEvents(t *testing.T) {
	w := newScoringWorld(t)
	rep := w.run(0)
	if rep.Scored != 15 || rep.Insufficient != 1 {
		t.Fatalf("día 0: %+v; want 15 puntuados y 1 sin horas suficientes", rep)
	}
	if got := w.events(app.EventKindSuggested); len(got) != 0 {
		t.Fatalf("la sugerencia nueva espera la histéresis: %v", got)
	}
	if got := w.events(app.EventScoreChanged); len(got) != 15 {
		t.Fatalf("primer scoring: un score.changed por cliente, got %d", len(got))
	}
	if len(w.src.written) != 15 || w.src.written[0].Model != domain.ScoringModel {
		t.Fatalf("serie diaria: %d filas", len(w.src.written))
	}
	// El mismo día no se repite.
	if rep := w.run(0); !rep.Skipped {
		t.Fatalf("segunda pasada del día: %+v", rep)
	}
	for d := 1; d < 7; d++ {
		w.run(d)
	}
	if got := w.events(app.EventKindSuggested); len(got) != 0 {
		t.Fatalf("antes de 7 días no hay kind_suggested: %d", len(got))
	}
	w.run(7)
	got := w.events(app.EventKindSuggested)
	if len(got) != 3 {
		t.Fatalf("día 7: kind_suggested para la oficina, la bloqueada y el pool dinámico; got %d", len(got))
	}
	byCustomer := map[string]map[string]any{}
	for _, e := range got {
		byCustomer[e["customer_id"].(string)] = e
	}
	off := byCustomer[w.ids[w.office].String()]
	if off["kind"] != "commercial" || off["previous_kind"] != "residential" || off["confidence"].(float64) != 1 ||
		off["apply_threshold"].(float64) != 80 || off["score_ref"] != "commercial-v1@2026-10-08" {
		t.Errorf("oficina: %v", off)
	}
	if reasons := off["reasons"].([]any); len(reasons) == 0 || reasons[0].(map[string]any)["code"] != domain.FeatInboundServices {
		t.Errorf("razones: %v", off["reasons"])
	}
	// El cliente con tipo manual también recibe la sugerencia (devices no lo cambia).
	if lk := byCustomer[w.ids[w.locked].String()]; lk == nil || lk["kind"] != "commercial" {
		t.Errorf("bloqueado: %v", lk)
	}
	// Pool dinámico: 100 × 0,6 = 60 → residencial con indicios.
	dyn := byCustomer[w.ids[w.dynamic].String()]
	if dyn["kind"] != "residential" || dyn["commercial_use_suspected"] != true || dyn["commercial_score"].(float64) != 60 || dyn["dynamic_pool"] != true {
		t.Errorf("dinámico: %v", dyn)
	}
	// Sin cambios: no se vuelve a publicar.
	w.run(8)
	if n := len(w.events(app.EventKindSuggested)); n != 3 {
		t.Errorf("sin cambios no se publica de nuevo: %d", n)
	}
}

func TestCustomerScoringAPI(t *testing.T) {
	w := newScoringWorld(t)
	w.run(0)
	id := w.ids[w.office]
	out, err := w.svc.CustomerScoring(w.ctx(app.PermCustomers), id, url.Values{"range": {"90d"}})
	if err != nil {
		t.Fatal(err)
	}
	latest, _ := out["latest"].(map[string]any)
	if latest == nil || latest["commercial_score"] != 100 || latest["suggested_kind"] != "commercial" || latest["pending_level"] != "commercial" {
		t.Fatalf("latest = %v", latest)
	}
	feats := latest["features"].([]domain.Feature)
	if len(feats) != len(domain.ScoringFeatures) {
		t.Errorf("features = %d", len(feats))
	}
	if series := out["series"].([]any); len(series) != 1 {
		t.Errorf("series = %v", series)
	}
	// Sin permiso customers.read: 403.
	if _, err := w.svc.CustomerScoring(w.ctx(app.PermRead), id, nil); err == nil {
		t.Error("sin customers.read debe fallar")
	}
	// Cliente conocido (con historial de seguridad) sin scoring: latest = null, sin eco del id.
	w.apply(w.cand(w.now))
	known, err := w.svc.CustomerScoring(w.ctx(app.PermCustomers), w.cust, url.Values{})
	if err != nil || known["latest"] != nil || len(known["series"].([]any)) != 0 {
		t.Errorf("sin scoring = %v, %v", known, err)
	}
	// Cliente desconocido en el tenant: 404.
	if _, err := w.svc.CustomerScoring(w.ctx(app.PermCustomers), uuid.New(), url.Values{}); !isNotFound(err) {
		t.Errorf("cliente desconocido: %v", err)
	}
	// Otro tenant no ve el scoring (RLS): 404.
	w.tenant = uuid.New()
	if _, err := w.svc.CustomerScoring(w.ctx(app.PermCustomers), id, url.Values{}); !isNotFound(err) {
		t.Errorf("otro tenant ve el scoring: %v", err)
	}
	if _, err := w.svc.CustomerScoring(w.ctx(app.PermCustomers), id, url.Values{"range": {"999d"}}); err == nil {
		t.Error("range > 400d debe fallar")
	}
}

func isNotFound(err error) bool {
	e, ok := apperr.As(err)
	return ok && e.Kind == apperr.KindNotFound
}

func TestScoringDisabledByTenant(t *testing.T) {
	w := newScoringWorld(t)
	err := w.db.TenantTx(context.Background(), pgdb.TenantID(w.tenant), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO detection.detector_config (tenant_id, detector, enabled, params)
			VALUES ($1, 'customer_scoring', false, '{}')`, w.tenant)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep := w.run(0); !rep.Skipped || rep.Scored != 0 {
		t.Fatalf("scoring desactivado: %+v", rep)
	}
}
