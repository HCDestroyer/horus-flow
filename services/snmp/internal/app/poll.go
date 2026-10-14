package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/clickhouse"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/snmpclient"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// runCycle sondea todos los objetivos con concurrencia acotada y encola el
// lote del ciclo en ClickHouse.
func (e *Engine) runCycle(ctx context.Context, scheduled time.Time) {
	e.mu.Lock()
	e.cycle++
	cycle := e.cycle
	e.mu.Unlock()
	routers := e.snapshot()
	var wg sync.WaitGroup
	var errs int
	var errMu sync.Mutex
	batch := &clickhouse.Batch{ID: fmt.Sprintf("snmp:%s:%d", e.instanceID, scheduled.Unix())}
	var batchMu sync.Mutex
	for _, r := range routers {
		select {
		case <-ctx.Done():
			return
		case e.sem <- struct{}{}:
		}
		wg.Add(1)
		go func(r *router) {
			defer wg.Done()
			defer func() { <-e.sem }()
			e.m.lag.Observe(e.now().Sub(scheduled).Seconds())
			res := e.pollOne(ctx, r, scheduled, cycle, false)
			if res.err != nil {
				errMu.Lock()
				errs++
				errMu.Unlock()
			}
			batchMu.Lock()
			batch.Devices = append(batch.Devices, res.device...)
			batch.Interfaces = append(batch.Interfaces, res.ifaces...)
			batchMu.Unlock()
		}(r)
	}
	wg.Wait()
	now := e.now()
	e.mu.Lock()
	e.lastCycle, e.lastErrs = &now, errs
	e.mu.Unlock()
	if e.o.Sink != nil {
		e.o.Sink.Enqueue(*batch)
	}
	e.updateGauges(now)
}

type pollResult struct {
	err    error
	device []clickhouse.DeviceRow
	ifaces []clickhouse.InterfaceRow
}

