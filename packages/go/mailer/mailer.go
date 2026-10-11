// Package mailer envía correo por el SMTP de la instalación (HORUS_SMTP_*):
// conexión con STARTTLS (por defecto), TLS implícito o en claro, AUTH PLAIN
// si hay usuario, y mensajes en texto plano o texto + HTML
// (multipart/alternative). Lo usan alerts (canal email) y auth
// (invitaciones). Sin lógica de dominio.
package mailer

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Modos de HORUS_SMTP_TLS.
const (
	TLSStartTLS = "starttls"
	TLSImplicit = "tls"
	TLSNone     = "none"
)

// Plazos por defecto de la conexión.
const (
	DefaultDialTimeout = 10 * time.Second
	DefaultIOTimeout   = 20 * time.Second
)

// ErrAuth: el servidor rechazó las credenciales (el mensaje empieza por
// "auth:").
var ErrAuth = errors.New("auth")

// ErrNotConfigured: sin HORUS_SMTP_HOST.
var ErrNotConfigured = errors.New("smtp not configured")

// Config es el SMTP de la instalación (HORUS_SMTP_*).
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	// TLS: starttls (por defecto), tls (implícito, 465) o none.
	TLS string
	// DialTimeout e IOTimeout acotan la conexión y la sesión (0 = por
	// defecto). El plazo del contexto, si es menor, manda.
	DialTimeout time.Duration
	IOTimeout   time.Duration
}

// Enabled indica si hay SMTP configurado.
func (c Config) Enabled() bool { return c.Host != "" }

// Sender es el remitente (HORUS_SMTP_FROM o horus@<host>).
func (c Config) Sender() string {
	if c.From != "" {
		return c.From
	}
	return "horus@" + c.Host
}

// ValidTLS indica si v es un modo de HORUS_SMTP_TLS válido ("" = starttls).
func ValidTLS(v string) bool {
	switch v {
	case "", TLSStartTLS, TLSImplicit, TLSNone:
		return true
	}
	return false
}

// Dial abre una sesión SMTP lista para MAIL (EHLO, STARTTLS y AUTH según la
// configuración). Un rechazo de credenciales devuelve un error que envuelve
// ErrAuth con el prefijo "auth:".
func Dial(ctx context.Context, c Config) (*smtp.Client, error) {
	if c.Host == "" {
		return nil, ErrNotConfigured
	}
	port := c.Port
	if port == 0 {
		port = 587
	}
	addr := net.JoinHostPort(c.Host, strconv.Itoa(port))
	dt := c.DialTimeout
	if dt <= 0 {
		dt = DefaultDialTimeout
	}
	d := net.Dialer{Timeout: dt}
	var conn net.Conn
	var err error
	if c.TLS == TLSImplicit {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // lo clasifica quien llama
	}
	it := c.IOTimeout
	if it <= 0 {
		it = DefaultIOTimeout
	}
	deadline := time.Now().Add(it)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	cl, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		_ = conn.Close()
		return nil, err //nolint:wrapcheck // lo clasifica quien llama
	}
	if err := cl.Hello("horus"); err != nil {
		_ = cl.Close()
		return nil, err //nolint:wrapcheck // lo clasifica quien llama
	}
	if c.TLS != TLSImplicit && c.TLS != TLSNone {
		if ok, _ := cl.Extension("STARTTLS"); ok {
			if err := cl.StartTLS(&tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}); err != nil {
				_ = cl.Close()
				return nil, err //nolint:wrapcheck // lo clasifica quien llama
			}
		}
	}
	if c.Username != "" {
		if ok, _ := cl.Extension("AUTH"); ok {
			if err := cl.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); err != nil {
				_ = cl.Close()
				return nil, fmt.Errorf("%w: %w", ErrAuth, err)
			}
		}
	}
	return cl, nil
}

// Message es un correo. Sin HTML se envía text/plain; con HTML,
// multipart/alternative con las dos versiones.
type Message struct {
	To      []string
	Subject string
	Text    string
	HTML    string
}

// Send abre una sesión y entrega m. Los errores llevan el prefijo "smtp:"
// y la fase (MAIL, RCPT, DATA…); el texto del servidor puede incluir la
// dirección del destinatario, nunca el cuerpo.
func Send(ctx context.Context, c Config, m Message) error {
	from := c.Sender()
	for _, a := range append([]string{from}, m.To...) {
		if strings.ContainsAny(a, "\r\n") {
			return errors.New("smtp: invalid address")
		}
	}
	if len(m.To) == 0 {
		return errors.New("smtp: no recipients")
	}
	cl, err := Dial(ctx, c)
	if err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	defer func() { _ = cl.Close() }()
	if err := cl.Mail(from); err != nil {
		return fmt.Errorf("smtp: MAIL: %w", err)
	}
	for _, r := range m.To {
		if err := cl.Rcpt(r); err != nil {
			return fmt.Errorf("smtp: RCPT: %w", err)
		}
	}
	w, err := cl.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if _, err := io.WriteString(w, Compose(from, m)); err != nil {
		return fmt.Errorf("smtp: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: end: %w", err)
	}
	return cl.Quit() //nolint:wrapcheck // fin de sesión
}

func crlf(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

// Compose construye las cabeceras y el cuerpo del mensaje (UTF-8, 8bit;
// asunto en codificación B de RFC 2047).
func Compose(from string, m Message) string {
	head := "From: " + from + "\r\nTo: " + strings.Join(m.To, ", ") +
		"\r\nSubject: =?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(m.Subject)) + "?=" + "\r\nMIME-Version: 1.0\r\n"
	if m.HTML == "" {
		return head + "Content-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" + crlf(m.Text) + "\r\n"
	}
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	boundary := "horus-" + hex.EncodeToString(b)
	part := func(typ, body string) string {
		return "--" + boundary + "\r\nContent-Type: " + typ + "; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" + crlf(body) + "\r\n"
	}
	return head + "Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n" +
		part("text/plain", m.Text) + part("text/html", m.HTML) + "--" + boundary + "--\r\n"
}
