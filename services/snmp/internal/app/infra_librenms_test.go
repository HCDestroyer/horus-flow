package app

import (
	"net/netip"
	"testing"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// Solo van a LibreNMS los servicios activos con ping y sin sondeo por router.
func TestLibreNMSTargets(t *testing.T) {
	router := uuid.New()
	svc := func(name, ip string, enabled bool, via *uuid.UUID, types ...string) domain.Service {
		s := domain.Service{Name: name, Target: netip.MustParseAddr(ip), Enabled: enabled, ProbeViaRouterID: via}
		for _, ty := range types {
			s.Checks = append(s.Checks, domain.Check{Type: ty})
		}
		return s
	}
	got := LibreNMSTargets([]domain.Service{
		svc("IPTV", "203.0.113.10", true, nil, domain.CheckTCP, domain.CheckICMP),
		svc("Web", "203.0.113.11", true, nil, domain.CheckHTTP),
		svc("Apagado", "203.0.113.12", false, nil, domain.CheckICMP),
		svc("Privado", "192.168.10.5", true, &router, domain.CheckICMP),
	})
	if len(got) != 1 || got[0].Name != "IPTV" || got[0].IP != "203.0.113.10" {
		t.Fatalf("objetivos = %+v", got)
	}
}
