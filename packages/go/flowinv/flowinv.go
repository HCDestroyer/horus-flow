// Package flowinv es el inventario que necesitan los roles collector e
// ingester del dominio flows (docs/events.md §4.5 `flows-inventory` e
// `ingester-known-clients`; contrato horus.devices.v1.InventoryService):
//
//   - exportadores: IP de túnel /32 → (tenant, router, nodo) e ifIndex → flow_role;
//   - realms (node_private | public) y prefijos de clientes con su rol
//     (customers | infrastructure | excluded) e ipv6_client_len;
//   - ASN propios de cada tenant (modo descubrimiento, I1-29);
//   - claves de clientes ya conocidos (descubrimiento, I1-05).
//
// Un Snapshot es inmutable; Store guarda el vigente y lo sustituye de forma
// atómica (fichero recargado en caliente o proyección de eventos de devices).
package flowinv

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sync/atomic"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/hcdestroyer/horus-flow/packages/go/iptrie"
)

// Roles de un prefijo de clientes (traffic-model.md §4.1).
const (
	RoleCustomers      = "customers"
	RoleInfrastructure = "infrastructure"
	RoleExcluded       = "excluded"
)

// Tipos de realm (traffic-model.md §4.2).
const (
	RealmNodePrivate = "node_private"
	RealmPublic      = "public"
)

// Roles de interfaz que importan a flows (devices.interface.flow_role).
const (
	FlowRoleCustomerEdge = "customer_edge"
	FlowRoleUpstream     = "upstream"
)

// DefaultIPv6ClientLen es el tamaño de cliente IPv6 por defecto (§4.8.2).
const DefaultIPv6ClientLen = 64

// Interface es el rol de un ifIndex del exportador.
type Interface struct {
	IfIndex     uint32    `yaml:"if_index" json:"if_index"`
	InterfaceID uuid.UUID `yaml:"interface_id" json:"interface_id"`
	FlowRole    string    `yaml:"flow_role" json:"flow_role"`
}

// Exporter es el router principal de un nodo con su IP de túnel.
type Exporter struct {
	TenantID   uuid.UUID   `yaml:"tenant_id" json:"tenant_id"`
	RouterID   uuid.UUID   `yaml:"router_id" json:"router_id"`
	SiteID     uuid.UUID   `yaml:"site_id" json:"site_id"`
	Name       string      `yaml:"name" json:"name"`
	TunnelIP   netip.Addr  `yaml:"tunnel_ip" json:"tunnel_ip"`
	AdminState string      `yaml:"admin_state" json:"admin_state"`
	Interfaces []Interface `yaml:"interfaces" json:"interfaces"`
	// MaxFlowsPerSecond es la cuota del tenant (0 = sin límite).
	MaxFlowsPerSecond int `yaml:"tenant_max_flows_per_second" json:"tenant_max_flows_per_second"`
}

// Realm agrupa clientes con el mismo espacio de direcciones.
type Realm struct {
	ID       uuid.UUID `yaml:"id" json:"id"`
	TenantID uuid.UUID `yaml:"tenant_id" json:"tenant_id"`
	Kind     string    `yaml:"kind" json:"kind"`
	SiteID   uuid.UUID `yaml:"site_id" json:"site_id"`
	Name     string    `yaml:"name" json:"name"`
}

// ClientPrefix es un prefijo declarado de un nodo.
type ClientPrefix struct {
	ID            uuid.UUID    `yaml:"id" json:"id"`
	TenantID      uuid.UUID    `yaml:"tenant_id" json:"tenant_id"`
	SiteID        uuid.UUID    `yaml:"site_id" json:"site_id"`
	RealmID       uuid.UUID    `yaml:"realm_id" json:"realm_id"`
	Prefix        netip.Prefix `yaml:"prefix" json:"prefix"`
	Role          string       `yaml:"role" json:"role"`
	DefaultKind   string       `yaml:"default_kind" json:"default_kind"`
	IPv6ClientLen int          `yaml:"ipv6_client_len" json:"ipv6_client_len"`
}

// Tenant guarda los datos del ISP que usa flows.
type Tenant struct {
	ID   uuid.UUID `yaml:"id" json:"id"`
	ASNs []uint32  `yaml:"asns" json:"asns"`
}

