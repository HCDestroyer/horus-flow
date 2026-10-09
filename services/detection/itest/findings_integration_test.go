//go:build integration

package itest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/tenanttest"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/app"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

type lifecycle struct {
	t      *testing.T
	db     *pgdb.DB
	svc    *app.Service
	tenant uuid.UUID
	now    time.Time
	cust   uuid.UUID
	site   uuid.UUID
}

func newLifecycle(t *testing.T) *lifecycle {
	db := openPG(t)
	l := &lifecycle{t: t, db: db, tenant: uuid.New(), now: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC), cust: uuid.New(), site: uuid.New()}
	l.svc = app.NewService(db, app.Options{Now: func() time.Time { return l.now }})
	return l
}

func (l *lifecycle) cand(last time.Time) domain.Candidate {
	return domain.Candidate{Kind: domain.KindScanning, Severity: domain.SeverityHigh, Confidence: 0.9, RuleVersion: "outbound-scan@1",
		RealmID: uuid.New(), SiteID: l.site, RouterID: uuid.New(), Client: netip.MustParseAddr("10.20.0.41"), CustomerID: l.cust,
		CustomerKind: "residential", Target: domain.Target{Type: domain.TargetRemotePort, Value: "23"}, Signals: []string{domain.SignalScanning},
		Summary: domain.Summary{Code: "outbound_scanning_port", Text: "Escaneo del puerto 23"}, Evidence: map[string]any{"destination_ports": []int{23}},
		Reasons:    []domain.Reason{{Code: "syn_only_ratio_high", Detail: "x", Data: map[string]any{"syn_ratio": 0.96}}},
		WindowFrom: last.Add(-5 * time.Minute), WindowTo: last, FirstSeen: last.Add(-4 * time.Minute), LastSeen: last}
}

func (l *lifecycle) apply(c domain.Candidate) {
	l.t.Helper()
	if _, err := l.svc.Apply(context.Background(), l.tenant, []domain.Candidate{c}, l.now); err != nil {
		l.t.Fatal(err)
	}
}

func (l *lifecycle) ctx(perms ...string) context.Context {
	p := &authz.Principal{Type: authz.TypeUser, Subject: "u", UserID: uuid.New(), Scope: authz.ScopeTenant, TenantID: l.tenant, Perms: map[string][]string{}}
	for _, x := range perms {
		p.Perms[x] = []string{authz.ScopeAll}
	}
	return authz.WithPrincipal(context.Background(), p)
}

