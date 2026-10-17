// Package webhooksig firma y verifica webhooks del ecosistema Horus
// (contrato docs/integrations/argosisp.md §1.3, ADR-0030):
//
//	X-Webhook-Id: <id estable del evento>          (deduplicación)
//	X-Webhook-Timestamp: <unix segundos>
//	X-Webhook-Signature: v1=<hex(HMAC-SHA256(secreto, timestamp + "." + cuerpo))>
//
// El receptor rechaza firmas inválidas y marcas de tiempo con más de
// [MaxSkew] de desfase. Lo usan el canal de notificación webhook (alerts,
// emisor) y el receptor de webhooks de ArgosISP (devices).
package webhooksig

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Cabeceras del contrato.
const (
	HeaderID        = "X-Webhook-Id"
	HeaderTimestamp = "X-Webhook-Timestamp"
	HeaderSignature = "X-Webhook-Signature"
	// Version es el prefijo de la firma.
	Version = "v1"
	// MaxSkew es el desfase máximo aceptado entre la marca de tiempo y el reloj del receptor.
	MaxSkew = 5 * time.Minute
	// MaxIDLen acota X-Webhook-Id.
	MaxIDLen = 128
)

// Errores de verificación (el receptor responde 401 sin detallar cuál).
var (
	ErrMissing   = errors.New("webhooksig: missing headers")
	ErrTimestamp = errors.New("webhooksig: timestamp outside allowed skew")
	ErrSignature = errors.New("webhooksig: invalid signature")
)

// Signature devuelve "v1=<hex>" para el cuerpo y la marca de tiempo (unix s).
func Signature(secret []byte, timestamp int64, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(strconv.FormatInt(timestamp, 10)))
	m.Write([]byte("."))
	m.Write(body)
	return Version + "=" + hex.EncodeToString(m.Sum(nil))
}

// Sign pone en h las tres cabeceras del contrato.
func Sign(h http.Header, secret []byte, id string, at time.Time, body []byte) {
	ts := at.Unix()
	h.Set(HeaderID, id)
	h.Set(HeaderTimestamp, strconv.FormatInt(ts, 10))
	h.Set(HeaderSignature, Signature(secret, ts, body))
}

// Verify comprueba las cabeceras de h contra el cuerpo y devuelve el id del
// webhook. Acepta varias firmas separadas por coma o espacio (rotación del
// secreto en el emisor) y varios secretos (rotación en el receptor).
func Verify(h http.Header, body []byte, now time.Time, secrets ...[]byte) (string, error) {
	id := strings.TrimSpace(h.Get(HeaderID))
	tsRaw := strings.TrimSpace(h.Get(HeaderTimestamp))
	sig := strings.TrimSpace(h.Get(HeaderSignature))
	if id == "" || len(id) > MaxIDLen || tsRaw == "" || sig == "" || len(secrets) == 0 {
		return "", ErrMissing
	}
	ts, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil {
		return "", ErrTimestamp
	}
	at := time.Unix(ts, 0)
	if d := now.Sub(at); d > MaxSkew || d < -MaxSkew {
		return "", ErrTimestamp
	}
	for _, cand := range strings.FieldsFunc(sig, func(r rune) bool { return r == ',' || r == ' ' }) {
		if !strings.HasPrefix(cand, Version+"=") {
			continue
		}
		for _, s := range secrets {
			if len(s) == 0 {
				continue
			}
			if hmac.Equal([]byte(cand), []byte(Signature(s, ts, body))) {
				return id, nil
			}
		}
	}
	return "", ErrSignature
}

// NewSecret genera un secreto de 32 bytes aleatorios codificado en base64url
// con prefijo "whsec_" (lo genera el receptor y se copia al emisor).
func NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(b), nil
}
