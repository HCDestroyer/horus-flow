//go:build integration

package itest

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/actions"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/engine"
)

// clientKey formatea la dirección del hallazgo como la clave de cliente del
// simulador (IPv4 o prefijo IPv6).
func clientKey(f domain.Finding) string {
	a := f.Address.Addr().Unmap()
	if a.Is4() {
		return a.String()
	}
	return f.Address.String()
}

type scenarioRun struct {
	w   *world
	exp simExpected
	s   time.Time // inicio de los datos trasladados
}

// runScenario reproduce el fixture IPFIX de un escenario del simulador por
// el pipeline real y evalúa el motor a evalAfter del inicio de los datos.
func runScenario(t *testing.T, name string, back time.Duration, withIngestRep, withEngineRep bool) *scenarioRun {
	t.Helper()
	base := repo("tools", "flowsim", "fixtures", "sim", name)
	exp := loadExpected(t, base+"/ipfix.expected.json")
	d, tenant, site, router := simInventory(exp)
	// Datos trasladados a las 12:00 UTC de hoy (menos back): fuera de la franja
	// nocturna de copias de seguridad y dentro del TTL de 7 días de flows_raw.
	noon := time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)
	s := noon.Add(-back)
	shiftBy := s.Sub(exp.Start)
	snap := scenarioSnapshot(t, exp, time.Now().Add(-24*time.Hour))
	var ing, eng = snap, snap
	if !withIngestRep {
		ing = nil
	}
	if !withEngineRep {
		eng = nil
	}
	w := newWorld(t, d, tenant, site, router, ing, eng)
	ex := exp.Exporters[0]
	w.replay(base+"/ipfix.hfsim.gz", shiftBy, ex.Totals.DataRecords-ex.ByStatus["tunnel"]-ex.ByStatus["excluded"])
	return &scenarioRun{w: w, exp: exp, s: s}
}

// checkExpected compara los hallazgos con los de expected.json (exactamente
// los mismos: cliente, kind y severidad) y las invariantes de C8/D11.
func checkExpected(t *testing.T, r *scenarioRun) []domain.Finding {
	t.Helper()
	got := r.w.findings()
	var gotKeys, wantKeys []string
	for _, f := range got {
		gotKeys = append(gotKeys, fmt.Sprintf("%s|%s|%s", clientKey(f), f.Kind, f.Severity))
	}
	for _, f := range r.exp.Findings {
		wantKeys = append(wantKeys, fmt.Sprintf("%s|%s|%s", f.Client, f.Kind, f.Severity))
	}
	slices.Sort(gotKeys)
	slices.Sort(wantKeys)
	if !slices.Equal(gotKeys, wantKeys) {
		for _, f := range got {
			t.Logf("hallazgo: %s %s %s %v", clientKey(f), f.Kind, f.Summary.Text, f.Reasons)
		}
		t.Fatalf("%s: hallazgos\n got  %v\n want %v", r.exp.Scenario, gotKeys, wantKeys)
	}
	for _, f := range got {
		withData := 0
		for _, rs := range f.Reasons {
			if len(rs.Data) > 0 {
				withData++
			}
		}
		if withData < 2 {
			t.Errorf("%s: %s con %d razones con dato (≥ 2): %+v", r.exp.Scenario, f.Kind, withData, f.Reasons)
		}
		if f.Summary.Text == "" || strings.Contains(f.Summary.Text, clientKey(f)) {
			t.Errorf("%s: resumen vacío o con la IP del cliente: %q", r.exp.Scenario, f.Summary.Text)
		}
		if f.SiteID != r.w.site || f.RouterID != r.w.router || f.CustomerID == uuid.Nil {
			t.Errorf("%s: nodo/router/cliente: %v %v %v", r.exp.Scenario, f.SiteID, f.RouterID, f.CustomerID)
		}
		if err := actions.Check(actions.Build(&f)); err != nil {
			t.Errorf("%s: acciones: %v", r.exp.Scenario, err)
		}
		t.Logf("%s: %s %s %s conf=%.2f (%s) — %s", r.exp.Scenario, clientKey(f), f.Kind, f.Severity, f.Confidence, f.ConfidenceLevel(), f.Summary.Text)
	}
	return got
}

// I1-11 criterio 1 (scan): escaneo horizontal Mirai (high) y vertical (medium).
func TestScenarioScan(t *testing.T) {
	r := runScenario(t, "scan", 0, false, false)
	r.w.evaluate(r.s.Add(7 * time.Minute))
	got := checkExpected(t, r)
	for _, f := range got {
		if f.Severity == domain.SeverityHigh {
			if f.Target.Type != domain.TargetRemotePort || (f.Target.Value != "23" && f.Target.Value != "2323") ||
				!slices.Contains(f.Signals, domain.SignalWatchPorts) || !slices.Contains(f.Signals, domain.SignalFanout) {
				t.Errorf("horizontal: %+v %v", f.Target, f.Signals)
			}
		} else if f.Target.Type != domain.TargetRemoteIP || f.Target.Value != "198.51.100.200" {
			t.Errorf("vertical: %+v", f.Target)
		}
	}
	// I1-12 criterio 1: el mismo patrón otra vez (misma ventana) no duplica ni actualiza.
	rep := r.w.evaluate(r.s.Add(7 * time.Minute))
	if rep.Applied.Opened != 0 || rep.Applied.Updated != 0 || len(r.w.findings()) != len(got) {
		t.Fatalf("reevaluación: %+v", rep.Applied)
	}
}

