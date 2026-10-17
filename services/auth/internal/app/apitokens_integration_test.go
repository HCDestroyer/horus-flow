package app

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// El enlace de conexión (contrato ArgosISP §1.4) emite el token con la
// plantilla a nombre de quien generó el enlace, solo si hoy tiene todos sus
// permisos y sigue siendo miembro activo; revocar es idempotente.
func TestIssueIntegrationToken(t *testing.T) {
	t.Parallel()
	f := newTokenFixture(t)
	req := api.IntegrationTokenRequest{TenantID: f.tenant, UserID: f.user, Template: "argosisp", Name: "ArgosISP (enlace)", Via: "connection_link"}
	tok, err := f.svc.IssueIntegrationToken(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	stored := f.store.tokens[len(f.store.tokens)-1]
	if !domain.LooksLikeAPIToken(tok.Raw) || stored.ID != tok.ID || len(stored.Permissions) != 5 || stored.UserID != f.user ||
		string(stored.Hash) == tok.Raw {
		t.Fatalf("token = %+v", stored)
	}
	// Exchange: el token emitido funciona con los permisos de la plantilla ∩ los del creador.
	if _, err := f.svc.ExchangeAPIToken(context.Background(), tok.Raw, "203.0.113.1"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RevokeIntegrationToken(context.Background(), f.tenant, tok.ID, "connection_link_failed"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ExchangeAPIToken(context.Background(), tok.Raw, ""); !errors.Is(err, api.ErrAPITokenInvalid) {
		t.Fatalf("revocado: %v", err)
	}
	if err := f.svc.RevokeIntegrationToken(context.Background(), f.tenant, tok.ID, "otra vez"); err != nil {
		t.Fatalf("revocar dos veces: %v", err)
	}
	// Sin un permiso de la plantilla (sso.issue) → no se emite.
	f.store.ms[0].Assignments[0].Permissions = []string{"customers.read", "snmp.read", "security.findings.read", "traffic.customer.read"}
	if _, err := f.svc.IssueIntegrationToken(context.Background(), req); !errors.Is(err, api.ErrIntegrationTokenNotGranted) {
		t.Fatalf("sin sso.issue: %v", err)
	}
	// Otro ISP → no se emite.
	req.TenantID = uuid.New()
	if _, err := f.svc.IssueIntegrationToken(context.Background(), req); !errors.Is(err, api.ErrIntegrationTokenNotGranted) {
		t.Fatalf("otro ISP: %v", err)
	}
}
