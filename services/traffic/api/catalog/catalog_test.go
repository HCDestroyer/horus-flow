package catalog

import (
	"bytes"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
)

func TestSeedSize(t *testing.T) {
	c, err := Seed()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(c.Def.Services); n < 28 || n > 40 {
		t.Fatalf("services = %d, want ≈30", n)
	}
	if n := len(c.Def.Categories); n < 9 || n > 13 {
		t.Fatalf("categories = %d, want ≈10", n)
	}
}

func TestClassify(t *testing.T) {
	c, err := Seed()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		ip     string
		asn    uint32
		proto  uint8
		port   uint16
		svc    string
		method string
		cat    string
	}{
		{"45.57.1.2", 0, 6, 443, "netflix", MethodPrefix, "video_streaming"},
		{"157.240.1.1", 0, 6, 5222, "whatsapp", MethodASNPort, "messaging"},
		{"157.240.1.1", 0, 6, 443, "meta_generic", MethodPrefix, "social"},
		{"1.1.1.1", 0, 17, 53, "cloudflare_dns", MethodPrefix, "network_services"},
		{"162.159.200.1", 0, 17, 123, "ntp", MethodPrefix, "network_services"},
		{"203.0.113.5", 30103, 17, 8801, "zoom", MethodASN, "video_calls"},
		{"203.0.113.5", 0, 6, 443, "web_https", MethodPort, "web"},
		{"203.0.113.5", 0, 6, 22, "", MethodNone, ""},
	}
	for _, k := range cases {
		r := c.Classify(netip.MustParseAddr(k.ip), k.asn, k.proto, k.port)
		if r.Method != k.method || (k.svc != "" && (r.ServiceID != ID("service", k.svc) || r.CategoryID != ID("category", k.cat))) {
			t.Errorf("%s:%d asn=%d ⇒ %+v, want %s/%s", k.ip, k.port, k.asn, r, k.svc, k.method)
		}
	}
	if _, asn, ok := c.LookupPrefix(netip.MustParseAddr("142.250.4.4")); !ok || asn != 15169 {
		t.Fatal("published range ASN fallback")
	}
}

func TestSnapshotRoundTripAndCorrupt(t *testing.T) {
	c, err := Seed()
	if err != nil {
		t.Fatal(err)
	}
	dir := datasets.SnapshotDir{Root: t.TempDir()}
	_, path, err := c.Publish(dir, datasets.SnapshotMeta{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := ReadSnapshot(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if c2.Version() != 1 || len(c2.Def.Services) != len(c.Def.Services) {
		t.Fatalf("round trip version=%d", c2.Version())
	}
	b[len(b)/2] ^= 0xff
	if _, err := ReadSnapshot(bytes.NewReader(b)); err == nil {
		t.Fatal("corrupt snapshot accepted")
	}
	if _, err := ReadSnapshot(bytes.NewReader([]byte("garbage"))); err == nil {
		t.Fatal("garbage accepted")
	}
	_ = filepath.Base(path)
}