func (l *lifecycle) outbox() map[string]int {
	l.t.Helper()
	out := map[string]int{}
	err := l.db.PlatformTx(context.Background(), func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT payload->>'type', count(*) FROM detection.outbox GROUP BY 1`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				return err
			}
			out[k] = n
		}
		return rows.Err()
	})
	if err != nil {
		l.t.Fatal(err)
	}
	return out
}

func status(err error) int {
	var e *apperr.Error
	if errors.As(err, &e) {
		return e.Status()
	}
	return 0
}

// I1-12 criterios 1–3 y estado de seguridad (D18).
func TestFindingLifecycle(t *testing.T) {
	l := newLifecycle(t)
	t0 := l.now.Add(-10 * time.Minute)
	l.apply(l.cand(t0))
	l.apply(l.cand(t0))                       // misma ocurrencia: sin cambios
	l.apply(l.cand(t0.Add(5 * time.Minute))) // nueva ocurrencia: updated
	fs := findingsOf(t, l.db, l.tenant)
	if len(fs) != 1 || fs[0].Occurrences != 2 || fs[0].Version != 2 || !fs[0].LastSeenAt.Equal(t0.Add(5*time.Minute)) {
		t.Fatalf("deduplicación: %+v", fs)
	}
	ob := l.outbox()
	if ob[app.EventOpened] != 1 || ob[app.EventUpdated] != 1 || ob[app.EventSecurityState] != 1 {
		t.Fatalf("eventos: %v", ob)
	}
	// Payload del evento sin datos personales (customer null, rendered_* null).
	var payload []byte
	_ = l.db.PlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT payload::text FROM detection.outbox WHERE payload->>'type' = $1`, app.EventOpened).Scan(&payload)
	})
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(payload, &env); err != nil || env.Data["customer"] != nil || env.Data["recommended_actions"] == nil {
		t.Fatalf("payload: %s", payload)
	}
	for _, a := range env.Data["recommended_actions"].([]any) {
		if r, _ := a.(map[string]any)["routeros"].(map[string]any); r != nil && r["rendered_commands"] != nil {
			t.Fatalf("rendered_commands en el evento: %v", r)
		}
	}
	var state string
	_ = l.db.TenantTx(context.Background(), pgdb.TenantID(l.tenant), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT state FROM detection.customer_security WHERE customer_id = $1`, l.cust).Scan(&state)
	})
	if state != domain.SecurityInfected {
		t.Fatalf("estado de seguridad = %s, quiero infected (alta + confianza alta)", state)
	}

	id := fs[0].ID
	mgr := l.ctx(app.PermRead, app.PermManage, app.PermCustomers)
	// Criterio 2: sin comentario → 422; versión vieja → 412; FP con comentario.
	if _, _, err := l.svc.Transition(mgr, id, 2, "false_positive", app.Transition{}); status(err) != http.StatusUnprocessableEntity {
		t.Fatalf("FP sin comentario: %v", err)
	}
	if _, _, err := l.svc.Transition(mgr, id, 1, "acknowledge", app.Transition{}); status(err) != http.StatusPreconditionFailed {
		t.Fatalf("If-Match viejo: %v", err)
	}
	doc, ver, err := l.svc.Transition(mgr, id, 2, "acknowledge", app.Transition{})
	if err != nil || doc["state"] != domain.StateAcknowledged || ver != 3 || doc["customer"] == nil {
		t.Fatalf("ack: %v %v", err, doc)
	}
	if _, _, err := l.svc.Transition(mgr, id, 3, "acknowledge", app.Transition{}); status(err) != http.StatusConflict {
		t.Fatalf("doble ack: %v", err)
	}
	c := "cámara del cliente en pruebas de seguridad"
	if _, _, err := l.svc.Transition(mgr, id, 3, "false_positive", app.Transition{Comment: &c}); err != nil {
		t.Fatal(err)
	}
	l.now = l.now.Add(time.Hour)
	l.apply(l.cand(t0.Add(30 * time.Minute))) // el patrón se repite en el silencio
	if fs := findingsOf(t, l.db, l.tenant); len(fs) != 1 || fs[0].State != domain.StateFalsePositive {
		t.Fatalf("FP reabierto en el silencio: %+v", fs)
	}
	var verdicts int
	_ = l.db.TenantTx(context.Background(), pgdb.TenantID(l.tenant), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM detection.verdict_feedback WHERE verdict = 'false_positive'`).Scan(&verdicts)
	})
	if verdicts != 1 {
		t.Fatalf("verdict_feedback = %d", verdicts)
	}

	// Criterio 3: resuelto + vuelve el patrón → nuevo hallazgo enlazado (reincidente).
	other := l.cand(t0)
	other.Target = domain.Target{Type: domain.TargetRemotePort, Value: "2323"}
	l.apply(other)
	fs = findingsOf(t, l.db, l.tenant)
	second := fs[len(fs)-1]
	if _, _, err := l.svc.Transition(mgr, second.ID, second.Version, "resolve", app.Transition{ActionsTaken: []string{"contact_customer"}}); err != nil {
		t.Fatal(err)
	}
	again := other
	again.LastSeen = l.now.Add(-time.Minute)
	l.apply(again)
	fs = findingsOf(t, l.db, l.tenant)
	last := fs[len(fs)-1]
	if len(fs) != 3 || last.PreviousFindingID == nil || *last.PreviousFindingID != second.ID || last.State != domain.StateOpen {
		t.Fatalf("reincidente: %+v", last)
	}

	// Sin customers.read: sin bloque customer ni comandos renderizados.
	doc, _, err = l.svc.Get(l.ctx(app.PermRead), last.ID)
	if err != nil || doc["customer"] != nil {
		t.Fatalf("vista sin customers.read: %v %v", err, doc["customer"])
	}
	// Listado con filtros y resumen.
	page, err := l.svc.List(l.ctx(app.PermRead), url.Values{"state": {"open"}}, nil)
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("lista open: %v %d", err, len(page.Data))
	}
	if _, err := l.svc.List(l.ctx(app.PermRead), url.Values{"state": {"nope"}}, nil); status(err) != http.StatusBadRequest {
		t.Fatalf("filtro inválido: %v", err)
	}
	sum, err := l.svc.Summary(l.ctx(app.PermRead), url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	if sum["affected_customers"] != 1 || sum["by_kind"].(map[string]int)[domain.KindScanning] != 1 ||
		sum["by_signal"].(map[string]int)[domain.SignalScanning] != 1 || len(sum["by_site"].([]map[string]any)) != 1 {
		t.Fatalf("summary: %v", sum)
	}
	// Auto-expiración tras 7 días sin ocurrencias.
	l.now = l.now.Add(8 * 24 * time.Hour)
	if n, err := l.svc.Expire(context.Background(), l.tenant, 7*24*time.Hour, l.now); err != nil || n != 1 {
		t.Fatalf("expire: %d %v", n, err)
	}
}

// RLS fail-closed en todas las tablas de detection (docs/database.md §1.2).
func TestDetectionRLS(t *testing.T) {
	l := newLifecycle(t)
	l.apply(l.cand(l.now.Add(-time.Minute)))
	tenanttest.AssertRLS(t, l.db.Pool, []string{"detection"}, []string{"detection.outbox", "detection.tenant_registry"},
		[]string{"detection.outbox"})
	var n int
	if err := l.db.AppTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM detection.finding`).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("sin tenant: %d filas, %v", n, err)
	}
	if err := l.db.TenantTx(context.Background(), pgdb.TenantID(uuid.New()), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM detection.finding`).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("otro tenant: %d filas, %v", n, err)
	}
}
