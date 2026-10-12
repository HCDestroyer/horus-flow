package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

type fakeTunnelStore struct {
	app.OnboardingStore
	router *domain.Router
	events []outbox.Event
}

func (f *fakeTunnelStore) SetRouterTunnel(_ context.Context, _ pgdb.TenantID, id uuid.UUID, fn func(r *domain.Router) bool, ev app.Events[domain.Router]) error {
	if f.router == nil || f.router.ID != id {
		return domain.ErrNotFound
	}
	if fn(f.router) {
		f.router.Version++
		f.events = append(f.events, ev(f.router)...)
	}
	return nil
}

// El primer flujo del colector (contrato C4) lleva el router a exporting una
// sola vez; sin túnel o en estados sin flujos no cambia nada.
func TestHandleExporterStateMarksExporting(t *testing.T) {
	env, err := natsx.DecodeEnvelope(example(t, "horus.flows.exporter.state_changed.json"))
	if err != nil {
		t.Fatal(err)
	}
	routerID := uuid.MustParse("0192e333-0000-7000-8000-000000000033")
	addr := "10.255.1.2"
	msg := &natsx.Message{Envelope: env, Tenant: *env.TenantID}

	f := &fakeTunnelStore{router: &domain.Router{ID: routerID, OnboardingState: devapi.OnboardingTunnelUp, TunnelAddress: &addr}}
	o := app.NewOnboarding(f, nil, func() time.Time { return time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC) })
	ctx := context.Background()
	if err := o.HandleExporterState(ctx, msg); err != nil {
		t.Fatal(err)
	}
	if f.router.OnboardingState != devapi.OnboardingExporting || len(f.events) != 1 || f.events[0].Type != "horus.devices.router.updated" {
		t.Fatalf("state=%s events=%v", f.router.OnboardingState, f.events)
	}
	if err := o.HandleExporterState(ctx, msg); err != nil || len(f.events) != 1 {
		t.Fatalf("redelivery must be a no-op: err=%v events=%d", err, len(f.events))
	}

	noTunnel := &fakeTunnelStore{router: &domain.Router{ID: routerID, OnboardingState: devapi.OnboardingPending}}
	if err := app.NewOnboarding(noTunnel, nil, nil).HandleExporterState(ctx, msg); err != nil || len(noTunnel.events) != 0 {
		t.Fatalf("router without tunnel must not change: err=%v events=%d", err, len(noTunnel.events))
	}
	if err := app.NewOnboarding(&fakeTunnelStore{}, nil, nil).HandleExporterState(ctx, msg); err != nil {
		t.Fatalf("deleted router must be acked: %v", err)
	}
}
