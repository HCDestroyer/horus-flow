package domain

import (
	"time"

	"github.com/google/uuid"
)

// CodeAsnRuleNotFound es el código de una regla ASN inexistente (o de otro tenant).
const CodeAsnRuleNotFound = "ASN_RULE_NOT_FOUND"

// Límites de una regla ASN (contrato AsnRuleInput y CHECK de devices.asn_rule).
const (
	MaxASN               = 4294967295
	MaxAsnRuleName       = 80
	MaxAsnRuleNote       = 300
	MaxAsnRuleSlug       = 64
	MaxAsnRulesPerTenant = 1000
)

// AsnRule es un ASN propio del ISP: el nombre, la categoría y (opcional) el
// servicio del catálogo con que flows clasifica el tráfico nuevo de ese ASN
// (classification_method = local_override). Sin servicio, el nombre de la
// regla hace de servicio.
type AsnRule struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	ASN       uint32
	Name      string
	Category  string
	Service   *string
	Note      *string
	CreatedBy *uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
	Version   int
}
