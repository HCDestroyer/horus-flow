package app

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// memTenantStore pagina tenants por (created_at, id) como el adaptador.
type memTenantStore struct {
	Store
	tenants []domain.Tenant
	calls   int
}

func (m *memTenantStore) ListTenants(_ context.Context, f TenantFilter) ([]domain.Tenant, error) {
	m.calls++
	var out []domain.Tenant
	for _, t := range m.tenants {
		if f.AfterCreated != nil && !(t.CreatedAt.After(*f.AfterCreated) ||
			(t.CreatedAt.Equal(*f.AfterCreated) && t.ID.String() > f.AfterID.String())) {
			continue
		}
		out = append(out, t)
		if len(out) == f.Limit {
			break
		}
	}
	return out, nil
}

func TestTenantSummariesPagesThroughAll(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st := &memTenantStore{}
	const n = 2*tenantSummaryBatch + 1
	for i := range n {
		st.tenants = append(st.tenants, domain.Tenant{ID: uuid.New(), Slug: "isp", Name: "ISP", Status: "active",
			CreatedAt: base.Add(time.Duration(i) * time.Second), Quotas: map[string]any{"max_routers": 5}})
	}
	svc, err := NewService(st, Options{Argon: domain.Argon2Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.TenantSummaries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n || st.calls != 3 {
		t.Fatalf("got %d tenants in %d calls, want %d in 3", len(got), st.calls, n)
	}
	for i, s := range got {
		if s.ID != st.tenants[i].ID || s.Slug != "isp" || s.Status != "active" {
			t.Fatalf("tenant %d = %+v", i, s)
		}
	}
}

func TestTenantSummariesEmpty(t *testing.T) {
	svc, err := NewService(&memTenantStore{}, Options{Argon: domain.Argon2Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.TenantSummaries(context.Background())
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want empty non-nil", got, err)
	}
}
