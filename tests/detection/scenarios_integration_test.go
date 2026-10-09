//go:build integration

package detection_test

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
)

type scenarioRun struct {
	w   *world
	exp simExpected
	s   time.Time // inicio de los datos trasladados
}

// runScenario reproduce el fixture IPFIX de un escenario del simulador por
// el pipeline real y evalúa el motor a evalAfter del inicio de los datos.
// clock fija el instante y la zona horaria del ISP de un escenario: los
// datos empiezan a las Hour:00 locales de Zone. La fecha es la de hace dos
// días (único dato del reloj real: flows_raw tiene TTL de 7 días respecto a
// la hora del servidor), así que el resultado no depende de la hora a la que
// se ejecute la prueba.
type clock struct {
	Zone string
	Hour int
}

// defaultClock: mediodía en Ciudad de México.
var defaultClock = clock{Zone: "America/Mexico_City", Hour: 12}

func (c clock) anchor(t *testing.T) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(c.Zone)
	if err != nil {
		t.Fatal(err)
	}
	d := time.Now().In(loc).AddDate(0, 0, -2)
	return time.Date(d.Year(), d.Month(), d.Day(), c.Hour, 0, 0, 0, loc).UTC()
}

func runScenario(t *testing.T, name string, back time.Duration, withIngestRep, withEngineRep bool) *scenarioRun {
	t.Helper()
	return runScenarioAt(t, name, defaultClock, back, withIngestRep, withEngineRep)
}

// runScenarioAt reproduce el fixture IPFIX de un escenario por el pipeline
// real con los datos trasladados al instante de c (menos back) y la zona
// horaria de c en dim.tenant (la que usa el motor para la franja de copias).
func runScenarioAt(t *testing.T, name string, c clock, back time.Duration, withIngestRep, withEngineRep bool) *scenarioRun {
	t.Helper()
	base := repo("tools", "flowsim", "fixtures", "sim", name)
	exp := loadExpected(t, base+"/ipfix.expected.json")
	d, tenant, site, router := simInventory(exp)
	s := c.anchor(t).Add(-back)
	shiftBy := s.Sub(exp.Start)
	snap := scenarioSnapshot(t, exp, s.Add(-24*time.Hour))
	var ing, eng = snap, snap
	if !withIngestRep {
		ing = nil
	}
	if !withEngineRep {
		eng = nil
	}
	w := newWorld(t, d, tenant, site, router, ing, eng)
	if _, err := w.ch.Exec(`INSERT INTO dim.tenant (tenant_id, name, timezone, version) VALUES (?, 'ISP de prueba', ?, 1)`, tenant, c.Zone); err != nil {
		t.Fatal(err)
	}
	ex := exp.Exporters[0]
	w.replay(base+"/ipfix.hfsim.gz", shiftBy, ex.Totals.DataRecords-ex.ByStatus["tunnel"]-ex.ByStatus["excluded"])
	return &scenarioRun{w: w, exp: exp, s: s}
}

