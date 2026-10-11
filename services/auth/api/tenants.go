package api

import (
	"context"

	"github.com/google/uuid"
)

// ServiceTenants → TenantDirectory.
const ServiceTenants = "auth.Tenants"

// TenantSummary es lo público de un tenant (ISP) para vistas de plataforma
// (GET /platform/overview): sin cuotas, ajustes ni miembros.
type TenantSummary struct {
	ID     uuid.UUID
	Slug   string
	Name   string
	Status string // active | suspended | offboarding
}

// TenantDirectory lista los tenants no borrados de la instalación (rol de
// plataforma; multi-tenant declarado), por fecha de alta.
type TenantDirectory interface {
	TenantSummaries(ctx context.Context) ([]TenantSummary, error)
}
