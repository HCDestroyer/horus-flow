package engine

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

// fakeSignals sirve filas fijas (solo lo que usan los tests).
type fakeSignals struct {
	Signals
	security  []SecurityRow
	customers map[ClientKey]Customer
	loc       Location
}

func (f *fakeSignals) Security(context.Context, uuid.UUID, time.Time, time.Time, Having) ([]SecurityRow, error) {
	return f.security, nil
}

func (f *fakeSignals) Customers(_ context.Context, _ uuid.UUID, keys []ClientKey) (map[ClientKey]Customer, error) {
	out := map[ClientKey]Customer{}
	for _, k := range keys {
		if c, ok := f.customers[k]; ok {
			out[k] = c
		}
	}
	return out, nil
}

func (f *fakeSignals) Locate(_ context.Context, _ uuid.UUID, _, _ time.Time, keys []ClientKey) (map[ClientKey]Location, error) {
	out := map[ClientKey]Location{}
	for _, k := range keys {
		out[k] = f.loc
	}
	return out, nil
}

type fakeSink struct {
	params  domain.Params
	allow   []AllowEntry
	applied []domain.Candidate
}

func (s *fakeSink) Params(context.Context, uuid.UUID) (domain.Params, error)   { return s.params, nil }
func (s *fakeSink) Allowlist(context.Context, uuid.UUID) ([]AllowEntry, error) { return s.allow, nil }
func (s *fakeSink) State(context.Context, uuid.UUID, string) (string, error)   { return "", nil }
func (s *fakeSink) SetState(context.Context, uuid.UUID, string, string) error  { return nil }
func (s *fakeSink) Expire(context.Context, uuid.UUID, time.Duration, time.Time) (int, error) {
	return 0, nil
}
func (s *fakeSink) Apply(_ context.Context, _ uuid.UUID, c []domain.Candidate, _ time.Time) (ApplyResult, error) {
	s.applied = append(s.applied, c...)
	return ApplyResult{Opened: len(c)}, nil
}

func smtpOnly() domain.Params {
	p := domain.DefaultParams()
	for _, d := range domain.AllDetectors {
		p.SetEnabled(d, false)
	}
	p.SMTP.Enabled = true
	p.AutoExpire = 0
	return p
}

// I1-11 criterio 3: un comercial usa el umbral de comerciales en SMTP.
func TestSMTPCommercialThreshold(t *testing.T) {
	realm := uuid.New()
	home := ClientKey{realm, netip.MustParseAddr("10.20.0.10")}
	biz := ClientKey{realm, netip.MustParseAddr("10.20.0.20")}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	sig := &fakeSignals{
		security: []SecurityRow{{Key: home, Site: uuid.New(), SMTPRemoteIPs: 50, SMTPFlows: 80, First: now.Add(-time.Hour), Last: now, MaxSampling: 1},
			{Key: biz, Site: uuid.New(), SMTPRemoteIPs: 50, SMTPFlows: 80, First: now.Add(-time.Hour), Last: now, MaxSampling: 1}},
		customers: map[ClientKey]Customer{biz: {ID: uuid.New(), Kind: "commercial", Known: true}},
		loc:       Location{Site: uuid.New(), Router: uuid.New()},
	}
	sink := &fakeSink{params: smtpOnly()}
	rep, err := New(sig, sink, nil, Options{}).Evaluate(context.Background(), uuid.New(), now, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Candidates) != 1 || rep.Candidates[0].Client != home.IP || rep.Candidates[0].Kind != domain.KindSpam {
		t.Fatalf("candidatos: %+v", rep.Candidates)
	}
	c := rep.Candidates[0]
	if c.Reasons[0].Data["threshold"] != 20 || c.CustomerKind != "residential" || c.CustomerID == uuid.Nil || c.RouterID == uuid.Nil {
		t.Fatalf("razón/cliente: %+v", c)
	}
	// El mismo volumen supera el umbral de comerciales si se baja.
	sink.params.SMTP.CommercialServers = 40
	rep, _ = New(sig, sink, nil, Options{}).Evaluate(context.Background(), uuid.New(), now, true)
	if len(rep.Candidates) != 2 {
		t.Fatalf("con umbral comercial 40: %d", len(rep.Candidates))
	}
}

// I1-11 criterio 4: muestreo declarado ⇒ confianza un nivel menos y razón.
func TestSamplingAndAllowlist(t *testing.T) {
	realm := uuid.New()
	k := ClientKey{realm, netip.MustParseAddr("10.20.0.10")}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	sig := &fakeSignals{security: []SecurityRow{{Key: k, Site: uuid.New(), SMTPRemoteIPs: 100, SMTPFlows: 300, Last: now, MaxSampling: 10, MinSampling: 10}},
		loc: Location{Site: uuid.New(), Router: uuid.New()}}
	sink := &fakeSink{params: smtpOnly()}
	rep, err := New(sig, sink, nil, Options{}).Evaluate(context.Background(), uuid.New(), now, true)
	if err != nil || len(rep.Candidates) != 1 {
		t.Fatalf("%v %+v", err, rep)
	}
	c := rep.Candidates[0]
	last := c.Reasons[len(c.Reasons)-1]
	if !c.SamplingLow || last.Code != "sampling_declared" || domain.ConfidenceLevel(c.Confidence) != domain.ConfidenceMedium {
		t.Fatalf("muestreo: conf=%v %+v", c.Confidence, last)
	}
	pf := netip.MustParsePrefix("10.20.0.0/24")
	sink.allow = []AllowEntry{{Prefix: &pf, Kinds: []string{domain.KindSpam}}}
	rep, _ = New(sig, sink, nil, Options{}).Evaluate(context.Background(), uuid.New(), now, true)
	if len(rep.Candidates) != 0 || rep.Skipped["allowlisted"] != 1 {
		t.Fatalf("allowlist: %+v", rep)
	}
	sink.allow = []AllowEntry{{Prefix: &pf, Kinds: []string{domain.KindC2}}}
	rep, _ = New(sig, sink, nil, Options{}).Evaluate(context.Background(), uuid.New(), now, true)
	if len(rep.Candidates) != 1 {
		t.Fatal("una allowlist de otro kind no debe aplicar")
	}
}

func TestHelpers(t *testing.T) {
	if fmtInt(1240) != "1240" || fmtInt(20000) != "20 000" || fmtInt(1234567) != "1 234 567" {
		t.Fatal(fmtInt(1234567))
	}
	if !inHours(23, 22, 7) || !inHours(3, 22, 7) || inHours(12, 22, 7) || !inHours(10, 9, 17) {
		t.Fatal("inHours")
	}
	got := map[int64]float64{}
	s := time.Unix(30, 0)
	spread(s, s.Add(time.Minute), func(m int64, f float64) { got[m] += f })
	if got[0] != 0.5 || got[1] != 0.5 {
		t.Fatalf("spread: %v", got)
	}
	tenant := uuid.New()
	k := ClientKey{uuid.New(), netip.MustParseAddr("10.0.0.1")}
	a, b := DerivedCustomerID(tenant, k), DerivedCustomerID(tenant, ClientKey{k.Realm, netip.MustParseAddr("10.0.0.1")})
	if a != b || a == DerivedCustomerID(uuid.New(), k) {
		t.Fatal("id derivado estable y por tenant")
	}
}