// pollOne sondea un router: SNMP (sistema, recursos, salud si toca,
// descubrimiento si toca, contadores), correlación de señales, estado con
// histéresis, eventos, telemetría, filas y persistencia.
func (e *Engine) pollOne(ctx context.Context, r *router, at time.Time, cycle uint64, manual bool) pollResult {
	r.mu.Lock()
	if r.polling {
		r.mu.Unlock()
		return pollResult{}
	}
	r.polling = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.polling = false
		r.mu.Unlock()
	}()
	start := e.now()
	cred, noCred := e.credential(ctx, r, start)
	var (
		sys     snmpclient.System
		res     snmpclient.Resources
		health  *snmpclient.Health
		disc    []snmpclient.Discovered
		ctrs    []snmpclient.Counters
		snmpErr error
	)
	r.mu.Lock()
	needDisc := r.st.DiscoveredAt == nil || start.Sub(*r.st.DiscoveredAt) >= e.o.Config.DiscoveryInterval
	needHealth := r.st.HealthAt == nil || start.Sub(*r.st.HealthAt) >= e.o.Config.HealthInterval || manual
	monitored := make([]int, 0, len(r.st.Interfaces))
	for _, x := range r.st.Interfaces {
		if x.Monitored {
			monitored = append(monitored, x.IfIndex)
		}
	}
	tunnel := r.Tunnel
	r.mu.Unlock()
	if cred != nil {
		snmpErr = func() error {
			pctx, cancel := context.WithTimeout(ctx, 4*e.o.Config.Timeout*time.Duration(e.o.Config.Retries+1)+10*time.Second)
			defer cancel()
			t, err := e.o.Dialer.Dial(pctx, tunnel, *cred)
			if err != nil {
				return err
			}
			defer func() { _ = t.Close() }()
			if sys, err = snmpclient.ReadSystem(pctx, t); err != nil {
				return err
			}
			if res, err = snmpclient.ReadResources(pctx, t); err != nil {
				return err
			}
			if needDisc {
				if disc, err = snmpclient.Discover(pctx, t); err != nil {
					return err
				}
				monitored = monitored[:0]
				for _, d := range disc {
					if domain.Monitored(d.Type, d.Name) {
						monitored = append(monitored, d.IfIndex)
					}
				}
			}
			if needHealth {
				h, err := snmpclient.ReadHealth(pctx, t)
				if err != nil {
					return err
				}
				health = &h
			}
			var up uint64
			if sys.UptimeTicks != nil {
				up = *sys.UptimeTicks
			}
			if ctrs, err = snmpclient.ReadCounters(pctx, t, monitored, at, up); err != nil {
				return err
			}
			return nil
		}()
	}
	dur := e.now().Sub(start)
	e.m.duration.WithLabelValues(vendorMikroTik).Observe(dur.Seconds())
	out := pollResult{}
	var events []Event
	r.mu.Lock()
	st := &r.st
	snmpSignal := api.SignalUnknown
	switch {
	case cred == nil:
		e.m.polls.WithLabelValues(vendorMikroTik, "no_credentials").Inc()
	case snmpErr != nil && snmpclient.IsAuth(snmpErr):
		snmpSignal = api.SignalAuthFailed
		r.cred = nil // se recarga en el siguiente ciclo (rotación)
		e.m.polls.WithLabelValues(vendorMikroTik, "auth_failed").Inc()
		out.err = snmpErr
	case snmpErr != nil:
		snmpSignal = api.SignalFail
		if errors.Is(snmpErr, context.DeadlineExceeded) || isTimeout(snmpErr) {
			e.m.polls.WithLabelValues(vendorMikroTik, "timeout").Inc()
		} else {
			e.m.polls.WithLabelValues(vendorMikroTik, "error").Inc()
		}
		out.err = snmpErr
	default:
		snmpSignal = api.SignalOK
		e.m.polls.WithLabelValues(vendorMikroTik, "ok").Inc()
	}
	if snmpSignal == api.SignalOK {
		events = append(events, e.applySNMP(r, at, dur, sys, res, health, disc, ctrs)...)
		st.LastSNMPOKAt = &at
	}
	// Señales externas.
	exporter := e.exporterState(ctx, r.RouterID)
	sig := api.Signals{ICMP: st.ICMP.Signal, SNMP: snmpSignal, WireGuard: st.WireGuard, Exporter: exporter}
	if sig.ICMP == "" {
		sig.ICMP = api.SignalUnknown
	}
	if sig.WireGuard == "" {
		sig.WireGuard = api.SignalUnknown
	}
	// Umbrales (solo con SNMP): sostenidos por rachas.
	var hits []api.ThresholdHit
	if snmpSignal == api.SignalOK {
		hits = st.Streaks.Evaluate(e.thresholdsFor(r.TenantID), st.Device)
	} else {
		st.Streaks = domain.Streaks{}
	}
	uplinkDown := false
	if up := st.iface(st.UplinkIfIndex); up != nil && up.OperStatus != nil && *up.OperStatus != api.OperUp && snmpSignal == api.SignalOK {
		uplinkDown = true
		hits = append(hits, api.ThresholdHit{Metric: "interface_down", Level: "warning", Value: 0, Threshold: 1, InterfaceName: str(up.Name)})
	}
	if hits == nil {
		hits = []api.ThresholdHit{}
	}
	state, reason := domain.Candidate(domain.Observation{Signals: sig, Hits: hits, UplinkDown: uplinkDown, NoCredentials: noCred})
	prev := st.Machine.State
	tr := st.Machine.Observe(state, reason, at)
	st.Signals, st.Thresholds, st.LastObservedAt = sig, hits, &at
	if tr.Changed {
		var previous *string
		if prev != "" {
			previous = &prev
		}
		events = append(events, Event{Type: TypeStateChanged, Entity: r.RouterID.String(), TenantID: &r.TenantID, AggregateType: "router",
			Version: st.Machine.Version, At: at, Data: map[string]any{"router_id": r.RouterID, "site_id": r.SiteID, "version": st.Machine.Version,
				"previous_state": previous, "state": tr.State, "reason": tr.Reason, "signals": sig, "changed_at": at.UTC().Format(time.RFC3339),
				"router_name": r.Name, "site_name": r.SiteName, "thresholds": hits}})
		e.log.InfoContext(ctx, "snmp: router state changed", "router_id", r.RouterID, "previous", prev, "state", tr.State, "reason", tr.Reason)
	}
	// Telemetría y filas ClickHouse (siempre: un ciclo sin SNMP también es una muestra).
	tele := e.telemetry(r, at, dur, snmpSignal == api.SignalOK)
	out.device = append(out.device, e.deviceRow(r, at, dur, snmpSignal == api.SignalOK))
	if snmpSignal == api.SignalOK {
		out.ifaces = append(out.ifaces, e.interfaceRows(r, at)...)
	}
	r.lastPoll = at
	saved, _ := json.Marshal(*st)
	r.mu.Unlock()

	for _, ev := range events {
		e.publish(ctx, ev)
	}
	for _, s := range tele {
		if e.o.Telemetry != nil {
			if err := e.o.Telemetry.Publish(ctx, s); err != nil {
				e.m.events.WithLabelValues("telemetry").Inc()
			}
		}
	}
	if e.o.Store != nil {
		if err := e.o.Store.Save(ctx, r.RouterID, saved); err != nil {
			e.m.events.WithLabelValues("state").Inc()
		}
	}
	return out
}

