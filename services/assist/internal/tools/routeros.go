package tools

import (
	"context"
	"sort"
	"strings"
)

// Catálogo de comandos RouterOS v7 verificados (docs/assist.md §Anclaje). El
// modelo no debe inventar sintaxis de CLI: copia estas plantillas, sustituye
// los marcadores {…} con datos de las herramientas y conserva el comando para
// deshacer. La verificación de la respuesta (grounding) marca como "no
// verificado" cualquier comando de escritura cuyo prefijo no esté aquí.

// Command es una plantilla verificada.
type Command struct {
	ID      string   `json:"id"`
	Topic   string   `json:"topic"`
	Purpose string   `json:"purpose"`
	Impact  string   `json:"impact"` // none | low | medium
	Command string   `json:"command"`
	Undo    string   `json:"undo,omitempty"`
	Notes   string   `json:"notes,omitempty"`
	Params  []string `json:"params,omitempty"`
}

// Topics del catálogo (argumento `topic` de routeros_commands).
var Topics = []string{"diagnostics", "flows", "wireguard", "clock", "containment", "customer_limits"}

var catalog = []Command{
	// --- Diagnóstico de solo lectura ---------------------------------------------------------
	{ID: "sys_resource", Topic: "diagnostics", Purpose: "Versión de RouterOS, uptime, CPU y memoria", Impact: "none",
		Command: "/system resource print"},
	{ID: "sys_health", Topic: "diagnostics", Purpose: "Temperatura y voltaje (si el equipo lo expone)", Impact: "none",
		Command: "/system health print"},
	{ID: "log_recent", Topic: "diagnostics", Purpose: "Últimas 50 líneas del registro", Impact: "none",
		Command: "/log print follow-only=no where message~\".\" limit=50"},
	{ID: "ip_address", Topic: "diagnostics", Purpose: "Direcciones IP de las interfaces", Impact: "none",
		Command: "/ip address print"},
	{ID: "ip_route_default", Topic: "diagnostics", Purpose: "Ruta por defecto activa", Impact: "none",
		Command: "/ip route print where dst-address=0.0.0.0/0"},
	{ID: "conn_count", Topic: "diagnostics", Purpose: "Conexiones rastreadas de una IP de cliente (volumen de sesiones)", Impact: "none",
		Command: "/ip firewall connection print count-only where src-address~\"{customer_ip}\"", Params: []string{"customer_ip"}},
	{ID: "conn_top_dst", Topic: "diagnostics", Purpose: "Destinos y puertos de las conexiones activas de una IP de cliente", Impact: "none",
		Command: "/ip firewall connection print where src-address~\"{customer_ip}\"", Params: []string{"customer_ip"}},
	{ID: "torch_customer", Topic: "diagnostics", Purpose: "Tráfico en vivo de una IP de cliente (interactivo, Ctrl+C para salir)", Impact: "none",
		Command: "/tool torch interface={lan_interface} src-address={customer_ip} duration=10", Params: []string{"lan_interface", "customer_ip"},
		Notes: "No deja configuración; solo muestra."},
	{ID: "dns_cache", Topic: "diagnostics", Purpose: "Consultas DNS recientes en caché (si el router hace DNS)", Impact: "none",
		Command: "/ip dns cache print where name~\"{domain}\"", Params: []string{"domain"}},
	// --- Exportación de flujos ---------------------------------------------------------------
	{ID: "tf_status", Topic: "flows", Purpose: "Estado de Traffic Flow (habilitado, interfaces, timeouts)", Impact: "none",
		Command: "/ip traffic-flow print"},
	{ID: "tf_targets", Topic: "flows", Purpose: "Destinos de exportación (colector, puerto, versión)", Impact: "none",
		Command: "/ip traffic-flow target print"},
	{ID: "tf_ipfix_fields", Topic: "flows", Purpose: "Campos IPFIX exportados", Impact: "none",
		Command: "/ip traffic-flow ipfix print"},
	{ID: "tf_enable", Topic: "flows", Purpose: "Habilitar Traffic Flow en todas las interfaces", Impact: "low",
		Command: "/ip traffic-flow set enabled=yes interfaces=all", Undo: "/ip traffic-flow set enabled=no"},
	{ID: "tf_target_add", Topic: "flows", Purpose: "Añadir el colector de Horus como destino IPFIX", Impact: "low",
		Command: "/ip traffic-flow target add dst-address={collector_ip} port={collector_port} version=ipfix comment=\"horus\"",
		Undo:    "/ip traffic-flow target remove [find comment=\"horus\"]", Params: []string{"collector_ip", "collector_port"}},
	{ID: "tf_timeouts", Topic: "flows", Purpose: "Timeouts activo e inactivo recomendados por Horus", Impact: "low",
		Command: "/ip traffic-flow set active-flow-timeout=1m inactive-flow-timeout=15s",
		Undo:    "/ip traffic-flow set active-flow-timeout=30m inactive-flow-timeout=15s"},
	// --- WireGuard ---------------------------------------------------------------------------
	{ID: "wg_interfaces", Topic: "wireguard", Purpose: "Interfaces WireGuard (running, MTU, puerto)", Impact: "none",
		Command: "/interface wireguard print"},
	{ID: "wg_peers", Topic: "wireguard", Purpose: "Peers: último handshake, bytes y endpoint", Impact: "none",
		Command: "/interface wireguard peers print detail"},
	{ID: "wg_ping_hub", Topic: "wireguard", Purpose: "Conectividad hasta la IP del hub dentro del túnel", Impact: "none",
		Command: "/ping {hub_tunnel_ip} count=5", Params: []string{"hub_tunnel_ip"}},
	{ID: "wg_endpoint_dns", Topic: "wireguard", Purpose: "Resolución del nombre del endpoint del hub", Impact: "none",
		Command: "/resolve {hub_endpoint_host}", Params: []string{"hub_endpoint_host"}},
	{ID: "wg_keepalive", Topic: "wireguard", Purpose: "Keepalive de 25 s en el peer de Horus (túneles tras NAT)", Impact: "low",
		Command: "/interface wireguard peers set [find comment=\"horus\"] persistent-keepalive=25s",
		Undo:    "/interface wireguard peers set [find comment=\"horus\"] persistent-keepalive=0s"},
	{ID: "wg_fw_udp", Topic: "wireguard", Purpose: "Comprobar que el firewall deja salir UDP hacia el hub", Impact: "none",
		Command: "/ip firewall filter print where chain=output"},
	// --- Reloj ------------------------------------------------------------------------------
	{ID: "clock_print", Topic: "clock", Purpose: "Hora y zona horaria del router", Impact: "none",
		Command: "/system clock print"},
	{ID: "ntp_status", Topic: "clock", Purpose: "Estado del cliente NTP (sincronizado, servidor, desfase)", Impact: "none",
		Command: "/system ntp client print"},
	{ID: "ntp_enable", Topic: "clock", Purpose: "Activar NTP con servidores públicos", Impact: "low",
		Command: "/system ntp client set enabled=yes servers=time.cloudflare.com,pool.ntp.org",
		Undo:    "/system ntp client set enabled=no"},
	// --- Contención (siempre reversible, con comentario para localizarla) --------------------
	{ID: "addr_list_add", Topic: "containment", Purpose: "Marcar la IP del cliente en una lista de direcciones (sin bloquear nada por sí sola)", Impact: "low",
		Command: "/ip firewall address-list add list=horus-revisar address={customer_ip} comment=\"horus {finding_id}\" timeout=1d",
		Undo:    "/ip firewall address-list remove [find comment=\"horus {finding_id}\"]", Params: []string{"customer_ip", "finding_id"},
		Notes: "Caduca sola en 1 día (timeout)."},
	{ID: "fw_drop_dst_port", Topic: "containment", Purpose: "Bloquear desde la IP del cliente un puerto de destino concreto (p. ej. 25 o 23) con registro", Impact: "medium",
		Command: "/ip firewall filter add chain=forward src-address={customer_ip} protocol={protocol} dst-port={port} action=drop log=yes log-prefix=\"horus-{finding_id}\" comment=\"horus {finding_id}\" place-before=0",
		Undo:    "/ip firewall filter remove [find comment=\"horus {finding_id}\"]", Params: []string{"customer_ip", "protocol", "port", "finding_id"},
		Notes: "Afecta solo a ese cliente y puerto. Revisar que place-before=0 no rompa reglas de aceptación previas."},
	{ID: "fw_drop_dst_ip", Topic: "containment", Purpose: "Bloquear desde la IP del cliente el acceso a un destino remoto concreto (p. ej. un C2) con registro", Impact: "medium",
		Command: "/ip firewall filter add chain=forward src-address={customer_ip} dst-address={remote_ip} action=drop log=yes log-prefix=\"horus-{finding_id}\" comment=\"horus {finding_id}\" place-before=0",
		Undo:    "/ip firewall filter remove [find comment=\"horus {finding_id}\"]", Params: []string{"customer_ip", "remote_ip", "finding_id"}},
	{ID: "fw_disable_rule", Topic: "containment", Purpose: "Desactivar (sin borrar) una regla añadida por Horus", Impact: "low",
		Command: "/ip firewall filter disable [find comment=\"horus {finding_id}\"]",
		Undo:    "/ip firewall filter enable [find comment=\"horus {finding_id}\"]", Params: []string{"finding_id"}},
	// --- Límite temporal de un cliente -------------------------------------------------------
	{ID: "queue_limit", Topic: "customer_limits", Purpose: "Limitar temporalmente el ancho de banda de la IP del cliente", Impact: "medium",
		Command: "/queue simple add name=\"horus-{finding_id}\" target={customer_ip}/32 max-limit={up_limit}/{down_limit} comment=\"horus {finding_id}\" place-before=0",
		Undo:    "/queue simple remove [find comment=\"horus {finding_id}\"]", Params: []string{"finding_id", "customer_ip", "up_limit", "down_limit"},
		Notes: "max-limit es subida/bajada, p. ej. 2M/10M."},
}

