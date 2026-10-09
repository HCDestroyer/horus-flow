package clickhouse

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/hcdestroyer/horus-flow/services/ingester/internal/app"
)

// Writer inserta filas en flows.flows_raw por lotes. Es el escritor único
// de la tabla (ADR-0015).
type Writer struct {
	conn driver.Conn
	stmt string
}

// OpenWriter abre una conexión nativa. Si user no está vacío sustituye al
// usuario del DSN (horus_ingester con su contraseña).
func OpenWriter(dsn, user, password string) (*Writer, error) {
	if user != "" {
		u, err := url.Parse(dsn)
		if err != nil {
			return nil, fmt.Errorf("clickhouse dsn: %w", err)
		}
		u.User = url.User(user)
		dsn = u.String()
	}
	opts, err := ch.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("clickhouse dsn: %w", err)
	}
	if password != "" {
		opts.Auth.Password = password
	}
	conn, err := ch.Open(opts)
	if err != nil {
		return nil, err
	}
	return &Writer{conn: conn, stmt: "INSERT INTO flows.flows_raw (" + strings.Join(app.Columns, ", ") + ")"}, nil
}

// Ping comprueba la conexión.
func (w *Writer) Ping(ctx context.Context) error { return w.conn.Ping(ctx) }

// Close cierra la conexión.
func (w *Writer) Close() error { return w.conn.Close() }

// Insert escribe las filas de un lote en un solo INSERT con
// insert_deduplication_token = batch_id: un lote reentregado no duplica
// filas (events.md §5.5).
func (w *Writer) Insert(ctx context.Context, batchID string, rows []app.Row) error {
	if len(rows) == 0 {
		return nil
	}
	ctx = ch.Context(ctx, ch.WithSettings(ch.Settings{"insert_deduplication_token": batchID, "insert_deduplicate": 1}))
	b, err := w.conn.PrepareBatch(ctx, w.stmt)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	for i := range rows {
		if err := b.Append(rows[i].Values()...); err != nil {
			_ = b.Abort()
			return fmt.Errorf("append row: %w", err)
		}
	}
	if err := b.Send(); err != nil {
		return fmt.Errorf("insert flows_raw: %w", err)
	}
	return nil
}
