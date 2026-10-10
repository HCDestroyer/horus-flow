//go:build chbench

package clickhouse

// Banco de pruebas de escritura en flows_raw (FLOW, isp10k): compara un INSERT
// por lote de 500 filas (I1), INSERT agrupados de 50 000 filas y async_insert
// por lote con deduplicación, midiendo filas/s, CPU de ClickHouse en las
// consultas INSERT (incluye las vistas materializadas) y en los merges.
//
//	HORUS_CH_TEST_DSN=clickhouse://horus@127.0.0.1:39000/horus \
//	HORUS_CH_TEST_PASSWORD_FILE=pw.txt CHBENCH_ROWS=1000000 CHBENCH_MODES=batch500,group50k,async500 \
//	go test -tags chbench -run TestWriteModes -v -timeout 1h ./services/ingester/internal/adapters/clickhouse/
//
// AVISO: vacía las tablas flows.* del destino (aplicar antes las migraciones;
// solo contra un ClickHouse efímero).

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/ingester/internal/app"
)

func benchWriter(t *testing.T) *Writer {
	t.Helper()
	dsn := os.Getenv("HORUS_CH_TEST_DSN")
	if dsn == "" {
		t.Skip("HORUS_CH_TEST_DSN not set")
	}
	var pw string
	if f := os.Getenv("HORUS_CH_TEST_PASSWORD_FILE"); f != "" {
		b, err := os.ReadFile(f) //nolint:gosec // ruta de prueba
		if err != nil {
			t.Fatal(err)
		}
		pw = strings.TrimSpace(string(b))
	}
	w, err := OpenWriter(dsn, "", pw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

// benchRows genera filas parecidas a las de un ISP de 10 000 clientes.
func benchRows(n int, rng *rand.Rand, base time.Time) []app.Row {
	tenant := uuid.MustParse("0192e000-0000-7000-8000-000000000001")
	site := uuid.MustParse("0192e222-0000-7000-8000-000000000022")
	router := uuid.MustParse("0192e333-0000-7000-8000-000000000033")
	realm := uuid.MustParse("0192e444-0000-7000-8000-000000000044")
	services := make([]uuid.UUID, 40)
	for i := range services {
		services[i] = uuid.NewSHA1(uuid.NameSpaceOID, []byte{byte(i)})
	}
	batch := uuid.New()
	rows := make([]app.Row, n)
	for i := range rows {
		if i%500 == 0 {
			batch = uuid.New()
		}
		c := rng.IntN(10000)
		client := netip.AddrFrom4([4]byte{100, 64 + byte(c>>8), byte(c), byte(rng.IntN(2) + 1)})
		remote := netip.AddrFrom4([4]byte{byte(rng.IntN(200) + 1), byte(rng.IntN(256)), byte(rng.IntN(256)), byte(rng.IntN(256))})
		ts := base.Add(time.Duration(i) * time.Millisecond / 20)
		dir := app.DirDownload
		if rng.IntN(2) == 0 {
			dir = app.DirUpload
		}
		status := app.StatusAttributed
		if rng.IntN(50) == 0 {
			status = app.StatusUnknown
		}
		r := app.Row{TenantID: tenant, TS: ts, FlowStart: ts.Add(-time.Duration(rng.IntN(60000)) * time.Millisecond),
			ReceivedAt: ts, SiteID: site, RouterID: router, RealmID: realm, AttributionStatus: status, Direction: dir,
			ClientIP: client, ClientPort: uint16(rng.IntN(60000) + 1024), RemoteIP: remote, //nolint:gosec // acotado
			RemotePort: []uint16{443, 443, 443, 80, 53, 123, 5222, 3478, 8080}[rng.IntN(9)], Protocol: []uint8{6, 6, 17}[rng.IntN(3)],
			TCPFlags: 0x1b, Bytes: uint64(rng.IntN(1_000_000)), Packets: uint64(rng.IntN(800) + 1), //nolint:gosec // acotado
			DurationMs: uint32(rng.IntN(60000)), MergedFlows: 1, FlowSource: "ipfix", //nolint:gosec // acotado
			RemoteASN: uint32(rng.IntN(3000) + 1), RemotePrefix: remote, RemotePrefixLen: 24, //nolint:gosec // acotado
			RemoteCountry: "US", ServiceID: services[rng.IntN(len(services))], BatchID: batch}
		if rng.IntN(2000) == 0 {
			r.ReputationCategory = "scanner"
		}
		rows[i] = r
	}
	return rows
}

func benchExec(ctx context.Context, t *testing.T, w *Writer, q string) {
	t.Helper()
	if err := w.conn.Exec(ctx, q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func benchUint(ctx context.Context, t *testing.T, w *Writer, q string) uint64 {
	t.Helper()
	var v uint64
	if err := w.conn.QueryRow(ctx, q).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

func (w *Writer) insertAsync(ctx context.Context, token string, rows []app.Row) error {
	ctx = ch.Context(ctx, ch.WithSettings(ch.Settings{"insert_deduplication_token": token, "insert_deduplicate": 1,
		"async_insert": 1, "wait_for_async_insert": 1, "async_insert_deduplicate": 1,
		"deduplicate_blocks_in_dependent_materialized_views": 1}))
	b, err := w.conn.PrepareBatch(ctx, w.stmt)
	if err != nil {
		return err
	}
	for i := range rows {
		if err := b.Append(rows[i].Values()...); err != nil {
			_ = b.Abort()
			return err
		}
	}
	return b.Send()
}

func TestWriteModes(t *testing.T) {
	w := benchWriter(t)
	ctx := context.Background()
	total, _ := strconv.Atoi(os.Getenv("CHBENCH_ROWS"))
	if total <= 0 {
		total = 1_000_000
	}
	modes := strings.Split(os.Getenv("CHBENCH_MODES"), ",")
	if os.Getenv("CHBENCH_MODES") == "" {
		modes = []string{"batch500", "group50k", "async500"}
	}
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // datos de prueba
	rows := benchRows(total, rng, time.Now().UTC().Truncate(time.Hour))
	for _, mode := range modes {
		tables := []string{"flows_raw", "customer_5m", "customer_1h", "customer_1d", "site_5m", "site_1h", "site_1d",
			"unattributed_1h", "client_security_1m", "client_security_1h", "client_port_1m", "reputation_hit"}
		for _, tb := range tables {
			benchExec(ctx, t, w, "TRUNCATE TABLE flows."+tb)
		}
		benchExec(ctx, t, w, "SYSTEM FLUSH LOGS")
		mark := benchUint(ctx, t, w, "SELECT toUInt64(toUnixTimestamp64Milli(now64(3)))")
		time.Sleep(1100 * time.Millisecond)
		size, conc, insert := 500, 4, w.Insert
		switch mode {
		case "group50k":
			size, conc = 50000, 2
		case "async500":
			conc, insert = 32, w.insertAsync
		}
		var next atomic.Int64
		var wg sync.WaitGroup
		start := time.Now()
		for range conc {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					i := int(next.Add(int64(size))) - size
					if i >= total {
						return
					}
					chunk := rows[i:min(i+size, total)]
					if err := insert(ctx, uuid.NewString(), chunk); err != nil {
						t.Errorf("%s insert: %v", mode, err)
						return
					}
				}
			}()
		}
		wg.Wait()
		wall := time.Since(start)
		time.Sleep(20 * time.Second) // merges de lo recién insertado
		benchExec(ctx, t, w, "SYSTEM FLUSH LOGS")
		since := fmt.Sprintf("fromUnixTimestamp64Milli(%d)", mark)
		cpuIns := benchUint(ctx, t, w, "SELECT toUInt64(sum(ProfileEvents['UserTimeMicroseconds'] + ProfileEvents['SystemTimeMicroseconds'])) FROM system.query_log WHERE type = 'QueryFinish' AND query_kind = 'Insert' AND event_time_microseconds >= "+since)
		mergeMs := benchUint(ctx, t, w, "SELECT toUInt64(sum(duration_ms)) FROM system.part_log WHERE event_type = 'MergeParts' AND database = 'flows' AND event_time_microseconds >= "+since)
		parts := benchUint(ctx, t, w, "SELECT count() FROM system.part_log WHERE event_type = 'NewPart' AND database = 'flows' AND event_time_microseconds >= "+since)
		got := benchUint(ctx, t, w, "SELECT count() FROM flows.flows_raw")
		agg := benchUint(ctx, t, w, "SELECT sum(flows) FROM flows.customer_5m")
		attributed := uint64(0)
		for i := range rows {
			if rows[i].AttributionStatus == app.StatusAttributed {
				attributed++
			}
		}
		t.Logf("%-9s rows=%d wall=%.1fs rate=%.0f rows/s insert_cpu=%.1fs (%.1f µs/row) merge=%.1fs new_parts=%d raw=%d customer_5m.flows=%d (want %d)",
			mode, total, wall.Seconds(), float64(total)/wall.Seconds(), float64(cpuIns)/1e6, float64(cpuIns)/float64(total),
			float64(mergeMs)/1e3, parts, got, agg, attributed)
		if got != uint64(total) || agg != attributed {
			t.Errorf("%s: rows or aggregates mismatch", mode)
		}
	}
}
