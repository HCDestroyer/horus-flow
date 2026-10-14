// Package postgres es el adaptador PostgreSQL del asistente (esquema assist,
// RLS por tenant con SET LOCAL horus.tenant_id; docs/database.md §3).
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/app"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/domain"
)

// Store implementa app.Store.
type Store struct{ db *pgdb.DB }

// New crea el store.
func New(db *pgdb.DB) *Store { return &Store{db: db} }

var _ app.Store = (*Store)(nil)

func tenantTx(ctx context.Context, db *pgdb.DB, tenant uuid.UUID, fn func(pgx.Tx) error) error {
	return db.TenantTx(ctx, pgdb.TenantID(tenant), fn)
}

// GetSettings implementa app.Store.
func (s *Store) GetSettings(ctx context.Context, tenant uuid.UUID) (*domain.Settings, error) {
	var st domain.Settings
	err := tenantTx(ctx, s.db, tenant, func(tx pgx.Tx) error {
		var base, kek *string
		var ct, dek []byte
		err := tx.QueryRow(ctx, `SELECT tenant_id, enabled, provider, model, base_url, api_key_ciphertext, api_key_dek_wrapped,
			api_key_kek_id, effort, daily_request_limit, version, updated_at, updated_by FROM assist.settings WHERE tenant_id = $1`, tenant).
			Scan(&st.TenantID, &st.Enabled, &st.Provider, &st.Model, &base, &ct, &dek, &kek, &st.Effort, &st.DailyRequestLimit,
				&st.Version, &st.UpdatedAt, &st.UpdatedBy)
		if errors.Is(err, pgx.ErrNoRows) {
			return app.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("assist: get settings: %w", err)
		}
		if base != nil {
			st.BaseURL = *base
		}
		if ct != nil && kek != nil {
			st.Sealed = &domain.SealedKey{Ciphertext: ct, DEKWrapped: dek, KEKID: *kek}
			st.APIKeySet = true
		}
		st.Stored = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// PutSettings implementa app.Store (upsert; version sube en cada cambio).
func (s *Store) PutSettings(ctx context.Context, in *domain.Settings) (*domain.Settings, error) {
	var base *string
	if in.BaseURL != "" {
		base = &in.BaseURL
	}
	var ct, dek []byte
	var kek *string
	if in.Sealed != nil {
		ct, dek, kek = in.Sealed.Ciphertext, in.Sealed.DEKWrapped, &in.Sealed.KEKID
	}
	err := tenantTx(ctx, s.db, in.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO assist.settings (tenant_id, enabled, provider, model, base_url, api_key_ciphertext,
			api_key_dek_wrapped, api_key_kek_id, effort, daily_request_limit, version, updated_at, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 1, $11, $12)
			ON CONFLICT (tenant_id) DO UPDATE SET enabled = EXCLUDED.enabled, provider = EXCLUDED.provider, model = EXCLUDED.model,
			  base_url = EXCLUDED.base_url, api_key_ciphertext = EXCLUDED.api_key_ciphertext,
			  api_key_dek_wrapped = EXCLUDED.api_key_dek_wrapped, api_key_kek_id = EXCLUDED.api_key_kek_id, effort = EXCLUDED.effort,
			  daily_request_limit = EXCLUDED.daily_request_limit, version = assist.settings.version + 1,
			  updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by`,
			in.TenantID, in.Enabled, in.Provider, in.Model, base, ct, dek, kek, in.Effort, in.DailyRequestLimit, in.UpdatedAt.UTC(),
			in.UpdatedBy)
		if err != nil {
			return fmt.Errorf("assist: put settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetSettings(ctx, in.TenantID)
}

// RecordAudit implementa app.Store.
func (s *Store) RecordAudit(ctx context.Context, e *domain.AuditEntry) error {
	calls := e.ToolCalls
	if calls == nil {
		calls = []domain.ToolUse{}
	}
	b, err := json.Marshal(calls)
	if err != nil {
		return fmt.Errorf("assist: audit tool calls: %w", err)
	}
	return tenantTx(ctx, s.db, e.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO assist.audit (id, tenant_id, user_id, feature, context_id, provider, model, input_tokens,
			output_tokens, tool_calls, duration_ms, outcome, error_code, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
			e.ID, e.TenantID, e.UserID, e.Feature, e.ContextID, e.Provider, e.Model, e.InputTokens, e.OutputTokens, b, e.DurationMS,
			e.Outcome, e.ErrorCode, e.CreatedAt.UTC())
		if err != nil {
			return fmt.Errorf("assist: insert audit: %w", err)
		}
		return nil
	})
}

// CountRequests implementa app.Store: consultas a la IA del día que cuentan
// para el límite: las que el proveedor respondió (correctas o declinadas) y
// los errores que llegaron a gastar tokens. Un fallo sin tokens (proveedor
// caído, tiempo agotado antes de la primera respuesta) no consume cupo.
func (s *Store) CountRequests(ctx context.Context, tenant uuid.UUID, since time.Time) (int, error) {
	var n int
	err := tenantTx(ctx, s.db, tenant, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT count(*) FROM assist.audit WHERE tenant_id = $1 AND created_at >= $2
			AND feature IN ('finding', 'router')
			AND (outcome IN ('success', 'refused') OR (outcome = 'error' AND input_tokens + output_tokens > 0))`, tenant, since.UTC()).Scan(&n)
		if err != nil {
			return fmt.Errorf("assist: count requests: %w", err)
		}
		return nil
	})
	return n, err
}

// SaveAnalysis implementa app.Store: guarda el análisis y purga los del
// tenant anteriores a purgeBefore (retención) en la misma transacción.
func (s *Store) SaveAnalysis(ctx context.Context, a *app.StoredAnalysis, purgeBefore time.Time) error {
	return tenantTx(ctx, s.db, a.TenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM assist.analysis WHERE tenant_id = $1 AND created_at < $2`, a.TenantID,
			purgeBefore.UTC()); err != nil {
			return fmt.Errorf("assist: purge analyses: %w", err)
		}
		_, err := tx.Exec(ctx, `INSERT INTO assist.analysis (id, tenant_id, user_id, feature, context_id, body, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, a.ID, a.TenantID, a.UserID, a.Feature, a.ContextID, a.Body, a.CreatedAt.UTC())
		if err != nil {
			return fmt.Errorf("assist: insert analysis: %w", err)
		}
		return nil
	})
}

