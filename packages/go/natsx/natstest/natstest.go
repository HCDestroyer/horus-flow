// Package natstest da a los tests de integración (build tag `integration`)
// un NATS JetStream real con los streams del contrato C4
// (packages/events/streams/streams.yaml).
//
// Si HORUS_TEST_NATS_URL está definida (p. ej. el NATS del compose de
// `make up`) se usa ese servidor; si no, se arranca un contenedor
// `nats:2.11-alpine -js` compartido por el binario de test.
package natstest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
)

// Image es la imagen de NATS de los tests.
const Image = "nats:2.11-alpine"

// EnvURL apunta a un servidor existente.
const EnvURL = "HORUS_TEST_NATS_URL"

var (
	once     sync.Once
	url      string
	startErr error
)

func server() (string, error) {
	once.Do(func() {
		if u := os.Getenv(EnvURL); u != "" {
			url = u
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				startErr = fmt.Errorf("natstest: docker: %v", r)
			}
		}()
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image: Image, Cmd: []string{"-js", "-sd", "/data"}, ExposedPorts: []string{"4222/tcp"},
				WaitingFor: wait.ForLog("Server is ready").WithStartupTimeout(time.Minute),
			},
			Started: true,
		})
		if err != nil {
			startErr = fmt.Errorf("natstest: start nats: %w", err)
			return
		}
		ep, err := c.PortEndpoint(ctx, "4222/tcp", "nats")
		if err != nil {
			startErr = err
			return
		}
		url = ep
	})
	return url, startErr
}

// URL devuelve la URL del servidor (omite el test si no hay Docker).
func URL(t testing.TB) string {
	t.Helper()
	u, err := server()
	if err != nil {
		t.Skipf("NATS no disponible: %v", err)
	}
	return u
}

// New conecta, aplica los streams del contrato y devuelve la conexión y su
// JetStream. Cada test debe usar durables con nombre propio.
func New(t testing.TB) (*nats.Conn, jetstream.JetStream) {
	t.Helper()
	nc, err := nats.Connect(URL(t))
	if err != nil {
		t.Fatalf("natstest: connect: %v", err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	defs, err := natsx.LoadStreams(streamsFile(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := natsx.EnsureStreams(ctx, js, defs, 32<<20); err != nil {
		t.Fatal(err)
	}
	return nc, js
}

func streamsFile(t testing.TB) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		p := filepath.Join(dir, "packages/events/streams/streams.yaml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("natstest: packages/events/streams/streams.yaml no encontrado")
		}
		dir = parent
	}
}
