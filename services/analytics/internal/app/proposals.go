package app

import (
	"context"
	"net/netip"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/chread"
)

// Querier ejecuta consultas por tenant (chread.Reader).
type Querier interface {
	Query(ctx context.Context, tenant uuid.UUID, sql string, args ...any) (chread.Rows, context.CancelFunc, error)
}

// Service agrupa las consultas de analytics.
type Service struct {
	Q   Querier
	Inv *flowinv.Store
	Now func() time.Time
	// Names resuelve nombres de servicios, categorías y organizaciones.
	Names Names
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Proposal es una propuesta de prefijo del modo descubrimiento (I1-29).
type Proposal struct {
	Prefix        string `json:"prefix"`
	DistinctIPs   int    `json:"distinct_ips"`
	Bytes         string `json:"bytes"`
	SuggestedRole string `json:"suggested_role"`
	Reason        string `json:"reason"`
	bytes         uint64
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// Longitudes de agregación de las propuestas (traffic-model.md §4.1 punto 3, §4.8.6).
const (
	ProposalV4Bits = 24
	ProposalV6Bits = 48
)

// PrefixProposals agrega flows.unattributed_1h del nodo en prefijos /24
// (IPv4) o /48 (IPv6, que llegan truncadas a /64) para confirmar como
// clientes. El ingester solo registra IPs privadas/CGNAT/ULA o de un ASN
// del ISP, así que una IP pública ajena nunca se propone (criterio 3).
func (s *Service) PrefixProposals(ctx context.Context, tenant, site uuid.UUID, r Range) ([]Proposal, error) {
	rows, cancel, err := s.Q.Query(ctx, tenant, `
		SELECT client_ip, sum(bytes) AS b
		FROM flows.unattributed_1h
		WHERE tenant_id = ? AND site_id = ? AND bucket >= toStartOfHour(?) AND bucket < ?
		  AND client_ip != toIPv6('::')
		GROUP BY client_ip`, tenant, site, r.From, r.To)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer func() { _ = rows.Close() }()
	agg := map[netip.Prefix]*Proposal{}
	inv := s.Inv.Load()
	for rows.Next() {
		var ip netip.Addr
		var b uint64
		if err := rows.Scan(&ip, &b); err != nil {
			return nil, err
		}
		ip = ip.Unmap()
		// Una IP que ya cae en un prefijo declarado no se propone.
		if _, ok := inv.Lookup(tenant, site, ip); ok {
			continue
		}
		bits := ProposalV4Bits
		if ip.Is6() {
			bits = ProposalV6Bits
		}
		pf, err := ip.Prefix(bits)
		if err != nil {
			continue
		}
		p := agg[pf]
		if p == nil {
			reason := "isp_public_asn"
			if ip.IsPrivate() || cgnat.Contains(ip) {
				reason = "private_or_cgnat"
			}
			p = &Proposal{Prefix: pf.String(), SuggestedRole: flowinv.RoleCustomers, Reason: reason}
			agg[pf] = p
		}
		p.DistinctIPs++
		p.bytes += b
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Proposal, 0, len(agg))
	for _, p := range agg {
		p.Bytes = strconv.FormatUint(p.bytes, 10)
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].bytes != out[j].bytes {
			return out[i].bytes > out[j].bytes
		}
		return out[i].Prefix < out[j].Prefix
	})
	return out, nil
}
