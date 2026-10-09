package engine

import (
	"context"
	"fmt"
	"math"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

// Versiones de las reglas de volumen y periodicidad.
const (
	RuleDDoS      = "ddos-burst@1"
	RuleAmp       = "ddos-amplification@1"
	RuleSustained = "sustained-upload@1"
	RuleBeacon    = "beaconing@1"
)

// spread reparte un valor de un flujo entre los minutos que abarca
// [start, end] (como el simulador): devuelve minuto (unix/60) → fracción.
func spread(start, end time.Time, fn func(minute int64, frac float64)) {
	st, en := start.UnixMilli(), end.UnixMilli()
	if en < st {
		en = st
	}
	m0, m1 := st/60000, en/60000
	span := float64(en - st)
	for m := m0; m <= m1; m++ {
		if span == 0 {
			fn(m, 1)
			return
		}
		lo, hi := max(st, m*60000), min(en, (m+1)*60000)
		if hi > lo {
			fn(m, float64(hi-lo)/span)
		}
	}
}

type minuteAgg struct {
	upBytes, downBytes float64
	dstPkts            map[netip.Addr]float64
	dstBytes           map[netip.Addr]float64
	ampPkts            map[uint16]float64
	ampDsts            map[uint16]map[netip.Addr]bool
}

func byMinute(flows []FlowRec, amp []uint16) map[ClientKey]map[int64]*minuteAgg {
	out := map[ClientKey]map[int64]*minuteAgg{}
	for _, f := range flows {
		mm := out[f.Key]
		if mm == nil {
			mm = map[int64]*minuteAgg{}
			out[f.Key] = mm
		}
		isAmp := f.Up && f.Protocol == 17 && slices.Contains(amp, f.RemotePort)
		spread(f.Start, f.End, func(m int64, frac float64) {
			a := mm[m]
			if a == nil {
				a = &minuteAgg{dstPkts: map[netip.Addr]float64{}, dstBytes: map[netip.Addr]float64{}, ampPkts: map[uint16]float64{},
					ampDsts: map[uint16]map[netip.Addr]bool{}}
				mm[m] = a
			}
			s := float64(max(f.Sampling, 1))
			if !f.Up {
				a.downBytes += frac * float64(f.Bytes) * s
				return
			}
			a.upBytes += frac * float64(f.Bytes) * s
			a.dstPkts[f.Remote] += frac * float64(f.Packets) * s
			a.dstBytes[f.Remote] += frac * float64(f.Bytes) * s
			if isAmp {
				a.ampPkts[f.RemotePort] += frac * float64(f.Packets) * s
				if a.ampDsts[f.RemotePort] == nil {
					a.ampDsts[f.RemotePort] = map[netip.Addr]bool{}
				}
				a.ampDsts[f.RemotePort][f.Remote] = true
			}
		})
	}
	return out
}

func topDests(m map[netip.Addr]float64, n int) []netip.Addr {
	ks := make([]netip.Addr, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.SortFunc(ks, func(a, b netip.Addr) int {
		if m[a] != m[b] {
			if m[a] > m[b] {
				return -1
			}
			return 1
		}
		return a.Compare(b)
	})
	if len(ks) > n {
		ks = ks[:n]
	}
	return ks
}

func sortedMinutes(mm map[int64]*minuteAgg) []int64 {
	ms := make([]int64, 0, len(mm))
	for m := range mm {
		ms = append(ms, m)
	}
	slices.Sort(ms)
	return ms
}

func flowsMeta(flows []FlowRec) map[ClientKey]FlowRec {
	out := map[ClientKey]FlowRec{}
	for _, f := range flows {
		m, ok := out[f.Key]
		if !ok {
			m = FlowRec{Start: f.Start, End: f.End}
		}
		if f.Start.Before(m.Start) {
			m.Start = f.Start
		}
		if f.End.After(m.End) {
			m.End = f.End
		}
		m.Sampling = maxU32(m.Sampling, f.Sampling)
		out[f.Key] = m
	}
	return out
}

func asnOf(flows []FlowRec) map[netip.Addr]uint32 {
	out := map[netip.Addr]uint32{}
	for _, f := range flows {
		if f.ASN != 0 {
			out[f.Remote] = f.ASN
		}
	}
	return out
}

// detectDDoS: ráfaga de pps hacia ≤ 3 destinos durante ≥ 2 min seguidos, o
// UDP de amplificación (53, 123, 1900, 11211, 19) hacia muchos reflectores.
func detectDDoS(ctx context.Context, e *env) ([]domain.Candidate, error) {
	p := e.p.DDoS
	minPkts := math.Min(p.MinPPS, p.AmpMinPPS) * 60 * float64(p.MinMinutes)
	rows, err := e.sig.Security(ctx, e.tenant, e.from, e.to, Having{MinUpPackets: uint64(minPkts * 0.8)})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	keys := keysOf(rows, func(r SecurityRow) ClientKey { return r.Key })
	flows, err := e.sig.ClientFlows(ctx, e.tenant, e.from.Add(-time.Minute), e.to, keys, 0)
	if err != nil {
		return nil, err
	}
	var up []FlowRec
	for _, f := range flows {
		if f.Up {
			up = append(up, f)
		}
	}
	mins := byMinute(up, p.AmpPorts)
	meta := flowsMeta(up)
	asns := asnOf(up)
	site := map[ClientKey]SecurityRow{}
	for _, r := range rows {
		site[r.Key] = r
	}
	var out []domain.Candidate
	for _, k := range keys {
		mm := mins[k]
		if len(mm) == 0 {
			continue
		}
		run, best, prev := 0, 0, int64(math.MinInt64)
		var peak, peakBps float64
		targets := map[netip.Addr]float64{}
		var perMin []int
		for _, m := range sortedMinutes(mm) {
			a := mm[m]
			top := topDests(a.dstPkts, p.MaxTargets)
			var pk, by float64
			for _, t := range top {
				pk += a.dstPkts[t]
				by += a.dstBytes[t]
			}
			if pps := pk / 60; pps > p.MinPPS {
				if run > 0 && m == prev+1 {
					run++
				} else {
					run = 1
				}
				for _, t := range top {
					if a.dstPkts[t] >= 0.1*pk {
						targets[t] += a.dstPkts[t]
					}
				}
				peak, peakBps = math.Max(peak, pps), math.Max(peakBps, by*8/60)
				perMin = append(perMin, len(top))
			} else {
				run = 0
			}
			best = max(best, run)
			prev = m
		}
		r := site[k]
		if best >= p.MinMinutes {
			out = append(out, ddosCandidate(e, k, r, meta[k], targets, asns, best, peak, peakBps))
			continue
		}
		if c, ok := ampCandidate(e, k, r, meta[k], mm); ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func ddosCandidate(e *env, k ClientKey, r SecurityRow, meta FlowRec, targets map[netip.Addr]float64, asns map[netip.Addr]uint32,
	minutesRun int, peak, peakBps float64) domain.Candidate {
	ts := topDests(targets, len(targets))
	main := ts[0]
	list := make([]string, len(ts))
	for i, t := range ts {
		list[i] = ipStr(t)
	}
	c := domain.Candidate{Kind: domain.KindDDoS, Severity: domain.SeverityHigh, RuleVersion: RuleDDoS, Confidence: 0.75,
		RealmID: k.Realm, Client: k.IP, SiteID: r.Site, Target: domain.Target{Type: domain.TargetRemoteIP, Value: ipStr(main)},
		RemoteIP: main.Unmap(), RemoteASN: asns[main], WindowFrom: e.from, WindowTo: e.to, FirstSeen: meta.Start, LastSeen: meta.End,
		MinSamplingRate: r.MinSampling, MaxSamplingRate: maxU32(r.MaxSampling, meta.Sampling)}
	if minutesRun >= 3 {
		c.Confidence += 0.1
	}
	c.AddSignal(domain.SignalDDoS)
	c.Reasons = []domain.Reason{
		{Code: "ddos_pps_burst", Detail: fmt.Sprintf("Ráfaga de %s paquetes/s (pico) durante %d min seguidos", fmtInt(int64(peak)), minutesRun),
			Weight: domain.W(0.6), Data: map[string]any{"pps_peak": math.Round(peak), "bps_peak": math.Round(peakBps), "minutes": minutesRun, "threshold_pps": e.p.DDoS.MinPPS}},
		{Code: "ddos_few_targets", Detail: fmt.Sprintf("Concentrada en %d destino(s): %s", len(list), joinStr(list, 3)),
			Weight: domain.W(0.3), Data: map[string]any{"targets": list}},
	}
	c.Summary = domain.Summary{Code: "ddos_burst", Text: fmt.Sprintf("Ráfaga de %s pps hacia %s durante %d min", fmtInt(int64(peak)), ipStr(main), minutesRun),
		Params: map[string]any{"pps_peak": math.Round(peak), "target": ipStr(main), "minutes": minutesRun}}
	c.Evidence = map[string]any{"pps_peak": math.Round(peak), "bps_peak": math.Round(peakBps), "distinct_destinations": len(list),
		"destination_sample": list, "duration_seconds": minutesRun * 60, "spoofing_suspected": false,
		"bytes_est": strconv.FormatUint(r.UpBytes*uint64(max(r.MaxSampling, 1)), 10), "packets_est": strconv.FormatUint(r.UpPackets*uint64(max(r.MaxSampling, 1)), 10)}
	if a := asns[main]; a != 0 {
		c.Evidence["target_asn"] = int(a)
	}
	return c
}

func ampCandidate(e *env, k ClientKey, r SecurityRow, meta FlowRec, mm map[int64]*minuteAgg) (domain.Candidate, bool) {
	p := e.p.DDoS
	for _, port := range p.AmpPorts {
		run, best, prev := 0, 0, int64(math.MinInt64)
		var peak float64
		dsts := map[netip.Addr]bool{}
		for _, m := range sortedMinutes(mm) {
			a := mm[m]
			pps := a.ampPkts[port] / 60
			if pps > p.AmpMinPPS && len(a.ampDsts[port]) >= p.AmpMinTargets {
				if run > 0 && m == prev+1 {
					run++
				} else {
					run = 1
				}
				peak = math.Max(peak, pps)
				for d := range a.ampDsts[port] {
					dsts[d] = true
				}
			} else {
				run = 0
			}
			best = max(best, run)
			prev = m
		}
		if best < p.MinMinutes {
			continue
		}
		c := domain.Candidate{Kind: domain.KindDDoS, Severity: domain.SeverityHigh, RuleVersion: RuleAmp, Confidence: 0.75,
			RealmID: k.Realm, Client: k.IP, SiteID: r.Site, Target: domain.Target{Type: domain.TargetRemotePort, Value: strconv.Itoa(int(port))},
			WindowFrom: e.from, WindowTo: e.to, FirstSeen: meta.Start, LastSeen: meta.End, MinSamplingRate: r.MinSampling, MaxSamplingRate: r.MaxSampling}
		c.AddSignal(domain.SignalDDoS)
		c.Reasons = []domain.Reason{
			{Code: "udp_amplification", Detail: fmt.Sprintf("UDP saliente al puerto de amplificación %d: %s paquetes/s durante %d min", port, fmtInt(int64(peak)), best),
				Weight: domain.W(0.6), Data: map[string]any{"port": int(port), "pps_peak": math.Round(peak), "minutes": best}},
			{Code: "amplification_reflectors", Detail: fmt.Sprintf("Hacia %d reflectores distintos", len(dsts)), Weight: domain.W(0.3),
				Data: map[string]any{"reflectors": len(dsts)}},
		}
		c.Summary = domain.Summary{Code: "ddos_amplification", Text: fmt.Sprintf("Tráfico de amplificación UDP/%d a %d reflectores durante %d min", port, len(dsts), best),
			Params: map[string]any{"port": int(port), "reflectors": len(dsts), "minutes": best}}
		c.Evidence = map[string]any{"pps_peak": math.Round(peak), "protocol": "udp", "destination_ports": []int{int(port)},
			"distinct_destinations": len(dsts), "duration_seconds": best * 60, "spoofing_suspected": false}
		return c, true
	}
	return domain.Candidate{}, false
}

func joinStr(s []string, n int) string {
	out := ""
	for i, x := range s {
		if i == n {
			return out + fmt.Sprintf(" y %d más", len(s)-n)
		}
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}

// detectSustained: subida > 80 % del total y > X Mbit/s durante ≥ 30 min
// seguidos (I1-30). Las copias de seguridad a nubes conocidas en la franja
// nocturna local del ISP no son hallazgo (falso positivo conocido).
func detectSustained(ctx context.Context, e *env) ([]domain.Candidate, error) {
	p := e.p.Sustained
	minBytes := p.MinMbps * 1e6 / 8 * 60 * float64(p.MinMinutes)
	rows, err := e.sig.Security(ctx, e.tenant, e.from, e.to, Having{MinUpBytes: uint64(minBytes * 0.8)})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	keys := keysOf(rows, func(r SecurityRow) ClientKey { return r.Key })
	flows, err := e.sig.ClientFlows(ctx, e.tenant, e.from.Add(-time.Minute), e.to, keys, 0)
	if err != nil {
		return nil, err
	}
	mins := byMinute(flows, nil)
	loc := time.UTC
	if tz, err := e.sig.Timezone(ctx, e.tenant); err == nil && tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	var out []domain.Candidate
	for _, r := range rows {
		mm := mins[r.Key]
		run, best, prev := 0, 0, int64(math.MinInt64)
		var peak float64
		var runStart, bestStart, bestEnd int64
		upTo := map[netip.Addr]float64{}
		var upTotal float64
		for _, m := range sortedMinutes(mm) {
			a := mm[m]
			total := a.upBytes + a.downBytes
			bps := a.upBytes * 8 / 60
			if total > 0 && a.upBytes/total > p.UploadShare && bps > p.MinMbps*1e6 {
				if run == 0 || m != prev+1 {
					run, runStart = 0, m
				}
				run++
				peak = math.Max(peak, bps)
				if run > best {
					best, bestStart, bestEnd = run, runStart, m
				}
				for d, b := range a.dstBytes {
					upTo[d] += b
					upTotal += b
				}
			} else {
				run = 0
			}
			prev = m
		}
		if best < p.MinMinutes {
			continue
		}
		from, to := time.Unix(bestStart*60, 0).UTC(), time.Unix((bestEnd+1)*60, 0).UTC()
		top := topDests(upTo, 3)
		var asnShare float64
		asns := asnOf(flows)
		cloud := map[uint32]bool{}
		for _, a := range p.CloudASNs {
			cloud[a] = true
		}
		for d, b := range upTo {
			if cloud[asns[d]] {
				asnShare += b
			}
		}
		asnShare = ratio(uint64(asnShare), uint64(math.Max(upTotal, 1)))
		if asnShare >= p.CloudShare && inHours(from.In(loc).Hour(), p.BackupFrom, p.BackupTo) {
			continue // copia de seguridad nocturna a una nube conocida
		}
		main := top[0]
		share := 0.0
		if upTotal > 0 {
			share = upTo[main] / upTotal
		}
		c := domain.Candidate{Kind: domain.KindOpenProxy, Severity: domain.SeverityMedium, RuleVersion: RuleSustained, Confidence: 0.55,
			RealmID: r.Key.Realm, Client: r.Key.IP, SiteID: r.Site, Target: domain.Target{Type: domain.TargetRemoteIP, Value: ipStr(main)},
			RemoteIP: main.Unmap(), RemoteASN: asns[main], WindowFrom: from, WindowTo: to, FirstSeen: from, LastSeen: to,
			MinSamplingRate: r.MinSampling, MaxSamplingRate: r.MaxSampling}
		c.AddSignal(domain.SignalSustained)
		upRatio := ratio(r.UpBytes, r.UpBytes+r.DownBytes)
		c.Reasons = []domain.Reason{
			{Code: "sustained_upload", Detail: fmt.Sprintf("Subida sostenida de %.1f Mbit/s (pico) durante %d min seguidos", peak/1e6, best),
				Weight: domain.W(0.5), Data: map[string]any{"minutes": best, "peak_mbps": domain.Round2(peak / 1e6), "threshold_mbps": p.MinMbps}},
			{Code: "upload_dominant", Detail: fmt.Sprintf("La subida es el %d %% del tráfico del cliente", int(upRatio*100)),
				Weight: domain.W(0.3), Data: map[string]any{"upload_ratio": domain.Round2(upRatio)}},
			{Code: "main_destination", Detail: fmt.Sprintf("El %d %% de la subida va a %s", int(share*100), ipStr(main)),
				Weight: domain.W(0.2), Data: map[string]any{"remote_ip": ipStr(main), "share": domain.Round2(share), "asn": asns[main]}},
		}
		if asnShare >= p.CloudShare {
			c.Confidence -= 0.1
			c.Reasons = append(c.Reasons, domain.Reason{Code: "cloud_destination_daytime", Detail: "El destino es una nube conocida, pero fuera de la franja de copias de seguridad",
				Weight: domain.W(0), Data: map[string]any{"cloud_share": domain.Round2(asnShare)}})
		}
		c.Summary = domain.Summary{Code: "sustained_upload", Text: fmt.Sprintf("Subida sostenida de %.0f Mbit/s durante %d min", peak/1e6, best),
			Params: map[string]any{"peak_mbps": domain.Round2(peak / 1e6), "minutes": best}}
		list := make([]string, len(top))
		for i, t := range top {
			list[i] = ipStr(t)
		}
		c.Evidence = map[string]any{"upload_ratio": domain.Round2(upRatio), "duration_seconds": best * 60, "bps_peak": math.Round(peak),
			"destination_sample": list, "bytes_est": strconv.FormatUint(r.UpBytes*uint64(max(r.MaxSampling, 1)), 10)}
		if a := asns[main]; a != 0 {
			c.Evidence["target_asn"] = int(a)
		}
		out = append(out, c)
	}
	return out, nil
}

func inHours(h, from, to int) bool {
	if from == to {
		return false
	}
	if from < to {
		return h >= from && h < to
	}
	return h >= from || h < to
}

// detectBeacon: conexiones al mismo destino con intervalo regular (CV < 0,2)
// y bytes pequeños durante ≥ 6 h (I1-30).
func detectBeacon(ctx context.Context, e *env) ([]domain.Candidate, error) {
	p := e.p.Beacon
	rows, err := e.sig.BeaconSeries(ctx, e.tenant, e.from, e.to, p.MinConns, p.MinSpan.D(), p.MaxMeanBytes, p.ExcludePorts)
	if err != nil {
		return nil, err
	}
	best := map[ClientKey]domain.Candidate{}
	for _, r := range rows {
		if slices.Contains(p.AllowASNs, r.ASN) || len(r.Starts) < max(p.MinConns, 3) {
			continue
		}
		st := slices.Clone(r.Starts)
		slices.SortFunc(st, func(a, b time.Time) int { return a.Compare(b) })
		span := st[len(st)-1].Sub(st[0])
		if span < p.MinSpan.D() {
			continue
		}
		var sum, sq float64
		n := float64(len(st) - 1)
		for i := 1; i < len(st); i++ {
			d := st[i].Sub(st[i-1]).Seconds()
			sum += d
			sq += d * d
		}
		mean := sum / n
		if mean <= 0 || mean < p.MinPeriod.D().Seconds() || mean > p.MaxPeriod.D().Seconds() {
			continue
		}
		cv := math.Sqrt(math.Max(sq/n-mean*mean, 0)) / mean
		if cv >= p.MaxCV || r.MeanBytes >= p.MaxMeanBytes {
			continue
		}
		remote := ipStr(r.Remote)
		c := domain.Candidate{Kind: domain.KindBeaconing, Severity: domain.SeverityMedium, RuleVersion: RuleBeacon,
			Confidence: 0.5 + (p.MaxCV-cv)/p.MaxCV*0.2, RealmID: r.Key.Realm, Client: r.Key.IP, SiteID: r.Site, RouterID: r.Router,
			Target: domain.Target{Type: domain.TargetRemoteIP, Value: remote}, RemoteIP: r.Remote.Unmap(), RemoteASN: r.ASN,
			WindowFrom: st[0], WindowTo: st[len(st)-1], FirstSeen: st[0], LastSeen: st[len(st)-1], MaxSamplingRate: r.Sampling, MinSamplingRate: r.Sampling}
		c.AddSignal(domain.SignalBeaconing)
		c.Reasons = []domain.Reason{
			{Code: "periodic_connections", Detail: fmt.Sprintf("%d conexiones a %s:%d/%s cada %.0f s (coef. de variación %.2f)", len(st), remote, r.RemotePort, protoName(r.Protocol), mean, cv),
				Weight: domain.W(0.6), Data: map[string]any{"connections": len(st), "interval_seconds": math.Round(mean), "interval_cv": domain.Round2(cv),
					"remote_ip": remote, "remote_port": int(r.RemotePort)}},
			{Code: "small_constant_payload", Detail: fmt.Sprintf("%.0f bytes de media por conexión durante %.1f h", r.MeanBytes, span.Hours()),
				Weight: domain.W(0.3), Data: map[string]any{"mean_bytes_out": math.Round(r.MeanBytes), "span_hours": domain.Round2(span.Hours())}},
		}
		c.Summary = domain.Summary{Code: "beaconing", Text: fmt.Sprintf("Conexiones periódicas a %s cada %.0f s durante %.1f h", remote, mean, span.Hours()),
			Params: map[string]any{"remote_ip": remote, "interval_seconds": math.Round(mean), "hours": domain.Round2(span.Hours())}}
		c.Evidence = map[string]any{"flows": len(st), "interval_seconds": math.Round(mean), "interval_cv": domain.Round2(cv),
			"destination_ports": []int{int(r.RemotePort)}, "destination_sample": []string{remote}, "protocol": protoName(r.Protocol),
			"duration_seconds": int(span.Seconds())}
		if r.ASN != 0 {
			c.Evidence["target_asn"] = int(r.ASN)
		}
		if b, ok := best[r.Key]; !ok || c.Confidence > b.Confidence {
			best[r.Key] = c
		}
	}
	out := make([]domain.Candidate, 0, len(best))
	for _, c := range best {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b domain.Candidate) int { return a.Client.Compare(b.Client) })
	return out, nil
}
