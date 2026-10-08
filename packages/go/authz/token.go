// Package authz es la librería común de autenticación y autorización de los
// módulos (docs/security.md §5.1, §6.4):
//
//   - access JWT EdDSA (Ed25519) de 10 min con los claims del contrato C7
//     (`iss`, `aud`, `sub`, `typ`, `scope`, `sid`, `tid`, `via_platform`,
//     `amr`, `auth_time`, `iat`, `exp`, `jti`, `perms`): [Signer] lo emite
//     (solo el módulo auth) y [Verifier] lo valida (gateway y módulos);
//   - [Principal] en el contexto de la petición y [Require] para el permiso
//     fino;
//   - [Guard]: middleware HTTP de los módulos que revalida el JWT, el ámbito
//     del token (`tenant`, `session`, `platform`) y el permiso, con errores
//     RFC 9457.
package authz

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Ámbitos de token (claim `scope`).
const (
	ScopeSession  = "session"
	ScopeTenant   = "tenant"
	ScopePlatform = "platform"
	ScopeKiosk    = "kiosk"
)

// Tipos de principal (claim `typ`).
const (
	TypeUser    = "user"
	TypeKiosk   = "kiosk"
	TypeService = "service"
)

// Audience es el `aud` de los access tokens de la API.
const Audience = "horus-api"

// DefaultTTL es la vida de un access token (security.md S4).
const DefaultTTL = 10 * time.Minute

// Leeway tolera desfase de reloj al validar `exp`/`iat`.
const Leeway = 30 * time.Second

// Errores de validación.
var (
	ErrTokenExpired = errors.New("authz: token expired")
	ErrInvalidToken = errors.New("authz: invalid token")
)

// Claims son los claims del access token.
type Claims struct {
	jwt.RegisteredClaims
	Typ         string              `json:"typ"`
	Scope       string              `json:"scope"`
	SID         string              `json:"sid,omitempty"`
	TID         string              `json:"tid,omitempty"`
	ViaPlatform bool                `json:"via_platform,omitempty"`
	AMR         []string            `json:"amr,omitempty"`
	AuthTime    int64               `json:"auth_time,omitempty"`
	Perms       map[string][]string `json:"perms,omitempty"`
}

// KeyID calcula el `kid` de una clave pública (huella SHA-256 truncada).
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return base64.RawURLEncoding.EncodeToString(sum[:12])
}

// Signer firma access tokens. Solo lo instancia el módulo auth.
type Signer struct {
	key      ed25519.PrivateKey
	kid      string
	issuer   string
	audience string
	ttl      time.Duration
	now      func() time.Time
}

// NewSigner crea un firmante. ttl 0 = [DefaultTTL]; now nil = time.Now.
func NewSigner(key ed25519.PrivateKey, issuer string, ttl time.Duration, now func() time.Time) *Signer {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if now == nil {
		now = time.Now
	}
	return &Signer{
		key: key, kid: KeyID(key.Public().(ed25519.PublicKey)), //nolint:forcetypeassert // Ed25519 siempre
		issuer: issuer, audience: Audience, ttl: ttl, now: now,
	}
}

// PublicKey devuelve la clave pública del firmante.
func (s *Signer) PublicKey() ed25519.PublicKey {
	return s.key.Public().(ed25519.PublicKey) //nolint:forcetypeassert // Ed25519 siempre
}

// TTL devuelve la vida de los tokens.
func (s *Signer) TTL() time.Duration { return s.ttl }

// Sign completa iss, aud, iat, exp, jti y firma c. Devuelve el token y su
// caducidad.
func (s *Signer) Sign(c Claims) (string, time.Time, error) {
	now := s.now().UTC()
	exp := now.Add(s.ttl)
	c.Issuer = s.issuer
	c.Audience = jwt.ClaimStrings{s.audience}
	c.IssuedAt = jwt.NewNumericDate(now)
	c.NotBefore = nil
	c.ExpiresAt = jwt.NewNumericDate(exp)
	if c.ID == "" {
		c.ID = uuid.Must(uuid.NewV7()).String()
	}
	t := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c)
	t.Header["kid"] = s.kid
	signed, err := t.SignedString(s.key)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("authz: sign: %w", err)
	}
	return signed, exp, nil
}

// KeySet son las claves públicas aceptadas, por `kid` (la vigente y la
// anterior durante la rotación).
type KeySet map[string]ed25519.PublicKey

// Add añade una clave.
func (k KeySet) Add(pub ed25519.PublicKey) { k[KeyID(pub)] = pub }

// Verifier valida access tokens.
type Verifier struct {
	keys   KeySet
	issuer string
	now    func() time.Time
}

// NewVerifier crea un validador. issuer vacío = no se comprueba `iss`.
func NewVerifier(keys KeySet, issuer string, now func() time.Time) *Verifier {
	if now == nil {
		now = time.Now
	}
	return &Verifier{keys: keys, issuer: issuer, now: now}
}

