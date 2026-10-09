// Package actions construye las acciones recomendadas de cada hallazgo (D11,
// C8 RecommendedAction): qué hacer, por qué, riesgo para el servicio del
// cliente, texto sugerido para el cliente y comandos RouterOS 7 (≥ 7.12, D15)
// como PLANTILLAS con su comando para deshacer. Horus nunca los ejecuta
// (execution = "manual", ADR-0022, ADR-0024 §4): el operador los copia.
//
// Todo lo que crean los comandos lleva comment="horus finding {{finding_id}}"
// para poder deshacerlo con un `remove [find comment=…]`. Para clientes IPv6
// (D22: el prefijo delegado es otro cliente) los comandos van por /ipv6.
package actions

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

// Valores fijos de los placeholders que no dependen del cliente.
const (
	MinVersion       = "7.12"
	QuarantineList   = "horus-quarantine"
	DefaultRateLimit = "2M/2M"
	Execution        = "manual"
)

// RouterOS son los comandos sugeridos de una acción.
type RouterOS struct {
	MinVersion           string    `json:"min_version"`
	Commands             []string  `json:"commands"`
	UndoCommands         []string  `json:"undo_commands"`
	Placeholders         []string  `json:"placeholders"`
	RenderedCommands     *[]string `json:"rendered_commands"`
	RenderedUndoCommands *[]string `json:"rendered_undo_commands"`
	Notes                *string   `json:"notes"`
}

// Action es una acción recomendada (C8 RecommendedAction).
type Action struct {
	Code            string    `json:"code"`
	Title           string    `json:"title"`
	Explanation     string    `json:"explanation"`
	Priority        int       `json:"priority"`
	Risk            string    `json:"risk"`
	Audience        string    `json:"audience"`
	Execution       string    `json:"execution"`
	CustomerMessage *string   `json:"customer_message"`
	RouterOS        *RouterOS `json:"routeros"`
}

func str(s string) *string { return &s }

const commentTpl = `comment="horus finding {{finding_id}}"`

// fw devuelve el prefijo de menú según la familia (D22).
func fw(v6 bool) string {
	if v6 {
		return "/ipv6 firewall"
	}
	return "/ip firewall"
}

