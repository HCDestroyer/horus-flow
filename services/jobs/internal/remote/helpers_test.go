package remote

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

// memRepo es un Repo en memoria con la misma semántica que Store.
type memRepo struct {
	mu     sync.Mutex
	inst   uuid.UUID
	dests  map[uuid.UUID]*Destination
	ledger map[uuid.UUID]map[string]LedgerEntry
}

func newMemRepo() *memRepo {
	return &memRepo{inst: uuid.Must(uuid.NewV7()), dests: map[uuid.UUID]*Destination{}, ledger: map[uuid.UUID]map[string]LedgerEntry{}}
}

func (m *memRepo) InstallationID(context.Context) (uuid.UUID, error) { return m.inst, nil }

func (m *memRepo) List(context.Context) ([]Destination, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Destination
	for _, d := range m.dests {
		out = append(out, *d)
	}
	return out, nil
}

func (m *memRepo) Get(_ context.Context, id uuid.UUID) (*Destination, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.dests[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := *d
	return &c, nil
}

func (m *memRepo) Create(_ context.Context, d *Destination) (*Destination, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.dests {
		if bytes.EqualFold([]byte(x.Name), []byte(d.Name)) {
			return nil, ErrNameInUse
		}
	}
	c := *d
	c.Version, c.CreatedAt, c.UpdatedAt = 1, time.Now(), time.Now()
	m.dests[d.ID] = &c
	out := c
	return &out, nil
}

func (m *memRepo) Update(_ context.Context, d *Destination, version int, reset bool) (*Destination, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.dests[d.ID]
	if !ok {
		return nil, ErrNotFound
	}
	if cur.Version != version {
		return nil, ErrVersionChanged
	}
	c := *cur
	c.Name, c.Host, c.Port, c.Username, c.Path, c.RetentionDays = d.Name, d.Host, d.Port, d.Username, d.Path, d.RetentionDays
	c.Paths = d.Paths
	c.Enabled, c.HostKeyFingerprint, c.VerifiedAt = d.Enabled, d.HostKeyFingerprint, d.VerifiedAt
	c.Version++
	m.dests[d.ID] = &c
	if reset {
		delete(m.ledger, d.ID)
	}
	out := c
	return &out, nil
}

func (m *memRepo) Delete(_ context.Context, id uuid.UUID, version int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.dests[id]
	if !ok {
		return ErrNotFound
	}
	if cur.Version != version {
		return ErrVersionChanged
	}
	delete(m.dests, id)
	delete(m.ledger, id)
	return nil
}

func (m *memRepo) SetCredentials(_ context.Context, id uuid.UUID, ct, dek []byte, kekID, kind string) (*Destination, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.dests[id]
	if !ok {
		return nil, ErrNotFound
	}
	cur.CredentialCiphertext, cur.CredentialDEK, cur.CredentialKEKID, cur.CredentialKind = ct, dek, kekID, kind
	cur.VerifiedAt = nil
	cur.Version++
	out := *cur
	return &out, nil
}

func (m *memRepo) RecordTest(_ context.Context, id uuid.UUID, pin *string, verified bool) (*Destination, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.dests[id]
	if !ok {
		return nil, ErrNotFound
	}
	if pin != nil && cur.HostKeyFingerprint == nil {
		p := *pin
		cur.HostKeyFingerprint = &p
		cur.Version++
	}
	if verified {
		now := time.Now()
		cur.VerifiedAt = &now
	}
	out := *cur
	return &out, nil
}

func (m *memRepo) RecordAttempt(_ context.Context, id uuid.UUID, at time.Time, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.dests[id]
	if !ok {
		return nil
	}
	cur.LastAttemptAt = &at
	if errMsg == "" {
		cur.LastSuccessAt, cur.LastError = &at, nil
	} else {
		e := errMsg
		cur.LastError = &e
	}
	return nil
}

func (m *memRepo) Ledger(_ context.Context, id uuid.UUID) (map[string]LedgerEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]LedgerEntry{}
	for k, v := range m.ledger[id] {
		out[k] = v
	}
	return out, nil
}

func (m *memRepo) RecordCopy(_ context.Context, id uuid.UUID, version int, e LedgerEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.dests[id]
	if !ok || cur.Version != version {
		return ErrVersionChanged
	}
	if m.ledger[id] == nil {
		m.ledger[id] = map[string]LedgerEntry{}
	}
	m.ledger[id][e.Rel] = e
	return nil
}

func (m *memRepo) ForgetCopy(_ context.Context, id uuid.UUID, rel string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.ledger[id], rel)
	return nil
}

// fakeAudit guarda las entradas de auditoría.
type fakeAudit struct {
	mu      sync.Mutex
	entries []authapi.AuditEntry
}

func (f *fakeAudit) Record(_ context.Context, e authapi.AuditEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, e)
	return nil
}

func (f *fakeAudit) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.entries {
		out = append(out, e.Action)
	}
	return out
}

// sftpServer es un servidor SSH+SFTP en proceso sobre el disco local.
type sftpServer struct {
	addr     string
	host     string
	port     int
	signer   ssh.Signer
	user     string
	password string
	authKey  ssh.PublicKey
	ln       net.Listener
	wg       sync.WaitGroup
}

func newHostSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// clientKeyPEM genera una clave de cliente OpenSSH (cifrada si passphrase).
func clientKeyPEM(t *testing.T, passphrase string) (string, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var blk *pem.Block
	if passphrase == "" {
		blk, err = ssh.MarshalPrivateKey(priv, "test")
	} else {
		blk, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "test", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(blk)), sp
}

func startSFTP(t *testing.T, signer ssh.Signer, user, password string, authKey ssh.PublicKey) *sftpServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &sftpServer{addr: ln.Addr().String(), signer: signer, user: user, password: password, authKey: authKey, ln: ln}
	s.host = "127.0.0.1"
	s.port = ln.Addr().(*net.TCPAddr).Port
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if c.User() == s.user && s.password != "" && string(pw) == s.password {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() == s.user && s.authKey != nil && bytes.Equal(k.Marshal(), s.authKey.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
	}
	cfg.AddHostKey(signer)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c, cfg)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		s.wg.Wait()
	})
	return s
}

func (s *sftpServer) serve(c net.Conn, cfg *ssh.ServerConfig) {
	defer func() { _ = c.Close() }()
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "only session")
			continue
		}
		ch, in, err := nc.Accept()
		if err != nil {
			return
		}
		go func() {
			for req := range in {
				ok := req.Type == "subsystem" && len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp"
				_ = req.Reply(ok, nil)
				if ok {
					srv, err := sftp.NewServer(ch)
					if err != nil {
						return
					}
					_ = srv.Serve()
					_ = ch.Close()
					return
				}
			}
		}()
	}
}

func testIdentity(t *testing.T) (*age.X25519Identity, string) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id, id.String()
}

// newTestService crea un Service con repositorio en memoria.
func newTestService(t *testing.T, root string, recipient age.Recipient) (*Service, *memRepo, *fakeAudit) {
	t.Helper()
	repo := newMemRepo()
	audit := &fakeAudit{}
	svc, err := NewService(Options{Repo: repo, Sealer: envelope.Ephemeral(), Recipient: recipient, RecipientMissing: "sin clave",
		Root: root, Settle: 0, TestTimeout: 5 * time.Second, Logger: slog.New(slog.DiscardHandler),
		Audit: func() (authapi.AuditRecorder, bool) { return audit, true }})
	if err != nil {
		t.Fatal(err)
	}
	return svc, repo, audit
}
