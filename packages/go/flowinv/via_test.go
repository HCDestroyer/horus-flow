package flowinv

import (
	"net/netip"
	"testing"

	"github.com/google/uuid"
)

// Switches vía router (ADR-0032): la identidad del exportador sFlow es la
// dirección Horus verificada, nunca la IP privada (que dos ISP repiten); dos
// ISP con el mismo switch privado 192.168.88.2 quedan cada uno con su
// exportador secundario, y dos verificados en modo directo con la misma IP se
// quedan fuera (ambigüedad: nunca se adivina el tenant).
func TestProjectorViaRouterSwitches(t *testing.T) {
	t.Parallel()
	tenantA, tenantB, site := uuid.New(), uuid.New(), uuid.New()
	swA, swB, dA, dB := uuid.New(), uuid.New(), uuid.New(), uuid.New()
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
	private := "192.168.88.2/32"
	sw := func(id uuid.UUID, via, state string) map[string]any {
		return map[string]any{"id": id, "site_id": site, "name": "sw", "admin_state": "active", "tunnel_address": nil, "device_type": "switch",
			"sflow_enabled": true, "connection_mode": "via_router", "management_address": private, "via_address": via, "onboarding_state": state}
	}
	apply(tenantA, 1, sw(swA, "10.255.1.40/32", "pending_configuration"))
	if len(exporters()) != 0 {
		t.Fatal("switch vía router sin verificar aceptado")
	}
	apply(tenantA, 2, sw(swA, "10.255.1.40/32", "snmp_verified"))
	apply(tenantB, 1, sw(swB, "10.255.1.41/32", "snmp_verified"))
	ex := exporters()
	a, b := ex[netip.MustParseAddr("10.255.1.40")], ex[netip.MustParseAddr("10.255.1.41")]
	if a.RouterID != swA || a.TenantID != tenantA || !a.Secondary || a.Direct || b.RouterID != swB || b.TenantID != tenantB || !b.Secondary {
		t.Fatalf("exportadores vía router: %+v", ex)
	}
	if _, ok := ex[netip.MustParseAddr("192.168.88.2")]; ok {
		t.Fatal("la IP privada nunca es identidad")
	}
	// Sin sFlow: no es exportador.
	x := sw(swA, "10.255.1.40/32", "snmp_verified")
	x["sflow_enabled"] = false
	apply(tenantA, 3, x)
	if _, ok := exporters()[netip.MustParseAddr("10.255.1.40")]; ok {
		t.Fatal("switch sin sFlow aceptado")
	}
	// Modo directo: la misma IP privada verificada en dos ISP → ambigua, fuera.
	direct := func(id uuid.UUID) map[string]any {
		return map[string]any{"id": id, "site_id": site, "name": "sw-d", "admin_state": "active", "tunnel_address": nil, "device_type": "switch",
			"sflow_enabled": true, "connection_mode": "direct", "management_address": private, "onboarding_state": "snmp_verified"}
	}
	apply(tenantA, 1, direct(dA))
	if e := exporters()[netip.MustParseAddr("192.168.88.2")]; e.RouterID != dA || !e.Direct {
		t.Fatalf("directo único: %+v", e)
	}
	apply(tenantB, 1, direct(dB))
	if _, ok := exporters()[netip.MustParseAddr("192.168.88.2")]; ok {
		t.Fatal("dos ISP con la misma dirección directa verificada: ninguno debe ser exportador")
	}
}
