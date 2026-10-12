package remote

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"filippo.io/age"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
)

// credentialAAD liga la credencial sellada a su destino: copiarla a otra
// fila no descifra.
func credentialAAD(id uuid.UUID) []byte { return []byte("remote_destination:" + id.String()) }

// secret es la credencial en claro (solo en memoria, nunca en logs).
type secret struct {
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}

// ValidateCredentials comprueba el cuerpo de PUT .../credentials: contraseña
// o clave privada OpenSSH (o ambas) y, si la clave está cifrada, que la
// passphrase la abre. Los mensajes nunca repiten el secreto.
func ValidateCredentials(c Credentials) error {
	var fields []problem.FieldError
	if c.Password == nil && c.PrivateKey == nil {
		return apperr.Validation(fieldErr("password", "REQUIRED", "Indique una contraseña o una clave privada OpenSSH."))
	}
	if c.Password != nil && (*c.Password == "" || len(*c.Password) > MaxPasswordLen) {
		fields = append(fields, fieldErr("password", "INVALID_VALUE", fmt.Sprintf("Entre 1 y %d caracteres.", MaxPasswordLen)))
	}
	if c.Passphrase != nil && len(*c.Passphrase) > MaxPasswordLen {
		fields = append(fields, fieldErr("passphrase", "INVALID_VALUE", fmt.Sprintf("Como mucho %d caracteres.", MaxPasswordLen)))
	}
	if c.PrivateKey == nil && c.Passphrase != nil && *c.Passphrase != "" {
		fields = append(fields, fieldErr("passphrase", "INVALID_VALUE", "La passphrase solo acompaña a una clave privada."))
	}
	if c.PrivateKey != nil {
		switch {
		case *c.PrivateKey == "" || len(*c.PrivateKey) > MaxPrivateKeyLen:
			fields = append(fields, fieldErr("private_key", "INVALID_VALUE", fmt.Sprintf("Entre 1 y %d caracteres.", MaxPrivateKeyLen)))
		default:
			pass := ""
			if c.Passphrase != nil {
				pass = *c.Passphrase
			}
			if _, err := parseSigner(*c.PrivateKey, pass); err != nil {
				var missing *ssh.PassphraseMissingError
				switch {
				case errors.As(err, &missing):
					fields = append(fields, fieldErr("passphrase", "REQUIRED", "La clave privada está cifrada: indique su passphrase."))
				case errors.Is(err, errBadPassphrase):
					fields = append(fields, fieldErr("passphrase", "INVALID_VALUE", "La passphrase no abre la clave privada."))
				default:
					fields = append(fields, fieldErr("private_key", "INVALID_FORMAT", "No es una clave privada OpenSSH/PEM válida."))
				}
			}
		}
	}
	if len(fields) > 0 {
		return apperr.Validation(fields...)
	}
	return nil
}

var errBadPassphrase = errors.New("remote: bad passphrase")

// parseSigner interpreta una clave privada (PEM/OpenSSH), cifrada o no.
func parseSigner(key, passphrase string) (ssh.Signer, error) {
	pem := []byte(strings.TrimSpace(key) + "\n")
	if passphrase == "" {
		return ssh.ParsePrivateKey(pem)
	}
	s, err := ssh.ParsePrivateKeyWithPassphrase(pem, []byte(passphrase))
	switch {
	case err == nil:
		return s, nil
	case errors.Is(err, x509.IncorrectPasswordError):
		return nil, errBadPassphrase
	case strings.Contains(err.Error(), "not password protected"):
		// Una clave sin cifrar con passphrase: se acepta y la passphrase se ignora.
		return ssh.ParsePrivateKey(pem)
	}
	if _, perr := ssh.ParsePrivateKey(pem); perr == nil {
		// Clave PEM sin cifrar (formato antiguo) con passphrase: se ignora.
		return ssh.ParsePrivateKey(pem)
	}
	return nil, err
}

// sealCredentials cifra la credencial para el destino id.
func sealCredentials(sealer *envelope.Sealer, id uuid.UUID, c Credentials) (ct, dek []byte, err error) {
	s := secret{}
	if c.Password != nil {
		s.Password = *c.Password
	}
	if c.PrivateKey != nil {
		s.PrivateKey = strings.TrimSpace(*c.PrivateKey) + "\n"
	}
	if c.Passphrase != nil {
		s.Passphrase = *c.Passphrase
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, nil, err
	}
	return sealer.Seal(raw, credentialAAD(id))
}

// openCredentials descifra la credencial de d.
func openCredentials(sealer *envelope.Sealer, d *Destination) (*secret, error) {
	if !d.HasCredentials() {
		return nil, errNoCredentials
	}
	raw, err := sealer.Open(d.CredentialCiphertext, d.CredentialDEK, credentialAAD(d.ID))
	if err != nil {
		return nil, errCredentialsUnreadable
	}
	var s secret
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, errCredentialsUnreadable
	}
	return &s, nil
}

var (
	errNoCredentials         = errors.New("el destino no tiene credenciales")
	errCredentialsUnreadable = errors.New("no se pueden descifrar las credenciales (¿cambió la KEK de jobs?)")
)

// authMethods construye los métodos de autenticación SSH de la credencial.
func (s *secret) authMethods() ([]ssh.AuthMethod, error) {
	var out []ssh.AuthMethod
	if s.PrivateKey != "" {
		signer, err := parseSigner(s.PrivateKey, s.Passphrase)
		if err != nil {
			return nil, errors.New("la clave privada guardada no es válida")
		}
		out = append(out, ssh.PublicKeys(signer))
	}
	if s.Password != "" {
		pw := s.Password
		out = append(out, ssh.Password(pw), ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range answers {
				answers[i] = pw
			}
			return answers, nil
		}))
	}
	return out, nil
}

// ParseAgeRecipient lee la clave pública age (age1…) de las copias. Admite
// también el archivo de identidad (AGE-SECRET-KEY-1…, con comentarios como
// lo deja age-keygen): en ese caso se deriva la pública.
func ParseAgeRecipient(v string) (age.Recipient, string, error) {
	v = strings.TrimSpace(v)
	for _, line := range strings.Split(v, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "age1") {
			r, err := age.ParseX25519Recipient(line)
			if err != nil {
				return nil, "", fmt.Errorf("clave pública age no válida: %w", err)
			}
			return r, r.String(), nil
		}
	}
	ids, err := age.ParseIdentities(strings.NewReader(v))
	if err != nil {
		return nil, "", errors.New("ni clave pública age (age1…) ni identidad age (AGE-SECRET-KEY-1…)")
	}
	for _, id := range ids {
		if x, ok := id.(*age.X25519Identity); ok {
			return x.Recipient(), x.Recipient().String(), nil
		}
	}
	return nil, "", errors.New("la identidad age no es X25519")
}

// encryptTo cifra src con age para r y escribe en dst.
func encryptTo(dst io.Writer, src io.Reader, r age.Recipient) (int64, error) {
	w, err := age.Encrypt(dst, r)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(w, src)
	if err != nil {
		return n, err
	}
	return n, w.Close()
}

// DecryptAll descifra un archivo age con una identidad (tests y
// restauración desde Go; en un servidor nuevo basta `age -d -i`).
func DecryptAll(ciphertext []byte, identity string) ([]byte, error) {
	ids, err := age.ParseIdentities(strings.NewReader(identity))
	if err != nil {
		return nil, err
	}
	r, err := age.Decrypt(bytes.NewReader(ciphertext), ids...)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}
