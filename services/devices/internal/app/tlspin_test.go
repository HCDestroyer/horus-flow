package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// Huellas de prueba (valores obviamente falsos).
var (
	fpA = strings.TrimSuffix(strings.Repeat("AA:", 32), ":")
	fpB = strings.TrimSuffix(strings.Repeat("BB:", 32), ":")
)

type fakePinStore struct {
	router *domain.Router
	cred   *domain.Credential
	audits []string
}

func (f *fakePinStore) GetRouter(_ context.Context, _ pgdb.TenantID, id uuid.UUID) (*domain.Router, error) {
	if f.router == nil || f.router.ID != id {
		return nil, domain.ErrNotFound
	}
	r := *f.router
	return &r, nil
}

func (f *fakePinStore) GetCredential(context.Context, pgdb.TenantID, uuid.UUID, string) (*domain.Credential, error) {
	if f.cred == nil {
		return nil, domain.ErrNotFound
	}
	c := *f.cred
	return &c, nil
}

// ObserveTLSFingerprint reproduce la sentencia SQL del adaptador postgres.
func (f *fakePinStore) ObserveTLSFingerprint(_ context.Context, _ pgdb.TenantID, _ uuid.UUID, observed, result string, at time.Time) (*domain.Credential, error) {
	c := f.cred
	switch {
	case c.TLSFingerprintSHA256 != nil && strings.EqualFold(*c.TLSFingerprintSHA256, observed):
		c.TLSPending, c.TLSPendingAt = nil, nil
	case c.TLSPending != nil && *c.TLSPending == observed:
	default:
		o := observed
		c.TLSPending, c.TLSPendingAt = &o, &at
	}
	if result != "" {
		c.LastResult = &result
	}
	out := *c
	return &out, nil
}

func (f *fakePinStore) ConfirmTLSFingerprint(_ context.Context, _ pgdb.TenantID, _ uuid.UUID, fp, by string, at time.Time) (*domain.Credential, error) {
	c := f.cred
	if c.TLSPending == nil || *c.TLSPending != fp {
		return nil, domain.ErrTLSPendingChanged
	}
	p := fp
	c.TLSFingerprintSHA256, c.TLSPending, c.TLSPendingAt, c.TLSConfirmedAt, c.TLSConfirmedBy = &p, nil, nil, &at, &by
	out := *c
	return &out, nil
}

func (f *fakePinStore) Emit(_ context.Context, evs []outbox.Event) error {
	for _, e := range evs {
		f.audits = append(f.audits, e.Data.(map[string]any)["action"].(string))
	}
	return nil
}

type fakeProber struct {
	fp    string
	err   error
	calls int
}

func (p *fakeProber) ProbeFingerprint(context.Context, string) (string, error) {
	p.calls++
	return p.fp, p.err
}

func pinFixture() (*fakePinStore, *fakeProber, *TLSPins, uuid.UUID) {
	tenant := uuid.New()
	tunnel := "10.255.0.2"
	r := &domain.Router{ID: uuid.New(), SiteID: uuid.New(), TunnelAddress: &tunnel}
	st := &fakePinStore{router: r, cred: &domain.Credential{ID: uuid.New(), RouterID: r.ID, Kind: domain.CredentialRouterOSAPI, Username: "horus"}}
	pr := &fakeProber{fp: strings.ToLower(fpA)}
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	return st, pr, NewTLSPins(st, pr, nil, func() time.Time { return now }, nil), tenant
}

func pinCtx(tenant uuid.UUID, perms ...string) context.Context { return asnCtx(tenant, perms...) }

func code(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Code
	}
	return ""
}

// Alta: la huella se captura (TOFU) pero queda pendiente; solo un
// administrador la fija y debe coincidir exactamente.
func TestTLSPinCaptureAndConfirm(t *testing.T) {
	st, pr, svc, tenant := pinFixture()
	ctx := pinCtx(tenant, PermTLSPinRead, PermTLSPinCapture, PermTLSPinConfirm)
	v, err := svc.Get(ctx, st.router.ID)
	if err != nil || v.Status != domain.TLSPinNotCaptured {
		t.Fatalf("inicial = %+v %v", v, err)
	}
	v, err = svc.Capture(ctx, st.router.ID)
	if err != nil || v.Status != domain.TLSPinPending || v.ObservedSHA256 == nil || *v.ObservedSHA256 != fpA || v.PinnedSHA256 != nil {
		t.Fatalf("captura = %+v %v", v, err)
	}
	if len(st.audits) != 1 || st.audits[0] != AuditTLSObserved {
		t.Fatalf("auditoría = %v", st.audits)
	}
	// Repetir la captura con la misma huella no vuelve a auditar.
	if _, err := svc.Capture(ctx, st.router.ID); err != nil || len(st.audits) != 1 || pr.calls != 2 {
		t.Fatalf("recaptura: %v %v", err, st.audits)
	}
	if _, err := svc.Confirm(ctx, st.router.ID, fpB); code(err) != domain.CodeTLSFingerprintMismatch {
		t.Fatalf("huella distinta: %v", err)
	}
	if _, err := svc.Confirm(ctx, st.router.ID, "zz"); code(err) != "VALIDATION_FAILED" && err == nil {
		t.Fatalf("formato: %v", err)
	}
	v, err = svc.Confirm(ctx, st.router.ID, strings.ReplaceAll(strings.ToLower(fpA), ":", ""))
	if err != nil || v.Status != domain.TLSPinConfirmed || *v.PinnedSHA256 != fpA || v.ConfirmedAt == nil || v.ObservedSHA256 != nil {
		t.Fatalf("confirmación = %+v %v", v, err)
	}
	if st.audits[len(st.audits)-1] != AuditTLSConfirmed {
		t.Fatalf("auditoría = %v", st.audits)
	}
	// Sin nada pendiente, confirmar es un conflicto.
	if _, err := svc.Confirm(ctx, st.router.ID, fpA); code(err) != domain.CodeTLSFingerprintMismatch {
		t.Fatalf("sin pendiente: %v", err)
	}
}

