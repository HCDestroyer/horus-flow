//go:build integration

package tenancy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // TOTP RFC 6238
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/services/auth"
	"github.com/hcdestroyer/horus-flow/services/devices"
	"github.com/hcdestroyer/horus-flow/services/gateway"
	"github.com/hcdestroyer/horus-flow/services/wgagent"
	"github.com/hcdestroyer/horus-flow/services/wireguard"
)

const (
	seedEmail    = "root@horus.test"
	seedPassword = "semilla-inicial-123"
	userPassword = "una-clave-bastante-larga"
)

// app es el binario horus en proceso con los roles auth, devices y gateway
// (el mismo cableado que services/cmd/horus) sobre una base vacía.
type app struct {
	t     *testing.T
	srv   *httptest.Server
	admin *pgxpool.Pool
	mux   *httpx.Mux
}

func startApp(t *testing.T) *app {
	t.Helper()
	dsn := pgtest.New(t)
	environ := []string{
		"HORUS_POSTGRES_DSN=" + dsn,
		"HORUS_AUTH_ARGON2_MEMORY_KIB=1024", "HORUS_AUTH_ARGON2_TIME=1",
		"HORUS_SEED_ADMIN_EMAIL=" + seedEmail, "HORUS_SEED_ADMIN_PASSWORD=" + seedPassword,
		// wireguard + wg-agent en proceso con la interfaz en memoria (sin NET_ADMIN).
		"HORUS_WGAGENT_DRIVER=memory", "HORUS_WG_ENDPOINT=horus.test",
		// Reconciliación periódica desactivada en la práctica: el túnel se asigna
		// al pedir el script (síncrono) y las versiones de los routers no cambian solas.
		"HORUS_WIREGUARD_RECONCILE_EVERY=1h", "HORUS_WGAGENT_REPORT_EVERY=200ms",
	}
	ctx, cancel := context.WithCancel(context.Background())
	mux := httpx.NewMux(nil, nil)
	services := module.NewServices()
	hreg := health.NewRegistry(health.Options{Service: "test"})
	var mods []module.Module
	for _, r := range []struct {
		name string
		f    module.Factory
	}{{auth.Role, auth.Register}, {devices.Role, devices.Register}, {wireguard.Role, wireguard.Register},
		{wgagent.Role, wgagent.Register}, {gateway.Role, gateway.Register}} {
		m, err := r.f(ctx, module.Deps{Role: r.name, Health: hreg.Role(r.name), Routes: mux.ForService(r.name),
			Common: config.Common{Env: "dev"}, Environ: environ, Services: services})
		if err != nil {
			t.Fatalf("register %s: %v", r.name, err)
		}
		if s, ok := m.(module.Starter); ok {
			if err := s.Start(ctx); err != nil {
				t.Fatalf("start %s: %v", r.name, err)
			}
		}
		mods = append(mods, m)
	}
	var wg sync.WaitGroup
	for _, m := range mods {
		wg.Add(1)
		go func() { defer wg.Done(); _ = m.Run(ctx) }()
	}
	srv := httptest.NewServer(mux.Handler())
	admin, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		srv.Close()
		cancel()
		wg.Wait()
		for _, m := range mods {
			if s, ok := m.(module.Stopper); ok {
				_ = s.Stop(context.Background())
			}
		}
		admin.Close()
	})
	return &app{t: t, srv: srv, admin: admin, mux: mux}
}

// resp es una respuesta decodificada.
type resp struct {
	Status int
	Body   map[string]any
	Raw    []byte
	Header http.Header
}

func (r resp) code() string { s, _ := r.Body["code"].(string); return s }
func (r resp) str(k string) string {
	s, _ := r.Body[k].(string)
	return s
}

// req es una petición de test.
type req struct {
	Method, Path, Token string
	Body                any
	Header              map[string]string
	Cookie              string // valor de __Secure-hf_rt
}

func (a *app) do(r req) resp {
	a.t.Helper()
	var body io.Reader
	if r.Body != nil {
		if s, ok := r.Body.(string); ok {
			body = strings.NewReader(s)
		} else {
			b, _ := json.Marshal(r.Body)
			body = bytes.NewReader(b)
		}
	}
	hr, err := http.NewRequestWithContext(context.Background(), r.Method, a.srv.URL+r.Path, body)
	if err != nil {
		a.t.Fatal(err)
	}
	if r.Body != nil {
		hr.Header.Set("Content-Type", "application/json")
	}
	if r.Token != "" {
		hr.Header.Set("Authorization", "Bearer "+r.Token)
	}
	if r.Cookie != "" {
		hr.Header.Set("Cookie", "__Secure-hf_rt="+r.Cookie)
		hr.Header.Set("X-Requested-With", "horus")
	}
	for k, v := range r.Header {
		hr.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(hr)
	if err != nil {
		a.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := resp{Status: res.StatusCode, Raw: raw, Header: res.Header, Body: map[string]any{}}
	_ = json.Unmarshal(raw, &out.Body)
	return out
}

// refreshCookie extrae el refresh de Set-Cookie.
func refreshCookie(r resp) string {
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if c.Name == "__Secure-hf_rt" {
			return c.Value
		}
	}
	return ""
}

func (a *app) must(r resp, status int) resp {
	a.t.Helper()
	if r.Status != status {
		a.t.Fatalf("status %d, quiero %d: %s", r.Status, status, r.Raw)
	}
	return r
}

// totpCode calcula el código TOTP actual de un secreto base32 (RFC 6238).
func totpCode(secret string, at time.Time) string {
	key, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(at.Unix()/30))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff)%1_000_000)
}

