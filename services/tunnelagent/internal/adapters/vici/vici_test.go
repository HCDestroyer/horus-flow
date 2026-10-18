package vici_test

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	govici "github.com/strongswan/govici/vici"

	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/adapters/vici"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/domain"
)

var testPSK = strings.Repeat("prueba", 8)

func tunnel() domain.Tunnel {
	return domain.Tunnel{Transport: domain.TransportIPsec, Index: 7, TunnelAddress: netip.MustParseAddr("10.255.1.2"),
		HubAddress: netip.MustParseAddr("10.255.0.1"), LocalID: "horus.example.net", RemoteID: "rt-1.example.net", PSK: testPSK, MTU: 1400}
}

func TestConnMessage(t *testing.T) {
	t.Parallel()
	m, err := vici.ConnMessage(tunnel())
	if err != nil {
		t.Fatal(err)
	}
	conn, ok := m.Get("horus-7").(*govici.Message)
	if !ok {
		t.Fatalf("mensaje = %s", m)
	}
	if conn.Get("version") != "2" || !slices.Equal(conn.Get("remote_addrs").([]string), []string{"%any"}) ||
		!slices.Equal(conn.Get("proposals").([]string), vici.IKEProposals) || conn.Get("dpd_delay") != vici.DPDDelay {
		t.Fatalf("conn = %s", conn)
	}
	local := conn.Get("local").(*govici.Message)
	remote := conn.Get("remote").(*govici.Message)
	if local.Get("auth") != "psk" || local.Get("id") != "horus.example.net" || remote.Get("id") != "rt-1.example.net" {
		t.Fatalf("identidades = %s / %s", local, remote)
	}
	child := conn.Get("children").(*govici.Message).Get("horus-7").(*govici.Message)
	if child.Get("if_id_in") != "7" || child.Get("if_id_out") != "7" || child.Get("start_action") != "none" ||
		!slices.Equal(child.Get("esp_proposals").([]string), vici.ESPProposals) {
		t.Fatalf("child = %s", child)
	}
	if strings.Contains(m.String(), testPSK) {
		t.Fatal("la PSK no va en load-conn")
	}
	tt := tunnel()
	tt.Remote = netip.MustParseAddr("198.51.100.7")
	m, _ = vici.ConnMessage(tt)
	if got := m.Get("horus-7").(*govici.Message).Get("remote_addrs").([]string); got[0] != "198.51.100.7" {
		t.Fatalf("remote_addrs = %v", got)
	}
	sh, _ := vici.SharedMessage(tunnel())
	if sh.Get("data") != testPSK || sh.Get("type") != "IKE" || !slices.Equal(sh.Get("owners").([]string), []string{"horus.example.net", "rt-1.example.net"}) {
		t.Fatalf("shared = %v", sh.Keys())
	}
}

type fake struct {
	calls []string
	sas   []*govici.Message
	conns []string
}

func (f *fake) Call(_ context.Context, cmd string, _ *govici.Message) (*govici.Message, error) {
	f.calls = append(f.calls, cmd)
	out := govici.NewMessage()
	if cmd == "get-conns" {
		_ = out.Set("conns", f.conns)
	}
	return out, nil
}
func (f *fake) StreamedSAs(context.Context) ([]*govici.Message, error) { return f.sas, nil }
func (f *fake) Close() error                                           { return nil }

func msg(t *testing.T, kv map[string]any) *govici.Message {
	t.Helper()
	m := govici.NewMessage()
	for k, v := range kv {
		if err := m.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func TestDaemon(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0).UTC()
	child := msg(t, map[string]any{"state": "INSTALLED", "bytes-in": "1000", "bytes-out": "500", "install-time": "60"})
	ike := msg(t, map[string]any{"state": "ESTABLISHED", "established": "3600", "remote-host": "198.51.100.20", "remote-port": "4500",
		"child-sas": msg(t, map[string]any{"horus-7-3": child})})
	f := &fake{conns: []string{"horus-7", "ajena", "horus-0"}, sas: []*govici.Message{msg(t, map[string]any{"horus-7": ike, "otra": ike})}}
	d := vici.NewWithCaller(f, func() time.Time { return now })
	ctx := context.Background()
	if err := d.Load(ctx, tunnel()); err != nil {
		t.Fatal(err)
	}
	if err := d.Unload(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.calls, []string{"load-shared", "load-conn", "unload-conn", "terminate", "unload-shared"}) {
		t.Fatalf("llamadas = %v", f.calls)
	}
	conns, err := d.Conns(ctx)
	if err != nil || !slices.Equal(conns, []uint32{7}) {
		t.Fatalf("conns = %v, %v", conns, err)
	}
	sas, err := d.SAs(ctx)
	if err != nil || len(sas) != 1 {
		t.Fatalf("sas = %v, %v", sas, err)
	}
	sa := sas[7]
	if sa.State != "established" || !sa.ChildInstalled || sa.Endpoint != "198.51.100.20:4500" || sa.RxBytes != 1000 ||
		!sa.EstablishedAt.Equal(now.Add(-time.Hour)) || !sa.LastRekeyAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("sa = %+v", sa)
	}
}
