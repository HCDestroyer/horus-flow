package tools

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/hcdestroyer/horus-flow/services/assist/internal/mask"
)

const uuidPattern = `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`

func uuidProp(desc string) map[string]any {
	return map[string]any{"type": "string", "pattern": uuidPattern, "description": desc}
}

func object(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// Horus devuelve las herramientas de solo lectura de Horus (v1).
func Horus() []Tool {
	return []Tool{
		{
			Name: "get_finding",
			Description: "Detalle de UN hallazgo de seguridad por su finding_id (UUID). Úsala primero en cualquier análisis de " +
				"hallazgo. Devuelve: tipo, severidad, confianza (0–1), estado, razones, señales, cliente afectado (IP " +
				"seudonimizada, tipo, estado de seguridad), línea de tiempo y la evidencia de flujos agregada por destino " +
				"remoto, puerto, protocolo y sentido (flujos, bytes, primer y último instante, ASN, categoría de reputación). " +
				"No devuelve: nombres ni contactos del cliente, ni flujos individuales. Si falta permiso de evidencia, " +
				"evidence_flows lo dice.",
			Schema: object(map[string]any{
				"finding_id":       uuidProp("ID del hallazgo."),
				"include_evidence": map[string]any{"type": "boolean", "description": "Incluir flujos de evidencia agregados (por defecto true)."},
			}, "finding_id"),
			Permissions: []string{"security.findings.read", "security.evidence.read"},
			Run:         getFinding,
			Facts:       findingFacts,
		},
		{
			Name: "list_open_findings",
			Description: "Lista los hallazgos abiertos o reconocidos del ISP, de mayor a menor severidad (resumen por hallazgo: " +
				"id, tipo, severidad, confianza, cliente seudonimizado, última señal). Úsala para contexto general o para " +
				"ver si otros clientes muestran la misma señal; para el detalle de uno, get_finding. Una lista vacía significa " +
				"que no hay hallazgos abiertos con ese filtro, no que falten datos.",
			Schema: object(map[string]any{
				"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "Máximo de hallazgos (por defecto 20)."},
				"severity": map[string]any{"type": "string", "enum": []string{"low", "medium", "high", "critical"}},
			}),
			Permissions: []string{"security.findings.read"},
			Run:         listOpenFindings,
			Facts:       findingsListFacts,
		},
		{
			Name: "get_exporter_status",
			Description: "Estado de los exportadores de flujos (NetFlow/IPFIX) del ISP, o de un router con router_id. Devuelve " +
				"por exportador: estado (exporting = normal; silent = sin flujos recientes; lossy = pérdida de registros; " +
				"clock_skew = reloj desfasado; pending_configuration = nunca envió), desde cuándo, último flujo, flujos/s, " +
				"loss_ratio (0–1), desfase de reloj en segundos y muestreo. Úsala para saber si Horus está recibiendo tráfico " +
				"y de qué routers. No devuelve configuración del router ni del túnel (para eso, get_router).",
			Schema: object(map[string]any{
				"router_id": uuidProp("Router concreto (opcional)."),
			}),
			Permissions: []string{"flows.read"},
			Run:         getExporterStatus,
			Facts:       exporterFacts,
		},
		{
			Name: "get_router",
			Description: "Ficha completa de UN router por su router_id (UUID). Úsala primero en cualquier diagnóstico de router. " +
				"Devuelve: nombre, nodo, estado, estado del alta (onboarding), versión de RouterOS, modelo, avisos, qué " +
				"credenciales están configuradas (nunca sus valores), el peer WireGuard (estado, último handshake y su " +
				"antigüedad en segundos, endpoint, keepalive) y su exportador de flujos. Si el router no tiene peer o " +
				"exportador, el campo lo dice (null o texto). No devuelve la configuración interna del router ni ejecuta " +
				"nada en él.",
			Schema: object(map[string]any{
				"router_id": uuidProp("ID del router."),
			}, "router_id"),
			Permissions: []string{"devices.read", "wireguard.read", "flows.read"},
			Run:         getRouter,
			Facts:       routerFacts,
		},
		{
			Name: "get_traffic_top",
			Description: "Top 10 del tráfico del ISP en un rango (por defecto 24h) por services, categories, organizations, asns " +
				"o customers (seudonimizados), con bytes de bajada y subida por fila. Úsala para contexto de volumen; no " +
				"sirve para ver el tráfico de un cliente concreto (usa la evidencia del hallazgo). Las cifras son del rango " +
				"pedido, no tasas instantáneas.",
			Schema: object(map[string]any{
				"dimension": map[string]any{"type": "string", "enum": []string{"services", "categories", "organizations", "asns", "customers"}},
				"range":     map[string]any{"type": "string", "enum": []string{"15m", "1h", "6h", "24h", "7d", "30d"}, "description": "Por defecto 24h."},
				"site_id":   uuidProp("Limitar a un nodo (opcional)."),
				"direction": map[string]any{"type": "string", "enum": []string{"both", "download", "upload"}},
			}, "dimension"),
			Permissions: []string{"traffic.read"},
			Run:         getTrafficTop,
			Facts:       trafficTopFacts,
		},
		{
			Name: "get_customer_profile",
			Description: "Perfil de un cliente por su customer_id (el campo customer_id de un hallazgo), sin datos personales: " +
				"tipo (residential/commercial), estado, estado de seguridad (clean/suspected/infected/mitigated), nodo y " +
				"sus hallazgos recientes. Úsala para saber si el cliente reincide o tiene otras señales. No devuelve " +
				"nombre, alias, contacto ni ubicación.",
			Schema: object(map[string]any{
				"customer_ref": uuidProp("customer_id del cliente."),
			}, "customer_ref"),
			Permissions: []string{"customers.read", "security.findings.read"},
			Run:         getCustomerProfile,
			Facts:       customerFacts,
		},
		{
			Name: "reputation_lookup",
			Description: "Contexto de reputación de un ASN (asn) O de una IP/prefijo remoto (prefix), uno de los dos: si está " +
				"en la allowlist del ISP (tráfico esperado), qué hallazgos abiertos lo tienen como objetivo y qué feeds de " +
				"reputación están activos. LIMITACIÓN: no dice si el indicador está en un feed; esa coincidencia viene como " +
				"reputation_category en la evidencia de get_finding. No afirmes que una IP es maliciosa solo por esta herramienta.",
			Schema: object(map[string]any{
				"asn":    map[string]any{"type": "integer", "minimum": 1, "maximum": 4294967295},
				"prefix": map[string]any{"type": "string", "maxLength": 50, "description": "IP o prefijo CIDR remoto."},
			}),
			Permissions: []string{"security.findings.read"},
			Run:         reputationLookup,
			Facts:       reputationFacts,
		},
		routerosTool(),
	}
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// pick copia las claves keys de m (si existen).
func pick(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}

func forbiddenOr(err error) (string, bool) {
	var he *HTTPError
	if errors.As(err, &he) && (he.Status == 403 || he.Status == 404 || he.Status == 503) {
		switch he.Status {
		case 403:
			return "sin permiso para este dato", true
		case 404:
			return "no existe", true
		default:
			return "no disponible ahora", true
		}
	}
	return "", false
}

func getFinding(ctx context.Context, env Env, args map[string]any) (any, error) {
	id := Str(args, "finding_id")
	doc, err := env.Reader.Get(ctx, "/api/v1/findings/"+id, nil)
	if err != nil {
		return nil, err
	}
	f := asMap(doc)
	delete(f, "tenant_id")
	out := map[string]any{"finding": env.Masker.Value(f, mask.Mixed)}
	if Bool(args, "include_evidence", true) {
		ev, err := env.Reader.Get(ctx, "/api/v1/findings/"+id+"/evidence", url.Values{"limit": {"200"}})
		if err != nil {
			msg, ok := forbiddenOr(err)
			if !ok {
				return nil, err
			}
			out["evidence_flows"] = msg
		} else {
			out["evidence_flows"] = aggregateEvidence(env.Masker, asList(asMap(ev)["data"]))
		}
	}
	return out, nil
}

// aggregateEvidence agrupa los flujos de evidencia por destino remoto, puerto,
// protocolo y sentido (top 20 por bytes). Un flujo "internal" apunta a otro
// cliente: su IP se seudonimiza.
func aggregateEvidence(m *mask.Masker, flows []any) map[string]any {
	type agg struct {
		Remote     string `json:"remote"`
		Port       any    `json:"remote_port"`
		Protocol   any    `json:"protocol"`
		Direction  string `json:"direction"`
		ASN        any    `json:"remote_asn,omitempty"`
		Reputation any    `json:"reputation_category,omitempty"`
		Flows      int    `json:"flows"`
		Bytes      uint64 `json:"bytes"`
		Packets    uint64 `json:"packets"`
		First      string `json:"first_ts"`
		Last       string `json:"last_ts"`
	}
	groups := map[string]*agg{}
	for _, raw := range flows {
		f := asMap(raw)
		dir, _ := f["direction"].(string)
		remote, _ := f["remote_ip"].(string)
		if dir == "internal" {
			remote = m.CustomerIP(remote)
		}
		key := fmt.Sprint(remote, "|", f["remote_port"], "|", f["protocol"], "|", dir)
		g := groups[key]
		if g == nil {
			g = &agg{Remote: remote, Port: f["remote_port"], Protocol: f["protocol"], Direction: dir, ASN: f["remote_asn"],
				Reputation: f["reputation_category"]}
			groups[key] = g
		}
		g.Flows++
		g.Bytes += parseUint(f["bytes"])
		g.Packets += parseUint(f["packets"])
		if ts, _ := f["ts"].(string); ts != "" {
			if g.First == "" || ts < g.First {
				g.First = ts
			}
			if ts > g.Last {
				g.Last = ts
			}
		}
	}
	list := make([]*agg, 0, len(groups))
	for _, g := range groups {
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Bytes != list[j].Bytes {
			return list[i].Bytes > list[j].Bytes
		}
		return list[i].Remote < list[j].Remote
	})
	total := len(list)
	if len(list) > 20 {
		list = list[:20]
	}
	return map[string]any{"flows_sampled": len(flows), "groups": total, "top": list}
}

func parseUint(v any) uint64 {
	switch x := v.(type) {
	case string:
		n, _ := strconv.ParseUint(x, 10, 64)
		return n
	case float64:
		if x > 0 {
			return uint64(x)
		}
	}
	return 0
}

func compactFinding(m *mask.Masker, raw any) any {
	f := asMap(raw)
	out := pick(f, "id", "kind", "severity", "confidence", "state", "customer_id", "router_id", "site_id",
		"first_seen_at", "last_seen_at", "occurrences", "primary_target", "customer")
	if s := asMap(f["summary"]); s != nil {
		out["summary"] = s["text"]
	}
	return m.Value(out, mask.Mixed)
}

func listOpenFindings(ctx context.Context, env Env, args map[string]any) (any, error) {
	q := url.Values{"state": {"open,acknowledged"}, "limit": {strconv.Itoa(Int(args, "limit", 20))}, "sort": {"-severity"}}
	if s := Str(args, "severity"); s != "" {
		q.Set("severity", s)
	}
	doc, err := env.Reader.Get(ctx, "/api/v1/findings", q)
	if err != nil {
		return nil, err
	}
	data := asList(asMap(doc)["data"])
	out := make([]any, 0, len(data))
	for _, f := range data {
		out = append(out, compactFinding(env.Masker, f))
	}
	return map[string]any{"findings": out, "count": len(out)}, nil
}

func getExporterStatus(ctx context.Context, env Env, args map[string]any) (any, error) {
	if id := Str(args, "router_id"); id != "" {
		doc, err := env.Reader.Get(ctx, "/api/v1/flow-exporters/"+id, nil)
		if err != nil {
			return nil, err
		}
		e := asMap(doc)
		delete(e, "tenant_id")
		return map[string]any{"exporter": env.Masker.Value(e, mask.Infra)}, nil
	}
	doc, err := env.Reader.Get(ctx, "/api/v1/flow-exporters", nil)
	if err != nil {
		return nil, err
	}
	data := asList(asMap(doc)["data"])
	counts := map[string]int{}
	list := make([]any, 0, len(data))
	for _, raw := range data {
		e := asMap(raw)
		delete(e, "tenant_id")
		if s, _ := e["state"].(string); s != "" {
			counts[s]++
		}
		list = append(list, env.Masker.Value(e, mask.Infra))
	}
	return map[string]any{"exporters": list, "by_state": counts}, nil
}

func getRouter(ctx context.Context, env Env, args map[string]any) (any, error) {
	id := Str(args, "router_id")
	doc, err := env.Reader.Get(ctx, "/api/v1/routers/"+id, nil)
	if err != nil {
		return nil, err
	}
	r := asMap(doc)
	delete(r, "tenant_id")
	out := map[string]any{"router": env.Masker.Value(r, mask.Infra)}
	peers, err := env.Reader.Get(ctx, "/api/v1/wireguard/peers", url.Values{"router_id": {id}})
	if err != nil {
		msg, ok := forbiddenOr(err)
		if !ok {
			return nil, err
		}
		out["wireguard_peer"] = msg
	} else if list := asList(asMap(peers)["data"]); len(list) > 0 {
		p := asMap(list[0])
		delete(p, "tenant_id")
		out["wireguard_peer"] = env.Masker.Value(p, mask.Infra)
	} else {
		out["wireguard_peer"] = nil
	}
	exp, err := env.Reader.Get(ctx, "/api/v1/flow-exporters/"+id, nil)
	if err != nil {
		msg, ok := forbiddenOr(err)
		if !ok {
			return nil, err
		}
		out["flow_exporter"] = msg
	} else {
		e := asMap(exp)
		delete(e, "tenant_id")
		out["flow_exporter"] = env.Masker.Value(e, mask.Infra)
	}
	return out, nil
}

func getTrafficTop(ctx context.Context, env Env, args map[string]any) (any, error) {
	q := url.Values{"dimension": {Str(args, "dimension")}, "n": {"10"}}
	rng := Str(args, "range")
	if rng == "" {
		rng = "24h"
	}
	q.Set("range", rng)
	if s := Str(args, "site_id"); s != "" {
		q.Set("site_id", s)
	}
	if d := Str(args, "direction"); d != "" {
		q.Set("direction", d)
	}
	doc, err := env.Reader.Get(ctx, "/api/v1/analytics/traffic/top", q)
	if err != nil {
		return nil, err
	}
	d := asMap(doc)
	data := asMap(d["data"])
	for _, raw := range asList(data["rows"]) {
		row := asMap(raw)
		if c := asMap(row["customer"]); c != nil {
			if a, _ := c["address"].(string); a != "" {
				row["label"] = env.Masker.CustomerIP(a)
			} else {
				row["label"] = "cliente"
			}
		}
	}
	return map[string]any{"top": env.Masker.Value(data, mask.Mixed), "meta": d["meta"]}, nil
}

func getCustomerProfile(ctx context.Context, env Env, args map[string]any) (any, error) {
	id := Str(args, "customer_ref")
	doc, err := env.Reader.Get(ctx, "/api/v1/customers/"+id, nil)
	if err != nil {
		return nil, err
	}
	c := asMap(doc)
	delete(c, "tenant_id")
	out := map[string]any{"customer": env.Masker.Value(c, mask.Customer)}
	fs, err := env.Reader.Get(ctx, "/api/v1/customers/"+id+"/findings", url.Values{"limit": {"10"}})
	if err != nil {
		msg, ok := forbiddenOr(err)
		if !ok {
			return nil, err
		}
		out["recent_findings"] = msg
	} else {
		list := asList(asMap(fs)["data"])
		compact := make([]any, 0, len(list))
		for _, f := range list {
			compact = append(compact, compactFinding(env.Masker, f))
		}
		out["recent_findings"] = compact
	}
	return out, nil
}

func reputationLookup(ctx context.Context, env Env, args map[string]any) (any, error) {
	asn := Int(args, "asn", 0)
	prefixArg := strings.TrimSpace(Str(args, "prefix"))
	if (asn == 0) == (prefixArg == "") {
		return nil, &PublicError{Msg: "Indica asn o prefix (uno de los dos)."}
	}
	var pfx netip.Prefix
	if prefixArg != "" {
		p, err := netip.ParsePrefix(prefixArg)
		if err != nil {
			a, err2 := netip.ParseAddr(prefixArg)
			if err2 != nil {
				return nil, &PublicError{Msg: "prefix: IP o CIDR no válido."}
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		pfx = p.Masked()
	}
	out := map[string]any{"query": map[string]any{"asn": asn, "prefix": prefixArg}}
	allow, err := env.Reader.Get(ctx, "/api/v1/reputation/allowlist", url.Values{"limit": {"200"}})
	if err != nil {
		return nil, err
	}
	var matches []any
	for _, raw := range asList(asMap(allow)["data"]) {
		e := asMap(raw)
		hit := false
		if asn != 0 {
			if f, ok := e["asn"].(float64); ok && int(f) == asn {
				hit = true
			}
		} else if s, _ := e["prefix"].(string); s != "" {
			if p, err := netip.ParsePrefix(s); err == nil && p.Overlaps(pfx) {
				hit = true
			}
		}
		if hit {
			matches = append(matches, pick(e, "prefix", "asn", "kinds", "reason", "created_at"))
		}
	}
	out["in_isp_allowlist"] = len(matches) > 0
	out["allowlist_entries"] = matches
	fs, err := env.Reader.Get(ctx, "/api/v1/findings", url.Values{"state": {"open,acknowledged"}, "limit": {"100"}})
	if err != nil {
		return nil, err
	}
	var related []any
	for _, raw := range asList(asMap(fs)["data"]) {
		f := asMap(raw)
		t := asMap(f["primary_target"])
		val, _ := t["value"].(string)
		typ, _ := t["type"].(string)
		switch {
		case asn != 0 && typ == "remote_asn" && strings.TrimPrefix(strings.ToUpper(val), "AS") == strconv.Itoa(asn):
		case pfx.IsValid() && (typ == "remote_ip" || typ == "remote_prefix") && targetIn(val, pfx):
		default:
			continue
		}
		related = append(related, compactFinding(env.Masker, f))
	}
	out["open_findings_with_this_target"] = related
	if src, err := env.Reader.Get(ctx, "/api/v1/reputation/sources", nil); err == nil {
		var feeds []any
		for _, raw := range asList(asMap(src)["data"]) {
			feeds = append(feeds, pick(asMap(raw), "name", "category", "entries", "age_seconds"))
		}
		out["reputation_feeds"] = feeds
	}
	out["note"] = "Horus no expone una consulta indicador por indicador: la coincidencia con feeds se refleja en los hallazgos (reputation_category)."
	return out, nil
}

func targetIn(v string, pfx netip.Prefix) bool {
	if p, err := netip.ParsePrefix(v); err == nil {
		return p.Overlaps(pfx)
	}
	if a, err := netip.ParseAddr(v); err == nil {
		return pfx.Contains(a)
	}
	return false
}
