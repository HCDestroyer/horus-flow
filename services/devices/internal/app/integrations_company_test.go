package app

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	secstatesapi "github.com/hcdestroyer/horus-flow/services/detection/api/securitystates"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// Pruebas de H7 (multiempresa), H8 (consulta 360 por external_service_id) y
// H9 (estado de seguridad por lotes) del contrato ArgosISP 1.2.0.

func (f *fakeIntStore) IdentityAt(_ context.Context, _ pgdb.TenantID, cid uuid.UUID, at time.Time) (*domain.ExternalIdentity, error) {
	for i := range f.idents {
		if f.idents[i].CustomerID == cid && f.idents[i].ValidAt(at) {
			x := f.idents[i]
			return &x, nil
		}
	}
	return nil, nil
}

func (f *fakeIntStore) ServiceIdentities(_ context.Context, _ pgdb.TenantID, ids []int64, at *time.Time, _ []uuid.UUID) ([]ServiceIdentity, error) {
	var out []ServiceIdentity
	for _, i := range f.idents {
		if i.ExternalServiceID == nil || !slices.Contains(ids, *i.ExternalServiceID) {
			continue
		}
		if (at == nil && i.ValidTo != nil) || (at != nil && !i.ValidAt(*at)) {
			continue
		}
		out = append(out, ServiceIdentity{Identity: i, Customer: *f.customers[i.CustomerID]})
	}
	slices.SortStableFunc(out, func(a, b ServiceIdentity) int { return b.Customer.LastSeen.Compare(a.Customer.LastSeen) })
	return out, nil
}

func (f *fakeIntStore) SetServiceCompany(_ context.Context, _ pgdb.TenantID, svc, company int64) (int, error) {
	n := 0
	for i := range f.idents {
		x := &f.idents[i]
		if x.ValidTo == nil && x.ExternalServiceID != nil && *x.ExternalServiceID == svc && (x.ExternalCompanyID == nil || *x.ExternalCompanyID != company) {
			x.ExternalCompanyID, x.CompanyName, x.CompanyNIT = &company, nil, nil
			n++
		}
	}
	return n, nil
}

func (f *fakeIntStore) CustomersByExternal(_ context.Context, _ pgdb.TenantID, svc, _ *int64) ([]domain.Customer, error) {
	var out []domain.Customer
	for _, i := range f.idents {
		if i.ValidTo == nil && svc != nil && i.ExternalServiceID != nil && *i.ExternalServiceID == *svc {
			out = append(out, *f.customers[i.CustomerID])
		}
	}
	return out, nil
}

func (f *fakeIntStore) PendingArgosWebhooks(context.Context, int) ([]ArgosWebhook, error) {
	out := f.pending
	f.pending = nil
	return out, nil
}

func (f *fakeIntStore) FinishArgosWebhook(context.Context, pgdb.TenantID, string, bool) error {
	return nil
}

type fakeSecStates struct {
	states map[uuid.UUID]secstatesapi.State
	asked  []uuid.UUID
}

func (r *fakeSecStates) SecurityStates(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]secstatesapi.State, error) {
	r.asked = ids
	out := map[uuid.UUID]secstatesapi.State{}
	for _, id := range ids {
		if s, ok := r.states[id]; ok {
			out[id] = s
		}
	}
	return out, nil
}

func companyRes(svc int64, company *int64, name string) domain.Resolution {
	r := domain.Resolution{Found: true, ExternalCustomerID: ptr[int64](455), ExternalServiceID: &svc, DisplayName: ptr("Ferretería López"),
		CompanyKnown: true, CompanyID: company}
	if name != "" {
		r.CompanyName, r.CompanyNIT = &name, ptr("1234567K")
	}
	return r
}

