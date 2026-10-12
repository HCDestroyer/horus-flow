package remote

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
)

func TestAgeRoundTrip(t *testing.T) {
	t.Parallel()
	id, identity := testIdentity(t)
	// La pública se acepta tal cual, y también el archivo de age-keygen.
	keygen := "# created: 2026-10-12T00:00:00Z\n# public key: " + id.Recipient().String() + "\n" + identity + "\n"
	for _, in := range []string{id.Recipient().String(), keygen, identity} {
		r, pub, err := ParseAgeRecipient(in)
		if err != nil || pub != id.Recipient().String() {
			t.Fatalf("ParseAgeRecipient(%.20q) = %q, %v", in, pub, err)
		}
		plain := make([]byte, 300_000)
		_, _ = rand.Read(plain)
		var ct bytes.Buffer
		n, err := encryptTo(&ct, bytes.NewReader(plain), r)
		if err != nil || n != int64(len(plain)) {
			t.Fatalf("encryptTo = %d, %v", n, err)
		}
		if bytes.Contains(ct.Bytes(), plain[:64]) || !strings.HasPrefix(ct.String(), "age-encryption.org/v1") {
			t.Fatal("el cifrado no parece age")
		}
		got, err := DecryptAll(ct.Bytes(), keygen)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("DecryptAll: %v", err)
		}
	}
	if _, _, err := ParseAgeRecipient("no es una clave"); err == nil {
		t.Fatal("clave basura aceptada")
	}
	other, otherID := testIdentity(t)
	_ = other
	var ct bytes.Buffer
	if _, err := encryptTo(&ct, strings.NewReader("x"), id.Recipient()); err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptAll(ct.Bytes(), otherID); err == nil {
		t.Fatal("otra identidad descifra")
	}
}

func TestValidateCredentials(t *testing.T) {
	t.Parallel()
	plainKey, _ := clientKeyPEM(t, "")
	encKey, _ := clientKeyPEM(t, "frase-secreta")
	ok := []Credentials{
		{Password: ptr("pw")},
		{PrivateKey: ptr(plainKey)},
		{PrivateKey: ptr(encKey), Passphrase: ptr("frase-secreta")},
		{PrivateKey: ptr(plainKey), Passphrase: ptr("ignorada")},
		{PrivateKey: ptr(plainKey), Password: ptr("pw")},
	}
	for i, c := range ok {
		if err := ValidateCredentials(c); err != nil {
			t.Errorf("caso %d: %v", i, err)
		}
	}
	bad := map[string]struct {
		c     Credentials
		field string
	}{
		"vacío":             {Credentials{}, "password"},
		"clave basura":      {Credentials{PrivateKey: ptr("-----BEGIN NOPE-----")}, "private_key"},
		"cifrada sin frase": {Credentials{PrivateKey: ptr(encKey)}, "passphrase"},
		"frase incorrecta":  {Credentials{PrivateKey: ptr(encKey), Passphrase: ptr("otra")}, "passphrase"},
		"frase sola":        {Credentials{Password: ptr("x"), Passphrase: ptr("y")}, "passphrase"},
		"contraseña vacía":  {Credentials{Password: ptr("")}, "password"},
	}
	for name, c := range bad {
		err := ValidateCredentials(c.c)
		e, isApp := apperr.As(err)
		if !isApp || len(e.Fields) == 0 || e.Fields[0].Field != c.field {
			t.Errorf("%s: err = %v", name, err)
			continue
		}
		// El mensaje nunca repite el secreto.
		for _, f := range e.Fields {
			if strings.Contains(f.Message, "frase-secreta") || strings.Contains(f.Message, "PRIVATE KEY") {
				t.Errorf("%s: el error repite el secreto: %q", name, f.Message)
			}
		}
	}
}

func TestSealCredentialsBoundToDestination(t *testing.T) {
	t.Parallel()
	sealer := envelope.Ephemeral()
	id := uuid.New()
	ct, dek, err := sealCredentials(sealer, id, Credentials{Password: ptr("s3cret")})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("s3cret")) {
		t.Fatal("la credencial está en claro")
	}
	d := &Destination{ID: id, CredentialCiphertext: ct, CredentialDEK: dek}
	s, err := openCredentials(sealer, d)
	if err != nil || s.Password != "s3cret" {
		t.Fatalf("open = %+v, %v", s, err)
	}
	// Copiada a otro destino no descifra (AAD).
	d2 := &Destination{ID: uuid.New(), CredentialCiphertext: ct, CredentialDEK: dek}
	if _, err := openCredentials(sealer, d2); err == nil {
		t.Fatal("la credencial abre en otro destino")
	}
	if _, err := openCredentials(envelope.Ephemeral(), d); err == nil {
		t.Fatal("la credencial abre con otra KEK")
	}
}

func TestHostKeyPolicy(t *testing.T) {
	t.Parallel()
	signer := newHostSigner(t)
	fp := Fingerprint(signer.PublicKey())
	if !strings.HasPrefix(fp, "SHA256:") || len(fp) != 50 {
		t.Fatalf("huella = %q", fp)
	}
	other := Fingerprint(newHostSigner(t).PublicKey())
	cases := []struct {
		name            string
		pinned, confirm string
		wantErr         error
	}{
		{"primera vez sin confirmar", "", "", errHostKeyUnconfirmed},
		{"primera vez confirmación distinta", "", other, errHostKeyUnconfirmed},
		{"primera vez confirmada", "", fp, nil},
		{"fijada coincide", fp, "", nil},
		{"fijada cambió", other, "", errHostKeyChanged},
		{"fijada cambió aunque se confirme la nueva", other, fp, errHostKeyChanged},
	}
	for _, c := range cases {
		p := &hostKeyPolicy{pinned: c.pinned, confirm: c.confirm}
		err := p.callback("h", nil, signer.PublicKey())
		if err != c.wantErr {
			t.Errorf("%s: err = %v, quiero %v", c.name, err, c.wantErr)
		}
		if p.presented != fp || p.accepted != (c.wantErr == nil) {
			t.Errorf("%s: presented=%q accepted=%v", c.name, p.presented, p.accepted)
		}
	}
}
