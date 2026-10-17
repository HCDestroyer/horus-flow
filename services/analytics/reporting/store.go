package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
)

// Schema es el esquema PostgreSQL (compartido con dashboards: un único
// juego de migraciones goose por esquema, dashboards.Migrations()).
const Schema = "analytics"

var (
	errNotFound = errors.New("reporting: not found")
	errVersion  = errors.New("reporting: version changed")
)

// Store es el repositorio de definiciones y ejecuciones.
type Store struct{ DB *pgdb.DB }

const defCols = `id, tenant_id, kind, enabled, weekday, hour, minute, timezone, formats, recipients, coalesce(tenant_name, ''),
	coalesce(tenant_slug, ''), created_at, updated_at, version`

func scanDef(row pgx.Row) (*Definition, error) {
	var d Definition
	var wd, h, m int16
	err := row.Scan(&d.ID, &d.TenantID, &d.Kind, &d.Enabled, &wd, &h, &m, &d.Timezone, &d.Formats, &d.Recipients, &d.TenantName,
		&d.TenantSlug, &d.CreatedAt, &d.UpdatedAt, &d.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reporting: scan definition: %w", err)
	}
	d.Weekday, d.Hour, d.Minute = int(wd), int(h), int(m)
	if d.Recipients == nil {
		d.Recipients = []string{}
	}
	return &d, nil
}

const runCols = `id, tenant_id, definition_id, kind, status, period_from, period_to, timezone, formats, files, error, created_by,
	attempts, created_at, started_at, completed_at, expires_at, purged_at`

func scanRun(row pgx.Row) (*Run, error) {
	var r Run
	var files []byte
	err := row.Scan(&r.ID, &r.TenantID, &r.DefinitionID, &r.Kind, &r.Status, &r.PeriodFrom, &r.PeriodTo, &r.Timezone, &r.Formats,
		&files, &r.Error, &r.CreatedBy, &r.Attempts, &r.CreatedAt, &r.StartedAt, &r.CompletedAt, &r.ExpiresAt, &r.PurgedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reporting: scan run: %w", err)
	}
	if err := json.Unmarshal(files, &r.Files); err != nil {
		return nil, fmt.Errorf("reporting: files: %w", err)
	}
	if r.Files == nil {
		r.Files = []File{}
	}
	return &r, nil
}

func collectRuns(rows pgx.Rows) ([]Run, error) {
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// EnsureDefinition crea la definición por defecto de (tenant, kind) si no
// existe (habilitada, lunes 07:00 en tz, PDF y CSV) y la devuelve.
func (s *Store) EnsureDefinition(ctx context.Context, t pgdb.TenantID, kind, tz, name, slug string, now time.Time) (*Definition, error) {
	var out *Definition
	err := s.DB.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = ensureDef(ctx, tx, t, kind, tz, name, slug, now)
		return err
	})
	return out, err
}

func ensureDef(ctx context.Context, tx pgx.Tx, t pgdb.TenantID, kind, tz, name, slug string, now time.Time) (*Definition, error) {
	if tz == "" || !ValidTimezone(tz) {
		tz = "UTC"
	}
	_, err := tx.Exec(ctx, `INSERT INTO analytics.report_definition (id, tenant_id, kind, weekday, hour, minute, timezone, tenant_name,
			tenant_slug, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, ''), nullif($9, ''), $10, $10) ON CONFLICT (tenant_id, kind) DO NOTHING`,
		uuid.Must(uuid.NewV7()), t.UUID(), kind, DefaultWeekday, DefaultHour, DefaultMinute, tz, name, slug, now)
	if err != nil {
		return nil, fmt.Errorf("reporting: ensure definition: %w", err)
	}
	return scanDef(tx.QueryRow(ctx, `SELECT `+defCols+` FROM analytics.report_definition WHERE tenant_id = $1 AND kind = $2`, t.UUID(), kind))
}

