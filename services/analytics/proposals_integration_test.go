//go:build integration

package analytics_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
)

// TestPrefixProposals: nodo sin prefijos con clientes en 10.20.0.0/24 ⇒ la
// propuesta incluye 10.20.0.0/24 con nº de IPs y bytes (I1-29 criterio 1);
// las IPs ya cubiertas por un prefijo declarado no se proponen.
func TestPrefixProposals(t *testing.T) {
	e := setup(t, flowinv.Data{})
	now := time.Now().UTC()
	var rows []row
	for i := range 40 {
		rows = append(rows, row{ts: now.Add(-10 * time.Minute), status: "unknown", direction: "unknown",
			client: netip.AddrFrom4([4]byte{10, 20, 0, byte(i%20 + 1)}), bytes: 1000, packets: 1})
	}
	rows = append(rows,
		row{ts: now.Add(-5 * time.Minute), status: "unknown", direction: "unknown", client: netip.MustParseAddr("100.64.3.9"), bytes: 5, packets: 1},
		row{ts: now.Add(-5 * time.Minute), status: "unknown", direction: "unknown", bytes: 7, packets: 1}, // sin candidata (::)
		row{ts: now.Add(-5 * time.Minute), status: "attributed", direction: "upload", realm: e.realm, client: netip.MustParseAddr("10.20.9.9"), bytes: 1, packets: 1},
	)
	e.insert(rows)

	code, body := e.get("/api/v1/sites/"+e.site.String()+"/prefix-proposals?range=1h", e.token(map[string][]string{"sites.read": all}, "tenant"))
	if code != 200 {
		t.Fatalf("status %d %v", code, body)
	}
	data, _ := body["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("proposals = %v", data)
	}
	first := data[0].(map[string]any)
	if first["prefix"] != "10.20.0.0/24" || first["distinct_ips"] != 20.0 || first["bytes"] != "40000" ||
		first["suggested_role"] != "customers" || first["reason"] != "private_or_cgnat" {
		t.Fatalf("first proposal = %v", first)
	}
	if data[1].(map[string]any)["prefix"] != "100.64.3.0/24" {
		t.Fatalf("second proposal = %v", data[1])
	}
	// Nodo de otro ISP o desconocido ⇒ 404; sin permiso ⇒ 403.
	if code, _ := e.get("/api/v1/sites/"+uuid.NewString()+"/prefix-proposals", e.token(map[string][]string{"sites.read": all}, "tenant")); code != 404 {
		t.Fatalf("unknown site = %d", code)
	}
	if code, _ := e.get("/api/v1/sites/"+e.site.String()+"/prefix-proposals", e.token(nil, "tenant")); code != 403 {
		t.Fatalf("no perm = %d", code)
	}
}
