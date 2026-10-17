package app

import (
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// persisted es el estado de un router que sobrevive a un reinicio (JSON en
// el bucket KV snmp_router_state, clave router_id).
type persisted struct {
	Machine        domain.Machine     `json:"machine"`
	Signals        api.Signals        `json:"signals"`
	Device         api.DeviceSample   `json:"device"`
	Thresholds     []api.ThresholdHit `json:"thresholds"`
	Streaks        domain.Streaks     `json:"streaks"`
	LastObservedAt *time.Time         `json:"last_observed_at"`
	LastSNMPOKAt   *time.Time         `json:"last_snmp_ok_at"`
	PrevUptime     *uint64            `json:"prev_uptime_ticks"`
	Interfaces     []ifState          `json:"interfaces"`
	DiscoveredAt   *time.Time         `json:"discovered_at"`
	UplinkIfIndex  int                `json:"uplink_if_index"`
	HealthAt       *time.Time         `json:"health_at"`
	// Señales externas al sondeo.
	WireGuard   string     `json:"wireguard"`
	WireGuardAt *time.Time `json:"wireguard_at"`
	ICMP        icmpState  `json:"icmp"`
	// ExporterLastFlowAt es el último flujo del exportador (estado del collector).
	ExporterLastFlowAt *time.Time `json:"exporter_last_flow_at,omitempty"`
}

type icmpState struct {
	Signal string     `json:"signal"`
	RTTMs  *float64   `json:"rtt_ms"`
	Loss   *float64   `json:"loss"`
	Fails  int        `json:"fails"`
	At     *time.Time `json:"at"`
}

// ifState es una interfaz descubierta con su última muestra y contadores.
type ifState struct {
	InterfaceID uuid.UUID        `json:"interface_id"`
	IfIndex     int              `json:"if_index"`
	Name        string           `json:"name"`
	Alias       string           `json:"alias"`
	Type        int              `json:"type"`
	SpeedBps    *int64           `json:"speed_bps"`
	AdminUp     *bool            `json:"admin_up"`
	OperStatus  *string          `json:"oper_status"`
	Monitored   bool             `json:"monitored"`
	LastSeenAt  *time.Time       `json:"last_seen_at"`
	Prev        *domain.Counters `json:"prev"`
	Sample      *ifSample        `json:"sample"`
	AvgTxBps    *float64         `json:"avg_tx_bps"`
}

// ifSample es la última muestra (InterfaceSample + crudos para depuración).
type ifSample struct {
	api.InterfaceSample
	RxBytesDelta *uint64 `json:"rx_bytes_delta,string"`
	TxBytesDelta *uint64 `json:"tx_bytes_delta,string"`
	RxOctetsRaw  uint64  `json:"rx_octets_raw,string"`
	TxOctetsRaw  uint64  `json:"tx_octets_raw,string"`
	SampledAt    time.Time
}

func (s *persisted) iface(ifIndex int) *ifState {
	for i := range s.Interfaces {
		if s.Interfaces[i].IfIndex == ifIndex {
			return &s.Interfaces[i]
		}
	}
	return nil
}

// status construye la fila de GET /routers/status con el estado efectivo.
func (e *Engine) status(r *router, now time.Time, healthy bool) api.RouterStatus {
	st := &r.st
	var observed, reason *string
	if st.Machine.State != "" {
		o, re := st.Machine.State, st.Machine.Reason
		observed, reason = &o, &re
	}
	eff, since := domain.Effective(st.Machine.State, st.Machine.Since, st.LastObservedAt, now, e.Interval(), healthy)
	sig := st.Signals
	if sig.ICMP == "" {
		sig.ICMP = api.SignalUnknown
	}
	if sig.SNMP == "" {
		sig.SNMP = api.SignalUnknown
	}
	if sig.WireGuard == "" {
		sig.WireGuard = api.SignalUnknown
	}
	dev := st.Device
	if dev.Sensors == nil {
		dev.Sensors = []api.Sensor{}
	}
	out := api.RouterStatus{RouterID: r.RouterID, SiteID: r.SiteID, Name: r.Name, SiteName: r.SiteName, Status: eff,
		ObservedState: observed, Reason: reason, Since: since, LastObservedAt: st.LastObservedAt, Signals: sig, Device: dev,
		Thresholds: st.Thresholds, LastFlowAt: st.ExporterLastFlowAt}
	if out.Thresholds == nil {
		out.Thresholds = []api.ThresholdHit{}
	}
	if len(st.Interfaces) > 0 && st.DiscoveredAt != nil {
		up, total := 0, 0
		for i := range st.Interfaces {
			x := &st.Interfaces[i]
			if !x.Monitored {
				continue
			}
			total++
			if x.OperStatus != nil && *x.OperStatus == api.OperUp {
				up++
			}
			if x.IfIndex == st.UplinkIfIndex && st.UplinkIfIndex != 0 {
				u := &api.Uplink{InterfaceID: x.InterfaceID, Name: x.Name, SpeedBps: x.SpeedBps}
				if x.Sample != nil {
					u.RxBps, u.TxBps = x.Sample.RxBps, x.Sample.TxBps
					if x.Sample.UtilizationRx != nil && x.Sample.UtilizationTx != nil {
						u.Utilization = f64(max(*x.Sample.UtilizationRx, *x.Sample.UtilizationTx))
					}
				}
				out.Uplink = u
			}
		}
		out.InterfacesUp, out.InterfacesTotal = &up, &total
	}
	return out
}

func (e *Engine) live(r *router, now time.Time, healthy bool) *api.RouterLiveMetrics {
	out := &api.RouterLiveMetrics{Status: e.status(r, now, healthy), IntervalSeconds: int(e.Interval().Seconds()), Interfaces: []api.InterfaceSample{}}
	for i := range r.st.Interfaces {
		x := &r.st.Interfaces[i]
		if !x.Monitored || x.Sample == nil {
			continue
		}
		s := x.Sample.InterfaceSample
		s.Uplink = x.IfIndex == r.st.UplinkIfIndex && r.st.UplinkIfIndex != 0
		out.Interfaces = append(out.Interfaces, s)
	}
	return out
}

func (e *Engine) interfaces(r *router) *api.RouterInterfaces {
	out := &api.RouterInterfaces{Data: []api.RouterInterface{}, DiscoveredAt: r.st.DiscoveredAt}
	for i := range r.st.Interfaces {
		x := &r.st.Interfaces[i]
		var alias *string
		if x.Alias != "" {
			a := x.Alias
			alias = &a
		}
		out.Data = append(out.Data, api.RouterInterface{ID: x.InterfaceID, IfIndex: x.IfIndex, Name: x.Name, Alias: alias,
			Type: domain.IfTypeName(x.Type), SpeedBps: x.SpeedBps, Monitored: x.Monitored,
			Uplink: x.IfIndex == r.st.UplinkIfIndex && r.st.UplinkIfIndex != 0, OperStatus: x.OperStatus, AdminUp: x.AdminUp,
			LastSeenAt: x.LastSeenAt})
	}
	return out
}

func f64(v float64) *float64 { return &v }
func str(s string) *string   { return &s }
