package platformevents

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
)

// Schema es el esquema PostgreSQL del registro.
const Schema = "platform_events"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations devuelve las migraciones goose del esquema.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err) //nolint:forbidigo // imposible: el directorio está embebido
	}
	return sub
}

// Migrate aplica las migraciones del registro.
func Migrate(ctx context.Context, db *pgdb.DB, logger *slog.Logger) (int, error) {
	return pgdb.Migrate(ctx, db, Schema, Migrations(), logger) //nolint:wrapcheck // error de pgdb con contexto
}

// Roles de PostgreSQL del registro (los crea la migración): la tabla tiene
// RLS forzada y todo acceso es de plataforma (BYPASSRLS).
const (
	AppRole      = "platform_events_app"
	PlatformRole = "platform_events_platform"
)

// Store es el acceso a platform_events.event.
type Store struct{ db *pgdb.DB }

// NewStore crea el acceso al registro.
func NewStore(db *pgdb.DB) *Store { return &Store{db: db} }

// Insert guarda evs en una transacción (idempotente por id).
func (s *Store) Insert(ctx context.Context, evs []Event) error {
	if len(evs) == 0 {
		return nil
	}
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error { //nolint:wrapcheck // error de pgdb con contexto
		b := &pgx.Batch{}
		for _, ev := range evs {
			details := ev.Details
			if details == nil {
				details = map[string]any{}
			}
			raw, err := json.Marshal(details)
			if err != nil {
				raw = []byte(`{}`)
			}
			b.Queue(`INSERT INTO platform_events.event (id, occurred_at, kind, severity, process, instance, role, version, tenant_id,
				message, details, trace_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) ON CONFLICT (id) DO NOTHING`,
				ev.ID, ev.OccurredAt, ev.Kind, ev.Severity, ev.Process, ev.Instance, ev.Role, ev.Version, ev.TenantID, ev.Message, raw, ev.TraceID)
		}
		if err := tx.SendBatch(ctx, b).Close(); err != nil {
			return fmt.Errorf("platformevents: insert: %w", err)
		}
		return nil
	})
}

// LastLifecycle devuelve el último process_started/process_stopped de
// (process, instance) anterior a before (nil si no hay).
func (s *Store) LastLifecycle(ctx context.Context, process, instance string, before time.Time) (*Event, error) {
	var out *Event
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		evs, err := scanEvents(tx.Query(ctx, `SELECT `+cols+` FROM platform_events.event
			WHERE process = $1 AND instance = $2 AND kind IN ('process_started', 'process_stopped') AND occurred_at < $3
			ORDER BY occurred_at DESC, id DESC LIMIT 1`, process, instance, before))
		if err != nil {
			return err
		}
		if len(evs) == 1 {
			out = &evs[0]
		}
		return nil
	})
	return out, err //nolint:wrapcheck // error de pgdb con contexto
}

// LastStarted devuelve el último process_started de process (cualquier
// instancia) anterior a before: base del diff de configuración.
func (s *Store) LastStarted(ctx context.Context, process string, before time.Time) (*Event, error) {
	var out *Event
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		evs, err := scanEvents(tx.Query(ctx, `SELECT `+cols+` FROM platform_events.event
			WHERE process = $1 AND kind = 'process_started' AND occurred_at < $2 ORDER BY occurred_at DESC, id DESC LIMIT 1`,
			process, before))
		if err != nil {
			return err
		}
		if len(evs) == 1 {
			out = &evs[0]
		}
		return nil
	})
	return out, err //nolint:wrapcheck // error de pgdb con contexto
}

// Purge borra los eventos anteriores a before y devuelve cuántos borró.
func (s *Store) Purge(ctx context.Context, before time.Time) (int64, error) {
	var n int64
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM platform_events.event WHERE occurred_at < $1`, before)
		if err != nil {
			return fmt.Errorf("platformevents: purge: %w", err)
		}
		n = tag.RowsAffected()
		return nil
	})
	return n, err //nolint:wrapcheck // error de pgdb con contexto
}

// Query filtra la lista de eventos (más recientes primero).
type Query struct {
	Kinds    []string
	Severity string // mínima: info < warn < error
	Process  string
	Role     string
	Since    *time.Time
	Until    *time.Time
	// Before/BeforeID: cursor (occurred_at, id) exclusivo.
	Before   *time.Time
	BeforeID uuid.UUID
	Limit    int
}

const cols = `id, occurred_at, kind, severity, process, instance, role, version, tenant_id, message, details, trace_id`

// List devuelve los eventos de q.
func (s *Store) List(ctx context.Context, q Query) ([]Event, error) {
	if q.Limit <= 0 || q.Limit > 1000 {
		q.Limit = 100
	}
	var out []Event
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		args := []any{}
		cond := "TRUE"
		add := func(c string, v ...any) {
			for _, x := range v {
				args = append(args, x)
				c = replaceNth(c, len(args))
			}
			cond += " AND " + c
		}
		if len(q.Kinds) > 0 {
			add("kind = ANY($?)", q.Kinds)
		}
		switch q.Severity {
		case SeverityWarn:
			cond += " AND severity IN ('warn', 'error')"
		case SeverityError:
			cond += " AND severity = 'error'"
		}
		if q.Process != "" {
			add("process = $?", q.Process)
		}
		if q.Role != "" {
			add("role = $?", q.Role)
		}
		if q.Since != nil {
			add("occurred_at >= $?", *q.Since)
		}
		if q.Until != nil {
			add("occurred_at < $?", *q.Until)
		}
		if q.Before != nil {
			add("(occurred_at, id) < ($?, $?)", *q.Before, q.BeforeID)
		}
		args = append(args, q.Limit)
		var err error
		out, err = scanEvents(tx.Query(ctx, fmt.Sprintf(`SELECT `+cols+` FROM platform_events.event WHERE %s
			ORDER BY occurred_at DESC, id DESC LIMIT $%d`, cond, len(args)), args...))
		return err
	})
	return out, err //nolint:wrapcheck // error de pgdb con contexto
}

// replaceNth sustituye el primer "$?" de c por $n.
func replaceNth(c string, n int) string {
	for i := 0; i+1 < len(c); i++ {
		if c[i] == '$' && c[i+1] == '?' {
			return c[:i] + fmt.Sprintf("$%d", n) + c[i+2:]
		}
	}
	return c
}

func scanEvents(rows pgx.Rows, err error) ([]Event, error) {
	if err != nil {
		return nil, fmt.Errorf("platformevents: query: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var ev Event
		var raw []byte
		if err := rows.Scan(&ev.ID, &ev.OccurredAt, &ev.Kind, &ev.Severity, &ev.Process, &ev.Instance, &ev.Role, &ev.Version,
			&ev.TenantID, &ev.Message, &raw, &ev.TraceID); err != nil {
			return nil, fmt.Errorf("platformevents: scan: %w", err)
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &ev.Details)
		}
		ev.OccurredAt = ev.OccurredAt.UTC()
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("platformevents: rows: %w", err)
	}
	return out, nil
}

// ErrUnavailable indica un registro sin base de datos.
var ErrUnavailable = errors.New("platformevents: store unavailable")
