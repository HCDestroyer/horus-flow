package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	collectorapi "github.com/hcdestroyer/horus-flow/services/collector/api"
)

type memUnregistered struct {
	rows []collectorapi.Unregistered
	err  error
}

func (m memUnregistered) ListUnregistered(context.Context) ([]collectorapi.Unregistered, error) {
	return append([]collectorapi.Unregistered(nil), m.rows...), m.err
}

func setupUnregistered(t *testing.T, lister Unregistered) (http.Handler, *authz.Signer) {
	t.Helper()
	key, err := authz.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := authz.NewSigner(key, "horus-auth", time.Minute, nil)
	ks := authz.KeySet{}
	ks.Add(signer.PublicKey())
	inv, err := flowinv.New(flowinv.Data{Exporters: []flowinv.Exporter{
		{TenantID: tenantA, RouterID: routerA, SiteID: uuid.New(), TunnelIP: netip.MustParseAddr("10.255.0.1")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	mux := httpx.NewMux(nil, slog.New(slog.DiscardHandler))
	New(authz.NewGuard(authz.NewVerifier(ks, "horus-auth", nil)), flowinv.NewStore(inv), memStates{}, lister, nil).
		Mount(mux.ForService("ingester"))
	return mux.Handler(), signer
}

func platformToken(t *testing.T, s *authz.Signer, perms map[string][]string) string {
	t.Helper()
	tok, _, err := s.Sign(authz.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: uuid.NewString()},
		Typ: authz.TypeUser, Scope: authz.ScopePlatform, Perms: perms})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

type unregPage struct {
	Data []map[string]any `json:"data"`
	Page struct {
		NextCursor *string `json:"next_cursor"`
		HasMore    bool    `json:"has_more"`
		Limit      int     `json:"limit"`
		Total      *int    `json:"total"`
	} `json:"page"`
}

const unregPath = "/api/v1/platform/exporters/unregistered"

func TestUnregisteredExportersAPI(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	rows := collectorapi.MergeUnregistered([]collectorapi.Unregistered{
		{ExporterIP: "203.0.113.9", FirstSeenAt: t0, LastSeenAt: t0.Add(3 * time.Minute), Datagrams: 12, Collector: "a"},
		{ExporterIP: "198.51.100.7", FirstSeenAt: t0, LastSeenAt: t0.Add(2 * time.Minute), Datagrams: 3, Collector: "a"},
		{ExporterIP: "198.51.100.8", FirstSeenAt: t0, LastSeenAt: t0.Add(2 * time.Minute), Datagrams: 4, Collector: "a"},
		// Ya registrado (túnel de routerA): no aparece.
		{ExporterIP: "10.255.0.1", FirstSeenAt: t0, LastSeenAt: t0.Add(4 * time.Minute), Datagrams: 99, Collector: "a"},
		{ExporterIP: "2001:db8::5", FirstSeenAt: t0, LastSeenAt: t0.Add(time.Minute), Datagrams: 1, Collector: "b"},
	})
	h, s := setupUnregistered(t, memUnregistered{rows: rows})
	tok := platformToken(t, s, map[string][]string{PermPlatformTenantsRead: {"*"}})

	w := do(h, unregPath+"?include_total=true", tok)
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	var p unregPage
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Data) != 4 || p.Page.HasMore || p.Page.Total == nil || *p.Page.Total != 4 || p.Page.Limit != 50 {
		t.Fatalf("page %s", w.Body)
	}
	first := p.Data[0]
	if first["exporter_ip"] != "203.0.113.9" || first["datagrams"] != "12" || first["first_seen_at"] != "2026-10-09T12:00:00.000Z" ||
		first["last_seen_at"] != "2026-10-09T12:03:00.000Z" || len(first) != 4 {
		t.Fatalf("row %v", first)
	}

	// Paginación keyset: 2 + 2, sin repetir ni saltar (empate en last_seen_at).
	var got []string
	next := ""
	for range 3 {
		q := url.Values{"limit": {"2"}}
		if next != "" {
			q.Set("cursor", next)
		}
		w := do(h, unregPath+"?"+q.Encode(), tok)
		if w.Code != http.StatusOK {
			t.Fatalf("page = %d %s", w.Code, w.Body)
		}
		var pg unregPage
		_ = json.Unmarshal(w.Body.Bytes(), &pg)
		for _, r := range pg.Data {
			got = append(got, r["exporter_ip"].(string))
		}
		if !pg.Page.HasMore {
			break
		}
		next = *pg.Page.NextCursor
	}
	want := []string{"203.0.113.9", "198.51.100.7", "198.51.100.8", "2001:db8::5"}
	if len(got) != len(want) {
		t.Fatalf("paged %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("paged %v, want %v", got, want)
		}
	}

	// Errores: cursor ajeno, límite, token de ISP, sin permiso, sin token.
	if w := do(h, unregPath+"?cursor=bogus.sig", tok); w.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor = %d", w.Code)
	}
	if w := do(h, unregPath+"?limit=0", tok); w.Code != http.StatusBadRequest {
		t.Fatalf("bad limit = %d", w.Code)
	}
	if w := do(h, unregPath, token(t, s, tenantA, map[string][]string{PermPlatformTenantsRead: {"*"}, PermFlowsRead: {"*"}})); w.Code != http.StatusForbidden {
		t.Fatalf("tenant token = %d", w.Code)
	}
	if w := do(h, unregPath, platformToken(t, s, map[string][]string{"platform.status.read": {"*"}})); w.Code != http.StatusForbidden {
		t.Fatalf("no perm = %d", w.Code)
	}
	if w := do(h, unregPath, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d", w.Code)
	}
}

func TestUnregisteredExportersEmptyAndUnavailable(t *testing.T) {
	h, s := setupUnregistered(t, nil)
	tok := platformToken(t, s, map[string][]string{PermPlatformTenantsRead: {"*"}})
	w := do(h, unregPath, tok)
	if w.Code != http.StatusOK || w.Body.String() == "" {
		t.Fatalf("empty = %d", w.Code)
	}
	var p unregPage
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || p.Data == nil || len(p.Data) != 0 {
		t.Fatalf("empty body %s", w.Body)
	}
	h, s = setupUnregistered(t, memUnregistered{err: errors.New("nats down")})
	if w := do(h, unregPath, platformToken(t, s, map[string][]string{PermPlatformTenantsRead: {"*"}})); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable = %d", w.Code)
	}
}
