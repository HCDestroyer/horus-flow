package domain

import (
	"time"

	"github.com/google/uuid"
)

// Sujetos de un pronóstico.
const (
	SubjectUplink              = "uplink"
	SubjectInternationalEgress = "international_egress"
)

// EgressNamespace deriva el subject_id de la salida internacional agregada
// de un ISP (UUIDv5(tenant)): estable, distinto de cualquier interface_id.
var EgressNamespace = uuid.MustParse("0192f2a0-8e4f-7a62-9b7d-5c3f2e0a1d28")

// EgressSubjectID es el subject_id de la salida internacional del ISP.
func EgressSubjectID(tenant uuid.UUID) uuid.UUID { return uuid.NewSHA1(EgressNamespace, tenant[:]) }

// SettingsRow son los ajustes guardados de un ISP.
type SettingsRow struct {
	Settings  CapacitySettings
	UpdatedAt time.Time
	Version   int
}

// ConditionRow es el estado sostenido de una condición de un enlace.
type ConditionRow struct {
	TenantID    uuid.UUID `json:"-"`
	InterfaceID uuid.UUID `json:"interface_id"`
	RouterID    uuid.UUID `json:"router_id"`
	Condition   string    `json:"condition"`
	Level       string    `json:"level"`
	Since       time.Time `json:"since"`
	Value       float64   `json:"value"`
}

// ForecastRow es el último pronóstico de un sujeto.
type ForecastRow struct {
	TenantID       uuid.UUID  `json:"-"`
	SubjectID      uuid.UUID  `json:"subject_id"`
	SubjectKind    string     `json:"subject_kind"`
	SiteID         *uuid.UUID `json:"site_id"`
	Label          string     `json:"label"`
	Level          string     `json:"level"`
	ComputedAt     time.Time  `json:"computed_at"`
	Forecast       Forecast   `json:"forecast"`
	Recommendation string     `json:"recommendation"`
}
