package pcapread

import (
	"path/filepath"
	"testing"
)

func TestRealFixture(t *testing.T) {
	ds, err := ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "mikrotik-real", "ipfix-nat-20s.pcapng"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 246 {
		t.Fatalf("datagrams = %d, want 246", len(ds))
	}
	if ds[0].Src.Addr().String() != "10.255.3.17" || ds[0].Dst.Port() != 4739 || ds[0].Time.IsZero() {
		t.Fatalf("first = %+v", ds[0].Src)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte{0x0a, 0x0d, 0x0d, 0x0a, 28, 0, 0, 0, 0x4d, 0x3c, 0x2b, 0x1a, 1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 28, 0, 0, 0})
	f.Fuzz(func(_ *testing.T, b []byte) { _, _ = Parse(b) })
}
