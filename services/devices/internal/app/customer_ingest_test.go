package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/hcdestroyer/horus-flow/packages/go/archtest"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

type fakeCustomers struct {
	app.CustomerStore
	realm      uuid.UUID
	discovered []app.NewCustomer
	seen       []app.SeenCustomer
	security   []securitySet
}

func (f *fakeCustomers) DiscoverCustomers(_ context.Context, _ pgdb.TenantID, realm uuid.UUID, c []app.NewCustomer, _ app.CustomerEvents) ([]domain.Customer, error) {
	f.realm, f.discovered = realm, c
	return nil, nil
}

func (f *fakeCustomers) TouchCustomers(_ context.Context, _ pgdb.TenantID, realm uuid.UUID, s []app.SeenCustomer, _ app.CustomerEvents) (int, error) {
	f.realm, f.seen = realm, s
	return 0, nil
}

type securitySet struct {
	tenant uuid.UUID
	id     uuid.UUID
	state  string
	open   int
}

func (f *fakeCustomers) SetCustomerSecurity(_ context.Context, t pgdb.TenantID, id uuid.UUID, state string, open int, _ time.Time) (bool, error) {
	f.security = append(f.security, securitySet{tenant: t.UUID(), id: id, state: state, open: open})
	return true, nil
}

func example(t *testing.T, name string) []byte {
	t.Helper()
	root, err := archtest.FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "packages/events/examples", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// El consumidor acepta el golden file del contrato C4 (productor FLOW).
func TestHandleFirstSeenContractExample(t *testing.T) {
	env, err := natsx.DecodeEnvelope(example(t, "horus.flows.client.first_seen.json"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeCustomers{}
	svc := app.NewCustomers(f, nil, nil, nil, nil)
	if err := svc.HandleFirstSeen(context.Background(), &natsx.Message{Envelope: env, Tenant: *env.TenantID}); err != nil {
		t.Fatal(err)
	}
	if f.realm.String() != "0192e111-0000-7000-8000-000000000011" || len(f.discovered) != 1 || f.discovered[0].Address.String() != "10.20.0.41" {
		t.Fatalf("discovered = %v %v", f.realm, f.discovered)
	}
}

func TestHandleActivityProtobuf(t *testing.T) {
	realm := uuid.New()
	pb := &flowpb.ClientActivitySummary{BatchID: uuid.NewString(), RealmID: realm.String(), Hour: time.Now().Truncate(time.Hour),
		Part: 1, Parts: 1, Clients: []flowpb.ActiveClient{{Address: "2001:db8:1:2::/64", LastSeen: time.Now(), Flows: 3}}}
	f := &fakeCustomers{}
	svc := app.NewCustomers(f, nil, nil, nil, nil)
	h := nats.Header{}
	h.Set(natsx.HeaderContentType, "application/x-protobuf; proto=horus.events.flows.v1.ClientActivitySummary")
	if err := svc.HandleActivity(context.Background(), &natsx.Message{Header: h, Data: pb.Marshal(), Tenant: uuid.New()}); err != nil {
		t.Fatal(err)
	}
	if f.realm != realm || len(f.seen) != 1 || f.seen[0].Address.String() != "2001:db8:1:2::" {
		t.Fatalf("seen = %v", f.seen)
	}
}

func TestHandleActivityJSONExample(t *testing.T) {
	f := &fakeCustomers{}
	svc := app.NewCustomers(f, nil, nil, nil, nil)
	h := nats.Header{}
	h.Set(natsx.HeaderContentType, "application/json")
	if err := svc.HandleActivity(context.Background(), &natsx.Message{Header: h, Data: example(t, "horus.flows.client.activity_summary.json"), Tenant: uuid.New()}); err != nil {
		t.Fatal(err)
	}
	if len(f.seen) != 1 || f.seen[0].Address.String() != "10.20.0.41" {
		t.Fatalf("seen = %v", f.seen)
	}
}

// El consumidor devices-customer-detection proyecta el golden file del contrato
// (estado y hallazgos abiertos) en la ficha del cliente, y rechaza sin
// reintento un estado desconocido.
func TestHandleSecurityStateContractExample(t *testing.T) {
	env, err := natsx.DecodeEnvelope(example(t, "horus.detection.customer.security_state_changed.json"))
	if err != nil {
		t.Fatal(err)
	}
	st := &fakeCustomers{}
	svc := app.NewCustomers(st, nil, nil, nil, nil)
	if err := svc.HandleSecurityState(context.Background(), &natsx.Message{Envelope: env, Tenant: *env.TenantID}); err != nil {
		t.Fatal(err)
	}
	if len(st.security) != 1 || st.security[0].tenant != *env.TenantID || st.security[0].id.String() != "0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55" ||
		st.security[0].state != "suspected" || st.security[0].open != 1 {
		t.Fatalf("proyección = %+v", st.security)
	}
	bad := *env
	bad.Data = []byte(`{"customer_id":"0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55","state":"hacked","open_findings":1}`)
	err = svc.HandleSecurityState(context.Background(), &natsx.Message{Envelope: &bad, Tenant: *env.TenantID})
	if err == nil || !natsx.IsPermanent(err) || len(st.security) != 1 {
		t.Fatalf("estado desconocido: %v", err)
	}
	// Otro tipo de evento en el mismo stream se ignora.
	other := *env
	other.Type = "horus.detection.customer.kind_suggested"
	if err := svc.HandleSecurityState(context.Background(), &natsx.Message{Envelope: &other, Tenant: *env.TenantID}); err != nil || len(st.security) != 1 {
		t.Fatalf("otro tipo: %v", err)
	}
}
