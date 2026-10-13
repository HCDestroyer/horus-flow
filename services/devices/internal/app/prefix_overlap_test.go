package app

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

type fakeFinder struct {
	clash *PrefixClash
	err   error
}

func (f fakeFinder) OverlappingPrefix(context.Context, pgdb.TenantID, uuid.UUID, string, netip.Prefix) (*PrefixClash, error) {
	return f.clash, f.err
}

func cp(site uuid.UUID, p string) *domain.ClientPrefix {
	return &domain.ClientPrefix{SiteID: site, RealmKind: "node_private", Prefix: netip.MustParsePrefix(p)}
}

// El 409 de solape dice con qué prefijo choca, su rol y dónde está, y lo
// repite como error del campo para que el formulario lo muestre.
func TestOverlapConflictNamesTheClash(t *testing.T) {
	t.Parallel()
	site, other := uuid.New(), uuid.New()
	cases := []struct {
		name  string
		clash PrefixClash
		want  []string
	}{
		{"mismo nodo", PrefixClash{Prefix: netip.MustParsePrefix("10.0.0.0/8"), Role: "customers", SiteID: site, SiteName: "Central"},
			[]string{"Solapa con 10.0.0.0/8 (Clientes)", "en este nodo"}},
		{"excluido", PrefixClash{Prefix: netip.MustParsePrefix("10.20.0.0/20"), Role: "excluded", SiteID: site},
			[]string{"10.20.0.0/20, que está excluido", "cámbiale el rol"}},
		{"otro nodo", PrefixClash{Prefix: netip.MustParsePrefix("190.0.0.0/22"), Role: "infrastructure", SiteID: other, SiteName: "Norte"},
			[]string{`en el nodo "Norte"`, "(Infraestructura)", "dos nodos"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := overlapConflict(context.Background(), fakeFinder{clash: &tc.clash}, pgdb.TenantID{}, []*domain.ClientPrefix{cp(site, "10.20.1.0/24")},
				func(int) string { return "prefix" })
			e, ok := apperr.As(err)
			if !ok || e.Status() != 409 || e.Code != domain.CodeClientPrefixOverlap {
				t.Fatalf("err = %v", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(e.Detail, w) {
					t.Errorf("detail %q no contiene %q", e.Detail, w)
				}
			}
			if len(e.Fields) != 1 || e.Fields[0].Field != "prefix" || e.Fields[0].Code != "OVERLAP" || e.Fields[0].Message != e.Detail {
				t.Fatalf("fields = %+v", e.Fields)
			}
		})
	}
}

// En un lote, el campo señala el elemento que choca; si el choque es entre
// dos del mismo lote, lo dice; si la búsqueda falla, queda el mensaje genérico.
func TestOverlapConflictBatchAndFallback(t *testing.T) {
	t.Parallel()
	site := uuid.New()
	field := func(i int) string { return "items[" + string(rune('0'+i)) + "].prefix" }
	batch := []*domain.ClientPrefix{cp(site, "10.1.0.0/16"), cp(site, "10.2.0.0/16"), cp(site, "10.2.5.0/24")}

	err := overlapConflict(context.Background(), fakeFinder{err: domain.ErrNotFound}, pgdb.TenantID{}, batch, field)
	e, _ := apperr.As(err)
	if e == nil || e.Fields[0].Field != "items[2].prefix" || !strings.Contains(e.Detail, "10.2.0.0/16, que también está en esta misma selección") {
		t.Fatalf("intra-lote: %v", err)
	}

	err = overlapConflict(context.Background(), fakeFinder{err: errors.New("db down")}, pgdb.TenantID{}, batch[:2], field)
	e, _ = apperr.As(err)
	if e == nil || e.Code != domain.CodeClientPrefixOverlap || len(e.Fields) != 0 || !strings.Contains(e.Detail, "se solapa con otro") {
		t.Fatalf("genérico: %v", err)
	}
}