// Catalog devuelve el catálogo completo (copia).
func Catalog() []Command {
	out := make([]Command, len(catalog))
	copy(out, catalog)
	return out
}

// CatalogByTopic devuelve las plantillas de uno o varios temas.
func CatalogByTopic(topics ...string) []Command {
	var out []Command
	for _, c := range catalog {
		for _, t := range topics {
			if c.Topic == t {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// CatalogMarkdown devuelve el catálogo (o unos temas) como texto para un prompt.
func CatalogMarkdown(topics ...string) string {
	list := catalog
	if len(topics) > 0 {
		list = CatalogByTopic(topics...)
	}
	var b strings.Builder
	b.WriteString("Plantillas RouterOS v7 verificadas (sustituye los marcadores {…} solo con datos de las herramientas; si falta un dato, deja el marcador y dilo):\n")
	for _, c := range list {
		b.WriteString("- [" + c.ID + "] " + c.Purpose + " (impacto " + c.Impact + ")\n  `" + c.Command + "`\n")
		if c.Undo != "" {
			b.WriteString("  deshacer: `" + c.Undo + "`\n")
		}
		if c.Notes != "" {
			b.WriteString("  nota: " + c.Notes + "\n")
		}
	}
	return b.String()
}

// Verbos de solo lectura: se admiten en cualquier ruta de RouterOS.
var readOnlyVerbs = map[string]bool{"print": true, "monitor": true, "export": true, "ping": true, "resolve": true, "torch": true,
	"traceroute": true, "bandwidth-test": false, "find": true, "get": true}

// writeRules son, por prefijo de escritura del catálogo, los parámetros
// admitidos y, para los que en la plantilla no llevan marcador, su valor fijo
// (action=drop, chain=forward, list=horus-revisar…). "" = valor libre.
var writeRules = func() map[string]map[string]map[string]bool {
	out := map[string]map[string]map[string]bool{}
	for _, c := range catalog {
		for _, cmd := range []string{c.Command, c.Undo} {
			p := CommandPrefix(cmd)
			if p == "" || IsReadOnlyPrefix(p) {
				continue
			}
			rule := out[p]
			if rule == nil {
				rule = map[string]map[string]bool{}
				out[p] = rule
			}
			for k, v := range params(cmd) {
				vals := rule[k]
				if vals == nil {
					vals = map[string]bool{}
					rule[k] = vals
				}
				if strings.Contains(v, "{") {
					vals[""] = true // libre
				} else {
					vals[v] = true
				}
			}
			if strings.Contains(cmd, "[find comment=") {
				rule["[find comment"] = map[string]bool{"": true}
			}
		}
	}
	return out
}()

// params extrae los parámetros key=value de un comando (valores sin comillas).
func params(cmd string) map[string]string {
	out := map[string]string{}
	rest := cmd
	if i := strings.Index(cmd, "["); i >= 0 { // el selector [find …] no son parámetros del comando
		if j := strings.Index(cmd[i:], "]"); j >= 0 {
			rest = cmd[:i] + cmd[i+j+1:]
		}
	}
	for _, w := range strings.Fields(rest) {
		k, v, ok := strings.Cut(w, "=")
		if !ok || strings.HasPrefix(k, "/") {
			continue
		}
		out[strings.ToLower(k)] = strings.Trim(v, `"'`)
	}
	return out
}

// CommandPrefix devuelve la ruta y el verbo de un comando RouterOS
// ("/ip firewall filter add"): las palabras iniciales hasta la primera que
// lleva "=", empieza por "[" o por comillas. "" si no es un comando (/…).
func CommandPrefix(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return ""
	}
	var words []string
	for _, w := range strings.Fields(line) {
		if strings.ContainsAny(w, "=[\"'{") || strings.HasPrefix(w, "-") {
			break
		}
		words = append(words, strings.ToLower(w))
	}
	return strings.Join(words, " ")
}

// IsReadOnlyPrefix indica si el prefijo termina en un verbo de solo lectura
// (o es /ping, /resolve, /tool torch…).
func IsReadOnlyPrefix(prefix string) bool {
	words := strings.Fields(prefix)
	if len(words) == 0 {
		return false
	}
	last := words[len(words)-1]
	if readOnlyVerbs[last] {
		return true
	}
	// Formas sin verbo: "/ping", "/resolve", "/tool torch" (el verbo es el propio comando).
	switch {
	case words[0] == "/ping", words[0] == "/resolve", words[0] == "/traceroute":
		return true
	case len(words) >= 2 && words[0] == "/tool" && (words[1] == "torch" || words[1] == "ping" || words[1] == "traceroute"):
		return true
	}
	return false
}

// CommandAllowed indica si un comando RouterOS está verificado: de solo
// lectura, o de escritura con un prefijo del catálogo cuyos parámetros son
// los de la plantilla (los fijos con el mismo valor; los marcadores, libres)
// y, si la plantilla etiqueta con comment="horus …", con esa etiqueta.
func CommandAllowed(line string) bool {
	p := CommandPrefix(line)
	if p == "" {
		return false
	}
	if IsReadOnlyPrefix(p) {
		return true
	}
	rule, ok := writeRules[p]
	if !ok {
		return false
	}
	// Comandos con selector: debe ser el de Horus.
	if strings.Contains(line, "[find") && !strings.Contains(line, `[find comment="horus`) {
		return false
	}
	if _, needsTag := rule["comment"]; needsTag {
		if ps := params(line); !strings.HasPrefix(ps["comment"], "horus") && !strings.Contains(line, `[find comment="horus`) {
			return false
		}
	}
	for k, v := range params(line) {
		vals, known := rule[k]
		if !known {
			return false
		}
		if vals[""] || vals[v] {
			continue
		}
		return false
	}
	return true
}

// WritePrefixes devuelve los prefijos de escritura autorizados (ordenados).
func WritePrefixes() []string {
	out := make([]string, 0, len(writeRules))
	for p := range writeRules {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func routerosCommands(_ context.Context, _ Env, args map[string]any) (any, error) {
	topic := Str(args, "topic")
	var list []Command
	if topic == "" || topic == "all" {
		list = Catalog()
	} else {
		list = CatalogByTopic(topic)
	}
	return map[string]any{
		"topic":    topic,
		"commands": list,
		"rules": []string{
			"Copia las plantillas tal cual y sustituye los marcadores {…} únicamente con valores devueltos por otras herramientas.",
			"Si no tienes el valor de un marcador, déjalo escrito entre llaves y dilo.",
			"No propongas comandos que no estén en este catálogo salvo consultas /print de solo lectura.",
			"Cada comando de escritura va acompañado de su comando de deshacer.",
		},
	}, nil
}

// routerosTool es la herramienta del catálogo.
func routerosTool() Tool {
	return Tool{
		Name: "routeros_commands",
		Description: "Catálogo de comandos RouterOS v7 VERIFICADOS, con su deshacer. Úsala SIEMPRE antes de sugerir un comando: " +
			"copia la plantilla y sustituye los marcadores {…} con datos de las demás herramientas. Temas: diagnostics " +
			"(solo lectura), flows (Traffic Flow/IPFIX), wireguard (túnel), clock (NTP/reloj), containment (lista de " +
			"direcciones, bloqueo por puerto o destino, todo reversible), customer_limits (límite temporal de ancho de banda). " +
			"No ejecuta nada; devuelve texto.",
		Schema: object(map[string]any{
			"topic": map[string]any{"type": "string", "enum": append([]string{"all"}, Topics...),
				"description": "Tema del catálogo (por defecto all)."},
		}),
		Run: routerosCommands,
		Facts: func(any) []string {
			return []string{"Los comandos del catálogo son plantillas verificadas para RouterOS v7; cualquier otro comando de escritura cuenta como no verificado."}
		},
	}
}
