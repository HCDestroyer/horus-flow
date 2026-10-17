package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	alertsapi "github.com/hcdestroyer/horus-flow/services/alerts/api"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// linkIntStore añade al fake de integraciones la lectura de un cliente.
type linkIntStore struct{ *fakeIntStore }

func (s linkIntStore) GetCustomer(_ context.Context, _ pgdb.TenantID, id uuid.UUID) (*domain.Customer, error) {
	if c, ok := s.customers[id]; ok {
		cp := *c
		return &cp, nil
	}
	return nil, domain.ErrNotFound
}

// fakeLinks reproduce en memoria la semántica del canje (CAS, repetición).
type fakeLinks struct {
	mu    sync.Mutex
	links map[uuid.UUID]*domain.ConnectionLink
}

func (f *fakeLinks) CreateConnectionLink(_ context.Context, _ pgdb.TenantID, l *domain.ConnectionLink) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, x := range f.links {
		if x.UsedAt == nil {
			delete(f.links, id) // el enlace nuevo anula el anterior sin usar
		}
	}
	c := *l
	f.links[l.ID] = &c
	return nil
}

func (f *fakeLinks) ClaimConnectionLink(_ context.Context, hash, req []byte, now time.Time, _ string) (*domain.ConnectionLink, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.links {
		if !bytes.Equal(l.CodeHash, hash) || !now.Before(l.ExpiresAt) {
			continue
		}
		if l.UsedAt != nil {
			if len(l.ResponseCiphertext) > 0 && bytes.Equal(l.RequestHash, req) {
				c := *l
				return &c, true, nil
			}
			return nil, false, domain.ErrNotFound
		}
		at := now
		l.UsedAt, l.RequestHash = &at, req
		c := *l
		return &c, false, nil
	}
	return nil, false, domain.ErrNotFound
}

func (f *fakeLinks) CompleteConnectionLink(_ context.Context, _ pgdb.TenantID, id uuid.UUID, ct, dek []byte, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links[id].ResponseCiphertext, f.links[id].ResponseDEK = ct, dek
	return nil
}

func (f *fakeLinks) ReleaseConnectionLink(_ context.Context, _ pgdb.TenantID, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if l := f.links[id]; l != nil && l.ResponseCiphertext == nil {
		l.UsedAt, l.RequestHash = nil, nil
	}
	return nil
}

func (f *fakeLinks) PurgeConnectionLinks(context.Context, time.Time) (int, error) { return 0, nil }

// fakePeer es ArgosISP: guarda lo que recibe y responde lo configurado.
type fakePeer struct {
	mu        sync.Mutex
	status    int
	resp      string
	err       error
	redeemed  []map[string]any
	ticketReq []map[string]any
	blocked   string // destinos que la guardia de egreso rechaza (subcadena)
}

func (p *fakePeer) CheckURL(_ context.Context, raw string) error {
	if p.blocked != "" && strings.Contains(raw, p.blocked) {
		return ErrArgosNotAllowed
	}
	return nil
}

func (p *fakePeer) RedeemLink(_ context.Context, origin string, body []byte) (int, []byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	m["_origin"] = origin
	p.redeemed = append(p.redeemed, m)
	return p.status, []byte(p.resp), p.err
}

func (p *fakePeer) RequestSSOTicket(_ context.Context, _ uuid.UUID, _, token string, body []byte) (int, []byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	m["_token"] = token
	p.ticketReq = append(p.ticketReq, m)
	return p.status, []byte(p.resp), p.err
}

type fakeTokens struct {
	mu         sync.Mutex
	notGranted bool
	issued     []authapi.IntegrationTokenRequest
	ids        []uuid.UUID
	revoked    []uuid.UUID
}

func (f *fakeTokens) IssueIntegrationToken(_ context.Context, r authapi.IntegrationTokenRequest) (authapi.IntegrationToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notGranted {
		return authapi.IntegrationToken{}, authapi.ErrIntegrationTokenNotGranted
	}
	f.issued = append(f.issued, r)
	id := uuid.New()
	f.ids = append(f.ids, id)
	return authapi.IntegrationToken{ID: id, Raw: "hf_pat_" + strings.Repeat("a", 43)}, nil
}

