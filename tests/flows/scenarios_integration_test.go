//go:build integration

package flows_test

import (
	"encoding/json"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/catalog"
)

type simExpected struct {
	Start     time.Time `json:"start"`
	Exporters []struct {
		ExporterIP    string `json:"exporter_ip"`
		IPv6ClientLen int    `json:"ipv6_client_len"`
		Prefixes      struct {
			Customers      []string `json:"customers"`
			Infrastructure []string `json:"infrastructure"`
			Excluded       []string `json:"excluded"`
		} `json:"prefixes"`
		Totals struct {
			DataRecords int `json:"data_records"`
		} `json:"totals"`
		ByStatus map[string]int `json:"by_status"`
		Clients  []struct {
			Key       string `json:"key"`
			BytesUp   uint64 `json:"bytes_up"`
			BytesDown uint64 `json:"bytes_down"`
		} `json:"clients"`
	} `json:"exporters"`
}

// shift traslada los tiempos de los lotes al presente: los fixtures del
// simulador son de enero de 2026 y flows_raw tiene TTL de 7 días.
func shift(d time.Duration) func(*nats.Msg) *nats.Msg {
	return func(m *nats.Msg) *nats.Msg {
		if !strings.HasPrefix(m.Subject, flowbus.SubjectBatchPrefix) {
			return m
		}
		fb, err := flowpb.UnmarshalFlowBatch(m.Data)
		if err != nil {
			return m
		}
		for i := range fb.Records {
			fb.Records[i].TS = fb.Records[i].TS.Add(d)
			fb.Records[i].FlowStart = fb.Records[i].FlowStart.Add(d)
		}
		m.Data = fb.Marshal()
		return m
	}
}

// TestScenarioNormalEnrichment: el escenario `normal` del simulador (IPFIX,
// IPv4 con NAT + IPv6 con prefijo delegado) atribuye como el verificador y
// ≥ 95 % de los bytes remotos tienen ASN; los destinos de servicios
// conocidos quedan con su servicio y categoría (I1-07 criterio 1).
func TestScenarioNormalEnrichment(t *testing.T) {
	base := repo("tools", "flowsim", "fixtures", "sim", "normal")
	var exp simExpected
	b, err := os.ReadFile(base + "/ipfix.expected.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &exp); err != nil {
		t.Fatal(err)
	}
	ex := exp.Exporters[0]
	tenant, site, router, realmPriv, realmPub := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	d := flowinv.Data{
		Exporters: []flowinv.Exporter{{TenantID: tenant, RouterID: router, SiteID: site, TunnelIP: netip.MustParseAddr(ex.ExporterIP)}},
		Realms: []flowinv.Realm{{ID: realmPriv, TenantID: tenant, Kind: flowinv.RealmNodePrivate, SiteID: site},
			{ID: realmPub, TenantID: tenant, Kind: flowinv.RealmPublic}},
	}
	add := func(list []string, role string) {
		for _, s := range list {
			pf := netip.MustParsePrefix(s)
			realm := realmPub
			if pf.Addr().IsPrivate() {
				realm = realmPriv
			}
			d.Prefixes = append(d.Prefixes, flowinv.ClientPrefix{ID: uuid.New(), TenantID: tenant, SiteID: site,
				RealmID: realm, Prefix: pf, Role: role, IPv6ClientLen: ex.IPv6ClientLen})
		}
	}
	add(ex.Prefixes.Customers, flowinv.RoleCustomers)
	add(ex.Prefixes.Infrastructure, flowinv.RoleInfrastructure)
	add(ex.Prefixes.Excluded, flowinv.RoleExcluded)
	p := startPipeline(t, d, tenant)
	p.replay(base+"/ipfix.hfsim.gz", shift(time.Since(exp.Start).Truncate(time.Hour)))
	p.waitCount(ex.Totals.DataRecords - ex.ByStatus["tunnel"] - ex.ByStatus["excluded"])

	got := p.counts("SELECT toString(attribution_status), count() FROM flows.flows_raw WHERE tenant_id = ? GROUP BY attribution_status")
	for k, v := range ex.ByStatus {
		if k != "tunnel" && k != "excluded" && got[k] != v {
			t.Errorf("attribution_status %s = %d, want %d", k, got[k], v)
		}
	}
	rows, err := p.db.Query(`SELECT IPv6NumToString(client_ip), sumIf(bytes, direction IN ('upload','internal')), sumIf(bytes, direction = 'download')
		FROM flows.flows_raw WHERE tenant_id = ? AND attribution_status IN ('attributed','internal') GROUP BY client_ip`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	type ud struct{ up, down uint64 }
	clients := map[string]ud{}
	for rows.Next() {
		var ip string
		var up, down uint64
		if err := rows.Scan(&ip, &up, &down); err != nil {
			t.Fatal(err)
		}
		a := netip.MustParseAddr(ip).Unmap()
		key := a.String()
		if a.Is6() {
			key = netip.PrefixFrom(a, ex.IPv6ClientLen).String()
		}
		clients[key] = ud{up, down}
	}
	_ = rows.Close()
	if len(clients) != len(ex.Clients) {
		t.Errorf("clients = %d, want %d", len(clients), len(ex.Clients))
	}
	for _, c := range ex.Clients {
		if g := clients[c.Key]; g.up != c.BytesUp || g.down != c.BytesDown {
			t.Errorf("client %s: got %+v want up=%d down=%d", c.Key, g, c.BytesUp, c.BytesDown)
		}
	}

	rr, err := p.db.Query(`SELECT IPv6NumToString(remote_ip), remote_asn, service_id, sum(bytes) FROM flows.flows_raw
		WHERE tenant_id = ? GROUP BY remote_ip, remote_asn, service_id`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	var total, withASN uint64
	svc := map[string]uuid.UUID{}
	for rr.Next() {
		var ip string
		var asn uint32
		var s uuid.UUID
		var b uint64
		if err := rr.Scan(&ip, &asn, &s, &b); err != nil {
			t.Fatal(err)
		}
		a := netip.MustParseAddr(ip).Unmap()
		// Solo IPs remotas de Internet (fuera de rangos privados y de documentación).
		if !a.IsGlobalUnicast() || a.IsPrivate() || netip.MustParsePrefix("100.64.0.0/10").Contains(a) ||
			netip.MustParsePrefix("2001:db8::/32").Contains(a) || netip.MustParsePrefix("203.0.113.0/24").Contains(a) {
			continue
		}
		total += b
		if asn != 0 {
			withASN += b
		}
		svc[a.String()] = s
	}
	_ = rr.Close()
	ratio := float64(withASN) / float64(max(total, 1))
	t.Logf("cobertura ASN: %.2f %% de %d bytes remotos", 100*ratio, total)
	if total == 0 || ratio < 0.95 {
		t.Fatalf("ASN coverage %.2f %%, want ≥ 95 %%", 100*ratio)
	}
	checked := 0
	for ip, s := range svc {
		a := netip.MustParseAddr(ip)
		var want string
		switch {
		case netip.MustParsePrefix("45.57.0.0/17").Contains(a), netip.MustParsePrefix("198.38.96.0/19").Contains(a):
			want = "netflix"
		case netip.MustParsePrefix("162.159.200.0/24").Contains(a):
			want = "ntp"
		case netip.MustParsePrefix("1.1.1.0/24").Contains(a):
			want = "cloudflare_dns"
		default:
			continue
		}
		checked++
		if s != catalog.ID("service", want) {
			t.Errorf("remote %s: service %s, want %s", ip, s, want)
		}
	}
	if checked == 0 {
		t.Fatal("no known-service destinations in the scenario")
	}
}
