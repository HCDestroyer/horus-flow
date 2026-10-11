package invitemail

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/mailer"
	"github.com/hcdestroyer/horus-flow/packages/go/mailer/mailertest"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
)

const token = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"

func delivery() app.InvitationDelivery {
	return app.InvitationDelivery{Email: "nueva@isp.test", TenantID: uuid.New(), TenantName: "ISP <Norte>\r\nBcc: x@y", Token: token,
		URL: "https://horus.example/invitation#token=" + token, ExpiresAt: time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)}
}

func TestSendInvitation(t *testing.T) {
	srv := mailertest.New(t)
	s := New(mailer.Config{Host: srv.Host, Port: srv.Port, From: "Horus <horus@isp.test>", TLS: mailer.TLSNone})
	if err := s.SendInvitation(context.Background(), delivery()); err != nil {
		t.Fatal(err)
	}
	ms := srv.Mails()
	if len(ms) != 1 || len(ms[0].To) != 1 || ms[0].To[0] != "nueva@isp.test" {
		t.Fatalf("correos = %+v", ms)
	}
	d := ms[0].Data
	subject := "Invitación a ISP <Norte> Bcc: x@y en Horus Flow"
	if !strings.Contains(d, "Subject: =?UTF-8?B?"+base64.StdEncoding.EncodeToString([]byte(subject))+"?=\r\n") {
		t.Fatalf("asunto: %q", d)
	}
	if strings.Contains(d, "\r\nBcc:") {
		t.Fatal("inyección de cabecera por el nombre del ISP")
	}
	for _, want := range []string{
		"multipart/alternative",
		"Le han invitado a unirse a ISP <Norte> Bcc: x@y en Horus Flow.", // texto
		"<strong>ISP &lt;Norte&gt; Bcc: x@y</strong>",                    // HTML escapado
		"https://horus.example/invitation#token=" + token + "\r\n",       // enlace en el texto
		`href="https://horus.example/invitation#token=` + token + `"`,    // enlace en el HTML
		"caduca el 14/10/2026 a las 09:30 (UTC)",
	} {
		if !strings.Contains(d, want) {
			t.Fatalf("falta %q en %q", want, d)
		}
	}
}

// Un fallo del SMTP devuelve un error sin la dirección, el token ni el enlace.
func TestSendInvitationFailureRedacted(t *testing.T) {
	srv := mailertest.New(t)
	srv.RejectRcpt = true
	s := New(mailer.Config{Host: srv.Host, Port: srv.Port, TLS: mailer.TLSNone})
	err := s.SendInvitation(context.Background(), delivery())
	if err == nil || err.Error() != "invitation email: RCPT failed" {
		t.Fatalf("err = %v", err)
	}
	// Servidor inaccesible.
	err = New(mailer.Config{Host: "127.0.0.1", Port: 1, TLS: mailer.TLSNone}).SendInvitation(context.Background(), delivery())
	if err == nil || strings.Contains(err.Error(), "nueva@isp.test") || strings.Contains(err.Error(), token) {
		t.Fatalf("err = %v", err)
	}
}

func TestComposeWithoutTenantName(t *testing.T) {
	d := delivery()
	d.TenantName = "  "
	m, err := Compose(d)
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Invitación a su ISP en Horus Flow" || !strings.Contains(m.Text, d.URL) || !strings.Contains(m.HTML, "lang=\"es\"") {
		t.Fatalf("m = %+v", m)
	}
}
