package datasets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Fetcher obtiene el contenido crudo de una fuente. La descarga real (HTTP)
// y los fixtures locales implementan la misma interfaz, de modo que los
// parsers y el pipeline se prueban sin Internet.
type Fetcher interface {
	Fetch(ctx context.Context, src Source) (io.ReadCloser, error)
}

// ErrNotFound indica que la fuente no tiene contenido disponible (p. ej. no
// hay fixture para ella).
var ErrNotFound = errors.New("datasets: contenido no disponible")

// HTTPFetcher descarga por HTTP(S). Respeta el proxy del entorno
// (HTTPS_PROXY) a través de http.DefaultTransport.
type HTTPFetcher struct {
	Client    *http.Client
	UserAgent string
	// Guard, si no es nil, protege las descargas de las fuentes
	// personalizadas (Source.IsCustom): https público, también tras DNS y
	// redirecciones. Las del catálogo son configuración de confianza.
	Guard *EgressGuard
}

// NewHTTPFetcher crea un HTTPFetcher con un timeout total razonable.
func NewHTTPFetcher(userAgent string, timeout time.Duration) *HTTPFetcher {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &HTTPFetcher{Client: &http.Client{Timeout: timeout}, UserAgent: userAgent}
}

// Fetch implementa Fetcher.
func (f *HTTPFetcher) Fetch(ctx context.Context, src Source) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("petición %s: %w", src.ID, err)
	}
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	if f.Guard != nil && src.IsCustom() {
		if err := f.Guard.CheckURL(ctx, req.URL); err != nil {
			return nil, fmt.Errorf("descargar %s: %w", src.ID, err)
		}
		client = f.Guard.guardedClient(client, req)
	}
	resp, err := client.Do(req) //nolint:gosec // URL declarada en la configuración de fuentes
	if err != nil {
		return nil, fmt.Errorf("descargar %s: %w", src.ID, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("descargar %s: HTTP %d", src.ID, resp.StatusCode)
	}
	return resp.Body, nil
}

// DirFetcher sirve fuentes desde un directorio local: el contenido de la
// fuente "x" es el archivo "x" o el primero que coincide con "x.*". Se usa con
// los fixtures de tests/fixtures/ y para cargas manuales sin red.
type DirFetcher struct {
	Dir string
}

// Fetch implementa Fetcher.
func (f DirFetcher) Fetch(_ context.Context, src Source) (io.ReadCloser, error) {
	candidates := []string{filepath.Join(f.Dir, src.ID)}
	if m, err := filepath.Glob(filepath.Join(f.Dir, src.ID+".*")); err == nil {
		candidates = append(candidates, m...)
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return os.Open(p) //nolint:gosec // ruta construida desde el id validado
		}
	}
	return nil, fmt.Errorf("%w: %s en %s", ErrNotFound, src.ID, f.Dir)
}
