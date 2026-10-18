package app

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/snmpclient"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// Switches de monitoreo (devices.router.device_type = switch): el mismo
// sondeador que los routers, con la elección de puertos del operador (o la de
// por defecto), uplinks marcados (varios), vecinos LLDP y flaps por puerto.
// Solo lectura; nunca exportador primario ni atribución.

const deviceTypeSwitch = "switch"

func deviceTypeOf(t Target) string {
	if t.Switch {
		return deviceTypeSwitch
	}
	return "router"
}

// neighborState es un vecino LLDP persistido.
type neighborState struct {
	LocalIfIndex int    `json:"local_if_index"`
	LocalPort    string `json:"local_port"`
	SysName      string `json:"sys_name"`
	PortID       string `json:"port_id"`
	PortDesc     string `json:"port_desc"`
	ChassisID    string `json:"chassis_id"`
	MgmtAddress  string `json:"mgmt_address"`
	Platform     string `json:"platform"`
	Source       string `json:"source"`
	// Known: nombre del equipo de Horus que es el vecino ("" si ninguno).
	Known string `json:"known"`
}

func neighborStates(nbs []snmpclient.Neighbor, known []domain.KnownDevice) []neighborState {
	out := make([]neighborState, 0, len(nbs))
	for _, n := range nbs {
		out = append(out, neighborState{LocalIfIndex: n.LocalIfIndex, LocalPort: n.LocalPort, SysName: n.SysName, PortID: n.PortID,
			PortDesc: n.PortDesc, ChassisID: n.ChassisID, MgmtAddress: n.MgmtAddress, Platform: n.Platform, Source: n.Source,
			Known: domain.MatchKnownDevice(n.SysName, n.MgmtAddress, known)})
	}
	return out
}

// discCandidates construye los candidatos de un descubrimiento recién leído.
func discCandidates(disc []snmpclient.Discovered, nbs []snmpclient.Neighbor, known []domain.KnownDevice) []domain.PortCandidate {
	sugg := map[int]bool{}
	for _, n := range nbs {
		if n.LocalIfIndex != 0 && domain.MatchKnownDevice(n.SysName, n.MgmtAddress, known) != "" {
			sugg[n.LocalIfIndex] = true
		}
	}
	out := make([]domain.PortCandidate, 0, len(disc))
	for _, d := range disc {
		out = append(out, domain.PortCandidate{IfIndex: d.IfIndex, Name: d.Name, Alias: d.Alias, IfType: d.Type, SuggestedUplink: sugg[d.IfIndex]})
	}
	return out
}

// stateCandidates construye los candidatos del estado (r.mu tomado).
func stateCandidates(st *persisted) []domain.PortCandidate {
	sugg := map[int]bool{}
	for _, n := range st.Neighbors {
		if n.LocalIfIndex != 0 && n.Known != "" {
			sugg[n.LocalIfIndex] = true
		}
	}
	out := make([]domain.PortCandidate, 0, len(st.Interfaces))
	for _, x := range st.Interfaces {
		out = append(out, domain.PortCandidate{IfIndex: x.IfIndex, Name: x.Name, Alias: x.Alias, IfType: x.Type, SuggestedUplink: sugg[x.IfIndex]})
	}
	return out
}

// switchChoices es la elección vigente: la guardada por el operador o la de
// por defecto sobre los candidatos.
func (e *Engine) switchChoices(t Target, cands []domain.PortCandidate) map[string]domain.PortChoice {
	if sel, ok := e.portSelection(t.RouterID); ok {
		return sel.Ports
	}
	return domain.DefaultPortSelection(cands)
}

// applyPortChoices vuelca la elección en las interfaces del switch (r.mu
// tomado). Un puerto que deja de sondearse pierde su muestra.
func (e *Engine) applyPortChoices(r *router) {
	st := &r.st
	ch := e.switchChoices(r.Target, stateCandidates(st))
	for i := range st.Interfaces {
		x := &st.Interfaces[i]
		c := ch[x.Name]
		if x.Monitored && !c.Monitored {
			x.Sample, x.Prev = nil, nil
		}
		x.Monitored, x.Uplink = c.Monitored, c.Monitored && c.Uplink
	}
	st.UplinkIfIndex = switchPrimaryUplink(st)
}

