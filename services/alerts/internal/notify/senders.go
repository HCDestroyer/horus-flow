package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Message es una notificación lista para enviar (sin secretos).
type Message struct {
	Subject string `json:"subject"`
	Text    string `json:"text"`
	Link    string `json:"link,omitempty"`
}

// SMTPConfig es el SMTP de la instalación (HORUS_SMTP_*).
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	// TLS: starttls (por defecto), tls (implícito, 465) o none.
	TLS string
}

// Senders agrupa los transportes (configurables en tests).
type Senders struct {
	SMTP           SMTPConfig
	TelegramAPI    string // https://api.telegram.org
	TelegramToken  string // bot de la instalación
	HTTPClient     func(tlsVerify bool, timeout time.Duration) *http.Client
	DefaultTimeout time.Duration
}

// LibreNMSAlertPath es el recurso de la API de LibreNMS donde Horus deja cada
// notificación (registro de eventos). Supuesto a validar con la instancia
// dedicada de la persona (D17).
const LibreNMSAlertPath = "/api/v0/eventlog"

// TestResult es ConnectionTestResult sin canal ni fecha.
type TestResult struct {
	OK            bool
	LatencyMS     *int
	RemoteVersion *string
	ErrorCode     *string
	Error         *string
}

func fail(code, msg string) TestResult {
	return TestResult{ErrorCode: &code, Error: truncate(msg)}
}

func (s *Senders) client(tlsVerify bool, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = s.DefaultTimeout
	}
	if s.HTTPClient != nil {
		return s.HTTPClient(tlsVerify, timeout)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // transporte estándar
	if !tlsVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // tls_verify=false explícito y auditado (D17)
	}
	return &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// classify traduce un error de red a ErrorCode del contrato (sin el cuerpo remoto).
func classify(err error) TestResult {
	var dns *net.DNSError
	var cert x509.UnknownAuthorityError
	var host x509.HostnameError
	var inv x509.CertificateInvalidError
	var tlsErr *tls.CertificateVerificationError
	var nerr net.Error
	switch {
	case errors.As(err, &dns):
		return fail("dns_failed", "No se pudo resolver el nombre del destino.")
	case errors.As(err, &cert), errors.As(err, &host), errors.As(err, &inv), errors.As(err, &tlsErr):
		return fail("tls_invalid", "Certificado TLS no válido para el destino.")
	case errors.As(err, &nerr) && nerr.Timeout():
		return fail("timeout", "El destino no respondió a tiempo.")
	}
	return fail("connect_failed", "No se pudo conectar con el destino.")
}

func httpStatus(code int) TestResult {
	switch code {
	case http.StatusUnauthorized:
		return fail("auth_failed", "Credenciales rechazadas por el destino.")
	case http.StatusForbidden:
		return fail("forbidden", "El usuario no tiene permiso en el destino.")
	}
	return fail("unexpected_response", "Respuesta inesperada del destino (HTTP "+strconv.Itoa(code)+").")
}

// ---------------------------------------------------------------- email

func (s *Senders) smtpClient(ctx context.Context) (*smtp.Client, error) {
	if s.SMTP.Host == "" {
		return nil, errors.New("smtp not configured")
	}
	port := s.SMTP.Port
	if port == 0 {
		port = 587
	}
	addr := net.JoinHostPort(s.SMTP.Host, strconv.Itoa(port))
	d := net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	if s.SMTP.TLS == "tls" {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: &tls.Config{ServerName: s.SMTP.Host, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // se clasifica
	}
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	c, err := smtp.NewClient(conn, s.SMTP.Host)
	if err != nil {
		_ = conn.Close()
		return nil, err //nolint:wrapcheck // se clasifica
	}
	if err := c.Hello("horus"); err != nil {
		_ = c.Close()
		return nil, err //nolint:wrapcheck // se clasifica
	}
	if s.SMTP.TLS != "tls" && s.SMTP.TLS != "none" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: s.SMTP.Host, MinVersion: tls.VersionTLS12}); err != nil {
				_ = c.Close()
				return nil, err //nolint:wrapcheck // se clasifica
			}
		}
	}
	if s.SMTP.Username != "" {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(smtp.PlainAuth("", s.SMTP.Username, s.SMTP.Password, s.SMTP.Host)); err != nil {
				_ = c.Close()
				return nil, fmt.Errorf("auth: %w", err)
			}
		}
	}
	return c, nil
}

func (s *Senders) testEmail(ctx context.Context) TestResult {
	if s.SMTP.Host == "" {
		return fail("missing_credentials", "El SMTP de la instalación no está configurado (HORUS_SMTP_HOST).")
	}
	c, err := s.smtpClient(ctx)
	if err != nil {
		if strings.HasPrefix(err.Error(), "auth:") {
			return fail("auth_failed", "El SMTP rechazó las credenciales de la instalación.")
		}
		return classify(err)
	}
	_ = c.Quit()
	return TestResult{OK: true}
}

