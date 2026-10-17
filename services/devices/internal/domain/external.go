package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Identidad externa del cliente (ADR-0030, contrato ArgosISP §2.4). La
// identidad de Horus sigue siendo (tenant, realm, ip); la externa es una
// anotación con vigencia porque en PPPoE una IP cambia de dueño.
const (
	SystemArgos        = "argosisp"
	AliasSourceManual  = "manual"
	AliasSourceArgos   = "argosisp"
	maxExternalTextLen = 120
	// IdentityMaxAge: el barrido re-resuelve las identidades más viejas que esto.
	IdentityMaxAge = 6 * time.Hour
	// Backoff ante ArgosISP caído: exponencial desde 30 s hasta 15 min.
	BackoffBase = 30 * time.Second
	BackoffMax  = 15 * time.Minute
)

// Estados de la última resolución de un cliente.
const (
	CheckFound    = "found"
	CheckNotFound = "not_found"
	CheckError    = "error"
)

// ExternalIdentity es un periodo de vigencia de la identidad en ArgosISP.
type ExternalIdentity struct {
	ID                 uuid.UUID
	TenantID           uuid.UUID
	CustomerID         uuid.UUID
	System             string
	ExternalCustomerID *int64
	ExternalServiceID  *int64
	// Empresa del servicio en ArgosISP (multiempresa, contrato 1.2.0 §2.4).
	ExternalCompanyID *int64
	CompanyName       *string
	CompanyNIT        *string
	DisplayName       *string
	Code              *string
	ServiceState      *string
	Plan              *string
	ValidFrom         time.Time
	ValidTo           *time.Time // nil = vigente
	SyncedAt          time.Time
	Stale             bool
}

// Current indica si el periodo sigue vigente.
func (i *ExternalIdentity) Current() bool { return i.ValidTo == nil }

// ValidAt indica si el periodo cubre el instante at.
func (i *ExternalIdentity) ValidAt(at time.Time) bool {
	return !at.Before(i.ValidFrom) && (i.ValidTo == nil || at.Before(*i.ValidTo))
}

// Resolution es lo que ArgosISP dice de una IP en un momento.
type Resolution struct {
	Found              bool
	ExternalCustomerID *int64
	ExternalServiceID  *int64
	DisplayName        *string
	Code               *string
	ServiceState       *string
	Plan               *string
	// CompanyKnown: la respuesta informó la empresa (resolutor por lotes, 1.2.0).
	// El respaldo GET /ip/:ip no la trae: entonces no se toca la que ya hubiera.
	CompanyKnown bool
	CompanyID    *int64
	CompanyName  *string
	CompanyNIT   *string
	// Since: desde cuándo el servicio tiene la IP según ArgosISP (si lo informa).
	Since *time.Time
}

func eqI64(a, b *int64) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

func eqStr(a, b *string) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

// SameOwner indica si la resolución es del mismo cliente y servicio de ArgosISP.
func (i *ExternalIdentity) SameOwner(r Resolution) bool {
	return eqI64(i.ExternalCustomerID, r.ExternalCustomerID) && eqI64(i.ExternalServiceID, r.ExternalServiceID)
}

// CleanText recorta y acota un texto que viene de ArgosISP (nil si queda vacío).
func CleanText(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, *s))
	if v == "" {
		return nil
	}
	if utf8.RuneCountInString(v) > maxExternalTextLen {
		v = string([]rune(v)[:maxExternalTextLen])
	}
	return &v
}

// IdentityPlan es el cambio que hay que aplicar tras una resolución.
type IdentityPlan struct {
	// Close cierra el periodo vigente en CloseAt.
	Close   bool
	CloseAt time.Time
	// Open abre un periodo nuevo.
	Open *ExternalIdentity
	// Refresh actualiza en sitio el periodo vigente (mismo dueño: nombre,
	// código, estado, plan, empresa; quita stale).
	Refresh *ExternalIdentity
}