// credential devuelve la credencial SNMPv3 (memorizada; recarga tras fallo
// de autenticación o cada 5 min si no existe).
func (e *Engine) credential(ctx context.Context, r *router, now time.Time) (*snmpclient.Credential, bool) {
	r.mu.Lock()
	if r.cred != nil {
		c := r.cred
		r.mu.Unlock()
		return c, false
	}
	if r.noCred && now.Sub(r.credAt) < credentialRetry {
		r.mu.Unlock()
		return nil, true
	}
	r.mu.Unlock()
	src, ok := e.o.Credentials()
	if !ok {
		return nil, true
	}
	c, err := src.RevealSNMPCredential(ctx, r.TenantID, r.RouterID)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.credAt = now
	if err != nil {
		if !errors.Is(err, devapi.ErrNoCredential) && !errors.Is(err, devapi.ErrRouterNotFound) {
			e.log.WarnContext(ctx, "snmp: credential not available", "router_id", r.RouterID, "error", err)
		}
		r.noCred = true
		return nil, true
	}
	r.noCred = false
	r.cred = &snmpclient.Credential{Username: c.Username, AuthProtocol: c.AuthProtocol, AuthPassword: c.AuthPassword,
		PrivProtocol: c.PrivProtocol, PrivPassword: c.PrivPassword}
	return r.cred, false
}

