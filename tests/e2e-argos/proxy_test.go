//go:build e2e

package e2eargos

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fallbackProxy está delante de la API real de ArgosISP y responde 404 al
// resolver por lotes, como una ArgosISP anterior a A1 (contrato §2.1,
// compatibilidad): así se prueba el respaldo GET /ip/:ip de Horus contra
// los datos reales.
type fallbackProxy struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newFallbackProxy(t *testing.T, target string) *fallbackProxy {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	p := &fallbackProxy{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.seen = append(p.seen, r.URL.Path)
		p.mu.Unlock()
		if r.URL.Path == "/api/v1/integraciones/horus/resolver" {
			http.NotFound(w, r)
			return
		}
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *fallbackProxy) hits(prefix string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, s := range p.seen {
		if strings.HasPrefix(s, prefix) {
			n++
		}
	}
	return n
}