// Verify valida firma (EdDSA, `kid` conocido), `aud`, `iss`, `exp` e `iat`
// y devuelve el principal.
func (v *Verifier) Verify(token string) (*Principal, error) {
	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithAudience(Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(Leeway),
		jwt.WithTimeFunc(v.now),
	}
	if v.issuer != "" {
		opts = append(opts, jwt.WithIssuer(v.issuer))
	}
	var c Claims
	_, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		pub, ok := v.keys[kid]
		if !ok {
			return nil, errors.New("unknown kid")
		}
		return pub, nil
	}, opts...)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err) //nolint:errorlint // no exponer el error interno como cadena de errores
	}
	return principalFromClaims(&c, token)
}

func principalFromClaims(c *Claims, raw string) (*Principal, error) {
	switch c.Scope {
	case ScopeSession, ScopeTenant, ScopePlatform, ScopeKiosk:
	default:
		return nil, fmt.Errorf("%w: scope %q", ErrInvalidToken, c.Scope)
	}
	p := &Principal{
		Type:        c.Typ,
		Subject:     c.Subject,
		Scope:       c.Scope,
		ViaPlatform: c.ViaPlatform,
		AMR:         slices.Clone(c.AMR),
		Perms:       c.Perms,
		TokenID:     c.ID,
		Raw:         raw,
	}
	if c.ExpiresAt != nil {
		p.ExpiresAt = c.ExpiresAt.Time
	}
	if c.AuthTime > 0 {
		p.AuthTime = time.Unix(c.AuthTime, 0).UTC()
	}
	if p.Type == TypeUser {
		id, err := uuid.Parse(c.Subject)
		if err != nil {
			return nil, fmt.Errorf("%w: sub", ErrInvalidToken)
		}
		p.UserID = id
	}
	if c.SID != "" {
		id, err := uuid.Parse(c.SID)
		if err != nil {
			return nil, fmt.Errorf("%w: sid", ErrInvalidToken)
		}
		p.SessionID = id
	}
	if c.TID != "" {
		id, err := uuid.Parse(c.TID)
		if err != nil {
			return nil, fmt.Errorf("%w: tid", ErrInvalidToken)
		}
		p.TenantID = id
	}
	// Coherencia ámbito ↔ tid: un token de tenant siempre lleva tid; uno de
	// sesión o plataforma nunca.
	switch {
	case (p.Scope == ScopeTenant || p.Scope == ScopeKiosk) && p.TenantID == uuid.Nil:
		return nil, fmt.Errorf("%w: tenant token without tid", ErrInvalidToken)
	case (p.Scope == ScopeSession || p.Scope == ScopePlatform) && p.TenantID != uuid.Nil:
		return nil, fmt.Errorf("%w: tid in %s token", ErrInvalidToken, p.Scope)
	}
	return p, nil
}

// GenerateKey crea una clave Ed25519 nueva.
func GenerateKey() (ed25519.PrivateKey, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("authz: generate key: %w", err)
	}
	return priv, nil
}

// MarshalPrivateKeyPEM codifica una clave privada en PEM PKCS#8.
func MarshalPrivateKeyPEM(key ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("authz: marshal key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// MarshalPublicKeyPEM codifica una clave pública en PEM PKIX.
func MarshalPublicKeyPEM(key ed25519.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return nil, fmt.Errorf("authz: marshal public key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

// ParsePrivateKeyPEM lee una clave Ed25519 PKCS#8 en PEM
// (`openssl genpkey -algorithm ed25519`).
func ParsePrivateKeyPEM(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("authz: no PEM block")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("authz: parse private key: %w", err)
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("authz: private key is not Ed25519")
	}
	return priv, nil
}

// ParsePublicKeysPEM lee una o varias claves públicas Ed25519 (PEM PKIX) o
// privadas (se usa su parte pública).
func ParsePublicKeysPEM(data []byte) (KeySet, error) {
	ks := KeySet{}
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		switch block.Type {
		case "PUBLIC KEY":
			k, err := x509.ParsePKIXPublicKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("authz: parse public key: %w", err)
			}
			pub, ok := k.(ed25519.PublicKey)
			if !ok {
				return nil, errors.New("authz: public key is not Ed25519")
			}
			ks.Add(pub)
		case "PRIVATE KEY":
			priv, err := ParsePrivateKeyPEM(pem.EncodeToMemory(block))
			if err != nil {
				return nil, err
			}
			ks.Add(priv.Public().(ed25519.PublicKey)) //nolint:forcetypeassert // Ed25519 siempre
		}
	}
	if len(ks) == 0 {
		return nil, errors.New("authz: no Ed25519 keys found")
	}
	return ks, nil
}
