# Horus Flow — Asistente de IA (rol `assist`)

> Estado: **implementado (v1)** · Módulo [`services/assist`](../services/assist/README.md) · Contrato
> [`assist.yaml`](../packages/schemas/openapi/v0/assist.yaml) · Esquema PostgreSQL `assist`.
> Relacionados: [`services.md`](services.md), [`security.md`](security.md), [`api.md`](api.md),
> [`frontend.md`](frontend.md).

El asistente explica en lenguaje claro lo que Horus observa y propone qué hacer. Dos formas de uso con
las **mismas herramientas de solo lectura** y el **mismo enmascarado**:

1. **Dentro de Horus**: botón "Analizar con IA" en el detalle de un hallazgo (sección "Qué hacer") y en
   la ficha de un router (pestaña Resumen).
2. **Servidor MCP**: Claude Desktop u otro cliente MCP consulta Horus con un token MCP.

## 1. Reglas del producto (no negociables)

- **Horus nunca escribe en los routers.** La IA puede sugerir comandos RouterOS (cada uno con su comando
  para deshacer) para que una persona los revise y los copie; nunca se ejecuta nada. Las herramientas son
  de solo lectura (solo `GET` a la API de Horus).
- **Lenguaje de señales.** El prompt obliga a describir comportamiento observado ("señales compatibles
  con…") y prohíbe afirmar una infección sin confirmación humana. La respuesta siempre lleva el aviso
  *"Generado por IA; revisa antes de aplicar. Horus no ejecuta nada en tus routers."*
- **Privacidad.** La IA solo ve datos enmascarados (§4). Cada consulta se audita (§6).
- **Credenciales write-only.** La clave de API se guarda cifrada y nunca vuelve en la API.

## 2. Arquitectura

```
 navegador ── POST /assist/analyses ──► gateway (borde) ──► assist (app.Service)
                                                              │ 1. ¿activo? ¿límite diario?
                                                              │ 2. lee el recurso como el usuario (404 si no lo ve)
                                                              │ 3. bucle del agente (máx. 8 pasos, timeout)
                                                              ▼
                                       proveedor de IA ◄──► tools.Registry ──► loopback (en proceso)
                                (Anthropic u OpenAI-compatible)   │ enmascarado      │ Bearer del usuario
                                                                  ▼                  ▼
                                                             mask.Masker     borde del gateway + handler del
                                                                             módulo dueño (RBAC, RLS, PII)
```

| Pieza | Paquete | Qué hace |
| --- | --- | --- |
| Módulo / rol | `services/assist` (`Role = "assist"`) | Cableado manual, migraciones, rutas REST y MCP |
| Dominio | `internal/domain` | Configuración y su validación, features, resultados auditables |
| Casos de uso | `internal/app` | Configuración (clave cifrada), prueba de conexión, análisis, límite diario, auditoría, tokens MCP |
| Bucle del agente | `internal/agent` | Pide al modelo, ejecuta en paralelo todas las herramientas de un turno y devuelve todos los resultados en **un** mensaje; herramienta fallida → `is_error`; tope de iteraciones; rechazo comprobado antes de leer el contenido |
| Proveedores | `internal/provider/{anthropic,openaicompat,fake}` | Interfaz común (conversación con historial nativo) y adaptadores |
| Herramientas | `internal/tools` | Registro compartido por el agente y el servidor MCP; validación de argumentos (JSON Schema reducido, sin propiedades extra) |
| Enmascarado | `internal/mask` | Seudónimos de clientes por análisis; quita alias, nombres y contactos |
| Lector | `internal/adapters/loopback` | `tools.Reader` que llama en proceso a la API REST como el usuario |
| Persistencia | `internal/adapters/postgres` | `assist.settings`, `assist.audit`, `assist.analysis` (historial), `assist.mcp_token` con RLS |
| MCP | `internal/adapters/mcpserver` | SDK oficial de Go de MCP, Streamable HTTP sin estado |

### 2.1 Cómo llegan las herramientas a los datos

Las herramientas **no** leen tablas de otros módulos (rompería las fronteras de módulo y el RLS de
cada esquema). Llaman a la **API REST del propio proceso** con el `Authorization: Bearer` del usuario que
pide el análisis: `httpx.ServiceMux.Loopback()` devuelve el handler completo del proceso (con el borde del
gateway), así que cada lectura pasa por la misma autenticación, permiso grueso, alcance por nodo, RLS,
visibilidad de datos personales (`customers.read`) y auditoría de accesos (p. ej. la evidencia, auditada)
que una petición del navegador. Si el usuario no tiene un permiso, la herramienta devuelve "sin permiso"
al modelo y este lo dice.

Consecuencia: el rol `assist` debe correr en el mismo proceso que `gateway` y los módulos de datos
(`detection`, `devices`, `wireguard`, `flows`, `analytics`), como en el perfil mínimo (`horus-app`). En
un despliegue partido, las herramientas cuyos datos no estén en el proceso responden "no disponible".

### 2.2 Servidor MCP y token delegado

Horus no tiene todavía API tokens de usuario de larga vida (el permiso `api_tokens.manage` existía sin
implementación) y los access tokens duran 10 min. Para MCP:

- En Administración › Inteligencia artificial, un usuario con `api_tokens.manage` crea un **token MCP**
  (`hmcp_…`, 256 bits). Solo se guarda su SHA-256; el valor se muestra una vez. Caduca a los
  `HORUS_ASSIST_MCP_TOKEN_TTL` (90 días) y se revoca desde la misma página.
- `POST /api/v1/assist/mcp` lleva el permiso especial `mcp_token` en la tabla del gateway: el borde no
  exige JWT y assist valida el token.
- En cada petición assist pide a **auth** (contrato en proceso `auth.Delegation`,
  `services/auth/api/delegation.go`) un token **delegado** de 2 min: `typ=service`, `scope=tenant`, con la
  intersección entre los permisos de lectura de las herramientas y los permisos **actuales** del usuario
  en el ISP (con sus alcances por nodo). auth comprueba en cada emisión que el usuario, su membresía y el
  ISP siguen activos; si no, el token MCP deja de servir. El borde no consulta sesión para `typ=service`.
- Requiere `auth` en el mismo proceso; si no, la página lo indica y el endpoint responde 503.

> Desviación respecto del encargo: el contrato `auth.Delegation` toca `services/auth` (ruta sensible,
> CODEOWNERS); es pequeño y está probado, pero necesita la revisión de la persona.

## 3. Proveedores

| Proveedor | Cuándo | Detalles |
| --- | --- | --- |
| `anthropic` (por defecto) | Claude por la API de Anthropic | SDK oficial `github.com/anthropics/anthropic-sdk-go`. Modelo por defecto `claude-opus-5-5`. Sistema estable con `cache_control` (herramientas + sistema = prefijo cacheado, sin fechas). El razonamiento no se desactiva (no se envía `thinking`); la profundidad se controla con `output_config.effort` (`low`/`medium`/`high`, por defecto `medium`). `tool_choice: auto` (forzar herramienta da 400 en este modelo), sin prefill. Respaldo del lado del servidor ante rechazos de los clasificadores (`fallbacks: "default"`, beta `server-side-fallback-2026-07-01`). `stop_reason: refusal` → `422 ASSIST_REFUSED`. Errores por código HTTP (el SDK ya reintenta 429/5xx dos veces) |
| `openai_compatible` | Modelo local (Ollama, vLLM, LM Studio): **ningún dato sale de la red del ISP** | `POST {base_url}/v1/chat/completions` con *function calling*, mismas semánticas de bucle. Clave opcional (Bearer). El cliente HTTP rechaza enlace local (incluida la IP de metadatos 169.254.169.254), multicast, loopback (salvo `HORUS_ASSIST_ALLOW_LOOPBACK=true`) y las IP de la propia instalación; las privadas se permiten. Sin redirecciones. No es para Claude |
| `fake` | Tests | Turnos de guion |

### 3.1 Configuración

Cada ISP configura su proveedor en **Administración › Inteligencia artificial** (`settings.manage`). Sin
configuración propia rige el **valor por defecto de plataforma** del entorno:

| Variable | Defecto | Uso |
| --- | --- | --- |
| `HORUS_ASSIST_DEFAULT_ENABLED` | `false` | Activo en los ISP sin configuración propia |
| `HORUS_ASSIST_PROVIDER` | `anthropic` | `anthropic` u `openai_compatible` |
| `HORUS_ASSIST_MODEL` | (del proveedor) | `claude-opus-5-5` / `llama3.1` |
| `HORUS_ASSIST_BASE_URL` | — | Solo `openai_compatible` |
| `HORUS_ASSIST_API_KEY(_FILE)` | — | Clave de plataforma (opcional); la usan los ISP sin clave propia con el mismo proveedor |
| `HORUS_ASSIST_EFFORT` | `medium` | Esfuerzo por defecto |
| `HORUS_ASSIST_DAILY_LIMIT` | `100` | Límite diario por defecto |
| `HORUS_ASSIST_KEK(_FILE)` | — (efímera en dev) | KEK de 32 B que cifra las claves por ISP |
| `HORUS_ASSIST_MAX_ITERATIONS` | `8` | Pasos máximos del bucle |
| `HORUS_ASSIST_REQUEST_TIMEOUT` | `120s` | Tiempo máximo de un análisis |
| `HORUS_ASSIST_HISTORY_RETENTION` | `2160h` | Cuánto se guardan los análisis para volver a verlos (`0` = no se guardan) |
| `HORUS_ASSIST_MAX_OUTPUT_TOKENS` | `16000` | Tokens de salida por turno |
| `HORUS_ASSIST_ALLOW_LOOPBACK` | `false` | Permite `base_url` en 127.0.0.1/::1 (sin contenedores) |
| `HORUS_ASSIST_MCP` / `HORUS_ASSIST_MCP_TOKEN_TTL` | `true` / `2160h` | Servidor MCP y vigencia de sus tokens |

La clave de API se cifra con *envelope encryption* (`packages/go/crypto/envelope`, el mismo helper de las
credenciales de routers y destinos remotos) con AAD `assist_api_key:<tenant>`: copiada a otro ISP no se
descifra. Al cambiar de proveedor sin enviar clave nueva, la guardada se descarta (la de Claude nunca se
envía a otro servidor).

**Ejemplo con Ollama en la red del ISP** (todo queda dentro):

```bash
# En un servidor del ISP con GPU
ollama pull llama3.1
OLLAMA_HOST=0.0.0.0:11434 ollama serve
```

En la página: proveedor "Servidor compatible con OpenAI (modelo local)", URL `http://10.0.0.5:11434`,
modelo `llama3.1` (o cualquier modelo con soporte de herramientas, p. ej. `qwen2.5`), "Probar conexión".
Para todo un despliegue: `HORUS_ASSIST_PROVIDER=openai_compatible`, `HORUS_ASSIST_BASE_URL=…`,
`HORUS_ASSIST_MODEL=llama3.1`, `HORUS_ASSIST_DEFAULT_ENABLED=true`.

## 4. Privacidad: qué ve la IA

Todo resultado de herramienta pasa por `mask.Masker` antes de salir hacia el proveedor:

- **IP de clientes** → seudónimo estable dentro del análisis: `Cliente N (10.20.x.x)` (IPv4 conserva el
  /16; IPv6 el /32). Se detectan por clave (`address`, `client_ip`, objetos `customer`…) y en la ficha de
  cliente cualquier IP; después se reemplaza toda aparición de esas IP en cualquier texto. Los flujos de
  evidencia `internal` (hacia otro cliente) también se seudonimizan.
- **Se eliminan** alias, nombres, notas, correos, teléfonos, usuarios, claves públicas y los comandos ya
  rellenados con la IP del cliente (`rendered_commands`).
- **Se conservan** comportamiento, puertos, protocolos, ASN, IP remotas (servidores C2, destinos), estados
  de routers, exportadores y túneles, y las direcciones de túnel/exportador (infraestructura del ISP).
- El mapa de seudónimos vive solo en el servidor; la respuesta se muestra tal cual (enmascarada). Una IP
  real que el modelo repita se reemplaza también en la respuesta.
- La evidencia se agrega (destino, puerto, protocolo, sentido → flujos, bytes, paquetes; top 20) y cada
  resultado se recorta a 24 KiB.

## 5. Herramientas (v1)

| Herramienta | Lee (como el usuario) | Permisos |
| --- | --- | --- |
| `get_finding(finding_id, include_evidence)` | `GET /findings/{id}` + `/evidence` (agregada) | `security.findings.read` (+ `security.evidence.read`) |
| `list_open_findings(limit, severity)` | `GET /findings?state=open,acknowledged` | `security.findings.read` |
| `get_exporter_status(router_id?)` | `GET /flow-exporters[/{id}]` | `flows.read` |
| `get_router(router_id)` | `GET /routers/{id}` + peer WireGuard + exportador | `devices.read`, `wireguard.read`, `flows.read` |
| `get_traffic_top(dimension, range, site_id, direction)` | `GET /analytics/traffic/top` | `traffic.read` (`customers` exige `customers.read`) |
| `get_customer_profile(customer_ref)` | `GET /customers/{id}` + sus hallazgos | `customers.read` |
| `reputation_lookup(asn \| prefix)` | allowlist del ISP, hallazgos abiertos con ese objetivo y feeds activos | `security.findings.read` |
| `routeros_commands(topic)` | catálogo de comandos RouterOS v7 verificados con su deshacer (no lee la API) | — |

`reputation_lookup` no es una consulta indicador por indicador (la API no la expone todavía): la
coincidencia con feeds se refleja en `reputation_category` de los hallazgos.

Diseño genérico: `POST /assist/analyses {feature, context_id}` con `feature` en un enum abierto (`finding`,
`router`); un punto de entrada nuevo es un permiso de lectura, una ruta de comprobación y un prompt de
usuario en `internal/prompt`.

## 6. Auditoría y límite diario

`assist.audit` guarda por consulta: usuario, ISP, feature, id de contexto (o nombre de la herramienta MCP),
proveedor, modelo (el que respondió), tokens de entrada/salida, herramientas usadas (nombre, veces,
errores), duración, resultado (`success|refused|error|limited|denied`) y código de error. **Nunca** la
respuesta, la evidencia ni IP (la respuesta enmascarada va aparte, en el historial: §6.1). Los cambios de configuración y de tokens MCP van además a la auditoría del
ISP (auth).

Límite diario por ISP (UTC): cuentan los análisis que el proveedor respondió (`success`, `refused`) y
los errores que llegaron a gastar tokens. Un fallo sin tokens (proveedor caído, tiempo agotado antes de la
primera respuesta) no consume cupo. Superado → `429 ASSIST_LIMIT_REACHED` con `Retry-After` hasta medianoche UTC. La prueba de
conexión y las llamadas MCP no cuentan (no hay llamada al proveedor de Horus), pero se auditan.

### 6.1 Historial

Cada análisis correcto se guarda en `assist.analysis` tal como se mostró: el Markdown **ya enmascarado**
(seudónimos, sin IP ni alias de clientes), el anclaje, el modelo, los tokens y las herramientas. Lo ve
cualquier miembro del ISP con permiso de lectura del recurso (el recurso se comprueba como el usuario,
igual que al analizar), y volver a verlo no consume consultas. Retención:
`HORUS_ASSIST_HISTORY_RETENTION` (90 días; los anteriores se borran al guardar uno nuevo del ISP; `0`
desactiva el historial). El panel "Análisis con IA" muestra el más reciente y deja elegir uno anterior.

## 7. API

`GET /assist/status` (cualquier miembro), `GET|PUT /assist/settings` (`settings.read` / `settings.manage`;
`api_key` write-only → `api_key_set`), `POST /assist/settings/test`, `POST /assist/analyses`,
`GET /assist/analyses?feature=&context_id=` (historial; permiso de lectura del recurso),
`GET /assist/audit`, `GET|POST /assist/mcp-tokens`, `DELETE /assist/mcp-tokens/{id}`
(`api_tokens.manage`), `POST /assist/mcp` (token MCP). Detalle en
[`assist.yaml`](../packages/schemas/openapi/v0/assist.yaml).

## 8. Conectar Claude Desktop (MCP)

1. Administración › Inteligencia artificial › Servidor MCP › "Crear token". Copia el token (`hmcp_…`).
2. En `claude_desktop_config.json` (Claude Desktop › Ajustes › Desarrollador › Editar configuración):

```json
{
  "mcpServers": {
    "horus": {
      "command": "npx",
      "args": [
        "mcp-remote",
        "https://horus.example.net/api/v1/assist/mcp",
        "--header",
        "Authorization: Bearer hmcp_XXXXXXXXXXXXXXXX"
      ]
    }
  }
}
```

3. Reinicia Claude Desktop: aparecen las 7 herramientas de Horus (solo lectura). Los clientes que
   admiten Streamable HTTP con cabeceras propias pueden apuntar directamente a la URL con
   `Authorization: Bearer hmcp_…`.

El servidor es sin estado (cada petición JSON-RPC se atiende sola, respuestas JSON). El asistente debe
estar activo en el ISP; si el usuario pierde un rol o la membresía, las herramientas afectadas devuelven
"sin permiso" o el token deja de servir.

## 9. Anclaje a los datos (anti-alucinación)

El asistente debe funcionar con cualquier modelo, también uno local pequeño que llame mal a las
herramientas o invente cifras con soltura. Por eso el anclaje no depende de la calidad del modelo:
se construye alrededor de él, en cinco capas (`internal/tools`, `internal/prompt`,
`internal/grounding`, `internal/app`).

| Capa | Qué hace | Dónde |
| --- | --- | --- |
| **Sobre de hechos** | Toda herramienta devuelve `{tool, as_of, facts[], missing[], units, data}`. `facts` son frases con los valores exactos y sus unidades, numeradas [F1], [F2]… para citarlas; `missing` lista lo que no se obtuvo (sin permiso, no disponible, sin dato) para que el modelo lo diga en vez de rellenarlo; `units` fija las unidades. Las listas largas se acortan por mitades (nunca se corta el JSON a medias). | `tools.Envelope`, `tools/facts.go` |
| **Datos precargados** | Antes del primer turno, el servicio ejecuta las herramientas primarias de la feature (`get_finding`; `get_router` + `get_exporter_status`) y las incluye en el mensaje junto con el catálogo de comandos del tema. Un modelo que no sepa llamar herramientas sigue teniendo todos los datos. Cuentan como uso de herramienta en la auditoría. | `app.seed`, `prompt.User` |
| **Catálogo RouterOS verificado** | `routeros_commands(topic)` devuelve plantillas de RouterOS v7 con su deshacer y marcadores `{…}`. Son los únicos comandos de escritura admitidos; además se admite cualquier consulta de solo lectura (`/… print`, `/ping`, `/tool torch`…). La verificación comprueba prefijo, parámetros (los fijos con su valor: `action=drop`, `chain=forward`, `list=horus-revisar`) y la etiqueta `comment="horus …"`. | `tools/routeros.go` |
| **Reglas de veracidad** | El prompt de sistema obliga a copiar literalmente cifras, IP, ASN, puertos, fechas y estados de los datos; a tratar `missing` como ausente; a no nombrar empresas, países ni malware que no estén en los datos; a decir "sin dato" o "no se puede determinar"; y a citar [F#]. Secciones fijas: Qué está pasando, Posibles causas, Qué hacer, Para deshacer, **Fuentes**. | `prompt.System` |
| **Verificación y corrección** | Tras la respuesta (ya enmascarada), `grounding.Check` extrae cada IP, ASN, UUID, puerto, porcentaje y cifra con unidad del texto y comprueba que exista en la evidencia (sobres de herramientas, datos precargados, prompt); los comandos de los bloques de código se validan contra el catálogo. Si algo falla, el modelo recibe **un turno de corrección** con la lista exacta (hasta 3 pasos). Si sigue fallando, la respuesta lleva un aviso `⚠ Verificación: …` y `grounding.status = partial` con lo no verificado; la UI lo muestra en ámbar. | `grounding`, `app.run`, `AssistGrounding` |

Detalles que evitan falsos positivos de la verificación: los números se comparan en todas sus
formas (`12.400.000`, `12400000`, `12,4`, redondeos a un decimal), un porcentaje se acepta si la
evidencia trae la proporción 0–1 (`confidence: 0.92` respalda "92 %"), y los enteros pequeños (≤ 12)
se consideran conteos de redacción ("3 pasos").

En el proveedor compatible con OpenAI se envía `temperature: 0` (menos invención al no muestrear).
En Claude el esfuerzo se controla con `effort`.

**MCP.** El servidor expone el mismo anclaje a clientes externos: cada herramienta devuelve el sobre
como texto y como `structuredContent` (con `outputSchema`); las *instructions* del servidor incluyen
las reglas de veracidad; los prompts `analizar_hallazgo(finding_id)` y `diagnosticar_router(router_id)`
devuelven el mensaje completo con los datos precargados y el catálogo; y los recursos `horus://guia`,
`horus://glosario` y `horus://routeros` dan el contexto de referencia. La verificación posterior la
hace solo la app (el cliente MCP controla su propio modelo).

## 10. Pruebas

- Go (unitarios): enmascarado, registro y validación de argumentos, bucle con proveedor falso (herramientas
  en paralelo en un único mensaje, error de herramienta, tope de iteraciones, rechazo), adaptador
  OpenAI-compatible contra `httptest`, forma de la petición a Anthropic contra un servidor falso (nunca la
  API real), servicio (clave nunca devuelta, cifrada y ligada al ISP, límite diario, auditoría), servidor
  MCP de punta a punta, token delegado de auth y su paso por el borde.
- Go (integración, `-tags integration`): store PostgreSQL con RLS forzada, aislamiento entre ISPs,
  migraciones arriba/abajo.
- Frontend: `tests/unit/markdown.test.ts` (Markdown seguro, sin HTML) y `tests/e2e/assist.spec.ts`
  contra la API simulada (`mocks/assist.ts`).
