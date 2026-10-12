package remote

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// errHostKeyChanged: la clave del servidor no es la confirmada (posible
// suplantación; nunca se acepta sola).
var errHostKeyChanged = errors.New("la clave del servidor cambió")

// errHostKeyUnconfirmed: primera conexión, falta confirmar la huella.
var errHostKeyUnconfirmed = errors.New("confirme la huella de la clave del servidor para continuar")

type (
	netConn = net.Conn
	sshAuth = ssh.AuthMethod
)

// Fingerprint es la huella OpenSSH SHA256:… de una clave pública.
func Fingerprint(k ssh.PublicKey) string { return ssh.FingerprintSHA256(k) }

// hostKeyPolicy decide sobre la clave que presenta el servidor (TOFU):
//   - con huella confirmada (pinned): solo esa;
//   - sin huella: solo si la persona confirma exactamente la presentada.
type hostKeyPolicy struct {
	pinned  string
	confirm string
	// Lo observado durante el handshake.
	presented string
	accepted  bool
}

func (p *hostKeyPolicy) callback(_ string, _ net.Addr, key ssh.PublicKey) error {
	p.presented = Fingerprint(key)
	switch {
	case p.pinned != "" && p.presented != p.pinned:
		return errHostKeyChanged
	case p.pinned == "" && (p.confirm == "" || p.confirm != p.presented):
		return errHostKeyUnconfirmed
	}
	p.accepted = true
	return nil
}

// dialOptions configura una conexión.
type dialOptions struct {
	Host    string
	Port    int
	User    string
	Auth    []ssh.AuthMethod
	Policy  *hostKeyPolicy
	Timeout time.Duration
	// Dial sustituye a net.Dialer (tests).
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// session es una conexión SSH con su cliente SFTP.
type session struct {
	conn net.Conn
	ssh  *ssh.Client
	sftp *sftp.Client
	once sync.Once
	done chan struct{} // cerrado al cerrar la sesión: deja de vigilar el contexto
}

// Close cierra SFTP, SSH y la conexión (idempotente).
func (s *session) Close() {
	s.once.Do(func() {
		if s.done != nil {
			close(s.done)
		}
		if s.sftp != nil {
			_ = s.sftp.Close()
		}
		if s.ssh != nil {
			_ = s.ssh.Close()
		}
		if s.conn != nil {
			_ = s.conn.Close()
		}
	})
}

// dialError lleva la etapa en la que falló la conexión.
type dialError struct {
	Stage string
	Err   error
}

func (e *dialError) Error() string { return e.Err.Error() }
func (e *dialError) Unwrap() error { return e.Err }

// dial conecta, verifica la clave del servidor, autentica y abre SFTP. El
// handshake y la autenticación tienen el plazo Timeout; después la conexión
// no tiene plazo (subidas largas) y se cierra al cancelar ctx.
func dial(ctx context.Context, o dialOptions) (*session, error) {
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	dctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	d := o.Dial
	if d == nil {
		nd := &net.Dialer{Timeout: o.Timeout, KeepAlive: 30 * time.Second}
		d = nd.DialContext
	}
	addr := net.JoinHostPort(o.Host, strconv.Itoa(o.Port))
	conn, err := d(dctx, "tcp", addr)
	if err != nil {
		return nil, &dialError{Stage: StageConnect, Err: fmt.Errorf("no se pudo conectar con %s: %s", addr, netReason(err))}
	}
	_ = conn.SetDeadline(time.Now().Add(o.Timeout))
	cfg := &ssh.ClientConfig{User: o.User, Auth: o.Auth, HostKeyCallback: o.Policy.callback, Timeout: o.Timeout}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		switch {
		case errors.Is(err, errHostKeyChanged):
			return nil, &dialError{Stage: StageHostKey, Err: errHostKeyChanged}
		case errors.Is(err, errHostKeyUnconfirmed):
			return nil, &dialError{Stage: StageHostKey, Err: errHostKeyUnconfirmed}
		case o.Policy.accepted:
			return nil, &dialError{Stage: StageAuth, Err: authReason(err, len(o.Auth))}
		default:
			return nil, &dialError{Stage: StageConnect, Err: fmt.Errorf("el servidor no completó el saludo SSH: %s", netReason(err))}
		}
	}
	client := ssh.NewClient(c, chans, reqs)
	sc, err := sftp.NewClient(client, sftp.UseConcurrentWrites(true), sftp.MaxConcurrentRequestsPerFile(16))
	if err != nil {
		_ = client.Close()
		_ = conn.Close()
		return nil, &dialError{Stage: StageAuth, Err: errors.New("el servidor no ofrece el subsistema SFTP")}
	}
	_ = conn.SetDeadline(time.Time{})
	s := &session{conn: conn, ssh: client, sftp: sc, done: make(chan struct{})}
	// Cancelar ctx corta una subida en curso (apagado).
	go func() {
		select {
		case <-ctx.Done():
			s.Close()
		case <-s.done:
		}
	}()
	return s, nil
}

