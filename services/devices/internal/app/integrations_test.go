package app

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/webhooksig"
	consumptionapi "github.com/hcdestroyer/horus-flow/services/analytics/api/consumption"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// fakeIntStore implementa lo que usan los casos de uso (el resto del
// interfaz queda sin implementar y entraría en pánico).
type fakeIntStore struct {
	IntegrationStore
	mu        sync.Mutex
	conn      *domain.ArgosConnector
	customers map[uuid.UUID]*domain.Customer
	idents    []domain.ExternalIdentity
	inbox     map[string]bool
	stale     []uuid.UUID
	sync      []ArgosSyncState
	windows   []ServiceWindow
}

func (f *fakeIntStore) GetArgosConnector(context.Context, pgdb.TenantID) (*domain.ArgosConnector, error) {
	if f.conn == nil {
		return nil, domain.ErrNotFound
	}
	c := *f.conn
	return &c, nil
}

func (f *fakeIntStore) ArgosConnectorByID(_ context.Context, id uuid.UUID) (*domain.ArgosConnector, error) {
	if f.conn == nil || f.conn.ID != id {
		return nil, domain.ErrNotFound
	}
	c := *f.conn
	return &c, nil
}

func (f *fakeIntStore) SaveArgosConnector(_ context.Context, _ pgdb.TenantID, c *domain.ArgosConnector, _ int) error {
	c.Version++
	cp := *c
	f.conn = &cp
	return nil
}

func (f *fakeIntStore) RecordArgosSync(_ context.Context, _ pgdb.TenantID, st ArgosSyncState) error {
	f.sync = append(f.sync, st)
	f.conn.Failures, f.conn.BackoffUntil, f.conn.LastSyncStatus = st.Failures, st.BackoffUntil, &st.Status
	return nil
}

func (f *fakeIntStore) CountIdentities(context.Context, pgdb.TenantID) (int, int, error) {
	return 0, 0, nil
}

func (f *fakeIntStore) CurrentIdentities(_ context.Context, _ pgdb.TenantID, ids []uuid.UUID) (map[uuid.UUID]domain.ExternalIdentity, error) {
	out := map[uuid.UUID]domain.ExternalIdentity{}
	for _, i := range f.idents {
		if i.ValidTo == nil {
			out[i.CustomerID] = i
		}
	}
	return out, nil
}

func (f *fakeIntStore) ApplyIdentityPlan(_ context.Context, _ pgdb.TenantID, cid uuid.UUID, plan domain.IdentityPlan, _ string, _ time.Time,
	alias func(*domain.Customer, *domain.ExternalIdentity) bool, _ CustomerEvents,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.idents {
		x := &f.idents[i]
		if x.CustomerID != cid || x.ValidTo != nil {
			continue
		}
		if plan.Close {
			at := plan.CloseAt
			x.ValidTo = &at
		}
		if plan.Refresh != nil && plan.Refresh.ID == x.ID {
			*x = *plan.Refresh
		}
	}
	if plan.Open != nil {
		f.idents = append(f.idents, *plan.Open)
	}
	var cur *domain.ExternalIdentity
	for i := range f.idents {
		if f.idents[i].CustomerID == cid && f.idents[i].ValidTo == nil {
			cur = &f.idents[i]
		}
	}
	alias(f.customers[cid], cur)
	return nil
}

func (f *fakeIntStore) MarkIdentitiesStale(_ context.Context, _ pgdb.TenantID, ids []uuid.UUID, _ time.Time) error {
	f.stale = append(f.stale, ids...)
	for i := range f.idents {
		for _, id := range ids {
			if f.idents[i].CustomerID == id && f.idents[i].ValidTo == nil {
				f.idents[i].Stale = true
			}
		}
	}
	return nil
}

func (f *fakeIntStore) InsertArgosWebhook(_ context.Context, _ pgdb.TenantID, id, _ string, _ []byte) (bool, error) {
	if f.inbox[id] {
		return false, nil
	}
	f.inbox[id] = true
	return true, nil
}

func (f *fakeIntStore) CustomersContaining(_ context.Context, _ pgdb.TenantID, addrs []netip.Addr, _ []uuid.UUID) (map[netip.Addr]domain.Customer, error) {
	out := map[netip.Addr]domain.Customer{}
	for _, a := range addrs {
		for _, c := range f.customers {
			if c.Address.Contains(a) {
				out[a] = *c
			}
		}
	}
	return out, nil
}

func (f *fakeIntStore) ServiceWindows(_ context.Context, _ pgdb.TenantID, ids []int64, _, _ time.Time) ([]ServiceWindow, error) {
	return f.windows, nil
}