// Cambio de huella: nunca se acepta sola; queda pendiente, se audita una vez
// y volver a fijarla es una acción explícita auditada con la anterior.
func TestTLSPinChangedRequiresRepin(t *testing.T) {
	st, pr, svc, tenant := pinFixture()
	pinned := fpA
	st.cred.TLSFingerprintSHA256 = &pinned
	pr.fp = fpB
	ctx := pinCtx(tenant, PermTLSPinRead, PermTLSPinCapture, PermTLSPinConfirm)
	v, err := svc.Capture(ctx, st.router.ID)
	if err != nil || v.Status != domain.TLSPinChanged || *v.PinnedSHA256 != fpA || *v.ObservedSHA256 != fpB ||
		v.LastResult == nil || *v.LastResult != "tls_fingerprint_changed" {
		t.Fatalf("cambio = %+v %v", v, err)
	}
	if _, err := svc.Capture(ctx, st.router.ID); err != nil || len(st.audits) != 1 || st.audits[0] != AuditTLSChanged {
		t.Fatalf("auditoría del cambio = %v %v", st.audits, err)
	}
	// Sin permiso de credenciales no se puede volver a fijar.
	if _, err := svc.Confirm(pinCtx(tenant, PermTLSPinCapture), st.router.ID, fpB); err == nil {
		t.Fatal("confirmó sin devices.credentials.write")
	}
	v, err = svc.Confirm(ctx, st.router.ID, fpB)
	if err != nil || v.Status != domain.TLSPinConfirmed || *v.PinnedSHA256 != fpB || st.audits[len(st.audits)-1] != AuditTLSRepinned {
		t.Fatalf("re-fijado = %+v %v %v", v, err, st.audits)
	}
	// Si el router vuelve a la huella fijada, no queda nada pendiente.
	if v, _ := svc.Capture(ctx, st.router.ID); v.Status != domain.TLSPinConfirmed || v.ObservedSHA256 != nil {
		t.Fatalf("igual a la fijada = %+v", v)
	}
}

func TestTLSPinCaptureErrors(t *testing.T) {
	st, pr, svc, tenant := pinFixture()
	ctx := pinCtx(tenant, PermTLSPinCapture)
	pr.err = ErrRouterUnreachable
	if _, err := svc.Capture(ctx, st.router.ID); code(err) != domain.CodeRouterUnreachable {
		t.Fatalf("inalcanzable: %v", err)
	}
	st.router.TunnelAddress = nil
	if _, err := svc.Capture(ctx, st.router.ID); code(err) != domain.CodeRouterUnreachable {
		t.Fatalf("sin túnel: %v", err)
	}
	if _, err := svc.Capture(ctx, uuid.New()); code(err) != domain.CodeRouterNotFound {
		t.Fatalf("otro router: %v", err)
	}
	if _, err := svc.Get(pinCtx(tenant), st.router.ID); err == nil {
		t.Fatal("lectura sin devices.read")
	}
}

type fakeNetwatch struct {
	err   error
	calls int
}

func (f *fakeNetwatch) ReadNetwatch(context.Context, ReadTarget) ([]devapi.NetwatchProbe, error) {
	f.calls++
	return nil, f.err
}

type fakeAPICreds struct{ st *fakePinStore }

func (f fakeAPICreds) APICredential(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Credential, string, error) {
	c, err := f.st.GetCredential(ctx, t, id, domain.CredentialRouterOSAPI)
	return c, "fake-pass", err
}

// En segundo plano: sin huella fijada se captura (pendiente) sin leer datos;
// con una huella distinta se registra el cambio y la lectura falla.
func TestRouterProbesNeverAcceptFingerprint(t *testing.T) {
	st, pr, svc, tenant := pinFixture()
	nw := &fakeNetwatch{}
	probes := NewRouterProbes(st, fakeAPICreds{st}, nw).WithTLSPins(svc)
	_, err := probes.ReadNetwatch(context.Background(), tenant, st.router.ID)
	if !errors.Is(err, devapi.ErrRouterAPIUnavailable) || nw.calls != 0 || st.cred.TLSPending == nil || st.cred.TLSFingerprintSHA256 != nil {
		t.Fatalf("sin huella: %v calls=%d cred=%+v", err, nw.calls, st.cred)
	}
	// Con la pendiente ya registrada no se vuelve a sondear en cada ciclo.
	_, _ = probes.ReadNetwatch(context.Background(), tenant, st.router.ID)
	if pr.calls != 1 {
		t.Fatalf("sondeos = %d", pr.calls)
	}
	pinned := fpB
	st.cred.TLSFingerprintSHA256, st.cred.TLSPending = &pinned, nil
	nw.err = ErrRouterFingerprint
	_, err = probes.ReadNetwatch(context.Background(), tenant, st.router.ID)
	if !errors.Is(err, devapi.ErrRouterAPIUnavailable) || *st.cred.TLSFingerprintSHA256 != fpB || st.cred.TLSPending == nil ||
		*st.cred.TLSPending != fpA || st.audits[len(st.audits)-1] != AuditTLSChanged {
		t.Fatalf("cambio en segundo plano: %v cred=%+v audits=%v", err, st.cred, st.audits)
	}
}
