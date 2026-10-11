// Package mailertest es un servidor SMTP mínimo en memoria para tests
// (EHLO, AUTH PLAIN opcional, MAIL, RCPT, DATA, QUIT; sin TLS).
package mailertest

import (
	"bufio"
	"encoding/base64"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Mail es un correo recibido.
type Mail struct {
	From string
	To   []string
	Data string
}

// Options configura el servidor antes de que empiece a aceptar conexiones.
type Options struct {
	// RejectRcpt responde 550 a RCPT TO.
	RejectRcpt bool
	// User y Password, si no están vacíos, anuncian AUTH PLAIN y exigen esas
	// credenciales.
	User, Password string
}

// Server es el servidor SMTP de prueba.
type Server struct {
	// Host y Port de escucha (127.0.0.1).
	Host string
	Port int

	o    Options
	ln   net.Listener
	mu   sync.Mutex
	mail []Mail
	wg   sync.WaitGroup
}

// New arranca un servidor en un puerto libre; se cierra al terminar el test.
// La configuración se fija aquí: cambiarla con el servidor ya escuchando
// sería una carrera con las sesiones.
func New(t testing.TB, opts ...Options) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().(*net.TCPAddr) //nolint:forcetypeassert // tcp
	s := &Server{Host: "127.0.0.1", Port: addr.Port, ln: ln}
	if len(opts) > 0 {
		s.o = opts[0]
	}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(func() {
		_ = ln.Close()
		s.wg.Wait()
	})
	return s
}

// Addr es host:port.
func (s *Server) Addr() string { return net.JoinHostPort(s.Host, strconv.Itoa(s.Port)) }

// Mails devuelve los correos recibidos.
func (s *Server) Mails() []Mail {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Mail(nil), s.mail...)
}

func (s *Server) serve() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.session(c)
		}()
	}
}

func (s *Server) session(c net.Conn) {
	defer func() { _ = c.Close() }()
	r := bufio.NewReader(c)
	say := func(line string) { _, _ = c.Write([]byte(line + "\r\n")) }
	say("220 mailertest ESMTP")
	var cur Mail
	authed := s.o.User == ""
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			if s.o.User != "" {
				say("250-mailertest")
				say("250 AUTH PLAIN")
			} else {
				say("250 mailertest")
			}
		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):]))
			parts := strings.Split(string(raw), "\x00")
			if len(parts) == 3 && parts[1] == s.o.User && parts[2] == s.o.Password {
				authed = true
				say("235 ok")
			} else {
				say("535 credenciales")
			}
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			if !authed {
				say("530 auth required")
				continue
			}
			cur = Mail{From: strings.Trim(line[len("MAIL FROM:"):], "<> ")}
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			if s.o.RejectRcpt {
				say("550 mailbox unavailable")
				continue
			}
			cur.To = append(cur.To, strings.Trim(line[len("RCPT TO:"):], "<> "))
			say("250 ok")
		case cmd == "DATA":
			say("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(strings.TrimPrefix(l, "."))
			}
			cur.Data = b.String()
			s.mu.Lock()
			s.mail = append(s.mail, cur)
			s.mu.Unlock()
			say("250 queued")
		case cmd == "RSET", cmd == "NOOP":
			say("250 ok")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("502 not implemented")
		}
	}
}
