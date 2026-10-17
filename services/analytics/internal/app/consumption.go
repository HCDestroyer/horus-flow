package app

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/analytics/api/consumption"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/chread"
)

// Consumption implementa consumption.Reader sobre los agregados de clientes.
type Consumption struct{ Svc *Service }

var _ consumption.Reader = Consumption{}

// ip6 es la clave client_ip (IPv6) de una dirección.
func ip6(a netip.Addr) netip.Addr { return netip.AddrFrom16(a.Unmap().As16()) }

func (c Consumption) query(ctx context.Context, tenant uuid.UUID, sql string, args ...any) (rowsScanner, func(), error) {
	rows, cancel, err := c.Svc.Q.Query(ctx, tenant, sql, args...)
	if err != nil {
		if errors.Is(err, ErrNoBackend) || errors.Is(err, chread.ErrUnavailable) {
			return nil, nil, fmt.Errorf("%w: %v", consumption.ErrUnavailable, err) //nolint:errorlint // se envuelve el centinela del contrato
		}
		return nil, nil, err
	}
	return rows, func() { _ = rows.Close(); cancel() }, nil
}

type rowsScanner interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

// Daily implementa consumption.Reader.
func (c Consumption) Daily(ctx context.Context, tenant uuid.UUID, windows []consumption.Window) ([][]consumption.Day, error) {
	out := make([][]consumption.Day, len(windows))
	if len(windows) == 0 {
		return out, nil
	}
	if len(windows) > consumption.MaxWindows {
		return nil, fmt.Errorf("%w: %d ventanas", ErrBadRequest, len(windows))
	}
	var from, to time.Time
	ips := map[netip.Addr]bool{}
	realms := map[uuid.UUID]bool{}
	for i, w := range windows {
		if !w.From.Before(w.To) {
			continue
		}
		if from.IsZero() || w.From.Before(from) {
			from = w.From
		}
		if w.To.After(to) {
			to = w.To
		}
		ips[ip6(w.Address)] = true
		realms[w.RealmID] = true
		out[i] = []consumption.Day{}
	}
	if from.IsZero() {
		return out, nil
	}
	tbl, step := "flows.customer_1h", time.Hour
	if to.Sub(from) > 35*24*time.Hour {
		tbl, step = "flows.customer_1d", 24*time.Hour
	}
	// Las direcciones y realms vienen validados (netip/uuid): sus literales no
	// pueden contener comillas.
	ipList := make([]string, 0, len(ips))
	for a := range ips {
		ipList = append(ipList, "toIPv6('"+a.String()+"')")
	}
	realmList := make([]string, 0, len(realms))
	for r := range realms {
		realmList = append(realmList, "toUUID('"+r.String()+"')")
	}
	sort.Strings(ipList)
	sort.Strings(realmList)
	sql := fmt.Sprintf(`SELECT realm_id, client_ip, toDateTime(bucket, 'UTC') AS b,
		       sumIf(bytes, direction = 'download') AS d, sumIf(bytes, direction IN ('upload', 'internal')) AS u
		FROM %s WHERE tenant_id = ? AND bucket >= ? AND bucket < ? AND realm_id IN (%s) AND client_ip IN (%s)
		GROUP BY realm_id, client_ip, b`, tbl, strings.Join(realmList, ", "), strings.Join(ipList, ", "))
	lo, hi := from.UTC().Truncate(step), to.UTC()
	var loArg, hiArg any = lo, hi
	if step == 24*time.Hour {
		loArg, hiArg = lo.Format(time.DateOnly), hi.Add(24*time.Hour-time.Nanosecond).Format(time.DateOnly)
	}
	rows, done, err := c.query(ctx, tenant, sql, tenant, loArg, hiArg)
	if err != nil {
		return nil, err
	}
	defer done()
	type key struct {
		realm uuid.UUID
		ip    netip.Addr
	}
	byKey := map[key][]int{}
	for i, w := range windows {
		k := key{w.RealmID, ip6(w.Address)}
		byKey[k] = append(byKey[k], i)
	}
	acc := make([]map[int64]*consumption.Day, len(windows))
	for rows.Next() {
		var realm uuid.UUID
		var ip netip.Addr
		var b time.Time
		var d, u uint64
		if err := rows.Scan(&realm, &ip, &b, &d, &u); err != nil {
			return nil, err
		}
		for _, i := range byKey[key{realm, ip6(ip)}] {
			w := windows[i]
			// Un bucket cuenta para la ventana que contiene su inicio.
			if b.Before(w.From) || !b.Before(w.To) {
				continue
			}
			day := b.UTC().Truncate(24 * time.Hour)
			if acc[i] == nil {
				acc[i] = map[int64]*consumption.Day{}
			}
			x := acc[i][day.Unix()]
			if x == nil {
				x = &consumption.Day{Day: day}
				acc[i][day.Unix()] = x
			}
			x.DownBytes += d
			x.UpBytes += u
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, m := range acc {
		for _, d := range m {
			out[i] = append(out[i], *d)
		}
		sort.Slice(out[i], func(a, b int) bool { return out[i][a].Day.Before(out[i][b].Day) })
	}
	return out, nil
}

// Summary implementa consumption.Reader.
func (c Consumption) Summary(ctx context.Context, tenant, realm uuid.UUID, addr netip.Addr, at time.Time, since *time.Time) (*consumption.Summary, error) {
	at = at.UTC()
	start := at.Add(-30 * 24 * time.Hour)
	if since != nil && since.After(start) {
		start = since.UTC()
	}
	res := &consumption.Summary{TopServices7d: []consumption.Service{}}
	if !start.Before(at) {
		return res, nil
	}
	ip := ip6(addr)
	rows, done, err := c.query(ctx, tenant, `SELECT bucket, sumIf(bytes, direction = 'download') AS d,
		sumIf(bytes, direction IN ('upload', 'internal')) AS u
		FROM flows.customer_1h WHERE tenant_id = ? AND realm_id = ? AND client_ip = ? AND bucket >= ? AND bucket < ?
		GROUP BY bucket`, tenant, realm, ip, start.Truncate(time.Hour), at)
	if err != nil {
		return nil, err
	}
	d24, d7 := at.Add(-24*time.Hour), at.Add(-7*24*time.Hour)
	for rows.Next() {
		var b time.Time
		var d, u uint64
		if err := rows.Scan(&b, &d, &u); err != nil {
			done()
			return nil, err
		}
		if b.Before(start) && since != nil {
			continue
		}
		res.Last30d.DownBytes += d
		res.Last30d.UpBytes += u
		if !b.Before(d7) {
			res.Last7d.DownBytes += d
			res.Last7d.UpBytes += u
		}
		if !b.Before(d24) {
			res.Last24h.DownBytes += d
			res.Last24h.UpBytes += u
		}
	}
	err = rows.Err()
	done()
	if err != nil {
		return nil, err
	}
	peakFrom := d24
	if start.After(peakFrom) {
		peakFrom = start
	}
	rows, done, err = c.query(ctx, tenant, `SELECT max(d) FROM (SELECT bucket, sumIf(bytes, direction = 'download') AS d
		FROM flows.customer_5m WHERE tenant_id = ? AND realm_id = ? AND client_ip = ? AND bucket >= ? AND bucket < ? GROUP BY bucket)`,
		tenant, realm, ip, peakFrom, at)
	if err != nil {
		return nil, err
	}
	if rows.Next() {
		var peak uint64
		if err := rows.Scan(&peak); err == nil {
			res.PeakDownBps24h = float64(peak) * 8 / 300
		}
	}
	done()
	topFrom := d7
	if start.After(topFrom) {
		topFrom = start
	}
	rows, done, err = c.query(ctx, tenant, `SELECT toString(service_id) AS s, sum(bytes) AS b
		FROM flows.customer_1h WHERE tenant_id = ? AND realm_id = ? AND client_ip = ? AND bucket >= ? AND bucket < ?
		GROUP BY s ORDER BY b DESC LIMIT 5`, tenant, realm, ip, topFrom.Truncate(time.Hour), at)
	if err != nil {
		return nil, err
	}
	defer done()
	rules := c.Svc.rules(tenant)
	for rows.Next() {
		var key string
		var b uint64
		if err := rows.Scan(&key, &b); err != nil {
			return nil, err
		}
		name := c.Svc.label(rules, DimServices, key)
		if name == "" || key == "" || key == uuid.Nil.String() {
			name = "Sin clasificar"
		}
		res.TopServices7d = append(res.TopServices7d, consumption.Service{Name: name, Bytes: b})
	}
	return res, rows.Err()
}
