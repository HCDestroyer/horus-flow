package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/pgstore"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// ErrNoStore: sin PostgreSQL no hay umbrales por ISP (solo los de la instalación).
var ErrNoStore = errors.New("snmp: thresholds store not configured")

// ErrThresholdVersion: If-Match no coincide con la versión guardada.
var ErrThresholdVersion = pgstore.ErrVersion

// ThresholdStore es el repositorio de umbrales por ISP.
type ThresholdStore interface {
	Get(ctx context.Context, tenant uuid.UUID) (*pgstore.Row, error)
	Put(ctx context.Context, tenant uuid.UUID, v domain.ThresholdValues, actor string, expect int) (*pgstore.Row, error)
	Delete(ctx context.Context, tenant uuid.UUID) error
}

// TenantThresholds es la respuesta de GET/PUT /routers/thresholds.
type TenantThresholds struct {
	domain.ThresholdValues
	Defaults   domain.ThresholdValues `json:"defaults"`
	Customized bool                   `json:"customized"`
	Version    int                    `json:"version"`
	UpdatedAt  *time.Time             `json:"updated_at"`
	// SustainedCycles: ciclos seguidos sobre el umbral para CPU y memoria.
	SustainedCycles int `json:"sustained_cycles"`
}

// Thresholds gestiona los umbrales por ISP y los aplica al sondeador.
type Thresholds struct {
	store ThresholdStore // nil = sin PostgreSQL
	eng   *Engine
}

// NewThresholds crea el servicio (store nil: solo lectura de los de la instalación).
func NewThresholds(store ThresholdStore, eng *Engine) *Thresholds { return &Thresholds{store: store, eng: eng} }

func (t *Thresholds) view(row *pgstore.Row) TenantThresholds {
	def := t.eng.DefaultThresholds()
	out := TenantThresholds{ThresholdValues: def, Defaults: def, SustainedCycles: t.eng.thresholds.SustainedCycles}
	if row != nil {
		at := row.UpdatedAt.UTC()
		out.ThresholdValues, out.Customized, out.Version, out.UpdatedAt = row.Values, true, row.Version, &at
	}
	return out
}

// Get devuelve los umbrales vigentes del ISP.
func (t *Thresholds) Get(ctx context.Context, tenant uuid.UUID) (TenantThresholds, error) {
	if t.store == nil {
		return t.view(nil), nil
	}
	row, err := t.store.Get(ctx, tenant)
	if err != nil {
		return TenantThresholds{}, err
	}
	return t.view(row), nil
}

// Put guarda (ya validados) y aplica en caliente los umbrales del ISP.
func (t *Thresholds) Put(ctx context.Context, tenant uuid.UUID, v domain.ThresholdValues, actor string, expect int) (TenantThresholds, error) {
	if t.store == nil {
		return TenantThresholds{}, ErrNoStore
	}
	row, err := t.store.Put(ctx, tenant, v, actor, expect)
	if err != nil {
		return TenantThresholds{}, err
	}
	t.eng.SetTenantThresholds(tenant, &row.Values)
	return t.view(row), nil
}

// Reset vuelve a los umbrales de la instalación.
func (t *Thresholds) Reset(ctx context.Context, tenant uuid.UUID) (TenantThresholds, error) {
	if t.store == nil {
		return TenantThresholds{}, ErrNoStore
	}
	if err := t.store.Delete(ctx, tenant); err != nil {
		return TenantThresholds{}, err
	}
	t.eng.SetTenantThresholds(tenant, nil)
	return t.view(nil), nil
}
