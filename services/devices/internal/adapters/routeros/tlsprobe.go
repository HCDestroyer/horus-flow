package routeros

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"time"

	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
)

var _ app.TLSProber = Reader{}

// ProbeFingerprint implementa app.TLSProber: solo el saludo TLS con el
// servicio www-ssl del router para leer la huella de su certificado. No
// envía ninguna petición HTTP ni las credenciales: una huella sin confirmar
// nunca recibe la contraseña del usuario de solo lectura.
func (r Reader) ProbeFingerprint(ctx context.Context, tunnelIP string) (string, error) {
	base := "https://" + tunnelIP
	if r.BaseURL != nil {
		base = r.BaseURL(tunnelIP)
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", app.ErrRouterUnreachable
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "443")
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var conn net.Conn
	if r.Dial != nil {
		conn, err = r.Dial(ctx, "tcp", host)
	} else {
		var d net.Dialer
		conn, err = d.DialContext(ctx, "tcp", host)
	}
	if err != nil {
		return "", app.ErrRouterUnreachable
	}
	defer func() { _ = conn.Close() }()
	// Se verifica por huella (la confirma un administrador), no por cadena:
	// el certificado del router es autofirmado.
	tc := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}) //nolint:gosec // solo lectura de la huella
	if err := tc.HandshakeContext(ctx); err != nil {
		return "", app.ErrRouterUnreachable
	}
	certs := tc.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.Join(app.ErrRouterUnreachable, errors.New("routeros: no certificate"))
	}
	return Fingerprint(certs[0].Raw), nil
}
