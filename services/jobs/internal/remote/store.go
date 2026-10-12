package remote

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
)

// Errores del almacén.
var (
	ErrNotFound       = errors.New("remote: destination not found")
	ErrVersionChanged = errors.New("remote: version changed")
	ErrNameInUse      = errors.New("remote: name in use")
)

// Store es el acceso a PostgreSQL (esquema jobs; tablas de plataforma sin
// tenant: rol de plataforma).
type Store struct{ db *pgdb.DB }

// NewStore crea el almacén.
func NewStore(db *pgdb.DB) *Store { return &Store{db: db} }

const destCols = `id, kind, name, host, port, username, path, retention_days, enabled, host_key_fingerprint,
  credential_ciphertext, credential_dek_wrapped, coalesce(credential_kek_id, ''), coalesce(credential_kind, ''), verified_at,
  last_success_at, last_attempt_at, last_error, created_at, updated_at, version`

func scanDest(row pgx.Row) (*Destination, error) {
	var d Destination
	err := row.Scan(&d.ID, &d.Kind, &d.Name, &d.Host, &d.Port, &d.Username, &d.Path, &d.RetentionDays, &d.Enabled,
		&d.HostKeyFingerprint, &d.CredentialCiphertext, &d.CredentialDEK, &d.CredentialKEKID, &d.CredentialKind, &d.VerifiedAt,
		&d.LastSuccessAt, &d.LastAttemptAt, &d.LastError, &d.CreatedAt, &d.UpdatedAt, &d.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// InstallationID devuelve el identificador estable de la instalación (lo
// crea la primera vez).
func (s *Store) InstallationID(ctx context.Context) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO jobs.installation (id) VALUES ($1) ON CONFLICT (singleton) DO NOTHING`,
			uuid.Must(uuid.NewV7())); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT id FROM jobs.installation`).Scan(&id)
	})
	return id, err
}

// List devuelve los destinos por fecha de alta.
func (s *Store) List(ctx context.Context) ([]Destination, error) {
	var out []Destination
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+destCols+` FROM jobs.remote_destination ORDER BY created_at, id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			d, err := scanDest(rows)
			if err != nil {
				return err
			}
			out = append(out, *d)
		}
		return rows.Err()
	})
	return out, err
}

// Get devuelve un destino.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (*Destination, error) {
	var d *Destination
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var err error
		d, err = scanDest(tx.QueryRow(ctx, `SELECT `+destCols+` FROM jobs.remote_destination WHERE id = $1`, id))
		return err
	})
	return d, err
}

func mapWriteErr(err error) error {
	if code, constraint, ok := pgdb.ConstraintError(err); ok && code == pgdb.SQLStateUniqueViolation && constraint == "ux_destination__name" {
		return ErrNameInUse
	}
	return err
}

// Create inserta un destino.
func (s *Store) Create(ctx context.Context, d *Destination) (*Destination, error) {
	var out *Destination
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanDest(tx.QueryRow(ctx, `INSERT INTO jobs.remote_destination
			(id, kind, name, host, port, username, path, retention_days, enabled)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, false) RETURNING `+destCols,
			d.ID, d.Kind, d.Name, d.Host, d.Port, d.Username, d.Path, d.RetentionDays))
		return err
	})
	if err != nil {
		return nil, mapWriteErr(err)
	}
	return out, nil
}

// Update guarda la configuración editable de d si la versión sigue siendo
// version (sube la versión). resetLedger borra lo copiado (otro servidor,
// usuario o ruta).
func (s *Store) Update(ctx context.Context, d *Destination, version int, resetLedger bool) (*Destination, error) {
	var out *Destination
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanDest(tx.QueryRow(ctx, `UPDATE jobs.remote_destination SET name = $3, host = $4, port = $5,
			username = $6, path = $7, retention_days = $8, enabled = $9, host_key_fingerprint = $10, verified_at = $11,
			updated_at = now(), version = version + 1
			WHERE id = $1 AND version = $2 RETURNING `+destCols,
			d.ID, version, d.Name, d.Host, d.Port, d.Username, d.Path, d.RetentionDays, d.Enabled, d.HostKeyFingerprint, d.VerifiedAt))
		if errors.Is(err, ErrNotFound) {
			return s.versionOrMissing(ctx, tx, d.ID)
		}
		if err != nil {
			return err
		}
		if resetLedger {
			_, err = tx.Exec(ctx, `DELETE FROM jobs.remote_copy WHERE destination_id = $1`, d.ID)
		}
		return err
	})
	if err != nil {
		return nil, mapWriteErr(err)
	}
	return out, nil
}

func (s *Store) versionOrMissing(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM jobs.remote_destination WHERE id = $1`, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return ErrVersionChanged
}

