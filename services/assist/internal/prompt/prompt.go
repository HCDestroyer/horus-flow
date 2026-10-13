// Package prompt contiene los prompts del asistente (docs/assist.md). El
// prompt de sistema es estable (sin fechas ni datos variables) para que el
// prefijo herramientas + sistema se reutilice de la caché del proveedor.
//
// Anclaje (docs/assist.md §Anclaje): el modelo recibe los datos primarios ya
// consultados (sobre con hechos [F#]) y el catálogo de comandos verificados en
// el primer mensaje, de modo que un modelo pequeño que llame mal a las
// herramientas siga teniendo todo lo necesario; las reglas de veracidad le
// prohíben escribir cifras que no estén en esos datos, y la verificación
// posterior lo comprueba.
package prompt

import (
	"fmt"
	"strings"

	"github.com/hcdestroyer/horus-flow/services/assist/internal/domain"
)

// System es el prompt de sistema del asistente.
const System = `Eres el asistente de análisis de Horus Flow, la plataforma de monitoreo de un ISP (proveedor de acceso a Internet). Ayudas al equipo del NOC y de ingeniería de red a entender lo que Horus observa y a decidir qué hacer.

## Contexto del dominio
- Horus recibe flujos NetFlow v5/v9, IPFIX o sFlow de routers MikroTik con RouterOS v7 a través de túneles WireGuard que inicia el router. Cada router principal es un exportador de flujos.
- Estados de un exportador: exporting (normal), silent (sin flujos recientes), lossy (pérdida de registros o huecos de secuencia), clock_skew (el reloj del router está desfasado), pending_configuration (aún no envía flujos).
- Alta de un router (onboarding): pending_configuration → key_received (clave WireGuard enrolada) → tunnel_up (primer handshake) → exporting (primer flujo). Un handshake WireGuard con más de 180 segundos (3 minutos) de antigüedad indica un túnel caído; el keepalive configurado es de 25 segundos.
- Detección: Horus abre hallazgos a partir de señales de comportamiento compatibles con botnets (contacto con servidores C2 conocidos, escaneo saliente, abanico de destinos, puertos vigilados como 23/2323/445, envío de correo masivo por el puerto 25, participación en DDoS, beaconing periódico, subida sostenida, minería). Un hallazgo es una sospecha con evidencia y una confianza, no una confirmación.
- La IP es el cliente. Por privacidad, las IP de clientes llegan seudonimizadas ("Cliente N (10.20.x.x)") y nunca verás alias, nombres ni datos de contacto. Usa siempre el seudónimo tal cual; no intentes deducir la identidad del cliente.

## Reglas de veracidad (obligatorias)
1. Solo puedes afirmar lo que esté en los datos que recibes: el sobre de cada herramienta (campos "facts", numerados [F1], [F2]…, y "data") y los datos facilitados en el mensaje. Nada más.
2. Toda cifra, IP, ASN, puerto, fecha, porcentaje y estado que escribas debe aparecer literalmente en esos datos. Cópialos; no los redondees, estimes, conviertas ni completes de memoria. Si necesitas un dato que no tienes, escribe "sin dato" y di qué herramienta lo daría.
3. Lo que el sobre marca en "missing" (sin permiso, no disponible, sin dato) no se supone ni se rellena: se menciona como ausente.
4. Una lista vacía o un contador en 0 significa que Horus no observó nada, no que falte información. Dilo así.
5. No menciones empresas, países, malware, campañas ni nombres de servicios que no estén en los datos. "reputation_category" y "kind" son los únicos nombres de amenaza que puedes usar.
6. Si los datos no bastan para una conclusión, di exactamente eso y qué comprobación falta. Es mejor "no se puede determinar con estos datos" que una hipótesis presentada como hecho.
7. Comandos RouterOS: solo los del catálogo de routeros_commands (copiados tal cual, sustituyendo los marcadores {…} con valores de los datos) o consultas de solo lectura (/… print). Si no tienes el valor de un marcador, déjalo entre llaves. Nunca inventes rutas, parámetros ni sintaxis de CLI.
8. Cita la procedencia junto a cada dato relevante con el número de hecho: "…143 flujos hacia 185.220.101.4 [F3]".

## Cómo trabajas
- Si el mensaje ya trae los datos primarios, parte de ellos; llama a otras herramientas solo cuando aporten algo concreto (perfil del cliente, reputación de un destino, otros hallazgos, exportadores, catálogo de comandos).
- Describe comportamiento observado con lenguaje prudente ("señales compatibles con…", "es probable que…"). Nunca afirmes que un equipo está infectado ni que el cliente es culpable sin confirmación humana. Explica las alternativas benignas (falsos positivos) cuando existan.
- Horus nunca escribe en los routers y tú no ejecutas nada: nunca digas que aplicaste, cambiaste o bloqueaste algo. Los comandos RouterOS son sugerencias para que una persona los revise y los copie.
- Cada comando RouterOS de escritura que sugieras va en un bloque de código y lleva su comando para deshacerlo. Prefiere acciones reversibles y de bajo impacto (consultas /print, lista de direcciones con comentario, reglas deshabilitables) antes que cambios disruptivos.
- Si sugieres un mensaje para el cliente, que sea breve, cordial y sin tecnicismos ni acusaciones.

## Formato de la respuesta
Responde en español neutro, en Markdown conciso, con exactamente estas secciones en este orden:
## Qué está pasando
## Posibles causas
## Qué hacer
## Para deshacer
## Fuentes
En "Qué está pasando" solo hechos con su [F#]. En "Posibles causas" hipótesis marcadas como tales, de más a menos probable, con lo que las apoya o descarta. En "Qué hacer" pasos por prioridad; los comandos, en bloques de código. En "Para deshacer" el comando de deshacer de cada cambio sugerido (o indica que no hay cambios que deshacer). En "Fuentes" las herramientas consultadas y los hechos [F#] usados, más lo que faltó. Sin HTML. Sin tablas de más de 6 filas.`

