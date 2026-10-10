package engine

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

// RuleC2 es la versión de la regla de contacto con C2 conocido.
const RuleC2 = "c2-contact@1"

// Categorías de flows_raw.reputation_category que evalúa el detector.
var repCategories = map[reputation.Category]string{
	reputation.CategoryBotnetCC: "botnet_cc", reputation.CategoryMining: "mining_pool", reputation.CategoryMalware: "malware_dist",
}

func protoName(p uint8) string {
	switch p {
	case 6:
		return "tcp"
	case 17:
		return "udp"
	case 1:
		return "icmp"
	case 58:
		return "icmpv6"
	}
	return strconv.Itoa(int(p))
}

type pairKey struct {
	k      ClientKey
	remote netip.Addr
}

// detectC2 evalúa la marca en ingesta (flows.reputation_hit, lookback) y
// verifica cada par cliente → indicador sobre flows_raw (respuesta, puerto,
// conexiones).
func detectC2(ctx context.Context, e *env) ([]domain.Candidate, error) {
	cats := []string{"botnet_cc", "malware_dist"}
	if e.p.C2.Mining {
		cats = append(cats, "mining_pool")
	}
	pairs, err := e.sig.ReputationPairs(ctx, e.tenant, e.from, e.to, cats)
	if err != nil {
		return nil, err
	}
	marked := map[pairKey]RepPair{}
	var remotes []netip.Addr
	for _, p := range pairs {
		if int(p.Confidence) < e.p.C2.MinIndicatorConfidence {
			continue
		}
		marked[pairKey{p.Key, p.Remote}] = p
		if !slices.Contains(remotes, p.Remote) {
			remotes = append(remotes, p.Remote)
		}
	}
	if len(remotes) == 0 {
		return nil, nil
	}
	flows, err := e.sig.IndicatorFlows(ctx, e.tenant, e.from, e.to, remotes)
	if err != nil {
		return nil, err
	}
	var out []domain.Candidate
	for _, f := range flows {
		m, ok := marked[pairKey{f.Key, f.Remote}]
		if !ok {
			continue
		}
		f.Category, f.SourceID, f.Confidence, f.Version = m.Category, m.SourceID, m.Confidence, m.Version
		if c, ok := c2Candidate(e.rep, f, nil); ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// retroSweep busca en flows_raw (retro_window, 7 días) tráfico hacia
// indicadores del snapshot vigente que la ingesta no pudo marcar porque
// entraron en el feed después (I1-10 criterio 2): la ventana del hallazgo es
// la del tráfico real.
func (en *Engine) retroSweep(ctx context.Context, tenant uuid.UUID, p *domain.Params, snap *reputation.Snapshot, end time.Time, force bool, cache map[ClientKey]Customer) ([]domain.Candidate, error) {
	const key = "c2_retro"
	state, err := en.sink.State(ctx, tenant, key)
	if err != nil {
		return nil, err
	}
	ver, at, _ := strings.Cut(state, "|")
	lastAt, _ := strconv.ParseInt(at, 10, 64)
	if !force && ver == snap.Meta.Version && end.Sub(time.Unix(lastAt, 0)) < p.C2.RetroInterval.D() {
		return nil, nil
	}
	want := map[string]bool{"botnet_cc": true}
	if p.C2.Mining {
		want["mining_pool"] = true
	}
	inds := map[netip.Addr]reputation.Indicator{}
	var ips []netip.Addr
	for pf, list := range snap.All() {
		if !pf.IsSingleIP() {
			continue
		}
		for _, ind := range list {
			if !want[repCategories[ind.Category]] || int(ind.Confidence) < p.C2.MinIndicatorConfidence ||
				(!ind.ExpiresAt.IsZero() && ind.ExpiresAt.Before(end)) {
				continue
			}
			if _, dup := inds[pf.Addr()]; !dup {
				ips = append(ips, pf.Addr())
			}
			inds[pf.Addr()] = ind
		}
	}
	var out []domain.Candidate
	from := end.Add(-p.C2.RetroWindow.D())
	for i := 0; i < len(ips); i += 5000 {
		batch := ips[i:min(i+5000, len(ips))]
		flows, err := en.sig.IndicatorFlows(ctx, tenant, from, end, batch)
		if err != nil {
			return out, err
		}
		for _, f := range flows {
			ind := inds[f.Remote.Unmap()]
			f.Category, f.SourceID, f.Confidence = repCategories[ind.Category], reputation.SourceID(ind.Source), ind.Confidence
			f.Version = uint32(snapVersion(snap)) //nolint:gosec // versión pequeña
			if c, ok := c2Candidate(snap, f, &ind); ok {
				c.Reasons = append(c.Reasons, domain.Reason{Code: "retroactive_sweep",
					Detail: "Detectado por el barrido retroactivo: el indicador entró en el feed después del tráfico", Weight: domain.W(0),
					Data: map[string]any{"snapshot_version": snap.Meta.Version}})
				out = append(out, c)
			}
		}
	}
	_ = cache
	return out, en.sink.SetState(ctx, tenant, key, snap.Meta.Version+"|"+strconv.FormatInt(end.Unix(), 10))
}

func snapVersion(s *reputation.Snapshot) int {
	if s == nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimPrefix(s.Meta.Version, "v"))
	return n
}

// indicatorFor busca los datos del indicador (fuente, fecha de inclusión,
// amenaza) en el snapshot; si no está (snapshot distinto al de la ingesta),
// usa la fuente por su id numérico.
func indicatorFor(snap *reputation.Snapshot, f RepPair) (reputation.Indicator, bool) {
	if snap != nil {
		var best reputation.Indicator
		found := false
		for _, h := range snap.Lookup(f.Remote.Unmap()) {
			if reputation.SourceID(h.Source) == f.SourceID || repCategories[h.Category] == f.Category {
				if !found || h.Confidence > best.Confidence {
					best, found = h.Indicator, true
				}
			}
		}
		if found {
			return best, true
		}
		for _, s := range snap.Meta.Sources {
			if reputation.SourceID(s.ID) == f.SourceID {
				return reputation.Indicator{Source: s.ID, Confidence: f.Confidence, FirstSeen: f.First}, false
			}
		}
	}
	return reputation.Indicator{Source: fmt.Sprintf("fuente #%d", f.SourceID), Confidence: f.Confidence, FirstSeen: f.First}, false
}

var catLabel = map[string]string{"botnet_cc": "servidor de mando y control (C2) de botnet", "mining_pool": "pool de minería",
	"malware_dist": "distribución de malware"}

func c2Candidate(snap *reputation.Snapshot, f RepPair, known *reputation.Indicator) (domain.Candidate, bool) {
	var ind reputation.Indicator
	if known != nil {
		ind = *known
	} else {
		ind, _ = indicatorFor(snap, f)
	}
	if f.Conns == 0 && f.Responded == 0 {
		return domain.Candidate{}, false
	}
	responded := f.Responded > 0
	remote := ipStr(f.Remote)
	base := float64(f.Confidence) / 100
	c := domain.Candidate{RuleVersion: RuleC2, RealmID: f.Key.Realm, Client: f.Key.IP, SiteID: f.Site, RouterID: f.Router,
		Target: domain.Target{Type: domain.TargetRemoteIP, Value: remote}, RemoteIP: f.Remote.Unmap(), RemoteASN: f.ASN,
		WindowFrom: f.First, WindowTo: f.Last, FirstSeen: f.First, LastSeen: f.Last, MaxSamplingRate: f.Sampling, MinSamplingRate: f.Sampling}
	if v := snapVersion(snap); v > 0 {
		c.ReputationVersion = &v
	} else if f.Version > 0 {
		v := int(f.Version)
		c.ReputationVersion = &v
	}
	switch f.Category {
	case "botnet_cc":
		c.Kind, c.Severity = domain.KindC2, domain.SeverityMedium
		c.Confidence = 0.3 + 0.4*base
		if responded {
			c.Severity, c.Confidence = domain.SeverityHigh, 0.6+0.4*base
		}
		c.AddSignal(domain.SignalC2)
	case "mining_pool":
		if !responded {
			return c, false
		}
		c.Kind, c.Severity, c.Confidence = domain.KindCryptomining, domain.SeverityMedium, 0.4+0.4*base
	case "malware_dist":
		if !responded {
			return c, false
		}
		c.Kind, c.Severity, c.Confidence = domain.KindReputationHit, domain.SeverityLow, 0.3+0.3*base
	default:
		return c, false
	}
	if f.Conns >= 3 {
		c.Confidence += 0.05
	}
	listed := ind.FirstSeen
	if listed.IsZero() {
		listed = f.First
	}
	label := catLabel[f.Category]
	threat := ""
	if ind.Threat != "" {
		threat = " (" + ind.Threat + ")"
	}
	port := strconv.Itoa(int(f.RemotePort)) + "/" + protoName(f.Protocol)
	c.Reasons = []domain.Reason{
		{Code: "reputation_listed", Detail: fmt.Sprintf("%s figura en %s como %s%s desde el %s", remote, ind.Source, label, threat, listed.UTC().Format("2006-01-02")),
			Weight: domain.W(0.5), Data: map[string]any{"remote_ip": remote, "feed": ind.Source, "category": f.Category,
				"listed_at": listed.UTC().Format(time.RFC3339), "threat": ind.Threat, "indicator_confidence": int(f.Confidence)}},
		{Code: "c2_connections", Detail: fmt.Sprintf("%d conexiones a %s:%s entre %s y %s UTC", f.Conns, remote, port,
			f.First.UTC().Format("2006-01-02 15:04"), f.Last.UTC().Format("2006-01-02 15:04")),
			Weight: domain.W(0.3), Data: map[string]any{"connections": f.Conns, "remote_port": int(f.RemotePort), "protocol": protoName(f.Protocol),
				"first_seen": f.First.UTC().Format(time.RFC3339), "last_seen": f.Last.UTC().Format(time.RFC3339)}},
	}
	if responded {
		c.Reasons = append(c.Reasons, domain.Reason{Code: "c2_responded", Detail: fmt.Sprintf("El servidor respondió (%d flujos de vuelta): hay sesión activa", f.Responded),
			Weight: domain.W(0.2), Data: map[string]any{"responded_flows": f.Responded}})
	} else {
		c.Reasons = append(c.Reasons, domain.Reason{Code: "c2_syn_only", Detail: "Solo intentos SYN sin respuesta: el C2 está caído o en sinkhole, pero el equipo sigue intentando contactarlo (señal compatible con un equipo comprometido)",
			Weight: domain.W(0.2), Data: map[string]any{"syn_only": f.SynOnly}})
	}
	c.Summary = domain.Summary{Code: "c2_contact", Text: fmt.Sprintf("Contacto con %s conocido %s:%s (%s)", strings.SplitN(label, " de ", 2)[0], remote, port, ind.Source),
		Params: map[string]any{"remote_ip": remote, "remote_port": int(f.RemotePort), "feed": ind.Source, "connections": f.Conns, "responded": responded}}
	if f.Category == "botnet_cc" {
		c.Summary.Text = fmt.Sprintf("Contacto con C2 de botnet conocido %s:%s (%s)", remote, port, ind.Source)
	}
	c.Evidence = map[string]any{
		"flows": f.Conns + f.Responded, "bytes_est": strconv.FormatUint(f.Bytes*uint64(max(f.Sampling, 1)), 10),
		"packets_est": strconv.FormatUint(f.Packets*uint64(max(f.Sampling, 1)), 10), "distinct_destinations": 1,
		"destination_ports": []int{int(f.RemotePort)}, "destination_sample": []string{remote}, "protocol": protoName(f.Protocol),
		"reputation_sources": []map[string]any{{"source": ind.Source, "indicator": remote, "category": f.Category,
			"listed_at": listed.UTC().Format(time.RFC3339), "responded": responded}},
		"duration_seconds": int(f.Last.Sub(f.First).Seconds()),
	}
	if f.ASN != 0 {
		c.Evidence["target_asn"] = int(f.ASN)
		c.Evidence["destination_asns"] = []int{int(f.ASN)}
	}
	return c, true
}
