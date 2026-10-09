// Package catalog es el contrato público del catálogo de clasificación de
// tráfico de `mod:traffic` (docs/traffic-model.md §6; I1-07): categorías,
// servicios, organizaciones con sus ASN, rangos publicados y reglas
// deterministas. El ingester lo carga en memoria (snapshot versionado,
// recarga en caliente) y guarda en cada fila service_id, category_id,
// classification_method/confidence y catalog_version; analytics lo usa para
// los nombres (dim.service, dim.category, dim.organization).
package catalog

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/iptrie"
)

// Kind es el tipo de snapshot en el contenedor de datasets.
const Kind = "catalog"

// PayloadFormat es la versión del payload (JSON de Definition).
const PayloadFormat = 1

// Métodos de clasificación (Enum8 classification_method de flows_raw).
const (
	MethodNone    = "none"
	MethodPrefix  = "prefix"
	MethodASNPort = "asn_port"
	MethodASN     = "asn"
	MethodPort    = "port"
)

// Namespace de los UUID deterministas del catálogo (UUIDv5).
var Namespace = uuid.MustParse("6f1d7c2a-4b8e-5d3f-9a10-3c5e7b9d1f20")

// ID devuelve el UUID determinista de una entidad del catálogo (kind: service, category, org).
func ID(kind, slug string) uuid.UUID { return uuid.NewSHA1(Namespace, []byte(kind+":"+slug)) }

// OrgIDForASN es el remote_org_id de un ASN sin organización en el catálogo.
func OrgIDForASN(asn uint32) uuid.UUID { return ID("asn", strconv.FormatUint(uint64(asn), 10)) }

// Category es una categoría.
type Category struct {
	Slug string `yaml:"slug" json:"slug"`
	Name string `yaml:"name" json:"name"`
}

// Organization es una organización con sus ASN.
type Organization struct {
	Slug    string   `yaml:"slug" json:"slug"`
	Name    string   `yaml:"name" json:"name"`
	Country string   `yaml:"country" json:"country"`
	ASNs    []uint32 `yaml:"asns" json:"asns"`
}

// Rule es una regla de un servicio: ASN (+ puertos/protocolos) o solo puertos.
type Rule struct {
	ASN        []uint32 `yaml:"asn" json:"asn,omitempty"`
	Ports      []uint16 `yaml:"ports" json:"ports,omitempty"`
	Protocols  []uint8  `yaml:"protocols" json:"protocols,omitempty"`
	Confidence uint8    `yaml:"confidence" json:"confidence"`
}

// Service es un servicio.
type Service struct {
	Slug     string `yaml:"slug" json:"slug"`
	Name     string `yaml:"name" json:"name"`
	Category string `yaml:"category" json:"category"`
	Org      string `yaml:"org" json:"org,omitempty"`
	Rules    []Rule `yaml:"rules" json:"rules,omitempty"`
}

// Prefix es un rango publicado: prefijo → ASN y servicio.
type Prefix struct {
	Prefix  netip.Prefix `yaml:"prefix" json:"prefix"`
	ASN     uint32       `yaml:"asn" json:"asn"`
	Service string       `yaml:"service" json:"service"`
}

// Definition es el catálogo serializable.
type Definition struct {
	Version       uint32         `yaml:"version" json:"version"`
	Categories    []Category     `yaml:"categories" json:"categories"`
	Organizations []Organization `yaml:"organizations" json:"organizations"`
	Services      []Service      `yaml:"services" json:"services"`
	Prefixes      []Prefix       `yaml:"prefixes" json:"prefixes"`
}

//go:embed seed.yaml
var seed []byte

// SeedDefinition devuelve el catálogo semilla embebido.
func SeedDefinition() (Definition, error) {
	var d Definition
	if err := yaml.Unmarshal(seed, &d); err != nil {
		return d, fmt.Errorf("catalog seed: %w", err)
	}
	return d, nil
}

// Seed construye el catálogo semilla.
func Seed() (*Catalog, error) {
	d, err := SeedDefinition()
	if err != nil {
		return nil, err
	}
	return New(d)
}

// Result es una clasificación.
type Result struct {
	ServiceID  uuid.UUID
	CategoryID uuid.UUID
	Method     string
	Confidence uint8
}

type portRule struct {
	service    *Service
	asns       []uint32
	ports      []uint16
	protos     []uint8
	confidence uint8
}

// Catalog es un catálogo inmutable listo para clasificar (seguro en concurrencia).
type Catalog struct {
	Def      Definition
	prefixes *iptrie.Table[*Prefix]
	services map[string]*Service
	asnRules map[uint32][]portRule // reglas con ASN (con o sin puertos)
	portOnly []portRule
	orgByASN map[uint32]*Organization
}

