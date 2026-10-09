package collector_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowbus/flowbustest"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/pcapread"
	"github.com/hcdestroyer/horus-flow/services/collector"
	"github.com/hcdestroyer/horus-flow/services/collector/api"
)

// TestModuleUDPToJetStream arranca el rol con NATS real (en proceso), envía
// la captura real por UDP desde 127.0.0.1 (registrado como IP de túnel) y
// comprueba los lotes en TLM_FLOWS y el estado en el bucket KV.
func TestModuleUDPToJetStream(t *testing.T) {
	url := flowbustest.URL(t)
	dir := t.TempDir()
	inv := filepath.Join(dir, "inv.yaml")
	if err := os.WriteFile(inv, []byte(`
exporters:
  - tenant_id: 0192e000-0000-7000-8000-000000000001
    router_id: 0192e333-0000-7000-8000-000000000033
    site_id: 0192e222-0000-7000-8000-000000000022
    tunnel_ip: 127.0.0.1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.LocalAddr().String()
	_ = ln.Close()
	env := []string{"HORUS_NATS_URL=" + url, "HORUS_NATS_ENSURE_STREAMS=true", "HORUS_FLOWS_INVENTORY_FILE=" + inv,
		"HORUS_COLLECTOR_LISTEN=" + addr, "HORUS_COLLECTOR_STATE_INTERVAL=200ms", "HORUS_TLM_FLOWS_MAX_BYTES=268435456"}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	mod, err := collector.Register(ctx, module.Deps{Environ: env, Common: config.Common{Env: "dev"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mod.(module.Starter).Start(ctx); err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- mod.Run(runCtx) }()

	ds, err := pcapread.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "mikrotik-real", "ipfix-nat-20s.pcapng"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range ds {
		if _, err := c.Write(d.Payload); err != nil {
			t.Fatal(err)
		}
		if i%20 == 19 {
			time.Sleep(2 * time.Millisecond) // no desbordar el búfer del socket
		}
	}
	_ = c.Close()

	_, js, err := flowbus.Connect(url, "test")
	if err != nil {
		t.Fatal(err)
	}
	var msgs uint64
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		st, err := js.Stream(ctx, flowbus.StreamTelemetry)
		if err == nil {
			info, _ := st.Info(ctx)
			msgs = info.State.Msgs
			if msgs >= 6 { // 2 596 registros en lotes de ≤ 500
				break
			}
		}
	}
	if msgs < 6 {
		t.Fatalf("batches in TLM_FLOWS = %d", msgs)
	}
	kv, err := js.KeyValue(ctx, flowbus.ExporterStateBucket)
	if err != nil {
		t.Fatal(err)
	}
	var fe api.FlowExporter
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if e, err := kv.Get(ctx, "0192e333-0000-7000-8000-000000000033"); err == nil {
			_ = json.Unmarshal(e.Value(), &fe)
			if fe.State != "" && fe.State != api.StatePendingConfiguration {
				break
			}
		}
	}
	// La captura es de 2026-10-09 04:36: su exportTime está desfasado respecto al reloj del test.
	if fe.State != api.StateClockSkew && fe.State != api.StateExporting && fe.State != api.StateLossy {
		t.Fatalf("exporter state = %q", fe.State)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_ = mod.(module.Stopper).Stop(ctx)
}
