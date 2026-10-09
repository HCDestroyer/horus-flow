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
}

func (f *fakeCustomers) DiscoverCustomers(_ context.Context, _ pgdb.TenantID, realm uuid.UUID, c []app.NewCustomer, _ app.CustomerEvents) ([]domain.Customer, error) {
	f.realm, f.discovered = realm, c
	return nil, nil
}

func (f *fakeCustomers) TouchCustomers(_ context.Context, _ pgdb.TenantID, realm uuid.UUID, s []app.SeenCustomer, _ app.CustomerEvents) (int, error) {
	f.realm, f.seen = realm, s
	return 0, nil
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
