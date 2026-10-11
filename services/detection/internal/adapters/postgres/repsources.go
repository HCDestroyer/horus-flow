package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

// Fuentes de reputación de plataforma (detection.reputation_source): tabla
// sin tenant, siempre con PlatformTx.

const repSourceCols = `id, numeric_id, key, dataset_id, origin, name, url, format, frequency, category, confidence, enabled,
	license, notes, min_entries, max_bytes, auth_header_name, auth_ciphertext, auth_dek_wrapped, auth_kek_id,
	terms_acknowledged_at, created_by, created_at, updated_at, version`

func scanRepSource(row pgx.Row) (*domain.ReputationSource, error) {
	var r domain.ReputationSource
	var ct, dek []byte
	var kek *string
	err := row.Scan(&r.ID, &r.NumericID, &r.Key, &r.DatasetID, &r.Origin, &r.Name, &r.URL, &r.Format, &r.Frequency, &r.Category,
		&r.Confidence, &r.Enabled, &r.License, &r.Notes, &r.MinEntries, &r.MaxBytes, &r.AuthHeaderName, &ct, &dek, &kek,
		&r.TermsAcknowledgedAt, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt, &r.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrSourceNotFound
	}
	if err != nil {
		return nil, err
	}
	if ct != nil && kek != nil {
		r.AuthSecret = &domain.SealedSecret{Ciphertext: ct, DEK: dek, KEKID: *kek}
	}
	r.CreatedAt, r.UpdatedAt = r.CreatedAt.UTC(), r.UpdatedAt.UTC()
	return &r, nil
}

func authCols(r *domain.ReputationSource) (ct, dek []byte, kek *string) {
	if r.AuthSecret == nil {
		return nil, nil, nil
	}
	k := r.AuthSecret.KEKID
	return r.AuthSecret.Ciphertext, r.AuthSecret.DEK, &k
}

func uniqueViolation(err error) bool { return pgdb.IsCode(err, pgdb.SQLStateUniqueViolation) }

// ListReputationSources devuelve todas las fuentes (catálogo primero).
func (s *Store) ListReputationSources(ctx context.Context) ([]domain.ReputationSource, error) {
	var out []domain.ReputationSource
	err := s.DB.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+repSourceCols+` FROM detection.reputation_source
			ORDER BY origin = 'custom', created_at, key`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanRepSource(rows)
			if err != nil {
				return err
			}
			out = append(out, *r)
		}
		return rows.Err()
	})
	return out, err
}

// GetReputationSource devuelve una fuente (domain.ErrSourceNotFound si no existe).
func (s *Store) GetReputationSource(ctx context.Context, id uuid.UUID) (*domain.ReputationSource, error) {
	var out *domain.ReputationSource
	err := s.DB.PlatformTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanRepSource(tx.QueryRow(ctx, `SELECT `+repSourceCols+` FROM detection.reputation_source WHERE id = $1`, id))
		return err
	})
	return out, err
}

// InsertReputationSource da de alta una fuente (domain.ErrSourceExists si
// repite clave, dataset o numeric_id).
func (s *Store) InsertReputationSource(ctx context.Context, r *domain.ReputationSource) error {
	ct, dek, kek := authCols(r)
	err := s.DB.PlatformTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO detection.reputation_source (`+repSourceCols+`)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25)`,
			r.ID, r.NumericID, r.Key, r.DatasetID, r.Origin, r.Name, r.URL, r.Format, r.Frequency, r.Category, r.Confidence, r.Enabled,
			r.License, r.Notes, r.MinEntries, r.MaxBytes, r.AuthHeaderName, ct, dek, kek, r.TermsAcknowledgedAt, r.CreatedBy,
			r.CreatedAt, r.UpdatedAt, r.Version)
		return err
	})
	if uniqueViolation(err) {
		return domain.ErrSourceExists
	}
	return err
}

// UpdateReputationSource guarda r si su versión en la base es expect
// (domain.ErrSourceStale si cambió, domain.ErrSourceNotFound si no existe).
func (s *Store) UpdateReputationSource(ctx context.Context, r *domain.ReputationSource, expect int) error {
	ct, dek, kek := authCols(r)
	return s.DB.PlatformTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE detection.reputation_source SET name = $3, url = $4, format = $5, frequency = $6,
			category = $7, confidence = $8, enabled = $9, license = $10, notes = $11, min_entries = $12, max_bytes = $13,
			auth_header_name = $14, auth_ciphertext = $15, auth_dek_wrapped = $16, auth_kek_id = $17, updated_at = $18, version = $19
			WHERE id = $1 AND version = $2`,
			r.ID, expect, r.Name, r.URL, r.Format, r.Frequency, r.Category, r.Confidence, r.Enabled, r.License, r.Notes, r.MinEntries,
			r.MaxBytes, r.AuthHeaderName, ct, dek, kek, r.UpdatedAt, r.Version)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
		return missingOrStale(ctx, tx, r.ID)
	})
}

