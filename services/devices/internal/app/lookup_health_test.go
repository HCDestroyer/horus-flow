package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/analytics/api/servingrouter"
	"github.com/hcdestroyer/horus-flow/services/collector/api/lastflow"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
	snmpapi "github.com/hcdestroyer/horus-flow/services/snmp/api"
)

type fakeServing struct {
	sv  *servingrouter.Serving
	err error
	q   servingrouter.Query
}

func (f *fakeServing) ServingRouter(_ context.Context, _ uuid.UUID, q servingrouter.Query) (*servingrouter.Serving, error) {
	f.q = q
	return f.sv, f.err
}

type fakeStatus struct{ rows []snmpapi.RouterStatus }

func (f fakeStatus) List(context.Context, uuid.UUID, []uuid.UUID) ([]snmpapi.RouterStatus, snmpapi.PollerHealth, error) {
	return f.rows, snmpapi.PollerHealth{}, nil
}

func (fakeStatus) Live(context.Context, uuid.UUID, uuid.UUID) (*snmpapi.RouterLiveMetrics, bool, error) {
	return nil, false, nil
}

type fakeLastFlow map[uuid.UUID]time.Time

func (f fakeLastFlow) LastFlowAt(context.Context, uuid.UUID, []uuid.UUID) (map[uuid.UUID]time.Time, error) {
	return f, nil
}

// Consulta 360: el router es el de los flujos más recientes del cliente (no
// el principal), con el último flujo del exportador del collector; sin flujos
// recientes o con la analítica caída, el principal con source=primary_router.
func TestIPLookupServingRouterAndLastFlow(t *testing.T) {
	t.Parallel()
	f := newIntFixture(t)
	site := uuid.New()
	c := f.addCustomer("100.64.1.20", nil, nil)
	c.SiteID, c.LastSeen = site, f.now.Add(-2*time.Minute)
	primary := &domain.Router{ID: uuid.New(), SiteID: site, Hostname: "rt-principal"}
	backupName := "Respaldo"
	backup := &domain.Router{ID: uuid.New(), SiteID: site, Hostname: "rt-2", DisplayName: &backupName}
	routers := map[uuid.UUID]*domain.Router{primary.ID: primary, backup.ID: backup}
	f.svc.Readers.PrimaryRouter = func(context.Context, pgdb.TenantID, uuid.UUID) (*domain.Router, error) { return primary, nil }
	f.svc.Readers.Router = func(_ context.Context, _ pgdb.TenantID, id uuid.UUID) (*domain.Router, error) {
		if r, ok := routers[id]; ok {
			return r, nil
		}
		return nil, domain.ErrNotFound
	}
	custLast := f.now.Add(-40 * time.Second)
	serving := &fakeServing{sv: &servingrouter.Serving{RouterID: backup.ID, LastFlowAt: custLast}}
	f.svc.Readers.ServingRouter = func() (servingrouter.Reader, bool) { return serving, true }
	snmpLast := f.now.Add(-70 * time.Second)
	exporting := "exporting"
	f.svc.Readers.Routers = func() (snmpapi.RouterStatusReader, bool) {
		return fakeStatus{rows: []snmpapi.RouterStatus{{RouterID: backup.ID, Status: snmpapi.StatusOnline, LastFlowAt: &snmpLast,
			Signals: snmpapi.Signals{Exporter: &exporting}}}}, true
	}
	exporterLast := f.now.Add(-12 * time.Second)
	f.svc.Readers.LastFlow = func() (lastflow.Reader, bool) { return fakeLastFlow{backup.ID: exporterLast}, true }
	p := &authz.Principal{Type: authz.TypeUser, Subject: uuid.NewString(), Scope: authz.ScopeTenant, TenantID: f.tenant,
		Perms: map[string][]string{"customers.read": {"*"}, "snmp.read": {"*"}}}
	ctx := authz.WithPrincipal(context.Background(), p)
	in := IPLookupInput{Address: ptr("100.64.1.20"), Include: []string{SectionHealth}}

	res, err := f.svc.IPLookup(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	h := res.Health
	if h == nil || h.Router == nil || h.Router.ID != backup.ID || h.Router.Source != RouterSourceFlows || h.Router.Name != "Respaldo" ||
		h.Router.Status != snmpapi.StatusOnline || h.Router.CustomerLastFlowAt == nil || !h.Router.CustomerLastFlowAt.Equal(custLast) {
		t.Fatalf("router que sirve = %+v", h.Router)
	}
	if h.LastFlowAt == nil || !h.LastFlowAt.Equal(exporterLast) || h.ExporterStatus == nil || *h.ExporterStatus != "exporting" {
		t.Fatalf("último flujo del exportador = %v %v", h.LastFlowAt, h.ExporterStatus)
	}
	if serving.q.SiteID != site || serving.q.Prefix != c.Address || !serving.q.At.Equal(*f.now) {
		t.Fatalf("consulta = %+v", serving.q)
	}
	// Sin el estado del collector, el del último sondeo SNMP.
	f.svc.Readers.LastFlow = nil
	if res, _ = f.svc.IPLookup(ctx, in); res.Health.LastFlowAt == nil || !res.Health.LastFlowAt.Equal(snmpLast) {
		t.Fatalf("respaldo snmp = %v", res.Health.LastFlowAt)
	}
	// Sin flujos recientes del cliente → principal, marcado.
	serving.sv = nil
	if res, _ = f.svc.IPLookup(ctx, in); res.Health.Router.ID != primary.ID || res.Health.Router.Source != RouterSourcePrimary ||
		res.Health.Router.CustomerLastFlowAt != nil || res.Health.LastFlowAt != nil {
		t.Fatalf("sin flujos = %+v", res.Health)
	}
	// Analítica caída → principal, sin fallar la consulta.
	serving.err = errors.New("clickhouse caído")
	if res, err = f.svc.IPLookup(ctx, in); err != nil || res.Health.Router.Source != RouterSourcePrimary {
		t.Fatalf("analítica caída = %+v %v", res, err)
	}
}
