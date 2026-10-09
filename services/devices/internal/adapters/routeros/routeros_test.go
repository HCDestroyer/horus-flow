package routeros

// Suite routeros-import (I1-28): fixtures REST de RouterOS 7.12 y de la rama
// long-term (7.20) servidos por un servidor TLS de pruebas que FALLA el test
// ante cualquier método que no sea GET (criterio 4).

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

var update = flag.Bool("update", false, "regenera los golden de testdata/")

type fixtureServer struct {
	*httptest.Server
	writes atomic.Int32
	gets   atomic.Int32
}

func serve(t *testing.T, version string) *fixtureServer {
	t.Helper()
	fs := &fixtureServer{}
	fs.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			fs.writes.Add(1)
			t.Errorf("escritura hacia RouterOS: %s %s", r.Method, r.URL.Path)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		fs.gets.Add(1)
		if u, p, ok := r.BasicAuth(); !ok || u != "horus" || p != "clave-ro" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		name := strings.ReplaceAll(strings.TrimPrefix(r.URL.Path, "/rest/"), "/", "_") + ".json"
		b, err := os.ReadFile(filepath.Join("testdata", version, name))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":400,"message":"Bad Request","detail":"no such command"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}))
	t.Cleanup(fs.Close)
	return fs
}

func (fs *fixtureServer) fingerprint() string { return Fingerprint(fs.Certificate().Raw) }

func client(fs *fixtureServer, pinned string) *Client {
	return New(Target{BaseURL: fs.URL, User: "horus", Password: "clave-ro", Pinned: pinned}, 5*time.Second, nil)
}

func itemsJSON(items []domain.ImportItem) []byte {
	type j struct {
		Prefix   string  `json:"prefix"`
		Origin   string  `json:"origin"`
		Name     string  `json:"origin_name"`
		Role     string  `json:"suggested_role"`
		Mode     string  `json:"suggested_assignment_mode"`
		Delegate *int    `json:"delegated_prefix_length"`
		V6Len    *int    `json:"suggested_ipv6_client_len"`
		Usage    *string `json:"ipv6_pool_usage"`
		Diff     string  `json:"diff"`
	}
	out := make([]j, 0, len(items))
	for _, it := range items {
		out = append(out, j{it.Prefix.String(), it.Origin, it.OriginName, it.SuggestedRole, it.SuggestedAssignmentMode,
			it.DelegatedPrefixLength, it.SuggestedIPv6ClientLen, it.IPv6PoolUsage, it.Diff})
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return append(b, '\n')
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (go test -update)", err)
	}
	if string(want) != string(got) {
		t.Fatalf("%s distinto del golden:\n%s", name, got)
	}
}

var tunnels = []netip.Prefix{netip.MustParsePrefix("10.255.0.0/16")}

// Criterio 1: fixtures de 7.12 y long-term → propuesta con rol sugerido,
// campos IPv6 (E-IPv6-1) y diferencias con lo existente.
func TestImportFromFixtures(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"7.12", "7.20"} {
		fs := serve(t, v)
		c := client(fs, "")
		f, err := c.ReadFacts(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", v, err)
		}
		if !strings.HasPrefix(f.Version, v) || c.ObservedFingerprint() != fs.fingerprint() {
			t.Fatalf("%s: versión %q, huella %q", v, f.Version, c.ObservedFingerprint())
		}
		existing := []domain.ClientPrefix{{ID: uuid.New(), Prefix: netip.MustParsePrefix("10.20.0.0/24")},
			{ID: uuid.New(), Prefix: netip.MustParsePrefix("100.64.0.0/20")}}
		items := domain.BuildImport(f, existing, tunnels)
		golden(t, "preview-"+v+".golden.json", itemsJSON(items))
		for _, it := range items {
			if tunnels[0].Overlaps(it.Prefix) {
				t.Fatalf("%s: propone el rango de túneles %s", v, it.Prefix)
			}
		}
		if fs.writes.Load() != 0 || fs.gets.Load() != int32(len(Paths)) {
			t.Fatalf("%s: GET=%d escrituras=%d", v, fs.gets.Load(), fs.writes.Load())
		}
	}
}

func find(items []domain.ImportItem, prefix string) *domain.ImportItem {
	for i := range items {
		if items[i].Prefix.String() == prefix {
			return &items[i]
		}
	}
	return nil
}