// applySNMP vuelca una lectura SNMP correcta en el estado (r.mu tomado).
func (e *Engine) applySNMP(r *router, at time.Time, dur time.Duration, sys snmpclient.System, res snmpclient.Resources,
	health *snmpclient.Health, disc []snmpclient.Discovered, ctrs []snmpclient.Counters) []Event {
	st := &r.st
	var events []Event
	d := &st.Device
	d.SampledAt = &at
	ms := int(dur.Milliseconds())
	d.PollDurationMs = &ms
	d.Model = sys.Model
	if sys.UptimeTicks != nil {
		secs := int64(*sys.UptimeTicks / 100) //nolint:gosec // uptime
		if st.PrevUptime != nil && *sys.UptimeTicks < *st.PrevUptime {
			prevSecs := int64(*st.PrevUptime / 100) //nolint:gosec // uptime
			events = append(events, Event{Type: TypeRebooted, Entity: r.RouterID.String(), TenantID: &r.TenantID, AggregateType: "router",
				Version: st.Machine.Version, At: at, Data: map[string]any{"router_id": r.RouterID, "previous_uptime_seconds": prevSecs,
					"uptime_seconds": secs, "detected_at": at.UTC().Format(time.RFC3339), "site_id": r.SiteID, "router_name": r.Name}})
		}
		st.PrevUptime = sys.UptimeTicks
		d.UptimeSeconds = &secs
	} else {
		d.UptimeSeconds = nil
	}
	if n := len(res.CPULoads); n > 0 {
		sum, mx := 0, 0
		for _, l := range res.CPULoads {
			sum += l
			mx = max(mx, l)
		}
		d.CPUPercent = f64(float64(sum) / float64(n))
		d.CPUMaxCorePercent = f64(float64(mx))
		d.CPUCores = &n
	} else {
		d.CPUPercent, d.CPUMaxCorePercent, d.CPUCores = nil, nil, nil
	}
	d.MemoryUsedBytes, d.MemoryTotalBytes, d.MemoryPercent = res.MemoryUsedBytes, res.MemoryTotalBytes, nil
	if res.MemoryTotalBytes != nil && *res.MemoryTotalBytes > 0 && res.MemoryUsedBytes != nil {
		d.MemoryPercent = f64(100 * float64(*res.MemoryUsedBytes) / float64(*res.MemoryTotalBytes))
	}
	if health != nil {
		d.TemperatureCelsius, d.CPUTemperatureCelsius, d.Voltage = health.TemperatureCelsius, health.CPUTemperatureCelsius, health.Voltage
		d.Firmware, d.SerialNumber = health.Firmware, health.SerialNumber
		d.Sensors = health.Sensors
		if d.Sensors == nil {
			d.Sensors = []api.Sensor{}
		}
		st.HealthAt = &at
	}
	if disc != nil {
		events = append(events, e.applyDiscovery(r, at, disc)...)
	}
	// Contadores → tasas.
	for _, c := range ctrs {
		x := st.iface(c.IfIndex)
		if x == nil {
			continue
		}
		var speed int64
		if x.SpeedBps != nil {
			speed = *x.SpeedBps
		}
		rates := domain.Delta(x.Prev, c.Counters, speed)
		cc := c.Counters
		x.Prev = &cc
		x.LastSeenAt = &at
		if c.OperStatus != nil {
			name := domain.OperStatusName(*c.OperStatus)
			if x.OperStatus != nil && *x.OperStatus != name {
				events = append(events, Event{Type: TypeOperStatusChanged, Entity: x.InterfaceID.String(), TenantID: &r.TenantID,
					AggregateType: "interface", At: at, Data: map[string]any{"interface_id": x.InterfaceID, "router_id": r.RouterID,
						"if_index": x.IfIndex, "name": x.Name, "previous": *x.OperStatus, "current": name, "observed_at": at.UTC().Format(time.RFC3339),
						"site_id": r.SiteID, "router_name": r.Name, "uplink": st.UplinkIfIndex != 0 && x.IfIndex == st.UplinkIfIndex}})
			}
			x.OperStatus = &name
		}
		s := &ifSample{SampledAt: at, RxOctetsRaw: c.InOctets, TxOctetsRaw: c.OutOctets}
		s.InterfaceID, s.IfIndex, s.Name, s.OperStatus, s.SpeedBps = x.InterfaceID, x.IfIndex, x.Name, x.OperStatus, x.SpeedBps
		if x.Alias != "" {
			s.Alias = str(x.Alias)
		}
		s.CounterDiscontinuity = rates.Discontinuity
		if !rates.Discontinuity {
			s.RxBps, s.TxBps, s.RxPps, s.TxPps = rates.RxBps, rates.TxBps, rates.RxPps, rates.TxPps
			s.RxErrorsDelta, s.TxErrorsDelta, s.RxDropsDelta, s.TxDropsDelta = rates.RxErrors, rates.TxErrors, rates.RxDrops, rates.TxDrops
			s.UtilizationRx, s.UtilizationTx = rates.UtilRx, rates.UtilTx
			s.RxBytesDelta, s.TxBytesDelta = rates.RxBytesDelta, rates.TxBytesDelta
			if rates.TxBps != nil {
				x.AvgTxBps = domain.EWMA(x.AvgTxBps, *rates.TxBps)
			}
		}
		x.Sample = s
	}
	// Uplink.
	cands := make([]domain.UplinkCandidate, 0, len(st.Interfaces))
	for _, x := range st.Interfaces {
		if !x.Monitored {
			continue
		}
		c := domain.UplinkCandidate{IfIndex: x.IfIndex, IfType: x.Type, Name: x.Name, Alias: x.Alias, AvgTxBps: x.AvgTxBps,
			OperUp: x.OperStatus != nil && *x.OperStatus == api.OperUp}
		for _, u := range r.Upstream {
			if u == x.IfIndex {
				c.Upstream = true
			}
		}
		cands = append(cands, c)
	}
	st.UplinkIfIndex = domain.ChooseUplink(cands)
	return events
}

