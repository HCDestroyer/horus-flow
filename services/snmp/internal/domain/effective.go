package domain

import (
	"sort"
	"strings"
	"time"

	"github.com/hcdestroyer/horus-flow/services/snmp/api"
)

// StaleAfterIntervals: observación más vieja que esto = stale (api.md §2.6).
const StaleAfterIntervals = 3

// Effective calcula el estado efectivo (8 valores) a partir del observado:
// unknown si nunca se observó, stale si la última observación supera 3
// intervalos o el sondeador no late, si no el observado. Devuelve el estado y
// desde cuándo rige.
func Effective(observed string, observedSince, lastObservedAt *time.Time, now time.Time, interval time.Duration, pollerHealthy bool) (status string, since *time.Time) {
	if observed == "" || lastObservedAt == nil {
		return api.StatusUnknown, nil
	}
	limit := time.Duration(StaleAfterIntervals) * interval
	if now.Sub(*lastObservedAt) > limit {
		t := lastObservedAt.Add(limit)
		return api.StatusStale, &t
	}
	if !pollerHealthy {
		return api.StatusStale, lastObservedAt
	}
	return observed, observedSince
}

// PollerHealthy: el último ciclo es más reciente que 3 intervalos.
func PollerHealthy(lastCycle *time.Time, now time.Time, interval time.Duration) bool {
	return lastCycle != nil && now.Sub(*lastCycle) <= time.Duration(StaleAfterIntervals)*interval
}

// statusRank es el orden "problemas primero" de GET /routers/status.
var statusRank = map[string]int{api.StatusCritical: 0, api.StatusOffline: 1, api.StatusUnreachable: 2, api.StatusDegraded: 2, api.StatusWarning: 3,
	api.StatusStale: 4, api.StatusUnknown: 5, api.StatusMaintenance: 6, api.StatusOnline: 7}

// SortProblemsFirst ordena por estado (critical, offline, degraded, warning,
// stale, unknown, online) y luego por nombre (sin distinguir mayúsculas).
func SortProblemsFirst(rows []api.RouterStatus) {
	sort.SliceStable(rows, func(i, j int) bool { return StatusLess(rows[i], rows[j]) })
}

// StatusLess es el orden de SortProblemsFirst (problemas primero, después
// por nombre).
func StatusLess(a, b api.RouterStatus) bool {
	ra, rb := statusRank[a.Status], statusRank[b.Status]
	if ra != rb {
		return ra < rb
	}
	na, nb := strings.ToLower(a.Name), strings.ToLower(b.Name)
	if na != nb {
		return na < nb
	}
	return a.RouterID.String() < b.RouterID.String()
}
