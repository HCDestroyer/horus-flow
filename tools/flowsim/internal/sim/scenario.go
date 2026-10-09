// Package sim genera tráfico de flujos determinista a partir de escenarios
// YAML (tools/flowsim/scenarios, contrato C10): clientes residenciales y
// comerciales, bots y tráfico del propio router, pasados por un emulador de la
// caché de Traffic Flow de RouterOS 7 (timeouts activo/inactivo) y codificados
// en IPFIX o NetFlow v9. Produce además el expected.json del escenario.
package sim

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/flow"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/scenarios"
)

// Load carga un escenario embebido por nombre (normal, scan…) o un fichero
// YAML por ruta.
func Load(nameOrPath string) (*Scenario, error) {
	var data []byte
	var err error
	if strings.HasSuffix(nameOrPath, ".yaml") || strings.ContainsRune(nameOrPath, os.PathSeparator) {
		data, err = os.ReadFile(nameOrPath) //nolint:gosec // ruta indicada por quien ejecuta la herramienta
		if err != nil {
			return nil, fmt.Errorf("escenario: %w", err)
		}
	} else {
		data, err = scenarios.FS.ReadFile(nameOrPath + ".yaml")
		if err != nil {
			return nil, fmt.Errorf("escenario %q desconocido (disponibles: %s)", nameOrPath, strings.Join(scenarios.Names(), ", "))
		}
	}
	return Parse(data)
}

// Duration es una duración legible en YAML ("5m", "15s").
type Duration time.Duration

// UnmarshalYAML implementa yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return fmt.Errorf("duración: %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("duración %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML implementa yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// D devuelve la duración como time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// Scenario es un escenario de simulación.
type Scenario struct {
	Name        string          `yaml:"name"`
	Description string          `yaml:"description"`
	Start       time.Time       `yaml:"start"`
	Duration    Duration        `yaml:"duration"`
	Rate        float64         `yaml:"rate"`
	NAT         bool            `yaml:"nat"`
	IPv6        bool            `yaml:"ipv6"`
	Fixture     FixtureSpec     `yaml:"fixture"`
	Export      ExportSpec      `yaml:"export"`
	Indicators  []IndicatorSpec `yaml:"indicators"`
	Exporters   []ExporterSpec  `yaml:"-"`
}

// FixtureSpec fija duración y tasa reducidas para grabar fixtures de CI.
type FixtureSpec struct {
	Duration Duration `yaml:"duration"`
	Rate     float64  `yaml:"rate"`
}

// ExportSpec son los parámetros de Traffic Flow (docs/vendors/mikrotik.md §2.2).
type ExportSpec struct {
	ActiveTimeout   Duration `yaml:"active_timeout"`
	InactiveTimeout Duration `yaml:"inactive_timeout"`
	TemplateRefresh int      `yaml:"template_refresh"`
	TemplateTimeout Duration `yaml:"template_timeout"`
	MaxDatagram     int      `yaml:"max_datagram"`
}

// IndicatorSpec es un indicador del feed de reputación de prueba.
type IndicatorSpec struct {
	IP         netip.Addr `yaml:"ip"`
	Kind       string     `yaml:"kind"`
	Confidence string     `yaml:"confidence"`
}

// PrefixSet son los prefijos de un nodo para un modo de direccionamiento.
type PrefixSet struct {
	Customers      []netip.Prefix `yaml:"customers"`
	Infrastructure []netip.Prefix `yaml:"infrastructure"`
	Excluded       []netip.Prefix `yaml:"excluded"`
	// Unlisted son rangos que aparecen del lado cliente pero no están dados
	// de alta (modo descubrimiento / IPs fuera de prefijos).
	Unlisted []netip.Prefix `yaml:"unlisted"`
	Resolver netip.Addr     `yaml:"resolver"`
}

// Prefixes agrupa los prefijos IPv4 con NAT, IPv4 públicos e IPv6.
type Prefixes struct {
	NAT    PrefixSet `yaml:"nat"`
	Public PrefixSet `yaml:"public"`
	V6     PrefixSet `yaml:"v6"`
}

