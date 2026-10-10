//go:build acceptance

// Package acceptkit reúne lo común de los e2e de aceptación contra el backend
// REAL (tests/acceptance/iN): cliente de la API pública /api/v1 (detrás de
// Traefik), TOTP, sesión del superadministrador con renovación por cookie y
// tokens por ISP.
package acceptkit

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
	"net/http/cookiejar"
	"os"
	"strings"
	"sync"
	"time"
)

// Resp es una respuesta HTTP ya leída.
type Resp struct {
	Status int
	Body   map[string]any
	Raw    []byte
	Header http.Header
}

// Str devuelve el campo k (cadena) del cuerpo.
func (r Resp) Str(k string) string { s, _ := r.Body[k].(string); return s }

// Code devuelve el código problem+json.
func (r Resp) Code() string { return r.Str("code") }

// Data devuelve el array "data" del cuerpo.
func (r Resp) Data() []map[string]any {
	raw, _ := r.Body["data"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, x := range raw {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// Call es una petición.
type Call struct {
	Method, Path, Token string
	Body                any
	Header              map[string]string
}

// Client habla con la API pública. Guarda cookies (refresh y kiosco).
type Client struct {
	Base string
	HTTP *http.Client
}

// New crea un cliente para base (http(s)://host:puerto). SSL_CERT_FILE sirve
// para confiar en el certificado autogenerado del modo ip_only.
func New(base string) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{Base: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 60 * time.Second, Jar: jar}}
}

// Fork devuelve un cliente con otro tarro de cookies (otro navegador/pantalla).
func (c *Client) Fork() *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{Base: c.Base, HTTP: &http.Client{Timeout: c.HTTP.Timeout, Jar: jar, Transport: c.HTTP.Transport}}
}