// ListDefinitions devuelve las definiciones del tenant.
func (s *Store) ListDefinitions(ctx context.Context, t pgdb.TenantID) ([]Definition, error) {
	var out []Definition
	err := s.DB.TenantTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+defCols+` FROM analytics.report_definition ORDER BY kind`)
		if err != nil {
			return fmt.Errorf("reporting: list definitions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			d, err := scanDef(rows)
			if err != nil {
				return err
			}
			out = append(out, *d)
		}
		return rows.Err()
	})
	return out, err
}

// GetDefinition devuelve una definición del tenant.
func (s *Store) GetDefinition(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*Definition, error) {
	var out *Definition
	err := s.DB.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = scanDef(tx.QueryRow(ctx, `SELECT `+defCols+` FROM analytics.report_definition WHERE id = $1`, id))
		return err
	})
	return out, err
}

// UpdateDefinition guarda d si su versión sigue siendo expect (sube la versión).
func (s *Store) UpdateDefinition(ctx context.Context, t pgdb.TenantID, d *Definition, expect int, now time.Time) error {
	return s.DB.TenantTx(ctx, t, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE analytics.report_definition SET enabled = $3, weekday = $4, hour = $5, minute = $6, timezone = $7,
				formats = $8, recipients = $9, updated_at = $10, version = version + 1
			WHERE id = $1 AND version = $2`, d.ID, expect, d.Enabled, d.Weekday, d.Hour, d.Minute, d.Timezone, d.Formats, d.Recipients, now)
		if err != nil {
			return fmt.Errorf("reporting: update definition: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return errVersion
		}
		d.Version, d.UpdatedAt = expect+1, now
		return nil
	})
}

// RefreshDirectory actualiza la copia de nombre, slug (y, si la definición
// sigue en UTC por defecto, la zona) del directorio de auth sin subir la versión.
func (s *Store) RefreshDirectory(ctx context.Context, t pgdb.TenantID, name, slug string) error {
	return s.DB.TenantTx(ctx, t, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE analytics.report_definition SET tenant_name = nullif($1, ''), tenant_slug = nullif($2, '')
			WHERE tenant_name IS DISTINCT FROM nullif($1, '') OR tenant_slug IS DISTINCT FROM nullif($2, '')`, name, slug)
		if err != nil {
			return fmt.Errorf("reporting: refresh directory: %w", err)
		}
		return nil
	})
}

// EnabledDefinitions lista las definiciones habilitadas de todos los tenants
// (planificador, analytics_platform).
func (s *Store) EnabledDefinitions(ctx context.Context) ([]Definition, error) {
	var out []Definition
	err := s.DB.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+defCols+` FROM analytics.report_definition WHERE enabled ORDER BY tenant_id, kind`)
		if err != nil {
			return fmt.Errorf("reporting: enabled definitions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			d, err := scanDef(rows)
			if err != nil {
				return err
			}
			out = append(out, *d)
		}
		return rows.Err()
	})
	return out, err
}

// DefinitionTenants devuelve los tenants que ya tienen alguna definición.
func (s *Store) DefinitionTenants(ctx context.Context) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	err := s.DB.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT tenant_id FROM analytics.report_definition`)
		if err != nil {
			return fmt.Errorf("reporting: definition tenants: %w", err)
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		for _, id := range ids {
			out[id] = true
		}
		return err
	})
	return out, err
}

// InsertScheduled crea la ejecución programada de (definición, periodo) si
// no existe; created indica si la creó esta llamada (unicidad parcial
// ux_report_run__scheduled: seguro con varias réplicas).
func (s *Store) InsertScheduled(ctx context.Context, d *Definition, p Period, now time.Time) (created bool, id uuid.UUID, err error) {
	id = uuid.Must(uuid.NewV7())
	err = s.DB.TenantTx(ctx, pgdb.TenantID(d.TenantID), func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO analytics.report_run (id, tenant_id, definition_id, kind, status, period_from, period_to,
				timezone, formats, created_at)
			VALUES ($1, $2, $3, $4, 'queued', $5, $6, $7, $8, $9) ON CONFLICT (definition_id, period_from) WHERE definition_id IS NOT NULL DO NOTHING`,
			id, d.TenantID, d.ID, d.Kind, p.From, p.To, p.Loc.String(), d.Formats, now)
		if err != nil {
			return fmt.Errorf("reporting: insert scheduled run: %w", err)
		}
		created = tag.RowsAffected() == 1
		return nil
	})
	return created, id, err
}