func (s *Senders) sendEmail(ctx context.Context, cfg EmailConfig, m Message) error {
	c, err := s.smtpClient(ctx)
	if err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	defer func() { _ = c.Close() }()
	from := s.SMTP.From
	if from == "" {
		from = "horus@" + s.SMTP.Host
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp: MAIL: %w", err)
	}
	for _, r := range cfg.Recipients {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("smtp: RCPT: %w", err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	subject := strings.TrimSpace(cfg.SubjectPrefix + " " + m.Subject)
	body := m.Text
	if m.Link != "" {
		body += "\r\n\r\n" + m.Link
	}
	msg := "From: " + from + "\r\nTo: " + strings.Join(cfg.Recipients, ", ") +
		"\r\nSubject: =?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(subject)) + "?=" +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" +
		strings.ReplaceAll(body, "\n", "\r\n") + "\r\n"
	if _, err := io.WriteString(w, msg); err != nil {
		return fmt.Errorf("smtp: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: end: %w", err)
	}
	return c.Quit() //nolint:wrapcheck // fin de sesión
}

// ---------------------------------------------------------------- telegram

func (s *Senders) telegramToken(cr Credentials) string {
	if cr.TelegramBotToken != "" {
		return cr.TelegramBotToken
	}
	return s.TelegramToken
}

func (s *Senders) telegramURL(token, method string) string {
	base := strings.TrimRight(s.TelegramAPI, "/")
	if base == "" {
		base = "https://api.telegram.org"
	}
	return base + "/bot" + token + "/" + method
}

func (s *Senders) testTelegram(ctx context.Context, cr Credentials) TestResult {
	tok := s.telegramToken(cr)
	if tok == "" {
		return fail("missing_credentials", "Sin token de bot (propio o de la instalación).")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.telegramURL(tok, "getMe"), nil)
	resp, err := s.client(true, 0).Do(req)
	if err != nil {
		return classify(scrub(err, tok))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return fail("auth_failed", "Token de bot rechazado por Telegram.")
		}
		return httpStatus(resp.StatusCode)
	}
	return TestResult{OK: true}
}

func (s *Senders) sendTelegram(ctx context.Context, cfg TelegramConfig, cr Credentials, m Message) error {
	tok := s.telegramToken(cr)
	if tok == "" {
		return errors.New("telegram: no bot token")
	}
	text := m.Subject + "\n" + m.Text
	if m.Link != "" {
		text += "\n" + m.Link
	}
	body, _ := json.Marshal(map[string]any{"chat_id": cfg.ChatID, "text": text, "disable_web_page_preview": true})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.telegramURL(tok, "sendMessage"), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client(true, 0).Do(req)
	if err != nil {
		return fmt.Errorf("telegram: %w", scrub(err, tok))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram: HTTP %d", resp.StatusCode)
	}
	return nil
}

// scrub quita el token de un error de net/http (la URL lo contiene).
func scrub(err error, secret string) error {
	if secret == "" {
		return err
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return &url.Error{Op: ue.Op, URL: strings.ReplaceAll(ue.URL, secret, "***"), Err: ue.Err}
	}
	return errors.New(strings.ReplaceAll(err.Error(), secret, "***"))
}

// ---------------------------------------------------------------- librenms

func librenmsAuth(req *http.Request, cfg LibreNMSConfig, cr Credentials) {
	if cr.APIToken != "" {
		req.Header.Set("X-Auth-Token", cr.APIToken)
		return
	}
	req.SetBasicAuth(cfg.Username, cr.Password)
}

func (s *Senders) testLibreNMS(ctx context.Context, cfg LibreNMSConfig, cr Credentials) TestResult {
	if cr.APIToken == "" && cr.Password == "" {
		return fail("missing_credentials", "Configure la contraseña o el token de API (PUT …/credentials).")
	}
	verify := cfg.TLSVerify == nil || *cfg.TLSVerify
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.BaseURL+"/api/v0/system", nil)
	if err != nil {
		return fail("connect_failed", "URL no válida.")
	}
	librenmsAuth(req, cfg, cr)
	resp, err := s.client(verify, time.Duration(cfg.TimeoutSeconds)*time.Second).Do(req)
	if err != nil {
		return classify(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return httpStatus(resp.StatusCode)
	}
	var body struct {
		System []struct {
			LocalVer string `json:"local_ver"`
		} `json:"system"`
	}
	res := TestResult{OK: true}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body) == nil && len(body.System) > 0 && body.System[0].LocalVer != "" {
		v := body.System[0].LocalVer
		res.RemoteVersion = &v
	}
	return res
}

func (s *Senders) sendLibreNMS(ctx context.Context, cfg LibreNMSConfig, cr Credentials, m Message, eventType, severity string) error {
	if cr.APIToken == "" && cr.Password == "" {
		return errors.New("librenms: missing credentials")
	}
	body, _ := json.Marshal(map[string]any{"type": "horus", "title": m.Subject, "message": m.Text, "link": m.Link,
		"event_type": eventType, "severity": severity})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+LibreNMSAlertPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("librenms: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	librenmsAuth(req, cfg, cr)
	resp, err := s.client(cfg.TLSVerify == nil || *cfg.TLSVerify, time.Duration(cfg.TimeoutSeconds)*time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("librenms: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("librenms: HTTP %d", resp.StatusCode)
	}
	return nil
}