// DeleteReputationSource borra una fuente con versión expect.
func (s *Store) DeleteReputationSource(ctx context.Context, id uuid.UUID, expect int) error {
	return s.DB.PlatformTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM detection.reputation_source WHERE id = $1 AND version = $2`, id, expect)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
		return missingOrStale(ctx, tx, id)
	})
}

func missingOrStale(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM detection.reputation_source WHERE id = $1)`, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return domain.ErrSourceStale
	}
	return domain.ErrSourceNotFound
}

// SyncCatalog concilia las filas del catálogo con la declaración de feeds:
// da de alta las nuevas (con su activación por defecto), actualiza la
// definición de las existentes sin tocar `enabled` (la decide el
// superadministrador) y borra las que ya no están declaradas.
func (s *Store) SyncCatalog(ctx context.Context, catalog []domain.ReputationSource) error {
	return s.DB.PlatformTx(ctx, func(tx pgx.Tx) error {
		ids := make([]string, 0, len(catalog))
		for _, r := range catalog {
			ids = append(ids, r.DatasetID)
			_, err := tx.Exec(ctx, `INSERT INTO detection.reputation_source (id, numeric_id, key, dataset_id, origin, name, url, format,
				frequency, category, confidence, enabled, license, notes, min_entries, max_bytes, created_at, updated_at, version)
				VALUES ($1, $2, $3, $4, 'catalog', $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $16, 1)
				ON CONFLICT (dataset_id) DO UPDATE SET name = EXCLUDED.name, url = EXCLUDED.url, format = EXCLUDED.format,
					frequency = EXCLUDED.frequency, category = EXCLUDED.category, confidence = EXCLUDED.confidence,
					license = EXCLUDED.license, notes = EXCLUDED.notes, min_entries = EXCLUDED.min_entries,
					max_bytes = EXCLUDED.max_bytes, updated_at = EXCLUDED.updated_at,
					version = detection.reputation_source.version + 1
				WHERE detection.reputation_source.origin = 'catalog'
					AND (detection.reputation_source.name, detection.reputation_source.url, detection.reputation_source.format,
						detection.reputation_source.frequency, detection.reputation_source.category,
						detection.reputation_source.confidence, detection.reputation_source.license,
						detection.reputation_source.notes, detection.reputation_source.min_entries,
						detection.reputation_source.max_bytes)
					IS DISTINCT FROM (EXCLUDED.name, EXCLUDED.url, EXCLUDED.format, EXCLUDED.frequency, EXCLUDED.category,
						EXCLUDED.confidence, EXCLUDED.license, EXCLUDED.notes, EXCLUDED.min_entries, EXCLUDED.max_bytes)`,
				r.ID, r.NumericID, r.Key, r.DatasetID, r.Name, r.URL, r.Format, r.Frequency, r.Category, r.Confidence, r.Enabled,
				r.License, r.Notes, r.MinEntries, r.MaxBytes, r.CreatedAt)
			if err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `DELETE FROM detection.reputation_source WHERE origin = 'catalog' AND dataset_id <> ALL($1)`, ids)
		return err
	})
}