// InsertOnDemand crea una ejecución a petición. Con la misma
// Idempotency-Key del mismo usuario devuelve la existente (replayed).
func (s *Store) InsertOnDemand(ctx context.Context, r *Run, idem *uuid.UUID) (out *Run, replayed bool, err error) {
	err = s.DB.TenantTx(ctx, pgdb.TenantID(r.TenantID), func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO analytics.report_run (id, tenant_id, kind, status, period_from, period_to, timezone, formats,
				created_by, idempotency_key, created_at)
			VALUES ($1, $2, $3, 'queued', $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (tenant_id, created_by, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING`,
			r.ID, r.TenantID, r.Kind, r.PeriodFrom, r.PeriodTo, r.Timezone, r.Formats, r.CreatedBy, idem, r.CreatedAt)
		if err != nil {
			return fmt.Errorf("reporting: insert run: %w", err)
		}
		if tag.RowsAffected() == 1 {
			out, err = scanRun(tx.QueryRow(ctx, `SELECT `+runCols+` FROM analytics.report_run WHERE id = $1`, r.ID))
			return err
		}
		replayed = true
		out, err = scanRun(tx.QueryRow(ctx, `SELECT `+runCols+` FROM analytics.report_run
			WHERE created_by = $1 AND idempotency_key = $2`, r.CreatedBy, idem))
		return err
	})
	return out, replayed, err
}

// CountRecentByUser cuenta las ejecuciones a petición de un usuario desde since (rate limit 10/h).
func (s *Store) CountRecentByUser(ctx context.Context, t pgdb.TenantID, user uuid.UUID, since time.Time) (int, time.Time, error) {
	var n int
	var oldest *time.Time
	err := s.DB.TenantTx(ctx, t, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*), min(created_at) FROM analytics.report_run WHERE created_by = $1 AND created_at >= $2`,
			user, since).Scan(&n, &oldest)
	})
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("reporting: count runs: %w", err)
	}
	if oldest == nil {
		return n, time.Time{}, nil
	}
	return n, *oldest, nil
}

// ListRuns devuelve una página de ejecuciones del tenant (más recientes
// primero; limit + 1 filas para saber si hay más).
func (s *Store) ListRuns(ctx context.Context, t pgdb.TenantID, kind string, before *time.Time, beforeID uuid.UUID, limit int) ([]Run, error) {
	var out []Run
	err := s.DB.TenantTx(ctx, t, func(tx pgx.Tx) error {
		args := []any{limit + 1}
		cond := ""
		if kind != "" {
			args = append(args, kind)
			cond += fmt.Sprintf(" AND kind = $%d", len(args))
		}
		if before != nil {
			args = append(args, *before, beforeID)
			cond += fmt.Sprintf(" AND (created_at, id) < ($%d, $%d)", len(args)-1, len(args))
		}
		rows, err := tx.Query(ctx, `SELECT `+runCols+` FROM analytics.report_run WHERE true`+cond+`
			ORDER BY created_at DESC, id DESC LIMIT $1`, args...)
		if err != nil {
			return fmt.Errorf("reporting: list runs: %w", err)
		}
		out, err = collectRuns(rows)
		return err
	})
	return out, err
}

// GetRun devuelve una ejecución del tenant.
func (s *Store) GetRun(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*Run, error) {
	var out *Run
	err := s.DB.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = scanRun(tx.QueryRow(ctx, `SELECT `+runCols+` FROM analytics.report_run WHERE id = $1`, id))
		return err
	})
	return out, err
}

