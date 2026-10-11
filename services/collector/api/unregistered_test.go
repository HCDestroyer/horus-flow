package api

import (
	"context"
	"encoding/json"
	"net/netip"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func TestUnregisteredKey(t *testing.T) {
	if got := UnregisteredKey("flows-collector-1", netip.MustParseAddr("2001:db8::1")); got != "flows-collector-1.2001_db8__1" {
		t.Fatalf("v6 key %q", got)
	}
	if got := UnregisteredKey("c 1", netip.MustParseAddr("::ffff:192.0.2.1")); got != "c_1.192_0_2_1" {
		t.Fatalf("mapped v4 key %q", got)
	}
}

// Varias instancias del collector suman por IP; orden por última vez.
func TestMergeUnregistered(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	out := MergeUnregistered([]Unregistered{
		{ExporterIP: "203.0.113.9", FirstSeenAt: t0, LastSeenAt: t0.Add(time.Minute), Datagrams: 5, Collector: "a"},
		{ExporterIP: "203.0.113.9", FirstSeenAt: t0.Add(-time.Minute), LastSeenAt: t0.Add(30 * time.Second), Datagrams: 7, Collector: "b"},
		{ExporterIP: "198.51.100.1", FirstSeenAt: t0, LastSeenAt: t0.Add(2 * time.Minute), Datagrams: 1, Collector: "a"},
		{ExporterIP: "not-an-ip", FirstSeenAt: t0, LastSeenAt: t0, Datagrams: 1},
	})
	if len(out) != 2 || out[0].ExporterIP != "198.51.100.1" || out[1].ExporterIP != "203.0.113.9" {
		t.Fatalf("merge %+v", out)
	}
	m := out[1]
	if m.Datagrams != 12 || !m.FirstSeenAt.Equal(t0.Add(-time.Minute)) || !m.LastSeenAt.Equal(t0.Add(time.Minute)) || m.Collector != "" {
		t.Fatalf("merged %+v", m)
	}
}

// Ida y vuelta por un NATS en proceso: lo que guardan dos collectors se lee
// sumado por IP; un bucket vacío devuelve una lista vacía.
func TestKVUnregisteredRoundTrip(t *testing.T) {
	s, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(),
		NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats not ready")
	}
	t.Cleanup(s.Shutdown)
	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	kv, err := js.CreateOrUpdateKeyValue(ctx, UnregisteredKVConfig())
	if err != nil {
		t.Fatal(err)
	}
	r := KVUnregistered{KV: kv}
	if rows, err := r.ListUnregistered(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("empty bucket = %v, %v", rows, err)
	}
	t0 := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	ip := netip.MustParseAddr("2001:db8::7")
	for _, u := range []Unregistered{
		{ExporterIP: ip.String(), FirstSeenAt: t0, LastSeenAt: t0.Add(time.Minute), Datagrams: 2, Collector: "a"},
		{ExporterIP: ip.String(), FirstSeenAt: t0, LastSeenAt: t0.Add(2 * time.Minute), Datagrams: 3, Collector: "b"},
	} {
		b, _ := json.Marshal(u)
		if _, err := kv.Put(ctx, UnregisteredKey(u.Collector, ip), b); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := r.ListUnregistered(ctx)
	if err != nil || len(rows) != 1 || rows[0].Datagrams != 5 || rows[0].ExporterIP != "2001:db8::7" {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
}