// H7: la empresa del resolutor se guarda en la identidad; el respaldo (sin
// empresa) no la borra; un webhook con empresa_id la refresca.
func TestCompanyStoredAndRefreshed(t *testing.T) {
	t.Parallel()
	f := newIntFixture(t)
	tt := pgdb.TenantID(f.tenant)
	a := f.addCustomer("100.64.1.20", nil, nil)
	f.res.res["100.64.1.20"] = companyRes(812, ptr[int64](2), "Fibra Sur")
	if err := f.svc.ResolveCustomers(context.Background(), tt, []domain.Customer{*a}); err != nil {
		t.Fatal(err)
	}
	cur := f.store.idents[0]
	if *cur.ExternalCompanyID != 2 || *cur.CompanyName != "Fibra Sur" || *cur.CompanyNIT != "1234567K" {
		t.Fatalf("empresa guardada = %+v", cur)
	}
	// Respaldo GET /ip/:ip: sin empresa → se conserva la conocida.
	fb := companyRes(812, nil, "")
	fb.CompanyKnown = false
	f.res.res["100.64.1.20"] = fb
	*f.now = f.now.Add(time.Hour)
	if err := f.svc.ResolveCustomers(context.Background(), tt, []domain.Customer{*a}); err != nil {
		t.Fatal(err)
	}
	if cur := f.store.idents[0]; cur.ExternalCompanyID == nil || *cur.ExternalCompanyID != 2 || *cur.CompanyName != "Fibra Sur" {
		t.Fatalf("el respaldo no borra la empresa: %+v", cur)
	}
	// Webhook de servicio con empresa_id: la identidad toma la empresa nueva
	// aunque la re-resolución (respaldo) no la informe.
	f.store.pending = []ArgosWebhook{{TenantID: f.tenant, ID: "evt-9", Event: "servicio.estado_cambiado",
		Payload: []byte(`{"id":"evt-9","evento":"servicio.estado_cambiado","payload":{"servicio_id":812,"estado":"activo","empresa_id":3}}`)}}
	if err := f.svc.ProcessWebhooks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cur := f.store.idents[0]; *cur.ExternalCompanyID != 3 || cur.CompanyName != nil {
		t.Fatalf("empresa refrescada por el webhook = %+v", cur)
	}
	// Un servicio sin empresa (empresa null en el resolutor 1.2.0) la deja en null.
	f.res.res["100.64.1.20"] = companyRes(812, nil, "")
	if err := f.svc.ResolveCustomers(context.Background(), tt, []domain.Customer{*a}); err != nil {
		t.Fatal(err)
	}
	if cur := f.store.idents[0]; cur.ExternalCompanyID != nil {
		t.Fatalf("empresa null = %+v", cur)
	}
	// Otro dueño por el respaldo: la vigencia nueva empieza sin empresa.
	other := domain.Resolution{Found: true, ExternalServiceID: ptr[int64](901)}
	f.res.res["100.64.1.20"] = other
	*f.now = f.now.Add(time.Hour)
	if err := f.svc.ResolveCustomers(context.Background(), tt, []domain.Customer{*a}); err != nil {
		t.Fatal(err)
	}
	if last := f.store.idents[len(f.store.idents)-1]; *last.ExternalServiceID != 901 || last.ExternalCompanyID != nil {
		t.Fatalf("vigencia nueva = %+v", last)
	}
}

func isKind(err error, k apperr.Kind) bool {
	ae, ok := apperr.As(err)
	return ok && ae.Kind == k
}

func isValidation(err error, field string) bool {
	ae, ok := apperr.As(err)
	if !ok || ae.Kind != apperr.KindInvalid {
		return false
	}
	for _, fe := range ae.Fields {
		if fe.Field == field {
			return true
		}
	}
	return false
}

