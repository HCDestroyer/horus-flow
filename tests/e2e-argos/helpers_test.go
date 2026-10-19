//go:build e2e

// Package e2eargos es la prueba de extremo a extremo Horus Flow ↔ ArgosISP
// (contrato docs/integrations/argosisp.md) con los DOS sistemas corriendo de
// verdad: el binario horus y la API, workers y aaa de ArgosISP. La levanta
// run.sh (make e2e-argos); ver README.md.
package e2eargos

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // TOTP RFC 6238
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"
)

// cfg son las direcciones de los dos sistemas (las pone run.sh).
type cfg struct {
	HorusURL, ArgosURL         string // orígenes de las API (sin /api/v1)
	HorusPG, ArgosPG           string // DSN de superusuario (siembra y verificación)
	CHURL, CHUser, CHPass      string // ClickHouse HTTP
	NATSURL                    string
	Ctl                        string // `run.sh ctl <stop|start> <horus|argos>`
	AAAURL                     string // servicio aaa de Argos (accounting RADIUS como rlm_rest)
	ArgosAdmin, ArgosAdminPass string
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func loadCfg(t *testing.T) cfg {
	c := cfg{
		HorusURL: env("E2E_HORUS_URL", ""), ArgosURL: env("E2E_ARGOS_URL", ""),
		HorusPG: env("E2E_HORUS_PG", ""), ArgosPG: env("E2E_ARGOS_PG", ""),
		CHURL: env("E2E_CH_URL", ""), CHUser: env("E2E_CH_USER", "horus"), CHPass: env("E2E_CH_PASSWORD", ""),
		NATSURL: env("E2E_NATS_URL", ""), Ctl: env("E2E_CTL", ""), AAAURL: env("E2E_AAA_URL", ""),
		ArgosAdmin: env("E2E_ARGOS_ADMIN", "admin@argosisp.local"), ArgosAdminPass: env("E2E_ARGOS_ADMIN_PASSWORD", "admin12345"),
	}
	if c.HorusURL == "" || c.ArgosURL == "" || c.HorusPG == "" || c.ArgosPG == "" {
		t.Skip("E2E_HORUS_URL, E2E_ARGOS_URL, E2E_HORUS_PG y E2E_ARGOS_PG no definidas: use `make e2e-argos`")
	}
	return c
}

// ---------------------------------------------------------------- resumen

type result struct {
	ID, Name, Status string
	Took             time.Duration
}

var (
	resMu   sync.Mutex
	results []result
)

func record(id, name, status string, took time.Duration) {
	resMu.Lock()
	defer resMu.Unlock()
	results = append(results, result{id, name, status, took})
}

func summary() string {
	resMu.Lock()
	defer resMu.Unlock()
	var b strings.Builder
	pass := 0
	b.WriteString("\n=== Resumen E2E Horus Flow ↔ ArgosISP ===\n")
	for _, r := range results {
		if r.Status == "PASS" {
			pass++
		}
		fmt.Fprintf(&b, "%-4s %-5s %6.1fs  %s\n", r.ID, r.Status, r.Took.Seconds(), r.Name)
	}
	fmt.Fprintf(&b, "Total: %d escenarios, %d PASS, %d FAIL\n", len(results), pass, len(results)-pass)
	return b.String()
}

// scenario corre un escenario como subtest y lo anota en el resumen.
func scenario(t *testing.T, id, name string, f func(t *testing.T)) bool {
	t.Helper()
	start := time.Now()
	ok := t.Run(id+"_"+strings.ReplaceAll(name, " ", "_"), f)
	st := "PASS"
	if !ok {
		st = "FAIL"
	}
	record(id, name, st, time.Since(start))
	return ok
}

// ---------------------------------------------------------------- HTTP

type resp struct {
	Status int
	Body   map[string]any
	Raw    []byte
	Header http.Header
}

func (r resp) str(k string) string { s, _ := r.Body[k].(string); return s }
func (r resp) obj(k string) map[string]any {
	m, _ := r.Body[k].(map[string]any)
	return m
}

type rq struct {
	Method, URL, Token string
	Body               any
	Header             map[string]string
	RawBody            []byte
}

var httpc = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func do(t *testing.T, r rq) resp {
	t.Helper()
	var body io.Reader
	if r.RawBody != nil {
		body = bytes.NewReader(r.RawBody)
	} else if r.Body != nil {
		b, _ := json.Marshal(r.Body)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), r.Method, r.URL, body)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	for k, v := range r.Header {
		req.Header.Set(k, v)
	}
	res, err := httpc.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", r.Method, r.URL, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := resp{Status: res.StatusCode, Raw: raw, Header: res.Header, Body: map[string]any{}}
	_ = json.Unmarshal(raw, &out.Body)
	return out
}

