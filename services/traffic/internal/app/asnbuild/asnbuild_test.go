package asnbuild

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/asn"
)

type memFetcher map[string]string

func (m memFetcher) Fetch(_ context.Context, s datasets.Source) (io.ReadCloser, error) {
	b, ok := m[s.ID]
	if !ok {
		return nil, errors.New("caída")
	}
	return io.NopCloser(strings.NewReader(b)), nil
}

// parse: cada línea "prefijo asn [país]"; el formato decide el papel.
func parse(path string, src datasets.Source) (*SourceData, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d := &SourceData{ASInfo: map[uint32]asn.ASInfo{}}
	switch src.Format {
	case "bgp":
		d.Role = RoleBGP
	case "ip2asn":
		d.Role = RoleIPToASN
	case "rir":
		d.Role = RoleRegistry
	}
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Fields(l)
		p, err := netip.ParsePrefix(f[0])
		if err != nil {
			return nil, err
		}
		if d.Role == RoleRegistry {
			d.Countries = append(d.Countries, CountryBlock{Prefix: p, Country: f[1]})
			continue
		}
		var n uint32
		for _, c := range f[1] {
			n = n*10 + uint32(c-'0')
		}
		d.Routes = append(d.Routes, asn.Entry{Prefix: p, Route: asn.Route{ASN: n, Source: src.ID}})
	}
	return d, nil
}

func source(id, format string) datasets.Source {
	return datasets.Source{ID: id, Kind: SourceKind, Format: format, URL: "https://example.org/" + id, License: "L",
		LicenseURL: "https://example.org/l", CommercialUse: datasets.CommercialYes, Frequency: datasets.Duration(time.Hour)}
}

func TestBuildConsolidationAndReview(t *testing.T) {
	svc := &Service{Store: &datasets.Store{Root: t.TempDir()}, Parse: parse, Fetcher: memFetcher{
		"bgp":    "192.0.2.0/24 64500\n198.51.100.0/26 64501\n",
		"ip2asn": "192.0.2.0/25 64496\n198.51.100.0/24 64497\n203.0.113.0/24 64498\n",
		"rir":    "198.51.100.0/24 MX\n",
	}}
	srcs := []datasets.Source{source("bgp", "bgp"), source("ip2asn", "ip2asn"), source("rir", "rir")}
	if _, _, err := svc.Build(srcs); !errors.Is(err, ErrNoRoutes) {
		t.Fatalf("sin datasets: %v", err)
	}
	for _, r := range svc.Fetch(context.Background(), srcs, nil) {
		if r.Status != datasets.StatusUpdated {
			t.Fatalf("fetch %+v", r)
		}
	}
	snap, reports, err := svc.Build(srcs)
	if err != nil {
		t.Fatal(err)
	}
	// 192.0.2.0/25 de iptoasn queda cubierto por el /24 de BGP: descartado.
	if snap.Len() != 4 || reports[0].Routes != 2 || reports[1].Routes != 2 {
		t.Fatalf("Len %d reports %+v", snap.Len(), reports)
	}
	if r, _ := snap.Lookup(netip.MustParseAddr("192.0.2.1")); r.ASN != 64500 {
		t.Fatalf("BGP debería ganar: %+v", r)
	}
	if r, _ := snap.Lookup(netip.MustParseAddr("198.51.100.200")); r.ASN != 64497 || r.Country != "MX" {
		t.Fatalf("iptoasn + país del RIR: %+v", r)
	}

	dir := datasets.SnapshotDir{Root: t.TempDir()}
	read := func(path string) (*asn.Snapshot, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		return asn.ReadSnapshot(f)
	}
	if m, _, _, err := Publish(dir, snap, false, read); err != nil || m.Version != "v1" {
		t.Fatalf("primera publicación: %v", err)
	}
	// Quitar BGP cambia > 5 %: requiere revisión.
	small, _, err := svc.Build(srcs[1:])
	if err != nil {
		t.Fatal(err)
	}
	if _, _, diff, err := Publish(dir, small, false, read); !errors.Is(err, ErrNeedsReview) || diff.Ratio <= ReviewThreshold {
		t.Fatalf("revisión: %v %+v", err, diff)
	}
	if m, _, _, err := Publish(dir, small, true, read); err != nil || m.Version != "v2" {
		t.Fatalf("aceptado: %v", err)
	}
}
