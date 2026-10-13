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
var statusRank = map[string]int{api.StatusCritical: 0, api.StatusOffline: 1, api.StatusDegraded: 2, api.StatusWarning: 3,
	api.StatusStale: 4, api.StatusUnknown: 5, api.StatusMaintenance: 6, api.StatusOnline: 7}

// SortProblemsFirst ordena por estado (critical, offline, degraded, warning,
// stale, unknown, online) y luego por nombre (sin distinguir mayúsculas).
func SortProblemsFirst(rows []api.RouterStatus) {
	sort.SliceStable(rows, func(i, j int) bool {
		ri, rj := statusRank[rows[i].Status], statusRank[rows[j].Status]
		if ri != rj {
			return ri < rj
		}
		ni, nj := strings.ToLower(rows[i].Name), strings.ToLower(rows[j].Name)
		if ni != nj {
			return ni < nj
		}
		return rows[i].RouterID.String() < rows[j].RouterID.String()
	})
}