// Claim reclama la ejecución pendiente más antigua (o una running cuyo
// plazo venció: el worker anterior murió) con FOR UPDATE SKIP LOCKED y la
// marca running hasta now+lease. nil si no hay trabajo.
func (s *Store) Claim(ctx context.Context, now time.Time, lease time.Duration, maxAttempts int) (*Run, error) {
	var out *Run
	err := s.DB.PlatformTx(ctx, func(tx pgx.Tx) error {
		r, err := scanRun(tx.QueryRow(ctx, `SELECT `+runCols+` FROM analytics.report_run
			WHERE (status = 'queued' OR (status = 'running' AND claimed_until < $1)) AND attempts < $2
			ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED`, now, maxAttempts))
		if errors.Is(err, errNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE analytics.report_run SET status = 'running', attempts = attempts + 1, claimed_until = $2,
				started_at = coalesce(started_at, $3) WHERE id = $1`, r.ID, now.Add(lease), now); err != nil {
			return fmt.Errorf("reporting: claim: %w", err)
		}
		r.Status, r.Attempts = StatusRunning, r.Attempts+1
		out = r
		return nil
	})
	return out, err
}

// FailExhausted marca failed las ejecuciones que agotaron los intentos
// (un worker murió varias veces generándolas).
func (s *Store) FailExhausted(ctx context.Context, now time.Time, maxAttempts int) ([]Run, error) {
	var out []Run
	err := s.DB.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE analytics.report_run SET status = 'failed', completed_at = $1, claimed_until = NULL,
				error = 'La generación se interrumpió repetidamente (reinicio del proceso o falta de memoria).'
			WHERE status = 'running' AND claimed_until < $1 AND attempts >= $2 RETURNING `+runCols, now, maxAttempts)
		if err != nil {
			return fmt.Errorf("reporting: fail exhausted: %w", err)
		}
		out, err = collectRuns(rows)
		return err
	})
	return out, err
}

// Finish cierra una ejecución (completed con archivos o failed con error) y
// escribe su evento en el outbox en la misma transacción.
func (s *Store) Finish(ctx context.Context, r *Run, ev outbox.Event) error {
	files, _ := json.Marshal(r.Files)
	return s.DB.TenantTx(ctx, pgdb.TenantID(r.TenantID), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE analytics.report_run SET status = $2, files = $3, error = $4, completed_at = $5, expires_at = $6,
				claimed_until = NULL WHERE id = $1`, r.ID, r.Status, files, r.Error, r.CompletedAt, r.ExpiresAt); err != nil {
			return fmt.Errorf("reporting: finish: %w", err)
		}
		return outbox.Insert(ctx, tx, Schema, ev)
	})
}

// Emit escribe eventos en el outbox del tenant (auditoría sin auth local).
func (s *Store) Emit(ctx context.Context, t pgdb.TenantID, evs []outbox.Event) error {
	return s.DB.TenantTx(ctx, t, func(tx pgx.Tx) error {
		for _, ev := range evs {
			if err := outbox.Insert(ctx, tx, Schema, ev); err != nil {
				return err
			}
		}
		return nil
	})
}

// Expired lista hasta limit ejecuciones con archivos caducados (retención).
func (s *Store) Expired(ctx context.Context, now time.Time, limit int) ([]Run, error) {
	var out []Run
	err := s.DB.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+runCols+` FROM analytics.report_run
			WHERE purged_at IS NULL AND expires_at IS NOT NULL AND expires_at <= $1 ORDER BY expires_at LIMIT $2`, now, limit)
		if err != nil {
			return fmt.Errorf("reporting: expired: %w", err)
		}
		out, err = collectRuns(rows)
		return err
	})
	return out, err
}

// MarkPurged registra que los archivos de la ejecución se borraron.
func (s *Store) MarkPurged(ctx context.Context, id uuid.UUID, now time.Time) error {
	return s.DB.PlatformTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE analytics.report_run SET purged_at = $2 WHERE id = $1`, id, now)
		if err != nil {
			return fmt.Errorf("reporting: purge: %w", err)
		}
		return nil
	})
}