// session es un usuario con sesión iniciada.
type session struct {
	a       *app
	email   string
	token   string // token de sesión
	refresh string
	secret  string // TOTP
}

func (a *app) login(email, password string) *session {
	a.t.Helper()
	r := a.must(a.do(req{Method: "POST", Path: "/api/v1/auth/login", Body: map[string]string{"username": email, "password": password}}), 200)
	if r.Body["mfa_required"] == true {
		a.t.Fatalf("login de %s pide 2FA: use loginMFA", email)
	}
	return &session{a: a, email: email, token: r.str("access_token"), refresh: refreshCookie(r)}
}

// enrollTOTP activa TOTP y deja la sesión verificada con 2FA.
func (s *session) enrollTOTP() {
	s.a.t.Helper()
	r := s.a.must(s.a.do(req{Method: "POST", Path: "/api/v1/me/totp/enroll", Token: s.token}), 200)
	s.secret = r.str("secret")
	s.a.must(s.a.do(req{Method: "POST", Path: "/api/v1/me/totp/confirm", Token: s.token,
		Body: map[string]string{"code": totpCode(s.secret, time.Now())}}), 200)
}

func (s *session) tenantToken(tenant string) string {
	s.a.t.Helper()
	r := s.a.must(s.a.do(req{Method: "POST", Path: "/api/v1/auth/token", Token: s.token, Body: map[string]string{"tenant_id": tenant}}), 200)
	return r.str("access_token")
}

func (s *session) platformToken() string {
	s.a.t.Helper()
	r := s.a.must(s.a.do(req{Method: "POST", Path: "/api/v1/auth/token", Token: s.token, Body: map[string]string{"scope": "platform"}}), 200)
	return r.str("access_token")
}

// superadmin hace el primer login del superadministrador semilla: cambio de
// contraseña obligatorio y alta de TOTP.
func (a *app) superadmin() *session {
	a.t.Helper()
	s := a.login(seedEmail, seedPassword)
	a.must(a.do(req{Method: "POST", Path: "/api/v1/me/password", Token: s.token,
		Body: map[string]string{"current_password": seedPassword, "new_password": "otra-clave-del-sistema-1"}}), 204)
	s.enrollTOTP()
	return s
}

// createUser inserta un usuario activo con contraseña userPassword.
func (a *app) createUser(email string) uuid.UUID {
	a.t.Helper()
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key := argon2.IDKey([]byte(userPassword), salt, 1, 1024, 1, 32)
	phc := fmt.Sprintf("$argon2id$v=19$m=1024,t=1,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
	id := uuid.Must(uuid.NewV7())
	if _, err := a.admin.Exec(context.Background(), `INSERT INTO auth."user" (id, email, display_name, password_hash, status) VALUES ($1, $2, $3, $4, 'active')`,
		id, email, strings.Split(email, "@")[0], phc); err != nil {
		a.t.Fatal(err)
	}
	return id
}

// grant da a un usuario un rol de sistema en un tenant (membresía activa).
func (a *app) grant(userID uuid.UUID, tenant, roleKey string) {
	a.t.Helper()
	ctx := context.Background()
	var mID uuid.UUID
	err := a.admin.QueryRow(ctx, `INSERT INTO auth.tenant_membership (id, tenant_id, user_id, status) VALUES ($1, $2, $3, 'active')
		ON CONFLICT (tenant_id, user_id) DO UPDATE SET status = 'active' RETURNING id`, uuid.Must(uuid.NewV7()), tenant, userID).Scan(&mID)
	if err != nil {
		a.t.Fatal(err)
	}
	if _, err := a.admin.Exec(ctx, `INSERT INTO auth.role_assignment (id, tenant_id, membership_id, role_id, scope_type)
		SELECT $1, $2, $3, id, 'tenant' FROM auth.role WHERE key = $4 AND tenant_id IS NULL`, uuid.Must(uuid.NewV7()), tenant, mID, roleKey); err != nil {
		a.t.Fatal(err)
	}
}

// createTenant da de alta un ISP como superadministrador.
func (a *app) createTenant(platformToken, slug, adminEmail string) string {
	a.t.Helper()
	r := a.must(a.do(req{Method: "POST", Path: "/api/v1/platform/tenants", Token: platformToken,
		Header: map[string]string{"Idempotency-Key": uuid.NewString()},
		Body:   map[string]any{"slug": slug, "name": "ISP " + slug, "country": "MX", "timezone": "America/Mexico_City", "initial_admin_email": adminEmail}}), 201)
	return r.str("id")
}

func (a *app) auditCount(action string) int {
	a.t.Helper()
	var n int
	if err := a.admin.QueryRow(context.Background(), `SELECT count(*) FROM auth.audit_log WHERE action = $1`, action).Scan(&n); err != nil {
		a.t.Fatal(err)
	}
	return n
}