// ListAnalyses implementa app.Store (del más reciente al más antiguo).
func (s *Store) ListAnalyses(ctx context.Context, tenant uuid.UUID, feature, contextID string, since time.Time,
	limit int) ([]json.RawMessage, error) {
	var out []json.RawMessage
	err := tenantTx(ctx, s.db, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT body FROM assist.analysis WHERE tenant_id = $1 AND feature = $2 AND context_id = $3
			AND created_at >= $4 ORDER BY created_at DESC, id DESC LIMIT $5`, tenant, feature, contextID, since.UTC(), limit)
		if err != nil {
			return fmt.Errorf("assist: list analyses: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var b []byte
			if err := rows.Scan(&b); err != nil {
				return fmt.Errorf("assist: scan analysis: %w", err)
			}
			out = append(out, json.RawMessage(b))
		}
		return rows.Err()
	})
	return out, err
}

// ListAudit implementa app.Store.
func (s *Store) ListAudit(ctx context.Context, tenant uuid.UUID, limit int, before *app.AuditCursor) ([]domain.AuditEntry, error) {
	var out []domain.AuditEntry
	err := tenantTx(ctx, s.db, tenant, func(tx pgx.Tx) error {
		q := `SELECT id, tenant_id, user_id, feature, context_id, provider, model, input_tokens, output_tokens, tool_calls,
			duration_ms, outcome, error_code, created_at FROM assist.audit WHERE tenant_id = $1`
		args := []any{tenant}
		if before != nil {
			q += ` AND (created_at, id) < ($2, $3)`
			args = append(args, before.CreatedAt.UTC(), before.ID)
		}
		q += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT %d`, limit)
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("assist: list audit: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var e domain.AuditEntry
			var calls []byte
			if err := rows.Scan(&e.ID, &e.TenantID, &e.UserID, &e.Feature, &e.ContextID, &e.Provider, &e.Model, &e.InputTokens,
				&e.OutputTokens, &calls, &e.DurationMS, &e.Outcome, &e.ErrorCode, &e.CreatedAt); err != nil {
				return fmt.Errorf("assist: scan audit: %w", err)
			}
			_ = json.Unmarshal(calls, &e.ToolCalls)
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// CreateMCPToken implementa app.Store.
func (s *Store) CreateMCPToken(ctx context.Context, t *domain.MCPToken) error {
	return tenantTx(ctx, s.db, t.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO assist.mcp_token (id, tenant_id, user_id, name, token_hash, prefix, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, t.ID, t.TenantID, t.UserID, t.Name, t.Hash, t.Prefix, t.CreatedAt.UTC(),
			t.ExpiresAt.UTC())
		if err != nil {
			return fmt.Errorf("assist: create mcp token: %w", err)
		}
		return nil
	})
}

const tokenCols = `id, tenant_id, user_id, name, token_hash, prefix, created_at, expires_at, last_used_at, revoked_at`

func scanToken(row pgx.Row) (*domain.MCPToken, error) {
	var t domain.MCPToken
	if err := row.Scan(&t.ID, &t.TenantID, &t.UserID, &t.Name, &t.Hash, &t.Prefix, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt,
		&t.RevokedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

// ListMCPTokens implementa app.Store.
func (s *Store) ListMCPTokens(ctx context.Context, tenant uuid.UUID) ([]domain.MCPToken, error) {
	var out []domain.MCPToken
	err := tenantTx(ctx, s.db, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+tokenCols+` FROM assist.mcp_token WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT 200`,
			tenant)
		if err != nil {
			return fmt.Errorf("assist: list mcp tokens: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanToken(rows)
			if err != nil {
				return fmt.Errorf("assist: scan mcp token: %w", err)
			}
			out = append(out, *t)
		}
		return rows.Err()
	})
	return out, err
}

// RevokeMCPToken implementa app.Store.
func (s *Store) RevokeMCPToken(ctx context.Context, tenant, id uuid.UUID, at time.Time) error {
	return tenantTx(ctx, s.db, tenant, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE assist.mcp_token SET revoked_at = coalesce(revoked_at, $3) WHERE tenant_id = $1 AND id = $2`,
			tenant, id, at.UTC())
		if err != nil {
			return fmt.Errorf("assist: revoke mcp token: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return app.ErrNotFound
		}
		return nil
	})
}

// MCPTokenByHash implementa app.Store (rol de plataforma: aún no se conoce el tenant).
func (s *Store) MCPTokenByHash(ctx context.Context, hash []byte) (*domain.MCPToken, error) {
	var t *domain.MCPToken
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var err error
		t, err = scanToken(tx.QueryRow(ctx, `SELECT `+tokenCols+` FROM assist.mcp_token WHERE token_hash = $1`, hash))
		if errors.Is(err, pgx.ErrNoRows) {
			return app.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("assist: mcp token by hash: %w", err)
		}
		return nil
	})
	return t, err
}

// TouchMCPToken implementa app.Store.
func (s *Store) TouchMCPToken(ctx context.Context, id uuid.UUID, at time.Time) error {
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE assist.mcp_token SET last_used_at = $2 WHERE id = $1`, id, at.UTC()); err != nil {
			return fmt.Errorf("assist: touch mcp token: %w", err)
		}
		return nil
	})
}
