package flowinv

import (
	"net/netip"
	"testing"

	"github.com/google/uuid"
)

// Modo directo (Huawei VRP): la dirección de gestión es la identidad del exportador solo cuando devices la
// verificó (snmp_verified / exporting) y nunca suplanta una IP de túnel ni otra dirección directa.
func TestProjectorDirectExporters(t *testing.T) {
	t.Parallel()
	tenantA, tenantB, site := uuid.New(), uuid.New(), uuid.New()
	hw, mk, squat := uuid.New(), uuid.New(), uuid.New()
	p := NewProjector(NewStore(nil), Data{}, nil)
	apply := func(tenant uuid.UUID, v int, data map[string]any) {
		t.Helper()
		if _, err := p.Apply(event(t, "horus.devices.router.updated", tenant, v, data)); err != nil {
			t.Fatal(err)
		}
	}
	exporters := func() map[netip.Addr]Exporter {
		out := map[netip.Addr]Exporter{}
		for _, e := range p.Data().Exporters {
			out[e.TunnelIP] = e
		}
		return out
	}
	mgmt := "198.51.100.20/32"
	apply(tenantA, 1, map[string]any{"id": hw, "site_id": site, "name": "ne40e", "admin_state": "active", "tunnel_address": nil,
		"connection_mode": "direct", "management_address": mgmt, "onboarding_state": "pending_configuration"})
	if len(exporters()) != 0 {
		t.Fatal("dirección sin verificar aceptada como exportador")
	}
	apply(tenantA, 2, map[string]any{"id": hw, "site_id": site, "name": "ne40e", "admin_state": "active", "tunnel_address": nil,
		"connection_mode": "direct", "management_address": mgmt, "onboarding_state": "snmp_verified"})
	e, ok := exporters()[netip.MustParseAddr("198.51.100.20")]
	if !ok || !e.Direct || e.RouterID != hw || e.TenantID != tenantA {
		t.Fatalf("exportador directo verificado: %+v", exporters())
	}
	// Un MikroTik con túnel en la misma IP (no debería pasar: la verificación SNMP lo impide) gana siempre.
	apply(tenantB, 1, map[string]any{"id": mk, "site_id": site, "name": "rt", "admin_state": "active", "tunnel_address": "198.51.100.20/32"})
	if e := exporters()[netip.MustParseAddr("198.51.100.20")]; e.RouterID != mk || e.Direct {
		t.Fatalf("la IP de túnel debe ganar: %+v", e)
	}
	apply(tenantB, 2, map[string]any{"id": mk, "site_id": site, "name": "rt", "admin_state": "active", "tunnel_address": "10.255.0.7/32"})
	// Dos routers directos que reclaman la misma dirección: ninguno se acepta.
	apply(tenantB, 1, map[string]any{"id": squat, "site_id": site, "name": "otro", "admin_state": "active", "tunnel_address": nil,
		"connection_mode": "direct", "management_address": mgmt, "onboarding_state": "exporting"})
	if _, ok := exporters()[netip.MustParseAddr("198.51.100.20")]; ok {
		t.Fatalf("dirección directa duplicada aceptada: %+v", exporters())
	}
	// Un MikroTik sin connection_mode (eventos anteriores) conserva el comportamiento de siempre.
	if e := exporters()[netip.MustParseAddr("10.255.0.7")]; e.RouterID != mk || e.Direct {
		t.Fatalf("MikroTik: %+v", e)
	}
}