// CustomerKey es la clave natural de un cliente conocido.
type CustomerKey struct {
	TenantID uuid.UUID  `yaml:"tenant_id" json:"tenant_id"`
	RealmID  uuid.UUID  `yaml:"realm_id" json:"realm_id"`
	Address  netip.Addr `yaml:"address" json:"address"`
}

// Data es la forma serializable del inventario (fichero YAML/JSON).
type Data struct {
	Tenants   []Tenant       `yaml:"tenants" json:"tenants"`
	Exporters []Exporter     `yaml:"exporters" json:"exporters"`
	Realms    []Realm        `yaml:"realms" json:"realms"`
	Prefixes  []ClientPrefix `yaml:"client_prefixes" json:"client_prefixes"`
	Customers []CustomerKey  `yaml:"customers" json:"customers"`
}

// Match es el resultado de buscar una IP en los prefijos de un nodo.
type Match struct {
	Prefix *ClientPrefix
	// OtherSite indica que el prefijo es de clientes de otro nodo del mismo
	// tenant (tránsito, §4.3 paso 4).
	OtherSite bool
}

// Snapshot es una vista inmutable del inventario.
type Snapshot struct {
	data      Data
	exporters map[netip.Addr]*Exporter
	byRouter  map[uuid.UUID]*Exporter
	ifaces    map[uuid.UUID]map[uint32]*Interface
	sites     map[uuid.UUID]*iptrie.Table[*ClientPrefix]
	tenants   map[uuid.UUID]*iptrie.Table[*ClientPrefix] // solo customers, todos los nodos
	realms    map[uuid.UUID]*Realm
	tenantASN map[uuid.UUID]map[uint32]bool
	sitePfx   map[uuid.UUID]int
}

