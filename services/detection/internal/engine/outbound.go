package engine

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

// Versiones de las reglas de comportamiento saliente.
const (
	RuleScan     = "outbound-scan@1"
	RuleVertical = "vertical-scan@1"
	RuleWatch    = "watched-ports@1"
	RuleFanout   = "fanout@1"
	RuleSMTP     = "smtp-direct@1"
)

var watchLabel = map[uint16]string{23: "Telnet", 2323: "Telnet alternativo", 37215: "Huawei HG532", 52869: "Realtek UPnP",
	7547: "TR-069", 5555: "ADB", 445: "SMB", 139: "NetBIOS", 6667: "IRC", 6697: "IRC/TLS", 3389: "RDP", 1433: "MSSQL", 8291: "Winbox", 25: "SMTP"}

func portLabel(p uint16) string {
	if l, ok := watchLabel[p]; ok {
		return fmt.Sprintf("%s (%d)", l, p)
	}
	return strconv.Itoa(int(p))
}

func ratio(a, b uint64) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// detectOutbound evalúa escaneo horizontal y vertical, puertos vigilados y
// fan-out en una ventana (5 min). Un cliente con escaneo horizontal produce
// un solo hallazgo outbound_scanning con el resto de señales como razones;
// el fan-out solo es hallazgo propio (outbound_fanout) cuando no es un
// escaneo TCP.
func detectOutbound(ctx context.Context, e *env) ([]domain.Candidate, error) {
	p := e.p
	minRem := uint64(p.Fanout.MinDestinations)
	if p.Scan.Enabled {
		minRem = min(minRem, uint64(p.Scan.MinDestinations))
	}
	rows, err := e.sig.Security(ctx, e.tenant, e.from, e.to, Having{MinRemoteIPs: minRem})
	if err != nil {
		return nil, err
	}
	sec := map[ClientKey]SecurityRow{}
	for _, r := range rows {
		sec[r.Key] = r
	}
	watch := map[ClientKey]WatchRow{}
	if p.WatchPorts.Enabled {
		ws, err := e.sig.WatchPorts(ctx, e.tenant, e.from, e.to, p.WatchPorts.MinDestinations, p.WatchPorts.ExcludePorts)
		if err != nil {
			return nil, err
		}
		for _, w := range ws {
			watch[w.Key] = w
		}
	}
	var out []domain.Candidate
	if p.Scan.Enabled {
		vs, err := e.sig.Vertical(ctx, e.tenant, e.from, e.to, p.Scan.VerticalMinPorts)
		if err != nil {
			return nil, err
		}
		for _, v := range vs {
			if ratio(v.Small, v.Flows) < p.Scan.VerticalSmallShare {
				continue
			}
			out = append(out, verticalCandidate(e, v))
		}
	}
	keys := keysOf(rows, func(r SecurityRow) ClientKey { return r.Key })
	for k := range watch {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return out, nil
	}
	init, err := e.sig.Initiated(ctx, e.tenant, e.from, e.to, keys)
	if err != nil {
		return nil, err
	}
	synPorts, err := e.sig.PortsByClient(ctx, e.tenant, e.from, e.to, keys, true, 5)
	if err != nil {
		return nil, err
	}
	perMin, err := e.sig.DestinationsPerMinute(ctx, e.tenant, e.from, e.to, keys)
	if err != nil {
		return nil, err
	}
	custs, err := e.customers(ctx, keys)
	if err != nil {
		return nil, err
	}
	win := minutes(e.to.Sub(e.from))
	for _, k := range keys {
		st, ok := init[k]
		if !ok {
			continue
		}
		s := sec[k]
		w, hasWatch := watch[k]
		synRatio := ratio(st.SynOnly, st.TCP)
		horizontal := p.Scan.Enabled && synRatio > p.Scan.SynOnlyRatio && st.SynDests >= uint64(p.Scan.MinDestinations)
		factor := 1.0
		if custs[k].Kind == "commercial" && p.Fanout.CommercialFactor > 0 {
			factor = p.Fanout.CommercialFactor
		}
		fan := p.Fanout.Enabled && float64(st.Dests) > float64(p.Fanout.MinDestinations)*factor &&
			float64(st.Nets24) > float64(p.Fanout.MinNets24)*factor
		if !horizontal && !hasWatch && !fan {
			continue
		}
		c := domain.Candidate{RealmID: k.Realm, Client: k.IP, SiteID: s.Site, WindowFrom: e.from, WindowTo: e.to,
			FirstSeen: st.First, LastSeen: st.Last, MinSamplingRate: st.MinSampling, MaxSamplingRate: st.MaxSampling}
		ev := map[string]any{"flows": int(st.TCP), "distinct_destinations": int(st.Dests), "distinct_nets24": int(st.Nets24),
			"syn_ratio": domain.Round2(synRatio), "destination_sample": st.Sample}
		if s.UpBytes > 0 {
			ev["bytes_est"] = strconv.FormatUint(s.UpBytes*uint64(max(st.MaxSampling, 1)), 10)
			ev["packets_est"] = strconv.FormatUint(s.UpPackets*uint64(max(st.MaxSampling, 1)), 10)
		}
		if m := perMin[k]; len(m) > 0 {
			ev["destinations_per_minute"] = m
		}
		synReason := domain.Reason{Code: "syn_only_ratio_high", Detail: fmt.Sprintf("%d %% de los flujos TCP salientes fueron SYN sin respuesta (%s destinos en %d min)",
			int(synRatio*100), fmtInt(st.SynDests), win), Weight: domain.W(0.45),
			Data: map[string]any{"syn_ratio": domain.Round2(synRatio), "destinations": st.SynDests, "nets24": st.SynNets24, "window_minutes": win}}
		var watchReason domain.Reason
		if hasWatch {
			ports := portsList(w.Ports, 13)
			top := w.Ports[0]
			watchReason = domain.Reason{Code: "watched_port_fanout",
				Detail: fmt.Sprintf("%s hacia %s destinos en %d min (%d puertos vigilados)", portLabel(top.Port), fmtInt(w.Destinations), win, len(w.Ports)),
				Weight: domain.W(0.35), Data: map[string]any{"port": int(top.Port), "ports": ports, "destinations": w.Destinations, "flows": w.Flows, "window_minutes": win}}
		}
		fanReason := domain.Reason{Code: "fanout_dispersed", Detail: fmt.Sprintf("%s IPs remotas distintas en %s redes /24 en %d min (no es una CDN)",
			fmtInt(st.Dests), fmtInt(st.Nets24), win), Weight: domain.W(0.2),
			Data: map[string]any{"remote_ips": st.Dests, "nets24": st.Nets24, "asns": st.ASNs, "window_minutes": win}}
		switch {
		case horizontal:
			ps := synPorts[k]
			c.Kind, c.Severity, c.RuleVersion = domain.KindScanning, domain.SeverityHigh, RuleScan
			c.Confidence = 0.6
			c.Reasons = []domain.Reason{synReason}
			c.AddSignal(domain.SignalScanning)
			if len(ps) > 0 {
				c.Target = domain.Target{Type: domain.TargetRemotePort, Value: strconv.Itoa(int(ps[0].Port))}
				ev["destination_ports"] = portsList(ps, 10)
				c.Summary = domain.Summary{Code: "outbound_scanning_port", Text: fmt.Sprintf("Escaneo del puerto %d a %s destinos en %d min", ps[0].Port, fmtInt(ps[0].Destinations), win),
					Params: map[string]any{"port": int(ps[0].Port), "destinations": ps[0].Destinations, "window_minutes": win}}
			} else {
				c.Target = domain.Target{Type: domain.TargetNone}
				c.Summary = domain.Summary{Code: "outbound_scanning", Text: fmt.Sprintf("Escaneo a %s destinos en %d min", fmtInt(st.SynDests), win)}
			}
			if hasWatch {
				c.Reasons = append(c.Reasons, watchReason)
				c.AddSignal(domain.SignalWatchPorts)
				c.Confidence += 0.15
			}
			if fan {
				c.Reasons = append(c.Reasons, fanReason)
				c.AddSignal(domain.SignalFanout)
				c.Confidence += 0.1
			}
			if synRatio > 0.9 {
				c.Confidence += 0.05
			}
		case hasWatch:
			top := w.Ports[0]
			c.Kind, c.Severity, c.RuleVersion = domain.KindScanning, domain.SeverityMedium, RuleWatch
			c.Confidence = 0.55
			if w.Destinations >= 100 {
				c.Confidence += 0.1
			}
			c.Target = domain.Target{Type: domain.TargetRemotePort, Value: strconv.Itoa(int(top.Port))}
			ev["destination_ports"] = portsList(w.Ports, 13)
			c.Reasons = []domain.Reason{watchReason, {Code: "syn_only_ratio", Detail: fmt.Sprintf("%d %% de los flujos TCP salientes fueron SYN sin respuesta", int(synRatio*100)),
				Weight: domain.W(0.1), Data: map[string]any{"syn_ratio": domain.Round2(synRatio)}}}
			c.AddSignal(domain.SignalWatchPorts)
			if fan {
				c.Reasons = append(c.Reasons, fanReason)
				c.AddSignal(domain.SignalFanout)
				c.Confidence += 0.1
			}
			c.Summary = domain.Summary{Code: "watched_port_contacts", Text: fmt.Sprintf("Conexiones a %s hacia %s destinos en %d min", portLabel(top.Port), fmtInt(w.Destinations), win),
				Params: map[string]any{"port": int(top.Port), "destinations": w.Destinations, "window_minutes": win}}
		default:
			c.Kind, c.Severity, c.RuleVersion = domain.KindFanout, domain.SeverityMedium, RuleFanout
			c.Confidence = 0.55
			if ratio(st.Nets24, st.Dests) > 0.8 {
				c.Confidence += 0.1
			}
			c.Target = domain.Target{Type: domain.TargetNone}
			c.Reasons = []domain.Reason{fanReason, {Code: "not_tcp_scan", Detail: fmt.Sprintf("Solo el %d %% de los flujos TCP son SYN sin respuesta: tráfico P2P/UDP, no un escaneo", int(synRatio*100)),
				Weight: domain.W(0.1), Data: map[string]any{"syn_ratio": domain.Round2(synRatio)}}}
			if factor > 1 {
				c.Reasons = append(c.Reasons, domain.Reason{Code: "commercial_threshold", Detail: fmt.Sprintf("Cliente comercial: umbrales ×%.0f", factor), Weight: domain.W(0)})
			}
			c.AddSignal(domain.SignalFanout)
			c.Summary = domain.Summary{Code: "outbound_fanout", Text: fmt.Sprintf("Tráfico hacia %s IPs en %s redes /24 en %d min", fmtInt(st.Dests), fmtInt(st.Nets24), win),
				Params: map[string]any{"remote_ips": st.Dests, "nets24": st.Nets24, "window_minutes": win}}
		}
		c.Evidence = ev
		out = append(out, c)
	}
	return out, nil
}