// applyDiscovery fusiona el inventario IF-MIB con el conocido (r.mu tomado).
func (e *Engine) applyDiscovery(r *router, at time.Time, disc []snmpclient.Discovered) []Event {
	st := &r.st
	known := map[int]*ifState{}
	for i := range st.Interfaces {
		known[st.Interfaces[i].IfIndex] = &st.Interfaces[i]
	}
	var next []ifState
	var payload []map[string]any
	for _, d := range disc {
		x := ifState{IfIndex: d.IfIndex}
		if k, ok := known[d.IfIndex]; ok && k.Name == d.Name {
			x = *k
		}
		x.Name, x.Alias, x.Type, x.AdminUp = d.Name, d.Alias, d.Type, d.AdminUp
		x.InterfaceID = api.InterfaceID(r.RouterID, d.Name)
		x.Monitored = domain.Monitored(d.Type, d.Name)
		x.SpeedBps = nil
		if d.HighSpeedMbps > 0 {
			sp := int64(d.HighSpeedMbps) * 1_000_000 //nolint:gosec // Mbit/s
			x.SpeedBps = &sp
		}
		if d.OperStatus != nil {
			x.OperStatus = str(domain.OperStatusName(*d.OperStatus))
		}
		x.LastSeenAt = &at
		next = append(next, x)
		payload = append(payload, map[string]any{"interface_id": x.InterfaceID, "if_index": d.IfIndex, "name": d.Name, "alias": d.Alias,
			"type": domain.IfTypeName(d.Type), "speed_bps": x.SpeedBps, "monitored": x.Monitored})
	}
	st.Interfaces = next
	st.DiscoveredAt = &at
	if payload == nil {
		payload = []map[string]any{}
	}
	return []Event{{Type: TypeInterfacesDiscovered, Entity: r.RouterID.String(), TenantID: &r.TenantID, AggregateType: "router",
		Version: st.Machine.Version, At: at, Data: map[string]any{"router_id": r.RouterID, "interfaces": payload,
			"discovered_at": at.UTC().Format(time.RFC3339)}}}
}

func (e *Engine) exporterState(ctx context.Context, id uuid.UUID) *string {
	if e.o.Exporters == nil {
		return nil
	}
	fe, err := e.o.Exporters.Get(ctx, id)
	if err != nil || fe == nil {
		return nil
	}
	return str(fe.State)
}

// telemetry construye las muestras TLM_SNMP (r.mu tomado).
func (e *Engine) telemetry(r *router, at time.Time, dur time.Duration, snmpOK bool) []Sample {
	if e.o.Telemetry == nil {
		return nil
	}
	st := &r.st
	batch := uuid.Must(uuid.NewV7()).String()
	dev := map[string]any{"batch_id": batch, "router_id": r.RouterID, "site_id": r.SiteID, "sampled_at": at.UTC().Format(time.RFC3339),
		"poll_duration_ms": dur.Milliseconds(), "snmp_ok": snmpOK, "uptime_seconds": st.Device.UptimeSeconds,
		"cpu_percent": st.Device.CPUPercent, "cpu_max_core_percent": st.Device.CPUMaxCorePercent, "cpu_cores": st.Device.CPUCores,
		"memory_used_bytes": u64s(st.Device.MemoryUsedBytes), "memory_total_bytes": u64s(st.Device.MemoryTotalBytes),
		"memory_percent": st.Device.MemoryPercent, "temperature_celsius": st.Device.TemperatureCelsius,
		"cpu_temperature_celsius": st.Device.CPUTemperatureCelsius, "voltage": st.Device.Voltage, "firmware": st.Device.Firmware,
		"model": st.Device.Model, "icmp_rtt_ms": st.ICMP.RTTMs, "icmp_loss_ratio": st.ICMP.Loss, "sensors": st.Device.Sensors,
		"state": st.Machine.State, "reason": st.Machine.Reason, "signals": st.Signals}
	body, _ := json.Marshal(dev)
	out := []Sample{{Subject: "horus.telemetry.snmp.device." + r.RouterID.String(), Type: TypeDeviceSampled, TenantID: r.TenantID,
		BatchID: batch + ":d", At: at, Body: body}}
	if !snmpOK {
		return out
	}
	var ifs []any
	for i := range st.Interfaces {
		x := &st.Interfaces[i]
		if x.Monitored && x.Sample != nil && x.Sample.SampledAt.Equal(at) {
			s := *x.Sample
			s.Uplink = x.IfIndex == st.UplinkIfIndex && st.UplinkIfIndex != 0
			ifs = append(ifs, s)
		}
	}
	if ifs == nil {
		ifs = []any{}
	}
	ib, _ := json.Marshal(map[string]any{"batch_id": batch, "router_id": r.RouterID, "sampled_at": at.UTC().Format(time.RFC3339),
		"interval_seconds": int(e.Interval().Seconds()), "interfaces": ifs})
	out = append(out, Sample{Subject: "horus.telemetry.snmp.interface." + r.RouterID.String(), Type: TypeInterfaceSampled,
		TenantID: r.TenantID, BatchID: batch + ":i", At: at, Body: ib})
	return out
}