// InterfaceSpec son los ifIndex del router simulado.
type InterfaceSpec struct {
	Upstream uint32 `yaml:"upstream"`
	Tunnel   uint32 `yaml:"tunnel"`
	Transit  uint32 `yaml:"transit"`
	// Access: "vlan" (un ifIndex para todos los clientes, MAC del CPE
	// visible) o "pppoe" (un ifIndex dinámico por cliente, sin MAC).
	Access        string `yaml:"access"`
	AccessIfIndex uint32 `yaml:"access_ifindex"`
	PPPoEBase     uint32 `yaml:"pppoe_ifindex_base"`
}

// ExporterSpec es un router principal de nodo que exporta flujos.
type ExporterSpec struct {
	Name                string     `yaml:"name"`
	ExporterIP          netip.Addr `yaml:"exporter_ip"`
	CollectorIP         netip.Addr `yaml:"collector_ip"`
	ObservationDomainID uint32     `yaml:"observation_domain_id"`
	Uptime              Duration   `yaml:"uptime"`
	WANIP               netip.Addr `yaml:"wan_ip"`
	// NATIPs son las IPs públicas del NAT del router principal (srcnat con
	// varias direcciones; la captura real usa 3). Cada cliente sale siempre
	// por la misma.
	NATIPs        []netip.Addr  `yaml:"nat_ips"`
	Gateway       netip.Addr    `yaml:"gateway"`
	GatewayV6     netip.Addr    `yaml:"gateway_v6"`
	HubIP         netip.Addr    `yaml:"hub_ip"`
	Interfaces    InterfaceSpec `yaml:"interfaces"`
	Prefixes      Prefixes      `yaml:"prefixes"`
	IPv6ClientLen int           `yaml:"ipv6_client_len"`
	RouterTraffic bool          `yaml:"router_traffic"`
	Populations   []Population  `yaml:"populations"`
}

// Population es un grupo de clientes con el mismo comportamiento.
type Population struct {
	Name  string `yaml:"name"`
	Count int    `yaml:"count"`
	// Range: customers (defecto), excluded, unlisted o customers@<exportador>
	// (tránsito: clientes de otro nodo vistos en este router).
	Range string `yaml:"range"`
	// Family: v4 (defecto; ipv6_share de ellos también con IPv6) o v6.
	Family    string         `yaml:"family"`
	IPv6Share float64        `yaml:"ipv6_share"`
	Kind      string         `yaml:"kind"`
	Behaviors []BehaviorSpec `yaml:"behaviors"`
	Expect    ExpectSpec     `yaml:"expect"`
}

// ExpectSpec declara lo que el escenario promete para cada cliente de la
// población.
type ExpectSpec struct {
	Signals  []string      `yaml:"signals"`
	Findings []FindingSpec `yaml:"findings"`
}

// FindingSpec es un hallazgo esperado (kinds provisionales hasta C8).
type FindingSpec struct {
	Kind     string   `yaml:"kind"`
	Severity string   `yaml:"severity"`
	Signals  []string `yaml:"signals"`
}

// BehaviorSpec es un comportamiento: un mapa de una sola clave
// (`- scan: {rate: 10}`).
type BehaviorSpec struct {
	Kind   string
	Params yaml.Node
}

// UnmarshalYAML implementa yaml.Unmarshaler.
func (b *BehaviorSpec) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		b.Kind = n.Value
		return nil
	}
	if n.Kind != yaml.MappingNode || len(n.Content) != 2 {
		return fmt.Errorf("línea %d: un comportamiento es un mapa de una sola clave", n.Line)
	}
	b.Kind = n.Content[0].Value
	b.Params = *n.Content[1]
	return nil
}

