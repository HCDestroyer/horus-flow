package natsx_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hcdestroyer/horus-flow/packages/go/archtest"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
)

func TestStreamsInSyncWithContract(t *testing.T) {
	root, err := archtest.FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(root, "packages/events/streams/streams.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, natsx.StreamsYAML()) {
		t.Fatal("packages/go/natsx/streams.v0.yaml diverge del contrato: cópielo de packages/events/streams/streams.yaml")
	}
	defs, err := natsx.ContractStreams()
	if err != nil || len(defs) < 10 {
		t.Fatalf("streams: %d %v", len(defs), err)
	}
}

func TestPermanent(t *testing.T) {
	base := errors.New("x")
	if !natsx.IsPermanent(natsx.Permanent(base)) || natsx.IsPermanent(base) || natsx.Permanent(nil) != nil {
		t.Fatal("Permanent")
	}
	if !errors.Is(natsx.Permanent(base), base) {
		t.Fatal("unwrap")
	}
}

// El compose de producción pasa la URL de NATS (con contraseña) como secreto:
// HORUS_NATS_URL_FILE. Sin resolverlo, el relay del outbox, el descubrimiento
// de clientes y el WebSocket quedaban inactivos en una instalación real.
func TestEnvValueFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "nats_url")
	if err := os.WriteFile(f, []byte("nats://u:p@nats:4222\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HORUS_NATS_URL=nats://old:4222", "HORUS_NATS_URL_FILE=" + f, "HORUS_PROCESS=x"}
	if got := natsx.EnvValue(env, "HORUS_NATS_URL"); got != "nats://u:p@nats:4222" {
		t.Fatalf("EnvValue = %q", got)
	}
	if got := natsx.EnvValue(env, "HORUS_PROCESS"); got != "x" {
		t.Fatalf("EnvValue sin _FILE = %q", got)
	}
	if got := natsx.EnvValue([]string{"HORUS_NATS_URL_FILE=/no/existe"}, "HORUS_NATS_URL"); got != "" {
		t.Fatalf("archivo ilegible = %q", got)
	}
}
