package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// memAccountStore añade a memMemberStore lo que usan los casos de uso de
// cuentas: identidad, pertenencia, baja y enlaces de restablecimiento.
type memAccountStore struct {
	*memMemberStore
	shared     bool
	emailTaken bool
	identity   *UserIdentityChange
	deleted    *UserDelete
	deleteErr  error
	resets     map[string]*domain.PasswordReset // hash → enlace
	created    []NewPasswordReset
	consumed   *ConsumedPasswordReset
	events     []OutboxEvent
	byEmail    map[string]*domain.User
	tokens     map[string]string
}

func (m *memAccountStore) UserSharedOutside(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return m.shared, nil
}

func (m *memAccountStore) UpdateUserIdentity(_ context.Context, in UserIdentityChange) (*domain.User, error) {
	if m.emailTaken {
		return nil, ErrEmailTaken
	}
	m.identity = &in
	u := *m.user
	if in.Email != nil {
		u.Email = *in.Email
	}
	if in.DisplayName != nil {
		u.DisplayName = *in.DisplayName
	}
	u.Version++
	if in.Event != nil {
		m.events = append(m.events, in.Event(u, identityFields(in.DisplayName, in.Email)))
	}
	m.member.Email, m.member.DisplayName = u.Email, u.DisplayName
	return &u, nil
}

func (m *memAccountStore) DeleteUser(_ context.Context, in UserDelete) (*UserDeleteResult, error) {
	if m.deleteErr != nil {
		return nil, m.deleteErr
	}
	m.deleted = &in
	tenants := []uuid.UUID{uuid.New()}
	if in.Event != nil {
		m.events = append(m.events, in.Event(*m.user, tenants))
	}
	return &UserDeleteResult{Email: m.user.Email, Revoked: []uuid.UUID{uuid.New()}, Tenants: tenants}, nil
}

func (m *memAccountStore) CreatePasswordReset(_ context.Context, in NewPasswordReset) error {
	m.created = append(m.created, in)
	m.resets[string(in.TokenHash)] = &domain.PasswordReset{ID: in.ID, UserID: in.UserID, ExpiresAt: in.ExpiresAt, UserStatus: m.user.Status,
		UserEmail: m.user.Email, UserDisplayName: m.user.DisplayName}
	return nil
}

func (m *memAccountStore) PasswordResetByHash(_ context.Context, hash []byte) (*domain.PasswordReset, error) {
	r, ok := m.resets[string(hash)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return r, nil
}

func (m *memAccountStore) ConsumePasswordReset(_ context.Context, in ConsumedPasswordReset) (*PasswordResetResult, error) {
	r := m.resets[string(domain.HashSecret(tokenOf(m, in.Reset.ID)))]
	if r == nil || r.UsedAt != nil {
		return nil, ErrResetUsed
	}
	now := in.Now
	r.UsedAt = &now
	m.consumed = &in
	m.user.PasswordHash = in.PasswordHash
	return &PasswordResetResult{Revoked: []uuid.UUID{uuid.New(), uuid.New()}}, nil
}

func (m *memAccountStore) UserByEmail(_ context.Context, email string) (*domain.User, error) {
	if u, ok := m.byEmail[strings.ToLower(email)]; ok {
		return u, nil
	}
	return nil, domain.ErrNotFound
}

func (m *memAccountStore) PlatformUser(_ context.Context, id uuid.UUID) (*domain.PlatformUser, error) {
	if id != m.user.ID {
		return nil, domain.ErrNotFound
	}
	return &domain.PlatformUser{ID: m.user.ID, Email: m.user.Email, DisplayName: m.user.DisplayName, Status: m.user.Status}, nil
}

// tokenOf recupera el token en claro de un enlace creado (solo el test lo conoce).
func tokenOf(m *memAccountStore, id uuid.UUID) string {
	for hash, r := range m.resets {
		if r.ID == id {
			for tok, h := range m.tokens {
				if h == hash {
					return tok
				}
			}
		}
	}
	return ""
}

// resetSender registra los enlaces enviados. ForgotPassword envía en segundo
// plano: got se protege con mu y la prueba lee con deliveries().
type resetSender struct {
	mu  sync.Mutex
	got []PasswordResetDelivery
	err error
}

func (c *resetSender) SendPasswordReset(_ context.Context, d PasswordResetDelivery) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, d)
	return c.err
}