// switchPrimaryUplink es el uplink que resume el switch en la vista NOC: el
// de más tráfico de subida entre los elegidos (0 si no hay).
func switchPrimaryUplink(st *persisted) int {
	best, bestBps := 0, -1.0
	for _, x := range st.Interfaces {
		if !x.Uplink || !x.Monitored {
			continue
		}
		bps := 0.0
		if x.AvgTxBps != nil {
			bps = *x.AvgTxBps
		}
		if bps > bestBps {
			best, bestBps = x.IfIndex, bps
		}
	}
	return best
}

// switchUplinkHits evalúa los uplinks de un switch: uno caído (aviso
// interface_down) o inestable (≥ FlapThreshold cambios en la última hora,
// aviso interface_flapping). Los puertos de acceso no cambian el estado.
func switchUplinkHits(st *persisted, now time.Time) ([]api.ThresholdHit, bool) {
	var hits []api.ThresholdHit
	down := false
	for i := range st.Interfaces {
		x := &st.Interfaces[i]
		x.Flaps = domain.TrimFlaps(x.Flaps, now)
		if !x.Uplink || !x.Monitored {
			continue
		}
		if x.AdminUp != nil && !*x.AdminUp {
			continue // apagado a propósito
		}
		if x.OperStatus != nil && *x.OperStatus != api.OperUp {
			down = true
			hits = append(hits, api.ThresholdHit{Metric: "interface_down", Level: "warning", Value: 0, Threshold: 1, InterfaceName: str(x.Name)})
			continue
		}
		if n := domain.CountFlaps(x.Flaps, now, time.Hour); n >= domain.FlapThreshold {
			hits = append(hits, api.ThresholdHit{Metric: "interface_flapping", Level: "warning", Value: float64(n),
				Threshold: domain.FlapThreshold, InterfaceName: str(x.Name)})
		}
	}
	return hits, down
}

// ListSwitches devuelve el estado de los switches del tenant (filas de
// GET /switches/status): estado efectivo como los routers, plataforma y
// resumen de puertos.
func (e *Engine) ListSwitches(_ context.Context, tenant uuid.UUID, sites []uuid.UUID) ([]api.SwitchStatus, api.PollerHealth, error) {
	now := e.now()
	healthy := e.pollerHealthy(now)
	var out []api.SwitchStatus
	for _, r := range e.snapshot() {
		r.mu.Lock()
		if r.TenantID != tenant || !r.Switch || (len(sites) > 0 && !slices.Contains(sites, r.SiteID)) {
			r.mu.Unlock()
			continue
		}
		s := api.SwitchStatus{RouterStatus: e.status(r, now, healthy), Platform: r.Platform, SFlowEnabled: r.SFlow,
			NeighborsCount: len(r.st.Neighbors)}
		for i := range r.st.Interfaces {
			x := &r.st.Interfaces[i]
			if !x.Monitored {
				continue
			}
			s.PortsMonitored++
			if x.Uplink {
				s.Uplinks++
				if x.OperStatus != nil && *x.OperStatus != api.OperUp && (x.AdminUp == nil || *x.AdminUp) {
					s.UplinksDown++
				}
			}
			if domain.CountFlaps(x.Flaps, now, time.Hour) >= domain.FlapThreshold {
				s.PortsFlapping++
			}
		}
		r.mu.Unlock()
		out = append(out, s)
	}
	if out == nil {
		out = []api.SwitchStatus{}
	}
	slices.SortStableFunc(out, func(a, b api.SwitchStatus) int {
		switch {
		case domain.StatusLess(a.RouterStatus, b.RouterStatus):
			return -1
		case domain.StatusLess(b.RouterStatus, a.RouterStatus):
			return 1
		}
		return 0
	})
	return out, e.health(tenant, now, true), nil
}

