package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// CreateTenant simula el alta: si inviteS es invited guarda el hash del
// token del primer administrador (el que acepta InvitationByHash).
func (f *fakeStore) CreateTenant(_ context.Context, nt app.NewTenant) (*app.InviteResult, error) {
	if f.inviteS == domain.MembershipInvited {
		f.invHash = nt.Admin.TokenHash
		f.inv = &domain.Invitation{ID: uuid.New(), TenantID: nt.Tenant.ID, MembershipID: uuid.New(), UserID: uuid.New(),
			ExpiresAt: nt.Admin.ExpiresAt, MembershipStatus: domain.MembershipInvited, UserStatus: domain.UserPending,
			UserEmail: nt.Admin.Email, TenantName: nt.Tenant.Name}
		return &app.InviteResult{MembershipID: f.inv.MembershipID, UserID: f.inv.UserID, Status: domain.MembershipInvited, UserCreated: true,
			TokenStored: true}, nil
	}
	return &app.InviteResult{MembershipID: uuid.New(), UserID: uuid.New(), Status: domain.MembershipActive}, nil
}

func (f *fakeStore) AcceptInvitation(context.Context, app.AcceptedInvitation) error { return nil }

func TestCreateTenantHTTP(t *testing.T) {
	e := newEnv(t)
	tok := e.token(authz.ScopePlatform, "platform_admin", 0)
	body := func(slug, email string) string {
		return `{"slug":"` + slug + `","name":"ISP Norte","country":"MX","timezone":"America/Mexico_City","initial_admin_email":"` + email + `"}`
	}
	idem := func() []string { return []string{"Idempotency-Key", uuid.NewString()} }

	// Cuenta activa existente: added, sin enlace; el cuerpo sigue siendo el Tenant.
	r := e.do("POST", "/api/v1/platform/tenants", tok, body("isp-norte", "root@horus.test"), idem()...)
	inv, _ := r.body["initial_admin_invitation"].(map[string]any)
	if r.status != http.StatusCreated || r.body["slug"] != "isp-norte" || r.body["id"] == nil || r.header.Get("ETag") != `"1"` ||
		!strings.HasSuffix(r.header.Get("Location"), "/api/v1/platform/tenants/"+r.body["id"].(string)) ||
		r.header.Get("Cache-Control") != "no-store" || inv == nil || inv["outcome"] != "added" || inv["invitation_url"] != nil ||
		inv["expires_at"] != nil || inv["email_sent"] != false || inv["user_id"] == nil {
		t.Fatalf("alta con cuenta existente = %d %v %s", r.status, r.header, r.raw)
	}
	for _, k := range []string{"invitation_url", "expires_at"} {
		if _, ok := inv[k]; !ok {
			t.Fatalf("InvitationIssued sin %s: %s", k, r.raw)
		}
	}
	if len(e.sender.got) != 0 {
		t.Fatal("se envió correo sin invitación")
	}

	// Email nuevo: invited con el enlace, el correo enviado y el token aceptado por inspect/accept.
	e.store.inviteS = domain.MembershipInvited
	r = e.do("POST", "/api/v1/platform/tenants", tok, body("isp-sur", "noc@isp-sur.test"), idem()...)
	inv, _ = r.body["initial_admin_invitation"].(map[string]any)
	link, _ := inv["invitation_url"].(string)
	const prefix = "https://horus.example/invitation#token="
	if r.status != http.StatusCreated || inv["outcome"] != "invited" || inv["email_sent"] != true || r.header.Get("Cache-Control") != "no-store" ||
		!strings.HasPrefix(link, prefix) || len(e.sender.got) != 1 || e.sender.got[0].URL != link {
		t.Fatalf("alta con email nuevo = %d %s", r.status, r.raw)
	}
	if exp, _ := inv["expires_at"].(string); !strings.HasSuffix(exp, "Z") {
		t.Fatalf("expires_at = %s", r.raw)
	}
	token := strings.TrimPrefix(link, prefix)
	ins := e.do("POST", "/api/v1/auth/invitations/inspect", "", `{"token":"`+token+`"}`)
	if ins.status != 200 || ins.body["email"] != "noc@isp-sur.test" || ins.body["tenant_name"] != "ISP Norte" || ins.body["needs_password"] != true {
		t.Fatalf("inspect = %d %s", ins.status, ins.raw)
	}
	if acc := e.do("POST", "/api/v1/auth/invitations/accept", "", `{"token":"`+token+`","password":"frase-larga-segura-987"}`); acc.status != http.StatusNoContent {
		t.Fatalf("accept = %d %s", acc.status, acc.raw)
	}

	// El correo falla: el alta sigue (201) con email_sent=false y el enlace.
	e.sender.err = errors.New("smtp: RCPT: 550")
	r = e.do("POST", "/api/v1/platform/tenants", tok, body("isp-este", "noc@isp-este.test"), idem()...)
	inv, _ = r.body["initial_admin_invitation"].(map[string]any)
	if r.status != http.StatusCreated || inv["email_sent"] != false || inv["invitation_url"] == nil || inv["outcome"] != "invited" {
		t.Fatalf("correo fallido = %d %s", r.status, r.raw)
	}

	// Re-autenticación antigua: 403 sin alta.
	if r := e.do("POST", "/api/v1/platform/tenants", e.token(authz.ScopePlatform, "platform_admin", time.Hour), body("isp-x", "a@b.test"), idem()...); r.status != 403 {
		t.Fatalf("sin re-auth = %d %s", r.status, r.raw)
	}
}
