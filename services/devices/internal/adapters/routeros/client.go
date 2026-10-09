// Package routeros es el adaptador de LECTURA de la API REST de RouterOS v7
// (docs/vendors/mikrotik.md §4 y §11.4, ADR-0022): por el túnel WireGuard, con
// el usuario de solo lectura `horus` (grupo horus-ro) y TLS con la huella
// del certificado fijada en el primer contacto (TOFU).
//
// Horus no escribe en el router: el transporte rechaza cualquier método que
// no sea GET (readOnlyTransport) antes de abrir la conexión, y el cliente no
// expone ningún método de escritura.
package routeros

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Errores del adaptador.
var (
	ErrUnreachable        = errors.New("routeros: router unreachable")
	ErrAuth               = errors.New("routeros: authentication failed")
	ErrWriteForbidden     = errors.New("routeros: only read methods are allowed (Horus does not write to the router)")
	ErrFingerprintChanged = errors.New("routeros: TLS fingerprint changed")
)

// FingerprintError lleva la huella observada cuando no coincide con la fijada.
type FingerprintError struct{ Observed string }

func (e *FingerprintError) Error() string { return ErrFingerprintChanged.Error() + ": " + e.Observed }

// Unwrap permite errors.Is(err, ErrFingerprintChanged).
func (e *FingerprintError) Unwrap() error { return ErrFingerprintChanged }

// readOnlyTransport deja pasar solo GET (y HEAD) hacia el router.
type readOnlyTransport struct{ next http.RoundTripper }

// RoundTrip implementa http.RoundTripper.
func (t readOnlyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return nil, fmt.Errorf("%w: %s %s", ErrWriteForbidden, r.Method, r.URL.Path)
	}
	return t.next.RoundTrip(r) //nolint:wrapcheck // error de transporte tal cual
}

// Fingerprint es el SHA-256 del certificado en formato AA:BB:… (mayúsculas).
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// Target es el router a leer.
type Target struct {
	// BaseURL: https://<ip-de-túnel> (o el servidor de fixtures en tests).
	BaseURL  string
	User     string
	Password string
	// Pinned es la huella fijada ("" = primer contacto: se acepta y se devuelve).
	Pinned string
}

// Client lee la API REST de un router.
type Client struct {
	t        Target
	http     *http.Client
	observed string
}

// Dialer permite sustituir la conexión (tests).
type Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

// New crea un cliente de solo lectura para t. timeout acota cada petición
// (RouterOS corta las REST a los 60 s).
func New(t Target, timeout time.Duration, dial Dialer) *Client {
	c := &Client{t: t}
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		// El certificado del router es autofirmado: se verifica por huella
		// fijada (VerifyConnection), no por cadena de confianza.
		InsecureSkipVerify: true, //nolint:gosec // verificación por huella en VerifyConnection
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("routeros: no certificate")
			}
			fp := Fingerprint(cs.PeerCertificates[0].Raw)
			c.observed = fp
			if t.Pinned != "" && !strings.EqualFold(fp, t.Pinned) {
				return &FingerprintError{Observed: fp}
			}
			return nil
		},
	}
	tr := &http.Transport{TLSClientConfig: tlsCfg, ResponseHeaderTimeout: timeout, MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second}
	if dial != nil {
		tr.DialContext = dial
	}
	c.http = &http.Client{Transport: readOnlyTransport{next: tr}, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return c
}

// ObservedFingerprint devuelve la huella vista en la última conexión.
func (c *Client) ObservedFingerprint() string { return c.observed }

// Row es una fila de la API REST (RouterOS devuelve todo como texto).
type Row map[string]string

// Print hace GET /rest/<path>?.proplist=… y devuelve las filas.
func (c *Client) Print(ctx context.Context, path string, proplist ...string) ([]Row, error) {
	u := strings.TrimRight(c.t.BaseURL, "/") + "/rest/" + strings.TrimLeft(path, "/")
	if len(proplist) > 0 {
		u += "?" + url.Values{".proplist": {strings.Join(proplist, ",")}}.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("routeros: %w", err)
	}
	req.SetBasicAuth(c.t.User, c.t.Password)
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		var fe *FingerprintError
		if errors.As(err, &fe) {
			return nil, fe
		}
		if errors.Is(err, ErrWriteForbidden) {
			return nil, ErrWriteForbidden
		}
		return nil, fmt.Errorf("%w: %s", ErrUnreachable, redact(err))
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read body", ErrUnreachable)
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return nil, ErrAuth
	case res.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w: GET %s: HTTP %d", ErrUnreachable, path, res.StatusCode)
	}
	var raw []map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		// Algunos menús (system/resource) devuelven un objeto.
		var one map[string]any
		if err2 := json.Unmarshal(body, &one); err2 != nil {
			return nil, fmt.Errorf("routeros: GET %s: invalid JSON", path)
		}
		raw = []map[string]any{one}
	}
	out := make([]Row, 0, len(raw))
	for _, m := range raw {
		r := Row{}
		for k, v := range m {
			r[k] = fmt.Sprint(v)
		}
		out = append(out, r)
	}
	return out, nil
}

// redact quita credenciales de un error de URL.
func redact(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Op + ": " + ue.Err.Error()
	}
	return err.Error()
}