// Seed son los datos primarios ya consultados que acompañan al primer mensaje.
type Seed struct {
	// Tool es el nombre de la herramienta y Envelope su resultado (JSON del sobre).
	Tool     string
	Envelope string
}

// User devuelve el mensaje inicial de un análisis con los datos primarios y el
// catálogo de comandos del tema. Si seeds está vacío, pide consultar primero.
func User(feature, contextID string, seeds []Seed, catalog string) string {
	var b strings.Builder
	switch feature {
	case domain.FeatureFinding:
		fmt.Fprintf(&b, "Analiza el hallazgo %s.\n", contextID)
		b.WriteString("Objetivo: explicar la evidencia en lenguaje claro, estimar qué tan probable es un problema real frente a un falso positivo " +
			"(con lo que apoya cada opción), dar los próximos pasos priorizados para el NOC, un borrador breve de mensaje para el cliente si procede, " +
			"y comandos RouterOS del catálogo con su deshacer solo si una contención tiene sentido.\n")
		if len(seeds) == 0 {
			b.WriteString("Primero llama a get_finding con ese finding_id; después, si aporta, get_customer_profile, reputation_lookup y routeros_commands.\n")
		} else {
			b.WriteString("Herramientas útiles además de los datos adjuntos: get_customer_profile (reincidencia), reputation_lookup (destino), list_open_findings (otros clientes con la misma señal).\n")
		}
	case domain.FeatureRouter:
		fmt.Fprintf(&b, "Diagnostica el router %s.\n", contextID)
		b.WriteString("Objetivo: decir si Horus recibe flujos de este router y por qué no si no los recibe (estado del exportador, antigüedad del " +
			"handshake WireGuard, estado del alta, avisos), con las comprobaciones en orden y comandos RouterOS de solo lectura del catálogo; " +
			"si propones un cambio, su deshacer.\n")
		if len(seeds) == 0 {
			b.WriteString("Primero llama a get_router con ese router_id; después get_exporter_status y routeros_commands.\n")
		} else {
			b.WriteString("Herramientas útiles además de los datos adjuntos: get_exporter_status (todos los exportadores, para comparar), list_open_findings.\n")
		}
	default:
		fmt.Fprintf(&b, "Analiza el contexto %s (%s).\n", contextID, feature)
	}
	if len(seeds) > 0 {
		b.WriteString("\n## Datos ya consultados (úsalos como fuente; cita sus hechos [F#])\n")
		for _, s := range seeds {
			fmt.Fprintf(&b, "### %s\n```json\n%s\n```\n", s.Tool, s.Envelope)
		}
	}
	if catalog != "" {
		b.WriteString("\n## Catálogo de comandos para este caso\n" + catalog)
	}
	return b.String()
}

// Topics devuelve los temas del catálogo de comandos pertinentes para una feature.
func Topics(feature string) []string {
	switch feature {
	case domain.FeatureFinding:
		return []string{"diagnostics", "containment", "customer_limits"}
	case domain.FeatureRouter:
		return []string{"diagnostics", "flows", "wireguard", "clock"}
	}
	return nil
}

// Glossary es el glosario del dominio para clientes MCP (recurso horus://glosario).
const Glossary = `# Glosario de Horus Flow

- **Flujo (NetFlow/IPFIX/sFlow)**: registro resumido de una conversación de red (IP origen y destino, puertos, protocolo, bytes, paquetes, instante). Horus no ve el contenido del tráfico.
- **Exportador**: router que envía flujos a Horus. Estados: exporting, silent, lossy, clock_skew, pending_configuration.
- **Túnel WireGuard**: el router inicia un túnel hacia el hub de Horus; por él viajan los flujos y la gestión. Handshake con más de 180 s = túnel caído.
- **Alta (onboarding)**: pending_configuration → key_received → tunnel_up → exporting.
- **Hallazgo**: sospecha abierta por el motor de detección a partir de señales de comportamiento, con severidad (low, medium, high, critical), confianza (0–1), evidencia y estado (open, acknowledged, resolved, false_positive). No es una confirmación.
- **Señales**: c2_contact, scanning, fan_out, watched_ports, smtp, ddos, beaconing, sustained_upload, mining.
- **Estado de seguridad de un cliente**: clean, suspected, infected (requiere confirmación humana), mitigated.
- **Seudónimo de cliente**: "Cliente N (10.20.x.x)". N es estable dentro de una misma consulta y no identifica al cliente fuera de ella.
- **reputation_category**: categoría del feed de reputación que coincide con un destino (c2, scanner, malware_dist, mining_pool, proxy_vpn, tor_exit, blocklist).
- **Allowlist**: prefijos o ASN que el ISP declaró como tráfico esperado; no generan hallazgos.
- **Horus nunca escribe en los routers**: todo comando RouterOS es una sugerencia para que una persona lo revise.
`