// fakeResolver devuelve resoluciones fijas o un error.
type fakeResolver struct {
	res   map[string]domain.Resolution
	err   error
	calls int
	mode  string
}

func (r *fakeResolver) Resolve(_ context.Context, ips []string) ([]ArgosResult, string, error) {
	r.calls++
	if r.err != nil {
		return nil, "", r.err
	}
	out := make([]ArgosResult, len(ips))
	for i, ip := range ips {
		out[i] = ArgosResult{IP: ip, Resolution: r.res[ip]}
	}
	mode := r.mode
	if mode == "" {
		mode = "batch"
	}
	return out, mode, nil
}

type intFixture struct {
	svc    *Integrations
	store  *fakeIntStore
	res    *fakeResolver
	tenant uuid.UUID
	now    *time.Time
	ctx    context.Context
}

func newIntFixture(t *testing.T) *intFixture {
	t.Helper()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	f := &intFixture{tenant: uuid.New(), now: &now, res: &fakeResolver{res: map[string]domain.Resolution{}}}
	f.store = &fakeIntStore{customers: map[uuid.UUID]*domain.Customer{}, inbox: map[string]bool{}}
	f.svc = NewIntegrations(IntegrationsOptions{Store: f.store, Sealer: envelope.Ephemeral(), BaseURL: "https://horus.example",
		Now: func() time.Time { return *f.now }, Dial: func(uuid.UUID, string, string) ArgosResolver { return f.res }})
	p := &authz.Principal{Type: authz.TypeUser, Subject: uuid.NewString(), Scope: authz.ScopeTenant, TenantID: f.tenant,
		Perms: map[string][]string{"settings.manage": {"*"}, "settings.read": {"*"}, "customers.read": {"*"}, "customers.update": {"*"},
			"traffic.customer.read": {"*"}}}
	f.ctx = authz.WithPrincipal(context.Background(), p)
	tok := "atk_prueba_123"
	enabled := true
	if _, err := f.svc.PutConnector(f.ctx, ConnectorInput{BaseURL: ptr("https://argos.example/api/v1"), Enabled: &enabled, Token: &tok}); err != nil {
		t.Fatal(err)
	}
	return f
}

func ptr[T any](v T) *T { return &v }

func (f *intFixture) addCustomer(addr string, alias *string, source *string) *domain.Customer {
	c := &domain.Customer{ID: uuid.New(), TenantID: f.tenant, RealmID: uuid.Nil, Address: netip.MustParsePrefix(addr + "/32"),
		Alias: alias, AliasSource: source, Status: "active"}
	f.store.customers[c.ID] = c
	return c
}

func TestConnectorValidationAndSecrets(t *testing.T) {
	t.Parallel()
	f := newIntFixture(t)
	c := f.store.conn
	if !c.Active() || !c.HasSecret() || string(c.TokenCiphertext) == "atk_prueba_123" {
		t.Fatalf("token cifrado y secreto generado: %+v", c)
	}
	if u := f.svc.WebhookURL(c); *u != "https://horus.example/api/v1/integrations/argosisp/webhook/"+c.ID.String() {
		t.Fatalf("webhook_url = %s", *u)
	}
	en := true
	for _, bad := range []string{"http://argos.example", "ftp://x", "https://user:pw@argos.example", "https://"} {
		if _, err := f.svc.PutConnector(f.ctx, ConnectorInput{BaseURL: &bad, Enabled: &en}); err == nil {
			t.Errorf("%s debía rechazarse", bad)
		}
	}
	if _, ok := ValidateBaseURL("http://10.0.0.5:3333/api/v1", true); !ok {
		t.Fatal("en laboratorio se permite http")
	}
}

