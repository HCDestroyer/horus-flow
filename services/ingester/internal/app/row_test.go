package app

import (
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestColumnSlicesMatchValues: el INSERT columnar lleva lo mismo que Values,
// columna a columna.
func TestColumnSlicesMatchValues(t *testing.T) {
	now := time.Now().UTC()
	rows := []Row{
		{TenantID: uuid.New(), TS: now, FlowStart: now.Add(-time.Second), ReceivedAt: now, SiteID: uuid.New(),
			AttributionStatus: StatusAttributed, Direction: DirUpload, ClientIP: netip.MustParseAddr("10.64.0.9"),
			ClientPort: 51000, RemoteIP: netip.MustParseAddr("2001:db8::1"), RemotePort: 443, Protocol: 6, Bytes: 10,
			Packets: 2, MergedFlows: 1, FlowSource: "ipfix", RemoteCountry: "MX", RemoteASN: 15169, BatchID: uuid.New()},
		{TenantID: uuid.New(), AttributionStatus: StatusUnknown, Direction: DirUnknown, RemoteCountry: "XYZ",
			ReputationCategory: "scanner", FlowSource: "ipfix"},
	}
	cols := ColumnSlices(rows)
	if len(cols) != len(Columns) {
		t.Fatalf("%d column slices for %d columns", len(cols), len(Columns))
	}
	for r := range rows {
		vals := rows[r].Values()
		for c := range cols {
			got := reflect.ValueOf(cols[c]).Index(r).Interface()
			if !reflect.DeepEqual(got, vals[c]) {
				t.Errorf("row %d column %s: %v != %v", r, Columns[c], got, vals[c])
			}
		}
	}
}

func benchRows(n int) []Row {
	rows := make([]Row, n)
	for i := range rows {
		rows[i] = Row{TenantID: uuid.New(), TS: time.Now(), ClientIP: netip.MustParseAddr("10.64.0.9"),
			RemoteIP: netip.MustParseAddr("8.8.8.8"), AttributionStatus: StatusAttributed, Direction: DirUpload, FlowSource: "ipfix"}
	}
	return rows
}

// BenchmarkInsertValues / BenchmarkInsertColumns: preparar 50 000 filas fila a
// fila (I1) frente a por columnas.
func BenchmarkInsertValues(b *testing.B) {
	rows := benchRows(50000)
	b.ReportAllocs()
	for b.Loop() {
		for i := range rows {
			_ = rows[i].Values()
		}
	}
}

func BenchmarkInsertColumns(b *testing.B) {
	rows := benchRows(50000)
	b.ReportAllocs()
	for b.Loop() {
		_ = ColumnSlices(rows)
	}
}