// I1-10 criterio 1: C2 activo (high, con respuesta) y C2 caído (medium, solo SYN).
func TestScenarioC2(t *testing.T) {
	r := runScenario(t, "c2", 0, true, true)
	r.w.evaluate(r.s.Add(7 * time.Minute))
	got := checkExpected(t, r)
	for _, f := range got {
		codes := map[string]map[string]any{}
		for _, rs := range f.Reasons {
			codes[rs.Code] = rs.Data
		}
		lst := codes["reputation_listed"]
		if lst == nil || lst["feed"] != "flowsim-test-feed" || lst["listed_at"] == nil || lst["remote_ip"] != f.Target.Value || codes["c2_connections"]["connections"] == nil {
			t.Errorf("razones C2 (IP remota, feed, fecha de inclusión, nº de conexiones): %+v", f.Reasons)
		}
		if (f.Severity == domain.SeverityHigh) != (codes["c2_responded"] != nil) {
			t.Errorf("severidad %s sin la razón de respuesta coherente: %+v", f.Severity, f.Reasons)
		}
	}
}

// I1-10 criterio 2: el indicador entra en el feed después del tráfico (hace 3
// días; la ingesta no lo marcó) y el barrido retroactivo abre el hallazgo
// con la ventana real.
func TestC2RetroactiveSweep(t *testing.T) {
	r := runScenario(t, "c2", 72*time.Hour, false, true)
	r.w.evaluate(time.Now())
	got := checkExpected(t, r)
	for _, f := range got {
		if f.WindowFrom.Before(r.s.Add(-time.Minute)) || f.WindowFrom.After(r.s.Add(3*time.Minute)) || f.LastSeenAt.After(r.s.Add(10*time.Minute)) {
			t.Errorf("ventana real: %s–%s, datos desde %s", f.WindowFrom, f.WindowTo, r.s)
		}
		retro := false
		for _, rs := range f.Reasons {
			retro = retro || rs.Code == "retroactive_sweep"
		}
		if !retro {
			t.Errorf("falta la razón retroactive_sweep: %+v", f.Reasons)
		}
	}
}

// I1-10 criterio 4: un prefijo en la allowlist del ISP no genera hallazgo.
func TestC2Allowlist(t *testing.T) {
	r := runScenario(t, "c2", 0, true, true)
	pf := netip.MustParsePrefix("203.0.113.64/28") // cubre los dos C2 del escenario
	err := r.w.pg.TenantTx(context.Background(), pgdb.TenantID(r.w.tenant), func(tx pgx.Tx) error {
		return postgres.InsertAllow(context.Background(), tx, postgres.AllowEntry{ID: uuid.New(), TenantID: r.w.tenant, Prefix: &pf,
			Kinds: []string{}, Reason: "servidores propios", CreatedAt: time.Now(), CreatedBy: uuid.New()})
	})
	if err != nil {
		t.Fatal(err)
	}
	rep := r.w.evaluate(r.s.Add(7 * time.Minute))
	if n := len(r.w.findings()); n != 0 || rep.Skipped["allowlisted"] != 2 {
		t.Fatalf("allowlist: %d hallazgos, %v", n, rep.Skipped)
	}
}

func TestScenarioFanout(t *testing.T) {
	r := runScenario(t, "fanout", 0, false, false)
	r.w.evaluate(r.s.Add(7 * time.Minute))
	checkExpected(t, r)
}

func TestScenarioSpam(t *testing.T) {
	r := runScenario(t, "spam", 0, false, false)
	r.w.evaluate(r.s.Add(7 * time.Minute))
	got := checkExpected(t, r)
	if len(got) == 1 && got[0].Target.Value != "25" {
		t.Errorf("spam target: %+v", got[0].Target)
	}
}

func TestScenarioDDoS(t *testing.T) {
	r := runScenario(t, "dos_out", 0, false, false)
	r.w.evaluate(r.s.Add(7 * time.Minute))
	got := checkExpected(t, r)
	if len(got) == 1 && got[0].Target.Value != "198.51.100.50" {
		t.Errorf("ddos target: %+v", got[0].Target)
	}
}

