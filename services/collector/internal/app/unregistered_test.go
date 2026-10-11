package app

import (
	"context"
	"encoding/json"
	"net/netip"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/services/collector/api"
)

// Las IPs no registradas se guardan en el bucket de no registrados (clave por
// collector e IP), como mucho una vez cada unregPutEvery y solo si llegaron
// datagramas nuevos.
func TestUnregisteredStoredInKV(t *testing.T) {
	e, _, _ := newEngine(t, &fakeSink{}, nil)
	st := e.States
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	st.now = func() time.Time { return now }
	ukv := &fakeKV{}
	st.UseUnregistered(ukv, "collector-a")
	v4 := netip.MustParseAddr("203.0.113.9")
	v6 := netip.MustParseAddr("2001:db8::9")
	st.Unregistered(v4, now.Add(-time.Minute))
	st.Unregistered(v4, now)
	st.Unregistered(v6, now)

	// Una evaluación urgente (onlyDirty) no toca el bucket.
	st.Evaluate(context.Background(), true)
	if len(ukv.m) != 0 {
		t.Fatalf("dirty evaluation wrote %d keys", len(ukv.m))
	}
	st.Evaluate(context.Background(), false)
	get := func(ip netip.Addr) api.Unregistered {
		t.Helper()
		raw, ok := ukv.m[api.UnregisteredKey("collector-a", ip)]
		if !ok {
			t.Fatalf("no key for %s (keys %v)", ip, ukv.m)
		}
		var u api.Unregistered
		if err := json.Unmarshal(raw, &u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	u := get(v4)
	if u.ExporterIP != "203.0.113.9" || u.Datagrams != 2 || !u.FirstSeenAt.Equal(now.Add(-time.Minute)) || !u.LastSeenAt.Equal(now) ||
		u.Collector != "collector-a" {
		t.Fatalf("v4 entry %+v", u)
	}
	if get(v6).ExporterIP != "2001:db8::9" {
		t.Fatal("v6 entry")
	}

	// Nuevo datagrama antes de unregPutEvery: no se reescribe todavía.
	now = now.Add(10 * time.Second)
	st.Unregistered(v4, now)
	st.Evaluate(context.Background(), false)
	if get(v4).Datagrams != 2 {
		t.Fatal("rewritten before unregPutEvery")
	}
	now = now.Add(unregPutEvery)
	st.Evaluate(context.Background(), false)
	if got := get(v4).Datagrams; got != 3 {
		t.Fatalf("datagrams after unregPutEvery = %d", got)
	}
	// Sin datagramas nuevos no hay escritura (el TTL del bucket la caduca).
	before := string(ukv.m[api.UnregisteredKey("collector-a", v6)])
	now = now.Add(time.Minute)
	st.Evaluate(context.Background(), false)
	if string(ukv.m[api.UnregisteredKey("collector-a", v6)]) != before {
		t.Fatal("rewritten without new datagrams")
	}
}
