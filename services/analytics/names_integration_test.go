//go:build integration

package analytics_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/archtest"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/chread"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/app"
)

// analytics-names: los eventos de devices (golden files del contrato) llegan a
// dim.site/dim.router/dim.customer con el usuario real de analytics, y la ficha
// del cliente deja de responder 404 en su tráfico.
func TestNamesProjectionFeedsCustomerTraffic(t *testing.T) {
	e := setup(t, flowinv.Data{})
	r, err := chread.Open(e.srv.DSN, chread.UserAnalytics, analyticsPassword, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	proj := app.DimProjector{Store: func() app.DimStore { return r }}
	ctx := context.Background()
	customer := "0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55"
	tok := e.token(map[string][]string{"traffic.customer.read": all}, "tenant")
	path := "/api/v1/analytics/customers/" + customer + "/traffic?range=24h"

	if code, _ := e.get(path, tok); code != http.StatusNotFound {
		t.Fatalf("before projection: want 404, got %d", code)
	}
	for _, name := range []string{"site.created", "router.created", "customer.discovered", "customer.reset"} {
		if err := proj.Handle(ctx, message(t, e.tenant, "horus.devices."+name+".json")); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if code, body := e.get(path, tok); code != http.StatusOK {
		t.Fatalf("after projection: want 200, got %d %v", code, body)
	}
	var siteName, routerName string
	var resetAt *time.Time
	scan(t, r, e.tenant, `SELECT name FROM dim.site FINAL WHERE tenant_id = ? AND deleted = 0`, &siteName)
	scan(t, r, e.tenant, `SELECT name FROM dim.router FINAL WHERE tenant_id = ? AND deleted = 0`, &routerName)
	scan(t, r, e.tenant, `SELECT reset_at FROM dim.customer FINAL WHERE tenant_id = ? AND deleted = 0`, &resetAt)
	if siteName != "Centro" || routerName != "rt-centro-01" || resetAt == nil {
		t.Fatalf("dims: site=%q router=%q reset_at=%v", siteName, routerName, resetAt)
	}

	// purged (sin dirección ni realm en el evento): se completa con la fila vigente.
	if err := proj.Handle(ctx, message(t, e.tenant, "horus.devices.customer.purged.json")); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.get(path, tok); code != http.StatusNotFound {
		t.Fatalf("after purge: want 404, got %d", code)
	}
}

func message(t *testing.T, tenant uuid.UUID, file string) *natsx.Message {
	t.Helper()
	root, err := archtest.FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "packages/events/examples", file))
	if err != nil {
		t.Fatal(err)
	}
	env, err := natsx.DecodeEnvelope(b)
	if err != nil {
		t.Fatal(err)
	}
	env.TenantID = &tenant
	return &natsx.Message{Envelope: env, Tenant: tenant}
}

func scan(t *testing.T, r *chread.Reader, tenant uuid.UUID, sql string, dest any) {
	t.Helper()
	rows, cancel, err := r.Query(context.Background(), tenant, sql, tenant)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		t.Fatal(fmt.Errorf("no rows: %s", sql))
	}
	if err := rows.Scan(dest); err != nil {
		t.Fatal(err)
	}
}