func (c *resetSender) deliveries() []PasswordResetDelivery {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]PasswordResetDelivery(nil), c.got...)
}

func newAccountEnv(t *testing.T, withMail bool) (*Service, *memAccountStore, *resetSender, *domain.Catalog) {
	t.Helper()
	cat, err := domain.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	uid := uuid.New()
	base := &memMemberStore{
		user:    &domain.User{ID: uid, Email: "ana@isp.test", DisplayName: "Ana Belén", Status: domain.UserActive, PasswordHash: "$argon2id$x", Version: 1},
		session: &domain.Session{ID: uuid.New(), UserID: uid, ExpiresAt: time.Now().Add(time.Hour)},
		member:  &domain.Member{MembershipID: uuid.New(), UserID: uid, Email: "ana@isp.test", DisplayName: "Ana Belén", Status: domain.MembershipActive, Version: 2},
	}
	for _, r := range cat.TenantRoles {
		base.roles = append(base.roles, domain.TenantRoleRecord{ID: r.ID, Key: r.Key, Name: r.Name, IsSystem: true, RequiresMFA: r.RequiresMFA, Permissions: r.Permissions})
	}
	st := &memAccountStore{memMemberStore: base, resets: map[string]*domain.PasswordReset{}, byEmail: map[string]*domain.User{"ana@isp.test": base.user}}
	snd := &resetSender{}
	o := Options{Catalog: cat, PublicBaseURL: "https://horus.example", Argon: domain.Argon2Params{Memory: 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}}
	if withMail {
		o.PasswordResets = snd
	}
	svc, err := NewService(st, o)
	if err != nil {
		t.Fatal(err)
	}
	return svc, st, snd, cat
}

func linkToken(t *testing.T, url string) string {
	t.Helper()
	const prefix = "/reset-password#token="
	i := strings.Index(url, prefix)
	if i < 0 {
		t.Fatalf("enlace = %q", url)
	}
	return url[i+len(prefix):]
}

