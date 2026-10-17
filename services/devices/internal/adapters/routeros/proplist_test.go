package routeros

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
)

// En RouterOS 7.12 real (CHR del laboratorio), `.proplist` con una propiedad que la versión no
// tiene trunca la respuesta (solo vuelven las propiedades anteriores, sin error) y
// `GET /rest/system/resource?.proplist=…` no responde nunca: esos menús se piden sin `.proplist`.
func TestNoProplistOnVersionDependentMenus(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	queries := map[string]string{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/rest/")
		mu.Lock()
		queries[path] = r.URL.Query().Get(".proplist")
		mu.Unlock()
		b, err := os.ReadFile(filepath.Join("testdata", "chr-7.12", strings.ReplaceAll(path, "/", "_")+".json"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	c := New(Target{BaseURL: srv.URL, User: "horus", Password: "x"}, 5*time.Second, nil)
	if _, err := c.ReadFacts(context.Background()); err != nil {
		t.Fatal(err)
	}
	rd := Reader{BaseURL: func(string) string { return srv.URL }}
	if _, err := rd.ReadNetwatch(context.Background(), app.ReadTarget{TunnelIP: "x", User: "horus", Password: "x",
		Pinned: Fingerprint(srv.Certificate().Raw)}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, p := range []string{"system/resource", "ppp/profile", "ipv6/dhcp-server", "tool/netwatch"} {
		if q, ok := queries[p]; !ok || q != "" {
			t.Errorf("%s: .proplist=%q (leído=%v)", p, q, ok)
		}
	}
	if queries["ip/pool"] != "name,ranges" {
		t.Errorf("ip/pool: .proplist=%q", queries["ip/pool"])
	}
}
