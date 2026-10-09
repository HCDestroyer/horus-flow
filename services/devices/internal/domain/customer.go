package domain

import (
	"encoding/json"
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// Códigos de error de clientes.
const (
	CodeCustomerNotFound   = "CUSTOMER_NOT_FOUND"
	CodeCustomerKindLocked = "CUSTOMER_KIND_LOCKED"
)

// Valores del cliente (docs/database.md §2.3).
var (
	CustomerKindSources = []string{"default", "scoring", "manual"}
	CustomerStatuses    = []string{"active", "inactive"}
	SecurityStates      = []string{"clean", "suspected", "infected", "mitigated"}
)

// Ciclo de vida por defecto (docs/database.md §2.3.4).
const (
	DefaultInactivityDays  = 30
	DefaultRetentionMonths = 25
)

// Customer es un cliente = IP observada (ADR-0018).
type Customer struct {
	ID                     uuid.UUID
	TenantID               uuid.UUID
	RealmID                uuid.UUID
	Address                netip.Prefix // /32 IPv4 o el prefijo IPv6 del cliente
	SiteID                 uuid.UUID
	ClientPrefixID         *uuid.UUID
	Kind                   string
	KindSource             string
	KindLocked             bool
	KindConfidence         *int
	KindChangedAt          *time.Time
	CommercialUseSuspected bool
	SecurityState          string
	OpenFindings           int
	Alias                  *string
	AliasSource            *string
	Notes                  *string
	Status                 string
	InactiveReason         *string
	FirstSeen              time.Time
	LastSeen               time.Time
	ResetAt                *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
	Version                int
	// DefaultKind es el tipo por defecto de su prefijo (reset).
	DefaultKind string
}

// AddressString es la IP canónica: sin máscara en IPv4 /32 y con ella en IPv6.
func (c *Customer) AddressString() string { return CanonicalString(c.Address) }

// CanonicalString formatea una clave de cliente.
func CanonicalString(p netip.Prefix) string {
	if p.Addr().Is4() && p.Bits() == 32 || p.Addr().Is6() && p.Bits() == 128 {
		return p.Addr().String()
	}
	return p.String()
}

// CanonicalAddress devuelve la clave de un cliente: IPv4 /32 o IPv6 truncada
// a v6len (64 por defecto; D22: cada familia es un cliente independiente).
func CanonicalAddress(a netip.Addr, v6len int) netip.Prefix {
	a = a.Unmap()
	if a.Is4() {
		return netip.PrefixFrom(a, 32)
	}
	if v6len <= 0 || v6len > 128 {
		v6len = 64
	}
	p, _ := a.Prefix(v6len)
	return p
}

// ParseCustomerAddress interpreta "10.0.0.1", "2001:db8::/64" o "2001:db8::1".
func ParseCustomerAddress(s string) (netip.Addr, int, bool) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Addr(), p.Bits(), true
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, 0, false
	}
	return a, a.BitLen(), true
}

// KindChange es una entrada del historial inmutable.
type KindChange struct {
	ID           uuid.UUID
	CustomerID   uuid.UUID
	FromKind     *string
	ToKind       string
	Source       string // default | scoring | manual | reset
	Reasons      json.RawMessage
	ReasonCodes  []string
	ModelRef     *string
	Confidence   *int
	ActorID      *uuid.UUID
	ManualReason *string
	ChangedAt    time.Time
}

// SetKind aplica un cambio manual y devuelve la entrada del historial (nil si
// ya tenía ese tipo bloqueado: 200 sin cambio).
func (c *Customer) SetKind(kind, reason string, actor *uuid.UUID, now time.Time) *KindChange {
	if c.Kind == kind && c.KindLocked && c.KindSource == "manual" {
		return nil
	}
	from := c.Kind
	c.Kind, c.KindSource, c.KindLocked, c.KindConfidence = kind, "manual", true, nil
	c.KindChangedAt = &now
	return &KindChange{ID: uuid.Must(uuid.NewV7()), CustomerID: c.ID, FromKind: &from, ToKind: kind, Source: "manual",
		Reasons: json.RawMessage("[]"), ReasonCodes: []string{}, ActorID: actor, ManualReason: &reason, ChangedAt: now}
}

// Reset vuelve al tipo por defecto, borra alias y notas y fija reset_at.
func (c *Customer) Reset(reason string, actor *uuid.UUID, now time.Time) *KindChange {
	from := c.Kind
	def := c.DefaultKind
	if def == "" {
		def = "residential"
	}
	c.Kind, c.KindSource, c.KindLocked, c.KindConfidence = def, "default", false, nil
	c.KindChangedAt = &now
	c.Alias, c.AliasSource, c.Notes = nil, nil, nil
	c.CommercialUseSuspected = false
	c.ResetAt = &now
	return &KindChange{ID: uuid.Must(uuid.NewV7()), CustomerID: c.ID, FromKind: &from, ToKind: def, Source: "reset",
		Reasons: json.RawMessage("[]"), ReasonCodes: []string{}, ActorID: actor, ManualReason: &reason, ChangedAt: now}
}

// InactiveBefore devuelve el corte de inactividad.
func InactiveBefore(now time.Time, days int) time.Time {
	if days <= 0 {
		days = DefaultInactivityDays
	}
	return now.AddDate(0, 0, -days)
}

// PurgeBefore devuelve el corte de purga por retención.
func PurgeBefore(now time.Time, months int) time.Time {
	if months <= 0 {
		months = DefaultRetentionMonths
	}
	return now.AddDate(0, -months, 0)
}