func verticalCandidate(e *env, v VerticalRow) domain.Candidate {
	win := minutes(e.to.Sub(e.from))
	remote := ipStr(v.Remote)
	c := domain.Candidate{Kind: domain.KindScanning, Severity: domain.SeverityMedium, RuleVersion: RuleVertical,
		RealmID: v.Key.Realm, Client: v.Key.IP, Target: domain.Target{Type: domain.TargetRemoteIP, Value: remote},
		RemoteIP: v.Remote.Unmap(), RemoteASN: v.ASN, WindowFrom: e.from, WindowTo: e.to, FirstSeen: v.First, LastSeen: v.Last,
		Confidence: 0.6}
	if v.Ports >= 200 {
		c.Confidence += 0.1
	}
	c.AddSignal(domain.SignalScanning)
	c.Reasons = []domain.Reason{
		{Code: "vertical_scan", Detail: fmt.Sprintf("%s puertos distintos (%d–%d) de un mismo destino %s en %d min", fmtInt(v.Ports), v.MinPort, v.MaxPort, remote, win),
			Weight: domain.W(0.6), Data: map[string]any{"remote_ip": remote, "ports": v.Ports, "port_from": int(v.MinPort), "port_to": int(v.MaxPort), "window_minutes": win}},
		{Code: "small_flows", Detail: fmt.Sprintf("%d %% de los %s flujos tienen ≤ 3 paquetes (sondeos)", int(ratio(v.Small, v.Flows)*100), fmtInt(v.Flows)),
			Weight: domain.W(0.2), Data: map[string]any{"small_ratio": domain.Round2(ratio(v.Small, v.Flows)), "flows": v.Flows, "syn_only": v.SynOnly}},
	}
	c.Summary = domain.Summary{Code: "outbound_scanning_vertical", Text: fmt.Sprintf("Escaneo de %s puertos de %s en %d min", fmtInt(v.Ports), remote, win),
		Params: map[string]any{"remote_ip": remote, "ports": v.Ports, "window_minutes": win}}
	c.Evidence = map[string]any{"flows": int(v.Flows), "distinct_destinations": 1, "destination_sample": []string{remote},
		"syn_ratio": domain.Round2(ratio(v.SynOnly, v.Flows)), "distinct_ports": int(v.Ports)}
	if v.ASN != 0 {
		c.Evidence["target_asn"] = int(v.ASN)
	}
	return c
}

