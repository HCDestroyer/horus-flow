package reporting

import (
	"errors"
	"fmt"
	"time"
)

// Period es un intervalo semiabierto [From, To) expresado en instantes UTC
// y la zona horaria del ISP con la que se calculó y se muestra.
type Period struct {
	From, To time.Time
	Loc      *time.Location
}

// MaxRange es el máximo de un reporte a petición (api.md: rango acotado).
const MaxRange = 31 * 24 * time.Hour

// LoadLocation resuelve una zona IANA; vacía o desconocida → UTC (el
// llamante valida antes si debe rechazarla).
func LoadLocation(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

// ValidTimezone indica si name es una zona IANA conocida por el proceso.
func ValidTimezone(name string) bool {
	if name == "" || name == "Local" {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

// weekStart es el lunes 00:00 (hora local de loc) de la semana ISO de t.
// La aritmética es de calendario (time.Date normaliza), así que un cambio
// de horario dentro de la semana no desplaza los límites.
func weekStart(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	back := (int(l.Weekday()) + 6) % 7 // lunes = 0
	return time.Date(l.Year(), l.Month(), l.Day()-back, 0, 0, 0, 0, loc)
}

// addDays suma días de calendario a una medianoche local.
func addDays(t time.Time, days int) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day()+days, 0, 0, 0, 0, t.Location())
}

// LastWeek devuelve la última semana ISO completa anterior a now en loc:
// del lunes 00:00 al lunes 00:00 siguiente (hora local; 167, 168 o 169 h
// reales según haya cambio de horario).
func LastWeek(now time.Time, loc *time.Location) Period {
	ws := weekStart(now, loc)
	return Period{From: addDays(ws, -7).UTC(), To: ws.UTC(), Loc: loc}
}

// ISOWeek devuelve el año y la semana ISO del inicio del periodo (en su zona).
func (p Period) ISOWeek() (year, week int) {
	return p.From.In(p.Loc).ISOWeek()
}

// Previous es el periodo de la misma duración inmediatamente anterior
// (comparativa de tráfico). Para una semana ISO es la semana anterior en
// calendario local.
func (p Period) Previous() Period {
	from, to := p.From.In(p.Loc), p.To.In(p.Loc)
	if isMidnight(from) && isMidnight(to) {
		days := int(to.Sub(from).Round(24*time.Hour) / (24 * time.Hour))
		return Period{From: addDays(from, -days).UTC(), To: from.UTC(), Loc: p.Loc}
	}
	d := p.To.Sub(p.From)
	return Period{From: p.From.Add(-d), To: p.From, Loc: p.Loc}
}

func isMidnight(t time.Time) bool {
	return t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0
}

// Schedule es la programación semanal de una definición.
type Schedule struct {
	Weekday int // ISO: 1 = lunes … 7 = domingo
	Hour    int
	Minute  int
	Loc     *time.Location
}

// runAt es el momento de ejecución de la semana que empieza en ws (lunes local).
func (s Schedule) runAt(ws time.Time) time.Time {
	return time.Date(ws.Year(), ws.Month(), ws.Day()+s.Weekday-1, s.Hour, s.Minute, 0, 0, ws.Location())
}

// Due devuelve el periodo que toca generar en now: la semana ISO anterior a
// la última ejecución programada ya vencida (la de esta semana si ya pasó su
// hora; si no, la de la semana pasada, lo que recupera una ejecución perdida
// si el rol estuvo parado). ok es false si esa ejecución es anterior a
// notBefore (alta de la definición: no se generan reportes retroactivos).
// La unicidad (definición, periodo) en PostgreSQL hace el resto idempotente.
func (s Schedule) Due(now, notBefore time.Time) (Period, bool) {
	loc := s.Loc
	if loc == nil {
		loc = time.UTC
	}
	s.Loc = loc
	ws := weekStart(now, loc)
	run := s.runAt(ws)
	if now.Before(run) {
		ws = addDays(ws, -7)
		run = s.runAt(ws)
	}
	if run.Before(notBefore) {
		return Period{}, false
	}
	return Period{From: addDays(ws, -7).UTC(), To: ws.UTC(), Loc: loc}, true
}

// Next es la próxima ejecución programada estrictamente posterior a now.
func (s Schedule) Next(now time.Time) time.Time {
	loc := s.Loc
	if loc == nil {
		loc = time.UTC
	}
	ws := weekStart(now, loc)
	run := s.runAt(ws)
	if !run.After(now) {
		run = s.runAt(addDays(ws, 7))
	}
	return run.UTC()
}

// Errores de validación de un rango a petición.
var (
	ErrRangeInvalid  = errors.New("reporting: from debe ser anterior a to")
	ErrRangeTooLarge = fmt.Errorf("reporting: el rango no puede superar %d días", int(MaxRange/(24*time.Hour)))
	ErrRangeFuture   = errors.New("reporting: el rango no puede empezar en el futuro")
)

// CustomRange valida un rango a petición: from < to, como mucho MaxRange,
// sin empezar en el futuro; to se recorta a now.
func CustomRange(from, to, now time.Time, loc *time.Location) (Period, error) {
	if !from.Before(to) {
		return Period{}, ErrRangeInvalid
	}
	if from.After(now) {
		return Period{}, ErrRangeFuture
	}
	if to.After(now) {
		to = now
	}
	if !from.Before(to) {
		return Period{}, ErrRangeInvalid
	}
	if to.Sub(from) > MaxRange {
		return Period{}, ErrRangeTooLarge
	}
	return Period{From: from.UTC(), To: to.UTC(), Loc: loc}, nil
}
