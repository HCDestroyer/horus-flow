package feedsync

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
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
)

type memFetcher map[string]string

func (m memFetcher) Fetch(_ context.Context, s datasets.Source) (io.ReadCloser, error) {
	body, ok := m[s.ID]
	if !ok {
		return nil, errors.New("caída")
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

// parseLines: "ip expira-en-horas" por línea; "x" es inválido.
func parseLines(path string, src datasets.Source, fetchedAt time.Time) ([]reputation.Entry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []reputation.Entry
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Fields(l)
		a, err := netip.ParseAddr(f[0])
		if err != nil {
			return nil, errors.New("corrupto")
		}
		ind := reputation.Indicator{Source: src.ID, Category: reputation.CategoryBotnetCC, Confidence: 90, FirstSeen: fetchedAt}
		if len(f) > 1 {
			d, _ := time.ParseDuration(f[1])
			ind.ExpiresAt = fetchedAt.Add(d)
		}
		out = append(out, reputation.Entry{Prefix: netip.PrefixFrom(a, a.BitLen()), Indicator: ind})
	}
	return out, nil
}

func source(id string, cu datasets.CommercialUse) datasets.Source {
	return datasets.Source{ID: id, Kind: SourceKind, URL: "https://example.org/" + id, Format: "x", License: "L",
		LicenseURL: "https://example.org/l", CommercialUse: cu, Frequency: datasets.Duration(time.Hour)}
}

func TestFetchAndCompile(t *testing.T) {
	now := time.Date(2026, 10, 8, 4, 0, 0, 0, time.UTC)
	fetch := memFetcher{"a": "192.0.2.1\n192.0.2.2 1h\n", "b": "198.51.100.1\n"}
	svc := &Service{Store: &datasets.Store{Root: t.TempDir()}, Fetcher: fetch, Parse: parseLines, Now: func() time.Time { return now }}
	srcs := []datasets.Source{source("a", datasets.CommercialYes), source("b", datasets.CommercialYes), source("c", datasets.CommercialYes)}
	other := source("asn", datasets.CommercialYes)
	other.Kind = "asn"
	srcs = append(srcs, other)

	if _, _, err := svc.Compile(srcs); !errors.Is(err, ErrNoSources) {
		t.Fatalf("sin datasets: %v", err)
	}
	res := svc.Fetch(context.Background(), srcs, nil)
	if len(res) != 3 || res[0].Status != datasets.StatusUpdated || res[2].Status != datasets.StatusFailed {
		t.Fatalf("Fetch = %+v", res)
	}

	// b llega corrupto: se conserva la versión anterior.
	fetch["b"] = "x\n"
	if r := svc.Fetch(context.Background(), srcs[1:2], nil); r[0].Status != datasets.StatusFailed {
		t.Fatalf("corrupto: %+v", r)
	}

	// Dos horas después la entrada con TTL de 1 h caduca.
	now = now.Add(2 * time.Hour)
	snap, reports, err := svc.Compile(srcs)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Len() != 2 || len(snap.Meta.Sources) != 2 || snap.Meta.Sources[0].ID != "a" {
		t.Fatalf("snapshot %d prefijos, fuentes %+v", snap.Len(), snap.Meta.Sources)
	}
	if reports[0].Expired != 1 || !reports[1].Used || reports[2].Used {
		t.Fatalf("reports = %+v", reports)
	}
	if hits := snap.Lookup(netip.MustParseAddr("198.51.100.1")); len(hits) != 1 {
		t.Fatal("la última versión válida de b debería estar en el snapshot")
	}

	// Si la licencia de a pasa a "no", sus datos ya guardados dejan de usarse.
	srcs[0].CommercialUse = datasets.CommercialNo
	snap, _, err = svc.Compile(srcs)
	if err != nil {
		t.Fatal(err)
	}
	if hits := snap.Lookup(netip.MustParseAddr("192.0.2.1")); hits != nil {
		t.Fatal("una fuente sin uso comercial no debe entrar en el snapshot")
	}
}