func must(t *testing.T, r resp, status int) resp {
	t.Helper()
	if r.Status != status {
		t.Fatalf("HTTP %d, se esperaba %d: %s", r.Status, status, trunc(r.Raw))
	}
	return r
}

func trunc(b []byte) string {
	if len(b) > 1200 {
		return string(b[:1200]) + "…"
	}
	return string(b)
}

// get recorre un JSON por claves ("a.b.0.c").
func get(v any, path string) any {
	for _, k := range strings.Split(path, ".") {
		switch x := v.(type) {
		case map[string]any:
			v = x[k]
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(x) {
				return nil
			}
			v = x[i]
		default:
			return nil
		}
	}
	return v
}

func gs(v any, path string) string {
	switch x := get(v, path).(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%s = %v, se esperaba %v", what, got, want)
	}
}

// waitFor reintenta cond hasta timeout.
func waitFor(t *testing.T, what string, timeout time.Duration, cond func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := ""
	for time.Now().Before(deadline) {
		ok, detail := cond()
		if ok {
			return
		}
		last = detail
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("esperando %s (%s): %s", what, timeout, last)
}

// ---------------------------------------------------------------- bases

func pool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func q1(t *testing.T, p *pgxpool.Pool, sql string, args ...any) any {
	t.Helper()
	var v any
	if err := p.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return v
}

func exec1(t *testing.T, p *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := p.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func chExec(t *testing.T, c cfg, sql string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, c.CHURL+"/", strings.NewReader(sql))
	req.SetBasicAuth(c.CHUser, c.CHPass)
	res, err := httpc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("clickhouse: %d %s", res.StatusCode, b)
	}
}

// ctl para o arranca un proceso de la pila (degradación).
func ctl(t *testing.T, c cfg, action, what string) {
	t.Helper()
	if c.Ctl == "" {
		t.Skip("E2E_CTL no definida")
	}
	out, err := exec.Command("bash", c.Ctl, "ctl", action, what).CombinedOutput() //nolint:gosec // script propio
	if err != nil {
		t.Fatalf("ctl %s %s: %v\n%s", action, what, err, out)
	}
}

// ---------------------------------------------------------------- firma §1.3

func sign(secret string, ts int64, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(ts, 10) + "."))
	m.Write(body)
	return "v1=" + hex.EncodeToString(m.Sum(nil))
}

func signedHeaders(secret, id string, at time.Time, body []byte) map[string]string {
	return map[string]string{"X-Webhook-Id": id, "X-Webhook-Timestamp": strconv.FormatInt(at.Unix(), 10),
		"X-Webhook-Signature": sign(secret, at.Unix(), body)}
}

// ---------------------------------------------------------------- Horus

// horusUserPassword se genera en cada corrida (nada fijo en el repositorio).
var horusUserPassword = "e2e-" + rand.Text()

// totp calcula códigos TOTP sin repetir paso (Horus rechaza un paso ya usado).
type totp struct {
	secret string
	last   int64
}

func totpAt(secret string, step int64) string {
	key, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // paso positivo
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff)%1_000_000)
}

func (o *totp) code() string {
	for {
		cur := time.Now().Unix() / 30
		for s := cur - 1; s <= cur+1; s++ {
			if s > o.last {
				o.last = s
				return totpAt(o.secret, s)
			}
		}
		time.Sleep(time.Until(time.Unix((cur+1)*30, 0)) + 200*time.Millisecond)
	}
}

