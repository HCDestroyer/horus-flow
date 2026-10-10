package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", rel)
}

func TestParseSizeAndDuration(t *testing.T) {
	for in, want := range map[string]int64{"": 0, "1024": 1024, "64KiB": 64 << 10, "512MiB": 512 << 20,
		"1GiB": 1 << 30, "50GB": 50e9, "8MB": 8e6, "10B": 10} {
		got, err := parseSize(in)
		if err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	if _, err := parseSize("1XB"); err == nil {
		t.Error("parseSize(1XB): want error")
	}
	for in, want := range map[string]time.Duration{"": 0, "30d": 30 * 24 * time.Hour, "24h": 24 * time.Hour, "2m": 2 * time.Minute} {
		got, err := parseDuration(in)
		if err != nil || got != want {
			t.Errorf("parseDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if got := expandEnv("a ${X:-5GB} ${Y:-z}", map[string]string{"X": "7GB"}); got != "a 7GB z" {
		t.Errorf("expandEnv = %q", got)
	}
}

// TestNATSProvisionContract aplica el streams.yaml real (C4) y kv.yaml a un
// NATS en proceso dos veces (idempotencia) y comprueba streams, durable y KV.
func TestNATSProvisionContract(t *testing.T) {
	s, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(),
		JetStreamMaxStore: 1 << 40, NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats not ready")
	}
	t.Cleanup(s.Shutdown)

	// JetStream reserva max_bytes de cada stream contra el disco libre (≈16 GiB en I1 sin
	// TLM_FLOWS): el test usa el contrato real con GiB → MiB para caber en cualquier máquina.
	raw, err := os.ReadFile(repoFile(t, "packages/events/streams/streams.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	streams := filepath.Join(t.TempDir(), "streams.yaml")
	if err := os.WriteFile(streams, []byte(strings.ReplaceAll(string(raw), "GiB", "MiB")), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--url", s.ClientURL(), "--streams", streams,
		"--kv", repoFile(t, "infrastructure/nats/kv.yaml"), "--increment", "I1"}
	env := []string{"HORUS_TLM_FLOWS_MAX_BYTES=268435456"}
	// Instalación anterior: TLM_FLOWS sin compresión ni límite. La actualización
	// activa s2 sin recrear el stream.
	if nc, err := nats.Connect(s.ClientURL()); err == nil {
		js, _ := jetstream.New(nc)
		if _, err := js.CreateStream(context.Background(), jetstream.StreamConfig{Name: "TLM_FLOWS",
			Subjects: []string{"horus.telemetry.flows.>"}, Storage: jetstream.FileStorage, MaxBytes: -1}); err != nil {
			t.Fatal(err)
		}
		nc.Close()
	}
	for i := range 2 {
		var out, errb bytes.Buffer
		if code := natsProvision(context.Background(), args, env, &out, &errb); code != exitOK {
			t.Fatalf("run %d: code %d: %s", i, code, errb.String())
		}
		if i == 1 && !strings.Contains(out.String(), "stream TLM_FLOWS: actualizado") {
			t.Errorf("second run should update, got:\n%s", out.String())
		}
	}

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	ctx := context.Background()
	st, err := js.Stream(ctx, "TLM_FLOWS")
	if err != nil {
		t.Fatal(err)
	}
	info := st.CachedInfo().Config
	if info.MaxBytes != 268435456 || info.MaxAge != 24*time.Hour || info.MaxMsgSize != 1<<20 || !info.AllowDirect ||
		info.Compression != jetstream.S2Compression {
		t.Errorf("TLM_FLOWS config = %+v", info)
	}
	if _, err := js.Stream(ctx, "SNMP_EVENTS"); err == nil {
		t.Error("SNMP_EVENTS (since I2) should not exist in I1")
	}
	if _, err := js.Consumer(ctx, "TLM_FLOWS", "flows-ingester"); err != nil {
		t.Errorf("durable flows-ingester: %v", err)
	}
	for _, b := range []string{"flow_exporter_state", "flows_ingester_groups"} {
		if _, err := js.KeyValue(ctx, b); err != nil {
			t.Errorf("kv %s: %v", b, err)
		}
	}
}