func TestWebhookVerifyAndDedupe(t *testing.T) {
	t.Parallel()
	f := newIntFixture(t)
	url, secret, err := f.svc.RotateWebhookSecret(f.ctx)
	if err != nil || url == "" || secret == "" {
		t.Fatalf("secreto: %v", err)
	}
	body := []byte(`{"id":"evt-1","evento":"sesion.iniciada","ocurrido_en":"2026-10-17T11:59:00Z","payload":{"servicio_id":812,"ipv4":"100.64.1.20"}}`)
	h := http.Header{}
	webhooksig.Sign(h, []byte(secret), "evt-1", *f.now, body)
	id := f.store.conn.ID
	if err := f.svc.ReceiveWebhook(context.Background(), id, h, body); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ReceiveWebhook(context.Background(), id, h, body); err != nil {
		t.Fatalf("duplicado: debe aceptarse sin reprocesar: %v", err)
	}
	if len(f.store.inbox) != 1 {
		t.Fatalf("inbox = %v", f.store.inbox)
	}
	bad := h.Clone()
	bad.Set(webhooksig.HeaderSignature, "v1="+string(make([]byte, 0))+"00")
	if err := f.svc.ReceiveWebhook(context.Background(), id, bad, body); !errors.Is(err, ErrWebhookRejected) {
		t.Fatalf("firma inválida: %v", err)
	}
	*f.now = f.now.Add(6 * time.Minute)
	if err := f.svc.ReceiveWebhook(context.Background(), id, h, body); !errors.Is(err, ErrWebhookRejected) {
		t.Fatalf("desfase > 5 min: %v", err)
	}
	if err := f.svc.ReceiveWebhook(context.Background(), uuid.New(), h, body); !errors.Is(err, ErrWebhookUnknown) {
		t.Fatalf("conector desconocido: %v", err)
	}
	// Rotación: el secreto anterior sigue valiendo 24 h.
	*f.now = f.now.Add(-6 * time.Minute)
	if _, _, err := f.svc.RotateWebhookSecret(f.ctx); err != nil {
		t.Fatal(err)
	}
	h2 := http.Header{}
	webhooksig.Sign(h2, []byte(secret), "evt-2", *f.now, body)
	if err := f.svc.ReceiveWebhook(context.Background(), id, h2, body); err != nil {
		t.Fatalf("secreto anterior en gracia: %v", err)
	}
}

func TestResolveAppliesValidityAndKeepsManualAlias(t *testing.T) {
	t.Parallel()
	f := newIntFixture(t)
	manual := domain.AliasSourceManual
	a := f.addCustomer("100.64.1.20", nil, nil)
	b := f.addCustomer("100.64.1.21", ptr("Puesto a mano"), &manual)
	res := domain.Resolution{Found: true, ExternalCustomerID: ptr[int64](455), ExternalServiceID: ptr[int64](812), DisplayName: ptr("Ferretería López")}
	f.res.res["100.64.1.20"], f.res.res["100.64.1.21"] = res, res
	if err := f.svc.ResolveCustomers(context.Background(), pgdb.TenantID(f.tenant), []domain.Customer{*a, *b}); err != nil {
		t.Fatal(err)
	}
	if *a.Alias != "Ferretería López" || *a.AliasSource != domain.AliasSourceArgos {
		t.Fatalf("alias de ArgosISP: %+v", a)
	}
	if *b.Alias != "Puesto a mano" || *b.AliasSource != domain.AliasSourceManual {
		t.Fatalf("el alias manual nunca se pisa: %+v", b)
	}
	// La IP de a pasa a otro servicio: se cierra la vigencia anterior y se abre otra.
	*f.now = f.now.Add(time.Hour)
	f.res.res["100.64.1.20"] = domain.Resolution{Found: true, ExternalCustomerID: ptr[int64](900), ExternalServiceID: ptr[int64](901), DisplayName: ptr("Ana")}
	if err := f.svc.ResolveCustomers(context.Background(), pgdb.TenantID(f.tenant), []domain.Customer{*a}); err != nil {
		t.Fatal(err)
	}
	var periods []domain.ExternalIdentity
	for _, i := range f.store.idents {
		if i.CustomerID == a.ID {
			periods = append(periods, i)
		}
	}
	if len(periods) != 2 || periods[0].ValidTo == nil || *periods[0].ExternalServiceID != 812 || periods[1].ValidTo != nil {
		t.Fatalf("vigencias = %+v", periods)
	}
	if *a.Alias != "Ana" {
		t.Fatalf("alias del nuevo dueño = %v", *a.Alias)
	}
	// Un hallazgo de antes del cambio se atribuye al dueño anterior.
	before := f.now.Add(-30 * time.Minute)
	if !periods[0].ValidAt(before) || periods[1].ValidAt(before) {
		t.Fatal("el hallazgo anterior es del servicio 812")
	}
}