type horus struct {
	t  *testing.T
	c  cfg
	db *pgxpool.Pool
}

func (h *horus) url(p string) string { return h.c.HorusURL + "/api/v1" + p }

func (h *horus) do(method, path, token string, body any, hdr ...map[string]string) resp {
	h.t.Helper()
	r := rq{Method: method, URL: h.url(path), Token: token, Body: body}
	if len(hdr) > 0 {
		r.Header = hdr[0]
	}
	return do(h.t, r)
}

// hsession es un usuario con sesión en Horus.
type hsession struct {
	h        *horus
	email    string
	password string
	session  string
	tenant   string
	ttoken   string
	issuedAt time.Time
	otp      *totp
}

func (h *horus) login(email, password string) *hsession {
	h.t.Helper()
	r := must(h.t, h.do("POST", "/auth/login", "", map[string]string{"username": email, "password": password}), 200)
	s := &hsession{h: h, email: email, password: password}
	if r.Body["mfa_required"] == true {
		h.t.Fatalf("login de %s pide 2FA", email)
	}
	s.session = r.str("access_token")
	return s
}

// loginMFA inicia sesión con segundo factor.
func (s *hsession) relogin() {
	h := s.h
	h.t.Helper()
	r := must(h.t, h.do("POST", "/auth/login", "", map[string]string{"username": s.email, "password": s.password}), 200)
	if r.Body["mfa_required"] == true {
		r = must(h.t, h.do("POST", "/auth/mfa/verify", "", map[string]string{"mfa_token": r.str("mfa_token"), "code": s.otp.code()}), 200)
	}
	s.session = r.str("access_token")
	if s.tenant != "" {
		s.useTenant(s.tenant)
	}
}

func (s *hsession) enrollTOTP() {
	h := s.h
	h.t.Helper()
	r := must(h.t, h.do("POST", "/me/totp/enroll", s.session, nil), 200)
	s.otp = &totp{secret: r.str("secret")}
	must(h.t, h.do("POST", "/me/totp/confirm", s.session, map[string]string{"code": s.otp.code()}), 200)
}

func (s *hsession) useTenant(id string) {
	h := s.h
	h.t.Helper()
	r := must(h.t, h.do("POST", "/auth/token", s.session, map[string]string{"tenant_id": id}), 200)
	s.tenant, s.ttoken, s.issuedAt = id, r.str("access_token"), time.Now()
}

// tok devuelve un token del ISP con auth_time reciente (re-autenticación
// cada 4 min: las rutas 🔒 piden ≤ 5 min y el token dura 10).
func (s *hsession) tok() string {
	h := s.h
	h.t.Helper()
	if time.Since(s.issuedAt) > 4*time.Minute {
		body := map[string]any{"password": s.password}
		if s.otp != nil {
			body["code"] = s.otp.code()
		}
		r := h.do("POST", "/auth/reauth", s.ttoken, body)
		if r.Status != 200 {
			s.relogin()
			r = must(h.t, h.do("POST", "/auth/reauth", s.ttoken, map[string]any{"password": s.password, "code": s.otp.code()}), 200)
		}
		s.ttoken, s.issuedAt = r.str("access_token"), time.Now()
	}
	return s.ttoken
}