func TestImportRules(t *testing.T) {
	t.Parallel()
	fs := serve(t, "7.20")
	f, err := client(fs, "").ReadFacts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items := domain.BuildImport(f, nil, tunnels)
	cases := map[string]func(*domain.ImportItem) bool{
		"100.64.0.0/18": func(i *domain.ImportItem) bool { return i.Origin == "ip_pool" && i.SuggestedRole == "customers" },
		"2001:db8:2000::/40": func(i *domain.ImportItem) bool {
			return *i.IPv6PoolUsage == "dhcpv6_pd" && *i.SuggestedIPv6ClientLen == 48
		},
		"2001:db8:fe00::/64": func(i *domain.ImportItem) bool {
			return *i.IPv6PoolUsage == "ppp_link_shared" && i.SuggestedRole == "infrastructure"
		},
		"2001:db8:3000::/44": func(i *domain.ImportItem) bool {
			return *i.IPv6PoolUsage == "dhcpv6_pd" && *i.DelegatedPrefixLength == 60
		},
		"2001:db8:4000::/64": func(i *domain.ImportItem) bool {
			return *i.IPv6PoolUsage == "dhcpv6_address" && i.SuggestedIPv6ClientLen == nil && *i.DelegatedPrefixLength == 128
		},
		"2001:db8:5000::/48": func(i *domain.ImportItem) bool {
			return i.SuggestedIPv6ClientLen == nil && *i.IPv6PoolUsage == "unused"
		},
		"172.16.50.0/24": func(i *domain.ImportItem) bool {
			return i.Origin == "interface_address" && i.SuggestedRole == "infrastructure"
		},
		"203.0.113.0/29": func(i *domain.ImportItem) bool { return i.SuggestedRole == "infrastructure" },
	}
	for pfx, ok := range cases {
		it := find(items, pfx)
		if it == nil || !ok(it) {
			t.Errorf("%s: %+v", pfx, it)
		}
	}
	for _, absent := range []string{"10.255.9.0/24", "172.16.60.0/24", "2001:db8:2000::/48"} {
		if find(items, absent) != nil {
			t.Errorf("propone %s (túnel, interfaz deshabilitada o cubierta por un pool)", absent)
		}
	}
}

// Criterio 4: ningún método de escritura llega al router.
func TestOnlyReadMethods(t *testing.T) {
	t.Parallel()
	fs := serve(t, "7.12")
	c := client(fs, "")
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req, _ := http.NewRequestWithContext(context.Background(), m, fs.URL+"/rest/ip/pool", strings.NewReader(`{"name":"x"}`))
		_, err := c.http.Do(req)
		if !errors.Is(err, ErrWriteForbidden) {
			t.Fatalf("%s permitido: %v", m, err)
		}
	}
	if fs.writes.Load() != 0 || fs.gets.Load() != 0 {
		t.Fatal("una escritura llegó al router")
	}
}

// Criterio 3: huella TLS distinta, router caído o credenciales rechazadas.
func TestFingerprintAndErrors(t *testing.T) {
	t.Parallel()
	fs := serve(t, "7.12")
	if _, err := client(fs, fs.fingerprint()).ReadFacts(context.Background()); err != nil {
		t.Fatalf("huella fijada correcta: %v", err)
	}
	c := client(fs, strings.Repeat("AB:", 31)+"AB")
	_, err := c.ReadFacts(context.Background())
	var fe *FingerprintError
	if !errors.As(err, &fe) || fe.Observed != fs.fingerprint() || !errors.Is(err, ErrFingerprintChanged) {
		t.Fatalf("huella cambiada: %v", err)
	}
	if fs.gets.Load() != int32(len(Paths)) {
		t.Fatal("con huella distinta se enviaron credenciales")
	}
	bad := New(Target{BaseURL: fs.URL, User: "horus", Password: "otra"}, 5*time.Second, nil)
	if _, err := bad.ReadFacts(context.Background()); !errors.Is(err, ErrAuth) {
		t.Fatalf("credenciales: %v", err)
	}
	down := httptest.NewTLSServer(http.NotFoundHandler())
	url := down.URL
	down.Close()
	if _, err := New(Target{BaseURL: url, User: "horus", Password: "clave-ro"}, time.Second, nil).ReadFacts(context.Background()); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("router caído: %v", err)
	}
	// Reader traduce a los errores de la aplicación.
	r := Reader{Timeout: 2 * time.Second, BaseURL: func(string) string { return fs.URL }}
	if _, obs, err := r.Read(context.Background(), app.ReadTarget{TunnelIP: "10.255.3.17", User: "horus", Password: "clave-ro",
		Pinned: strings.Repeat("CD:", 31) + "CD"}); !errors.Is(err, app.ErrRouterFingerprint) || obs != fs.fingerprint() {
		t.Fatalf("reader huella: %v %s", err, obs)
	}
}