// Un administrador del ISP emite un enlace para un miembro que solo
// pertenece a su ISP; la persona lo canjea una vez, con la política de
// contraseñas, y se revocan todas sus sesiones.
func TestResetMemberPasswordAndRedeem(t *testing.T) {
	svc, st, snd, cat := newAccountEnv(t, true)
	tenant := uuid.New()
	ctx := tenantCtx(tenant, allPerms(cat, "tenant_admin"))

	// Compartida con otro ISP: la plataforma.
	st.shared = true
	_ = wantErr(t, errOnly(svc.ResetMemberPassword(ctx, st.user.ID, "")), 409, CodeUserShared)
	st.shared = false
	// Invitada: reenviar la invitación.
	st.member.Status = domain.MembershipInvited
	_ = wantErr(t, errOnly(svc.ResetMemberPassword(ctx, st.user.ID, "")), 409, problem.CodeConflict)
	st.member.Status = domain.MembershipActive
	// Sin users.manage.
	_ = wantErr(t, errOnly(svc.ResetMemberPassword(tenantCtx(tenant, allPerms(cat, "viewer")), st.user.ID, "")), 403, problem.CodePermissionDenied)

	out, err := svc.ResetMemberPassword(ctx, st.user.ID, "203.0.113.9")
	if err != nil {
		t.Fatal(err)
	}
	tok := linkToken(t, out.URL)
	if len(tok) < 40 || !out.EmailSent || len(snd.got) != 1 || !snd.got[0].RequestedByAdmin || snd.got[0].Email != "ana@isp.test" ||
		out.ExpiresAt.Sub(svc.now()) > domain.PasswordResetTTL {
		t.Fatalf("emitido = %+v, correo = %+v", out, snd.got)
	}
	if len(st.created) != 1 || st.created[0].TenantID == nil || *st.created[0].TenantID != tenant || string(st.created[0].TokenHash) != string(domain.HashSecret(tok)) {
		t.Fatalf("guardado = %+v", st.created)
	}
	if len(st.audits) == 0 || st.audits[len(st.audits)-1].Action != "members.password_reset_issued" {
		t.Fatalf("auditoría = %+v", st.audits)
	}
	st.tokens = map[string]string{tok: string(domain.HashSecret(tok))}

	info, err := svc.InspectPasswordReset(context.Background(), tok)
	if err != nil || info.Email != "ana@isp.test" || info.DisplayName != "Ana Belén" {
		t.Fatalf("inspect = %+v, %v", info, err)
	}
	e := wantErr(t, svc.ResetPassword(context.Background(), tok, "corta", LoginInput{}), 422, problem.CodeValidationFailed)
	if !hasField(e, "new_password", domain.PwTooShort) {
		t.Fatalf("fields = %+v", e.Fields)
	}
	e = wantErr(t, svc.ResetPassword(context.Background(), tok, "soy ana belén y lo sé 2026", LoginInput{}), 422, problem.CodeValidationFailed)
	if !hasField(e, "new_password", domain.PwPersonal) {
		t.Fatalf("fields = %+v", e.Fields)
	}
	if err := svc.ResetPassword(context.Background(), tok, "una clave larga y nueva 2026", LoginInput{IP: "198.51.100.7"}); err != nil {
		t.Fatal(err)
	}
	if st.consumed == nil || !strings.HasPrefix(st.user.PasswordHash, "$argon2id$") || st.user.PasswordHash == "$argon2id$x" {
		t.Fatalf("no se guardó la contraseña: %+v", st.consumed)
	}
	// Segundo uso: USED; token desconocido: INVALID_TOKEN; caducado: EXPIRED.
	e = wantErr(t, svc.ResetPassword(context.Background(), tok, "otra clave larga y nueva 2026", LoginInput{}), 422, problem.CodeValidationFailed)
	if !hasField(e, "token", "USED") {
		t.Fatalf("fields = %+v", e.Fields)
	}
	e = wantErr(t, errOnly(svc.InspectPasswordReset(context.Background(), strings.Repeat("x", 43))), 422, problem.CodeValidationFailed)
	if !hasField(e, "token", "INVALID_TOKEN") {
		t.Fatalf("fields = %+v", e.Fields)
	}
	for _, r := range st.resets {
		r.UsedAt = nil
		r.ExpiresAt = svc.now().Add(-time.Second)
	}
	e = wantErr(t, errOnly(svc.InspectPasswordReset(context.Background(), tok)), 422, problem.CodeValidationFailed)
	if !hasField(e, "token", "EXPIRED") {
		t.Fatalf("fields = %+v", e.Fields)
	}
}

