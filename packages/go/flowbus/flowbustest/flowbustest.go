// Package flowbustest arranca un servidor NATS con JetStream en proceso
// para los tests de flows (sin contenedores).
package flowbustest

import (
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// Server arranca NATS + JetStream en un puerto libre y devuelve el servidor.
func Server(t testing.TB) *server.Server {
	t.Helper()
	opts := &server.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(),
		NoLog: true, NoSigs: true, MaxPayload: 1 << 20,
	}
	s, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(func() {
		s.Shutdown()
		s.WaitForShutdown()
	})
	return s
}

// URL arranca un servidor y devuelve su URL de cliente.
func URL(t testing.TB) string { return Server(t).ClientURL() }
