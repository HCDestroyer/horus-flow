package app

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tw "github.com/hcdestroyer/horus-flow/services/analytics/api/trafficwidgets"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/chread"
	snmpapi "github.com/hcdestroyer/horus-flow/services/snmp/api"
)

// WidgetRoutersStatusGrid es el tipo de widget "Estado de routers" (EP-08,
// widget-types.json): una fila por router con el estado que publica el rol
// snmp en el proceso (RouterStatusReader.List).
const WidgetRoutersStatusGrid = "routers_status_grid"

// routersStatusColumns son las columnas de la tabla, en el orden y con los
// tipos que espera el frontend (apps/frontend/app/widgets/shapes.ts).
var routersStatusColumns = []tw.Column{
	{Key: "router", Type: "string"}, {Key: "site", Type: "string"}, {Key: "router_id", Type: "string"},
	{Key: "status", Type: "state"}, {Key: "reason", Type: "string"}, {Key: "since", Type: "timestamp"},
	{Key: "last_observed_at", Type: "timestamp"}, {Key: "cpu_percent", Type: "number"},
	{Key: "memory_percent", Type: "number"}, {Key: "temperature_celsius", Type: "number"},
	{Key: "uptime_seconds", Type: "number"}, {Key: "rx_bps", Type: "number"}, {Key: "tx_bps", Type: "number"},
	{Key: "uplink", Type: "string"}, {Key: "exporter_state", Type: "string"}, {Key: "tunnel_state", Type: "string"},
	{Key: "interfaces_up", Type: "number"}, {Key: "interfaces_total", Type: "number"},
}

// routersStatus resuelve routers_status_grid: filas de RouterStatusReader.List
// (problemas primero) filtradas por config.show y ordenadas por config.sort.
// Sin el rol snmp en el proceso el widget es "no disponible" (ErrUnavailable),
// nunca una lista vacía engañosa. No lleva datos personales.
func (w *Widgets) routersStatus(ctx context.Context, out *tw.WidgetData, sc Scope, cfg widgetConfig, now time.Time) error {
	rd := w.svc.routers()
	if rd == nil {
		return fmt.Errorf("%w: %v", chread.ErrUnavailable, ErrRoutersUnavailable)
	}
	list, health, err := rd.List(ctx, sc.Tenant, sc.Sites)
	if err != nil {
		return fmt.Errorf("%w: router status: %v", chread.ErrUnavailable, err)
	}
	list = slices.Clone(list) // el filtro y el orden no tocan la lista del lector
	if len(cfg.Show) > 0 {
		list = slices.DeleteFunc(list, func(r snmpapi.RouterStatus) bool { return !slices.Contains(cfg.Show, r.Status) })
	}
	sortRouters(list, cfg.Sort)
	out.Data = tw.Data{Kind: "table", Columns: routersStatusColumns, Rows: make([]map[string]any, 0, len(list))}
	for i := range list {
		out.Data.Rows = append(out.Data.Rows, routerRow(&list[i]))
	}
	if health.LastCycleAt != nil {
		f := int(now.Sub(*health.LastCycleAt).Seconds())
		if f < 0 {
			f = 0
		}
		out.Meta.FreshnessSeconds = &f
	}
	out.Meta.Partial = !health.Healthy
	return nil
}

// uplinkTraffic es rx+tx del enlace de subida (nil sin muestra).
func uplinkTraffic(r *snmpapi.RouterStatus) *float64 {
	if r.Uplink == nil || (r.Uplink.RxBps == nil && r.Uplink.TxBps == nil) {
		return nil
	}
	var t float64
	if r.Uplink.RxBps != nil {
		t += *r.Uplink.RxBps
	}
	if r.Uplink.TxBps != nil {
		t += *r.Uplink.TxBps
	}
	return &t
}

// sortRouters ordena por config.sort: status conserva el orden de List
// (problemas primero, después por nombre); name alfabético; cpu y traffic
// descendentes con los routers sin muestra al final (estable: empates como List).
func sortRouters(list []snmpapi.RouterStatus, by string) {
	var less func(a, b *snmpapi.RouterStatus) bool
	switch by {
	case snmpapi.StatusOrderByName:
		less = func(a, b *snmpapi.RouterStatus) bool {
			if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
				return c < 0
			}
			return a.SiteName < b.SiteName
		}
	case snmpapi.StatusOrderByCPU:
		less = func(a, b *snmpapi.RouterStatus) bool { return descNil(a.Device.CPUPercent, b.Device.CPUPercent) }
	case snmpapi.StatusOrderByTraffic:
		less = func(a, b *snmpapi.RouterStatus) bool { return descNil(uplinkTraffic(a), uplinkTraffic(b)) }
	default:
		return
	}
	sort.SliceStable(list, func(i, j int) bool { return less(&list[i], &list[j]) })
}

// descNil: mayor primero; nil al final.
func descNil(a, b *float64) bool {
	switch {
	case a == nil:
		return false
	case b == nil:
		return true
	}
	return *a > *b
}

func routerRow(r *snmpapi.RouterStatus) map[string]any {
	row := map[string]any{
		"router": r.Name, "site": r.SiteName, "router_id": r.RouterID.String(), "status": r.Status,
		"reason": r.Reason, "since": fmtTime(r.Since), "last_observed_at": fmtTime(r.LastObservedAt),
		"cpu_percent": r.Device.CPUPercent, "memory_percent": r.Device.MemoryPercent,
		"temperature_celsius": r.Device.TemperatureCelsius, "uptime_seconds": r.Device.UptimeSeconds,
		"rx_bps": nil, "tx_bps": nil, "uplink": nil, "exporter_state": r.Signals.Exporter,
		"tunnel_state": r.Signals.WireGuard, "interfaces_up": r.InterfacesUp, "interfaces_total": r.InterfacesTotal,
	}
	if r.Uplink != nil {
		row["rx_bps"], row["tx_bps"], row["uplink"] = r.Uplink.RxBps, r.Uplink.TxBps, r.Uplink.Name
	}
	return row
}

func fmtTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}
