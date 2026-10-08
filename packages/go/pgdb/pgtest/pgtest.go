// Package pgtest da a los tests de integración (build tag `integration`) una
// base PostgreSQL real y vacía por test (docs/conventions.md §5,
// testcontainers-go).
//
// Si HORUS_TEST_POSTGRES_DSN está definida (p. ej. el PostgreSQL del compose
// de `make up`, con un usuario administrador), se usa ese servidor; si no, se
// arranca un contenedor `postgres:18.6-alpine` compartido por el binario de
// test. Cada llamada a [New] crea una base nueva con nombre aleatorio y la
// borra al terminar el test.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Image es la imagen de PostgreSQL de los tests (la misma versión que el
// compose de desarrollo).
const Image = "postgres:18.6-alpine"

// EnvDSN es la variable que apunta a un servidor existente.
const EnvDSN = "HORUS_TEST_POSTGRES_DSN"

var (
	once     sync.Once
	adminDSN string
	startErr error
)

// server devuelve el DSN de administración del servidor compartido.
func server() (string, error) {
	once.Do(func() {
		if dsn := os.Getenv(EnvDSN); dsn != "" {
			adminDSN = dsn
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		// testcontainers entra en pánico si no encuentra Docker: se convierte
		// en error para omitir el test.
		defer func() {
			if r := recover(); r != nil {
				startErr = fmt.Errorf("pgtest: docker: %v", r)
			}
		}()
		c, err := tcpostgres.Run(ctx, Image,
			tcpostgres.WithDatabase("horus"),
			tcpostgres.WithUsername("horus"),
			tcpostgres.WithPassword("horus-test"),
			testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(2*time.Minute)),
		)
		if err != nil {
			startErr = fmt.Errorf("pgtest: start postgres: %w", err)
			return
		}
		// El contenedor vive lo que el binario de test (Ryuk lo recoge).
		adminDSN, startErr = c.ConnectionString(ctx, "sslmode=disable")
	})
	return adminDSN, startErr
}

// New crea una base vacía y devuelve su DSN (usuario administrador). Omite el
// test si Docker no está disponible y no hay HORUS_TEST_POSTGRES_DSN.
func New(t testing.TB) string {
	t.Helper()
	dsn, err := server()
	if err != nil {
		t.Skipf("PostgreSQL no disponible: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("pgtest: connect: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "t_" + hex.EncodeToString(b)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("pgtest: create database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c, err := pgx.Connect(ctx, dsn)
		if err != nil {
			return
		}
		defer func() { _ = c.Close(context.Background()) }()
		_, _ = c.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("pgtest: parse dsn: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}