// New valida y construye un catálogo.
func New(d Definition) (*Catalog, error) {
	c := &Catalog{Def: d, services: map[string]*Service{}, asnRules: map[uint32][]portRule{}, orgByASN: map[uint32]*Organization{}}
	cats := map[string]bool{}
	for _, k := range d.Categories {
		if k.Slug == "" || cats[k.Slug] {
			return nil, fmt.Errorf("catalog: category %q empty or duplicated", k.Slug)
		}
		cats[k.Slug] = true
	}
	orgs := map[string]bool{}
	for i := range d.Organizations {
		o := &c.Def.Organizations[i]
		orgs[o.Slug] = true
		for _, a := range o.ASNs {
			c.orgByASN[a] = o
		}
	}
	for i := range c.Def.Services {
		s := &c.Def.Services[i]
		if s.Slug == "" || c.services[s.Slug] != nil {
			return nil, fmt.Errorf("catalog: service %q empty or duplicated", s.Slug)
		}
		if !cats[s.Category] {
			return nil, fmt.Errorf("catalog: service %s: unknown category %q", s.Slug, s.Category)
		}
		if s.Org != "" && !orgs[s.Org] {
			return nil, fmt.Errorf("catalog: service %s: unknown org %q", s.Slug, s.Org)
		}
		c.services[s.Slug] = s
		for _, r := range s.Rules {
			pr := portRule{service: s, asns: r.ASN, ports: r.Ports, protos: r.Protocols, confidence: r.Confidence}
			if len(r.ASN) == 0 {
				if len(r.Ports) == 0 {
					return nil, fmt.Errorf("catalog: service %s: rule without asn nor ports", s.Slug)
				}
				c.portOnly = append(c.portOnly, pr)
				continue
			}
			for _, a := range r.ASN {
				c.asnRules[a] = append(c.asnRules[a], pr)
			}
		}
	}
	b := iptrie.NewBuilder(func(old, _ *Prefix) *Prefix { return old })
	for i := range c.Def.Prefixes {
		p := &c.Def.Prefixes[i]
		if c.services[p.Service] == nil {
			return nil, fmt.Errorf("catalog: prefix %s: unknown service %q", p.Prefix, p.Service)
		}
		if err := b.Insert(p.Prefix, p); err != nil {
			return nil, fmt.Errorf("catalog: prefix %s: %w", p.Prefix, err)
		}
	}
	c.prefixes = b.Build()
	return c, nil
}

// Version devuelve catalog_version.
func (c *Catalog) Version() uint32 { return c.Def.Version }

// LookupPrefix devuelve el rango publicado que contiene ip (respaldo de ASN).
func (c *Catalog) LookupPrefix(ip netip.Addr) (netip.Prefix, uint32, bool) {
	p, v, ok := c.prefixes.Lookup(ip)
	if !ok {
		return netip.Prefix{}, 0, false
	}
	return p, v.ASN, true
}

// Organization devuelve la organización de un ASN.
func (c *Catalog) Organization(asn uint32) (*Organization, bool) {
	o, ok := c.orgByASN[asn]
	return o, ok
}

func (r portRule) matches(proto uint8, port uint16) bool {
	return (len(r.ports) == 0 || slices.Contains(r.ports, port)) && (len(r.protos) == 0 || slices.Contains(r.protos, proto))
}

func (c *Catalog) result(s *Service, method string, conf uint8) Result {
	return Result{ServiceID: ID("service", s.Slug), CategoryID: ID("category", s.Category), Method: method, Confidence: conf}
}

// Classify clasifica el extremo remoto de un flujo: prefix > asn_port > asn
// > port. Un rango publicado genérico (*_generic) cede ante una regla
// asn_port más específica del mismo ASN.
func (c *Catalog) Classify(remote netip.Addr, asn uint32, proto uint8, remotePort uint16) Result {
	var byPrefix *Prefix
	if remote.IsValid() {
		if _, p, ok := c.prefixes.Lookup(remote); ok {
			byPrefix = p
			if asn == 0 {
				asn = p.ASN
			}
		}
	}
	var asnPort, asnOnly *portRule
	for i := range c.asnRules[asn] {
		r := &c.asnRules[asn][i]
		switch {
		case len(r.ports) > 0 || len(r.protos) > 0:
			if r.matches(proto, remotePort) && (asnPort == nil || r.confidence > asnPort.confidence) {
				asnPort = r
			}
		default:
			if asnOnly == nil || r.confidence > asnOnly.confidence {
				asnOnly = r
			}
		}
	}
	if byPrefix != nil && (asnPort == nil || !strings.HasSuffix(byPrefix.Service, "_generic")) {
		return c.result(c.services[byPrefix.Service], MethodPrefix, 85)
	}
	if asnPort != nil {
		return c.result(asnPort.service, MethodASNPort, asnPort.confidence)
	}
	if asnOnly != nil {
		return c.result(asnOnly.service, MethodASN, asnOnly.confidence)
	}
	var best *portRule
	for i := range c.portOnly {
		r := &c.portOnly[i]
		if r.matches(proto, remotePort) && (best == nil || r.confidence > best.confidence) {
			best = r
		}
	}
	if best != nil {
		return c.result(best.service, MethodPort, best.confidence)
	}
	return Result{Method: MethodNone}
}

// Payload codifica la definición (formato PayloadFormat).
func (c *Catalog) Payload() ([]byte, error) { return json.Marshal(c.Def) }

// Publish guarda el catálogo como nueva versión en d: catalog_version = N de vN.
func (c *Catalog) Publish(d datasets.SnapshotDir, meta datasets.SnapshotMeta) (*datasets.SnapshotManifest, string, error) {
	p, err := c.Payload()
	if err != nil {
		return nil, "", err
	}
	meta.Kind, meta.PayloadFormat = Kind, PayloadFormat
	meta.Entries = len(c.Def.Services)
	return d.Publish(meta, p)
}

// ErrCorrupt indica un snapshot de catálogo inválido.
var ErrCorrupt = errors.New("catalog: corrupt snapshot")

// ReadSnapshot lee un snapshot; catalog_version sale de meta.Version (vN).
func ReadSnapshot(r io.Reader) (*Catalog, error) {
	meta, payload, err := datasets.DecodeSnapshot(r, Kind)
	if err != nil {
		return nil, err
	}
	if meta.PayloadFormat != PayloadFormat {
		return nil, fmt.Errorf("%w: payload format %d", ErrCorrupt, meta.PayloadFormat)
	}
	var d Definition
	if err := json.Unmarshal(payload, &d); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if n, err := strconv.ParseUint(strings.TrimPrefix(meta.Version, "v"), 10, 32); err == nil && n > 0 {
		d.Version = uint32(n)
	}
	return New(d)
}
