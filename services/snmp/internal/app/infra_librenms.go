package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	alertsapi "github.com/hcdestroyer/horus-flow/services/alerts/api"
	snmpapi "github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// librenmsSyncTimeout acota la sincronización del grupo de infraestructura.
const librenmsSyncTimeout = 2 * time.Minute

// librenmsHook mantiene el grupo «Horus · Infraestructura» de LibreNMS
// (alerts) con los servicios que LibreNMS puede vigilar por ping: activos,
// con chequeo ICMP y sin sondeo a través de un router (una IP privada que
// solo se alcanza por reenvío de puertos no responde a ping desde fuera).
type librenmsHook struct {
	lookup func() (alertsapi.LibreNMSInfrastructure, bool)
	log    *slog.Logger
	mu     sync.Map // tenant → *sync.Mutex: una sincronización a la vez por ISP
}

// SetLibreNMS activa la sincronización con LibreNMS tras cada alta, edición o
// baja de un servicio (sin alerts en el proceso, no hace nada).
func (i *Infra) SetLibreNMS(lookup func() (alertsapi.LibreNMSInfrastructure, bool), log *slog.Logger) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	i.librenms = &librenmsHook{lookup: lookup, log: log}
}

// LibreNMSTargets son los servicios que se dan de alta en LibreNMS.
func LibreNMSTargets(all []domain.Service) []alertsapi.InfraService {
	out := make([]alertsapi.InfraService, 0, len(all))
	for k := range all {
		s := &all[k]
		if !s.Enabled || s.ProbeViaRouterID != nil {
			continue
		}
		for _, c := range s.Checks {
			if c.Type == domain.CheckICMP {
				out = append(out, alertsapi.InfraService{Name: s.Name, IP: s.Target.String()})
				break
			}
		}
	}
	return out
}

// syncLibreNMS sincroniza en segundo plano el grupo del ISP; un fallo solo se
// registra (la siguiente edición o «Sincronizar» en la consola lo corrige).
func (i *Infra) syncLibreNMS(t pgdb.TenantID) {
	h := i.librenms
	if h == nil || h.lookup == nil {
		return
	}
	hook, ok := h.lookup()
	if !ok {
		return
	}
	m, _ := h.mu.LoadOrStore(t.UUID(), &sync.Mutex{})
	mu := m.(*sync.Mutex) //nolint:forcetypeassert // solo se guardan *sync.Mutex
	go func() {
		mu.Lock()
		defer mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), librenmsSyncTimeout)
		defer cancel()
		all, err := i.store.List(ctx, t)
		if err != nil {
			h.log.WarnContext(ctx, "snmp: librenms infra sync: list services", "tenant_id", t.UUID(), "error", err)
			return
		}
		if _, err := hook.SyncInfrastructure(ctx, t.UUID(), LibreNMSTargets(all)); err != nil {
			h.log.WarnContext(ctx, "snmp: librenms infra sync failed", "tenant_id", t.UUID(), "error", err)
		}
	}()
}

// InfraTargets implementa snmpapi.InfraTargets para la sincronización
// completa de LibreNMS que lanza alerts.
type InfraTargets struct{ Infra *Infra }

// LibreNMSTargets implementa snmpapi.InfraTargets.
func (x InfraTargets) LibreNMSTargets(ctx context.Context, tenant uuid.UUID) ([]snmpapi.InfraTarget, error) {
	if !x.Infra.Available() {
		return nil, unavailable()
	}
	all, err := x.Infra.store.List(ctx, pgdb.TenantID(tenant))
	if err != nil {
		return nil, err
	}
	src := LibreNMSTargets(all)
	out := make([]snmpapi.InfraTarget, 0, len(src))
	for _, t := range src {
		out = append(out, snmpapi.InfraTarget{Name: t.Name, IP: t.IP})
	}
	return out, nil
}
