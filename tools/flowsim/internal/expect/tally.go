package expect

import (
	"net/netip"
	"slices"
	"sort"
	"strings"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/flow"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/signals"
)

// Tally acumula registros atribuidos en los conteos de expected.json. Lo usan
// el generador (verdad de terreno) y el verificador (lo decodificado).
type Tally struct {
	recordsV4, recordsV6 uint64
	bytes, packets       uint64
	byStatus             map[string]uint64
	byRule               map[string]uint64
	clients              map[string]*Client
	unattributed         map[netip.Addr]uint64
}

// NewTally crea un acumulador de conteos vacío.
func NewTally() *Tally {
	return &Tally{
		byStatus:     map[string]uint64{},
		byRule:       map[string]uint64{},
		clients:      map[string]*Client{},
		unattributed: map[netip.Addr]uint64{},
	}
}

// Add incorpora un registro atribuido.
func (t *Tally) Add(r *flow.Record, at signals.Attribution) {
	if r.IsV6() {
		t.recordsV6++
	} else {
		t.recordsV4++
	}
	t.bytes += r.Bytes
	t.packets += r.Packets
	t.byStatus[at.Status]++
	if at.Rule != "" {
		t.byRule[at.Rule]++
	}
	switch at.Status {
	case signals.StatusAttributed, signals.StatusInternal:
		c := t.clients[at.Client]
		if c == nil {
			fam := 4
			if strings.Contains(at.Client, ":") {
				fam = 6
			}
			c = &Client{Key: at.Client, Family: fam}
			t.clients[at.Client] = c
		}
		c.Records++
		if at.Upload {
			c.BytesUp += r.Bytes
			c.PacketsUp += r.Packets
		} else {
			c.BytesDown += r.Bytes
			c.PacketsDown += r.Packets
		}
	case signals.StatusUnknown:
		t.unattributed[at.Unattributed]++
	}
}

// Fill vuelca los conteos en un Exporter (sin totales de transporte).
func (t *Tally) Fill(e *Exporter) {
	e.Totals.RecordsV4 = t.recordsV4
	e.Totals.RecordsV6 = t.recordsV6
	e.Totals.DataRecords = t.recordsV4 + t.recordsV6
	e.Totals.Bytes = t.bytes
	e.Totals.Packets = t.packets
	e.ByStatus = map[string]uint64{}
	for k, v := range t.byStatus {
		e.ByStatus[k] = v
	}
	e.ByRule = map[string]uint64{}
	for k, v := range t.byRule {
		e.ByRule[k] = v
	}
	e.Clients = make([]Client, 0, len(t.clients))
	for _, k := range sortedClientKeys(t.clients) {
		e.Clients = append(e.Clients, *t.clients[k])
	}
	ips := make([]netip.Addr, 0, len(t.unattributed))
	for a := range t.unattributed {
		ips = append(ips, a)
	}
	slices.SortFunc(ips, func(a, b netip.Addr) int { return a.Compare(b) })
	e.Unattributed = make([]Unattributed, 0, len(ips))
	for _, a := range ips {
		e.Unattributed = append(e.Unattributed, Unattributed{IP: a.String(), Records: t.unattributed[a]})
	}
}

func sortedClientKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