// "Olvidé mi contraseña": misma respuesta exista o no la cuenta; sin SMTP no
// se emite nada y la respuesta lo dice; límite por cuenta.
func TestForgotPassword(t *testing.T) {
	svc, st, _, _ := newAccountEnv(t, false)
	ok, err := svc.ForgotPassword(context.Background(), "ana@isp.test", LoginInput{})
	if err != nil || ok || len(st.created) != 0 {
		t.Fatalf("sin SMTP = %v, %v, creados %d", ok, err, len(st.created))
	}
	_ = wantErr(t, errOnly(svc.ForgotPassword(context.Background(), "no-es-email", LoginInput{})), 422, problem.CodeValidationFailed)

	svc, st, snd, _ := newAccountEnv(t, true)
	for _, email := range []string{"ana@isp.test", "nadie@isp.test"} {
		ok, err := svc.ForgotPassword(context.Background(), email, LoginInput{})
		if err != nil || !ok {
			t.Fatalf("%s = %v, %v", email, ok, err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(snd.deliveries()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := snd.deliveries(); len(st.created) != 1 || st.created[0].CreatedBy != uuid.Nil || len(got) != 1 || got[0].RequestedByAdmin {
		t.Fatalf("creados = %+v, correos = %+v", st.created, got)
	}
	// Cuenta bloqueada o pendiente: nada que emitir, misma respuesta.
	st.user.Status = domain.UserDisabled
	if ok, err := svc.ForgotPassword(context.Background(), "ana@isp.test", LoginInput{}); err != nil || !ok || len(st.created) != 1 {
		t.Fatalf("bloqueada = %v, %v, creados %d", ok, err, len(st.created))
	}
	st.user.Status = domain.UserActive
	for i := 0; i < 3; i++ {
		if _, err := svc.ForgotPassword(context.Background(), "ANA@isp.test", LoginInput{}); err != nil {
			t.Fatal(err)
		}
	}
	_ = wantErr(t, errOnly(svc.ForgotPassword(context.Background(), "ana@isp.test", LoginInput{})), 429, problem.CodeRateLimited)
}

// Nombre y correo de un miembro: solo cuentas exclusivas del ISP, correo
// único, evento user.updated y auditoría.
func TestUpdateMember(t *testing.T) {
	svc, st, _, cat := newAccountEnv(t, false)
	tenant := uuid.New()
	ctx := tenantCtx(tenant, allPerms(cat, "tenant_admin"))

	e := wantErr(t, errOnly(svc.UpdateMember(ctx, st.user.ID, rawPatch(t, `{"email":"mal","display_name":"","locale":"es"}`), "")), 422, problem.CodeValidationFailed)
	if !hasField(e, "email", "INVALID_EMAIL") || !hasField(e, "display_name", "INVALID_VALUE") || !hasField(e, "locale", "UNKNOWN_FIELD") {
		t.Fatalf("fields = %+v", e.Fields)
	}
	_ = wantErr(t, errOnly(svc.UpdateMember(ctx, st.user.ID, rawPatch(t, `{}`), "")), 422, problem.CodeValidationFailed)
	st.shared = true
	_ = wantErr(t, errOnly(svc.UpdateMember(ctx, st.user.ID, rawPatch(t, `{"display_name":"Ana María"}`), "")), 409, CodeUserShared)
	st.shared = false
	st.emailTaken = true
	_ = wantErr(t, errOnly(svc.UpdateMember(ctx, st.user.ID, rawPatch(t, `{"email":"otra@isp.test"}`), "")), 409, problem.CodeAlreadyExists)
	st.emailTaken = false

	m, err := svc.UpdateMember(ctx, st.user.ID, rawPatch(t, `{"email":" nueva@isp.test ","display_name":"Ana María"}`), "203.0.113.9")
	if err != nil || m.Email != "nueva@isp.test" || m.DisplayName != "Ana María" {
		t.Fatalf("miembro = %+v, %v", m, err)
	}
	if len(st.events) != 1 || st.events[0].Type != "horus.auth.user.updated" || st.events[0].Data["email"] != "nueva@isp.test" {
		t.Fatalf("eventos = %+v", st.events)
	}
	if a := st.audits[len(st.audits)-1]; a.Action != "members.updated" || a.TenantID != tenant {
		t.Fatalf("auditoría = %+v", a)
	}
}

// PATCH /me con correo: exige re-autenticación reciente y correo libre.
func TestUpdateMeEmail(t *testing.T) {
	svc, st, _, _ := newAccountEnv(t, false)
	old := authz.WithPrincipal(context.Background(), &authz.Principal{Type: authz.TypeUser, Subject: st.user.ID.String(), UserID: st.user.ID,
		SessionID: st.session.ID, Scope: authz.ScopeSession, AuthTime: time.Now().Add(-time.Hour)})
	_ = wantErr(t, errOnly(svc.UpdateMe(old, authz.FromContext(old), rawPatch(t, `{"email":"nueva@isp.test"}`), "")), 403, problem.CodeReauthRequired)
	// El mismo correo no exige nada.
	if _, err := svc.UpdateMe(old, authz.FromContext(old), rawPatch(t, `{"email":"ana@isp.test","display_name":"Ana B"}`), ""); err != nil {
		t.Fatal(err)
	}
	fresh := authz.WithPrincipal(context.Background(), &authz.Principal{Type: authz.TypeUser, Subject: st.user.ID.String(), UserID: st.user.ID,
		SessionID: st.session.ID, Scope: authz.ScopeSession, AuthTime: time.Now()})
	if _, err := svc.UpdateMe(fresh, authz.FromContext(fresh), rawPatch(t, `{"email":"nueva@isp.test"}`), ""); err != nil {
		t.Fatal(err)
	}
	if st.profile == nil || st.profile.Email != "nueva@isp.test" {
		t.Fatalf("perfil = %+v", st.profile)
	}
}

// Baja de una cuenta por la plataforma: nunca la propia; el repositorio
// decide el último administrador; evento user.deleted y auditoría.
func TestDeleteUser(t *testing.T) {
	svc, st, _, _ := newAccountEnv(t, false)
	root := uuid.New()
	ctx := platformCtx(root, domain.RolePlatformAdmin)
	e := wantErr(t, svc.DeleteUser(platformCtx(st.user.ID, domain.RolePlatformAdmin), st.user.ID, ""), 422, problem.CodeValidationFailed)
	if !hasField(e, "user_id", CodeCannotDeleteSelf) {
		t.Fatalf("fields = %+v", e.Fields)
	}
	_ = wantErr(t, svc.DeleteUser(ctx, uuid.New(), ""), 404, problem.CodeNotFound)
	_ = wantErr(t, svc.DeleteUser(platformCtx(root, "platform_auditor"), st.user.ID, ""), 403, problem.CodePermissionDenied)
	st.deleteErr = ErrLastPlatformAdmin
	_ = wantErr(t, svc.DeleteUser(ctx, st.user.ID, ""), 409, CodeLastPlatformAdmin)
	st.deleteErr = nil
	svc.cache["x"] = cachedStatus{}
	if err := svc.DeleteUser(ctx, st.user.ID, "203.0.113.9"); err != nil {
		t.Fatal(err)
	}
	if st.deleted == nil || len(svc.cache) != 0 || len(st.events) != 1 || st.events[0].Type != "horus.auth.user.deleted" {
		t.Fatalf("baja = %+v, eventos = %+v", st.deleted, st.events)
	}
	if a := st.audits[len(st.audits)-1]; a.Action != "platform.user.deleted" || a.Scope != "platform" {
		t.Fatalf("auditoría = %+v", a)
	}
}

// Plataforma: correo y nombre de cualquier cuenta; enlace de
// restablecimiento salvo pendiente o bloqueada.
func TestPlatformUserIdentityAndReset(t *testing.T) {
	svc, st, snd, _ := newAccountEnv(t, true)
	ctx := platformCtx(uuid.New(), domain.RolePlatformAdmin)
	e := wantErr(t, errOnly(svc.UpdatePlatformUser(ctx, st.user.ID, rawPatch(t, `{"status":"locked","email":"x"}`), "")), 422, problem.CodeValidationFailed)
	if !hasField(e, "email", "INVALID_EMAIL") {
		t.Fatalf("fields = %+v", e.Fields)
	}
	_ = wantErr(t, errOnly(svc.UpdatePlatformUser(ctx, st.user.ID, rawPatch(t, `{}`), "")), 422, problem.CodeValidationFailed)
	if _, err := svc.UpdatePlatformUser(ctx, st.user.ID, rawPatch(t, `{"email":"ana.b@isp.test","display_name":"Ana B"}`), ""); err != nil {
		t.Fatal(err)
	}
	if st.identity == nil || *st.identity.Email != "ana.b@isp.test" || st.audits[len(st.audits)-1].Action != "platform.user.updated" {
		t.Fatalf("identidad = %+v", st.identity)
	}

	st.user.Status = domain.UserPending
	_ = wantErr(t, errOnly(svc.ResetUserPassword(ctx, st.user.ID, "")), 409, problem.CodeConflict)
	st.user.Status = domain.UserDisabled
	_ = wantErr(t, errOnly(svc.ResetUserPassword(ctx, st.user.ID, "")), 409, problem.CodeConflict)
	st.user.Status = domain.UserActive
	snd.err = context.DeadlineExceeded
	out, err := svc.ResetUserPassword(ctx, st.user.ID, "")
	if err != nil || out.EmailSent || len(st.created) != 1 || st.created[0].TenantID != nil {
		t.Fatalf("emitido = %+v, %v", out, err)
	}
	if st.audits[len(st.audits)-1].Action != "platform.user.password_reset_issued" {
		t.Fatalf("auditoría = %+v", st.audits[len(st.audits)-1])
	}
}

func errOnly[T any](_ T, err error) error { return err }
