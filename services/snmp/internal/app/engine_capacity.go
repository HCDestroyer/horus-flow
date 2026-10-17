package app

import (
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/api"
)

// IfaceSnapshot es una interfaz sondeada con su última muestra (para el
// análisis de capacidad: enlaces de subida, utilización en vivo).
type IfaceSnapshot struct {
	InterfaceID uuid.UUID
	Name        string
	Alias       string
	SpeedBps    *int64
	OperStatus  *string
	AutoUplink  bool
	Monitored   bool
	RxBps       *float64
	TxBps       *float64
	SampledAt   *time.Time
}

// RouterSnapshot es un router objetivo con sus interfaces.
type RouterSnapshot struct {
	TenantID uuid.UUID
	SiteID   uuid.UUID
	RouterID uuid.UUID
	Name     string
	SiteName string
	Status   string
	Reason   string
	// Vendor elige de dónde salen las sondas por enlace: netwatch por la API
	// REST (mikrotik) o NQA por SNMP (huawei).
	Vendor     string
	Interfaces []IfaceSnapshot
}

// Snapshot devuelve los routers objetivo (todos los tenants) con sus
// interfaces y el estado efectivo, sin bloquear el sondeo más que una copia.
func (e *Engine) Snapshot(now time.Time) []RouterSnapshot {
	healthy := e.pollerHealthy(now)
	routers := e.snapshot()
	out := make([]RouterSnapshot, 0, len(routers))
	for _, r := range routers {
		r.mu.Lock()
		st := e.status(r, now, healthy)
		rs := RouterSnapshot{TenantID: r.TenantID, SiteID: r.SiteID, RouterID: r.RouterID, Name: r.Name, SiteName: r.SiteName,
			Status: st.Status, Vendor: r.Vendor}
		if st.Reason != nil {
			rs.Reason = *st.Reason
		}
		for i := range r.st.Interfaces {
			x := &r.st.Interfaces[i]
			is := IfaceSnapshot{InterfaceID: x.InterfaceID, Name: x.Name, Alias: x.Alias, SpeedBps: x.SpeedBps, OperStatus: x.OperStatus,
				AutoUplink: r.st.UplinkIfIndex != 0 && x.IfIndex == r.st.UplinkIfIndex, Monitored: x.Monitored}
			if x.Sample != nil {
				is.RxBps, is.TxBps = x.Sample.RxBps, x.Sample.TxBps
				at := x.Sample.SampledAt
				is.SampledAt = &at
			}
			rs.Interfaces = append(rs.Interfaces, is)
		}
		r.mu.Unlock()
		out = append(out, rs)
	}
	return out
}

// OperUp interpreta el estado operativo (nil = desconocido).
func (i IfaceSnapshot) OperUp() *bool {
	if i.OperStatus == nil {
		return nil
	}
	v := *i.OperStatus == api.OperUp
	return &v
}
