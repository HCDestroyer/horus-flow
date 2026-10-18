package app

import (
	"context"
	"encoding/json"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

type memAudit struct{ entries []authapi.AuditEntry }

func (m *memAudit) Record(_ context.Context, e authapi.AuditEntry) error {
	m.entries = append(m.entries, e)
	return nil
}

func mikrotikRouter(tenant uuid.UUID) *domain.Router {
	tun := "10.255.0.17"
	return &domain.Router{ID: uuid.MustParse("0192e333-0000-7000-8000-0000000000b2"), TenantID: tenant, SiteID: uuid.New(), Hostname: "rt-1",
		Vendor: domain.VendorMikroTik, ConnectionMode: domain.ConnectionWireGuard, ProtectionLevel: domain.ProtectionEncrypted,
		OnboardingState: devapi.OnboardingExporting, TunnelAddress: &tun, Version: 1}
}

func sp(s string) *string { return &s }

func TestTransportSet(t *testing.T) {
	t.Parallel()
	tenant := uuid.New()
	st := &memDirectStore{router: mikrotikRouter(tenant)}
	audit := &memAudit{}
	tr := NewTransport(st, []netip.Prefix{netip.MustParsePrefix("10.255.0.0/16")}, func() (authapi.AuditRecorder, bool) { return audit, true },
		nil, func() time.Time { return time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC) })
	ctx := asnCtx(tenant, PermCredentialsWrite)
	id := st.router.ID

	// Validaciones.
	cases := map[string]struct {
		in   TransportInput
		code string
	}{
		"modo desconocido":        {TransportInput{ConnectionMode: "l2tp"}, "VALIDATION_FAILED"},
		"directo en MikroTik":     {TransportInput{ConnectionMode: "direct", Path: sp("private")}, domain.CodeConnectionModeNotSupported},
		"ipsec sin identidad":     {TransportInput{ConnectionMode: "ipsec"}, "VALIDATION_FAILED"},
		"gre sin IP":              {TransportInput{ConnectionMode: "gre", Path: sp("private")}, "VALIDATION_FAILED"},
		"gre sin camino":          {TransportInput{ConnectionMode: "gre", RemoteAddress: sp("198.51.100.7")}, "VALIDATION_FAILED"},
		"gre con id":              {TransportInput{ConnectionMode: "gre", RemoteAddress: sp("198.51.100.7"), RemoteID: sp("rt.example"), Path: sp("private")}, "VALIDATION_FAILED"},
		"IP de Horus":             {TransportInput{ConnectionMode: "gre", RemoteAddress: sp("10.255.9.9"), Path: sp("private")}, "VALIDATION_FAILED"},
		"IP privada especial":     {TransportInput{ConnectionMode: "gre", RemoteAddress: sp("127.0.0.1"), Path: sp("private")}, "VALIDATION_FAILED"},
		"id inyectado":            {TransportInput{ConnectionMode: "ipsec", RemoteID: sp("rt\" x")}, "VALIDATION_FAILED"},
		"gre por Internet sin ok": {TransportInput{ConnectionMode: "gre", RemoteAddress: sp("198.51.100.7"), Path: sp("internet")}, domain.CodeUnencryptedConfirmation},
	}
	for name, tc := range cases {
		if _, err := tr.Set(ctx, id, tc.in); !isCode(err, tc.code) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if len(st.events) != 0 || len(audit.entries) != 0 {
		t.Fatalf("una petición rechazada cambió algo: %v %v", st.events, audit.entries)
	}
	// Sin permiso: 403; router de otro tenant: 404.
	if _, err := tr.Set(asnCtx(tenant, "devices.read"), id, TransportInput{ConnectionMode: "ipsec", RemoteAddress: sp("198.51.100.7")}); !isCode(err, "PERMISSION_DENIED") {
		t.Fatalf("sin permiso: %v", err)
	}
	if _, err := tr.Set(asnCtx(uuid.New(), PermCredentialsWrite), uuid.New(), TransportInput{ConnectionMode: "ipsec"}); !isCode(err, domain.CodeRouterNotFound) {
		t.Fatalf("otro router: %v", err)
	}

	// IPsec con IP pública: la identidad por defecto es la IP; vuelve a pending_configuration y conserva la IP de túnel.
	r, err := tr.Set(ctx, id, TransportInput{ConnectionMode: "ipsec", RemoteAddress: sp("198.51.100.7")})
	if err != nil {
		t.Fatal(err)
	}
	if r.ConnectionMode != "ipsec" || r.ProtectionLevel != domain.ProtectionEncrypted || *r.TunnelRemoteID != "198.51.100.7" ||
		r.OnboardingState != devapi.OnboardingPending || r.TunnelAddress == nil {
		t.Fatalf("ipsec: %+v", r)
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != AuditTransportChanged || len(st.events) != 1 {
		t.Fatalf("auditoría/eventos: %+v / %d", audit.entries, len(st.events))
	}
	ev := st.events[0].Data.(map[string]any)
	if ev["connection_mode"] != "ipsec" || ev["protection_level"] != "encrypted" || !slices.Contains(ev["changed_fields"].([]string), "connection_mode") {
		t.Fatalf("evento: %v", ev)
	}
	// Idempotente.
	if _, err := tr.Set(ctx, id, TransportInput{ConnectionMode: "ipsec", RemoteAddress: sp("198.51.100.7")}); err != nil || len(audit.entries) != 1 {
		t.Fatalf("repetir: %v (%d auditorías)", err, len(audit.entries))
	}
	// GRE por un camino privado: sin confirmación.
	r, err = tr.Set(ctx, id, TransportInput{ConnectionMode: "gre", RemoteAddress: sp("198.51.100.7"), Path: sp("private")})
	if err != nil || r.ProtectionLevel != domain.ProtectionUnencryptedPrivate || r.TunnelRemoteID != nil {
		t.Fatalf("gre privado: %+v %v", r, err)
	}
	// GRE por Internet con confirmación explícita: auditado con la confirmación.
	r, err = tr.Set(ctx, id, TransportInput{ConnectionMode: "gre", RemoteAddress: sp("198.51.100.7"), Path: sp("internet"), ConfirmUnencryptedInternet: true})
	if err != nil || r.ProtectionLevel != domain.ProtectionUnencryptedInternet {
		t.Fatalf("gre internet: %+v %v", r, err)
	}
	last := audit.entries[len(audit.entries)-1]
	if last.Changes["confirm_unencrypted_internet"] != true || last.Changes["path"] != "internet" {
		t.Fatalf("auditoría sin confirmación: %v", last.Changes)
	}
	b, _ := json.Marshal(last.Changes)
	if !strings.Contains(string(b), `"to":"unencrypted_internet"`) {
		t.Fatalf("auditoría sin nivel: %s", b)
	}
	// De vuelta a WireGuard: sin IP pública ni identidad.
	r, err = tr.Set(ctx, id, TransportInput{ConnectionMode: "wireguard"})
	if err != nil || r.TunnelRemoteAddress != nil || r.ProtectionLevel != domain.ProtectionEncrypted {
		t.Fatalf("wireguard: %+v %v", r, err)
	}
}

func TestTransportHuaweiDirect(t *testing.T) {
	t.Parallel()
	tenant := uuid.New()
	rt := huaweiRouter(tenant)
	mgmt := "198.51.100.20"
	rt.ManagementAddress = &mgmt
	st := &memDirectStore{router: rt}
	tr := NewTransport(st, nil, nil, nil, nil)
	ctx := asnCtx(tenant, PermCredentialsWrite)
	if _, err := tr.Set(ctx, rt.ID, TransportInput{ConnectionMode: "wireguard"}); !isCode(err, domain.CodeConnectionModeNotSupported) {
		t.Fatalf("wireguard en Huawei: %v", err)
	}
	// Directo por Internet exige confirmación; por camino privado no.
	if _, err := tr.Set(ctx, rt.ID, TransportInput{ConnectionMode: "direct", Path: sp("internet")}); !isCode(err, domain.CodeUnencryptedConfirmation) {
		t.Fatalf("directo internet: %v", err)
	}
	r, err := tr.Set(ctx, rt.ID, TransportInput{ConnectionMode: "direct", Path: sp("private")})
	if err != nil || domain.ProtectionOf(r.ConnectionMode, r.ProtectionLevel) != domain.ProtectionUnencryptedPrivate || r.ManagementAddress == nil {
		t.Fatalf("directo privado: %+v %v", r, err)
	}
	// A IPsec con FQDN (IP dinámica): se borra la dirección de gestión.
	r, err = tr.Set(ctx, rt.ID, TransportInput{ConnectionMode: "ipsec", RemoteID: sp("NE40E-1.ISP.example")})
	if err != nil || r.ManagementAddress != nil || *r.TunnelRemoteID != "ne40e-1.isp.example" || r.TunnelRemoteAddress != nil {
		t.Fatalf("ipsec fqdn: %+v %v", r, err)
	}
	// Y de vuelta a directo: la IP de túnel deja de ser su identidad.
	tun := "10.255.0.30"
	st.router.TunnelAddress = &tun
	r, err = tr.Set(ctx, rt.ID, TransportInput{ConnectionMode: "direct", Path: sp("private")})
	if err != nil || r.TunnelAddress != nil || r.TunnelRemoteID != nil {
		t.Fatalf("directo: %+v %v", r, err)
	}
}

func TestTunnelPSK(t *testing.T) {
	t.Parallel()
	tenant := uuid.New()
	st := &memDirectStore{router: mikrotikRouter(tenant)}
	on := NewOnboarding(st, envelope.Ephemeral(), nil)
	ctx := context.Background()
	if _, err := on.RevealTunnelPSK(ctx, tenant, st.router.ID); err != devapi.ErrNoCredential {
		t.Fatalf("sin PSK: %v", err)
	}
	psk, err := on.IssueTunnelPSK(ctx, tenant, st.router.ID)
	if err != nil || len(psk) != 48 {
		t.Fatalf("psk = %q, %v", psk, err)
	}
	if len(st.creds) != 1 || st.creds[0].Kind != domain.CredentialIPsecPSK || strings.Contains(string(st.creds[0].Ciphertext), psk) {
		t.Fatalf("credencial: %+v", st.creds)
	}
	got, err := on.RevealTunnelPSK(ctx, tenant, st.router.ID)
	if err != nil || got != psk {
		t.Fatalf("reveal = %q, %v", got, err)
	}
	// Otro tenant (AAD distinto) no descifra.
	if _, err := on.RevealTunnelPSK(ctx, uuid.New(), st.router.ID); err == nil {
		t.Fatal("PSK descifrada con otro tenant")
	}
	if _, err := on.IssueTunnelPSK(ctx, tenant, uuid.New()); err != devapi.ErrRouterNotFound {
		t.Fatalf("router inexistente: %v", err)
	}
}

func TestParseIKEID(t *testing.T) {
	t.Parallel()
	ok := map[string]string{"198.51.100.7": "ip", "rt-1.isp.example": "fqdn", "noc@isp.example": "user_fqdn", "keyid:rt_1": "key_id"}
	for in, kind := range ok {
		if k, _, valid := domain.ParseIKEID(in); !valid || k != kind {
			t.Errorf("%s = %s, %v", in, k, valid)
		}
	}
	for _, in := range []string{"", "rt", "a b.example", "10.0.0.1/32", "::1", "keyid:", "rt;x.example", "127.0.0.1", "@isp.example"} {
		if _, _, valid := domain.ParseIKEID(in); valid {
			t.Errorf("%q aceptada", in)
		}
	}
}