// Delete borra el destino (y su ledger) si la versión coincide.
func (s *Store) Delete(ctx context.Context, id uuid.UUID, version int) error {
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM jobs.remote_destination WHERE id = $1 AND version = $2`, id, version)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return s.versionOrMissing(ctx, tx, id)
		}
		return nil
	})
}

// SetCredentials guarda la credencial sellada (sube la versión y pide una
// prueba nueva antes de habilitar).
func (s *Store) SetCredentials(ctx context.Context, id uuid.UUID, ct, dek []byte, kekID, kind string) (*Destination, error) {
	var out *Destination
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanDest(tx.QueryRow(ctx, `UPDATE jobs.remote_destination SET credential_ciphertext = $2,
			credential_dek_wrapped = $3, credential_kek_id = $4, credential_kind = $5, verified_at = NULL,
			updated_at = now(), version = version + 1 WHERE id = $1 RETURNING `+destCols, id, ct, dek, kekID, kind))
		return err
	})
	return out, err
}

// RecordTest guarda el resultado de una prueba: fija la huella si se
// confirmó (solo si no había otra) y marca verified_at si fue completa.
func (s *Store) RecordTest(ctx context.Context, id uuid.UUID, pin *string, verified bool) (*Destination, error) {
	var out *Destination
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanDest(tx.QueryRow(ctx, `UPDATE jobs.remote_destination SET
			host_key_fingerprint = coalesce(host_key_fingerprint, $2),
			verified_at = CASE WHEN $3 THEN now() ELSE verified_at END,
			version = CASE WHEN $2::text IS NOT NULL AND host_key_fingerprint IS NULL THEN version + 1 ELSE version END,
			updated_at = CASE WHEN $2::text IS NOT NULL AND host_key_fingerprint IS NULL THEN now() ELSE updated_at END
			WHERE id = $1 RETURNING `+destCols, id, pin, verified))
		return err
	})
	return out, err
}

// RecordAttempt guarda el resultado de una copia (no sube la versión: el
// estado no es configuración). errMsg vacío = correcta.
func (s *Store) RecordAttempt(ctx context.Context, id uuid.UUID, at time.Time, errMsg string) error {
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		if errMsg == "" {
			_, err := tx.Exec(ctx, `UPDATE jobs.remote_destination SET last_attempt_at = $2, last_success_at = $2,
				last_error = NULL WHERE id = $1`, id, at)
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE jobs.remote_destination SET last_attempt_at = $2, last_error = $3 WHERE id = $1`,
			id, at, errMsg)
		return err
	})
}

// Ledger devuelve lo copiado a un destino, por ruta relativa.
func (s *Store) Ledger(ctx context.Context, id uuid.UUID) (map[string]LedgerEntry, error) {
	out := map[string]LedgerEntry{}
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT rel_path, size, mtime, sha256, remote_name, uploaded_at
			FROM jobs.remote_copy WHERE destination_id = $1`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e LedgerEntry
			if err := rows.Scan(&e.Rel, &e.Size, &e.MTime, &e.SHA256, &e.RemoteName, &e.UploadedAt); err != nil {
				return err
			}
			e.MTime = e.MTime.UTC()
			out[e.Rel] = e
		}
		return rows.Err()
	})
	return out, err
}

// RecordCopy apunta un archivo subido, solo si el destino sigue con la
// versión con la que empezó la copia (si la configuración cambió a mitad,
// devuelve ErrVersionChanged y la copia se para).
func (s *Store) RecordCopy(ctx context.Context, id uuid.UUID, version int, e LedgerEntry) error {
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO jobs.remote_copy (destination_id, rel_path, size, mtime, sha256, remote_name, uploaded_at)
			SELECT $1, $3, $4, $5, $6, $7, $8 WHERE EXISTS (SELECT 1 FROM jobs.remote_destination WHERE id = $1 AND version = $2)
			ON CONFLICT (destination_id, rel_path) DO UPDATE SET size = EXCLUDED.size, mtime = EXCLUDED.mtime,
			  sha256 = EXCLUDED.sha256, remote_name = EXCLUDED.remote_name, uploaded_at = EXCLUDED.uploaded_at`,
			id, version, e.Rel, e.Size, e.MTime, e.SHA256, e.RemoteName, e.UploadedAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrVersionChanged
		}
		return nil
	})
}

// ForgetCopy quita un archivo del ledger (borrado por la retención remota).
func (s *Store) ForgetCopy(ctx context.Context, id uuid.UUID, rel string) error {
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM jobs.remote_copy WHERE destination_id = $1 AND rel_path = $2`, id, rel)
		return err
	})
}