func u64s(v *uint64) any {
	if v == nil {
		return nil
	}
	return fmt.Sprint(*v)
}

func (e *Engine) deviceRow(r *router, at time.Time, dur time.Duration, snmpOK bool) clickhouse.DeviceRow {
	st := &r.st
	row := clickhouse.DeviceRow{TenantID: r.TenantID, SiteID: r.SiteID, RouterID: r.RouterID, TS: at,
		PollDurationMs: uint32(min(dur.Milliseconds(), 1<<32-1)), //nolint:gosec // acotado
		ICMPOK:         st.ICMP.Signal == api.SignalOK, SNMPOK: snmpOK, ICMPRTTMs: f32(st.ICMP.RTTMs), ICMPLossRatio: f32(st.ICMP.Loss)}
	if snmpOK {
		if st.Device.UptimeSeconds != nil {
			u := uint64(*st.Device.UptimeSeconds) //nolint:gosec // uptime ≥ 0
			row.UptimeSeconds = &u
		}
		row.CPUPercent, row.CPUMaxCorePercent, row.MemoryPercent = f32(st.Device.CPUPercent), f32(st.Device.CPUMaxCorePercent), f32(st.Device.MemoryPercent)
		row.MemoryUsedBytes, row.MemoryTotalBytes = st.Device.MemoryUsedBytes, st.Device.MemoryTotalBytes
		row.TemperatureCelsius, row.CPUTemperatureCelsius, row.Voltage = f32(st.Device.TemperatureCelsius), f32(st.Device.CPUTemperatureCelsius), f32(st.Device.Voltage)
	}
	return row
}

func (e *Engine) interfaceRows(r *router, at time.Time) []clickhouse.InterfaceRow {
	st := &r.st
	var out []clickhouse.InterfaceRow
	for i := range st.Interfaces {
		x := &st.Interfaces[i]
		if !x.Monitored || x.Sample == nil || !x.Sample.SampledAt.Equal(at) {
			continue
		}
		s := x.Sample
		row := clickhouse.InterfaceRow{TenantID: r.TenantID, SiteID: r.SiteID, RouterID: r.RouterID, InterfaceID: x.InterfaceID,
			IfIndex: uint32(x.IfIndex), Name: x.Name, TS: at, OperUp: x.OperStatus != nil && *x.OperStatus == api.OperUp, //nolint:gosec // ifIndex
			RxBps: s.RxBps, TxBps: s.TxBps, RxPps: s.RxPps, TxPps: s.TxPps,
			RxErrors: u64(s.RxErrorsDelta), TxErrors: u64(s.TxErrorsDelta), RxDrops: u64(s.RxDropsDelta), TxDrops: u64(s.TxDropsDelta),
			UtilizationRx: f32(s.UtilizationRx), UtilizationTx: f32(s.UtilizationTx)}
		if x.SpeedBps != nil && *x.SpeedBps > 0 {
			sp := uint64(*x.SpeedBps)
			row.SpeedBps = &sp
		}
		out = append(out, row)
	}
	return out
}