// PlanResolution decide qué hacer con la identidad vigente cur (o nil) ante
// la resolución r en now:
//   - mismo dueño → refrescar datos y quitar stale (la empresa solo si la
//     resolución la informa: el respaldo no la trae y se conserva la anterior);
//   - otro dueño → cerrar la vigente y abrir una nueva (desde r.Since si es
//     posterior al inicio de la vigente; si no, desde now);
//   - no encontrada → cerrar la vigente (la IP ya no es de nadie en ArgosISP).
func PlanResolution(cur *ExternalIdentity, r Resolution, now time.Time, tenant, customer uuid.UUID) IdentityPlan {
	var p IdentityPlan
	if !r.Found {
		if cur != nil {
			p.Close, p.CloseAt = true, now
		}
		return p
	}
	fill := func(i *ExternalIdentity) {
		i.ExternalCustomerID, i.ExternalServiceID = r.ExternalCustomerID, r.ExternalServiceID
		i.DisplayName, i.Code = CleanText(r.DisplayName), CleanText(r.Code)
		i.ServiceState, i.Plan = CleanText(r.ServiceState), CleanText(r.Plan)
		if r.CompanyKnown {
			i.ExternalCompanyID, i.CompanyName, i.CompanyNIT = r.CompanyID, CleanText(r.CompanyName), CleanText(r.CompanyNIT)
		}
		i.SyncedAt, i.Stale = now, false
	}
	if cur != nil && cur.SameOwner(r) {
		upd := *cur
		fill(&upd)
		p.Refresh = &upd
		return p
	}
	from := now
	if r.Since != nil && !r.Since.After(now) {
		from = *r.Since
	}
	if cur != nil {
		if !from.After(cur.ValidFrom) {
			from = now // un "desde" anterior a la vigencia actual no puede solaparla
		}
		p.Close, p.CloseAt = true, from
	}
	open := &ExternalIdentity{ID: uuid.Must(uuid.NewV7()), TenantID: tenant, CustomerID: customer, System: SystemArgos, ValidFrom: from}
	fill(open)
	p.Open = open
	return p
}

// ExternalAlias aplica al cliente el nombre de su identidad vigente (name =
// nil: sin identidad vigente). Un alias manual NUNCA se toca; sin identidad,
// un alias que vino de ArgosISP se quita (era del dueño anterior de la IP).
// Devuelve si cambió algo.
func (c *Customer) ExternalAlias(name *string) bool {
	if c.AliasSource != nil && *c.AliasSource == AliasSourceManual {
		return false
	}
	fromArgos := c.AliasSource != nil && *c.AliasSource == AliasSourceArgos
	if name == nil {
		if !fromArgos {
			return false
		}
		c.Alias, c.AliasSource = nil, nil
		return true
	}
	// Otro origen automático (routeros_ppp) o sin alias: ArgosISP manda.
	if fromArgos && eqStr(c.Alias, name) {
		return false
	}
	v, src := *name, AliasSourceArgos
	c.Alias, c.AliasSource = &v, &src
	return true
}

// NextBackoff devuelve la espera tras failures fallos seguidos de ArgosISP
// (30 s, 1, 2, 4, 8 min… hasta 15 min).
func NextBackoff(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	d := BackoffBase
	for i := 1; i < failures && d < BackoffMax; i++ {
		d *= 2
	}
	return min(d, BackoffMax)
}

// ArgosConnector es la configuración por ISP del conector ArgosISP. El token
// atk_ y los secretos del webhook se guardan cifrados (sobre: DEK envuelta
// con la KEK de devices) y nunca se devuelven.
type ArgosConnector struct {
	ID                   uuid.UUID // opaco: forma parte de la URL del webhook
	TenantID             uuid.UUID
	BaseURL              string
	Enabled              bool
	TokenCiphertext      []byte
	TokenDEK             []byte
	SecretCiphertext     []byte
	SecretDEK            []byte
	PrevSecretCiphertext []byte
	PrevSecretDEK        []byte
	PrevSecretUntil      *time.Time
	KEKID                *string
	Resolver             *string // batch | fallback
	LastSyncAt           *time.Time
	LastSyncStatus       *string // ok | degraded | failing
	LastError            *string
	Failures             int
	BackoffUntil         *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
	Version              int
}

// HasToken indica si hay token guardado.
func (c *ArgosConnector) HasToken() bool { return len(c.TokenCiphertext) > 0 }

// HasSecret indica si hay secreto de webhook.
func (c *ArgosConnector) HasSecret() bool { return len(c.SecretCiphertext) > 0 }

// Active indica si el conector puede resolver (activo y con token).
func (c *ArgosConnector) Active() bool {
	return c != nil && c.Enabled && c.HasToken() && c.BaseURL != ""
}

// InBackoff indica si hay que esperar antes de volver a llamar a ArgosISP.
func (c *ArgosConnector) InBackoff(now time.Time) bool {
	return c.BackoffUntil != nil && now.Before(*c.BackoffUntil)
}