func (h *horus) createUser(email string) uuid.UUID {
	h.t.Helper()
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key := argon2.IDKey([]byte(horusUserPassword), salt, 1, 1024, 1, 32)
	phc := fmt.Sprintf("$argon2id$v=19$m=1024,t=1,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
	id := uuid.Must(uuid.NewV7())
	exec1(h.t, h.db, `INSERT INTO auth."user" (id, email, display_name, password_hash, status) VALUES ($1, $2, $3, $4, 'active')`,
		id, email, strings.Split(email, "@")[0], phc)
	return id
}

func (h *horus) grant(user uuid.UUID, tenant, role string) {
	h.t.Helper()
	var mID uuid.UUID
	if err := h.db.QueryRow(context.Background(), `INSERT INTO auth.tenant_membership (id, tenant_id, user_id, status) VALUES ($1, $2, $3, 'active')
		ON CONFLICT (tenant_id, user_id) DO UPDATE SET status = 'active' RETURNING id`, uuid.Must(uuid.NewV7()), tenant, user).Scan(&mID); err != nil {
		h.t.Fatal(err)
	}
	exec1(h.t, h.db, `INSERT INTO auth.role_assignment (id, tenant_id, membership_id, role_id, scope_type)
		SELECT $1, $2, $3, id, 'tenant' FROM auth.role WHERE key = $4 AND tenant_id IS NULL`, uuid.Must(uuid.NewV7()), tenant, mID, role)
}

// isp es un ISP de Horus con su administrador (2FA) y su nodo.
type isp struct {
	id, slug      string
	admin         *hsession
	adminID       string
	site, realm   string
	router        string
	clientPrefix  string
	customersByIP map[string]string
}

func (h *horus) newISP(platform, slug, cidr string) *isp {
	h.t.Helper()
	email := "admin@" + slug + ".test"
	uid := h.createUser(email)
	r := must(h.t, h.do("POST", "/platform/tenants", platform, map[string]any{"slug": slug, "name": "ISP " + slug, "country": "GT",
		"timezone": "America/Guatemala", "initial_admin_email": email}, map[string]string{"Idempotency-Key": uuid.NewString()}), 201)
	x := &isp{id: r.str("id"), slug: slug, adminID: uid.String(), customersByIP: map[string]string{}}
	s := h.login(email, horusUserPassword)
	s.enrollTOTP()
	s.useTenant(x.id)
	x.admin = s
	tok := s.tok()
	x.site = must(h.t, h.do("POST", "/sites", tok, map[string]any{"name": "Nodo Centro"}), 201).str("id")
	x.router = must(h.t, h.do("POST", "/routers", tok, map[string]any{"site_id": x.site, "name": "rt-centro", "routeros_version": "7.16"}), 201).str("id")
	x.clientPrefix = must(h.t, h.do("POST", "/sites/"+x.site+"/client-prefixes", tok, map[string]any{"prefix": cidr, "role": "customers"}), 201).str("id")
	x.realm = must(h.t, h.do("GET", "/sites/"+x.site, tok, nil), 200).str("private_realm_id")
	return x
}

// customer inserta un cliente visto ahora (lo que haría el ingester al
// descubrirlo) y lo devuelve.
func (h *horus) customer(x *isp, addr string) string {
	h.t.Helper()
	id := uuid.Must(uuid.NewV7()).String()
	exec1(h.t, h.db, `INSERT INTO devices.customer (id, tenant_id, realm_id, address, site_id, client_prefix_id, first_seen, last_seen)
		VALUES ($1, $2, $3, $4::inet, $5, $6, now() - interval '40 days', now())`, id, x.id, x.realm, addr, x.site, x.clientPrefix)
	x.customersByIP[addr] = id
	return id
}

// ---------------------------------------------------------------- Argos

type argos struct {
	t  *testing.T
	c  cfg
	db *pgxpool.Pool
}

func (a *argos) url(p string) string { return a.c.ArgosURL + "/api/v1" + p }

func (a *argos) do(method, path, token string, body any, hdr ...map[string]string) resp {
	a.t.Helper()
	r := rq{Method: method, URL: a.url(path), Token: token, Body: body}
	if len(hdr) > 0 {
		r.Header = hdr[0]
	}
	return do(a.t, r)
}

func (a *argos) login(email, password string) string {
	a.t.Helper()
	return must(a.t, a.do("POST", "/auth/login", "", map[string]string{"email": email, "password": password}), 200).str("token")
}

// version lee la versión actual de una entidad (ediciones con control de versión).
func (a *argos) version(token, path string) int {
	a.t.Helper()
	v, _ := must(a.t, a.do("GET", path, token, nil), 200).Body["version"].(float64)
	return int(v)
}
