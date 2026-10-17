package webhooksig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSignatureMatchesContract(t *testing.T) {
	t.Parallel()
	secret := []byte("s3cr3t")
	body := []byte(`{"id":"1","event":"finding.opened"}`)
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("1760700000." + string(body)))
	want := "v1=" + hex.EncodeToString(m.Sum(nil))
	if got := Signature(secret, 1760700000, body); got != want {
		t.Fatalf("firma = %s, quiero %s", got, want)
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()
	secret := []byte("s3cr3t")
	body := []byte(`{"evento":"sesion.iniciada"}`)
	now := time.Unix(1760700000, 0)
	h := http.Header{}
	Sign(h, secret, "evt-1", now, body)

	if id, err := Verify(h, body, now.Add(time.Minute), secret); err != nil || id != "evt-1" {
		t.Fatalf("válida: id=%q err=%v", id, err)
	}
	// Rotación: el secreto viejo y el nuevo valen a la vez.
	if _, err := Verify(h, body, now, []byte("otro"), secret); err != nil {
		t.Fatalf("con dos secretos: %v", err)
	}
	if _, err := Verify(h, []byte(`{"evento":"x"}`), now, secret); !errors.Is(err, ErrSignature) {
		t.Fatalf("cuerpo alterado: %v", err)
	}
	if _, err := Verify(h, body, now, []byte("otro")); !errors.Is(err, ErrSignature) {
		t.Fatalf("secreto equivocado: %v", err)
	}
	if _, err := Verify(h, body, now.Add(6*time.Minute), secret); !errors.Is(err, ErrTimestamp) {
		t.Fatalf("desfase de 6 min: %v", err)
	}
	if _, err := Verify(h, body, now.Add(-6*time.Minute), secret); !errors.Is(err, ErrTimestamp) {
		t.Fatalf("desfase futuro: %v", err)
	}
	h2 := h.Clone()
	h2.Del(HeaderID)
	if _, err := Verify(h2, body, now, secret); !errors.Is(err, ErrMissing) {
		t.Fatalf("sin id: %v", err)
	}
	h3 := h.Clone()
	h3.Set(HeaderSignature, "v0=abc, "+h.Get(HeaderSignature))
	if _, err := Verify(h3, body, now, secret); err != nil {
		t.Fatalf("varias firmas: %v", err)
	}
	h4 := h.Clone()
	h4.Set(HeaderSignature, strings.TrimPrefix(h.Get(HeaderSignature), "v1="))
	if _, err := Verify(h4, body, now, secret); !errors.Is(err, ErrSignature) {
		t.Fatalf("sin versión: %v", err)
	}
}

func TestNewSecret(t *testing.T) {
	t.Parallel()
	a, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewSecret()
	if a == b || !strings.HasPrefix(a, "whsec_") || len(a) < 40 {
		t.Fatalf("secretos %q %q", a, b)
	}
}
