package dashboards

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

// Schema es el esquema PostgreSQL del submódulo.
const Schema = "analytics"

var (
	errNotFound = errors.New("dashboards: not found")
	errVersion  = errors.New("dashboards: version changed")
)

type store struct{ db *pgdb.DB }

const dashCols = `id, tenant_id, owner_id, name, visibility, template_key, template_version, layout, default_range, refresh_seconds,
	variables, widgets, created_at, updated_at, version`

func scanDashboard(row pgx.Row) (*Dashboard, error) {
	var d Dashboard
	var layout, vars, widgets []byte
	err := row.Scan(&d.ID, &d.TenantID, &d.OwnerID, &d.Name, &d.Visibility, &d.TemplateKey, &d.TemplateVersion, &layout,
		&d.DefaultRange, &d.RefreshSeconds, &vars, &widgets, &d.CreatedAt, &d.UpdatedAt, &d.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("dashboards: scan: %w", err)
	}
	if err := json.Unmarshal(layout, &d.Layout); err != nil {
		return nil, fmt.Errorf("dashboards: layout: %w", err)
	}
	_ = json.Unmarshal(vars, &d.Variables)
	if err := json.Unmarshal(widgets, &d.Widgets); err != nil {
		return nil, fmt.Errorf("dashboards: widgets: %w", err)
	}
	if d.Widgets == nil {
		d.Widgets = []Widget{}
	}
	return &d, nil
}

func emit(ctx context.Context, tx pgx.Tx, evs []outbox.Event) error {
	for _, ev := range evs {
		if err := outbox.Insert(ctx, tx, Schema, ev); err != nil {
			return err
		}
	}
	return nil
}

func (s *store) listDashboards(ctx context.Context, t pgdb.TenantID, owner uuid.UUID, vis []string, after *time.Time, afterID uuid.UUID, limit int) ([]Dashboard, error) {
	var out []Dashboard
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		args := []any{t.UUID(), owner, limit}
		cond := ""
		if len(vis) > 0 {
			args = append(args, vis)
			cond += fmt.Sprintf(" AND visibility = ANY($%d)", len(args))
		}
		if after != nil {
			args = append(args, *after, afterID)
			cond += fmt.Sprintf(" AND (created_at, id) > ($%d, $%d)", len(args)-1, len(args))
		}
		rows, err := tx.Query(ctx, `SELECT `+dashCols+` FROM analytics.dashboard
			WHERE (tenant_id IS NULL OR (tenant_id = $1 AND (visibility = 'tenant' OR owner_id = $2)))`+cond+`
			ORDER BY created_at, id LIMIT $3`, args...)
		if err != nil {
			return fmt.Errorf("dashboards: list: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			d, err := scanDashboard(rows)
			if err != nil {
				return err
			}
			out = append(out, *d)
		}
		return rows.Err()
	})
	return out, err
}

func (s *store) getDashboard(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*Dashboard, error) {
	var out *Dashboard
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = scanDashboard(tx.QueryRow(ctx, `SELECT `+dashCols+` FROM analytics.dashboard
			WHERE id = $1 AND (tenant_id IS NULL OR tenant_id = $2)`, id, t.UUID()))
		return err
	})
	return out, err
}

func dashArgs(d *Dashboard) (layout, vars, widgets []byte) {
	layout, _ = json.Marshal(d.Layout)
	vars, _ = json.Marshal(d.Variables)
	widgets, _ = json.Marshal(d.Widgets)
	return
}

func (s *store) insertDashboard(ctx context.Context, t pgdb.TenantID, d *Dashboard, ev func(*Dashboard) []outbox.Event) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		l, v, w := dashArgs(d)
		_, err := tx.Exec(ctx, `INSERT INTO analytics.dashboard (id, tenant_id, owner_id, name, visibility, template_key, template_version,
			layout, default_range, refresh_seconds, variables, widgets, created_at, updated_at, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13, 1)`,
			d.ID, d.TenantID, d.OwnerID, d.Name, d.Visibility, d.TemplateKey, d.TemplateVersion, l, d.DefaultRange, d.RefreshSeconds, v, w, d.CreatedAt)
		if err != nil {
			return fmt.Errorf("dashboards: insert: %w", err)
		}
		return emit(ctx, tx, ev(d))
	})
}

func (s *store) updateDashboard(ctx context.Context, t pgdb.TenantID, d *Dashboard, expect int, ev func(*Dashboard) []outbox.Event) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		l, v, w := dashArgs(d)
		err := tx.QueryRow(ctx, `UPDATE analytics.dashboard SET name = $3, visibility = $4, layout = $5, default_range = $6,
			refresh_seconds = $7, variables = $8, widgets = $9, updated_at = now(), version = version + 1
			WHERE id = $1 AND tenant_id = $2 AND version = $10 RETURNING version, updated_at`,
			d.ID, t.UUID(), d.Name, d.Visibility, l, d.DefaultRange, d.RefreshSeconds, v, w, expect).Scan(&d.Version, &d.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return errVersion
		}
		if err != nil {
			return fmt.Errorf("dashboards: update: %w", err)
		}
		return emit(ctx, tx, ev(d))
	})
}