// I1-11 criterio 2 (commercial) e I1-10 criterio 3 (normal): ningún hallazgo.
func TestScenarioCommercialAndNormal(t *testing.T) {
	for _, name := range []string{"commercial", "normal"} {
		t.Run(name, func(t *testing.T) {
			r := runScenario(t, name, 0, false, true)
			// Todas las ventanas: 5 min, la hora de SMTP y la de subida sostenida.
			r.w.evaluate(r.s.Add(7 * time.Minute))
			r.w.evaluate(r.s.Add(40 * time.Minute))
			checkExpected(t, r)
		})
	}
}

// I1-30 criterio 1: beacon cada 5 min ± 5 % durante 6,5 h.
func TestScenarioBeacon(t *testing.T) {
	r := runScenario(t, "beacon", 0, false, false)
	r.w.evaluate(r.s.Add(time.Duration(r.exp.Duration)*time.Second + 3*time.Minute))
	got := checkExpected(t, r)
	if len(got) == 1 {
		if iv, _ := got[0].Evidence["interval_seconds"].(float64); iv < 280 || iv > 320 {
			t.Errorf("intervalo: %v", got[0].Evidence)
		}
		if cv, _ := got[0].Evidence["interval_cv"].(float64); cv >= 0.2 {
			t.Errorf("cv: %v", got[0].Evidence)
		}
	}
}

// I1-30: subida sostenida de 12 Mbit/s durante 34 min (open_proxy_abuse).
func TestScenarioSustained(t *testing.T) {
	r := runScenario(t, "sustained_out", 0, false, false)
	r.w.evaluate(r.s.Add(time.Duration(r.exp.Duration)*time.Second + 3*time.Minute))
	checkExpected(t, r)
}

// Captura real del PO (anonimizada): el escaneo vertical entre dos hosts
// internos (285 puertos) es tráfico `internal` y no genera hallazgos.
func TestRealCaptureInternalScanIsIgnored(t *testing.T) {
	exp := loadExpected(t, repo("tests", "fixtures", "mikrotik-real", "ipfix-nat-20s.expected.json"))
	ex := exp.Exporters[0]
	tenant, site, router, realm := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	d := flowinv.Data{
		Exporters: []flowinv.Exporter{{TenantID: tenant, RouterID: router, SiteID: site, Name: "po-router", TunnelIP: netip.MustParseAddr(ex.ExporterIP)}},
		Realms:    []flowinv.Realm{{ID: realm, TenantID: tenant, Kind: flowinv.RealmNodePrivate, SiteID: site}},
	}
	for _, p := range ex.Prefixes.Customers {
		d.Prefixes = append(d.Prefixes, flowinv.ClientPrefix{ID: uuid.New(), TenantID: tenant, SiteID: site, RealmID: realm,
			Prefix: netip.MustParsePrefix(p), Role: flowinv.RoleCustomers})
	}
	for _, p := range ex.Prefixes.Infrastructure {
		d.Prefixes = append(d.Prefixes, flowinv.ClientPrefix{ID: uuid.New(), TenantID: tenant, SiteID: site, RealmID: realm,
			Prefix: netip.MustParsePrefix(p), Role: flowinv.RoleInfrastructure})
	}
	w := newWorld(t, d, tenant, site, router, nil, nil)
	w.replay(repo("tests", "fixtures", "mikrotik-real", "ipfix-nat-20s.pcapng"), 0, ex.Totals.DataRecords-ex.ByStatus["tunnel"])
	var internal uint64
	if err := w.ch.QueryRow(`SELECT count() FROM flows.flows_raw WHERE tenant_id = ? AND direction = 'internal'`, tenant).Scan(&internal); err != nil {
		t.Fatal(err)
	}
	if internal == 0 {
		t.Fatal("la captura debería tener tráfico internal")
	}
	w.evaluate(exp.Start.Add(time.Duration(exp.Duration)*time.Second + 4*time.Minute))
	if got := w.findings(); len(got) != 0 {
		for _, f := range got {
			t.Logf("%s %s %s", clientKey(f), f.Kind, f.Summary.Text)
		}
		t.Fatalf("la captura real produjo %d hallazgos", len(got))
	}
}

// Aislamiento entre ISP en ClickHouse: el motor de otro tenant no ve los
// datos del escenario (row policy por SQL_horus_tenant + filtro tenant_id).
func TestTenantIsolationInClickHouse(t *testing.T) {
	r := runScenario(t, "scan", 0, false, false)
	other := uuid.New()
	rep, err := r.w.engine.Evaluate(context.Background(), other, r.s.Add(7*time.Minute), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Candidates) != 0 || len(findingsOf(t, r.w.pg, other)) != 0 {
		t.Fatalf("el tenant %s ve candidatos de otro ISP: %d", other, len(rep.Candidates))
	}
	rows, err := r.w.reader.Security(context.Background(), other, r.s.Add(-time.Hour), r.s.Add(time.Hour), engine.Having{})
	if err != nil || len(rows) != 0 {
		t.Fatalf("lectura cruzada: %d filas, %v", len(rows), err)
	}
}
