package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
)

// ScoringState es la fila de detection.customer_scoring: el último scoring
// de un cliente y el estado de su histéresis.
type ScoringState struct {
	CustomerID      uuid.UUID
	RealmID         uuid.UUID
	SiteID          uuid.UUID
	Address         netip.Prefix
	Model           string
	ComputedAt      time.Time
	WindowFrom      time.Time
	WindowTo        time.Time
	CommercialScore int
	RawScore        int
	Level           string
	Confidence      float64
	DynamicPool     bool
	CustomerKind    string
	KindLocked      bool
	Features        json.RawMessage
	Reasons         json.RawMessage
	Published       *string
	PublishedAt     *time.Time
	Pending         *string
	PendingSince    *time.Time
	Version         int
	UpdatedAt       time.Time
}

const scoringCols = `customer_id, realm_id, site_id, address, model_version, computed_at, window_from, window_to, commercial_score,
	raw_score, level, confidence, dynamic_pool, customer_kind, kind_locked, features, reasons, published_level, published_at,
	pending_level, pending_since, version, updated_at`

func scanScoring(row pgx.Row) (*ScoringState, error) {
	var s ScoringState
	var score, raw int16
	err := row.Scan(&s.CustomerID, &s.RealmID, &s.SiteID, &s.Address, &s.Model, &s.ComputedAt, &s.WindowFrom, &s.WindowTo, &score,
		&raw, &s.Level, &s.Confidence, &s.DynamicPool, &s.CustomerKind, &s.KindLocked, &s.Features, &s.Reasons, &s.Published,
		&s.PublishedAt, &s.Pending, &s.PendingSince, &s.Version, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.CommercialScore, s.RawScore = int(score), int(raw)
	return &s, nil
}

// GetScoring devuelve el último scoring de un cliente.
func GetScoring(ctx context.Context, tx pgx.Tx, customer uuid.UUID) (*ScoringState, error) {
	return scanScoring(tx.QueryRow(ctx, `SELECT `+scoringCols+` FROM detection.customer_scoring WHERE customer_id = $1`, customer))
}

// ScoringStates devuelve el estado de todos los clientes puntuados del tenant.
func ScoringStates(ctx context.Context, tx pgx.Tx) (map[uuid.UUID]*ScoringState, error) {
	rows, err := tx.Query(ctx, `SELECT `+scoringCols+` FROM detection.customer_scoring`)
	if err != nil {
		return nil, fmt.Errorf("scoring states: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]*ScoringState{}
	for rows.Next() {
		s, err := scanScoring(rows)
		if err != nil {
			return nil, fmt.Errorf("scoring states: %w", err)
		}
		out[s.CustomerID] = s
	}
	return out, rows.Err()
}

// PutScoring inserta o sustituye el scoring de un cliente.
func PutScoring(ctx context.Context, tx pgx.Tx, t pgdb.TenantID, s *ScoringState) error {
	_, err := tx.Exec(ctx, `INSERT INTO detection.customer_scoring (tenant_id, `+scoringCols+`)
		VALUES ($1, $2, $3, $4, $5::inet, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
		ON CONFLICT (tenant_id, customer_id) DO UPDATE SET realm_id = EXCLUDED.realm_id, site_id = EXCLUDED.site_id,
			address = EXCLUDED.address, model_version = EXCLUDED.model_version, computed_at = EXCLUDED.computed_at,
			window_from = EXCLUDED.window_from, window_to = EXCLUDED.window_to, commercial_score = EXCLUDED.commercial_score,
			raw_score = EXCLUDED.raw_score, level = EXCLUDED.level, confidence = EXCLUDED.confidence,
			dynamic_pool = EXCLUDED.dynamic_pool, customer_kind = EXCLUDED.customer_kind, kind_locked = EXCLUDED.kind_locked,
			features = EXCLUDED.features, reasons = EXCLUDED.reasons, published_level = EXCLUDED.published_level,
			published_at = EXCLUDED.published_at, pending_level = EXCLUDED.pending_level, pending_since = EXCLUDED.pending_since,
			version = EXCLUDED.version, updated_at = EXCLUDED.updated_at`,
		t.UUID(), s.CustomerID, s.RealmID, s.SiteID, s.Address.String(), s.Model, s.ComputedAt, s.WindowFrom, s.WindowTo,
		int16(s.CommercialScore), int16(s.RawScore), s.Level, s.Confidence, s.DynamicPool, s.CustomerKind, s.KindLocked, //nolint:gosec // 0–100
		s.Features, s.Reasons, s.Published, s.PublishedAt, s.Pending, s.PendingSince, s.Version, s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("put scoring: %w", err)
	}
	return nil
}
