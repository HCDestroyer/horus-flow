package domain

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

// Sealer implementa envelope encryption (security.md §8.2): una DEK
// aleatoria por secreto cifra el secreto con AES-256-GCM y AAD; la DEK se
// envuelve con la KEK del módulo (archivo fuera de la BD).
type Sealer struct {
	kek   []byte
	kekID string
}

// NewSealer crea un sellador con una KEK de 32 B.
func NewSealer(kek []byte) (*Sealer, error) {
	if len(kek) != 32 {
		return nil, errors.New("auth: KEK must be 32 bytes")
	}
	sum := sha256.Sum256(kek)
	return &Sealer{kek: kek, kekID: fmt.Sprintf("kek-%x", sum[:6])}, nil
}

// KEKID identifica la KEK (para rotación).
func (s *Sealer) KEKID() string { return s.kekID }

func gcmSeal(key, plaintext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err //nolint:wrapcheck // se envuelve en el llamador
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err //nolint:wrapcheck // se envuelve en el llamador
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err //nolint:wrapcheck // se envuelve en el llamador
	}
	return g.Seal(nonce, nonce, plaintext, aad), nil
}

func gcmOpen(key, sealed, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err //nolint:wrapcheck // se envuelve en el llamador
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err //nolint:wrapcheck // se envuelve en el llamador
	}
	if len(sealed) < g.NonceSize() {
		return nil, errors.New("short ciphertext")
	}
	return g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], aad) //nolint:wrapcheck // se envuelve en el llamador
}

// Seal cifra plaintext ligado a aad (p. ej. "totp:<user_id>").
func (s *Sealer) Seal(plaintext, aad []byte) (ciphertext, dekWrapped []byte, err error) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return nil, nil, fmt.Errorf("auth: dek: %w", err)
	}
	if ciphertext, err = gcmSeal(dek, plaintext, aad); err != nil {
		return nil, nil, fmt.Errorf("auth: seal: %w", err)
	}
	if dekWrapped, err = gcmSeal(s.kek, dek, []byte(s.kekID)); err != nil {
		return nil, nil, fmt.Errorf("auth: wrap dek: %w", err)
	}
	return ciphertext, dekWrapped, nil
}

// Open descifra un secreto sellado con Seal.
func (s *Sealer) Open(ciphertext, dekWrapped, aad []byte) ([]byte, error) {
	dek, err := gcmOpen(s.kek, dekWrapped, []byte(s.kekID))
	if err != nil {
		return nil, fmt.Errorf("auth: unwrap dek: %w", err)
	}
	pt, err := gcmOpen(dek, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("auth: open: %w", err)
	}
	return pt, nil
}

// MAC firma datos con una clave derivada de la KEK para un propósito
// (p. ej. el mfa_token de un uso).
func (s *Sealer) MAC(purpose string, data []byte) []byte {
	k := hmac.New(sha256.New, s.kek)
	k.Write([]byte("horus-auth:" + purpose))
	m := hmac.New(sha256.New, k.Sum(nil))
	m.Write(data)
	return m.Sum(nil)
}
