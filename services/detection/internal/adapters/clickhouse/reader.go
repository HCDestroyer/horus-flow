// Package clickhouse es el lector ClickHouse de detection (usuario
// horus_detection, rol horus_detection_role = lector por tenant). Toda
// consulta lleva un filtro explícito `tenant_id = ?` y fija el ajuste
// SQL_horus_tenant que exige la row policy p_tenant (fail-closed si falta,
// docs/database.md §5.1): dos barreras independientes.
package clickhouse

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

// UserDetection es el usuario ClickHouse del módulo.
const UserDetection = "horus_detection"

// ErrUnavailable indica ClickHouse caído o sin respuesta a tiempo.
var ErrUnavailable = errors.New("detection: clickhouse unavailable")

// ErrNoTenantFilter es un error de programación: SQL sin tenant_id.
var ErrNoTenantFilter = errors.New("detection: query without tenant_id filter")

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
		timeout = 30 * time.Second
	}
	return &Reader{conn: conn, timeout: timeout}, nil
}

// Close cierra la conexión.
func (r *Reader) Close() error { return r.conn.Close() }

// Ping comprueba ClickHouse.
func (r *Reader) Ping(ctx context.Context) error { return r.conn.Ping(ctx) }

// query ejecuta sql para tenant y llama a scan por fila.
func (r *Reader) query(ctx context.Context, tenant uuid.UUID, sql string, scan func(driver.Rows) error, args ...any) error {
	if !strings.Contains(sql, "tenant_id = ?") || tenant == uuid.Nil {
		return ErrNoTenantFilter
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	ctx = ch.Context(ctx, ch.WithSettings(ch.Settings{
		"SQL_horus_tenant":   ch.CustomSetting{Value: tenant.String()},
		"max_execution_time": int(r.timeout.Seconds()),
	}))
	rows, err := r.conn.Query(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return fmt.Errorf("detection: scan: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}
