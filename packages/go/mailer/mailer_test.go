package mailer_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/mailer"
	"github.com/hcdestroyer/horus-flow/packages/go/mailer/mailertest"
)

func cfg(s *mailertest.Server) mailer.Config {
	return mailer.Config{Host: s.Host, Port: s.Port, From: "horus@isp.test", TLS: mailer.TLSNone}
}

func TestSendPlainText(t *testing.T) {
	srv := mailertest.New(t)
	err := mailer.Send(context.Background(), cfg(srv), mailer.Message{To: []string{"noc@isp.test"}, Subject: "[Horus] Túnel caído", Text: "línea 1\nlínea 2"})
	if err != nil {
		t.Fatal(err)
	}
	ms := srv.Mails()
	if len(ms) != 1 || ms[0].From != "horus@isp.test" || len(ms[0].To) != 1 || ms[0].To[0] != "noc@isp.test" {
		t.Fatalf("correos = %+v", ms)
	}
	d := ms[0].Data
	for _, want := range []string{"Subject: =?UTF-8?B?W0hvcnVzXSBUw7puZWwgY2HDrWRv?=\r\n", "Content-Type: text/plain; charset=UTF-8\r\n",
		"\r\n\r\nlínea 1\r\nlínea 2\r\n"} {
		if !strings.Contains(d, want) {
			t.Fatalf("falta %q en %q", want, d)
		}
	}
	if strings.Contains(d, "\r\r") {
		t.Fatalf("CR sueltos: %q", d)
	}
}

func TestSendMultipartAndAuth(t *testing.T) {
	srv := mailertest.New(t)
	srv.User, srv.Password = "horus", "s3creto"
	c := cfg(srv)
	c.Username, c.Password = "horus", "mala"
	err := mailer.Send(context.Background(), c, mailer.Message{To: []string{"a@isp.test"}, Subject: "x", Text: "t"})
	if !errors.Is(err, mailer.ErrAuth) || !strings.Contains(err.Error(), "auth:") {
		t.Fatalf("credenciales malas = %v", err)
	}
	c.Password = "s3creto"
	if err := mailer.Send(context.Background(), c, mailer.Message{To: []string{"a@isp.test"}, Subject: "x", Text: "texto", HTML: "<p>html</p>"}); err != nil {
		t.Fatal(err)
	}
	d := srv.Mails()[0].Data
	for _, want := range []string{"multipart/alternative; boundary=", "Content-Type: text/plain; charset=UTF-8", "texto",
		"Content-Type: text/html; charset=UTF-8", "<p>html</p>"} {
		if !strings.Contains(d, want) {
			t.Fatalf("falta %q en %q", want, d)
		}
	}
}

func TestSendErrors(t *testing.T) {
	srv := mailertest.New(t)
	srv.RejectRcpt = true
	if err := mailer.Send(context.Background(), cfg(srv), mailer.Message{To: []string{"a@isp.test"}, Text: "t"}); err == nil || !strings.Contains(err.Error(), "RCPT") {
		t.Fatalf("RCPT rechazado = %v", err)
	}
	if err := mailer.Send(context.Background(), cfg(srv), mailer.Message{To: []string{"a@isp.test\r\nBcc: x@y"}, Text: "t"}); err == nil {
		t.Fatal("inyección de cabeceras aceptada")
	}
	if err := mailer.Send(context.Background(), mailer.Config{}, mailer.Message{To: []string{"a@isp.test"}}); !errors.Is(err, mailer.ErrNotConfigured) {
		t.Fatalf("sin host = %v", err)
	}
	// Servidor que no responde: el plazo del contexto manda.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // acepta y calla
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	err = mailer.Send(ctx, mailer.Config{Host: "127.0.0.1", Port: port, TLS: mailer.TLSNone}, mailer.Message{To: []string{"a@isp.test"}})
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("sin respuesta = %v tras %v", err, time.Since(start))
	}
}

func TestValidTLS(t *testing.T) {
	for _, v := range []string{"", "starttls", "tls", "none"} {
		if !mailer.ValidTLS(v) {
			t.Fatal(v)
		}
	}
	if mailer.ValidTLS("ssl") {
		t.Fatal("ssl aceptado")
	}
}