// checkExpected compara los hallazgos con los de expected.json (exactamente
// los mismos: cliente, kind y severidad) y las invariantes de C8.
func checkExpected(t *testing.T, r *scenarioRun) []finding {
	t.Helper()
	got := r.w.findings()
	var gotKeys, wantKeys []string
	for _, f := range got {
		gotKeys = append(gotKeys, f.key())
	}
	for _, f := range r.exp.Findings {
		wantKeys = append(wantKeys, f.Client+"|"+f.Kind+"|"+f.Severity)
	}
	slices.Sort(gotKeys)
	slices.Sort(wantKeys)
	if !slices.Equal(gotKeys, wantKeys) {
		for _, f := range got {
			t.Logf("hallazgo: %s %s %v", f.key(), f.Summary, f.Reasons)
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
		if f.Summary == "" || strings.Contains(f.Summary, f.Client) {
			t.Errorf("%s: resumen vacío o con la IP del cliente: %q", r.exp.Scenario, f.Summary)
		}
		if f.Site != r.w.site || f.Router != r.w.router || f.Customer == uuid.Nil {
			t.Errorf("%s: nodo/router/cliente: %v %v %v", r.exp.Scenario, f.Site, f.Router, f.Customer)
		}
		t.Logf("%s: %s conf=%.2f — %s", r.exp.Scenario, f.key(), f.Confidence, f.Summary)
	}
	return got
}

// I1-11 criterio 1 (scan): escaneo horizontal Mirai (high) y vertical (medium).
func TestScenarioScan(t *testing.T) {
	r := runScenario(t, "scan", 0, false, false)
	r.w.evaluate(r.s.Add(7 * time.Minute))
	got := checkExpected(t, r)
	for _, f := range got {
		if f.Severity == "high" {
			if f.TargetType != "remote_port" || (f.TargetValue != "23" && f.TargetValue != "2323") ||
				!slices.Contains(f.Signals, "watched_ports") || !slices.Contains(f.Signals, "fan_out") {
				t.Errorf("horizontal: %s %s %v", f.TargetType, f.TargetValue, f.Signals)
			}
		} else if f.TargetType != "remote_ip" || f.TargetValue != "198.51.100.200" {
			t.Errorf("vertical: %s %s", f.TargetType, f.TargetValue)
		}
	}
	// I1-12 criterio 1: el mismo patrón otra vez (misma ventana) no duplica ni actualiza.
	rep := r.w.evaluate(r.s.Add(7 * time.Minute))
	if rep.Opened != 0 || rep.Updated != 0 || len(r.w.findings()) != len(got) {
		t.Fatalf("reevaluación: %+v", rep)
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
		if lst == nil || lst["feed"] != "flowsim-test-feed" || lst["listed_at"] == nil || lst["remote_ip"] != f.TargetValue || codes["c2_connections"]["connections"] == nil {
			t.Errorf("razones C2 (IP remota, feed, fecha de inclusión, nº de conexiones): %+v", f.Reasons)
		}
		if (f.Severity == "high") != (codes["c2_responded"] != nil) {
			t.Errorf("severidad %s sin la razón de respuesta coherente: %+v", f.Severity, f.Reasons)
		}
	}
}

// I1-10 criterio 2: el indicador entra en el feed después del tráfico (hace 3
// días; la ingesta no lo marcó) y el barrido retroactivo abre el hallazgo
// con la ventana real.
func TestC2RetroactiveSweep(t *testing.T) {
	r := runScenario(t, "c2", 72*time.Hour, false, true)
	r.w.evaluate(defaultClock.anchor(t)) // el indicador entra hoy; el tráfico es de hace 3 días
	got := checkExpected(t, r)
	for _, f := range got {
		if f.WindowFrom.Before(r.s.Add(-time.Minute)) || f.WindowFrom.After(r.s.Add(3*time.Minute)) || f.LastSeen.After(r.s.Add(10*time.Minute)) {
			t.Errorf("ventana real: %s–%s, datos desde %s", f.WindowFrom, f.LastSeen, r.s)
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
	if _, err := r.w.pg.Exec(context.Background(), `INSERT INTO detection.reputation_allowlist (id, tenant_id, prefix, reason, created_by)
		VALUES ($1, $2, $3, 'servidores propios', $4)`, uuid.New(), r.w.tenant, pf, uuid.New()); err != nil {
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
	if len(got) == 1 && got[0].TargetValue != "25" {
		t.Errorf("spam target: %s", got[0].TargetValue)
	}
}

func TestScenarioDDoS(t *testing.T) {
	r := runScenario(t, "dos_out", 0, false, false)
	r.w.evaluate(r.s.Add(7 * time.Minute))
	got := checkExpected(t, r)
	if len(got) == 1 && got[0].TargetValue != "198.51.100.50" {
		t.Errorf("ddos target: %s", got[0].TargetValue)
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
			t.Logf("%s %s", f.key(), f.Summary)
		}
		t.Fatalf("la captura real produjo %d hallazgos", len(got))
	}
}

// Aislamiento entre ISP en ClickHouse: el motor de otro tenant no ve los
// datos del escenario (row policy por SQL_horus_tenant + filtro tenant_id) y
// el usuario horus_detection sin el ajuste no lee nada (fail-closed).
func TestTenantIsolationInClickHouse(t *testing.T) {
	r := runScenario(t, "scan", 0, false, false)
	other := uuid.New()
	rep, err := detectionEvaluate(r.w, other, r.s.Add(7*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Candidates != 0 || rep.Opened != 0 {
		t.Fatalf("el tenant %s ve candidatos de otro ISP: %+v", other, rep)
	}
	var mine, theirs uint64
	if err := r.w.chDet.QueryRow("SELECT count() FROM flows.client_security_1m SETTINGS SQL_horus_tenant = '" + r.w.tenant.String() + "'").Scan(&mine); err != nil {
		t.Fatal(err)
	}
	if err := r.w.chDet.QueryRow("SELECT count() FROM flows.client_security_1m SETTINGS SQL_horus_tenant = '" + other.String() + "'").Scan(&theirs); err != nil {
		t.Fatal(err)
	}
	if mine == 0 || theirs != 0 {
		t.Fatalf("row policy: propio %d, ajeno %d", mine, theirs)
	}
	if err := r.w.chDet.QueryRow("SELECT count() FROM flows.flows_raw").Scan(&theirs); err == nil {
		t.Fatal("consulta sin SQL_horus_tenant aceptada")
	}
}

// Independencia de la hora y de la zona horaria: los escenarios con ventanas
// largas (beacon 6,5 h, sustained_out 35 min) y uno de 5 min (scan) dan los
// mismos hallazgos con los datos a las 03:00, 12:00 y 23:00 locales de tres
// zonas distintas (el destino de sustained_out no es una nube conocida en el
// pipeline de prueba, así que la franja de copias nocturnas no lo exime).
func TestScenariosAnyHourAndZone(t *testing.T) {
	for _, zone := range []string{"America/Mexico_City", "Europe/Madrid", "UTC"} {
		for _, hour := range []int{3, 12, 23} {
			c := clock{Zone: zone, Hour: hour}
			t.Run(fmt.Sprintf("%s-%02d", strings.ReplaceAll(zone, "/", "_"), hour), func(t *testing.T) {
				for _, name := range []string{"beacon", "sustained_out", "scan"} {
					r := runScenarioAt(t, name, c, 0, false, false)
					// Fin de los datos + margen (la ventana de 5 min del escaneo cubre sus 2 min).
					r.w.evaluate(r.s.Add(time.Duration(r.exp.Duration)*time.Second + 5*time.Minute))
					checkExpected(t, r)
				}
			})
		}
	}
}
