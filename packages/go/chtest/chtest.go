// Package chtest da a los tests de integración (build tag `integration`) de
// flows un ClickHouse real con el esquema de Horus aplicado.
//
// Si HORUS_CH_TEST_DSN está definida (+ HORUS_CH_TEST_PASSWORD_FILE) se usa
// ese servidor; si no, se arranca un contenedor
// clickhouse/clickhouse-server:26.8.20.9 (la versión del compose) compartido
// por el binario de test, con nofile = HORUS_CH_NOFILE (16384 por defecto,
// apto para sandboxes de CI). El esquema usa bases fijas (flows, dim): los
// tests aíslan sus datos por tenant_id.
package chtest

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-units"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/hcdestroyer/horus-flow/packages/go/chmigrate"
)

// Image es la imagen de ClickHouse de los tests.
const Image = "clickhouse/clickhouse-server:26.8.20.9"

// Server es un ClickHouse de test.
type Server struct {
	DSN      string // clickhouse://user@host:port/db (administrador)
	Password string
}

var (
	once     sync.Once
	srv      Server
	startErr error
)

func start() (Server, error) {
	once.Do(func() {
		if dsn := os.Getenv("HORUS_CH_TEST_DSN"); dsn != "" {
			srv.DSN = dsn
			if f := os.Getenv("HORUS_CH_TEST_PASSWORD_FILE"); f != "" {
				b, err := os.ReadFile(f) //nolint:gosec // ruta de test
				if err != nil {
					startErr = err
					return
				}
				srv.Password = strings.TrimSpace(string(b))
			}
			return
		}
		nofile := int64(16384)
		if v, err := strconv.ParseInt(os.Getenv("HORUS_CH_NOFILE"), 10, 64); err == nil && v > 0 {
			nofile = v
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				startErr = fmt.Errorf("chtest: docker: %v", r)
			}
		}()
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			Started: true,
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        Image,
				ExposedPorts: []string{"9000/tcp", "8123/tcp"},
				Env: map[string]string{
					"CLICKHOUSE_DB": "horus", "CLICKHOUSE_USER": "horus", "CLICKHOUSE_PASSWORD": "horus-test",
					"CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT": "1",
				},
				HostConfigModifier: func(hc *container.HostConfig) {
					hc.Ulimits = []*units.Ulimit{{Name: "nofile", Soft: nofile, Hard: nofile}}
				},
				WaitingFor: wait.ForHTTP("/ping").WithPort("8123/tcp").WithStartupTimeout(3 * time.Minute),
			},
		})
		if err != nil {
			startErr = fmt.Errorf("chtest: start clickhouse: %w", err)
			return
		}
		host, err := c.Host(ctx)
		if err != nil {
			startErr = err
			return
		}
		port, err := c.MappedPort(ctx, "9000/tcp")
		if err != nil {
			startErr = err
			return
		}
		srv = Server{DSN: fmt.Sprintf("clickhouse://horus@%s:%s/horus", host, port.Port()), Password: "horus-test"}
	})
	return srv, startErr
}

// Start devuelve un ClickHouse con el esquema migrado por migrate (p. ej.
// ingester.MigrateClickHouse). Omite el test si no hay Docker ni DSN.
func Start(t testing.TB, migrate func(ctx context.Context, dsn, password string) error) Server {
	t.Helper()
	s, err := start()
	if err != nil {
		t.Skipf("ClickHouse no disponible: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := chmigrate.Open(s.DSN, s.Password)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for {
		if err = db.PingContext(ctx); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("chtest: ClickHouse does not answer: %v", err)
		case <-time.After(500 * time.Millisecond):
		}
	}
	if migrate != nil {
		if err := migrate(ctx, s.DSN, s.Password); err != nil {
			t.Fatalf("chtest: migrate: %v", err)
		}
	}
	return s
}