func (f *fakeTokens) RevokeIntegrationToken(_ context.Context, _, id uuid.UUID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, id)
	return nil
}

type fakeHooks struct {
	got []alertsapi.IntegrationWebhook
}

func (f *fakeHooks) UpsertIntegrationWebhook(_ context.Context, _ uuid.UUID, w alertsapi.IntegrationWebhook) (uuid.UUID, error) {
	f.got = append(f.got, w)
	if w.ChannelID != nil {
		return *w.ChannelID, nil
	}
	return uuid.New(), nil
}

type fakeTickets struct{ got []authapi.SSOTicketRequest }

func (f *fakeTickets) IssueSSOTicket(_ context.Context, r authapi.SSOTicketRequest) (authapi.SSOTicketIssued, error) {
	f.got = append(f.got, r)
	return authapi.SSOTicketIssued{Ticket: "ticket-de-prueba-0123456789", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

type fakeProfiles struct{}

func (fakeProfiles) UserProfile(_ context.Context, id uuid.UUID) (*authapi.UserProfile, error) {
	return &authapi.UserProfile{ID: id, Email: "ana@isp.test", DisplayName: "Ana"}, nil
}

type linkFixture struct {
	svc     *Integrations
	store   *fakeIntStore
	links   *fakeLinks
	peer    *fakePeer
	tokens  *fakeTokens
	hooks   *fakeHooks
	tickets *fakeTickets
	tenant  uuid.UUID
	user    uuid.UUID
	now     *time.Time
	ctx     context.Context
}

var allLinkPerms = []string{"settings.manage", "settings.read", "api_tokens.manage", "alerts.manage", "customers.read", "security.findings.read",
	"snmp.read", "sso.issue", "traffic.customer.read", "devices.read"}

func newLinkFixture(t *testing.T) *linkFixture {
	t.Helper()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	f := &linkFixture{tenant: uuid.New(), user: uuid.New(), now: &now, links: &fakeLinks{links: map[uuid.UUID]*domain.ConnectionLink{}},
		peer: &fakePeer{status: 200}, tokens: &fakeTokens{}, hooks: &fakeHooks{}, tickets: &fakeTickets{}}
	f.store = &fakeIntStore{customers: map[uuid.UUID]*domain.Customer{}, inbox: map[string]bool{}}
	f.svc = NewIntegrations(IntegrationsOptions{Store: linkIntStore{f.store}, Sealer: envelope.Ephemeral(), BaseURL: "https://horus.example",
		Now: func() time.Time { return *f.now }, Ecosystem: Ecosystem{Links: f.links, Peer: f.peer,
			Tokens:   func() (authapi.IntegrationTokens, bool) { return f.tokens, true },
			Webhooks: func() (alertsapi.IntegrationWebhooks, bool) { return f.hooks, true },
			Tickets:  func() (authapi.SSOTickets, bool) { return f.tickets, true },
			Profiles: func() (authapi.UserProfiles, bool) { return fakeProfiles{}, true }}})
	f.ctx = f.as(allLinkPerms...)
	return f
}

func (f *linkFixture) as(perms ...string) context.Context {
	m := map[string][]string{}
	for _, p := range perms {
		m[p] = []string{"*"}
	}
	return authz.WithPrincipal(context.Background(), &authz.Principal{Type: authz.TypeUser, Subject: f.user.String(), UserID: f.user,
		Scope: authz.ScopeTenant, TenantID: f.tenant, Perms: m, AuthTime: *f.now})
}

func (f *linkFixture) apiToken(id uuid.UUID) context.Context {
	return authz.WithPrincipal(context.Background(), &authz.Principal{Type: authz.TypeService, Subject: f.user.String(), Scope: authz.ScopeTenant,
		TenantID: f.tenant, Perms: map[string][]string{"sso.issue": {"*"}}, Via: authz.ViaAPIToken, APITokenID: id})
}

func codeOfErr(err error) string {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		if ae.Code == problem.CodeValidationFailed && len(ae.Fields) > 0 {
			return ae.Fields[0].Code
		}
		return ae.Code
	}
	return ""
}

func (f *linkFixture) generate(t *testing.T) string {
	t.Helper()
	l, err := f.svc.CreateConnectionLink(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	code, ok := strings.CutPrefix(l.URL, "https://horus.example/conectar#")
	if !ok || !domain.LooksLikeConnectionCode(code) || !l.ExpiresAt.Equal(f.now.Add(15*time.Minute)) {
		t.Fatalf("enlace = %+v", l)
	}
	return code
}

func redeemBody(code string) RedeemCodeInput {
	in := RedeemCodeInput{Codigo: code, Sistema: "argosisp", BaseURL: "https://argos.example", Token: "atk_prueba_1234"}
	in.Webhook = &struct {
		URL     string `json:"url"`
		Secreto string `json:"secreto"`
	}{URL: "https://argos.example/api/v1/integraciones/horus/webhook", Secreto: "secreto-de-prueba-0001"}
	return in
}

func TestConnectionLinkGenerateHashOnlyAndPermissions(t *testing.T) {
	t.Parallel()
	f := newLinkFixture(t)
	code := f.generate(t)
	for _, l := range f.links.links {
		if bytes.Contains(l.CodeHash, []byte(code)) || !bytes.Equal(l.CodeHash, domain.HashConnectionCode(code)) || l.CreatedBy != f.user {
			t.Fatal("solo se guarda el hash del código, con quien lo generó")
		}
	}
	// Un enlace nuevo anula el anterior sin usar.
	f.generate(t)
	if len(f.links.links) != 1 {
		t.Fatalf("enlaces vigentes = %d", len(f.links.links))
	}
	// Sin api_tokens.manage o alerts.manage (el canje emite un token y crea un canal) → 403.
	if _, err := f.svc.CreateConnectionLink(f.as("settings.manage", "alerts.manage")); codeOfErr(err) != problem.CodePermissionDenied {
		t.Fatalf("sin api_tokens.manage: %v", err)
	}
	// Sin un permiso de la plantilla (el token nunca lleva más que su creador) → 403.
	if _, err := f.svc.CreateConnectionLink(f.as("settings.manage", "api_tokens.manage", "alerts.manage")); codeOfErr(err) != problem.CodePermissionDenied {
		t.Fatalf("sin permisos de la plantilla: %v", err)
	}
	// Sin HORUS_PUBLIC_BASE_URL → 409.
	f.svc.baseURL = ""
	if _, err := f.svc.CreateConnectionLink(f.ctx); codeOfErr(err) != CodeIntegrationNotConfig {
		t.Fatalf("sin URL pública: %v", err)
	}
}

func TestConnectionCodeRedeemSingleUseIdempotentDisabled(t *testing.T) {
	t.Parallel()
	f := newLinkFixture(t)
	code := f.generate(t)
	in := redeemBody(code)
	body, replay, err := f.svc.RedeemConnectionCode(context.Background(), in, "203.0.113.9")
	if err != nil || replay {
		t.Fatalf("canje: %v %v", err, replay)
	}
	var out redeemResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	c := f.store.conn
	if out.Token == "" || out.Webhook.Secreto == "" || out.ConectorID != c.ID.String() ||
		out.Webhook.URL != "https://horus.example/api/v1/integrations/argosisp/webhook/"+c.ID.String() {
		t.Fatalf("respuesta = %s", body)
	}
	// Conector APAGADO, token de Argos cifrado y URL base con /api/v1.
	if c.Enabled || c.Active() || !c.HasToken() || bytes.Contains(c.TokenCiphertext, []byte("atk_prueba")) || c.BaseURL != "https://argos.example/api/v1" ||
		*c.LinkMethod != domain.LinkGenerated || *c.PeerAPITokenID != f.tokens.ids[0] || c.WebhookChannelID == nil {
		t.Fatalf("conector = %+v", c)
	}
	if f.tokens.issued[0].UserID != f.user || f.tokens.issued[0].Template != "argosisp" {
		t.Fatalf("token emitido = %+v", f.tokens.issued[0])
	}
	if len(f.hooks.got) != 1 || f.hooks.got[0].URL != in.Webhook.URL || f.hooks.got[0].Secret != in.Webhook.Secreto {
		t.Fatalf("canal webhook = %+v", f.hooks.got)
	}
	// La respuesta guardada para la idempotencia está cifrada.
	for _, l := range f.links.links {
		if bytes.Contains(l.ResponseCiphertext, []byte(out.Token)) {
			t.Fatal("respuesta en claro")
		}
	}
	// Misma petición repetida (reintento de red): misma respuesta, sin emitir otro token.
	again, replay, err := f.svc.RedeemConnectionCode(context.Background(), in, "203.0.113.9")
	if err != nil || !replay || !bytes.Equal(again, body) || len(f.tokens.issued) != 1 {
		t.Fatalf("repetición: %v %v", err, replay)
	}
	// Otra petición con el código usado (replay de un tercero) → 410.
	other := redeemBody(code)
	other.Token = "atk_otro_token_99"
	if _, _, err := f.svc.RedeemConnectionCode(context.Background(), other, ""); codeOfErr(err) != CodeConnectionLinkInvalid {
		t.Fatalf("código usado con otra petición: %v", err)
	}
	// Tras caducar, ni la repetición idempotente.
	*f.now = f.now.Add(16 * time.Minute)
	if _, _, err := f.svc.RedeemConnectionCode(context.Background(), in, ""); codeOfErr(err) != CodeConnectionLinkInvalid {
		t.Fatalf("caducado: %v", err)
	}
}

func TestConnectionCodeExpiredUnknownAndValidation(t *testing.T) {
	t.Parallel()
	f := newLinkFixture(t)
	code := f.generate(t)
	*f.now = f.now.Add(15*time.Minute + time.Second)
	if _, _, err := f.svc.RedeemConnectionCode(context.Background(), redeemBody(code), ""); codeOfErr(err) != CodeConnectionLinkInvalid {
		t.Fatalf("caducado: %v", err)
	}
	unknown, _ := domain.GenerateConnectionCode()
	if _, _, err := f.svc.RedeemConnectionCode(context.Background(), redeemBody(unknown), ""); codeOfErr(err) != CodeConnectionLinkInvalid {
		t.Fatalf("desconocido: %v", err)
	}
	for name, mut := range map[string]func(*RedeemCodeInput){
		"http":       func(in *RedeemCodeInput) { in.BaseURL = "http://argos.example" },
		"sistema":    func(in *RedeemCodeInput) { in.Sistema = "horus" },
		"webhook":    func(in *RedeemCodeInput) { in.Webhook.URL = "https://u:p@argos.example/hook" },
		"secreto":    func(in *RedeemCodeInput) { in.Webhook.Secreto = "corto" },
		"token":      func(in *RedeemCodeInput) { in.Token = "con espacio y más" },
		"sin código": func(in *RedeemCodeInput) { in.Codigo = "x" },
	} {
		in := redeemBody(code)
		mut(&in)
		if _, _, err := f.svc.RedeemConnectionCode(context.Background(), in, ""); codeOfErr(err) == "" || codeOfErr(err) == CodeConnectionLinkInvalid {
			t.Errorf("%s: %v (quiero 422)", name, err)
		}
	}
	if len(f.tokens.issued) != 0 {
		t.Fatal("un canje rechazado no emite tokens")
	}
}

func TestConnectionCodeSSRFReleasesCode(t *testing.T) {
	t.Parallel()
	f := newLinkFixture(t)
	code := f.generate(t)
	f.peer.blocked = "argos.interna"
	in := redeemBody(code)
	in.BaseURL = "https://argos.interna"
	if _, _, err := f.svc.RedeemConnectionCode(context.Background(), in, ""); codeOfErr(err) != "NOT_ALLOWED" {
		t.Fatalf("destino privado: %v", err)
	}
	if f.store.conn != nil || len(f.tokens.issued) != 0 {
		t.Fatal("un destino rechazado no cambia nada")
	}
	// El código sigue sirviendo (mientras no caduque) con un destino válido.
	if _, _, err := f.svc.RedeemConnectionCode(context.Background(), redeemBody(code), ""); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionCodeGeneratorLostPermissions(t *testing.T) {
	t.Parallel()
	f := newLinkFixture(t)
	code := f.generate(t)
	f.tokens.notGranted = true
	if _, _, err := f.svc.RedeemConnectionCode(context.Background(), redeemBody(code), ""); codeOfErr(err) != CodeConnectionLinkRejected {
		t.Fatalf("generador sin permisos: %v", err)
	}
	f.tokens.notGranted = false
	if _, _, err := f.svc.RedeemConnectionCode(context.Background(), redeemBody(code), ""); codeOfErr(err) != CodeConnectionLinkInvalid {
		t.Fatalf("el código queda consumido: %v", err)
	}
}

func TestConnectionRelinkRevokesPreviousToken(t *testing.T) {
	t.Parallel()
	f := newLinkFixture(t)
	if _, _, err := f.svc.RedeemConnectionCode(context.Background(), redeemBody(f.generate(t)), ""); err != nil {
		t.Fatal(err)
	}
	ch := *f.store.conn.WebhookChannelID
	if _, _, err := f.svc.RedeemConnectionCode(context.Background(), redeemBody(f.generate(t)), ""); err != nil {
		t.Fatal(err)
	}
	if len(f.tokens.revoked) != 1 || f.tokens.revoked[0] != f.tokens.ids[0] {
		t.Fatalf("revocados = %v", f.tokens.revoked)
	}
	if *f.store.conn.WebhookChannelID != ch || f.store.conn.PrevSecretCiphertext == nil {
		t.Fatal("reutiliza el canal y el secreto anterior vale 24 h")
	}
}

func peerOK(f *linkFixture) {
	f.peer.status, f.peer.resp = 200, `{"token":"atk_de_prueba_1234","webhook":{"url":"https://api.argos.example/api/v1/integraciones/horus/webhook",`+
		`"secreto":"secreto-de-prueba-0002"},"conector_id":17}`
}

func TestRedeemArgosLinkAsSystemB(t *testing.T) {
	t.Parallel()
	f := newLinkFixture(t)
	peerOK(f)
	code, _ := domain.GenerateConnectionCode()
	v, err := f.svc.RedeemLink(f.ctx, "https://panel.argos.example/conectar?api=https://api.argos.example#"+code)
	if err != nil {
		t.Fatal(err)
	}
	c := v.Connector
	if c.Enabled || c.BaseURL != "https://api.argos.example/api/v1" || *c.PeerConnectorID != "17" || *c.LinkMethod != domain.LinkRedeemed {
		t.Fatalf("conector = %+v", c)
	}
	sent := f.peer.redeemed[0]
	hook, _ := sent["webhook"].(map[string]any)
	if sent["_origin"] != "https://api.argos.example" || sent["codigo"] != code || sent["sistema"] != "horus" ||
		sent["base_url"] != "https://horus.example" || hook["url"] != *f.svc.WebhookURL(c) || len(hook["secreto"].(string)) < 16 {
		t.Fatalf("petición a Argos = %v", sent)
	}
	if f.tokens.issued[0].UserID != f.user || len(f.tokens.revoked) != 0 {
		t.Fatal("token emitido a nombre de quien canjea")
	}
	if f.hooks.got[0].URL != "https://api.argos.example/api/v1/integraciones/horus/webhook" || f.hooks.got[0].Secret != "secreto-de-prueba-0002" {
		t.Fatalf("canal = %+v", f.hooks.got)
	}
}

func TestRedeemArgosLinkFailuresRevokeToken(t *testing.T) {
	t.Parallel()
	code, _ := domain.GenerateConnectionCode()
	link := "https://panel.argos.example/conectar#" + code
	cases := map[string]struct {
		set  func(*fakePeer)
		want string
	}{
		"vencido":        {func(p *fakePeer) { p.status = 410 }, CodeConnectionLinkExpired},
		"rechazado":      {func(p *fakePeer) { p.status = 422 }, CodeConnectionLinkRejected},
		"caído":          {func(p *fakePeer) { p.err = ErrArgosUnavailable }, CodeIntegrationUnavailble},
		"respuesta mala": {func(p *fakePeer) { p.status, p.resp = 200, `{"token":"x"}` }, CodeIntegrationUnavailble},
		"webhook privado": {func(p *fakePeer) {
			p.status, p.blocked = 200, "argos.interna"
			p.resp = strings.ReplaceAll(okResp, "api.argos.example", "argos.interna")
		}, CodeIntegrationUnavailble},
		"url sin https":     {func(p *fakePeer) { p.status, p.resp = 200, strings.ReplaceAll(okResp, "https://api", "http://api") }, CodeIntegrationUnavailble},
		"guardia de egreso": {func(p *fakePeer) { p.err = ErrArgosNotAllowed }, "NOT_ALLOWED"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newLinkFixture(t)
			tc.set(f.peer)
			if _, err := f.svc.RedeemLink(f.ctx, link); codeOfErr(err) != tc.want {
				t.Fatalf("%v (quiero %s)", err, tc.want)
			}
			if len(f.tokens.revoked) != 1 || f.tokens.revoked[0] != f.tokens.ids[0] || f.store.conn != nil {
				t.Fatal("el token emitido se revoca y no cambia nada")
			}
		})
	}
	f := newLinkFixture(t)
	for _, bad := range []string{"https://panel.argos.example/otra#" + code, "http://panel.argos.example/conectar#" + code, "no es un enlace"} {
		if _, err := f.svc.RedeemLink(f.ctx, bad); codeOfErr(err) != "INVALID_FORMAT" {
			t.Errorf("%s: %v", bad, err)
		}
	}
	f.peer.blocked = "10.0.0.5"
	if _, err := f.svc.RedeemLink(f.ctx, "https://10.0.0.5/conectar#"+code); codeOfErr(err) != "NOT_ALLOWED" {
		t.Fatalf("destino privado: %v", err)
	}
	if len(f.tokens.issued) != 0 {
		t.Fatal("un enlace inválido no emite tokens")
	}
}

const okResp = `{"token":"atk_de_prueba_1234","webhook":{"url":"https://api.argos.example/api/v1/integraciones/horus/webhook",` +
	`"secreto":"secreto-de-prueba-0002"},"conector_id":"c-1"}`

func (f *linkFixture) activeConnector(peerToken *uuid.UUID) {
	f.store.conn = &domain.ArgosConnector{ID: uuid.New(), TenantID: f.tenant, BaseURL: "https://api.argos.example/api/v1", Enabled: true,
		PeerAPITokenID: peerToken}
	ct, dek, _ := f.svc.sealer.Seal([]byte("atk_prueba_1234"), connAAD(f.store.conn, "token"))
	f.store.conn.TokenCiphertext, f.store.conn.TokenDEK = ct, dek
}

func ticketInput(tipo, id string) SSOTicketInput {
	var in SSOTicketInput
	in.Usuario = &struct {
		IDExterno string  `json:"id_externo"`
		Correo    *string `json:"correo"`
		Nombre    *string `json:"nombre"`
	}{IDExterno: "u-17", Correo: ptr("ana@isp.gt")}
	in.Destino = &struct {
		Tipo      string `json:"tipo"`
		IDExterno string `json:"id_externo"`
	}{Tipo: tipo, IDExterno: id}
	return in
}

func TestIssueSSOTicketOnlyAPITokenOfConnector(t *testing.T) {
	t.Parallel()
	f := newLinkFixture(t)
	linked := uuid.New()
	// Sin conector activo → 409.
	if _, err := f.svc.IssueSSOTicket(f.apiToken(linked), ticketInput("servicio", "812")); codeOfErr(err) != CodeIntegrationNotConfig {
		t.Fatalf("sin conector: %v", err)
	}
	f.activeConnector(&linked)
	// Un token de persona (navegador) no emite tickets.
	if _, err := f.svc.IssueSSOTicket(f.as("sso.issue"), ticketInput("servicio", "812")); codeOfErr(err) != problem.CodePermissionDenied {
		t.Fatalf("token de persona: %v", err)
	}
	// Otro API token del ISP (no el del canje) → 403.
	if _, err := f.svc.IssueSSOTicket(f.apiToken(uuid.New()), ticketInput("servicio", "812")); codeOfErr(err) != problem.CodePermissionDenied {
		t.Fatalf("otro token: %v", err)
	}
	if _, err := f.svc.IssueSSOTicket(f.apiToken(linked), ticketInput("factura", "1")); codeOfErr(err) != "INVALID_VALUE" {
		t.Fatalf("destino desconocido: %v", err)
	}
	res, err := f.svc.IssueSSOTicket(f.apiToken(linked), ticketInput("servicio", "812"))
	if err != nil {
		t.Fatal(err)
	}
	if res.URL != "https://horus.example/sso/entrar#ticket-de-prueba-0123456789" {
		t.Fatalf("url = %s", res.URL)
	}
	got := f.tickets.got[0]
	if got.TenantID != f.tenant || got.ConnectorID != f.store.conn.ID || got.APITokenID != linked || got.TargetID != "812" ||
		got.ExternalUserID != "u-17" {
		t.Fatalf("ticket = %+v", got)
	}
	// Conector apagado: no se emiten tickets.
	f.store.conn.Enabled = false
	if _, err := f.svc.IssueSSOTicket(f.apiToken(linked), ticketInput("servicio", "812")); codeOfErr(err) != CodeIntegrationNotConfig {
		t.Fatalf("conector apagado: %v", err)
	}
	if ok, _ := f.svc.SSOConnectorActive(context.Background(), f.tenant, f.store.conn.ID); ok {
		t.Fatal("conector apagado no canjea")
	}
}

func TestResolveSSOTargetAppliesACL(t *testing.T) {
	t.Parallel()
	f := newLinkFixture(t)
	siteA, siteB := uuid.New(), uuid.New()
	mk := func(site uuid.UUID, seen time.Time, svc int64) *domain.Customer {
		c := &domain.Customer{ID: uuid.New(), TenantID: f.tenant, SiteID: site, Address: netip.MustParsePrefix("100.64.0.1/32"), LastSeen: seen}
		f.store.customers[c.ID] = c
		f.store.idents = append(f.store.idents, domain.ExternalIdentity{CustomerID: c.ID, ExternalServiceID: &svc})
		return c
	}
	older := mk(siteA, f.now.Add(-time.Hour), 812)
	newer := mk(siteB, *f.now, 812)
	all := f.as("customers.read")
	got, err := f.svc.ResolveSSOTarget(all, "servicio", "812")
	if err != nil || got.Kind != "customer" || got.ID != newer.ID {
		t.Fatalf("servicio: %+v %v", got, err)
	}
	// Alcance solo del nodo A: ve el del nodo A, nunca el de B.
	scoped := authz.WithPrincipal(context.Background(), &authz.Principal{Type: authz.TypeUser, Scope: authz.ScopeTenant, TenantID: f.tenant,
		Perms: map[string][]string{"customers.read": {"site:" + siteA.String()}}})
	if got, err := f.svc.ResolveSSOTarget(scoped, "servicio", "812"); err != nil || got.ID != older.ID {
		t.Fatalf("con alcance: %+v %v", got, err)
	}
	onlyB := authz.WithPrincipal(context.Background(), &authz.Principal{Type: authz.TypeUser, Scope: authz.ScopeTenant, TenantID: f.tenant,
		Perms: map[string][]string{"customers.read": {"site:" + uuid.NewString()}}})
	if _, err := f.svc.ResolveSSOTarget(onlyB, "servicio", "812"); codeOfErr(err) != domain.CodeCustomerNotFound {
		t.Fatalf("fuera de alcance: %v", err)
	}
	// Sin el permiso del recurso → 403 (el ticket no da permisos).
	if _, err := f.svc.ResolveSSOTarget(f.as("devices.read"), "servicio", "812"); codeOfErr(err) != problem.CodePermissionDenied {
		t.Fatalf("sin customers.read: %v", err)
	}
	if _, err := f.svc.ResolveSSOTarget(all, "servicio", "999"); codeOfErr(err) != domain.CodeCustomerNotFound {
		t.Fatalf("servicio sin identidad: %v", err)
	}
	fid := uuid.New()
	if got, err := f.svc.ResolveSSOTarget(f.as("security.findings.read"), "hallazgo", fid.String()); err != nil || got.ID != fid {
		t.Fatalf("hallazgo: %+v %v", got, err)
	}
	if _, err := f.svc.ResolveSSOTarget(all, "hallazgo", fid.String()); codeOfErr(err) != problem.CodePermissionDenied {
		t.Fatalf("hallazgo sin permiso: %v", err)
	}
}

func TestOpenArgosValidatesReturnedURL(t *testing.T) {
	t.Parallel()
	f := newLinkFixture(t)
	c := &domain.Customer{ID: uuid.New(), TenantID: f.tenant, SiteID: uuid.New(), Address: netip.MustParsePrefix("100.64.0.9/32")}
	f.store.customers[c.ID] = c
	svc := int64(812)
	// Conector apagado → 409.
	if _, err := f.svc.OpenArgos(f.ctx, OpenArgosInput{CustomerID: c.ID}); codeOfErr(err) != CodeIntegrationNotConfig {
		t.Fatalf("sin conector: %v", err)
	}
	f.activeConnector(nil)
	if _, err := f.svc.OpenArgos(f.ctx, OpenArgosInput{CustomerID: c.ID}); codeOfErr(err) != CodeExternalIdentityNotFound {
		t.Fatalf("sin identidad: %v", err)
	}
	f.store.idents = append(f.store.idents, domain.ExternalIdentity{CustomerID: c.ID, ExternalServiceID: &svc})
	ticket := strings.Repeat("t", 43)
	f.peer.status, f.peer.resp = 201, `{"url":"https://panel.argos.example/sso/entrar#`+ticket+`","expira_en":"2026-10-17T12:01:00Z"}`
	res, err := f.svc.OpenArgos(f.ctx, OpenArgosInput{CustomerID: c.ID})
	if err != nil || res.URL != "https://panel.argos.example/sso/entrar#"+ticket {
		t.Fatalf("abrir: %+v %v", res, err)
	}
	req := f.peer.ticketReq[0]
	usr, _ := req["usuario"].(map[string]any)
	dst, _ := req["destino"].(map[string]any)
	if req["_token"] != "atk_prueba_1234" || usr["id_externo"] != f.user.String() || dst["tipo"] != "servicio" || dst["id_externo"] != "812" {
		t.Fatalf("petición = %v", req)
	}
	for _, bad := range []string{
		`{"url":"https://panel.argos.example/sso/entrar?ticket=` + ticket + `"}`,
		`{"url":"javascript:alert(1)"}`,
		`{"url":"https://evil.example/otra#` + ticket + `"}`,
	} {
		f.peer.resp = bad
		if _, err := f.svc.OpenArgos(f.ctx, OpenArgosInput{CustomerID: c.ID}); codeOfErr(err) != CodeIntegrationUnavailble {
			t.Errorf("%s: %v", bad, err)
		}
	}
	f.peer.status = 403
	if _, err := f.svc.OpenArgos(f.ctx, OpenArgosInput{CustomerID: c.ID}); codeOfErr(err) != CodeIntegrationUnavailble {
		t.Fatalf("token sin sso.emitir: %v", err)
	}
}
