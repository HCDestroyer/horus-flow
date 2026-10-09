package app

import (
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
)

const invYAML = `
tenants:
  - {id: 0192e000-0000-7000-8000-000000000001, asns: [64500]}
exporters:
  - tenant_id: 0192e000-0000-7000-8000-000000000001
    router_id: 0192e333-0000-7000-8000-000000000033
    site_id: 0192e222-0000-7000-8000-000000000022
    tunnel_ip: 10.255.3.17
    interfaces: [{if_index: 2, flow_role: upstream}, {if_index: 7, flow_role: customer_edge}]
  - tenant_id: 0192e000-0000-7000-8000-000000000002
    router_id: 0192e333-0000-7000-8000-000000000034
    site_id: 0192e222-0000-7000-8000-000000000023
    tunnel_ip: 10.255.3.18
client_prefixes:
  - {id: 0192e444-0000-7000-8000-000000000044, tenant_id: 0192e000-0000-7000-8000-000000000001, site_id: 0192e222-0000-7000-8000-000000000022, realm_id: 0192e111-0000-7000-8000-000000000011, prefix: 10.20.0.0/16, role: customers}
  - {id: 0192e444-0000-7000-8000-000000000045, tenant_id: 0192e000-0000-7000-8000-000000000001, site_id: 0192e222-0000-7000-8000-000000000022, realm_id: 0192e111-0000-7000-8000-000000000012, prefix: 2001:db8:1000::/40, role: customers, ipv6_client_len: 56}
  - {id: 0192e444-0000-7000-8000-000000000046, tenant_id: 0192e000-0000-7000-8000-000000000001, site_id: 0192e222-0000-7000-8000-000000000022, realm_id: 0192e111-0000-7000-8000-000000000011, prefix: 192.0.2.10/31, role: infrastructure}
  - {id: 0192e444-0000-7000-8000-000000000047, tenant_id: 0192e000-0000-7000-8000-000000000001, site_id: 0192e222-0000-7000-8000-000000000099, realm_id: 0192e111-0000-7000-8000-000000000013, prefix: 10.30.0.0/16, role: customers}
  - {id: 0192e444-0000-7000-8000-000000000048, tenant_id: 0192e000-0000-7000-8000-000000000002, site_id: 0192e222-0000-7000-8000-000000000023, realm_id: 0192e111-0000-7000-8000-000000000021, prefix: 10.20.0.0/16, role: customers}
`

func store(t *testing.T) *flowinv.Store {
	t.Helper()
	s, err := flowinv.Parse([]byte(invYAML))
	if err != nil {
		t.Fatal(err)
	}
	return flowinv.NewStore(s)
}

func a(s string) netip.Addr { return netip.MustParseAddr(s) }

func TestAttributeRules(t *testing.T) {
	inv := store(t).Load()
	e := Exporter{TenantID: uuid.MustParse("0192e000-0000-7000-8000-000000000001"),
		SiteID: uuid.MustParse("0192e222-0000-7000-8000-000000000022"), RouterID: uuid.MustParse("0192e333-0000-7000-8000-000000000033")}
	cases := []struct {
		name           string
		r              flowpb.FlowRecord
		status, dir    string
		rule           string
		client, remote string
	}{
		{"upload", flowpb.FlowRecord{SrcIP: a("10.20.0.5"), DstIP: a("8.8.8.8"), PostNATSrcIP: a("192.0.2.10")},
			StatusAttributed, DirUpload, RuleUploadSrc, "10.20.0.5", "8.8.8.8"},
		{"download NAT (IE 226)", flowpb.FlowRecord{SrcIP: a("8.8.8.8"), DstIP: a("192.0.2.10"), PostNATDstIP: a("10.20.0.5")},
			StatusAttributed, DirDownload, RuleDownloadPostNATDst, "10.20.0.5", "8.8.8.8"},
		{"download sin NAT", flowpb.FlowRecord{SrcIP: a("8.8.8.8"), DstIP: a("10.20.0.6")},
			StatusAttributed, DirDownload, RuleDownloadDst, "10.20.0.6", "8.8.8.8"},
		{"internal", flowpb.FlowRecord{SrcIP: a("10.20.0.5"), DstIP: a("10.20.1.1")},
			StatusInternal, DirInternal, RuleInternal, "10.20.0.5", "10.20.1.1"},
		{"IPv6 truncada al prefijo delegado", flowpb.FlowRecord{SrcIP: a("2001:db8:1000:2a17:9c3e::1"), DstIP: a("2606:4700::1")},
			StatusAttributed, DirUpload, RuleUploadSrc, "2001:db8:1000:2a00::", "2606:4700::1"},
		{"IPv6 sin paso NAT", flowpb.FlowRecord{SrcIP: a("2606:4700::1"), DstIP: a("2001:db8:1000:2a17::5")},
			StatusAttributed, DirDownload, RuleDownloadDst, "2001:db8:1000:2a00::", "2606:4700::1"},
		{"tránsito", flowpb.FlowRecord{SrcIP: a("10.30.0.5"), DstIP: a("8.8.8.8")}, StatusTransit, DirUnknown, "", "", "8.8.8.8"},
		{"infraestructura", flowpb.FlowRecord{SrcIP: a("192.0.2.10"), DstIP: a("8.8.8.8")}, StatusInfrastructure, DirUnknown, "", "", "8.8.8.8"},
		{"enlace local", flowpb.FlowRecord{SrcIP: a("fe80::1"), DstIP: a("ff02::1")}, StatusInfrastructure, DirUnknown, "", "", "ff02::1"},
		{"desconocido por interfaz", flowpb.FlowRecord{SrcIP: a("8.8.8.8"), DstIP: a("172.16.5.5"), InputIfIndex: 2},
			StatusUnknown, DirUnknown, "", "172.16.5.5", "8.8.8.8"},
	}
	for _, c := range cases {
		var row Row
		if !Attribute(inv, e, &c.r, &row) {
			t.Fatalf("%s: dropped", c.name)
		}
		client := ""
		if row.ClientIP.IsValid() {
			client = row.ClientIP.String()
		}
		if row.AttributionStatus != c.status || row.Direction != c.dir || row.Rule != c.rule || client != c.client || row.RemoteIP.String() != c.remote {
			t.Errorf("%s: got %s/%s/%s client=%s remote=%s", c.name, row.AttributionStatus, row.Direction, row.Rule, client, row.RemoteIP)
		}
	}
}

