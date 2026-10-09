package flowpb

import (
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sampleBatch() *FlowBatch {
	t0 := time.Date(2026, 10, 9, 4, 36, 30, 123_000_000, time.UTC)
	return &FlowBatch{
		BatchID: "0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f33", CollectorID: "c1", TenantID: "t", RouterID: "r",
		ExporterIP: netip.MustParseAddr("10.255.3.17"), SamplingRate: 1,
		ReceivedFrom: t0, ReceivedTo: t0.Add(time.Second),
		Records: []FlowRecord{
			{
				ExporterIP: netip.MustParseAddr("10.255.3.17"), FlowStart: t0, TS: t0.Add(59 * time.Second),
				SrcIP: netip.MustParseAddr("10.20.0.5"), DstIP: netip.MustParseAddr("192.0.2.1"),
				SrcPort: 51544, DstPort: 443, Protocol: 6, TCPFlags: 27, Bytes: 1840, Packets: 13,
				InputIfIndex: 12, OutputIfIndex: 2, FlowDirection: "ingress", FlowSource: "ipfix",
				PostNATSrcIP: netip.MustParseAddr("192.0.2.10"), PostNATSrcPort: 0, HasPostNATSrcPort: true,
				PostNATDstIP: netip.MustParseAddr("192.0.2.1"), PostNATDstPort: 443, HasPostNATDstPort: true,
				BatchID: "0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f33",
			},
			{
				FlowStart: t0, TS: t0, SrcIP: netip.MustParseAddr("2001:db8::1"), DstIP: netip.MustParseAddr("2001:db8:1::2"),
				Protocol: 58, ICMPTypeCode: 128 << 8, Bytes: 64, Packets: 1, FlowSource: "netflow_v9",
				NextHop: netip.MustParseAddr("fe80::1"), SamplingRate: 100,
			},
		},
	}
}

func TestFlowBatchRoundTrip(t *testing.T) {
	in := sampleBatch()
	out, err := UnmarshalFlowBatch(in.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip:\n in=%+v\nout=%+v", in, out)
	}
}

func TestSummariesRoundTrip(t *testing.T) {
	h := time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC)
	a := &ClientActivitySummary{BatchID: "b", RealmID: "r", Hour: h, Part: 1, Parts: 2,
		Clients: []ActiveClient{{Address: "10.20.0.5", LastSeen: h.Add(time.Minute), BytesEst: 1 << 40, Flows: 7}}}
	a2, err := UnmarshalClientActivitySummary(a.Marshal())
	if err != nil || !reflect.DeepEqual(a, a2) {
		t.Fatalf("activity: %v %+v", err, a2)
	}
	s := &TrafficSummary{BatchID: "b", SiteID: "s", WindowFrom: h, WindowTo: h.Add(10 * time.Second),
		DownBps: 1.5e9, UpBps: 2e8, FlowsPerSecond: 152, ActiveCustomers: 236, Partial: true}
	s2, err := UnmarshalTrafficSummary(s.Marshal())
	if err != nil || !reflect.DeepEqual(s, s2) {
		t.Fatalf("traffic: %v %+v", err, s2)
	}
}

func TestMalformedNoPanic(t *testing.T) {
	b := sampleBatch().Marshal()
	for i := range b {
		_, _ = UnmarshalFlowBatch(b[:i])
	}
	if _, err := UnmarshalFlowBatch([]byte{0x0a, 0xff}); err == nil {
		t.Fatal("expected error")
	}
}

func FuzzUnmarshalFlowBatch(f *testing.F) {
	f.Add(sampleBatch().Marshal())
	f.Fuzz(func(_ *testing.T, b []byte) {
		_, _ = UnmarshalFlowBatch(b)
		_, _ = UnmarshalClientActivitySummary(b)
	})
}

// TestContract compara los números de campo que usa el códec con los del .proto (C4).
func TestContract(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "protobuf", "horus", "events", "flows", "v1", "flows.proto"))
	if err != nil {
		t.Fatal(err)
	}
	fields := parseProto(string(src))
	want := map[string]map[string]int{
		"FlowRecord": {
			"exporter_ip": 1, "observation_domain_id": 2, "flow_start": 3, "ts": 4, "src_ip": 5, "dst_ip": 6,
			"src_port": 7, "dst_port": 8, "protocol": 9, "tcp_flags": 10, "icmp_type_code": 11, "bytes": 12,
			"packets": 13, "input_if_index": 14, "output_if_index": 15, "flow_direction": 16, "src_as": 17,
			"dst_as": 18, "next_hop": 19, "vlan_id": 20, "post_nat_src_ip": 21, "post_nat_src_port": 22,
			"sampling_rate": 23, "flow_source": 24, "batch_id": 25, "post_nat_dst_ip": 26, "post_nat_dst_port": 27,
		},
		"FlowBatch": {
			"batch_id": 1, "collector_id": 2, "tenant_id": 3, "router_id": 4, "exporter_ip": 5,
			"sampling_rate": 6, "received_from": 7, "received_to": 8, "records": 9,
		},
		"ActiveClient":          {"address": 1, "last_seen": 2, "bytes_est": 3, "flows": 4},
		"ClientActivitySummary": {"batch_id": 1, "realm_id": 2, "hour": 3, "part": 4, "parts": 5, "clients": 6},
		"TrafficSummary": {
			"batch_id": 1, "site_id": 2, "window_from": 3, "window_to": 4, "down_bps": 5, "up_bps": 6,
			"flows_per_second": 7, "active_customers": 8, "partial": 9,
		},
	}
	for msg, w := range want {
		if !reflect.DeepEqual(fields[msg], w) {
			t.Errorf("%s: proto=%v codec=%v", msg, fields[msg], w)
		}
	}
}

var (
	msgRe   = regexp.MustCompile(`(?m)^message (\w+) \{`)
	fieldRe = regexp.MustCompile(`^\s*(?:optional |repeated )?[\w.]+ (\w+) = (\d+);`)
)

func parseProto(src string) map[string]map[string]int {
	out := map[string]map[string]int{}
	var cur string
	for _, line := range strings.Split(src, "\n") {
		if m := msgRe.FindStringSubmatch(line); m != nil {
			cur = m[1]
			out[cur] = map[string]int{}
			continue
		}
		if strings.HasPrefix(line, "}") {
			cur = ""
			continue
		}
		if m := fieldRe.FindStringSubmatch(line); m != nil && cur != "" {
			n, _ := strconv.Atoi(m[2])
			out[cur][m[1]] = n
		}
	}
	return out
}