// Do ejecuta r.
func (c *Client) Do(r Call) (Resp, error) {
	var body io.Reader
	if r.Body != nil {
		if s, ok := r.Body.(string); ok {
			body = strings.NewReader(s)
		} else {
			b, err := json.Marshal(r.Body)
			if err != nil {
				return Resp{}, err
			}
			body = bytes.NewReader(b)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, r.Method, c.Base+r.Path, body)
	if err != nil {
		return Resp{}, err
	}
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	for k, v := range r.Header {
		req.Header.Set(k, v)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return Resp{}, fmt.Errorf("%s %s: %w", r.Method, r.Path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := Resp{Status: res.StatusCode, Raw: raw, Header: res.Header, Body: map[string]any{}}
	_ = json.Unmarshal(raw, &out.Body)
	return out, nil
}

// TOTP calcula el código de un secreto base32 (RFC 6238, SHA-1, 6 dígitos, 30 s).
func TOTP(secret string, at time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	if err != nil {
		return "", fmt.Errorf("secreto TOTP no es base32: %w", err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(at.Unix()/30)) //nolint:gosec // instante positivo
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff)%1_000_000), nil
}

// RandomSuffix devuelve 6 caracteres hexadecimales aleatorios.
func RandomSuffix() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Creds son las credenciales del superadministrador. Se guardan en un
// archivo de estado (fuera del repositorio) para poder repetir la batería
// contra la misma instalación: el primer login cambia la contraseña semilla y
// activa TOTP, y eso no se puede repetir.
type Creds struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	TOTPSecret string `json:"totp_secret,omitempty"`
	LastStep   int64  `json:"last_totp_step,omitempty"`
}

// LoadCreds lee path (si no existe, devuelve def).
func LoadCreds(path string, def Creds) (Creds, error) {
	b, err := os.ReadFile(path) //nolint:gosec // archivo de estado de la batería
	if errors.Is(err, os.ErrNotExist) {
		return def, nil
	}
	if err != nil {
		return def, err
	}
	var c Creds
	if err := json.Unmarshal(b, &c); err != nil {
		return def, err
	}
	if c.Email != def.Email {
		return def, nil // otra instalación u otro usuario: se empieza de cero
	}
	return c, nil
}

// SaveCreds escribe c en path (0600).
func SaveCreds(path string, c Creds) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// Session es la sesión del superadministrador: login (con cambio de
// contraseña y alta de TOTP la primera vez), renovación por la cookie de
// refresh y tokens de plataforma y de ISP.
type Session struct {
	C         *Client
	Creds     Creds
	CredsPath string
	Logf      func(string, ...any)

	mu      sync.Mutex
	access  string
	expires time.Time
	tenant  map[string]tok
}

type tok struct {
	v   string
	exp time.Time
}

func expiry(r Resp) time.Time {
	if t, err := time.Parse(time.RFC3339, r.Str("expires_at")); err == nil {
		return t
	}
	return time.Now().Add(5 * time.Minute)
}

func (s *Session) logf(f string, a ...any) {
	if s.Logf != nil {
		s.Logf(f, a...)
	}
}

func (s *Session) save() {
	if s.CredsPath != "" {
		_ = SaveCreds(s.CredsPath, s.Creds)
	}
}

// code devuelve un código TOTP de un paso no usado (auth rechaza reutilizarlo).
func (s *Session) code() (string, error) {
	for {
		step := time.Now().Unix() / 30
		if step > s.Creds.LastStep {
			c, err := TOTP(s.Creds.TOTPSecret, time.Now())
			if err != nil {
				return "", err
			}
			s.Creds.LastStep = step
			s.save()
			return c, nil
		}
		d := time.Duration(30-time.Now().Unix()%30)*time.Second + 500*time.Millisecond
		s.logf("..  esperando %s al siguiente paso TOTP (anti-reutilización)", d.Round(time.Second))
		time.Sleep(d)
	}
}

func (s *Session) post(token, path string, body any) (Resp, error) {
	return s.C.Do(Call{Method: http.MethodPost, Path: path, Token: token, Body: body})
}

// Login inicia sesión. seedPassword es la contraseña semilla de una
// instalación nueva (se usa si las credenciales guardadas no funcionan).
func (s *Session) Login(seedPassword string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.login(seedPassword)
}

func (s *Session) login(seedPassword string) error {
	try := func(pw string) (Resp, error) {
		return s.post("", "/api/v1/auth/login", map[string]string{"username": s.Creds.Email, "password": pw})
	}
	r, err := try(s.Creds.Password)
	if err != nil {
		return err
	}
	if r.Status == http.StatusUnauthorized && seedPassword != "" && seedPassword != s.Creds.Password {
		if r, err = try(seedPassword); err != nil {
			return err
		}
		if r.Status == http.StatusOK {
			s.Creds.Password, s.Creds.TOTPSecret, s.Creds.LastStep = seedPassword, "", 0
		}
	}
	if r.Status != http.StatusOK {
		return fmt.Errorf("login de %s: HTTP %d %s", s.Creds.Email, r.Status, r.Raw)
	}
	if r.Body["mfa_required"] == true {
		if s.Creds.TOTPSecret == "" {
			return fmt.Errorf("el superadministrador %s tiene TOTP: indique su secreto (ACCEPT_ADMIN_TOTP_SECRET_FILE)", s.Creds.Email)
		}
		c, err := s.code()
		if err != nil {
			return err
		}
		if r, err = s.post("", "/api/v1/auth/mfa/verify", map[string]string{"mfa_token": r.Str("mfa_token"), "code": c}); err != nil {
			return err
		}
		if r.Status != http.StatusOK {
			return fmt.Errorf("segundo factor: HTTP %d %s", r.Status, r.Raw)
		}
	}
	s.access, s.expires = r.Str("access_token"), expiry(r)
	s.tenant = map[string]tok{}
	// Primer login de una instalación nueva: contraseña y TOTP obligatorios (D14).
	me, err := s.C.Do(Call{Method: http.MethodGet, Path: "/api/v1/me", Token: s.access})
	if err != nil {
		return err
	}
	if me.Body["must_change_password"] == true {
		pw := "Accept-" + RandomSuffix() + RandomSuffix() + "-clave-larga"
		r, err := s.post(s.access, "/api/v1/me/password", map[string]string{"current_password": s.Creds.Password, "new_password": pw})
		if err != nil || r.Status != http.StatusNoContent {
			return fmt.Errorf("cambio de contraseña obligatorio: %v HTTP %d %s", err, r.Status, r.Raw)
		}
		s.Creds.Password = pw
		s.save()
		s.logf("ok  contraseña semilla cambiada (guardada en %s)", s.CredsPath)
	}
	if me.Body["mfa_enabled"] != true {
		enr, err := s.post(s.access, "/api/v1/me/totp/enroll", nil)
		if err != nil || enr.Status != http.StatusOK || enr.Str("secret") == "" {
			return fmt.Errorf("alta de TOTP: %v HTTP %d %s", err, enr.Status, enr.Raw)
		}
		s.Creds.TOTPSecret, s.Creds.LastStep = enr.Str("secret"), 0
		c, err := s.code()
		if err != nil {
			return err
		}
		if r, err := s.post(s.access, "/api/v1/me/totp/confirm", map[string]string{"code": c}); err != nil || r.Status != http.StatusOK {
			return fmt.Errorf("confirmar TOTP: %v HTTP %d %s", err, r.Status, r.Raw)
		}
		s.save()
		s.logf("ok  TOTP activado (secreto guardado en %s)", s.CredsPath)
		return s.login("") // la sesión anterior no tenía 2FA: se repite el login
	}
	return nil
}

// Access devuelve un token de sesión vigente (renueva con la cookie de
// refresh o, si falla, repite el login).
func (s *Session) Access() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accessLocked()
}

func (s *Session) accessLocked() (string, error) {
	if s.access != "" && time.Until(s.expires) > 90*time.Second {
		return s.access, nil
	}
	r, err := s.C.Do(Call{Method: http.MethodPost, Path: "/api/v1/auth/refresh", Header: map[string]string{"X-Requested-With": "horus"}})
	if err == nil && r.Status == http.StatusOK && r.Str("access_token") != "" {
		s.access, s.expires = r.Str("access_token"), expiry(r)
		s.tenant = map[string]tok{}
		return s.access, nil
	}
	if err := s.login(""); err != nil {
		return "", err
	}
	return s.access, nil
}

// Token devuelve un token de ámbito tenant (id de ISP) o "platform".
func (s *Session) Token(scope string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tenant[scope]; ok && time.Until(t.exp) > 90*time.Second {
		return t.v, nil
	}
	a, err := s.accessLocked()
	if err != nil {
		return "", err
	}
	body := map[string]string{"tenant_id": scope}
	if scope == "platform" {
		body = map[string]string{"scope": "platform"}
	}
	r, err := s.post(a, "/api/v1/auth/token", body)
	if err != nil {
		return "", err
	}
	if r.Status != http.StatusOK {
		return "", fmt.Errorf("POST /auth/token %s: HTTP %d %s", scope, r.Status, r.Raw)
	}
	s.tenant[scope] = tok{v: r.Str("access_token"), exp: expiry(r)}
	return r.Str("access_token"), nil
}