// H8: consulta 360 por external_service_id (identidad vigente, o la vigente
// en at), exactamente uno de address o external_service_id, 404 sin identidad.
func TestIPLookupByExternalService(t *testing.T) {
	t.Parallel()
	f := newIntFixture(t)
	tt := pgdb.TenantID(f.tenant)
	v4 := f.addCustomer("100.64.1.20", nil, nil)
	v6 := f.addCustomer("2800:abcd:1:2::1", nil, nil)
	v4.LastSeen, v6.LastSeen = f.now.Add(-time.Minute), f.now.Add(-time.Hour)
	f.res.res["100.64.1.20"] = companyRes(812, ptr[int64](2), "Fibra Sur")
	f.res.res["2800:abcd:1:2::1"] = companyRes(812, ptr[int64](2), "Fibra Sur")
	if err := f.svc.ResolveCustomers(context.Background(), tt, []domain.Customer{*v4, *v6}); err != nil {
		t.Fatal(err)
	}
	opened := *f.now
	res, err := f.svc.IPLookup(f.ctx, IPLookupInput{ExternalServiceID: ptr[int64](812), Include: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Customer.ID != v4.ID || res.External == nil || *res.External.ExternalServiceID != 812 || *res.External.ExternalCompanyID != 2 {
		t.Fatalf("por servicio: cliente visto más recientemente con su identidad = %+v", res)
	}
	// La IP cambia de servicio: el servicio 812 ya no tiene identidad vigente → 404.
	*f.now = f.now.Add(time.Hour)
	f.res.res["100.64.1.20"] = domain.Resolution{Found: true, ExternalServiceID: ptr[int64](901)}
	f.res.res["2800:abcd:1:2::1"] = domain.Resolution{}
	if err := f.svc.ResolveCustomers(context.Background(), tt, []domain.Customer{*v4, *v6}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.IPLookup(f.ctx, IPLookupInput{ExternalServiceID: ptr[int64](812)}); !isKind(err, apperr.KindNotFound) {
		t.Fatalf("sin identidad vigente → 404: %v", err)
	}
	// Con at dentro de la vigencia anterior, resuelve al cliente de entonces.
	at := opened.Add(30 * time.Minute)
	res, err = f.svc.IPLookup(f.ctx, IPLookupInput{ExternalServiceID: ptr[int64](812), At: &at, Include: []string{}})
	if err != nil || res.Customer.ID != v4.ID || *res.External.ExternalServiceID != 812 {
		t.Fatalf("por servicio en at = %+v %v", res, err)
	}
	// Exactamente uno de los dos.
	if _, err := f.svc.IPLookup(f.ctx, IPLookupInput{Address: ptr("100.64.1.20"), ExternalServiceID: ptr[int64](812)}); !isValidation(err, "address") {
		t.Fatalf("con ambos → 422: %v", err)
	}
	if _, err := f.svc.IPLookup(f.ctx, IPLookupInput{}); !isValidation(err, "address") {
		t.Fatalf("sin ninguno → 422: %v", err)
	}
	if _, err := f.svc.IPLookup(f.ctx, IPLookupInput{ExternalServiceID: ptr[int64](0)}); !isValidation(err, "external_service_id") {
		t.Fatalf("id no positivo → 422: %v", err)
	}
}

// H9: estado de seguridad por lotes desde detection, por IP y por servicio
// (la IP de peor estado), con not_found por elemento y límites del lote.
func TestSecurityStatesBatch(t *testing.T) {
	t.Parallel()
	f := newIntFixture(t)
	tt := pgdb.TenantID(f.tenant)
	p := &authz.Principal{Type: authz.TypeUser, Subject: uuid.NewString(), Scope: authz.ScopeTenant, TenantID: f.tenant,
		Perms: map[string][]string{"security.findings.read": {"*"}}}
	ctx := authz.WithPrincipal(context.Background(), p)
	v4 := f.addCustomer("100.64.1.20", nil, nil)
	v6 := f.addCustomer("2800:abcd:1:2::1", nil, nil)
	plain := f.addCustomer("100.64.1.30", nil, nil)
	plain.FirstSeen = f.now.Add(-48 * time.Hour)
	f.res.res["100.64.1.20"] = companyRes(812, nil, "")
	f.res.res["2800:abcd:1:2::1"] = companyRes(812, nil, "")
	if err := f.svc.ResolveCustomers(context.Background(), tt, []domain.Customer{*v4, *v6}); err != nil {
		t.Fatal(err)
	}
	high := "high"
	since := f.now.Add(-2 * time.Hour)
	sec := &fakeSecStates{states: map[uuid.UUID]secstatesapi.State{
		v4.ID: {State: "clean", Since: since},
		v6.ID: {State: "infected", OpenFindings: 2, MaxSeverity: &high, Since: since},
	}}
	// Sin detection en el proceso → 503.
	if _, err := f.svc.SecurityStates(ctx, SecurityStatesInput{Items: []SecurityStateItem{{Address: ptr("100.64.1.20")}}}); !isKind(err, apperr.KindUnavailable) {
		t.Fatalf("sin detection → 503: %v", err)
	}
	f.svc.Readers.SecurityStates = func() (secstatesapi.Reader, bool) { return sec, true }
	res, err := f.svc.SecurityStates(ctx, SecurityStatesInput{Items: []SecurityStateItem{
		{ExternalServiceID: ptr[int64](812)}, {Address: ptr("100.64.1.20")}, {Address: ptr("100.64.1.30")},
		{ExternalServiceID: ptr[int64](999)}, {Address: ptr("192.0.2.9")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	svc := res.Items[0]
	if svc.Error != nil || svc.State != "infected" || svc.OpenFindings != 2 || *svc.MaxSeverity != "high" || !strings.HasPrefix(*svc.Address, "2800:abcd:1:2::1") {
		t.Fatalf("por servicio: la IP de peor estado = %+v", svc)
	}
	if byIP := res.Items[1]; byIP.State != "clean" || *byIP.ExternalServiceID != 812 || !byIP.Since.Equal(since) {
		t.Fatalf("por IP con su servicio = %+v", byIP)
	}
	if none := res.Items[2]; none.State != "clean" || none.MaxSeverity != nil || !none.Since.Equal(plain.FirstSeen) || none.ExternalServiceID != nil {
		t.Fatalf("sin estado en detection: limpio desde que se vio = %+v", none)
	}
	for _, i := range []int{3, 4} {
		if e := res.Items[i].Error; e == nil || *e != "not_found" {
			t.Fatalf("elemento %d = %+v", i, res.Items[i])
		}
	}
	if len(sec.asked) != 3 {
		t.Fatalf("una sola consulta a detection, sin repetir clientes: %v", sec.asked)
	}
	// Límites del lote y validación por elemento.
	if _, err := f.svc.SecurityStates(ctx, SecurityStatesInput{Items: make([]SecurityStateItem, 501)}); !isValidation(err, "items") {
		t.Fatalf("más de 500 → 422: %v", err)
	}
	if _, err := f.svc.SecurityStates(ctx, SecurityStatesInput{}); !isValidation(err, "items") {
		t.Fatalf("lote vacío → 422: %v", err)
	}
	if _, err := f.svc.SecurityStates(ctx, SecurityStatesInput{Items: []SecurityStateItem{{Address: ptr("x"), ExternalServiceID: ptr[int64](1)}}}); !isValidation(err, "items[0]") {
		t.Fatalf("ambos en un elemento → 422: %v", err)
	}
	if _, err := f.svc.SecurityStates(ctx, SecurityStatesInput{Items: []SecurityStateItem{{Address: ptr("no-es-ip")}}}); !isValidation(err, "items[0].address") {
		t.Fatalf("IP no válida → 422: %v", err)
	}
	// Sin conector activo solo se acepta address.
	f.store.conn.Enabled = false
	if _, err := f.svc.SecurityStates(ctx, SecurityStatesInput{Items: []SecurityStateItem{{ExternalServiceID: ptr[int64](812)}}}); !isValidation(err, "items") {
		t.Fatalf("sin conector, external_service_id → 422: %v", err)
	}
	// Sin security.findings.read → 403.
	if _, err := f.svc.SecurityStates(f.ctx, SecurityStatesInput{Items: []SecurityStateItem{{Address: ptr("100.64.1.20")}}}); !isKind(err, apperr.KindForbidden) {
		t.Fatalf("sin permiso → 403: %v", err)
	}
}
