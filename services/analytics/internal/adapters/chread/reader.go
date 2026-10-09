// Package chread es el lector ClickHouse de analytics. Toda consulta lleva
// el tenant: se exige un filtro `tenant_id` en el SQL y se fija el ajuste
// SQL_horus_tenant que usa la row policy p_tenant (fail-closed si falta,
// docs/database.md §5.1; test de arquitectura en conventions.md §5).
package chread

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
)

// UserAnalytics es el usuario ClickHouse de analytics (lector por tenant).
const UserAnalytics = "horus_analytics"

// ErrUnavailable indica ClickHouse caído o que no responde a tiempo.
var ErrUnavailable = errors.New("analytics unavailable")

// ErrNoTenantFilter es un error de programación: SQL sin tenant_id.
var ErrNoTenantFilter = errors.New("chread: query without tenant_id filter")

// Reader ejecuta consultas por tenant.
type Reader struct {
	conn    driver.Conn
	timeout time.Duration
}

// Open abre la conexión; user/password sustituyen a los del DSN si no están vacíos.
func Open(dsn, user, password string, timeout time.Duration) (*Reader, error) {
	if user != "" {
		u, err := url.Parse(dsn)
		if err != nil {
			return nil, err
		}
		u.User = url.User(user)
		dsn = u.String()
	}
	opts, err := ch.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	if password != "" {
		opts.Auth.Password = password
	}
	opts.DialTimeout = 5 * time.Second
	conn, err := ch.Open(opts)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Reader{conn: conn, timeout: timeout}, nil
}

// Close cierra la conexión.
func (r *Reader) Close() error { return r.conn.Close() }

// Ping comprueba ClickHouse.
func (r *Reader) Ping(ctx context.Context) error { return r.conn.Ping(ctx) }

// Rows es el resultado de una consulta.
type Rows = driver.Rows

// Query ejecuta sql para tenant. sql debe filtrar por tenant_id.
func (r *Reader) Query(ctx context.Context, tenant uuid.UUID, sql string, args ...any) (Rows, context.CancelFunc, error) {
	if !strings.Contains(sql, "tenant_id = ") || tenant == uuid.Nil {
		return nil, nil, ErrNoTenantFilter
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	ctx = ch.Context(ctx, ch.WithSettings(ch.Settings{
		"SQL_horus_tenant":   ch.CustomSetting{Value: tenant.String()},
		"max_execution_time": int(r.timeout.Seconds()),
	}))
	rows, err := r.conn.Query(ctx, sql, args...)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return rows, cancel, nil
}

// InsertDims escribe las dimensiones de plataforma del catálogo (analytics es
// el escritor único de dim.*, database.md §4): services, categorías y
// organizaciones con su catalog_version (ReplacingMergeTree por versión).
func (r *Reader) InsertDims(ctx context.Context, table string, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	b, err := r.conn.PrepareBatch(ctx, "INSERT INTO "+table)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := b.Append(row...); err != nil {
			_ = b.Abort()
			return err
		}
	}
	return b.Send()
}