// ArgosISP caído: se conserva la identidad (stale), espera exponencial, y la
// siguiente llamada no sale mientras dure la espera.
func TestResolveDegradation(t *testing.T) {
	t.Parallel()
	f := newIntFixture(t)
	a := f.addCustomer("100.64.1.20", nil, nil)
	f.res.res["100.64.1.20"] = domain.Resolution{Found: true, ExternalServiceID: ptr[int64](812), DisplayName: ptr("X")}
	tt := pgdb.TenantID(f.tenant)
	if err := f.svc.ResolveCustomers(context.Background(), tt, []domain.Customer{*a}); err != nil {
		t.Fatal(err)
	}
	f.res.err = ErrArgosUnavailable
	if err := f.svc.ResolveCustomers(context.Background(), tt, []domain.Customer{*a}); !errors.Is(err, ErrArgosUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if len(f.store.stale) != 1 || !f.store.idents[0].Stale || *a.Alias != "X" {
		t.Fatalf("identidad conservada como stale: %+v", f.store.idents)
	}
	last := f.store.sync[len(f.store.sync)-1]
	if last.Status != "failing" || last.Failures != 1 || !last.BackoffUntil.Equal(f.now.Add(30*time.Second)) {
		t.Fatalf("sync = %+v", last)
	}
	calls := f.res.calls
	if err := f.svc.ResolveCustomers(context.Background(), tt, []domain.Customer{*a}); !errors.Is(err, errArgosSkipped) || f.res.calls != calls {
		t.Fatalf("en espera no se llama a ArgosISP: %v", err)
	}
	// Vuelve: identidad refrescada, sin stale.
	*f.now = f.now.Add(time.Minute)
	f.res.err = nil
	if err := f.svc.ResolveCustomers(context.Background(), tt, []domain.Customer{*a}); err != nil {
		t.Fatal(err)
	}
	if f.store.idents[0].Stale || f.store.conn.Failures != 0 {
		t.Fatalf("recuperado: %+v", f.store.idents[0])
	}
}

type fakeConsumption struct{ windows []consumptionapi.Window }

func (f *fakeConsumption) Daily(_ context.Context, _ uuid.UUID, ws []consumptionapi.Window) ([][]consumptionapi.Day, error) {
	f.windows = ws
	out := make([][]consumptionapi.Day, len(ws))
	for i, w := range ws {
		out[i] = []consumptionapi.Day{{Day: w.From.Truncate(24 * time.Hour), UpBytes: 10, DownBytes: uint64(100 * (i + 1))}}
	}
	return out, nil
}

func (f *fakeConsumption) Summary(context.Context, uuid.UUID, uuid.UUID, netip.Addr, time.Time, *time.Time) (*consumptionapi.Summary, error) {
	return &consumptionapi.Summary{}, nil
}

// Consumo por servicio de ArgosISP: suma las IPs que tuvo el servicio, cada
// una recortada a su vigencia dentro del periodo pedido.
func TestConsumptionByServiceSumsValidityWindows(t *testing.T) {
	t.Parallel()
	f := newIntFixture(t)
	cons := &fakeConsumption{}
	f.svc.Readers.Consumption = func() (consumptionapi.Reader, bool) { return cons, true }
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	change := time.Date(2026, 10, 10, 8, 12, 0, 0, time.UTC)
	f.store.windows = []ServiceWindow{
		{ServiceID: 812, RealmID: uuid.Nil, Address: netip.MustParsePrefix("100.64.1.20/32"), From: from.Add(-48 * time.Hour), To: &change},
		{ServiceID: 812, RealmID: uuid.Nil, Address: netip.MustParsePrefix("100.64.1.33/32"), From: change},
	}
	a := f.addCustomer("100.64.1.50", nil, nil)
	_ = a
	res, err := f.svc.Consumption(f.ctx, ConsumptionInput{From: &from, To: &to, Granularity: "day", Items: []ConsumptionItem{
		{ExternalServiceID: ptr[int64](812)}, {Address: ptr("100.64.1.50")}, {Address: ptr("192.0.2.9")}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cons.windows) != 3 {
		t.Fatalf("ventanas = %+v", cons.windows)
	}
	if !cons.windows[0].From.Equal(from) || !cons.windows[0].To.Equal(change) || !cons.windows[1].From.Equal(change) || !cons.windows[1].To.Equal(to) {
		t.Fatalf("recorte a la vigencia: %+v", cons.windows[:2])
	}
	svc := res.Items[0]
	if svc.DownBytes != 300 || svc.UpBytes != 20 || len(svc.Addresses) != 2 || len(svc.Series) != 2 {
		t.Fatalf("servicio = %+v", svc)
	}
	if res.Items[1].DownBytes != 300 || res.Items[1].Error != nil {
		t.Fatalf("por dirección = %+v", res.Items[1])
	}
	if res.Items[2].Error == nil || *res.Items[2].Error != "not_found" {
		t.Fatalf("sin cliente = %+v", res.Items[2])
	}
	// Sin conector activo solo se acepta address.
	f.store.conn.Enabled = false
	if _, err := f.svc.Consumption(f.ctx, ConsumptionInput{From: &from, To: &to, Items: []ConsumptionItem{{ExternalServiceID: ptr[int64](812)}}}); err == nil {
		t.Fatal("sin conector, external_service_id → 422")
	}
	if _, err := f.svc.Consumption(f.ctx, ConsumptionInput{From: &from, To: &to, Items: make([]ConsumptionItem, 501)}); err == nil {
		t.Fatal("más de 500 elementos → 422")
	}
}
