// Package loadkit es el arnés común de las pruebas de carga (tests/load) y de
// fallo (tests/chaos) de la historia I1-26: cliente de la API pública con el
// superadministrador semilla, alta del ISP de prueba, inventario base del
// collector, simulador de flujos, métricas Prometheus, lag del consumer de
// JetStream, recuento en ClickHouse y sondas de latencia y de kiosco.
//
// Trabaja contra el compose de tests/load/stack.sh (variables LOAD_*).
package loadkit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // TOTP RFC 6238 usa HMAC-SHA1
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Resp es una respuesta de la API.
type Resp struct {
	Status int
	Body   map[string]any
	Raw    []byte
	Header http.Header
}

// Str devuelve un campo de texto del cuerpo.
func (r Resp) Str(k string) string { s, _ := r.Body[k].(string); return s }

// API habla con /api/v1 como el superadministrador semilla. Renueva solo los
// tokens (10 min de vida; claves nuevas tras reiniciar horus-app) repitiendo
// el login con contraseña y TOTP.
type API struct {
	Base  string
	HTTP  *http.Client
	Email string

	mu       sync.Mutex
	state    *State
	statePth string
	session  string
	tenant   string
	lastStep int64
}

// State es lo que sobrevive entre ejecuciones contra el mismo compose: el
// primer login cambia la contraseña semilla y activa TOTP (una sola vez).
type State struct {
	Password   string    `json:"password"`
	TOTPSecret string    `json:"totp_secret"`
	TenantID   string    `json:"tenant_id,omitempty"`
	SiteID     string    `json:"site_id,omitempty"`
	RouterID   string    `json:"router_id,omitempty"`
	KioskID    string    `json:"kiosk_id,omitempty"`
	Created    time.Time `json:"created"`
}

// NewAPI prepara el cliente; statePath guarda contraseña y secreto TOTP.
func NewAPI(base, email, seedPasswordFile, statePath string) (*API, error) {
	a := &API{Base: strings.TrimRight(base, "/"), Email: email, statePth: statePath,
		HTTP: &http.Client{Timeout: 30 * time.Second}}
	if b, err := os.ReadFile(statePath); err == nil { //nolint:gosec // ruta de la prueba
		var st State
		if json.Unmarshal(b, &st) == nil && st.Password != "" {
			a.state = &st
		}
	}
	if a.state == nil {
		raw, err := os.ReadFile(seedPasswordFile) //nolint:gosec // ruta de la prueba
		if err != nil {
			return nil, fmt.Errorf("contraseña semilla: %w", err)
		}
		a.state = &State{Password: strings.TrimSpace(string(raw))}
	}
	return a, nil
}

// State devuelve una copia del estado guardado.
func (a *API) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return *a.state
}

// SaveState actualiza y persiste el estado.
func (a *API) SaveState(f func(*State)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	f(a.state)
	return a.saveLocked()
}

func (a *API) saveLocked() error {
	b, err := json.MarshalIndent(a.state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.statePth, b, 0o600)
}

// Call es una petición.
type Call struct {
	Method, Path, Token string
	Body                any
	Header              map[string]string
}

// Do ejecuta una petición sin reintentos.
func (a *API) Do(ctx context.Context, c Call) (Resp, error) {
	var body io.Reader
	if c.Body != nil {
		b, err := json.Marshal(c.Body)
		if err != nil {
			return Resp{}, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, c.Method, a.Base+c.Path, body)
	if err != nil {
		return Resp{}, err
	}
	if c.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	for k, v := range c.Header {
		req.Header.Set(k, v)
	}
	res, err := a.HTTP.Do(req)
	if err != nil {
		return Resp{}, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := Resp{Status: res.StatusCode, Raw: raw, Header: res.Header, Body: map[string]any{}}
	_ = json.Unmarshal(raw, &out.Body)
	return out, nil
}

// expect ejecuta c (con el token del ISP si tenant) y comprueba el estado.
func (a *API) expect(ctx context.Context, tenant bool, c Call, what string, status int) (Resp, error) {
	var r Resp
	var err error
	if tenant {
		r, err = a.TenantDo(ctx, c)
	} else {
		r, err = a.Do(ctx, c)
	}
	return check(r, err, what, status)
}

func check(r Resp, err error, what string, status int) (Resp, error) {
	if err != nil {
		return r, fmt.Errorf("%s: %w", what, err)
	}
	if r.Status != status {
		return r, fmt.Errorf("%s: HTTP %d, se esperaba %d: %s", what, r.Status, status, r.Raw)
	}
	return r, nil
}

// totp calcula el código RFC 6238 del paso step.
func totp(secret string, step int64) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // paso positivo
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff)%1_000_000), nil
}

