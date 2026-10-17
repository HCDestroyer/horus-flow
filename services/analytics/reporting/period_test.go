package reporting

import (
	"errors"
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("zona %s no disponible: %v", name, err)
	}
	return loc
}

func TestLastWeekISOInTenantTimezone(t *testing.T) {
	t.Parallel()
	mx := mustLoc(t, "America/Mexico_City") // UTC−6 todo el año desde 2022
	// Lunes 12/10/2026 08:00 en México = 14:00 UTC → semana del 05/10 al 12/10.
	now := time.Date(2026, 10, 12, 14, 0, 0, 0, time.UTC)
	p := LastWeek(now, mx)
	if got, want := p.From, time.Date(2026, 10, 5, 6, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("from = %v, quiero %v", got, want)
	}
	if got, want := p.To, time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("to = %v, quiero %v", got, want)
	}
	if y, w := p.ISOWeek(); y != 2026 || w != 41 {
		t.Fatalf("semana ISO = %d-W%02d, quiero 2026-W41", y, w)
	}
	// Lunes 12/10 a las 03:00 UTC sigue siendo domingo 11/10 en México:
	// la última semana completa es la anterior.
	p2 := LastWeek(time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC), mx)
	if got, want := p2.To, time.Date(2026, 10, 5, 6, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("antes de la medianoche local: to = %v, quiero %v", got, want)
	}
}

func TestLastWeekAcrossDST(t *testing.T) {
	t.Parallel()
	madrid := mustLoc(t, "Europe/Madrid")
	// El domingo 25/10/2026 a las 03:00 CEST se vuelve a las 02:00 CET:
	// la semana del 19/10 al 26/10 dura 169 h.
	p := LastWeek(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC), madrid)
	if got, want := p.From, time.Date(2026, 10, 18, 22, 0, 0, 0, time.UTC); !got.Equal(want) { // 19/10 00:00 CEST
		t.Fatalf("from = %v, quiero %v", got, want)
	}
	if got, want := p.To, time.Date(2026, 10, 25, 23, 0, 0, 0, time.UTC); !got.Equal(want) { // 26/10 00:00 CET
		t.Fatalf("to = %v, quiero %v", got, want)
	}
	if d := p.To.Sub(p.From); d != 169*time.Hour {
		t.Fatalf("duración = %v, quiero 169 h", d)
	}
	// Semana con el cambio de primavera (29/03/2026): 167 h.
	sp := LastWeek(time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC), madrid)
	if d := sp.To.Sub(sp.From); d != 167*time.Hour {
		t.Fatalf("primavera: duración = %v, quiero 167 h", d)
	}
	// Los límites son medianoche local.
	for _, x := range []time.Time{p.From, p.To, sp.From, sp.To} {
		if l := x.In(madrid); l.Hour() != 0 || l.Minute() != 0 || l.Weekday() != time.Monday {
			t.Fatalf("límite %v no es lunes 00:00 local", l)
		}
	}
	// El periodo anterior de una semana es la semana de calendario anterior (168 h aquí).
	prev := p.Previous()
	if !prev.To.Equal(p.From) || prev.From.In(madrid).Weekday() != time.Monday || prev.From.In(madrid).Hour() != 0 {
		t.Fatalf("previous = %v → %v", prev.From.In(madrid), prev.To.In(madrid))
	}
}

func TestPreviousOfCustomRange(t *testing.T) {
	t.Parallel()
	p := Period{From: time.Date(2026, 10, 1, 10, 30, 0, 0, time.UTC), To: time.Date(2026, 10, 2, 10, 30, 0, 0, time.UTC), Loc: time.UTC}
	prev := p.Previous()
	if !prev.To.Equal(p.From) || prev.To.Sub(prev.From) != 24*time.Hour {
		t.Fatalf("previous = %v", prev)
	}
}

func TestScheduleDue(t *testing.T) {
	t.Parallel()
	mx := mustLoc(t, "America/Mexico_City")
	s := Schedule{Weekday: 1, Hour: 7, Minute: 0, Loc: mx}
	created := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	// Lunes 12/10 06:59 local: aún no toca la de esta semana → la de la semana pasada
	// (periodo 28/09–05/10), que recupera una ejecución perdida.
	p, ok := s.Due(time.Date(2026, 10, 12, 12, 59, 0, 0, time.UTC), created)
	if !ok || !p.From.Equal(time.Date(2026, 9, 28, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("antes de la hora: %v %v", p, ok)
	}
	// Lunes 12/10 07:00 local: periodo 05/10–12/10.
	p, ok = s.Due(time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC), created)
	if !ok || !p.From.Equal(time.Date(2026, 10, 5, 6, 0, 0, 0, time.UTC)) || !p.To.Equal(time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("a la hora: %v %v", p, ok)
	}
	// Miércoles: el mismo periodo (idempotente por la unicidad en PostgreSQL).
	p2, _ := s.Due(time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC), created)
	if !p2.From.Equal(p.From) {
		t.Fatalf("miércoles: %v, quiero %v", p2.From, p.From)
	}
	// Definición creada el martes 13/10: la ejecución del lunes 12/10 es anterior → nada.
	if _, ok := s.Due(time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC), time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)); ok {
		t.Fatal("no se generan reportes retroactivos a la creación")
	}
	// Domingo 07:00 (weekday 7).
	sun := Schedule{Weekday: 7, Hour: 7, Loc: time.UTC}
	p, ok = sun.Due(time.Date(2026, 10, 18, 8, 0, 0, 0, time.UTC), created)
	if !ok || !p.To.Equal(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("domingo: %v %v", p, ok)
	}
	// Próxima ejecución.
	if got, want := s.Next(time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC)), time.Date(2026, 10, 19, 13, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("next = %v, quiero %v", got, want)
	}
	if got, want := s.Next(time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)), time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("next = %v, quiero %v", got, want)
	}
}

func TestCustomRange(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	p, err := CustomRange(from, now.Add(48*time.Hour), now, time.UTC)
	if err != nil || !p.To.Equal(now) {
		t.Fatalf("to se recorta a now: %v %v", p, err)
	}
	for _, c := range []struct {
		from, to time.Time
		want     error
	}{
		{now, now.Add(-time.Hour), ErrRangeInvalid},
		{now.Add(time.Hour), now.Add(2 * time.Hour), ErrRangeFuture},
		{now, now.Add(time.Hour), ErrRangeInvalid}, // vacío tras recortar
		{now.Add(-32 * 24 * time.Hour), now, ErrRangeTooLarge},
	} {
		if _, err := CustomRange(c.from, c.to, now, time.UTC); !errors.Is(err, c.want) {
			t.Errorf("%v–%v: %v, quiero %v", c.from, c.to, err, c.want)
		}
	}
}

func TestValidTimezone(t *testing.T) {
	t.Parallel()
	if !ValidTimezone("America/Bogota") || ValidTimezone("Marte/Olympus") || ValidTimezone("") || ValidTimezone("Local") {
		t.Fatal("ValidTimezone")
	}
	if LoadLocation("nope") != time.UTC {
		t.Fatal("zona desconocida → UTC")
	}
}
