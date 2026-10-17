package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func i64(v int64) *int64   { return &v }
func str(v string) *string { return &v }

func TestPlanResolution(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	tenant, cust := uuid.New(), uuid.New()
	res := Resolution{Found: true, ExternalCustomerID: i64(455), ExternalServiceID: i64(812), DisplayName: str("  Ferretería López "),
		Code: str("C-000455"), ServiceState: str("activo"), Plan: str("Fibra 100")}

	// Sin identidad: se abre desde now.
	p := PlanResolution(nil, res, now, tenant, cust)
	if p.Close || p.Open == nil || !p.Open.ValidFrom.Equal(now) || *p.Open.DisplayName != "Ferretería López" || p.Open.System != SystemArgos {
		t.Fatalf("alta = %+v", p)
	}
	cur := p.Open

	// Mismo dueño: refresco en sitio (quita stale).
	cur.Stale = true
	res2 := res
	res2.ServiceState = str("suspendido")
	p = PlanResolution(cur, res2, now.Add(time.Hour), tenant, cust)
	if p.Close || p.Open != nil || p.Refresh == nil || *p.Refresh.ServiceState != "suspendido" || p.Refresh.Stale || p.Refresh.ID != cur.ID {
		t.Fatalf("refresco = %+v", p)
	}

	// Otro dueño (la IP pasó a otro servicio) con "desde" informado por ArgosISP.
	since := now.Add(2 * time.Hour)
	other := Resolution{Found: true, ExternalCustomerID: i64(900), ExternalServiceID: i64(901), DisplayName: str("Ana"), Since: &since}
	p = PlanResolution(cur, other, now.Add(3*time.Hour), tenant, cust)
	if !p.Close || !p.CloseAt.Equal(since) || p.Open == nil || !p.Open.ValidFrom.Equal(since) {
		t.Fatalf("cambio de dueño = %+v", p)
	}
	// Un "desde" anterior al inicio de la vigencia actual no la solapa.
	old := now.Add(-time.Hour)
	other.Since = &old
	p = PlanResolution(cur, other, now.Add(3*time.Hour), tenant, cust)
	if !p.CloseAt.Equal(now.Add(3*time.Hour)) || !p.Open.ValidFrom.Equal(now.Add(3*time.Hour)) {
		t.Fatalf("desde anterior = %+v", p)
	}

	// No encontrada: se cierra.
	p = PlanResolution(cur, Resolution{}, now.Add(4*time.Hour), tenant, cust)
	if !p.Close || p.Open != nil {
		t.Fatalf("no encontrada = %+v", p)
	}
	if p := PlanResolution(nil, Resolution{}, now, tenant, cust); p.Close || p.Open != nil || p.Refresh != nil {
		t.Fatalf("no encontrada sin identidad = %+v", p)
	}
}

// Una IP reasignada entre servicios: un hallazgo de ayer se atribuye al dueño de ayer.
func TestIdentityValidAt(t *testing.T) {
	t.Parallel()
	change := time.Date(2026, 10, 17, 8, 12, 0, 0, time.UTC)
	yesterday := ExternalIdentity{ExternalServiceID: i64(812), ValidFrom: change.Add(-48 * time.Hour), ValidTo: &change}
	today := ExternalIdentity{ExternalServiceID: i64(901), ValidFrom: change}
	finding := change.Add(-20 * time.Hour)
	if !yesterday.ValidAt(finding) || today.ValidAt(finding) {
		t.Fatal("el hallazgo de ayer es del dueño anterior")
	}
	if yesterday.ValidAt(change) || !today.ValidAt(change) {
		t.Fatal("en el instante del cambio manda el nuevo dueño")
	}
	if !today.Current() || yesterday.Current() {
		t.Fatal("vigencias")
	}
}

func TestExternalAliasNeverOverwritesManual(t *testing.T) {
	t.Parallel()
	manual, argos, ppp := AliasSourceManual, AliasSourceArgos, "routeros_ppp"
	c := Customer{Alias: str("Puesto a mano"), AliasSource: &manual}
	if c.ExternalAlias(str("Ferretería López")) || *c.Alias != "Puesto a mano" {
		t.Fatal("un alias manual nunca lo pisa ArgosISP")
	}
	if c.ExternalAlias(nil) || c.Alias == nil {
		t.Fatal("ni lo borra")
	}
	c = Customer{Alias: str("flopez"), AliasSource: &ppp}
	if !c.ExternalAlias(str("Ferretería López")) || *c.AliasSource != AliasSourceArgos || *c.Alias != "Ferretería López" {
		t.Fatalf("ArgosISP manda sobre routeros_ppp: %+v", c)
	}
	if c.ExternalAlias(str("Ferretería López")) {
		t.Fatal("sin cambios no cuenta")
	}
	c = Customer{}
	if !c.ExternalAlias(str("Ana")) || *c.AliasSource != argos {
		t.Fatal("sin alias toma el de ArgosISP")
	}
	if !c.ExternalAlias(nil) || c.Alias != nil || c.AliasSource != nil {
		t.Fatal("sin identidad vigente se quita el nombre del dueño anterior")
	}
}

func TestCleanTextAndBackoff(t *testing.T) {
	t.Parallel()
	if CleanText(str("  \t ")) != nil || *CleanText(str("a\x00b")) != "ab" || len([]rune(*CleanText(str(strings.Repeat("ñ", 500))))) != 120 {
		t.Fatal("CleanText")
	}
	want := []time.Duration{0, 30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for f, w := range want {
		if got := NextBackoff(f); got != w {
			t.Errorf("NextBackoff(%d) = %v, quiero %v", f, got, w)
		}
	}
}
