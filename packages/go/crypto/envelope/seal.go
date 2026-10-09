// Package envelope implementa la envelope encryption de los secretos de los
// módulos (docs/security.md S9 y §8.2): una DEK aleatoria por secreto cifra
// el secreto con AES-256-GCM y un AAD que lo liga a su dueño (p. ej.
// "routeros_api:<tenant>:<router>"); la DEK se envuelve con la KEK del módulo,
// que vive fuera de la base (archivo/Docker secret). Copiar un secreto a otra
// fila o a otro tenant no descifra (el AAD no coincide); borrar la DEK
// envuelta es crypto-shredding.
package envelope

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// KeySize es el tamaño de la KEK y de cada DEK.
const KeySize = 32

// ErrOpen indica que un secreto no se pudo descifrar (KEK, AAD o datos
// distintos). No distingue el motivo.
var ErrOpen = errors.New("envelope: cannot open secret")

// Sealer cifra y descifra secretos con una KEK.
type Sealer struct {
	kek   []byte
	kekID string
}

// New crea un Sealer con una KEK de 32 B.
func New(kek []byte) (*Sealer, error) {
	if len(kek) != KeySize {
		return nil, fmt.Errorf("envelope: KEK must be %d bytes, got %d", KeySize, len(kek))
	}
	sum := sha256.Sum256(kek)
	k := make([]byte, KeySize)
	copy(k, kek)
	return &Sealer{kek: k, kekID: fmt.Sprintf("kek-%x", sum[:6])}, nil
}

// Ephemeral crea un Sealer con una KEK aleatoria (solo desarrollo y tests:
// lo sellado deja de poder abrirse al reiniciar).
func Ephemeral() *Sealer {
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		panic(err) //nolint:forbidigo // crypto/rand no falla en plataformas soportadas
	}
	s, _ := New(k)
	return s
}

// ParseKEK interpreta una KEK de 32 B en hexadecimal (64 caracteres) o base64.
func ParseKEK(v string) ([]byte, error) {
	v = strings.TrimSpace(v)
	if b, err := hex.DecodeString(v); err == nil && len(b) == KeySize {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(v); err == nil && len(b) == KeySize {
			return b, nil
		}
	}
	return nil, fmt.Errorf("envelope: KEK must hold %d bytes (hex or base64)", KeySize)
}

// KEKID identifica la KEK (columna kek_id, para rotación).
func (s *Sealer) KEKID() string { return s.kekID }

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("envelope: %w", err)
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("envelope: %w", err)
	}
	return g, nil
}

func gcmSeal(key, plaintext, aad []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("envelope: nonce: %w", err)
	}
	return g.Seal(nonce, nonce, plaintext, aad), nil
}

func gcmOpen(key, sealed, aad []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < g.NonceSize()+g.Overhead() {
		return nil, ErrOpen
	}
	pt, err := g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], aad)
	if err != nil {
		return nil, ErrOpen
	}
	return pt, nil
}

// Seal cifra plaintext ligado a aad. Devuelve el texto cifrado y la DEK
// envuelta con la KEK.
func (s *Sealer) Seal(plaintext, aad []byte) (ciphertext, dekWrapped []byte, err error) {
	dek := make([]byte, KeySize)
	if _, err := rand.Read(dek); err != nil {
		return nil, nil, fmt.Errorf("envelope: dek: %w", err)
	}
	if ciphertext, err = gcmSeal(dek, plaintext, aad); err != nil {
		return nil, nil, err
	}
	if dekWrapped, err = gcmSeal(s.kek, dek, []byte(s.kekID)); err != nil {
		return nil, nil, err
	}
	return ciphertext, dekWrapped, nil
}

// Open descifra un secreto sellado con Seal y el mismo aad.
func (s *Sealer) Open(ciphertext, dekWrapped, aad []byte) ([]byte, error) {
	dek, err := gcmOpen(s.kek, dekWrapped, []byte(s.kekID))
	if err != nil {
		return nil, ErrOpen
	}
	return gcmOpen(dek, ciphertext, aad)
}
