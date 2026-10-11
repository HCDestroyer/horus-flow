package httpapi

import (
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	collectorapi "github.com/hcdestroyer/horus-flow/services/collector/api"
)

// unregSort liga el cursor al orden del listado (última vez descendente, IP).
const unregSort = "last_seen_at:desc,exporter_ip"

func tsMillis(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// listUnregistered sirve GET /platform/exporters/unregistered: IPs que
// envían flujos sin ser exportador de ningún router (el collector descarta
// sus datagramas). Vista de plataforma: solo IP, primera y última vez y
// datagramas, sin tenant, router ni contenido de los flujos.
func (h *Handler) listUnregistered(w http.ResponseWriter, r *http.Request) {
	req, err := pagination.ParseRequest(r.URL.Query())
	if err != nil {
		code := problem.CodeValidationFailed
		if errors.Is(err, pagination.ErrInvalidCursor) {
			code = problem.CodeInvalidCursor
		}
		problem.Std(w, r, http.StatusBadRequest, code)
		return
	}
	fk := pagination.FilterKey()
	var after *collectorapi.Unregistered
	if req.Cursor != "" {
		cur, err := h.cursor.Decode(req.Cursor, unregSort, fk, "")
		if err != nil || len(cur.Keys) != 2 {
			problem.Std(w, r, http.StatusBadRequest, problem.CodeInvalidCursor)
			return
		}
		at, err := time.Parse(time.RFC3339Nano, cur.Keys[0])
		if err != nil {
			problem.Std(w, r, http.StatusBadRequest, problem.CodeInvalidCursor)
			return
		}
		after = &collectorapi.Unregistered{LastSeenAt: at, ExporterIP: cur.Keys[1]}
	}
	var rows []collectorapi.Unregistered
	if h.unreg != nil {
		if rows, err = h.unreg.ListUnregistered(r.Context()); err != nil {
			problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
			return
		}
	}
	// Una IP que ya es exportador registrado deja de aparecer aunque su
	// entrada no haya caducado todavía.
	registered := map[netip.Addr]bool{}
	for _, e := range h.inv.Load().Data().Exporters {
		registered[e.TunnelIP.Unmap()] = true
	}
	kept := rows[:0]
	for _, u := range rows {
		ip, err := netip.ParseAddr(u.ExporterIP)
		if err != nil || registered[ip.Unmap()] {
			continue
		}
		kept = append(kept, u)
	}
	rows = kept
	total := len(rows)
	if after != nil {
		i := 0
		for i < len(rows) && !unregAfter(rows[i], *after) {
			i++
		}
		rows = rows[i:]
	}
	page := pagination.Page{Limit: req.Limit}
	if len(rows) > req.Limit {
		rows = rows[:req.Limit]
		last := rows[len(rows)-1]
		next := h.cursor.Encode(pagination.Cursor{Keys: []string{last.LastSeenAt.UTC().Format(time.RFC3339Nano), last.ExporterIP},
			Sort: unregSort, Filter: fk})
		page.NextCursor, page.HasMore = &next, true
	}
	if req.IncludeTotal {
		page.Total = &total
	}
	data := make([]map[string]any, 0, len(rows))
	for _, u := range rows {
		data = append(data, map[string]any{"exporter_ip": u.ExporterIP, "first_seen_at": tsMillis(u.FirstSeenAt),
			"last_seen_at": tsMillis(u.LastSeenAt), "datagrams": strconv.FormatUint(u.Datagrams, 10)})
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data, "page": page})
}

// unregAfter indica si u va detrás de la clave c en el orden del listado.
func unregAfter(u, c collectorapi.Unregistered) bool {
	if !u.LastSeenAt.Equal(c.LastSeenAt) {
		return u.LastSeenAt.Before(c.LastSeenAt)
	}
	return u.ExporterIP > c.ExporterIP
}