// SwitchPorts devuelve los puertos de un switch con su elección, última
// muestra, flaps y vecino (GET /switches/{id}/ports). ok=false si no es un
// switch del tenant.
func (e *Engine) SwitchPorts(_ context.Context, tenant, id uuid.UUID) (*api.SwitchPorts, bool, error) {
	r := e.find(tenant, id)
	if r == nil {
		return nil, false, nil
	}
	now := e.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.Switch {
		return nil, false, nil
	}
	st := &r.st
	sel, configured := e.portSelection(id)
	out := &api.SwitchPorts{RouterID: id, Platform: r.Platform, DiscoveredAt: st.DiscoveredAt, NeighborsAt: st.NeighborsAt,
		IntervalSeconds: int(e.Interval().Seconds()), MaxPorts: domain.MaxSwitchPorts, Data: []api.SwitchPort{},
		Selection: api.PortSelectionMeta{Configured: configured}}
	if configured {
		v := sel.Version
		out.Selection.Version, out.Selection.UpdatedAt, out.Selection.UpdatedBy = &v, &sel.UpdatedAt, sel.UpdatedBy
	}
	nbByIf := map[int][]neighborState{}
	for _, n := range st.Neighbors {
		nbByIf[n.LocalIfIndex] = append(nbByIf[n.LocalIfIndex], n)
	}
	sugg := map[int]bool{}
	for _, c := range stateCandidates(st) {
		sugg[c.IfIndex] = c.SuggestedUplink
	}
	for i := range st.Interfaces {
		x := &st.Interfaces[i]
		if !x.Monitored && !domain.IsSwitchPort(x.Type, x.Name) {
			continue // VLAN, gestión, loopback…: no son puertos del switch
		}
		p := api.SwitchPort{InterfaceID: x.InterfaceID, IfIndex: x.IfIndex, Name: x.Name, Type: domain.IfTypeName(x.Type), SpeedBps: x.SpeedBps,
			AdminUp: x.AdminUp, OperStatus: x.OperStatus, Monitored: x.Monitored, Uplink: st.isUplink(x, true), SuggestedUplink: sugg[x.IfIndex],
			Flaps1h: domain.CountFlaps(x.Flaps, now, time.Hour), Flaps24h: domain.CountFlaps(x.Flaps, now, domain.FlapWindow),
			LastChangeAt: x.LastChangeAt, Neighbors: []api.SwitchNeighbor{}}
		if x.Alias != "" {
			a := x.Alias
			p.Alias = &a
		}
		if x.Sample != nil {
			s := x.Sample.InterfaceSample
			s.Uplink = p.Uplink
			p.Sample = &s
		}
		for _, n := range nbByIf[x.IfIndex] {
			p.Neighbors = append(p.Neighbors, neighborView(n))
		}
		out.Data = append(out.Data, p)
		if x.Monitored {
			out.MonitoredCount++
		}
	}
	return out, true, nil
}

// SwitchNeighbors devuelve los vecinos LLDP de un switch.
func (e *Engine) SwitchNeighbors(_ context.Context, tenant, id uuid.UUID) (*api.SwitchNeighbors, bool, error) {
	r := e.find(tenant, id)
	if r == nil {
		return nil, false, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.Switch {
		return nil, false, nil
	}
	out := &api.SwitchNeighbors{Data: []api.SwitchNeighbor{}, DiscoveredAt: r.st.NeighborsAt}
	for _, n := range r.st.Neighbors {
		out.Data = append(out.Data, neighborView(n))
	}
	return out, true, nil
}

// SwitchPortNames devuelve los nombres de los puertos descubiertos del
// switch (validación de la elección) y su nodo.
func (e *Engine) SwitchPortNames(tenant, id uuid.UUID) (names map[string]bool, site uuid.UUID, ok bool) {
	r := e.find(tenant, id)
	if r == nil {
		return nil, uuid.Nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.Switch {
		return nil, uuid.Nil, false
	}
	names = map[string]bool{}
	for _, x := range r.st.Interfaces {
		names[x.Name] = true
	}
	return names, r.SiteID, true
}

func neighborView(n neighborState) api.SwitchNeighbor {
	v := api.SwitchNeighbor{LocalIfIndex: n.LocalIfIndex, LocalPort: n.LocalPort, SysName: n.SysName, PortID: n.PortID,
		PortDesc: n.PortDesc, ChassisID: n.ChassisID, Source: n.Source}
	if n.MgmtAddress != "" {
		a := n.MgmtAddress
		v.MgmtAddress = &a
	}
	if n.Platform != "" {
		p := n.Platform
		v.Platform = &p
	}
	if n.Known != "" {
		k := n.Known
		v.KnownDevice = &k
	}
	return v
}