func f32(v *float64) *float32 {
	if v == nil {
		return nil
	}
	x := float32(*v)
	return &x
}

func u64(v *int64) *uint64 {
	if v == nil || *v < 0 {
		return nil
	}
	x := uint64(*v)
	return &x
}

func (e *Engine) publish(ctx context.Context, ev Event) {
	if e.o.Events == nil {
		return
	}
	if err := e.o.Events.Publish(ctx, ev); err != nil {
		e.m.events.WithLabelValues("event").Inc()
		e.log.WarnContext(ctx, "snmp: event not published", "type", ev.Type, "error", err)
	}
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

// runICMP hace ping a todos los objetivos.
func (e *Engine) runICMP(ctx context.Context) {
	routers := e.snapshot()
	var wg sync.WaitGroup
	for _, r := range routers {
		select {
		case <-ctx.Done():
			return
		case e.sem <- struct{}{}:
		}
		wg.Add(1)
		go func(r *router) {
			defer wg.Done()
			defer func() { <-e.sem }()
			pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			res, err := e.o.Pinger.Ping(pctx, r.Tunnel, icmpProbes, 2*time.Second)
			now := e.now()
			r.mu.Lock()
			defer r.mu.Unlock()
			ic := &r.st.ICMP
			ic.At = &now
			if err != nil {
				return
			}
			if res.Received > 0 {
				ic.Fails = 0
				ic.Signal = api.SignalOK
				ms := float64(res.AvgRTT.Microseconds()) / 1000
				ic.RTTMs = &ms
			} else {
				ic.Fails++
				ic.RTTMs = nil
				if ic.Fails >= icmpFailCycles {
					ic.Signal = api.SignalFail
				} else if ic.Signal == "" {
					ic.Signal = api.SignalUnknown
				}
			}
			loss := res.Loss()
			ic.Loss = &loss
			r.st.Device.ICMPRTTMs, r.st.Device.ICMPLossRatio = ic.RTTMs, ic.Loss
		}(r)
	}
	wg.Wait()
}

// heartbeat publica horus.snmp.poller.heartbeat (tenant plataforma) y
// actualiza las gauges.
func (e *Engine) heartbeat(ctx context.Context) {
	now := e.now()
	e.m.heartbeat.Set(float64(now.Unix()))
	e.updateGauges(now)
	e.mu.RLock()
	last, errs, n := e.lastCycle, e.lastErrs, len(e.routers)
	e.mu.RUnlock()
	var lastAt any
	if last != nil {
		lastAt = last.UTC().Format(time.RFC3339)
	}
	e.publish(ctx, Event{Type: TypeHeartbeat, Entity: uuid.NewSHA1(instanceNamespace, []byte(e.instanceID)).String(), AggregateType: "poller",
		At: now, Data: map[string]any{"instance_id": e.instanceID, "shards_owned": 1, "targets": n, "last_cycle_at": lastAt,
			"poll_errors_last_cycle": errs, "interval_seconds": int(e.Interval().Seconds())}})
}

func (e *Engine) updateGauges(now time.Time) {
	healthy := e.pollerHealthy(now)
	counts := map[string]int{}
	stale := 0
	for _, r := range e.snapshot() {
		r.mu.Lock()
		st, _ := domain.Effective(r.st.Machine.State, r.st.Machine.Since, r.st.LastObservedAt, now, e.Interval(), healthy)
		r.mu.Unlock()
		counts[st]++
		if st == api.StatusStale {
			stale++
		}
	}
	for _, s := range []string{api.StatusOnline, api.StatusWarning, api.StatusCritical, api.StatusDegraded, api.StatusOffline, api.StatusStale, api.StatusUnknown} {
		e.m.routers.WithLabelValues(s).Set(float64(counts[s]))
	}
	e.m.stale.Set(float64(stale))
}

func (e *Engine) pollerHealthy(now time.Time) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.o.Config.Enabled && domain.PollerHealthy(e.lastCycle, now, e.Interval())
}
