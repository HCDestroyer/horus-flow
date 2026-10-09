// Package grpcx es la base común del gRPC interno (docs/api.md §5.3 y §5.5,
// docs/security.md §5.3): credenciales mTLS desde HORUS_TLS_CA_FILE,
// HORUS_TLS_CERT_FILE y HORUS_TLS_KEY_FILE (modo `mtls`, por defecto) o sin
// TLS (modo `disabled`, solo tests locales y desarrollo), deadline por
// defecto de 2 s en el cliente y servidor con recuperación de pánicos.
package grpcx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// Modos de TLS (GRPC_TLS_MODE).
const (
	ModeMTLS     = "mtls"
	ModeDisabled = "disabled"
)

// DefaultDeadline es el deadline de una llamada sin deadline propio.
const DefaultDeadline = 2 * time.Second

// TLSConfig son las rutas de los certificados del proceso.
type TLSConfig struct {
	Mode     string
	CAFile   string
	CertFile string
	KeyFile  string
	// ServerName esperado en el certificado del servidor (cliente).
	ServerName string
}

func (c TLSConfig) load(server bool) (*tls.Config, error) {
	if c.CAFile == "" || c.CertFile == "" || c.KeyFile == "" {
		return nil, errors.New("grpcx: mtls requires HORUS_TLS_CA_FILE, HORUS_TLS_CERT_FILE and HORUS_TLS_KEY_FILE")
	}
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("grpcx: load cert: %w", err)
	}
	caPEM, err := os.ReadFile(c.CAFile)
	if err != nil {
		return nil, fmt.Errorf("grpcx: read CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("grpcx: CA file has no certificates")
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}}
	if server {
		cfg.ClientCAs, cfg.ClientAuth = pool, tls.RequireAndVerifyClientCert
	} else {
		cfg.RootCAs, cfg.ServerName = pool, c.ServerName
	}
	return cfg, nil
}

// ServerCredentials devuelve las credenciales de un servidor.
func (c TLSConfig) ServerCredentials() (credentials.TransportCredentials, error) {
	if c.Mode == ModeDisabled {
		return insecure.NewCredentials(), nil
	}
	cfg, err := c.load(true)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(cfg), nil
}

// ClientCredentials devuelve las credenciales de un cliente.
func (c TLSConfig) ClientCredentials() (credentials.TransportCredentials, error) {
	if c.Mode == ModeDisabled {
		return insecure.NewCredentials(), nil
	}
	cfg, err := c.load(false)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(cfg), nil
}

// deadline aplica DefaultDeadline a las llamadas sin deadline.
func deadline(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultDeadline)
		defer cancel()
	}
	return invoker(ctx, method, req, reply, cc, opts...)
}

// Dial crea un cliente hacia addr (la conexión se abre al primer uso).
func Dial(addr string, c TLSConfig) (*grpc.ClientConn, error) {
	creds, err := c.ClientCredentials()
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds), grpc.WithChainUnaryInterceptor(deadline))
	if err != nil {
		return nil, fmt.Errorf("grpcx: dial %s: %w", addr, err)
	}
	return conn, nil
}

// NewServer crea un servidor con recuperación de pánicos y deadline por
// defecto en las llamadas entrantes sin deadline.
func NewServer(c TLSConfig, logger *slog.Logger) (*grpc.Server, error) {
	creds, err := c.ServerCredentials()
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	recoverer := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorContext(ctx, "grpc panic", slog.String("method", info.FullMethod), slog.Any("panic", r))
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, DefaultDeadline)
			defer cancel()
		}
		return h(ctx, req)
	}
	return grpc.NewServer(grpc.Creds(creds), grpc.ChainUnaryInterceptor(recoverer)), nil
}

// Serve escucha en addr y sirve s hasta que ctx se cancela (parada ordenada).
func Serve(ctx context.Context, s *grpc.Server, addr string) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("grpcx: listen %s: %w", addr, err)
	}
	errc := make(chan error, 1)
	go func() { errc <- s.Serve(ln) }()
	select {
	case <-ctx.Done():
		done := make(chan struct{})
		go func() { s.GracefulStop(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			s.Stop()
		}
		return nil
	case err := <-errc:
		return fmt.Errorf("grpcx: serve: %w", err)
	}
}
