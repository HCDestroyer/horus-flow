package app

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// memKioskStore simula auth.kiosk y auth.kiosk_credential_rotated (sobrevive
// a "reinicios": varios Kiosks comparten la misma instancia).
type memKioskStore struct {
	KioskStore
	k       domain.Kiosk
	rotated map[string]RotatedCredential
}

func (m *memKioskStore) KioskByCredential(_ context.Context, hash []byte) (*domain.Kiosk, *RotatedCredential, error) {
	k := m.k
	if bytes.Equal(hash, m.k.CredentialHash) {
		return &k, nil, nil
	}
	if r, ok := m.rotated[string(hash)]; ok {
		return &k, &r, nil
	}
	return nil, nil, domain.ErrNotFound
}

func (m *memKioskStore) RotateKioskCredential(_ context.Context, _ *domain.Kiosk, oldHash, newHash []byte, _ string, now time.Time, boot uuid.UUID) (bool, error) {
	if !bytes.Equal(oldHash, m.k.CredentialHash) {
		return false, nil
	}
	m.k.CredentialHash = newHash
	b := boot
	m.rotated[string(oldHash)] = RotatedCredential{SuccessorHash: newHash, RotatedBy: &b, RotatedAt: now}
	return true, nil
}

func (m *memKioskStore) UpdateKiosk(_ context.Context, _ pgdb.TenantID, k *domain.Kiosk, _ int, _ KioskEvents) error {
	m.k = *k
	return nil
}

func newKioskEnv(t *testing.T) (*memKioskStore, *authz.Signer, string) {
	t.Helper()
	key, err := authz.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cred := domain.NewKioskCredential()
	st := &memKioskStore{rotated: map[string]RotatedCredential{}, k: domain.Kiosk{ID: uuid.New(), TenantID: uuid.New(), Status: domain.KioskActive,
		CredentialHash: domain.HashSecret(cred), ExpiresAt: time.Now().Add(24 * time.Hour)}}
	return st, authz.NewSigner(key, "horus-auth", 0, nil), cred
}

// D23: si el proceso muere tras confirmar la rotación y antes de entregar la
// cookie nueva, el kiosco vuelve con la anterior y el arranque siguiente
// reanuda la rotación en vez de revocarlo; la reutilización real (misma
// credencial dos veces en un arranque, o con la sucesora ya usada) revoca.
func TestKioskRotationSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	st, signer, cred0 := newKioskEnv(t)
	boot1 := NewKiosks(st, signer, nil, nil, "", nil, nil)
	if _, err := boot1.Token(ctx, cred0, "127.0.0.1"); err != nil { // la respuesta (cred1) "se pierde": kill -9
		t.Fatal(err)
	}
	boot2 := NewKiosks(st, signer, nil, nil, "", nil, nil)
	tok, err := boot2.Token(ctx, cred0, "127.0.0.1")
	if err != nil {
		t.Fatalf("kiosk revoked after a restart lost its rotated cookie: %v", err)
	}
	if st.k.Status != domain.KioskActive {
		t.Fatal("kiosk revoked")
	}
	// La credencial reanudada funciona y rota de nuevo.
	if _, err := boot2.Token(ctx, tok.Credential, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	// cred0 otra vez: su sucesora ya no es la vigente → reutilización → revoca.
	if _, err := boot2.Token(ctx, cred0, "127.0.0.1"); !errors.Is(err, errKioskCred) {
		t.Fatalf("reuse accepted: %v", err)
	}
	if st.k.Status != domain.KioskRevoked {
		t.Fatal("reuse did not revoke")
	}
}

func TestKioskReuseInSameBootRevokes(t *testing.T) {
	ctx := context.Background()
	st, signer, cred0 := newKioskEnv(t)
	k := NewKiosks(st, signer, nil, nil, "", nil, nil)
	if _, err := k.Token(ctx, cred0, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Token(ctx, cred0, "127.0.0.1"); !errors.Is(err, errKioskCred) || st.k.Status != domain.KioskRevoked {
		t.Fatalf("same-boot reuse: err=%v status=%s", err, st.k.Status)
	}
}

func TestKioskResumeExpiresAfterGrace(t *testing.T) {
	ctx := context.Background()
	st, signer, cred0 := newKioskEnv(t)
	past := time.Now().Add(-KioskRotationGrace - time.Minute)
	boot1 := NewKiosks(st, signer, nil, nil, "", func() time.Time { return past }, nil)
	if _, err := boot1.Token(ctx, cred0, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	boot2 := NewKiosks(st, signer, nil, nil, "", nil, nil)
	if _, err := boot2.Token(ctx, cred0, "127.0.0.1"); !errors.Is(err, errKioskCred) {
		t.Fatalf("resume after grace: %v", err)
	}
}
