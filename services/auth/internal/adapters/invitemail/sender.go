// Package invitemail entrega las invitaciones a un ISP por el SMTP de la
// instalación (HORUS_SMTP_*, packages/go/mailer): correo en español, texto
// plano + HTML sencillo, con el enlace de aceptación y su caducidad.
package invitemail

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"strings"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/mailer"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
)

// Sender implementa app.InvitationSender.
type Sender struct {
	cfg mailer.Config
}

// New crea el canal con el SMTP de la instalación. Sin plazos propios, la
// conexión y la sesión se acotan a 10 s (además del plazo del contexto).
func New(cfg mailer.Config) *Sender {
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 10 * time.Second
	}
	if cfg.IOTimeout <= 0 {
		cfg.IOTimeout = 10 * time.Second
	}
	return &Sender{cfg: cfg}
}

var (
	_ app.InvitationSender    = (*Sender)(nil)
	_ app.PasswordResetSender = (*Sender)(nil)
)

// sendError es el único error que devuelve el canal: el del servidor puede
// llevar la dirección y nunca se propaga.
type sendError struct{ phase string }

func (e *sendError) Error() string { return "invitation email: " + e.phase }

// SendInvitation envía el correo. El error no incluye la dirección, el token
// ni el enlace: solo la fase que falló.
func (s *Sender) SendInvitation(ctx context.Context, d app.InvitationDelivery) error {
	m, err := Compose(d)
	if err != nil {
		return &sendError{phase: "compose"}
	}
	if err := mailer.Send(ctx, s.cfg, m); err != nil {
		return &sendError{phase: phase(err)}
	}
	return nil
}

func phase(err error) string {
	msg := err.Error()
	switch {
	case errors.Is(err, mailer.ErrAuth):
		return "auth rejected"
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "i/o timeout"):
		return "timeout"
	}
	for _, p := range []string{"MAIL", "RCPT", "DATA", "write", "end"} {
		if strings.HasPrefix(msg, "smtp: "+p+":") {
			return p + " failed"
		}
	}
	return "connect failed"
}

// tenantLabel es el nombre del ISP para el asunto y el cuerpo.
func tenantLabel(name string) string {
	name = strings.Join(strings.Fields(name), " ") // sin saltos de línea en el asunto
	if name == "" {
		return "su ISP"
	}
	return name
}

var htmlTmpl = template.Must(template.New("invitation").Parse(`<!doctype html>
<html lang="es"><head><meta charset="utf-8"><title>{{.Subject}}</title></head>
<body style="font-family:Arial,Helvetica,sans-serif;color:#1f2933;line-height:1.5;max-width:560px;margin:0 auto;padding:24px">
<p>Hola:</p>
<p>Le han invitado a unirse a <strong>{{.Tenant}}</strong> en Horus Flow.</p>
<p>Para aceptar la invitación abra este enlace:</p>
<p><a href="{{.URL}}" style="display:inline-block;background:#1d4ed8;color:#ffffff;text-decoration:none;padding:10px 18px;border-radius:6px">Aceptar la invitación</a></p>
<p style="font-size:13px;color:#52606d">Si el botón no funciona, copie esta dirección en el navegador:<br><span style="word-break:break-all">{{.URL}}</span></p>
<p>El enlace es personal, sirve una sola vez y caduca el {{.Expires}}.</p>
<p style="font-size:13px;color:#52606d">Si no esperaba esta invitación, ignore este correo: sin aceptarla no se crea ningún acceso.</p>
</body></html>
`))