// decodeStrict decodifica un nodo rechazando claves desconocidas.
func decodeStrict(n *yaml.Node, out any) error {
	if n.Kind == 0 {
		return nil
	}
	raw, err := yaml.Marshal(n)
	if err != nil {
		return fmt.Errorf("yaml: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("yaml (línea %d): %w", n.Line, err)
	}
	return nil
}

func mp(s string) netip.Prefix { return netip.MustParsePrefix(s) }
func ma(s string) netip.Addr   { return netip.MustParseAddr(s) }

// defaultExporter devuelve los valores por defecto del exportador i. Las
// direcciones públicas son de documentación (RFC 5737/3849) o de pruebas
// (198.18.0.0/15, RFC 2544) para no usar IPs de clientes reales.
func defaultExporter(i int) ExporterSpec {
	n := byte(i)
	return ExporterSpec{
		Name:        fmt.Sprintf("node-%c", 'a'+i),
		ExporterIP:  netip.AddrFrom4([4]byte{10, 255, 0, 2 + n}),
		CollectorIP: ma("10.255.0.1"),
		Uptime:      Duration(72*time.Hour + time.Duration(i)*time.Hour),
		WANIP:       netip.AddrFrom4([4]byte{203, 0, 113, 2 + n}),
		NATIPs: []netip.Addr{
			netip.AddrFrom4([4]byte{203, 0, 113, 10 + 3*n}),
			netip.AddrFrom4([4]byte{203, 0, 113, 11 + 3*n}),
			netip.AddrFrom4([4]byte{203, 0, 113, 12 + 3*n}),
		},
		Gateway:       ma("203.0.113.1"),
		GatewayV6:     netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0xff, n, 15: 1}),
		HubIP:         ma("192.0.2.10"),
		IPv6ClientLen: 64,
		RouterTraffic: true,
		Interfaces: InterfaceSpec{
			Upstream: 1, Tunnel: 30, Transit: 3, Access: "vlan", AccessIfIndex: 12, PPPoEBase: 100,
		},
		Prefixes: Prefixes{
			NAT: PrefixSet{
				Customers:      []netip.Prefix{mp("10.20.0.0/24")},
				Infrastructure: []netip.Prefix{mp("10.20.255.0/24"), mp("203.0.113.0/28")},
				Unlisted:       []netip.Prefix{mp("172.16.50.0/24"), mp("192.168.88.0/24")},
				Resolver:       ma("10.20.255.53"),
			},
			Public: PrefixSet{
				Customers:      []netip.Prefix{netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 18, 2 * n, 0}), 23)},
				Infrastructure: []netip.Prefix{netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 19, 255 - n, 0}), 24), mp("203.0.113.0/28")},
				Unlisted:       []netip.Prefix{netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 19, n, 0}), 24)},
				Resolver:       netip.AddrFrom4([4]byte{198, 19, 255 - n, 53}),
			},
			V6: PrefixSet{
				Customers:      []netip.Prefix{netip.PrefixFrom(netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0x10 + n}), 40)},
				Infrastructure: []netip.Prefix{netip.PrefixFrom(netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0xff, n}), 48)},
				Unlisted:       []netip.Prefix{netip.PrefixFrom(netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0xf0, n}), 48)},
				Resolver:       netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0xff, n, 15: 0x53}),
			},
		},
	}
}

type rawScenario struct {
	Scenario  `yaml:",inline"`
	Exporters []yaml.Node `yaml:"exporters"`
}

// Parse lee un escenario YAML, aplica valores por defecto y lo valida.
func Parse(data []byte) (*Scenario, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("escenario: %w", err)
	}
	if len(root.Content) == 0 {
		return nil, errors.New("escenario vacío")
	}
	raw := rawScenario{Scenario: Scenario{
		Start:    time.Date(2026, 1, 5, 18, 0, 0, 0, time.UTC),
		Duration: Duration(5 * time.Minute),
		Rate:     200,
		NAT:      true,
		IPv6:     true,
		Export: ExportSpec{
			ActiveTimeout:   Duration(time.Minute),
			InactiveTimeout: Duration(15 * time.Second),
			TemplateRefresh: 20,
			TemplateTimeout: Duration(time.Minute),
			MaxDatagram:     1392,
		},
	}}
	if err := decodeStrict(root.Content[0], &raw); err != nil {
		return nil, fmt.Errorf("escenario: %w", err)
	}
	sc := raw.Scenario
	for i := range raw.Exporters {
		spec := defaultExporter(i)
		if err := decodeStrict(&raw.Exporters[i], &spec); err != nil {
			return nil, fmt.Errorf("exportador %d: %w", i, err)
		}
		sc.Exporters = append(sc.Exporters, spec)
	}
	if err := sc.validate(); err != nil {
		return nil, fmt.Errorf("escenario %q: %w", sc.Name, err)
	}
	return &sc, nil
}

