package flowinv

import (
	"net/netip"
	"testing"

	"github.com/google/uuid"
)

const sample = `
tenants:
  - id: 0192e000-0000-7000-8000-000000000001
    asns: [64500]
exporters:
  - tenant_id: 0192e000-0000-7000-8000-000000000001
    router_id: 0192e333-0000-7000-8000-000000000033
    site_id: 0192e222-0000-7000-8000-000000000022
    tunnel_ip: 10.255.3.17
    interfaces: [{if_index: 2, flow_role: upstream}]
realms:
  - {id: 0192e111-0000-7000-8000-000000000011, tenant_id: 0192e000-0000-7000-8000-000000000001, kind: node_private, site_id: 0192e222-0000-7000-8000-000000000022}
client_prefixes:
  - {id: 0192e444-0000-7000-8000-000000000044, tenant_id: 0192e000-0000-7000-8000-000000000001, site_id: 0192e222-0000-7000-8000-000000000022, realm_id: 0192e111-0000-7000-8000-000000000011, prefix: 10.20.0.0/16, role: customers}
  - {id: 0192e444-0000-7000-8000-000000000045, tenant_id: 0192e000-0000-7000-8000-000000000001, site_id: 0192e222-0000-7000-8000-000000000022, realm_id: 0192e111-0000-7000-8000-000000000011, prefix: 10.20.9.0/24, role: excluded}
  - {id: 0192e444-0000-7000-8000-000000000046, tenant_id: 0192e000-0000-7000-8000-000000000001, site_id: 0192e222-0000-7000-8000-000000000099, realm_id: 0192e111-0000-7000-8000-000000000012, prefix: 10.30.0.0/16, role: customers}
`

func TestParseAndLookup(t *testing.T) {
	s, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := s.Exporter(netip.MustParseAddr("10.255.3.17"))
	if !ok || e.SiteID.String() != "0192e222-0000-7000-8000-000000000022" {
		t.Fatalf("exporter %+v", e)
	}
	if i, ok := s.Interface(e.RouterID, 2); !ok || i.FlowRole != FlowRoleUpstream {
		t.Fatal("interface")
	}
	m, ok := s.Lookup(e.TenantID, e.SiteID, netip.MustParseAddr("10.20.1.1"))
	if !ok || m.Prefix.Role != RoleCustomers || m.OtherSite || m.Prefix.IPv6ClientLen != 64 {
		t.Fatalf("lookup %+v", m)
	}
	if !s.ExcludedFor(e.SiteID, netip.MustParseAddr("10.20.9.4")) {
		t.Fatal("excluded")
	}
	m, ok = s.Lookup(e.TenantID, e.SiteID, netip.MustParseAddr("10.30.1.1"))
	if !ok || !m.OtherSite {
		t.Fatalf("transit %+v", m)
	}
	if _, ok := s.Lookup(uuid.New(), e.SiteID, netip.MustParseAddr("10.30.1.1")); ok {
		t.Fatal("other tenant must not match other-site prefixes")
	}
	if !s.TenantASN(e.TenantID, 64500) || s.SitePrefixCount(e.SiteID) != 2 {
		t.Fatal("asn/count")
	}
}

func TestInvalid(t *testing.T) {
	if _, err := Parse([]byte("client_prefixes: [{prefix: 10.0.0.0/8, role: bogus}]")); err == nil {
		t.Fatal("expected error")
	}
}
