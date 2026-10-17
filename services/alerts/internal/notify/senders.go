package notify

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/mailer"
)

// Message es una notificación lista para enviar (sin secretos).
type Message struct {
	Subject string `json:"subject"`
	Text    string `json:"text"`
	Link    string `json:"link,omitempty"`
	// Webhook es el cuerpo firmado del canal webhook ({id, event, occurred_at,
	// tenant, data}); solo en entregas de canales webhook.
	Webhook json.RawMessage `json:"webhook,omitempty"`
	// DeliveryID identifica la entrega (no se guarda con el mensaje): en
	// LibreNMS va al final de la línea de syslog («| id=<entrega>») para
	// reconocer el duplicado de un reintento tras un reinicio (D23).
	DeliveryID string `json:"-"`
}

// SMTPConfig es el SMTP de la instalación (HORUS_SMTP_*; packages/go/mailer).
type SMTPConfig = mailer.Config

// Senders agrupa los transportes (configurables en tests).
type Senders struct {
	SMTP           SMTPConfig
	TelegramAPI    string // https://api.telegram.org
	TelegramToken  string // bot de la instalación
	HTTPClient     func(tlsVerify bool, timeout time.Duration) *http.Client
	DefaultTimeout time.Duration
	// WebhookGuard limita los destinos del canal webhook (https y direcciones
	// públicas; modo laboratorio con HORUS_ALERTS_WEBHOOK_ALLOW_PRIVATE).
	WebhookGuard *datasets.EgressGuard
}

// TestResult es ConnectionTestResult sin canal ni fecha.
type TestResult struct {
	OK            bool
	LatencyMS     *int
	RemoteVersion *string
	ErrorCode     *string
	Error         *string
	// Telegram: @usuario del bot y título del chat comprobados.
	BotUsername *string
	ChatTitle   *string
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

func (s *Senders) testEmail(ctx context.Context) TestResult {
	if s.SMTP.Host == "" {
		return fail("missing_credentials", "El SMTP de la instalación no está configurado (HORUS_SMTP_HOST).")
	}
	c, err := mailer.Dial(ctx, s.SMTP)
	if err != nil {
		if errors.Is(err, mailer.ErrAuth) {
			return fail("auth_failed", "El SMTP rechazó las credenciales de la instalación.")
		}
		return classify(err)
	}
	_ = c.Quit()
	return TestResult{OK: true}
}

func (s *Senders) sendEmail(ctx context.Context, cfg EmailConfig, m Message) error {
	body := m.Text
	if m.Link != "" {
		body += "\n\n" + m.Link
	}
	return mailer.Send(ctx, s.SMTP, mailer.Message{To: cfg.Recipients, Subject: strings.TrimSpace(cfg.SubjectPrefix + " " + m.Subject), Text: body}) //nolint:wrapcheck // ya lleva el prefijo smtp:
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
