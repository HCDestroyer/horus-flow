package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/snmpclient"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// Errores de la lectura de pruebas NQA.
var (
	errNQANoTarget     = errors.New("snmp: router not polled")
	errNQANoCredential = errors.New("snmp: no SNMP credential")
)

// ReadPings lee por SNMP (solo lectura) las pruebas ICMP de Horus (admin-name
// `horus`) del router: DISMAN-PING-MIB o NQA-MIB (Huawei VRP,
// docs/vendors/huawei.md §4). Usa la misma credencial y dirección que el
// sondeo.
func (e *Engine) ReadPings(ctx context.Context, tenant, routerID uuid.UUID) ([]snmpclient.PingResult, error) {
	e.mu.RLock()
	r := e.routers[routerID]
	e.mu.RUnlock()
	if r == nil || r.TenantID != tenant {
		return nil, errNQANoTarget
	}
	cred, _ := e.credential(ctx, r, e.now())
	if cred == nil {
		return nil, errNQANoCredential
	}
	r.mu.Lock()
	addr := r.Tunnel
	r.mu.Unlock()
	t, err := e.o.Dialer.Dial(ctx, addr, *cred)
	if err != nil {
		return nil, err
	}
	defer func() { _ = t.Close() }()
	return snmpclient.ReadPingResults(ctx, t, domain.NQAOwner)
}

func nqaErrorText(err error) string {
	switch {
	case errors.Is(err, errNQANoTarget):
		return "router_not_polled"
	case errors.Is(err, errNQANoCredential):
		return "no_snmp_credential"
	case snmpclient.IsAuth(err):
		return "auth_failed"
	}
	return "unreachable"
}

// NQASamples convierte las pruebas NQA de Horus de un router en lecturas
// con el mismo modelo que netwatch: test-name `gw-<interfaz>` → puerta de
// enlace por esa interfaz (emparejada con la interfaz descubierta por SNMP),
// `intl-<ip>` → referencia internacional. El jitter es la desviación típica
// del RTT (DISMAN-PING-MIB no publica jitter).
func NQASamples(r RouterSnapshot, res []snmpclient.PingResult, now time.Time) []domain.ProbeSample {
	var out []domain.ProbeSample
	for _, p := range res {
		ref, _, ok := domain.ParseNQATestName(p.Test)
		if !ok {
			continue
		}
		s := domain.ProbeSample{TenantID: r.TenantID, SiteID: r.SiteID, RouterID: r.RouterID, Source: domain.ProbeSourceRouterNQA, Ref: ref,
			Target: p.Target, At: now, Sent: p.Sent, Received: p.Responses, LossPct: p.LossPct, RTTAvgMs: p.AvgRTTMs, RTTMinMs: p.MinRTTMs,
			RTTMaxMs: p.MaxRTTMs, JitterMs: p.StdDevMs}
		switch {
		case p.Responses != nil && *p.Responses > 0:
			s.Up = boolPtr(true)
		case p.Sent != nil && *p.Sent > 0:
			s.Up = boolPtr(false)
		}
		label := "Internacional " + p.Target
		iface := ""
		if ref == domain.ProbeRefGateway {
			label = "Puerta de enlace (" + p.Test + ", interfaz no encontrada)"
			for _, x := range r.Interfaces {
				if domain.NQAMatchesInterface(p.Test, x.Name) {
					s.InterfaceID, iface = x.InterfaceID, domain.SafeIfName(x.Name)
					label = "Puerta de enlace por " + x.Name
					break
				}
			}
			if iface == "" {
				iface = p.Test
			}
		}
		s.Label = label
		s.ProbeID = domain.ProbeID(r.RouterID, p.Target, ref, iface)
		out = append(out, s)
	}
	return out
}
