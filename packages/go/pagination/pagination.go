// Package pagination implementa la paginación por cursor opaco keyset del
// contrato (docs/api.md §1.5, common.yaml PageInfo): `{data, page}`, límite
// 1–200 (50 por defecto) y cursor firmado con HMAC ligado al orden, a los
// filtros y al tenant del token (un cursor ajeno → 400 INVALID_CURSOR).
package pagination

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// Límites de página.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Errores.
var (
	ErrInvalidLimit  = errors.New("pagination: invalid limit")
	ErrInvalidCursor = errors.New("pagination: invalid cursor")
)

// Page es `page` de una colección.
type Page struct {
	NextCursor *string `json:"next_cursor"`
	PrevCursor *string `json:"prev_cursor"`
	HasMore    bool    `json:"has_more"`
	Limit      int     `json:"limit"`
	Total      *int    `json:"total"`
}

// Cursor es el contenido de un cursor: la última clave de orden vista.
type Cursor struct {
	Keys   []string `json:"k"`
	Sort   string   `json:"s"`
	Filter string   `json:"f"`
	Tenant string   `json:"t"`
}

// Codec firma y valida cursores.
type Codec struct{ key []byte }

// NewCodec crea un codec con key; si key está vacía genera una aleatoria (los
// cursores dejan de valer al reiniciar el proceso).
func NewCodec(key []byte) *Codec {
	if len(key) == 0 {
		key = make([]byte, 32)
		_, _ = rand.Read(key)
	}
	return &Codec{key: key}
}

func (c *Codec) mac(b []byte) []byte {
	m := hmac.New(sha256.New, c.key)
	m.Write(b)
	return m.Sum(nil)
}

// Encode serializa y firma cur.
func (c *Codec) Encode(cur Cursor) string {
	b, _ := json.Marshal(cur) //nolint:errchkjson // tipos simples
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(c.mac(b))
}

// Decode valida la firma de s y que esté ligado a sort, filter y tenant.
func (c *Codec) Decode(s, sort, filter, tenant string) (Cursor, error) {
	var cur Cursor
	body, sig, ok := strings.Cut(s, ".")
	if !ok {
		return cur, ErrInvalidCursor
	}
	b, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return cur, ErrInvalidCursor
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, c.mac(b)) {
		return cur, ErrInvalidCursor
	}
	if err := json.Unmarshal(b, &cur); err != nil {
		return cur, ErrInvalidCursor
	}
	if cur.Sort != sort || cur.Filter != filter || cur.Tenant != tenant || len(cur.Keys) == 0 {
		return cur, ErrInvalidCursor
	}
	return cur, nil
}

// Request son los parámetros comunes de una colección.
type Request struct {
	Limit        int
	Cursor       string
	IncludeTotal bool
}

// ParseRequest lee limit, cursor e include_total de q.
func ParseRequest(q url.Values) (Request, error) {
	req := Request{Limit: DefaultLimit, Cursor: q.Get("cursor")}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxLimit {
			return req, ErrInvalidLimit
		}
		req.Limit = n
	}
	if len(req.Cursor) > 2048 {
		return req, ErrInvalidCursor
	}
	if v := q.Get("include_total"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return req, ErrInvalidLimit
		}
		req.IncludeTotal = b
	}
	return req, nil
}

// FilterKey resume los filtros de una petición en una cadena estable para
// ligarlos al cursor.
func FilterKey(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return base64.RawURLEncoding.EncodeToString(h[:9])
}