func (sc *Scenario) validate() error {
	if sc.Name == "" {
		return errors.New("falta name")
	}
	if sc.Duration <= 0 || sc.Rate < 0 {
		return errors.New("duration debe ser > 0 y rate >= 0")
	}
	if len(sc.Exporters) == 0 {
		return errors.New("falta al menos un exportador")
	}
	e := sc.Export
	if e.ActiveTimeout <= 0 || e.InactiveTimeout <= 0 || e.TemplateRefresh <= 0 || e.TemplateTimeout <= 0 {
		return errors.New("export: timeouts y template_refresh deben ser > 0")
	}
	if e.InactiveTimeout.D() < time.Second {
		return errors.New("export: inactive_timeout debe ser >= 1s")
	}
	names := map[string]bool{}
	for _, ex := range sc.Exporters {
		if names[ex.Name] {
			return fmt.Errorf("exportador %q duplicado", ex.Name)
		}
		names[ex.Name] = true
		if !ex.ExporterIP.IsValid() || !ex.CollectorIP.IsValid() {
			return fmt.Errorf("%s: exporter_ip y collector_ip son obligatorias", ex.Name)
		}
		if ex.Interfaces.Access != "vlan" && ex.Interfaces.Access != "pppoe" {
			return fmt.Errorf("%s: interfaces.access debe ser vlan o pppoe", ex.Name)
		}
		if len(ex.NATIPs) == 0 {
			return fmt.Errorf("%s: nat_ips necesita al menos una IP", ex.Name)
		}
		for _, a := range ex.NATIPs {
			if !a.Is4() || flow.IsClientPrivate(a) {
				return fmt.Errorf("%s: nat_ips %s debe ser una IPv4 pública", ex.Name, a)
			}
		}
		if ex.IPv6ClientLen != 64 && ex.IPv6ClientLen != 56 && ex.IPv6ClientLen != 48 {
			return fmt.Errorf("%s: ipv6_client_len debe ser 48, 56 o 64", ex.Name)
		}
		for _, set := range []PrefixSet{ex.Prefixes.NAT, ex.Prefixes.Public} {
			if !containsAny(set.Infrastructure, ex.WANIP) {
				return fmt.Errorf("%s: wan_ip %s debe estar en los prefijos de infraestructura", ex.Name, ex.WANIP)
			}
			if !containsAny(set.Infrastructure, set.Resolver) {
				return fmt.Errorf("%s: resolver %s debe estar en los prefijos de infraestructura", ex.Name, set.Resolver)
			}
		}
		for _, p := range ex.Populations {
			if p.Count <= 0 {
				return fmt.Errorf("%s/%s: count debe ser > 0", ex.Name, p.Name)
			}
			if p.Family != "" && p.Family != "v4" && p.Family != "v6" {
				return fmt.Errorf("%s/%s: family debe ser v4 o v6", ex.Name, p.Name)
			}
			if r, ok := strings.CutPrefix(p.Range, "customers@"); ok && !names[r] && !hasExporter(sc.Exporters, r) {
				return fmt.Errorf("%s/%s: exportador %q desconocido en range", ex.Name, p.Name, r)
			}
			switch {
			case p.Range == "", p.Range == "customers", p.Range == "excluded", p.Range == "unlisted",
				strings.HasPrefix(p.Range, "customers@"):
			default:
				return fmt.Errorf("%s/%s: range %q inválido", ex.Name, p.Name, p.Range)
			}
			if len(p.Behaviors) == 0 {
				return fmt.Errorf("%s/%s: falta behaviors", ex.Name, p.Name)
			}
		}
	}
	return nil
}

func hasExporter(es []ExporterSpec, name string) bool {
	for _, e := range es {
		if e.Name == name {
			return true
		}
	}
	return false
}

func containsAny(ps []netip.Prefix, a netip.Addr) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
