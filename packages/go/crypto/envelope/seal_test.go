package envelope

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"
)

func TestSealOpen(t *testing.T) {
	t.Parallel()
	s := Ephemeral()
	ct, dek, err := s.Seal([]byte("secreto"), []byte("routeros_api:t1:r1"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("secreto")) {
		t.Fatal("texto en claro en el cifrado")
	}
	pt, err := s.Open(ct, dek, []byte("routeros_api:t1:r1"))
	if err != nil || string(pt) != "secreto" {
		t.Fatalf("open = %q, %v", pt, err)
	}
	// Otro AAD (otro tenant/router) no abre.
	if _, err := s.Open(ct, dek, []byte("routeros_api:t2:r1")); !errors.Is(err, ErrOpen) {
		t.Fatalf("aad distinto: %v", err)
	}
	// Otra KEK no abre.
	if _, err := Ephemeral().Open(ct, dek, []byte("routeros_api:t1:r1")); !errors.Is(err, ErrOpen) {
		t.Fatalf("kek distinta: %v", err)
	}
	if _, err := s.Open(ct[:5], dek, nil); !errors.Is(err, ErrOpen) {
		t.Fatalf("truncado: %v", err)
	}
}

func TestParseKEK(t *testing.T) {
	t.Parallel()
	raw := bytes.Repeat([]byte{7}, KeySize)
	for _, v := range []string{hex.EncodeToString(raw), base64.StdEncoding.EncodeToString(raw), " " + base64.RawURLEncoding.EncodeToString(raw) + "\n"} {
		k, err := ParseKEK(v)
		if err != nil || !bytes.Equal(k, raw) {
			t.Fatalf("ParseKEK(%q) = %x, %v", v, k, err)
		}
	}
	if _, err := ParseKEK("corta"); err == nil {
		t.Fatal("KEK corta aceptada")
	}
	if _, err := New([]byte("x")); err == nil {
		t.Fatal("New con KEK corta")
	}
	s1, _ := New(raw)
	s2, _ := New(raw)
	if s1.KEKID() != s2.KEKID() || s1.KEKID() == Ephemeral().KEKID() {
		t.Fatal("KEKID no determinista")
	}
}
