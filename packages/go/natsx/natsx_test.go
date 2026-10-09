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