// code devuelve un código TOTP de un paso no usado aún (auth rechaza reutilizarlo).
func (a *API) code(ctx context.Context) (string, error) {
	for {
		step := time.Now().Unix() / 30
		if step > a.lastStep {
			a.lastStep = step
			return totp(a.state.TOTPSecret, step)
		}
		wait := time.Until(time.Unix((step+1)*30, 0)) + 300*time.Millisecond
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// login obtiene una sesión con 2FA; la primera vez cambia la contraseña
// semilla y da de alta TOTP (como el e2e de I0).
func (a *API) loginLocked(ctx context.Context) error {
	r, err := a.expect(ctx, false, Call{Method: http.MethodPost, Path: "/api/v1/auth/login",
		Body: map[string]string{"username": a.Email, "password": a.state.Password}}, "login", 200)
	if err != nil {
		return err
	}
	if r.Body["mfa_required"] == true {
		code, err := a.code(ctx)
		if err != nil {
			return err
		}
		m, err := a.expect(ctx, false, Call{Method: http.MethodPost, Path: "/api/v1/auth/mfa/verify",
			Body: map[string]string{"mfa_token": r.Str("mfa_token"), "code": code}}, "mfa/verify", 200)
		if err != nil {
			return err
		}
		a.session = m.Str("access_token")
		return nil
	}
	// Primer login: contraseña nueva y TOTP.
	sess := r.Str("access_token")
	newPw := "horus-load-" + randomHex(8) + "-clave-larga"
	if _, err := a.expect(ctx, false, Call{Method: http.MethodPost, Path: "/api/v1/me/password", Token: sess,
		Body: map[string]string{"current_password": a.state.Password, "new_password": newPw}}, "cambio de contraseña", 204); err != nil {
		return err
	}
	a.state.Password = newPw
	enr, err := a.expect(ctx, false, Call{Method: http.MethodPost, Path: "/api/v1/me/totp/enroll", Token: sess}, "totp/enroll", 200)
	if err != nil {
		return err
	}
	a.state.TOTPSecret = enr.Str("secret")
	code, err := a.code(ctx)
	if err != nil {
		return err
	}
	if _, err := a.expect(ctx, false, Call{Method: http.MethodPost, Path: "/api/v1/me/totp/confirm", Token: sess,
		Body: map[string]string{"code": code}}, "totp/confirm", 200); err != nil {
		return err
	}
	a.state.Created = time.Now().UTC()
	if err := a.saveLocked(); err != nil {
		return err
	}
	return a.loginLocked(ctx)
}

// Session devuelve un token de sesión válido (login si hace falta).
func (a *API) Session(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == "" {
		if err := a.loginLocked(ctx); err != nil {
			return "", err
		}
	}
	return a.session, nil
}

// Platform devuelve un token de plataforma.
func (a *API) Platform(ctx context.Context) (string, error) {
	sess, err := a.Session(ctx)
	if err != nil {
		return "", err
	}
	r, err := a.expect(ctx, false, Call{Method: http.MethodPost, Path: "/api/v1/auth/token", Token: sess,
		Body: map[string]string{"scope": "platform"}}, "token de plataforma", 200)
	return r.Str("access_token"), err
}

// TenantToken devuelve un token del ISP de prueba (cacheado hasta un 401).
func (a *API) TenantToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	tok, tenant := a.tenant, a.state.TenantID
	a.mu.Unlock()
	if tok != "" {
		return tok, nil
	}
	if tenant == "" {
		return "", errors.New("sin ISP de prueba (Setup)")
	}
	sess, err := a.Session(ctx)
	if err != nil {
		return "", err
	}
	r, err := a.Do(ctx, Call{Method: http.MethodPost, Path: "/api/v1/auth/token", Token: sess,
		Body: map[string]string{"tenant_id": tenant}})
	if err == nil && r.Status == http.StatusUnauthorized {
		a.Invalidate()
		if sess, err = a.Session(ctx); err != nil {
			return "", err
		}
		r, err = a.Do(ctx, Call{Method: http.MethodPost, Path: "/api/v1/auth/token", Token: sess,
			Body: map[string]string{"tenant_id": tenant}})
	}
	if r, err = check(r, err, "token del ISP", 200); err != nil {
		return "", err
	}
	a.mu.Lock()
	a.tenant = r.Str("access_token")
	a.mu.Unlock()
	return r.Str("access_token"), nil
}

// Invalidate olvida los tokens (tras un 401).
func (a *API) Invalidate() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.session, a.tenant = "", ""
}

