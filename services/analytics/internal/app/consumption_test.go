package app

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/analytics/api/consumption"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/chread"
)

type consRows struct {
	chread.Rows
	rows [][]any
	i    int
}

func (f *consRows) Next() bool   { f.i++; return f.i <= len(f.rows) }
func (f *consRows) Close() error { return nil }
func (f *consRows) Err() error   { return nil }
func (f *consRows) Scan(dest ...any) error {
	r := f.rows[f.i-1]
	for i, d := range dest {
		switch p := d.(type) {
		case *time.Time:
			*p = r[i].(time.Time)
		case *uuid.UUID:
			*p = r[i].(uuid.UUID)
		case *netip.Addr:
			*p = r[i].(netip.Addr)
		case *uint64:
			*p = r[i].(uint64)
		case *string:
			*p = r[i].(string)
		default:
			return errors.New("consRows: destino no soportado")
		}
	}
	return nil
}

type consQ struct {
	rows [][]any
	sqls []string
	args [][]any
}

func (q *consQ) Query(_ context.Context, _ uuid.UUID, sql string, args ...any) (chread.Rows, context.CancelFunc, error) {
	q.sqls, q.args = append(q.sqls, sql), append(q.args, args)
	return &consRows{rows: q.rows}, func() {}, nil
}

// Una IP que cambia de dueño a las 08:12: el bucket de las 08:00 es del
// dueño anterior (la ventana que contiene el inicio del bucket); un servicio
// con dos IPs suma ambas, cada una en su vigencia.
func TestConsumptionDailyWindows(t *testing.T) {
	t.Parallel()
	realm := uuid.New()
	a := netip.MustParseAddr("100.64.1.20")
	b := netip.MustParseAddr("100.64.1.21")
	h := func(day, hour int) time.Time { return time.Date(2026, 10, day, hour, 0, 0, 0, time.UTC) }
	// customer_1h devuelve client_ip como IPv6 (IPv4 mapeada).
	a6, b6 := netip.AddrFrom16(a.As16()), netip.AddrFrom16(b.As16())
	q := &consQ{rows: [][]any{
		{realm, a6, h(1, 7), uint64(100), uint64(10)},
		{realm, a6, h(1, 8), uint64(200), uint64(20)}, // 08:00 → dueño anterior (cambio a las 08:12)
		{realm, a6, h(1, 9), uint64(400), uint64(40)},
		{realm, a6, h(2, 3), uint64(800), uint64(80)},
		{realm, b6, h(1, 23), uint64(1000), uint64(100)},
		{uuid.New(), a6, h(1, 9), uint64(9999), uint64(9999)}, // otro realm: no cuenta
	}}
	c := Consumption{Svc: &Service{Q: q}}
	change := time.Date(2026, 10, 1, 8, 12, 0, 0, time.UTC)
	out, err := c.Daily(context.Background(), uuid.New(), []consumption.Window{
		{RealmID: realm, Address: a, From: h(1, 0), To: change},   // dueño anterior de a
		{RealmID: realm, Address: a, From: change, To: h(3, 0)},   // dueño nuevo de a
		{RealmID: realm, Address: b, From: h(1, 12), To: h(3, 0)}, // segunda IP del servicio nuevo
		{RealmID: realm, Address: b, From: h(5, 0), To: h(4, 0)},  // ventana vacía
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out[0]) != 1 || out[0][0].DownBytes != 300 || out[0][0].UpBytes != 30 {
		t.Fatalf("dueño anterior = %+v", out[0])
	}
	if len(out[1]) != 2 || out[1][0].DownBytes != 400 || out[1][1].Day != h(2, 0) || out[1][1].DownBytes != 800 {
		t.Fatalf("dueño nuevo = %+v", out[1])
	}
	if len(out[2]) != 1 || out[2][0].DownBytes != 1000 {
		t.Fatalf("segunda IP = %+v", out[2])
	}
	if out[3] != nil {
		t.Fatalf("ventana vacía = %+v", out[3])
	}
	sql := q.sqls[0]
	if !strings.Contains(sql, "flows.customer_1h") || !strings.Contains(sql, "tenant_id = ?") || !strings.Contains(sql, "toIPv6('::ffff:100.64.1.20')") {
		t.Fatalf("sql = %s", sql)
	}
	// Rangos de más de 35 días usan el agregado diario.
	q2 := &consQ{}
	if _, err := (Consumption{Svc: &Service{Q: q2}}).Daily(context.Background(), uuid.New(), []consumption.Window{
		{RealmID: realm, Address: a, From: h(1, 0), To: h(1, 0).Add(60 * 24 * time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q2.sqls[0], "flows.customer_1d") {
		t.Fatalf("sql largo = %s", q2.sqls[0])
	}
}

func TestConsumptionUnavailable(t *testing.T) {
	t.Parallel()
	c := Consumption{Svc: &Service{Q: errQ{}}}
	_, err := c.Daily(context.Background(), uuid.New(), []consumption.Window{{RealmID: uuid.New(), Address: netip.MustParseAddr("10.0.0.1"),
		From: time.Unix(0, 0), To: time.Unix(3600, 0)}})
	if !errors.Is(err, consumption.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

type errQ struct{}

func (errQ) Query(context.Context, uuid.UUID, string, ...any) (chread.Rows, context.CancelFunc, error) {
	return nil, nil, ErrNoBackend
}
