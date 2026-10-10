//go:build integration

package flows_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
)

type goldenExpected struct {
	Exporters []struct {
		ExporterIP string `json:"exporter_ip"`
		Prefixes   struct {
			Customers      []string `json:"customers"`
			Infrastructure []string `json:"infrastructure"`
		} `json:"prefixes"`
		Totals struct {
			DataRecords int `json:"data_records"`
		} `json:"totals"`
		ByStatus map[string]int `json:"by_status"`
		ByRule   map[string]int `json:"by_rule"`
		Clients  []struct {
			Key       string `json:"key"`
			Records   int    `json:"records"`
			BytesUp   uint64 `json:"bytes_up"`
			BytesDown uint64 `json:"bytes_down"`
		} `json:"clients"`
		SequenceGaps uint64 `json:"sequence_gaps"`
		LostRecords  uint64 `json:"lost_records"`
	} `json:"exporters"`
}

// TestGoldenRealMikroTik es el test dorado de I1-03/I1-04/I1-05: reproduce la
// captura real anonimizada de un MikroTik con NAT en el router principal por
// el collector y el ingester reales y comprueba en flows.flows_raw los
// recuentos de subida / bajada / internos / desconocidos y los bytes por
// cliente de ipfix-nat-20s.expected.json (regla de docs/traffic-model.md §4.4,
// bajada por IE 226), los saltos de secuencia, first_seen de cada cliente una
// vez y que un lote reentregado no duplica filas.
//
// Se ejecuta con 1 y con 8 hilos de decodificación en el collector
// (HORUS_COLLECTOR_DECODE_WORKERS, D23 §4): el resultado debe ser idéntico.
func TestGoldenRealMikroTik(t *testing.T) {
	for _, n := range []int{1, 8} {
		t.Run(fmt.Sprintf("decode-workers-%d", n), func(t *testing.T) { goldenRealMikroTik(t, n) })
	}
}

