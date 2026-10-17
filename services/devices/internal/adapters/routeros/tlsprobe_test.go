package routeros

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
)

// La captura de la huella es solo el saludo TLS: ninguna petición HTTP llega
// al router (ni, por tanto, la contraseña del usuario de solo lectura).
func TestProbeFingerprintHandshakeOnly(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer srv.Close()
	r := Reader{BaseURL: func(string) string { return srv.URL }}
	fp, err := r.ProbeFingerprint(context.Background(), "10.255.0.2")
	if err != nil || fp != Fingerprint(srv.Certificate().Raw) {
		t.Fatalf("huella = %q %v", fp, err)
	}
	if requests.Load() != 0 {
		t.Fatalf("la captura hizo %d peticiones HTTP", requests.Load())
	}
	srv.Close()
	if _, err := r.ProbeFingerprint(context.Background(), "10.255.0.2"); !errors.Is(err, app.ErrRouterUnreachable) {
		t.Fatalf("caído: %v", err)
	}
}