func placeholders(cmds ...[]string) []string {
	var out []string
	for _, list := range cmds {
		for _, c := range list {
			for _, part := range strings.Split(c, "{{")[1:] {
				name, _, ok := strings.Cut(part, "}}")
				if ok && !slices.Contains(out, name) {
					out = append(out, name)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

func ros(cmds, undo []string, notes string) *RouterOS {
	r := &RouterOS{MinVersion: MinVersion, Commands: cmds, UndoCommands: undo, Placeholders: placeholders(cmds, undo)}
	if notes != "" {
		r.Notes = str(notes)
	}
	return r
}

func contact(msg string) Action {
	return Action{Code: "contact_customer", Title: "Avisar al cliente",
		Explanation: "Avisar al cliente para que revise sus equipos (router, cámaras, DVR, ordenadores) suele resolver el problema " +
			"sin cortar el servicio. No tiene efecto técnico y no hay nada que deshacer.",
		Risk: "low", Audience: "customer_support", CustomerMessage: str(msg)}
}

func quarantine(v6 bool) Action {
	return Action{Code: "quarantine_address_list", Title: "Aislar la IP en la address-list de cuarentena",
		Explanation: "Si el cliente no responde y la actividad continúa, añada la IP a una address-list que su política de cuarentena " +
			"ya trate (walled garden). Afecta a todo el servicio del cliente; caduca sola en 1 día y se deshace borrando la entrada.",
		Risk: "high", Audience: "network_engineer",
		RouterOS: ros(
			[]string{fw(v6) + " address-list add list={{address_list}} address={{customer_address}} timeout=1d " + commentTpl},
			[]string{fw(v6) + " address-list remove [find list={{address_list}} " + commentTpl + "]"},
			"La address-list no hace nada por sí sola: requiere una regla de cuarentena existente en su router.")}
}

func blockDestination(v6 bool, why string) Action {
	return Action{Code: "block_destination", Title: "Bloquear el destino para esta IP",
		Explanation: why + " La regla solo corta el tráfico de este cliente hacia ese destino; el resto de su servicio sigue igual. " +
			"Se deshace borrando la regla con el comentario del hallazgo.",
		Risk: "low", Audience: "network_engineer",
		RouterOS: ros(
			[]string{fw(v6) + " filter add chain=forward src-address={{customer_address}} dst-address={{remote_ip}} action=drop " + commentTpl + " place-before=0"},
			[]string{fw(v6) + " filter remove [find " + commentTpl + "]"},
			"Revise el orden de reglas si usa listas de interfaces o reglas propias en forward.")}
}

func blockPorts(v6 bool, proto string, ports []int, title string) Action {
	ps := make([]string, len(ports))
	for i, p := range ports {
		ps[i] = strconv.Itoa(p)
	}
	return Action{Code: "block_outbound_port", Title: title,
		Explanation: "Corta la propagación sin dejar al cliente sin Internet: solo bloquea esos puertos de salida desde su IP. " +
			"Se deshace borrando la regla con el comentario del hallazgo.",
		Risk: "low", Audience: "network_engineer",
		RouterOS: ros(
			[]string{fw(v6) + " filter add chain=forward src-address={{customer_address}} protocol=" + proto + " dst-port=" +
				strings.Join(ps, ",") + " action=drop " + commentTpl + " place-before=0"},
			[]string{fw(v6) + " filter remove [find " + commentTpl + "]"},
			"Revise el orden de reglas si usa listas de interfaces o reglas propias en forward.")}
}

func rateLimit(why string) Action {
	return Action{Code: "rate_limit_customer", Title: "Limitar el ancho de banda del cliente",
		Explanation: why + " Una cola simple limita la subida y la bajada de esta IP ({{rate_limit}}) sin cortar el servicio. " +
			"Se deshace borrando la cola con el comentario del hallazgo.",
		Risk: "medium", Audience: "network_engineer",
		RouterOS: ros(
			[]string{`/queue simple add name="horus-{{finding_id}}" target={{customer_address}} max-limit={{rate_limit}} ` + commentTpl},
			[]string{"/queue simple remove [find " + commentTpl + "]"},
			"Si el cliente ya tiene una cola por su plan, colóquela antes con place-before o ajuste la existente.")}
}

func inspect(what string) Action {
	return Action{Code: "inspect_device", Title: "Revisar el equipo del cliente",
		Explanation: what + " Pida al cliente (o a su técnico) que identifique el equipo con la IP afectada y lo revise; no cambia nada en la red.",
		Risk: "low", Audience: "customer_support"}
}

func credentials() Action {
	return Action{Code: "change_default_credentials", Title: "Cambiar contraseñas por defecto y actualizar firmware",
		Explanation: "Los puertos atacados (Telnet, TR-069, ADB, UPnP de Realtek/Huawei) son los que usan Mirai y sus variantes para " +
			"reclutar cámaras, DVR y routers con contraseñas de fábrica. Cambiarlas y actualizar el firmware evita que el equipo se reinfecte tras reiniciarlo.",
		Risk: "low", Audience: "customer_support"}
}

func monitor(what string) Action {
	return Action{Code: "monitor", Title: "Vigilar la evolución",
		Explanation: what + " Si el patrón continúa o aparecen otras señales, el hallazgo se actualiza y subirá el estado del cliente.",
		Risk: "low", Audience: "noc"}
}

func allowlist(what string) Action {
	return Action{Code: "add_to_allowlist", Title: "Añadir a la allowlist del ISP si es legítimo",
		Explanation: what + " Márquelo como falso positivo con un comentario y, si es permanente, añada el destino o el cliente a la " +
			"allowlist del ISP (Seguridad → Allowlist) para que no vuelva a generar hallazgos. Se deshace borrando la entrada.",
		Risk: "low", Audience: "noc"}
}

func reviewKind() Action {
	return Action{Code: "review_customer_kind", Title: "Revisar el tipo de cliente",
		Explanation: "Si el cliente es una empresa con servidor de correo propio, marque la IP como Comercial (Clientes → Tipo): el " +
			"detector usa el umbral de comerciales y este patrón dejará de ser un hallazgo. Si es un hogar, el envío directo al puerto 25 " +
			"no es normal.",
		Risk: "low", Audience: "noc"}
}

var miraiPorts = map[int]bool{23: true, 2323: true, 7547: true, 5555: true, 37215: true, 52869: true}

func intList(v any) []int {
	switch x := v.(type) {
	case []int:
		return x
	case []any:
		out := make([]int, 0, len(x))
		for _, e := range x {
			if f, ok := e.(float64); ok {
				out = append(out, int(f))
			}
		}
		return out
	}
	return nil
}

func protoName(v any) string {
	if s, ok := v.(string); ok && (s == "udp" || s == "tcp") {
		return s
	}
	return "tcp"
}

// Build devuelve las acciones de f (orden = prioridad).
func Build(f *domain.Finding) []Action {
	v6 := f.Address.Addr().Is6() && !f.Address.Addr().Is4In6()
	var out []Action
	ports := intList(f.Evidence["destination_ports"])
	switch f.Kind {
	case domain.KindC2:
		out = []Action{
			contact("Detectamos desde su conexión comunicaciones con un servidor de control de redes de equipos comprometidos (botnet). " +
				"Le recomendamos revisar con un antivirus sus ordenadores y teléfonos, cambiar las contraseñas del router y de las cámaras " +
				"o grabadores y actualizar su firmware."),
			blockDestination(v6, "El destino es un servidor de mando y control (C2) listado en fuentes de reputación aprobadas."),
			inspect("El equipo comprometido suele ser un ordenador, un teléfono o un dispositivo IoT."),
			quarantine(v6),
		}
	case domain.KindScanning:
		if f.Target.Type == domain.TargetRemoteIP {
			out = []Action{
				contact("Detectamos desde su conexión un barrido de puertos contra un mismo servidor, compatible con un equipo comprometido " +
					"o con una herramienta de auditoría. Si no reconoce la actividad, le recomendamos revisar sus equipos."),
				blockDestination(v6, "El destino es el objetivo del escaneo vertical."),
				inspect("Un escaneo de muchos puertos de un solo destino lo hace una herramienta (nmap…) o malware."),
				quarantine(v6),
			}
			break
		}
		msg := "Detectamos desde su conexión intentos de conexión masivos"
		if len(ports) > 0 {
			msg += " a los puertos " + joinInts(ports)
		}
		msg += ", compatibles con un equipo comprometido. Le recomendamos cambiar las contraseñas por defecto de cámaras, DVR y router y actualizar su firmware."
		out = []Action{contact(msg)}
		if len(ports) > 0 {
			out = append(out, blockPorts(v6, "tcp", ports, "Bloquear la salida a los puertos escaneados desde esta IP"))
		}
		mirai := false
		for _, p := range ports {
			mirai = mirai || miraiPorts[p]
		}
		if mirai {
			out = append(out, credentials())
		}
		out = append(out, quarantine(v6))
	case domain.KindFanout:
		out = []Action{
			contact("Detectamos desde su conexión tráfico hacia cientos de redes distintas en pocos minutos, compatible con un equipo " +
				"que forma parte de una red de equipos comprometidos (botnet P2P). Le recomendamos revisar sus equipos y actualizar su firmware."),
			inspect("El patrón (cientos de destinos dispersos por Internet) es típico de bots P2P en routers y dispositivos IoT."),
			rateLimit("Reduce el impacto mientras se revisa el equipo."),
			quarantine(v6),
		}
	case domain.KindSpam:
		out = []Action{
			contact("Detectamos desde su conexión envío directo de correo a muchos servidores distintos, compatible con un equipo que " +
				"envía spam. Si no tiene un servidor de correo propio, le recomendamos revisar sus equipos con un antivirus."),
			{Code: "block_smtp_outbound", Title: "Bloquear SMTP saliente (25/tcp) de esta IP",
				Explanation: "Los clientes residenciales envían correo por el puerto 587 de su proveedor; bloquear el 25 de salida corta el spam " +
					"sin afectar al correo normal. Se deshace borrando la regla con el comentario del hallazgo.",
				Risk: "low", Audience: "network_engineer",
				RouterOS: ros(
					[]string{fw(v6) + " filter add chain=forward src-address={{customer_address}} protocol=tcp dst-port=25 action=drop " + commentTpl + " place-before=0"},
					[]string{fw(v6) + " filter remove [find " + commentTpl + "]"},
					"Si el cliente es una empresa con servidor de correo propio, no lo aplique: revise su tipo.")},
		}
		if f.CustomerKind != "commercial" {
			out = append(out, reviewKind())
		}
		out = append(out, quarantine(v6))
	case domain.KindDDoS:
		why := "El destino es la víctima del ataque."
		proto := protoName(f.Evidence["protocol"])
		out = []Action{blockDestination(v6, why)}
		if len(ports) > 0 && f.Target.Type == domain.TargetRemotePort {
			out = []Action{blockPorts(v6, proto, ports, "Bloquear el tráfico de amplificación saliente de esta IP")}
		}
		out = append(out,
			rateLimit("Contiene el volumen saliente mientras se localiza el equipo."),
			contact("Detectamos desde su conexión un volumen muy alto de tráfico hacia pocos destinos, compatible con un equipo "+
				"que participa en un ataque de denegación de servicio. Le recomendamos desconectar y revisar el equipo afectado."),
			inspect("Los bots de DDoS suelen ser cámaras, DVR o routers con firmware antiguo."),
			quarantine(v6))
	case domain.KindBeaconing:
		out = []Action{
			monitor("Las conexiones periódicas a un mismo destino son típicas de un bot consultando a su C2, pero también de agentes legítimos (actualizaciones, telemetría)."),
			allowlist("Si el destino es un servicio conocido del cliente (VPN, telemetría, copias)…"),
			blockDestination(v6, "Si el destino no es legítimo, cortar la comunicación con él impide que el bot reciba órdenes."),
			contact("Detectamos desde su conexión conexiones periódicas y automáticas a un servidor desconocido, compatibles con un " +
				"equipo comprometido. Le recomendamos revisar sus equipos con un antivirus."),
		}
	case domain.KindOpenProxy:
		out = []Action{
			contact("Detectamos desde su conexión una subida de datos sostenida y muy superior a la habitual, compatible con un equipo " +
				"usado como proxy por terceros o con una fuga de datos. Si no reconoce la actividad, le recomendamos revisar sus equipos."),
			allowlist("Si es una copia de seguridad o un servicio legítimo del cliente…"),
			rateLimit("Reduce el abuso mientras se revisa el equipo."),
			inspect("Las apps de 'compartir ancho de banda', los proxies residenciales y el malware de exfiltración producen este patrón."),
		}
	case domain.KindCryptomining:
		out = []Action{
			contact("Detectamos desde su conexión comunicación con un pool de minería de criptomonedas. Si no reconoce la actividad, " +
				"algún equipo puede estar comprometido con un minero."),
			blockDestination(v6, "El destino es un pool de minería listado en fuentes de reputación."),
			inspect("Los mineros ocultos ralentizan los equipos y suben el consumo eléctrico."),
		}
	default:
		out = []Action{
			monitor("La IP remota aparece en una fuente de reputación; una coincidencia aislada no indica por sí sola un equipo comprometido."),
			allowlist("Si el destino es legítimo para este cliente…"),
		}
	}
	for i := range out {
		out[i].Priority = i + 1
		out[i].Execution = Execution
		if out[i].RouterOS != nil {
			out[i].Explanation = strings.ReplaceAll(out[i].Explanation, "{{rate_limit}}", DefaultRateLimit)
		}
	}
	return out
}

func joinInts(ps []int) string {
	s := make([]string, len(ps))
	for i, p := range ps {
		s[i] = strconv.Itoa(p)
	}
	return strings.Join(s, ", ")
}

// CustomerAddress formatea la dirección del cliente para RouterOS: la IPv4
// sin máscara y el prefijo IPv6 delegado con su longitud (D22).
func CustomerAddress(f *domain.Finding) string {
	a := f.Address.Addr().Unmap()
	if a.Is4() {
		return a.String()
	}
	return f.Address.Masked().String()
}

// Values son los valores de los placeholders de f.
func Values(f *domain.Finding) map[string]string {
	v := map[string]string{
		"customer_address": CustomerAddress(f),
		"finding_id":       f.ID.String(),
		"address_list":     QuarantineList,
		"rate_limit":       DefaultRateLimit,
		"protocol":         protoName(f.Evidence["protocol"]),
	}
	switch f.Target.Type {
	case domain.TargetRemoteIP:
		v["remote_ip"] = f.Target.Value
	case domain.TargetRemotePort:
		v["remote_port"] = f.Target.Value
	case domain.TargetRemotePrefix:
		v["remote_prefix"] = f.Target.Value
	}
	return v
}

// Render rellena rendered_* (solo API y con customers.read). Si render es
// false los deja en null (eventos, kioscos, sin customers.read).
func Render(list []Action, values map[string]string, render bool) []Action {
	out := slices.Clone(list)
	for i := range out {
		if out[i].RouterOS == nil {
			continue
		}
		r := *out[i].RouterOS
		r.RenderedCommands, r.RenderedUndoCommands = nil, nil
		if render {
			c, u := fill(r.Commands, values), fill(r.UndoCommands, values)
			r.RenderedCommands, r.RenderedUndoCommands = &c, &u
		}
		out[i].RouterOS = &r
	}
	return out
}

func fill(tpls []string, values map[string]string) []string {
	out := make([]string, len(tpls))
	for i, t := range tpls {
		for k, v := range values {
			t = strings.ReplaceAll(t, "{{"+k+"}}", v)
		}
		out[i] = t
	}
	return out
}

// Check comprueba invariantes de D11 (tests): ejecución manual, plantillas
// que empiezan por "/", placeholders conocidos y comando de deshacer para
// todo lo que crea algo.
func Check(list []Action) error {
	known := map[string]bool{"customer_address": true, "finding_id": true, "remote_ip": true, "remote_prefix": true,
		"remote_port": true, "protocol": true, "address_list": true, "rate_limit": true}
	for _, a := range list {
		if a.Execution != Execution {
			return fmt.Errorf("%s: execution %q", a.Code, a.Execution)
		}
		if a.RouterOS == nil {
			continue
		}
		if len(a.RouterOS.Commands) == 0 || len(a.RouterOS.UndoCommands) == 0 {
			return fmt.Errorf("%s: sin comandos o sin deshacer", a.Code)
		}
		for _, c := range append(slices.Clone(a.RouterOS.Commands), a.RouterOS.UndoCommands...) {
			if !strings.HasPrefix(c, "/") || len(c) > 1000 {
				return fmt.Errorf("%s: plantilla inválida %q", a.Code, c)
			}
		}
		for _, c := range a.RouterOS.Commands {
			if !strings.Contains(c, commentTpl) {
				return fmt.Errorf("%s: el comando no lleva el comentario del hallazgo: %q", a.Code, c)
			}
		}
		for _, p := range a.RouterOS.Placeholders {
			if !known[p] {
				return fmt.Errorf("%s: placeholder desconocido %q", a.Code, p)
			}
		}
	}
	return nil
}
