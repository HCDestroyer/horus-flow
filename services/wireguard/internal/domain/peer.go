package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// Códigos de error del contrato.
const (
	CodePoolExhausted    = "WIREGUARD_IP_POOL_EXHAUSTED"
	CodePublicKeyInUse   = "WIREGUARD_PUBLIC_KEY_IN_USE"
	CodeTokenInvalid     = "ENROLLMENT_TOKEN_INVALID"
	CodeRouterNotFound   = "ROUTER_NOT_FOUND"
	CodeRouterOSTooOld   = "ROUTEROS_VERSION_UNSUPPORTED"
	CodePeerNotFound     = "NOT_FOUND"
	CodeServiceUnavailbl = "SERVICE_UNAVAILABLE"
)

// Estados de peer y de handshake (contrato Peer).
const (
	StatusAwaitingEnrollment = "awaiting_enrollment"
	StatusPendingHandshake   = "pending_handshake"
	StatusActive             = "active"
	StatusRevoked            = "revoked"

	HandshakeNever = "never"
	HandshakeOK    = "ok"
	HandshakeStale = "stale"
)

// Keepalive es el persistent-keepalive del router (ADR-0022).
const Keepalive = 25

// StaleAfter: sin handshake en este tiempo el túnel se considera caído
// (WireGuard renegocia cada 2 min con tráfico; con keepalive 25 s un túnel
// sano nunca pasa de ~2 min).
const StaleAfter = 180 * time.Second

// TokenTTL es la vida de un token de enrolamiento.
const TokenTTL = 24 * time.Hour

// MaxTokenFailures invalidan el token.
const MaxTokenFailures = 5

// ErrNotFound lo devuelve el repositorio cuando no hay fila en el tenant.
var ErrNotFound = errors.New("wireguard: not found")

// Peer es el túnel de un router.
type Peer struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	ServerID        uuid.UUID
	RouterID        uuid.UUID
	Address         netip.Addr
	PublicKey       *string
	Status          string
	HandshakeState  string
	Keepalive       int
	EnrolledAt      *time.Time
	EnrolledFromIP  *netip.Addr
	ActivatedAt     *time.Time
	RevokedAt       *time.Time
	RevokedReason   *string
	LastHandshakeAt *time.Time
	Endpoint        *string
	RxBytes         uint64
	TxBytes         uint64
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Version         int
	// Token vigente más reciente (para la vista Peer.enrollment).
	Token *Token
}

// AllowedIP es la /32 del peer en el hub.
func (p *Peer) AllowedIP() string { return netip.PrefixFrom(p.Address, p.Address.BitLen()).String() }

// HandshakeStateAt calcula el estado del handshake en now.
func HandshakeStateAt(last *time.Time, now time.Time) string {
	switch {
	case last == nil || last.IsZero():
		return HandshakeNever
	case now.Sub(*last) > StaleAfter:
		return HandshakeStale
	}
	return HandshakeOK
}

// Token es un token de enrolamiento (solo su hash se guarda).
type Token struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	RouterID       uuid.UUID
	PeerID         uuid.UUID
	Hash           []byte
	ExpiresAt      time.Time
	UsedAt         *time.Time
	RevokedAt      *time.Time
	FailedAttempts int
	CreatedAt      time.Time
}

// State es el estado del token en now (contrato: pending|used|expired|revoked).
func (t *Token) State(now time.Time) string {
	switch {
	case t.UsedAt != nil:
		return "used"
	case t.RevokedAt != nil || t.FailedAttempts >= MaxTokenFailures:
		return "revoked"
	case !now.Before(t.ExpiresAt):
		return "expired"
	}
	return "pending"
}

// Usable indica si el token puede enrolar en now.
func (t *Token) Usable(now time.Time) bool { return t.State(now) == "pending" }

// NewToken genera un token de 256 bits (base64url sin relleno, 43
// caracteres) y su hash SHA-256.
func NewToken() (plain string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("wireguard: token: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(b)
	return plain, HashToken(plain), nil
}

// HashToken es el hash con el que se busca un token.
func HashToken(plain string) []byte {
	s := sha256.Sum256([]byte("horus-enroll-v1:" + plain))
	return s[:]
}

var pubKeyRe = regexp.MustCompile(`^[A-Za-z0-9+/]{42}[AEIMQUYcgkosw480]=$`)

// ValidPublicKey aplica el patrón del contrato (44 caracteres base64 de una
// clave Curve25519 de 32 B).
func ValidPublicKey(k string) bool {
	if !pubKeyRe.MatchString(k) {
		return false
	}
	b, err := base64.StdEncoding.DecodeString(k)
	return err == nil && len(b) == 32
}
