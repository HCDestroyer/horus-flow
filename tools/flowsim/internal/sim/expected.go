package sim

import (
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strings"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/expect"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/flow"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/signals"
)

// Expected es el expected.json producido por el generador.
type Expected = expect.Expected

func prefixStrings(ps ...[]netip.Prefix) []string {
	out := []string{}
	for _, l := range ps {
		for _, p := range l {
			out = append(out, p.Masked().String())
		}
	}
	return out
}

func (g *gen) expected() (*Expected, error) {
	e := &Expected{
		Schema:          expect.Schema,
		Scenario:        g.sc.Name,
		Description:     strings.TrimSpace(g.sc.Description),
		Seed:            g.opt.Seed,
		Protocol:        g.opt.Protocol.String(),
		NAT:             g.nat,
		IPv6:            g.ipv6,
		NATFields:       g.natFields,
		TemplateProfile: string(g.tmplOpt.EffectiveProfile(g.opt.Protocol)),
		Fixture:         g.opt.Fixture,
		Rate:            g.rate,
		Start:           g.start,
		DurationSeconds: float64(g.durMs) / 1000,
		Export: expect.ExportParams{
			ActiveTimeoutSeconds:   g.sc.Export.ActiveTimeout.D().Seconds(),
			InactiveTimeoutSeconds: g.sc.Export.InactiveTimeout.D().Seconds(),
			TemplateRefreshPackets: g.sc.Export.TemplateRefresh,
			TemplateTimeoutSeconds: g.sc.Export.TemplateTimeout.D().Seconds(),
			MaxDatagram:            g.sc.Export.MaxDatagram,
			CollectorPort:          g.collPort,
		},
		Indicators: []signals.Indicator{},
		Findings:   []expect.Finding{},
	}
	if g.opt.MaxDatagram > 0 {
		e.Export.MaxDatagram = g.opt.MaxDatagram
	}
	for _, i := range g.sc.Indicators {
		e.Indicators = append(e.Indicators, signals.Indicator{IP: i.IP, Kind: i.Kind, Confidence: i.Confidence, Source: "flowsim-test-feed"})
	}
	for _, es := range g.exps {
		spec := es.spec
		ex := expect.Exporter{
			Name:                spec.Name,
			ExporterIP:          spec.ExporterIP.String(),
			CollectorIP:         spec.CollectorIP.String(),
			ObservationDomainID: spec.ObservationDomainID,
			BootTime:            g.start.Add(-spec.Uptime.D()),
			Interfaces: expect.Interfaces{
				Upstream: spec.Interfaces.Upstream, Tunnel: spec.Interfaces.Tunnel,
				Transit: spec.Interfaces.Transit, Access: spec.Interfaces.Access,
			},
			Prefixes: expect.Prefixes{
				Customers:      prefixStrings(es.v4.Customers, es.v6.Customers),
				Infrastructure: prefixStrings(es.v4.Infrastructure, es.v6.Infrastructure),
				Excluded:       prefixStrings(es.v4.Excluded, es.v6.Excluded),
			},
			IPv6ClientLen: spec.IPv6ClientLen,
		}
		if g.natFields && g.nat {
			for _, a := range spec.NATIPs {
				ex.NATIPs = append(ex.NATIPs, a.String())
			}
		}
		v4t, v6t := es.exp.TemplatesInUse()
		for _, t := range []flow.Template{v4t, v6t} {
			ti := expect.Template{ID: t.ID, Fields: make([][2]uint16, len(t.Fields))}
			for i, f := range t.Fields {
				ti.Fields[i] = [2]uint16{f.ID, f.Len}
			}
			ex.Templates = append(ex.Templates, ti)
		}
		es.tally.Fill(&ex)
		st := es.exp.Stats()
		ex.Totals.Datagrams = st.Datagrams
		ex.Totals.TemplateSends = st.TemplateSends
		if st.DataRecords != ex.Totals.DataRecords {
			return nil, fmt.Errorf("%s: incoherencia interna: %d registros codificados, %d contados", spec.Name, st.DataRecords, ex.Totals.DataRecords)
		}
		// Población y tipo de cada cliente.
		info := map[string]*client{}
		for _, c := range es.clients {
			if c.keyV4 != "" {
				info[c.keyV4] = c
			}
			if c.keyV6 != "" {
				info[c.keyV6] = c
			}
		}
		for i := range ex.Clients {
			if c := info[ex.Clients[i].Key]; c != nil {
				ex.Clients[i].Population = c.pop.Name
				ex.Clients[i].Kind = c.kind
			}
		}
		e.Exporters = append(e.Exporters, ex)
	}
	e.Signals = g.acc.Results()
	if e.Signals == nil {
		e.Signals = []signals.Signal{}
	}
	if err := g.checkExpectations(e); err != nil {
		if !g.opt.AllowUnmet && !g.sc.ReportOnly {
			return nil, err
		}
		e.Warnings = append(e.Warnings, err.Error())
	}
	return e, nil
}

// actorKey es la clave sobre la que actúan los comportamientos de seguridad.
func (c *client) actorKey() string {
	if c.keyV4 != "" {
		return c.keyV4
	}
	return c.keyV6
}

// checkExpectations compara las señales calculadas con las declaradas en el
// escenario y construye los hallazgos esperados.
func (g *gen) checkExpectations(e *Expected) error {
	got := map[string]map[string]signals.Signal{} // exportador|cliente -> señal
	for _, s := range e.Signals {
		k := s.Exporter + "|" + s.Client
		if got[k] == nil {
			got[k] = map[string]signals.Signal{}
		}
		got[k][s.Name] = s
	}
	var problems []string
	declared := map[string]bool{}
	for _, es := range g.exps {
		seen := map[string]bool{}
		for _, ex := range e.Exporters {
			if ex.Name == es.spec.Name {
				for _, c := range ex.Clients {
					seen[c.Key] = true
				}
			}
		}
		for _, c := range es.clients {
			key := c.actorKey()
			k := es.spec.Name + "|" + key
			declared[k] = true
			want := slices.Clone(c.pop.Expect.Signals)
			sort.Strings(want)
			have := make([]string, 0, len(got[k]))
			for name := range got[k] {
				have = append(have, name)
			}
			sort.Strings(have)
			if (len(want) > 0 || len(have) > 0) && !slices.Equal(want, have) {
				problems = append(problems, fmt.Sprintf("%s %s (%s): señales esperadas %v, calculadas %v", es.spec.Name, key, c.pop.Name, want, have))
			}
			if !seen[key] {
				continue
			}
			for _, f := range c.pop.Expect.Findings {
				reasons := map[string]any{}
				for _, sn := range f.Signals {
					if s, ok := got[k][sn]; ok {
						reasons[sn] = s.Detail
					} else {
						problems = append(problems, fmt.Sprintf("%s %s: el hallazgo %s necesita la señal %s", es.spec.Name, key, f.Kind, sn))
					}
				}
				e.Findings = append(e.Findings, expect.Finding{
					Exporter: es.spec.Name, Client: key, Kind: f.Kind, Severity: f.Severity,
					Signals: slices.Clone(f.Signals), Reasons: reasons,
				})
			}
		}
	}
	// Señales en claves que no son de actor (p. ej. la IPv6 de un doble pila).
	for k, m := range got {
		if !declared[k] {
			for name := range m {
				problems = append(problems, fmt.Sprintf("%s: señal inesperada %s", k, name))
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("el escenario %q no cumple sus expectativas con estos parámetros:\n  %s", g.sc.Name, strings.Join(problems, "\n  "))
	}
	return nil
}