// New valida y construye un Snapshot.
func New(d Data) (*Snapshot, error) {
	s := &Snapshot{
		data:      d,
		exporters: map[netip.Addr]*Exporter{},
		byRouter:  map[uuid.UUID]*Exporter{},
		ifaces:    map[uuid.UUID]map[uint32]*Interface{},
		sites:     map[uuid.UUID]*iptrie.Table[*ClientPrefix]{},
		tenants:   map[uuid.UUID]*iptrie.Table[*ClientPrefix]{},
		realms:    map[uuid.UUID]*Realm{},
		tenantASN: map[uuid.UUID]map[uint32]bool{},
		sitePfx:   map[uuid.UUID]int{},
	}
	var errs []error
	for i := range d.Exporters {
		e := &d.Exporters[i]
		if !e.TunnelIP.IsValid() || e.TenantID == uuid.Nil || e.RouterID == uuid.Nil {
			errs = append(errs, fmt.Errorf("exporter %d: tunnel_ip, tenant_id and router_id are required", i))
			continue
		}
		ip := e.TunnelIP.Unmap()
		if _, dup := s.exporters[ip]; dup {
			errs = append(errs, fmt.Errorf("exporter %s: duplicated tunnel_ip", ip))
			continue
		}
		s.exporters[ip] = e
		s.byRouter[e.RouterID] = e
		m := map[uint32]*Interface{}
		for j := range e.Interfaces {
			m[e.Interfaces[j].IfIndex] = &e.Interfaces[j]
		}
		s.ifaces[e.RouterID] = m
	}
	for i := range d.Realms {
		s.realms[d.Realms[i].ID] = &d.Realms[i]
	}
	for _, t := range d.Tenants {
		m := map[uint32]bool{}
		for _, a := range t.ASNs {
			m[a] = true
		}
		s.tenantASN[t.ID] = m
	}
	keep := func(old, _ *ClientPrefix) *ClientPrefix { return old }
	siteB := map[uuid.UUID]*iptrie.Builder[*ClientPrefix]{}
	tenB := map[uuid.UUID]*iptrie.Builder[*ClientPrefix]{}
	for i := range d.Prefixes {
		p := &d.Prefixes[i]
		switch p.Role {
		case RoleCustomers, RoleInfrastructure, RoleExcluded:
		default:
			errs = append(errs, fmt.Errorf("prefix %s: unknown role %q", p.Prefix, p.Role))
			continue
		}
		if !p.Prefix.IsValid() {
			errs = append(errs, fmt.Errorf("prefix %d: invalid prefix", i))
			continue
		}
		if p.IPv6ClientLen == 0 {
			p.IPv6ClientLen = DefaultIPv6ClientLen
		}
		if b := siteB[p.SiteID]; b == nil {
			siteB[p.SiteID] = iptrie.NewBuilder(keep)
		}
		if err := siteB[p.SiteID].Insert(p.Prefix, p); err != nil {
			errs = append(errs, fmt.Errorf("prefix %s: %w", p.Prefix, err))
			continue
		}
		s.sitePfx[p.SiteID]++
		if p.Role == RoleCustomers {
			if b := tenB[p.TenantID]; b == nil {
				tenB[p.TenantID] = iptrie.NewBuilder(keep)
			}
			_ = tenB[p.TenantID].Insert(p.Prefix, p)
		}
	}
	for k, b := range siteB {
		s.sites[k] = b.Build()
	}
	for k, b := range tenB {
		s.tenants[k] = b.Build()
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return s, nil
}

// Data devuelve los datos del snapshot (no modificar).
func (s *Snapshot) Data() Data { return s.data }

// Exporter devuelve el exportador con esa IP de túnel.
func (s *Snapshot) Exporter(ip netip.Addr) (*Exporter, bool) {
	e, ok := s.exporters[ip.Unmap()]
	return e, ok
}

// Router devuelve el exportador de un router.
func (s *Snapshot) Router(id uuid.UUID) (*Exporter, bool) {
	e, ok := s.byRouter[id]
	return e, ok
}

// Interface devuelve el rol de un ifIndex de un router.
func (s *Snapshot) Interface(router uuid.UUID, ifIndex uint32) (*Interface, bool) {
	i, ok := s.ifaces[router][ifIndex]
	return i, ok
}

// Realm devuelve un realm.
func (s *Snapshot) Realm(id uuid.UUID) (*Realm, bool) {
	r, ok := s.realms[id]
	return r, ok
}

// SitePrefixCount devuelve cuántos prefijos tiene un nodo (0 = modo descubrimiento).
func (s *Snapshot) SitePrefixCount(site uuid.UUID) int { return s.sitePfx[site] }

// TenantASN indica si asn es un ASN propio del tenant.
func (s *Snapshot) TenantASN(tenant uuid.UUID, asn uint32) bool { return s.tenantASN[tenant][asn] }

// Lookup busca ip en los prefijos del nodo site; si no está, en los prefijos
// de clientes de otros nodos del tenant (tránsito).
func (s *Snapshot) Lookup(tenant, site uuid.UUID, ip netip.Addr) (Match, bool) {
	if t := s.sites[site]; t != nil {
		if _, p, ok := t.Lookup(ip); ok {
			return Match{Prefix: p}, true
		}
	}
	if t := s.tenants[tenant]; t != nil {
		if _, p, ok := t.Lookup(ip); ok && p.SiteID != site {
			return Match{Prefix: p, OtherSite: true}, true
		}
	}
	return Match{}, false
}

// ExcludedFor indica si ip cae en un prefijo excluded del nodo.
func (s *Snapshot) ExcludedFor(site uuid.UUID, ip netip.Addr) bool {
	t := s.sites[site]
	if t == nil {
		return false
	}
	_, p, ok := t.Lookup(ip)
	return ok && p.Role == RoleExcluded
}

// Parse lee un inventario YAML (o JSON, que es YAML válido).
func Parse(b []byte) (*Snapshot, error) {
	var d Data
	if err := yaml.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("flowinv: %w", err)
	}
	return New(d)
}

// LoadFile lee un inventario de disco.
func LoadFile(path string) (*Snapshot, error) {
	b, err := os.ReadFile(path) //nolint:gosec // ruta de configuración
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Store guarda el snapshot vigente.
type Store struct {
	cur atomic.Pointer[Snapshot]
}

// NewStore crea un Store con un snapshot inicial (vacío si nil).
func NewStore(s *Snapshot) *Store {
	st := &Store{}
	if s == nil {
		s, _ = New(Data{})
	}
	st.cur.Store(s)
	return st
}

// Load devuelve el snapshot vigente.
func (st *Store) Load() *Snapshot { return st.cur.Load() }

// Swap sustituye el snapshot vigente.
func (st *Store) Swap(s *Snapshot) { st.cur.Store(s) }