func netReason(err error) string {
	var ne net.Error
	switch {
	case errors.As(err, &ne) && ne.Timeout(), errors.Is(err, context.DeadlineExceeded):
		return "tiempo de espera agotado"
	case strings.Contains(err.Error(), "connection refused"):
		return "conexión rechazada"
	case strings.Contains(err.Error(), "no such host"):
		return "el nombre no resuelve"
	case strings.Contains(err.Error(), "network is unreachable"), strings.Contains(err.Error(), "no route to host"):
		return "red inalcanzable"
	case errors.Is(err, io.EOF):
		return "el servidor cerró la conexión"
	}
	return "error de red"
}

func authReason(err error, methods int) error {
	if methods == 0 {
		return errors.New("sin credenciales: póngalas con PUT .../credentials")
	}
	if strings.Contains(err.Error(), "unable to authenticate") {
		return errors.New("el servidor rechazó el usuario o las credenciales")
	}
	return fmt.Errorf("fallo de autenticación: %s", netReason(err))
}

// display es el texto de un error para la persona (primera letra en mayúscula).
func display(err error) string {
	m := err.Error()
	r, n := utf8.DecodeRuneInString(m)
	if n == 0 {
		return m
	}
	return string(unicode.ToUpper(r)) + m[n:]
}

func randomSuffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// probeWrite crea la ruta (mkdir -p) y escribe y borra un archivo de prueba.
func (s *session) probeWrite(dir string) error {
	if err := s.sftp.MkdirAll(dir); err != nil {
		return fmt.Errorf("no se pudo crear la carpeta %s: %s", dir, sftpReason(err))
	}
	name := path.Join(dir, ".horus-test-"+randomSuffix())
	f, err := s.sftp.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return fmt.Errorf("no se pudo escribir en %s: %s", dir, sftpReason(err))
	}
	_, werr := f.Write([]byte("horus remote destination test\n"))
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = s.sftp.Remove(name)
		return fmt.Errorf("no se pudo escribir en %s", dir)
	}
	if err := s.sftp.Remove(name); err != nil {
		return fmt.Errorf("se escribió pero no se pudo borrar el archivo de prueba en %s: %s", dir, sftpReason(err))
	}
	return nil
}

func sftpReason(err error) string {
	var se *sftp.StatusError
	switch {
	case errors.Is(err, os.ErrPermission):
		return "permiso denegado"
	case errors.Is(err, os.ErrNotExist):
		return "no existe"
	case errors.As(err, &se):
		if msg := strings.TrimSpace(se.Error()); msg != "" && len(msg) < 200 {
			return msg
		}
	}
	return "error SFTP"
}

// upload cifra src con enc y lo deja en name de forma atómica: escribe
// name.partial (determinista: un reinicio lo sobrescribe) y lo renombra.
func (s *session) upload(name string, write func(io.Writer) error) error {
	if err := s.sftp.MkdirAll(path.Dir(name)); err != nil {
		return fmt.Errorf("no se pudo crear %s: %s", path.Dir(name), sftpReason(err))
	}
	tmp := name + ".partial"
	f, err := s.sftp.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("no se pudo crear %s: %s", tmp, sftpReason(err))
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	if err := write(bw); err != nil {
		_ = f.Close()
		_ = s.sftp.Remove(tmp)
		return err
	}
	if err := bw.Flush(); err != nil {
		_ = f.Close()
		_ = s.sftp.Remove(tmp)
		return fmt.Errorf("error al subir %s: %s", name, sftpReason(err))
	}
	if err := f.Close(); err != nil {
		_ = s.sftp.Remove(tmp)
		return fmt.Errorf("error al cerrar %s: %s", tmp, sftpReason(err))
	}
	if err := s.sftp.PosixRename(tmp, name); err != nil {
		// Sin la extensión posix-rename: SFTP v3 no sobrescribe al renombrar.
		_ = s.sftp.Remove(name)
		if err := s.sftp.Rename(tmp, name); err != nil {
			return fmt.Errorf("no se pudo renombrar %s: %s", tmp, sftpReason(err))
		}
	}
	return nil
}

// remove borra un archivo remoto (no existir no es error).
func (s *session) remove(name string) error {
	if err := s.sftp.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no se pudo borrar %s: %s", name, sftpReason(err))
	}
	return nil
}