// TenantDo hace una petición con el token del ISP; ante un 401 renueva y repite una vez.
func (a *API) TenantDo(ctx context.Context, c Call) (Resp, error) {
	for attempt := 0; ; attempt++ {
		tok, err := a.TenantToken(ctx)
		if err != nil {
			return Resp{}, err
		}
		c.Token = tok
		r, err := a.Do(ctx, c)
		if err != nil || r.Status != http.StatusUnauthorized || attempt > 0 {
			return r, err
		}
		a.Invalidate()
	}
}

// Setup da de alta (una vez por compose) el ISP de prueba con un nodo y el
// prefijo de clientes del escenario `normal` del simulador.
func (a *API) Setup(ctx context.Context, prefixes ...string) (State, error) {
	if st := a.State(); st.TenantID != "" && st.SiteID != "" {
		return st, nil
	}
	pt, err := a.Platform(ctx)
	if err != nil {
		return State{}, err
	}
	slug := "load-" + randomHex(3)
	t, err := a.expect(ctx, false, Call{Method: http.MethodPost, Path: "/api/v1/platform/tenants", Token: pt,
		Header: map[string]string{"Idempotency-Key": uuid.NewString()},
		Body: map[string]any{"slug": slug, "name": "ISP de carga " + slug, "country": "MX", "timezone": "America/Mexico_City",
			"initial_admin_email": "noc@" + slug + ".example.net"}}, "alta del ISP", 201)
	if err != nil {
		return State{}, err
	}
	if err := a.SaveState(func(s *State) { s.TenantID = t.Str("id") }); err != nil {
		return State{}, err
	}
	site, err := a.expect(ctx, true, Call{Method: http.MethodPost, Path: "/api/v1/sites",
		Body: map[string]any{"name": "Nodo de carga", "code": "LOAD"}}, "alta del nodo", 201)
	if err != nil {
		return State{}, err
	}
	for _, prefix := range prefixes {
		body := map[string]any{"prefix": prefix, "role": "customers", "assignment_mode": "dynamic"}
		if strings.Contains(prefix, ":") {
			body["ipv6_client_len"] = 64 // /64 delegado por cliente (isp10k)
		}
		if _, err := a.expect(ctx, true, Call{Method: http.MethodPost, Path: "/api/v1/sites/" + site.Str("id") + "/client-prefixes",
			Body: body}, "alta del prefijo "+prefix, 201); err != nil {
			return State{}, err
		}
	}
	// El router del simulador no se da de alta por la API: su IP de túnel la
	// asignaría WireGuard; el inventario base lo registra con la IP de origen
	// real (ver WriteInventory).
	err = a.SaveState(func(s *State) { s.SiteID = site.Str("id"); s.RouterID = uuid.Must(uuid.NewV7()).String() })
	return a.State(), err
}
