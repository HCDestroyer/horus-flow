package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Orígenes de una fuente de reputación (D20).
const (
	OriginCatalog = "catalog" // catálogo base de la instalación (feeds.yaml)
	OriginCustom  = "custom"  // dada de alta por el superadministrador
)

// Errores del repositorio de fuentes de reputación.
var (
	ErrSourceNotFound = errors.New("detection: reputation source not found")
	// ErrSourceExists: clave (o identificador numérico derivado) repetida.
	ErrSourceExists = errors.New("detection: reputation source already exists")
	// ErrSourceStale: la versión no coincide (If-Match).
	ErrSourceStale = errors.New("detection: reputation source version mismatch")
)

// SealedSecret es un secreto con envelope encryption (packages/go/crypto/envelope).
type SealedSecret struct {
	Ciphertext []byte
	DEK        []byte // DEK envuelta con la KEK del módulo
	KEKID      string
}

// ReputationSource es una fuente de reputación gestionada por la API de
// plataforma (tabla detection.reputation_source). Las del catálogo copian su
// definición de feeds.yaml y solo guardan aquí su activación; las
// personalizadas son completas.
type ReputationSource struct {
	ID uuid.UUID
	// NumericID = reputation.SourceID(DatasetID): flows_raw.reputation_source_id.
	NumericID int
	// Key es la clave estable del contrato (^[a-z][a-z0-9_]{2,47}$).
	Key string
	// DatasetID es el id de la fuente en el almacén de datasets y en el
	// snapshot: el de feeds.yaml en el catálogo, "custom-<key>" si no.
	DatasetID  string
	Origin     string
	Name       string
	URL        string
	Format     string
	Frequency  string // 15m, 1h, 6h, 24h, 7d…
	Category   string // enum del contrato (reputation_category)
	Confidence int
	Enabled    bool
	License    *string
	Notes      *string
	MinEntries int
	MaxBytes   int64
	// AuthHeaderName y AuthSecret: cabecera de autenticación del feed (el
	// valor solo cifrado; nunca sale por la API).
	AuthHeaderName      *string
	AuthSecret          *SealedSecret
	TermsAcknowledgedAt *time.Time
	CreatedBy           *uuid.UUID
	CreatedAt           time.Time
	UpdatedAt           time.Time
	Version             int
}

// HasAuth indica si la fuente tiene cabecera de autenticación.
func (r *ReputationSource) HasAuth() bool { return r.AuthSecret != nil && r.AuthHeaderName != nil }