func goldenRealMikroTik(t *testing.T, decodeWorkers int) {
	var exp goldenExpected
	b, err := os.ReadFile(repo("tests", "fixtures", "mikrotik-real", "ipfix-nat-20s.expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &exp); err != nil {
		t.Fatal(err)
	}
	ex := exp.Exporters[0]
	tenant, site, router, realm := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	d := flowinv.Data{
		Exporters: []flowinv.Exporter{{TenantID: tenant, RouterID: router, SiteID: site, Name: "po-router",
			TunnelIP: netip.MustParseAddr(ex.ExporterIP)}},
		Realms: []flowinv.Realm{{ID: realm, TenantID: tenant, Kind: flowinv.RealmNodePrivate, SiteID: site}},
	}
	for _, p := range ex.Prefixes.Customers {
		d.Prefixes = append(d.Prefixes, flowinv.ClientPrefix{ID: uuid.New(), TenantID: tenant, SiteID: site, RealmID: realm,
			Prefix: netip.MustParsePrefix(p), Role: flowinv.RoleCustomers})
	}
	for _, p := range ex.Prefixes.Infrastructure {
		d.Prefixes = append(d.Prefixes, flowinv.ClientPrefix{ID: uuid.New(), TenantID: tenant, SiteID: site, RealmID: realm,
			Prefix: netip.MustParsePrefix(p), Role: flowinv.RoleInfrastructure})
	}
	p := startPipeline(t, d, tenant, "HORUS_INGESTER_FIRST_SEEN_INTERVAL=500ms")
	p.env = append(p.env, fmt.Sprintf("HORUS_COLLECTOR_DECODE_WORKERS=%d", decodeWorkers))
	st := p.replay(repo("tests", "fixtures", "mikrotik-real", "ipfix-nat-20s.pcapng"), nil)

	// Collector: 2 596 registros menos los 8 del túnel; saltos de secuencia medidos.
	want := ex.Totals.DataRecords - ex.ByStatus["tunnel"]
	if st.Records != want || st.SequenceGaps != ex.SequenceGaps || st.LostRecords != ex.LostRecords {
		t.Errorf("collector: records=%d gaps=%d lost=%d, want %d/%d/%d", st.Records, st.SequenceGaps, st.LostRecords,
			want, ex.SequenceGaps, ex.LostRecords)
	}
	t.Logf("collector: %d datagramas, %d registros publicados, %d saltos de secuencia, %d registros perdidos",
		st.Datagrams, st.Records, st.SequenceGaps, st.LostRecords)
	p.waitCount(want)

	byDir := p.counts("SELECT toString(direction), count() FROM flows.flows_raw WHERE tenant_id = ? GROUP BY direction")
	byStatus := p.counts("SELECT toString(attribution_status), count() FROM flows.flows_raw WHERE tenant_id = ? GROUP BY attribution_status")
	wantDir := map[string]int{
		"upload":   ex.ByRule["upload_src"],
		"download": ex.ByRule["download_post_nat_dst"] + ex.ByRule["download_dst"],
		"internal": ex.ByRule["internal"],
		"unknown":  ex.ByStatus["unknown"] + ex.ByStatus["infrastructure"] + ex.ByStatus["transit"],
	}
	wantStatus := map[string]int{"attributed": ex.ByStatus["attributed"], "internal": ex.ByStatus["internal"], "unknown": ex.ByStatus["unknown"]}
	t.Logf("flows_raw por dirección: %v (esperado %v)", byDir, wantDir)
	t.Logf("flows_raw por estado: %v (esperado %v)", byStatus, wantStatus)
	for k, v := range wantDir {
		if byDir[k] != v {
			t.Errorf("direction %s = %d, want %d", k, byDir[k], v)
		}
	}
	for k, v := range wantStatus {
		if byStatus[k] != v {
			t.Errorf("attribution_status %s = %d, want %d", k, byStatus[k], v)
		}
	}

	// Bytes y registros por cliente (IP privada pre-NAT, D12).
	rows, err := p.db.Query(`SELECT IPv6NumToString(client_ip), count(),
		sumIf(bytes, direction IN ('upload', 'internal')), sumIf(bytes, direction = 'download')
		FROM flows.flows_raw WHERE tenant_id = ? AND attribution_status IN ('attributed', 'internal')
		GROUP BY client_ip`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	type cl struct {
		n        int
		up, down uint64
	}
	got := map[string]cl{}
	for rows.Next() {
		var ip string
		var n, up, down uint64
		if err := rows.Scan(&ip, &n, &up, &down); err != nil {
			t.Fatal(err)
		}
		got[strings.TrimPrefix(ip, "::ffff:")] = cl{int(n), up, down}
	}
	_ = rows.Close()
	if len(got) != len(ex.Clients) {
		t.Errorf("clients = %d, want %d", len(got), len(ex.Clients))
	}
	for _, c := range ex.Clients {
		if g := got[c.Key]; g.n != c.Records || g.up != c.BytesUp || g.down != c.BytesDown {
			t.Errorf("client %s: got %+v want records=%d up=%d down=%d", c.Key, g, c.Records, c.BytesUp, c.BytesDown)
		}
	}
	t.Logf("clientes IPv4: %d (esperado %d)", len(got), len(ex.Clients))

	// Descubrimiento (I1-05): first_seen del realm cubre los 236 clientes, una vez cada uno.
	ctx := context.Background()
	evs, err := p.js.Stream(ctx, flowbus.StreamEvents)
	if err != nil {
		t.Fatal(err)
	}
	fs := map[string]int{}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline) && len(fs) < len(ex.Clients); time.Sleep(300 * time.Millisecond) {
		fs = map[string]int{}
		info, _ := evs.Info(ctx)
		for seq := uint64(1); seq <= info.State.LastSeq; seq++ {
			raw, err := evs.GetMsg(ctx, seq)
			if err != nil || raw.Subject != flowbus.TypeClientFirstSeen+"."+realm.String() {
				continue
			}
			var env flowbus.Envelope
			var fsp struct {
				Clients []struct {
					Address string `json:"address"`
				} `json:"clients"`
			}
			if json.Unmarshal(raw.Data, &env) != nil || json.Unmarshal(env.Data, &fsp) != nil || raw.Header.Get(flowbus.HeaderTenant) != tenant.String() {
				t.Fatal("bad first_seen")
			}
			for _, c := range fsp.Clients {
				fs[c.Address]++
			}
		}
	}
	for ip, n := range fs {
		if n != 1 {
			t.Errorf("first_seen %s emitted %d times", ip, n)
		}
	}
	if len(fs) != len(ex.Clients) {
		t.Errorf("first_seen keys = %d, want %d", len(fs), len(ex.Clients))
	}
	t.Logf("first_seen: %d claves", len(fs))

	// Reentrega fuera de la ventana de deduplicación de JetStream: el mismo
	// lote con otro Nats-Msg-Id no duplica filas (insert_deduplication_token).
	tlm, err := p.js.Stream(ctx, flowbus.StreamTelemetry)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := tlm.GetMsg(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	m := nats.NewMsg(raw.Subject)
	m.Data, m.Header = raw.Data, raw.Header
	m.Header.Set(flowbus.HeaderMsgID, "redelivery-"+uuid.NewString())
	if _, err := p.js.PublishMsg(ctx, m); err != nil {
		t.Fatal(err)
	}
	cons, err := p.js.Consumer(ctx, flowbus.StreamTelemetry, flowbus.ConsumerIngester)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if info, err := cons.Info(ctx); err == nil && info.NumPending == 0 && info.NumAckPending == 0 {
			break
		}
	}
	if n := p.count(); n != want {
		t.Fatalf("after redelivery flows_raw = %d, want %d (duplicated batch)", n, want)
	}
}