func (s *store) deleteDashboard(ctx context.Context, t pgdb.TenantID, d *Dashboard, expect int, ev func(*Dashboard) []outbox.Event) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM analytics.dashboard WHERE id = $1 AND tenant_id = $2 AND version = $3`, d.ID, t.UUID(), expect)
		if err != nil {
			return fmt.Errorf("dashboards: delete: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return errVersion
		}
		return emit(ctx, tx, ev(d))
	})
}

// seedTemplates inserta o actualiza las plantillas de sistema (rol de plataforma).
func (s *store) seedTemplates(ctx context.Context, templates []Dashboard) error {
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		for i := range templates {
			d := &templates[i]
			l, v, w := dashArgs(d)
			_, err := tx.Exec(ctx, `INSERT INTO analytics.dashboard (id, tenant_id, owner_id, name, visibility, template_key, template_version,
				layout, default_range, refresh_seconds, variables, widgets, created_at, updated_at, version)
				VALUES ($1, NULL, NULL, $2, 'system', $3, $4, $5, $6, $7, $8, $9, $10, $10, $11)
				ON CONFLICT (id) DO UPDATE SET name = excluded.name, template_version = excluded.template_version, layout = excluded.layout,
					default_range = excluded.default_range, refresh_seconds = excluded.refresh_seconds, variables = excluded.variables,
					widgets = excluded.widgets, updated_at = excluded.updated_at, version = excluded.version
				WHERE analytics.dashboard.version < excluded.version`,
				d.ID, d.Name, d.TemplateKey, d.TemplateVersion, l, d.DefaultRange, d.RefreshSeconds, v, w, d.CreatedAt, d.Version)
			if err != nil {
				return fmt.Errorf("dashboards: seed %s: %w", d.Name, err)
			}
		}
		return nil
	})
}

const playlistCols = `id, tenant_id, name, items, transition, created_at, updated_at, version`

func scanPlaylist(row pgx.Row) (*Playlist, error) {
	var p Playlist
	var items []byte
	err := row.Scan(&p.ID, &p.TenantID, &p.Name, &items, &p.Transition, &p.CreatedAt, &p.UpdatedAt, &p.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("dashboards: scan playlist: %w", err)
	}
	if err := json.Unmarshal(items, &p.Items); err != nil {
		return nil, fmt.Errorf("dashboards: playlist items: %w", err)
	}
	return &p, nil
}

func (s *store) listPlaylists(ctx context.Context, t pgdb.TenantID, after *time.Time, afterID uuid.UUID, limit int) ([]Playlist, error) {
	var out []Playlist
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		args := []any{t.UUID(), limit}
		cond := ""
		if after != nil {
			args = append(args, *after, afterID)
			cond = " AND (created_at, id) > ($3, $4)"
		}
		rows, err := tx.Query(ctx, `SELECT `+playlistCols+` FROM analytics.playlist WHERE tenant_id = $1`+cond+` ORDER BY created_at, id LIMIT $2`, args...)
		if err != nil {
			return fmt.Errorf("dashboards: list playlists: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			p, err := scanPlaylist(rows)
			if err != nil {
				return err
			}
			out = append(out, *p)
		}
		return rows.Err()
	})
	return out, err
}

func (s *store) getPlaylist(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*Playlist, error) {
	var out *Playlist
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = scanPlaylist(tx.QueryRow(ctx, `SELECT `+playlistCols+` FROM analytics.playlist WHERE tenant_id = $1 AND id = $2`, t.UUID(), id))
		return err
	})
	return out, err
}

// visibleDashboards devuelve cuáles de ids existen para el tenant (sistema o del ISP).
func (s *store) visibleDashboards(ctx context.Context, t pgdb.TenantID, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM analytics.dashboard WHERE id = ANY($1) AND (tenant_id IS NULL OR (tenant_id = $2 AND visibility <> 'private'))`,
			ids, t.UUID())
		if err != nil {
			return fmt.Errorf("dashboards: visible: %w", err)
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		for _, id := range ids {
			out[id] = true
		}
		return err
	})
	return out, err
}

func (s *store) savePlaylist(ctx context.Context, t pgdb.TenantID, p *Playlist, expect int, ev func(*Playlist) []outbox.Event) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		items, _ := json.Marshal(p.Items)
		if expect == 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO analytics.playlist (id, tenant_id, name, items, transition, created_at, updated_at, version)
				VALUES ($1, $2, $3, $4, $5, $6, $6, 1)`, p.ID, t.UUID(), p.Name, items, p.Transition, p.CreatedAt); err != nil {
				return fmt.Errorf("dashboards: insert playlist: %w", err)
			}
		} else {
			err := tx.QueryRow(ctx, `UPDATE analytics.playlist SET name = $3, items = $4, transition = $5, updated_at = now(), version = version + 1
				WHERE tenant_id = $1 AND id = $2 AND version = $6 RETURNING version, updated_at`, t.UUID(), p.ID, p.Name, items, p.Transition, expect).
				Scan(&p.Version, &p.UpdatedAt)
			if errors.Is(err, pgx.ErrNoRows) {
				return errVersion
			}
			if err != nil {
				return fmt.Errorf("dashboards: update playlist: %w", err)
			}
		}
		return emit(ctx, tx, ev(p))
	})
}

func (s *store) deletePlaylist(ctx context.Context, t pgdb.TenantID, p *Playlist, expect int, ev func(*Playlist) []outbox.Event) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM analytics.playlist WHERE tenant_id = $1 AND id = $2 AND version = $3`, t.UUID(), p.ID, expect)
		if err != nil {
			return fmt.Errorf("dashboards: delete playlist: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return errVersion
		}
		return emit(ctx, tx, ev(p))
	})
}