// Compose construye el correo de una invitación.
func Compose(d app.InvitationDelivery) (mailer.Message, error) {
	tenant := tenantLabel(d.TenantName)
	subject := "Invitación a " + tenant + " en Horus Flow"
	exp := d.ExpiresAt.UTC()
	expires := exp.Format("02/01/2006") + " a las " + exp.Format("15:04") + " (UTC)"
	text := "Hola:\n\n" +
		"Le han invitado a unirse a " + tenant + " en Horus Flow.\n\n" +
		"Para aceptar la invitación abra este enlace:\n\n" + d.URL + "\n\n" +
		"El enlace es personal, sirve una sola vez y caduca el " + expires + ".\n\n" +
		"Si no esperaba esta invitación, ignore este correo: sin aceptarla no se crea ningún acceso.\n"
	var b bytes.Buffer
	if err := htmlTmpl.Execute(&b, map[string]any{"Subject": subject, "Tenant": tenant, "URL": d.URL, "Expires": expires}); err != nil {
		return mailer.Message{}, err
	}
	return mailer.Message{To: []string{d.Email}, Subject: subject, Text: text, HTML: b.String()}, nil
}

var resetHTMLTmpl = template.Must(template.New("reset").Parse(`<!doctype html>
<html lang="es"><head><meta charset="utf-8"><title>{{.Subject}}</title></head>
<body style="font-family:Arial,Helvetica,sans-serif;color:#1f2933;line-height:1.5;max-width:560px;margin:0 auto;padding:24px">
<p>Hola{{if .Name}}, {{.Name}}{{end}}:</p>
<p>{{.Intro}}</p>
<p><a href="{{.URL}}" style="display:inline-block;background:#1d4ed8;color:#ffffff;text-decoration:none;padding:10px 18px;border-radius:6px">Elegir una contraseña nueva</a></p>
<p style="font-size:13px;color:#52606d">Si el botón no funciona, copie esta dirección en el navegador:<br><span style="word-break:break-all">{{.URL}}</span></p>
<p>El enlace es personal, sirve una sola vez y caduca el {{.Expires}}. Al usarlo se cierran todas las sesiones abiertas de la cuenta.</p>
<p style="font-size:13px;color:#52606d">{{.Outro}}</p>
</body></html>
`))

// ComposePasswordReset construye el correo de un enlace de restablecimiento.
func ComposePasswordReset(d app.PasswordResetDelivery) (mailer.Message, error) {
	subject := "Restablecer la contraseña de Horus Flow"
	exp := d.ExpiresAt.UTC()
	expires := exp.Format("02/01/2006") + " a las " + exp.Format("15:04") + " (UTC)"
	intro := "Recibimos una solicitud para restablecer la contraseña de su cuenta de Horus Flow. Para elegir una contraseña nueva abra este enlace:"
	outro := "Si usted no pidió restablecer la contraseña, ignore este correo: su contraseña actual sigue siendo válida."
	if d.RequestedByAdmin {
		intro = "Un administrador generó un enlace para que elija una contraseña nueva en Horus Flow. Para hacerlo abra este enlace:"
		outro = "Si no esperaba este correo, avise a su administrador."
	}
	name := strings.Join(strings.Fields(d.DisplayName), " ")
	greet := "Hola:"
	if name != "" {
		greet = "Hola, " + name + ":"
	}
	text := greet + "\n\n" + intro + "\n\n" + d.URL + "\n\n" +
		"El enlace es personal, sirve una sola vez y caduca el " + expires + ". Al usarlo se cierran todas las sesiones abiertas de la cuenta.\n\n" +
		outro + "\n"
	var b bytes.Buffer
	if err := resetHTMLTmpl.Execute(&b, map[string]any{"Subject": subject, "Name": name, "Intro": intro, "Outro": outro, "URL": d.URL, "Expires": expires}); err != nil {
		return mailer.Message{}, err
	}
	return mailer.Message{To: []string{d.Email}, Subject: subject, Text: text, HTML: b.String()}, nil
}

// SendPasswordReset envía el correo del enlace de restablecimiento; el error
// no incluye la dirección, el token ni el enlace.
func (s *Sender) SendPasswordReset(ctx context.Context, d app.PasswordResetDelivery) error {
	m, err := ComposePasswordReset(d)
	if err != nil {
		return &sendError{phase: "compose"}
	}
	if err := mailer.Send(ctx, s.cfg, m); err != nil {
		return &sendError{phase: phase(err)}
	}
	return nil
}