// TestSameIPTwoTenants: la misma 10.20.0.5 en dos ISP son clientes distintos (realm distinto).
func TestSameIPTwoTenants(t *testing.T) {
	p := &Processor{Inv: store(t)}
	mk := func(tenant, router string) *flowpb.FlowBatch {
		return &flowpb.FlowBatch{BatchID: uuid.NewString(), TenantID: tenant, RouterID: router, ReceivedTo: time.Now(),
			Records: []flowpb.FlowRecord{{SrcIP: a("10.20.0.5"), DstIP: a("8.8.8.8"), Bytes: 10, Packets: 1, TS: time.Now(), FlowStart: time.Now()}}}
	}
	r1, err := p.Rows(mk("0192e000-0000-7000-8000-000000000001", "0192e333-0000-7000-8000-000000000033"), "0192e000-0000-7000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := p.Rows(mk("0192e000-0000-7000-8000-000000000002", "0192e333-0000-7000-8000-000000000034"), "0192e000-0000-7000-8000-000000000002")
	if err != nil {
		t.Fatal(err)
	}
	if r1[0].ClientIP != r2[0].ClientIP || r1[0].RealmID == r2[0].RealmID || r1[0].TenantID == r2[0].TenantID {
		t.Fatalf("rows not separated by realm/tenant: %+v %+v", r1[0], r2[0])
	}
	// Horus-Tenant distinto del lote, o router de otro tenant ⇒ permanente.
	if _, err := p.Rows(mk("0192e000-0000-7000-8000-000000000001", "0192e333-0000-7000-8000-000000000033"), "0192e000-0000-7000-8000-000000000002"); err == nil {
		t.Fatal("header tenant mismatch accepted")
	}
	if _, err := p.Rows(mk("0192e000-0000-7000-8000-000000000002", "0192e333-0000-7000-8000-000000000033"), "0192e000-0000-7000-8000-000000000002"); err == nil {
		t.Fatal("router of another tenant accepted")
	}
}

func TestSamplingAndDiscoveryEligibility(t *testing.T) {
	p := &Processor{Inv: store(t)}
	fb := &flowpb.FlowBatch{BatchID: uuid.NewString(), TenantID: "0192e000-0000-7000-8000-000000000001",
		RouterID: "0192e333-0000-7000-8000-000000000033", SamplingRate: 10,
		Records: []flowpb.FlowRecord{
			{SrcIP: a("10.20.0.5"), DstIP: a("8.8.8.8"), Bytes: 10, Packets: 1},
			{SrcIP: a("8.8.8.8"), DstIP: a("9.9.9.9"), Bytes: 10, Packets: 1, InputIfIndex: 7}, // pública ajena: no candidata
		}}
	rows, err := p.Rows(fb, fb.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Bytes != 100 || rows[0].SamplingRate != 10 {
		t.Fatalf("sampling: %+v", rows[0])
	}
	if rows[1].AttributionStatus != StatusUnknown || rows[1].ClientIP.IsValid() {
		t.Fatalf("public non-ISP IP proposed: %+v", rows[1])
	}
}