// detectSMTP: SMTP saliente directo a ≥ N servidores distintos por hora,
// con umbral distinto para comerciales (I1-11 criterio 3).
func detectSMTP(ctx context.Context, e *env) ([]domain.Candidate, error) {
	p := e.p.SMTP
	rows, err := e.sig.Security(ctx, e.tenant, e.from, e.to, Having{MinSMTPRemoteIPs: uint64(min(p.ResidentialServers, p.CommercialServers))})
	if err != nil {
		return nil, err
	}
	custs, err := e.customers(ctx, keysOf(rows, func(r SecurityRow) ClientKey { return r.Key }))
	if err != nil {
		return nil, err
	}
	win := minutes(e.to.Sub(e.from))
	var out []domain.Candidate
	for _, r := range rows {
		kind := custs[r.Key].Kind
		threshold := p.ResidentialServers
		if kind == "commercial" {
			threshold = p.CommercialServers
		}
		if r.SMTPRemoteIPs < uint64(threshold) {
			continue
		}
		perHour := int(float64(r.SMTPRemoteIPs) * 60 / float64(max(win, 1)))
		if win <= 60 {
			perHour = int(r.SMTPRemoteIPs)
		}
		c := domain.Candidate{Kind: domain.KindSpam, Severity: domain.SeverityHigh, RuleVersion: RuleSMTP, Confidence: 0.7,
			RealmID: r.Key.Realm, Client: r.Key.IP, SiteID: r.Site, Target: domain.Target{Type: domain.TargetRemotePort, Value: "25"},
			WindowFrom: e.from, WindowTo: e.to, FirstSeen: r.First, LastSeen: r.Last, MinSamplingRate: r.MinSampling, MaxSamplingRate: r.MaxSampling}
		c.AddSignal(domain.SignalSMTP)
		kindLabel := map[string]string{"commercial": "comercial", "residential": "residencial"}[kind]
		if kindLabel == "" {
			kindLabel = kind
		}
		c.Reasons = []domain.Reason{
			{Code: "smtp_direct_servers", Detail: fmt.Sprintf("%s servidores SMTP distintos (25/tcp) en %d min; umbral para un cliente %s: %d/h",
				fmtInt(r.SMTPRemoteIPs), win, kindLabel, threshold), Weight: domain.W(0.6),
				Data: map[string]any{"smtp_servers": r.SMTPRemoteIPs, "threshold": threshold, "customer_kind": kind, "window_minutes": win, "port": 25}},
			{Code: "smtp_flows", Detail: fmt.Sprintf("%s conexiones SMTP salientes", fmtInt(r.SMTPFlows)), Weight: domain.W(0.2),
				Data: map[string]any{"smtp_flows": r.SMTPFlows}},
		}
		if r.SMTPRemoteIPs >= uint64(3*threshold) {
			c.Confidence += 0.1
		}
		if kind != "commercial" && r.InboundPorts > 0 {
			c.Confidence -= 0.15
			c.Reasons = append(c.Reasons, domain.Reason{Code: "server_like_customer",
				Detail: "El cliente también acepta conexiones entrantes (servicio publicado): podría ser una empresa con servidor de correo; revise su tipo",
				Weight: domain.W(0), Data: map[string]any{"inbound_service_ports": r.InboundPorts, "suggested_kind": "commercial"}})
			c.Summary.Params = map[string]any{"suggested_kind": "commercial"}
		}
		if c.Summary.Params == nil {
			c.Summary.Params = map[string]any{}
		}
		c.Summary.Code = "spam_smtp_direct"
		c.Summary.Text = fmt.Sprintf("Envío directo de correo a %s servidores SMTP en %d min", fmtInt(r.SMTPRemoteIPs), win)
		c.Summary.Params["smtp_servers"], c.Summary.Params["window_minutes"] = r.SMTPRemoteIPs, win
		c.Evidence = map[string]any{"flows": int(r.SMTPFlows), "distinct_destinations": int(r.SMTPRemoteIPs), "destination_ports": []int{25},
			"smtp_servers_per_hour": perHour, "protocol": "tcp"}
		out = append(out, c)
	}
	return out, nil
}
