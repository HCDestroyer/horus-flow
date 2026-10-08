# Incremento 1 — Primer entregable: NOC de un nodo MikroTik

- **Objetivo:** que un ISP vea, en una pantalla de monitoreo, **qué clientes (IPs) de un nodo
  MikroTik consumen qué y cuáles muestran señales compatibles con botnet**, con evidencia
  suficiente para actuar (D1, D5, D8, D10).
- **Demostración de la persona:** genera el script de onboarding desde la ficha de su router
  (RouterOS v7 ≥ 7.12) y lo pega (el router se enrola solo con un token de un uso); importa los
  pools del MikroTik como prefijos de clientes; en ≤ 2 min el router está *Exportando* y aparecen
  clientes por IP, tops de tráfico/servicios/categorías/ASN y hallazgos de
  botnet explicados; cambia el tipo de una IP a *Comercial*; abre el dashboard "NOC del ISP" en una
  TV en modo kiosco que rota con "Seguridad" y se recupera solo de cortes.
- **Gate G1:** la persona prueba con su MikroTik real (I1-27) y acepta o rechaza el incremento.
- **Salida:** `make accept-i1` en verde con simulador y fixtures CHR; lista de validación de
  [`../vendors/mikrotik.md`](../vendors/mikrotik.md) §8.3 superada en el laboratorio (I1-25).
- **Regla de v1:** **Horus no escribe en el router.** Todo lo que cambia en el MikroTik lo pega el
  técnico (script de §7 de `vendors/mikrotik.md`).
- Plan, alcance y lo que se pospone: [`../roadmap.md`](../roadmap.md) §3 (I1). Reglas:
  [`README.md`](README.md). Agentes y olas: [`team.md`](team.md). Notación `mod:<módulo>`:
  [`team.md`](team.md) §2.

| ID | Historia | Agente | Área | Talla | Depende de | Ola |
| --- | --- | --- | --- | --- | --- | --- |
| I1-01 | Hub WireGuard mínimo: IPAM de túnel y enrolamiento del router | CORE | backend, security | M | I0-09, I0-02 | 2 |
| I1-02 | Script de onboarding RouterOS, token de enrolamiento y script inverso | CORE | backend, security | M | I1-01, I0-12 | 2 |
| I1-03 | Colector IPFIX / NetFlow v9 | FLOW | data | M | I0-10, I0-12, I1-01 (C2) | 2 |
| I1-04 | Ingesta a ClickHouse con atribución por prefijos de clientes | FLOW | data | M | I1-03, I0-13 | 2 |
| I1-05 | Descubrimiento de clientes en el ingester | FLOW | data | S | I1-04 | 2 |
| I1-06 | Registro de clientes: API, ciclo de vida, tipo manual y alias | CORE | backend, security | M | I1-05 (C4), I0-09 | 2 |
| I1-07 | Enriquecimiento ASN/organización y catálogo semilla | FLOW | data | M | I1-04, I0-17 | 2 |
| I1-08 | API de tráfico y datos de widgets | FLOW | data, backend | M | I1-07, I1-15 | 2 |
| I1-09 | Estado del exportador | FLOW | data | S | I1-03 | 2 |
| I1-10 | Motor de detección y contacto con C2 conocido | SEC | data, security | M | I1-04, I0-17 | 2 |
| I1-11 | Detectores de escaneo, fan-out, puertos vigilados, SMTP y DDoS | SEC | data, security | M | I1-10 | 2 |
| I1-12 | Hallazgos: ciclo de vida, API, eventos y retroalimentación | SEC | backend, security | M | I1-10 (C8) | 2 |
| I1-13 | Tiempo real: WebSocket con ámbito de ISP | CORE | backend, security | M | I0-08 (C6) | 2 |
| I1-14 | Kioscos: alta, código de enrolamiento y credencial de dispositivo | CORE | backend, security | M | I0-08 (C7) | 2 |
| I1-15 | Dashboards, playlists y catálogo de widgets en servidor | CORE | backend | M | I0-05 (C9) | 2 |
| I1-16 | UI Clientes: lista y detalle por IP | UI | frontend | M | I0-16, C5 | 2 |
| I1-17 | UI Tráfico: tops y series | UI | frontend | S | I0-16, C5 | 2 |
| I1-18 | UI Seguridad: hallazgos y detalle con evidencia | UI | frontend | M | I0-16, C5, C8 | 2 |
| I1-19 | UI Router: onboarding, prefijos de clientes y estado del exportador | UI | frontend | M | I0-15, C5 | 2 |
| I1-20 | Widgets de I1 y plantillas "NOC del ISP" y "Seguridad" | UI | frontend | M | I0-16, I1-15 | 3 |
| I1-21 | Modo kiosco en la UI | UI | frontend | M | I1-20, I1-14, I1-13 | 3 |
| I1-22 | Instalador de un servidor | PLAT | infra, security | M | I0-02 | 2 |
| I1-23 | Backup local y retención verificados | PLAT | infra | S | I0-13, I0-06 | 2 |
| I1-24 | `make accept-i1` con simulador y fixtures | INT | infra | M | I0-19 | 2–3 |
| I1-25 | Lista de validación técnica en laboratorio CHR | INT + FLOW | data, infra | M | I1-02, I1-04, I0-11 | 3 |
| I1-26 | Prueba de carga y pruebas de fallo de I1 | PLAT | infra, data | M | I1-04, I1-21 | 3 |
| I1-27 | Prueba con el MikroTik real de la persona | PERSONA (guía: INT) | — | S | todas | G1 |
| I1-28 | Importar pools y prefijos desde el MikroTik (solo lectura) | CORE | backend, security | M | I1-01, I1-02, I0-12 | 2 |
| I1-29 | Modo descubrimiento: propuestas de prefijos | FLOW | data | S | I1-04 | 2 |
| I1-30 | Detectores de beaconing y salida sostenida (*should*) | SEC | data, security | M | I1-11 | 3 |
| I1-31 | UI Consola de plataforma mínima | UI | frontend | S | I0-15 | 2 |

Camino crítico: I1-01 → I1-03 → I1-04 → I1-05/I1-10 → I1-11/I1-12 → I1-20 → I1-21 → I1-25 → I1-27.
Todas las historias son *must* salvo I1-30 (*should*).

---

## Conexión del router

### I1-01 · Hub WireGuard mínimo: IPAM de túnel y enrolamiento del router
- **Agente:** CORE · **Área:** backend, security · **Talla:** M · **Épica:** EP-07, EP-26
- **Depende de:** I0-09, I0-02

**Como** administrador del ISP **quiero** que mi router se conecte a Horus por un túnel cifrado
sin copiar claves a mano **para** que flujos y gestión no viajen en claro por Internet y Horus sepa
qué router envía cada flujo.

**Contexto:** [`../vendors/mikrotik.md`](../vendors/mikrotik.md) §5.1–5.2 y
[ADR-0022](../adr/0022-mikrotik-routeros-v7-primer-fabricante.md): el router es iniciador con
`persistent-keepalive=25s`; la clave privada se genera **en el router** y nunca sale; una `/32` por
router del rango de plataforma (nunca `100.64.0.0/10`), única en todo Horus; la IP de túnel es la
**identidad del exportador**. Alta de la clave pública por **`POST /api/v1/enroll/wireguard`** con
**token de enrolamiento** de un solo uso ([`../api.md`](../api.md) §2.4: 256 bits, solo su hash en BD,
ligado a tenant + router + peer previsto, TTL 24 h, revocable, rate limit, auditado). Módulos
`wireguard` y `wg-agent` ([ADR-0025](../adr/0025-binario-modular-con-roles.md)).
**Archivos:** `mod:wireguard`, `mod:wg-agent`.

**Criterios de aceptación**
1. **Dado** el evento de alta de router (I0-09), **cuando** lo consume `wireguard`, **entonces**
   asigna una IP de túnel libre y única globalmente; agotado el rango → `WIREGUARD_IP_POOL_EXHAUSTED`.
2. **Dado** un token válido y una clave pública WireGuard válida, **cuando** el router llama a
   `POST /enroll/wireguard`, **entonces** responde `202 {"peer_status": "pending_handshake"}`,
   `wg-agent` añade el peer al hub con `allowed-ips` = su `/32` en < 10 s y el token queda consumido.
3. **Dado** un token usado, caducado, revocado o de otro router, **cuando** se usa, **entonces**
   `ENROLLMENT_TOKEN_INVALID` sin revelar cuál de los casos es; 5 fallos invalidan el token.
4. **Dado** una clave pública ya registrada por otro router, **cuando** se envía, **entonces** se rechaza.
5. **Dado** un handshake, **cuando** `wg-agent` lo observa, **entonces** se publica el estado y el
   router muestra "Túnel activo · último handshake hace N s".
6. **Dado** que se reinicia `horus-wg-agent`, **cuando** vuelve, **entonces** reconstruye todos los
   peers desde el estado deseado (*fail-static*: no borra peers mientras el control no responde).

**Hecho cuando:** `make test-wireguard` (namespace de red y `wg` en CI) y `make test-tenancy` en verde.
**Fuera de alcance:** rotación de claves del router, segundo hub, preshared key (I3).

### I1-02 · Script de onboarding RouterOS, token de enrolamiento y script inverso
- **Agente:** CORE · **Área:** backend, security · **Talla:** M · **Épica:** EP-26
- **Depende de:** I1-01, I0-12 (script validado en CHR)

**Como** técnico del ISP **quiero** un script listo para pegar en mi MikroTik **para** conectarlo a
Horus con un solo pegado y poder deshacerlo.

**Contexto:** script, placeholders y notas operativas en
[`../vendors/mikrotik.md`](../vendors/mikrotik.md) §7, con la **opción B** de §5.2 (`/tool fetch` al
endpoint de enrolamiento con el token); todo con `comment="horus"`; IPFIX sin muestreo por el túnel;
usuario de solo lectura para la API (lo usa I1-28); SNMPv3; firewall solo desde el túnel; no tocar
parámetros globales de Traffic Flow sin avisar. RouterOS v7 ≥ 7.12. Contraseñas y token se muestran
**una sola vez** y se guardan cifradas o como hash ([`../security.md`](../security.md)).
**Horus no escribe en el router.**
**Archivos:** `mod:wireguard` (plantillas `.rsc` versionadas por versión de RouterOS).

**Criterios de aceptación**
1. **Dado** un router con IP de túnel asignada, **cuando** un usuario con `wireguard.write` pide el
   script, **entonces** recibe el `.rsc` con todos los placeholders resueltos (test que busca `<…>`)
   y un token de enrolamiento nuevo incluido en el `/tool fetch`.
2. **Dado** el script generado, **cuando** se compara con el fixture validado en CHR (I0-12) para
   7.12 y la última long-term, **entonces** solo difieren los valores de los placeholders.
3. **Dado** que se pide de nuevo, **cuando** ya hubo uno, **entonces** las contraseñas y el token no
   se vuelven a mostrar: "Regenerar" crea otros e invalida los anteriores, y queda auditado.
4. **Dado** el script inverso, **cuando** se genera, **entonces** elimina todo lo que lleva
   `comment="horus"` y solo el target de Traffic Flow de Horus.
5. **Dado** un `isp_viewer`, **cuando** pide el script, **entonces** `403`; otro ISP, `404`.
6. **Dado** RouterOS < 7.12 declarado, **cuando** pide el script, **entonces** `422` "RouterOS v7 ≥ 7.12 requerido".

**Hecho cuando:** `make test-onboarding-script` en verde; el script se pega en CHR sin errores (I1-25).

## Flujos, clientes y clasificación

### I1-03 · Colector IPFIX / NetFlow v9
- **Agente:** FLOW · **Área:** data · **Talla:** M · **Épica:** EP-10
- **Depende de:** I0-10, I0-12, I1-01 (mapa IP de túnel → router → ISP)

**Como** operador NOC **quiero** que Horus reciba los flujos del router de cada nodo **para**
saber qué tráfico pasa por él.

**Contexto:** [`../vendors/mikrotik.md`](../vendors/mikrotik.md) §2 (IPFIX preferente, v9 alternativa,
plantillas, `active=1m`, `inactive=15s`, limitaciones §2.7); [`../traffic-model.md`](../traffic-model.md)
(normalización); [`../events.md`](../events.md) (publicación de lotes); rol `collector` en su
propio contenedor ([ADR-0025](../adr/0025-binario-modular-con-roles.md)).
**Archivos:** `mod:collector`.

**Criterios de aceptación**
1. **Dado** los fixtures de I0-12 (IPFIX y v9, IPv4 e IPv6, ambas versiones de RouterOS),
   **cuando** se reproducen contra el colector, **entonces** decodifica el 100 % de los registros con
   los campos documentados.
2. **Dado** un datagrama desde una IP que no es una IP de túnel registrada, **cuando** llega,
   **entonces** se descarta, se cuenta en `horus_collector_dropped_total{reason="unknown_exporter"}`
   y no se publica nada.
3. **Dado** datos antes de recibir su plantilla, **cuando** llegan, **entonces** se retienen un tiempo
   acotado y se decodifican al llegar la plantilla, o se descartan con métrica.
4. **Dado** saltos en `sequenceNumber`, **cuando** se detectan, **entonces** se mide la pérdida por
   exportador; **dado** `exportTime` desfasado > 30 s, **entonces** se marca desfase de reloj.
5. **Dado** un paquete malformado, **cuando** llega, **entonces** se descarta sin pánico (fuzzing en CI).
6. **Dado** que el bus no acepta publicaciones, **cuando** dura < el búfer configurado, **entonces**
   no se pierden flujos; si se supera, se descartan con métrica (nunca se bloquea la recepción UDP).

**Hecho cuando:** `make test-collector` (replay de fixtures + fuzzing 60 s) en verde.

### I1-04 · Ingesta a ClickHouse con atribución por prefijos de clientes
- **Agente:** FLOW · **Área:** data · **Talla:** M · **Épica:** EP-10, EP-06
- **Depende de:** I1-03, I0-13

**Como** analista del ISP **quiero** que cada flujo quede guardado con su ISP, nodo, router, IP de
cliente e IP remota **para** consultar consumo por cliente.

**Contexto:** [`../traffic-model.md`](../traffic-model.md) §4 (atribución con `client_prefix` y sus
roles `customers` / `infrastructure` / `excluded`, realm, algoritmo en memoria, NAT en el router
principal, interfaces PPPoE dinámicas: atribuir por IP, nunca por interfaz), §4.6 (dirección),
[`../database.md`](../database.md) (tablas C3). Prefijos `excluded` se descartan ya en el colector.
**Archivos:** `mod:ingester`, `mod:collector` (filtro `excluded`).

**Criterios de aceptación**
1. **Dado** un flujo de subida (`src` en prefijo `customers`) y otro de bajada (`dst` en el prefijo),
   **cuando** se ingieren, **entonces** ambos quedan con la misma clave de cliente, dirección correcta
   y `tenant_id`/nodo/router del exportador.
2. **Dado** un flujo cuya IP local cae en `infrastructure`, **cuando** se ingiere, **entonces** queda
   con `attribution_status = infrastructure` y no crea cliente; en `excluded`, no se guarda.
3. **Dado** un nodo sin prefijos, **cuando** llegan flujos, **entonces** cuentan en los agregados del
   nodo y alimentan el modo descubrimiento (I1-29), sin crear clientes.
4. **Dado** la misma `10.0.0.5` en dos nodos o dos ISP, **cuando** se ingiere, **entonces** son
   clientes distintos (realm distinto).
5. **Dado** lotes del simulador a 5 000 flujos/s, **cuando** se ingieren 10 min, **entonces** no hay
   lag creciente del consumidor, los inserts son por lotes y un lote reentregado no duplica filas.
6. **Dado** que ClickHouse no responde, **cuando** dura menos que la retención del stream,
   **entonces** al volver se ingiere todo sin pérdida.

**Hecho cuando:** `make test-ingester` (ClickHouse y NATS efímeros + replay de escenarios) en verde.

### I1-05 · Descubrimiento de clientes en el ingester
- **Agente:** FLOW · **Área:** data · **Talla:** S · **Épica:** EP-06 · **Depende de:** I1-04

**Como** operador NOC **quiero** que cada IP de cliente vista aparezca sola como cliente **para** no
tener que dar de alta clientes (D1).

**Contexto:** [ADR-0018](../adr/0018-la-ip-es-el-cliente.md), [`../traffic-model.md`](../traffic-model.md)
§4.2 y §4.5: clave `(tenant, realm, dirección canónica)` (IPv6 truncada a `/64` por defecto); el
ingester publica `horus.flows.client.first_seen.<realm_id>` en lotes deduplicados cada 10 s y
`client.activity_summary` cada hora; `devices` crea el cliente (I1-06). Los flujos no esperan.
**Archivos:** `mod:ingester`.

**Criterios de aceptación**
1. **Dado** el escenario `normal` con 300 clientes, **cuando** pasan 10 s, **entonces** los lotes
   `first_seen` cubren exactamente esas 300 claves, una vez cada una.
2. **Dado** un cliente ya conocido, **cuando** vuelve a verse, **entonces** no se emite `first_seen`
   y sí aparece en el `activity_summary` horario.
3. **Dado** IPv6 de un mismo `/64`, **cuando** se ven varias direcciones, **entonces** es un solo cliente.

**Hecho cuando:** `make test-ingester SUITE=discovery` en verde.

### I1-06 · Registro de clientes: API, ciclo de vida, tipo manual y alias
- **Agente:** CORE · **Área:** backend, security · **Talla:** M · **Épica:** EP-06, EP-16
- **Depende de:** I1-05 (C4), I0-09

**Como** operador NOC **quiero** ver los clientes descubiertos, marcar una IP como comercial, ponerle
un alias o reiniciarla **para** reflejar lo que sé de mi red.

**Contexto:** [`../database.md`](../database.md) §2.3 (tabla `customer`: `kind`, `kind_source`,
`kind_locked`, `status`, `alias`, `reset_at`; historial inmutable `customer_kind_change`; ciclo de
vida §2.3.4: `inactive` tras 30 días sin tráfico — configurable por ISP —, reactivación, **purga** a
los 25 meses, `reset` manual); [`../api.md`](../api.md) §2.9 (`set-kind`, `unlock-kind`, `reset` con
`If-Match`, `GET /customers/stats`); permisos `customers.read` / `customers.manage` (C7). Acceso al
detalle auditado. La IP nunca va en la URL.
**Archivos:** `mod:devices`.

**Criterios de aceptación**
1. **Dado** un lote `first_seen`, **cuando** se consume, **entonces** se crea el cliente con el tipo
   por defecto del prefijo (`residential`, `kind_source = default`) de forma idempotente y se emite
   `customer.discovered`.
2. **Dado** un `isp_operator`, **cuando** hace `set-kind commercial` con motivo, **entonces**
   `kind_source = manual`, `kind_locked = true`, entrada en el historial con autor y evento
   `customer.kind_changed`; sin motivo → `422`; con `If-Match` desfasado → `412` con el valor actual.
3. **Dado** un cliente sin tráfico durante el umbral de inactividad, **cuando** corre el job diario
   (reloj simulado), **entonces** pasa a `inactive` (evento) y vuelve a `active` al reaparecer en un
   `activity_summary`.
4. **Dado** un cliente sin tráfico más allá de la retención (25 meses, reloj simulado), **cuando**
   corre la purga, **entonces** se borran el cliente y su historial y se emite `customer.purged`; si
   la IP reaparece, es un cliente nuevo.
5. **Dado** `reset` sobre un cliente, **cuando** se ejecuta, **entonces** el tipo vuelve al defecto,
   se borran alias y notas, `reset_at = now()` y queda la entrada `reset` en el historial.
6. **Dado** un `isp_viewer`, **cuando** intenta `set-kind` o `reset`, **entonces** `403`; un
   cliente de otro ISP → `404`.
7. **Dado** la lista, **cuando** filtro por nodo, tipo, origen, estado o "con hallazgos", **entonces**
   la paginación por cursor devuelve los correctos y `GET /customers/stats` cuadra con la lista.

**Hecho cuando:** `make test-devices SUITE=customers` y `make test-tenancy` en verde.

### I1-07 · Enriquecimiento ASN/organización y catálogo semilla
- **Agente:** FLOW · **Área:** data · **Talla:** M · **Épica:** EP-11, EP-12
- **Depende de:** I1-04, I0-17

**Como** ingeniero de red **quiero** ver el tráfico por ASN, organización, servicio y categoría
**para** entender qué consumen mis clientes.

**Contexto:** [`../traffic-model.md`](../traffic-model.md) (cadena IP → prefijo → ASN → organización
→ servicio → categoría, snapshot versionado cargado en memoria por el ingester,
[ADR-0015](../adr/0015-enriquecimiento-de-flujos-en-ingesta.md)); dataset de I0-17; catálogo
semilla de ≈ 30 servicios y ≈ 10 categorías (EP-12). Sin editor (I4).
**Archivos:** `mod:traffic` (catálogo semilla y snapshot), `mod:ingester` (aplicación).

**Criterios de aceptación**
1. **Dado** los escenarios del simulador, **cuando** se ingieren, **entonces** ≥ 95 % de los bytes
   remotos tienen ASN y los destinos de servicios conocidos del escenario quedan con su servicio y
   categoría esperados.
2. **Dado** un snapshot nuevo del catálogo, **cuando** se publica, **entonces** el ingester lo carga
   sin reiniciar y cada fila guarda la versión del catálogo usada.
3. **Dado** un snapshot corrupto, **cuando** se publica, **entonces** se rechaza y sigue el anterior.
4. **Dado** la métrica de cobertura, **cuando** se consulta, **entonces** informa % de bytes con ASN
   y con servicio por ISP.

**Hecho cuando:** `make test-ingester SUITE=enrichment` en verde.

### I1-08 · API de tráfico y datos de widgets
- **Agente:** FLOW · **Área:** data, backend · **Talla:** M · **Épica:** EP-15
- **Depende de:** I1-07

**Como** operador NOC **quiero** consultar quién y qué consume más **para** detectar saturación y
consumo anómalo.

**Contexto:** módulo `analytics` ([ADR-0025](../adr/0025-binario-modular-con-roles.md)); consultas
sobre agregados (C3) eligiendo granularidad según el rango; `meta.partial` cuando falta cobertura;
**datos por widget resueltos en servidor** (`GET /dashboards/{id}/widgets/{wid}/data`, con los
permisos de quien mira, caché en Valkey por tenant/tipo/config/rango/alcance, timeout 10 s, sin IPs
de clientes para kioscos sin `show_personal_data`) según [`../api.md`](../api.md) §2.11; toda
consulta filtra por `tenant_id` (test de arquitectura).
**Archivos:** `mod:analytics` (salvo `dashboards`).

**Criterios de aceptación**
1. **Dado** un rango y un ámbito (ISP, nodo o cliente), **cuando** pido top N de clientes, servicios,
   categorías u organizaciones/ASN, **entonces** devuelve N filas + "Otros" con bytes de subida y
   bajada, coherentes con los totales del escenario (± 1 %).
2. **Dado** una serie temporal de 24 h, **cuando** se pide, **entonces** usa agregados de 5 min y
   responde < 500 ms p95 con 30 días de datos simulados de un nodo de 2 000 clientes.
3. **Dado** un hueco de datos (exportador silencioso), **cuando** se pide la serie, **entonces** el
   hueco viene como ausencia (no cero) y `meta.partial = true`.
4. **Dado** ClickHouse caído, **cuando** se pide, **entonces** `503` con el código de "analítica no
   disponible" de `api.md`, y el resto de la API sigue respondiendo.
5. **Dado** un usuario de otro ISP, **cuando** pide el top de este ISP, **entonces** `404`.
6. **Dado** diez kioscos con el mismo dashboard, **cuando** piden los datos de un widget en la misma
   ventana, **entonces** ClickHouse recibe una sola consulta (caché).
7. **Dado** un kiosco sin `show_personal_data`, **cuando** pide `top_customers` o `findings_feed`,
   **entonces** la respuesta trae alias o IP enmascarada, nunca la IP completa.

**Hecho cuando:** `make test-analytics` en verde con presupuesto de latencia verificado.

### I1-09 · Estado del exportador
- **Agente:** FLOW · **Área:** data · **Talla:** S · **Épica:** EP-10, EP-09 · **Depende de:** I1-03

**Como** operador NOC **quiero** saber si cada router está enviando flujos **para** no confundir
"no hay tráfico" con "no recibimos datos".

**Contexto:** estados *Pendiente de configurar*, *Exportando*, *Silencioso* (sin flujos > 2 min),
*Con pérdidas* (pérdida por secuencia > 1 %), *Reloj desfasado*; evento de cambio de estado (C4);
principio "ausencia de datos ≠ cero" ([`../architecture.md`](../architecture.md)).
**Archivos:** `mod:collector` (métricas), `mod:ingester` o `mod:analytics` según C1 (estado).

**Criterios de aceptación**
1. **Dado** un router que empieza a exportar, **cuando** llega el primer lote válido, **entonces**
   pasa a *Exportando* en ≤ 30 s y se publica el evento.
2. **Dado** que deja de exportar, **cuando** pasan 2 min, **entonces** pasa a *Silencioso* (una
   sola vez, con histéresis para no oscilar).
3. **Dado** pérdida por secuencia > 1 % en 5 min, **cuando** se evalúa, **entonces** pasa a *Con pérdidas*.

**Hecho cuando:** `make test-collector SUITE=exporter-state` en verde.

## Detección de botnets

Las señales, umbrales iniciales y falsos positivos conocidos son los de
[`../traffic-model.md`](../traffic-model.md) §8 (fuente única; aquí no se repiten), configurables por
ISP y ajustados con el laboratorio (I1-25) y el router real (I1-27). Tipos de hallazgo (`kind`) y
estados según [`../api.md`](../api.md) §2.10. Con `active-flow-timeout=1m` la resolución temporal
efectiva es de un minuto. Lenguaje: "señales compatibles con…", **nunca** "infectado"
([ADR-0024](../adr/0024-deteccion-de-botnets-como-objetivo-principal.md)). Mitigación activa: fuera de v1.

### I1-10 · Motor de detección y contacto con C2 conocido
- **Agente:** SEC · **Área:** data, security · **Talla:** M · **Épica:** EP-13, EP-14
- **Depende de:** I1-04, I0-17

**Como** analista de seguridad del ISP **quiero** saber qué clientes contactan con servidores de
mando y control conocidos **para** avisarles antes de que su IP acabe en listas negras (D5).

**Contexto:** [ADR-0024](../adr/0024-deteccion-de-botnets-como-objetivo-principal.md);
[`../traffic-model.md`](../traffic-model.md) §8 (fila "Contacto con C2 conocido": marca en ingesta
`flows.reputation_hit` + barrido retroactivo sobre `flows_raw` de 7 días) y §11 (reputación en el
pipeline); feeds de I0-17; salida = hallazgos C8 (`botnet_c2_communication`, `reputation_hit`). El
motor es extensible: un detector = nombre, parámetros por ISP, señales, confianza y razones (`reasons[]`).
**Archivos:** `mod:detection` (motor y detector), `mod:ingester` (marca en ingesta: PR revisado por FLOW).

**Criterios de aceptación**
1. **Dado** el escenario `c2`, **cuando** se evalúa, **entonces** hay un hallazgo
   `botnet_c2_communication` para la IP esperada, con severidad alta si hubo respuesta y media si
   solo SYN, y razones con IP remota, feed, fecha de inclusión y nº de conexiones.
2. **Dado** un indicador que entra en un feed hoy y tráfico de hace 3 días hacia él, **cuando** corre
   el barrido retroactivo, **entonces** se genera el hallazgo con la ventana real.
3. **Dado** el escenario `normal`, **cuando** se evalúa, **entonces** no hay ningún hallazgo.
4. **Dado** un prefijo en la allowlist del ISP, **cuando** coincide, **entonces** no genera hallazgo.

**Hecho cuando:** `make test-detection SUITE=c2` y `make accept-i1 SCENARIO=c2,normal` en verde.

### I1-11 · Detectores de escaneo, fan-out, puertos vigilados, SMTP y DDoS
- **Agente:** SEC · **Área:** data, security · **Talla:** M · **Épica:** EP-14 · **Depende de:** I1-10

**Como** analista de seguridad del ISP **quiero** detectar comportamientos típicos de equipos
reclutados en botnets **para** encontrar clientes comprometidos aunque su C2 no esté en ninguna lista.

**Contexto:** filas de [`../traffic-model.md`](../traffic-model.md) §8: **Escaneo**, **Fan-out**
(dispersión por /24 para descartar CDN), **Puertos típicos de botnet** (lista `dim.watch_port`
editable), **SMTP saliente directo**, **Participación en DDoS**; agregados `client_security_1m`,
`client_port_1m`, `watch_port_flows`. `kind`: `outbound_scanning`, `spam_smtp_outbound`,
`ddos_participation`. Línea base por cliente cuando exista; en I1, umbrales absolutos de §8.
**Archivos:** `mod:detection`.

**Criterios de aceptación**
1. **Dado** los escenarios `scan`, `spam` y `dos_out`, **cuando** se evalúan, **entonces** cada uno
   produce exactamente los hallazgos de su `expected.json`, con ≥ 2 razones con dato (puerto, nº de
   destinos y de redes /24, pps, ventana).
2. **Dado** el escenario `normal` y el `commercial` (servidor legítimo con muchas conexiones
   entrantes y tráfico a CDN), **cuando** se evalúan, **entonces** no hay hallazgos de estos detectores.
3. **Dado** un cliente `commercial`, **cuando** se evalúa SMTP, **entonces** usa el umbral de comerciales.
4. **Dado** un exportador con muestreo declarado, **cuando** se evalúa, **entonces** la confianza
   baja un nivel y las razones lo indican.

**Hecho cuando:** `make test-detection` y `make accept-i1 SCENARIO=scan,spam,dos_out,commercial,normal` en verde.

### I1-12 · Hallazgos: ciclo de vida, API, eventos y retroalimentación
- **Agente:** SEC · **Área:** backend, security · **Talla:** M · **Épica:** EP-14
- **Depende de:** I1-10 (C8)

**Como** analista de seguridad del ISP **quiero** gestionar cada hallazgo de principio a fin **para**
saber qué está pendiente y enseñar a Horus qué no es un problema.

**Contexto:** [`../api.md`](../api.md) §2.10: estados `open` → `acknowledged` → `resolved`, o
`false_positive` (comentario obligatorio, alimenta `verdict_feedback`); `GET /findings`,
`GET /findings/{id}` con `reasons[]`, `GET /findings/{id}/evidence` (auditado,
`security.evidence.read`), `GET /security/summary` (widgets), allowlist del ISP; eventos
`horus.detection.finding.{opened,updated,resolved}`. Deduplicación: un hallazgo abierto por
(ISP, cliente, `kind`, objetivo principal) que se actualiza con nuevas ocurrencias. El
`security_state` del cliente se actualiza para la lista de clientes.
**Archivos:** `mod:detection`.

**Criterios de aceptación**
1. **Dado** ocurrencias repetidas del mismo patrón, **cuando** se detectan, **entonces** se actualiza
   el hallazgo abierto (contador, última vez, razones) y se emite `finding.updated`, no uno nuevo.
2. **Dado** `mark-false-positive` con comentario, **cuando** el patrón se repite en el periodo de
   silencio configurado, **entonces** no se reabre; sin comentario → `422`.
3. **Dado** un hallazgo `resolved`, **cuando** vuelve el patrón, **entonces** se abre uno nuevo
   enlazado al anterior ("reincidente").
4. **Dado** un `isp_viewer`, **cuando** intenta cambiar el estado, **entonces** `403`; sin
   `security.evidence.read`, la evidencia → `403`; otro ISP → `404`.
5. **Dado** `GET /security/summary`, **cuando** se pide, **entonces** devuelve conteos por señal,
   `kind` y nodo coherentes con la lista (base de los widgets `botnet_signals`, `security_by_node`).

**Hecho cuando:** `make test-detection SUITE=findings` y `make test-tenancy` en verde.

## Tiempo real, kiosco y dashboards (backend)

### I1-13 · Tiempo real: WebSocket con ámbito de ISP
- **Agente:** CORE · **Área:** backend, security · **Talla:** M · **Épica:** EP-T5, EP-09
- **Depende de:** I0-08 (C6)

**Como** operador NOC **quiero** que el dashboard se actualice solo **para** ver cambios sin recargar.

**Contexto:** protocolo WS de [`../api.md`](../api.md) (ticket de un solo uso, sobre de mensaje,
suscripción por tema, reanudación); temas de I1: resumen de tráfico del ISP/nodo, hallazgos,
estado de exportadores, clientes nuevos (contador). Fan-out desde NATS filtrando por ISP y permisos.
**Archivos:** `mod:gateway`.

**Criterios de aceptación**
1. **Dado** un usuario del ISP A suscrito al tema de hallazgos, **cuando** se abre un hallazgo en A,
   **entonces** lo recibe en < 2 s; **cuando** se abre en B, **entonces** no recibe nada.
2. **Dado** una suscripción a un tema de otro ISP, **cuando** se pide, **entonces** se rechaza sin
   revelar si el ISP existe.
3. **Dado** una reconexión con el último ID recibido, **cuando** se reanuda dentro de la ventana,
   **entonces** recibe lo perdido; fuera de la ventana, recibe la orden de pedir un snapshot REST.
4. **Dado** un token de kiosco (I1-14), **cuando** se conecta, **entonces** solo puede suscribirse
   a los temas de los dashboards de su lista de reproducción.

**Hecho cuando:** `make test-gateway SUITE=ws` y `make test-tenancy` en verde.

### I1-14 · Kioscos: alta, código de enrolamiento y credencial de dispositivo
- **Agente:** CORE · **Área:** backend, security · **Talla:** M · **Épica:** EP-25
- **Depende de:** I0-08 (C7)

**Como** administrador del ISP **quiero** dar de alta una TV del NOC sin dejar una sesión de
usuario abierta en ella **para** que esté siempre encendida sin riesgo.

**Contexto:** [`../api.md`](../api.md) §2.12 (el kiosco es un dispositivo: `POST /kiosks`, código de
8 caracteres + QR de un uso y 10 min, `POST /kiosk/enroll`, cookie `__Secure-hf_kiosk` rotativa,
JWT de kiosco de 10 min, `GET /kiosk/config`, revocación con cierre WS `4409`, `allowed_cidrs`,
`show_personal_data` falso por defecto, caducidad 180 días); [`../security.md`](../security.md) §5.5.
**Archivos:** `mod:auth` (kioscos y credenciales), `mod:gateway` (lista blanca `principal: kiosk`).

**Criterios de aceptación**
1. **Dado** un `isp_admin` con `kiosks.manage`, **cuando** crea un kiosco y genera un código,
   **entonces** obtiene código + QR válidos 10 min y de un solo uso (`Idempotency-Key` obligatorio).
2. **Dado** el código, **cuando** la TV lo canjea en `POST /kiosk/enroll`, **entonces** recibe la
   cookie de dispositivo; un segundo canje del mismo código → error; 10 fallos invalidan el código.
3. **Dado** un JWT de kiosco, **cuando** pide algo fuera de su lista blanca (escritura, `/me`, listas,
   exportaciones, `preview`, otro tenant), **entonces** `403 KIOSK_FORBIDDEN`.
4. **Dado** `allowed_cidrs`, **cuando** llega una petición desde otra IP (incluido el canje),
   **entonces** se rechaza.
5. **Dado** una revocación, **cuando** ocurre, **entonces** la credencial deja de valer y el
   WebSocket se cierra con `4409` en < 5 s.
6. **Dado** la reutilización de una cookie ya rotada, **cuando** se detecta, **entonces** se revoca
   el kiosco y se notifica al administrador.

**Hecho cuando:** `make test-auth SUITE=kiosk` y `make test-tenancy` en verde.

### I1-15 · Dashboards, playlists y catálogo de widgets en servidor
- **Agente:** CORE · **Área:** backend · **Talla:** M · **Épica:** EP-24 · **Depende de:** I0-05 (C9)

**Como** operador NOC **quiero** que los dashboards existan en el servidor **para** que la misma
plantilla se vea igual en mi navegador y en la TV.

**Contexto:** [`../api.md`](../api.md) §2.11 (documento de dashboard con `version`/`If-Match`,
`visibility`, `layout`, `widgets[]`; `GET /widget-types`; playlists; duplicar);
[`../frontend.md`](../frontend.md) §6.2–§6.5 (catálogo y plantillas de I1). En I1 se usan las
plantillas semilla y las playlists de kiosco; la edición libre llega en I2, pero los endpoints
CRUD se entregan ya (los usa el editor de I2). Los **datos** de cada widget los resuelve I1-08.
**Archivos:** `mod:analytics/dashboards`.

**Criterios de aceptación**
1. **Dado** el arranque, **cuando** se migra, **entonces** existen las plantillas "NOC del ISP" y
   "Seguridad" (versionadas) y cada `config` valida contra el `config_schema` de su tipo.
2. **Dado** `GET /widget-types`, **cuando** se pide, **entonces** lista los tipos de I1 de
   `frontend.md` §6.2 con `required_permission`, `contains_personal_data` y `kiosk_allowed`.
3. **Dado** un widget con tipo desconocido o `config` inválida, **cuando** se guarda, **entonces** `422`.
4. **Dado** una playlist con duraciones, **cuando** se asigna a un kiosco, **entonces** `GET /kiosk/config`
   la devuelve y un cambio emite el evento `dashboard`/`playlist` para que la TV la aplique sola.
5. **Dado** un dashboard de otro ISP, **cuando** se pide, **entonces** `404`.

**Hecho cuando:** `make test-analytics SUITE=dashboards` en verde.

## Interfaz

Todas las historias de UI cumplen [`../frontend.md`](../frontend.md) §9 (estados), §12 (permisos),
§13–§14 (visual y accesibilidad) y adjuntan capturas claro/oscuro. Trabajan contra mocks de C5 hasta
que el backend está en `main`, y la historia no se cierra hasta pasar contra el backend real.

### I1-16 · UI Clientes: lista y detalle por IP
- **Agente:** UI · **Área:** frontend · **Talla:** M · **Épica:** EP-06, EP-16
- **Depende de:** I0-16, C5

**Como** operador NOC **quiero** ver los clientes de mi ISP por IP con su tipo, estado y consumo
**para** encontrar rápido a quién mirar.

**Contexto:** [`../frontend.md`](../frontend.md) §8.1 (lista, detalle, tipo con origen y candado,
ciclo de vida activo/inactivo, "Reiniciar cliente", privacidad: la IP nunca en la URL).
**Archivos:** `apps/frontend/app/pages/t/[slug]/clients/`, `apps/frontend/app/components/clients/`.

**Criterios de aceptación**
1. **Dado** la lista, **cuando** cargo, **entonces** veo IP (monoespaciada) o alias, nodo, tipo con
   su origen, estado, tráfico 24 h ↓↑, principal, hallazgos abiertos y última vez visto.
2. **Dado** una búsqueda por IP, **cuando** la escribo, **entonces** se busca por `POST` y la URL no
   contiene la IP.
3. **Dado** el detalle, **cuando** cambio el tipo a Comercial, **entonces** se pide el motivo, el tipo
   muestra el candado de manual, el historial lo refleja y un toast confirma "Tipo actualizado"; ante
   `412`, la UI muestra el valor actual y permite reintentar.
4. **Dado** "Reiniciar cliente", **cuando** lo confirmo, **entonces** alias y notas desaparecen, el tipo
   vuelve al defecto y el historial muestra el reinicio.
5. **Dado** un cliente *Inactivo*, **cuando** abro su ficha, **entonces** se explica desde cuándo y por qué.
6. **Dado** un `isp_viewer`, **cuando** abre el detalle, **entonces** no ve "Cambiar tipo" ni "Reiniciar".

**Hecho cuando:** `pnpm -C apps/frontend e2e --grep @clients` en verde (mocks y backend real).

### I1-17 · UI Tráfico: tops y series
- **Agente:** UI · **Área:** frontend · **Talla:** S · **Épica:** EP-15 · **Depende de:** I0-16, C5

**Como** ingeniero de red **quiero** ver tops de clientes, servicios, categorías y organizaciones
con un rango de tiempo **para** entender el consumo de mi red.

**Contexto:** [`../frontend.md`](../frontend.md) §8.3 y reglas de gráficos (top N + "Otros", huecos
no ceros, `meta.partial`); reutiliza los widgets de I1-20.
**Archivos:** `apps/frontend/app/pages/t/[slug]/traffic/`.

**Criterios de aceptación**
1. **Dado** un rango y un nodo, **cuando** cambio cualquiera, **entonces** todos los gráficos se
   recalculan y el estado queda en la URL (sin IPs de clientes).
2. **Dado** `meta.partial`, **cuando** llega, **entonces** se muestra "Datos incompletos en este rango".
3. **Dado** analítica no disponible (`503`), **cuando** ocurre, **entonces** se muestra el aviso de
   degradado y el resto de la app funciona.

**Hecho cuando:** `pnpm -C apps/frontend e2e --grep @traffic` en verde.

### I1-18 · UI Seguridad: hallazgos y detalle con evidencia
- **Agente:** UI · **Área:** frontend · **Talla:** M · **Épica:** EP-14
- **Depende de:** I0-16, C5, C8

**Como** analista de seguridad del ISP **quiero** revisar cada hallazgo con su evidencia y decidir
**para** actuar con confianza y enseñar a Horus qué es ruido.

**Contexto:** [`../frontend.md`](../frontend.md) §8.2 (lista, detalle, narrativa, evidencia, acciones,
lenguaje no acusatorio).
**Archivos:** `apps/frontend/app/pages/t/[slug]/security/`, `apps/frontend/app/components/findings/`.

**Criterios de aceptación**
1. **Dado** la lista, **cuando** cargo, **entonces** veo severidad (icono + texto + color),
   resumen legible, cliente, nodo, tipo de hallazgo, confianza, estado (*Abierto*, *Reconocido*,
   *Resuelto*, *Falso positivo*) y última vez, ordenado por severidad y recencia.
2. **Dado** el detalle, **cuando** lo abro, **entonces** veo la narrativa ("Por qué lo marcamos"),
   ≥ 2 razones con dato, la línea de tiempo y las acciones *Reconocer*, *Resolver*, *Falso positivo*;
   "Ver flujos de evidencia" solo aparece con `security.evidence.read`.
3. **Dado** "Falso positivo", **cuando** lo pulso, **entonces** pide comentario obligatorio y explica
   qué hará Horus con él.
4. **Dado** un hallazgo nuevo por WebSocket, **cuando** llega, **entonces** aparece arriba sin mover
   el scroll y se anuncia en la región `aria-live` de forma agregada.

**Hecho cuando:** `pnpm -C apps/frontend e2e --grep @security` en verde.

### I1-19 · UI Router: onboarding, prefijos de clientes y estado del exportador
- **Agente:** UI · **Área:** frontend · **Talla:** M · **Épica:** EP-26, EP-05
- **Depende de:** I0-15, C5

**Como** administrador del ISP **quiero** conectar mi MikroTik y declarar qué IPs son de clientes
desde su ficha **para** empezar a ver datos sin leer documentación.

**Contexto:** [`../frontend.md`](../frontend.md) §8.4 (onboarding en tres pasos con enrolamiento
automático por token; pestaña "Prefijos de clientes" con roles, importación desde el MikroTik y
propuestas del modo descubrimiento).
**Archivos:** `apps/frontend/app/pages/t/[slug]/nodes/`, `.../routers/`,
`apps/frontend/app/components/onboarding/`, `apps/frontend/app/components/prefixes/`.

**Criterios de aceptación**
1. **Dado** un router "Pendiente de configurar", **cuando** abro su ficha, **entonces** veo los tres
   pasos con su estado y "Generar script"; el script se muestra con "Copiar", "Descargar .rsc" y el
   aviso de que contraseñas y token no se volverán a mostrar.
2. **Dado** que el router se enrola, hace handshake y envía flujos, **cuando** ocurre, **entonces**
   los pasos se marcan en vivo sin recargar; si el token caduca, se ofrece regenerarlo.
3. **Dado** la pestaña de prefijos, **cuando** añado uno que solapa, **entonces** el error aparece en
   línea (`CLIENT_PREFIX_OVERLAP`); puedo elegir rol *Clientes*, *Infraestructura* o *Excluido*.
4. **Dado** "Importar del MikroTik", **cuando** llega la lista (I1-28), **entonces** la reviso con
   casillas y rol propuesto y nada se aplica sin "Confirmar".
5. **Dado** un nodo sin prefijos, **cuando** abro la ficha, **entonces** veo el banner de modo
   descubrimiento y las propuestas (I1-29) con "Aceptar como Clientes", "Infraestructura" o "Excluir".
6. **Dado** un router *Silencioso*, **cuando** abro su ficha, **entonces** veo desde cuándo y qué revisar.

**Hecho cuando:** `pnpm -C apps/frontend e2e --grep @onboarding` en verde.

### I1-20 · Widgets de I1 y plantillas "NOC del ISP" y "Seguridad"
- **Agente:** UI · **Área:** frontend · **Talla:** M · **Épica:** EP-24 · **Depende de:** I0-16, I1-15

**Como** operador NOC **quiero** un dashboard listo para la pantalla del NOC **para** ver de un
vistazo el estado de tráfico y seguridad de mi ISP.

**Contexto:** catálogo de widgets marcados "I1" en [`../frontend.md`](../frontend.md) §6.2 (incluidos
los de seguridad para el modo NOC: `findings_summary`, `botnet_signals`, `findings_feed`,
`findings_trend`, `security_by_node`, `watched_ports`) y plantillas de §6.5; datos por
`GET /dashboards/{id}/widgets/{wid}/data` (I1-08); cada widget funciona en escala normal y mural (§7.4).
**Archivos:** `apps/frontend/app/widgets/*`, plantillas en el seed de I1-15 (PR a CORE).

**Criterios de aceptación**
1. **Dado** la plantilla "NOC del ISP", **cuando** se abre con datos del simulador, **entonces**
   muestra los widgets de §6.5 con datos reales y su frescura.
2. **Dado** un widget cuya fuente falla, **cuando** se renderiza, **entonces** solo él muestra error.
3. **Dado** escala mural a 1920×1080, **cuando** se captura, **entonces** cumple los tamaños mínimos
   de §7.4 (verificado con un test que mide el tamaño de fuente calculado de valores y etiquetas).
4. **Dado** la plantilla "Seguridad" con el escenario `scan`, **cuando** se abre, **entonces**
   `botnet_signals` y `watched_ports` muestran la señal y el puerto esperados, hablando de "clientes
   con señales", nunca de "infectados".
5. **Dado** un kiosco sin datos personales, **cuando** muestra `top_customers`, **entonces** ve alias
   o IP enmascarada.

**Hecho cuando:** `pnpm -C apps/frontend e2e --grep @dashboards` en verde con capturas normal y mural.

### I1-21 · Modo kiosco en la UI
- **Agente:** UI · **Área:** frontend · **Talla:** M · **Épica:** EP-25
- **Depende de:** I1-20, I1-14, I1-13

**Como** pantalla NOC **quiero** mostrar dashboards indefinidamente sin que nadie la toque **para**
que el equipo vea el estado de la red en todo momento.

**Contexto:** [`../frontend.md`](../frontend.md) §7 completo (enrolamiento por código, pantalla completa,
rotación, autorrefresco, legibilidad, tema oscuro, sin interacción, recuperación, actualización de
versión, protección de pantalla).
**Archivos:** `apps/frontend/app/pages/kiosk/`, `apps/frontend/app/layouts/kiosk.vue`,
`apps/frontend/app/composables/useKiosk.ts`.

**Criterios de aceptación**
1. **Dado** una TV en `/kiosk` sin credencial, **cuando** introduzco el código generado por el admin,
   **entonces** queda enrolada; con un código erróneo veo un mensaje legible a distancia.
2. **Dado** una TV enrolada, **cuando** arranca, **entonces** muestra el primer dashboard en tema
   oscuro, sin barra lateral ni controles, y rota según la playlist.
3. **Dado** un corte de red de 5 min, **cuando** ocurre, **entonces** mantiene los últimos datos con
   "Sin conexión desde HH:MM" bien visible y se recupera sola al volver, sin recargar a mano.
4. **Dado** un reinicio del servidor, **cuando** vuelve, **entonces** la TV reconecta y, si la versión
   del frontend cambió, se recarga sola en el siguiente cambio de dashboard.
5. **Dado** 24 h de ejecución simulada (Playwright con reloj acelerado), **cuando** termina,
   **entonces** la memoria del navegador no crece sin límite y no hay errores no capturados.
6. **Dado** una revocación (cierre `4409`), **cuando** ocurre, **entonces** vuelve a la pantalla de
   código en < 1 min.
7. **Dado** `prefers-reduced-motion`, **cuando** rota, **entonces** el cambio es un fundido corto o
   instantáneo.

**Hecho cuando:** `pnpm -C apps/frontend e2e --grep @kiosk` (incluye la prueba de 24 h acelerada)
en verde.

### I1-28 · Importar pools y prefijos desde el MikroTik (solo lectura)
- **Agente:** CORE · **Área:** backend, security · **Talla:** M · **Épica:** EP-26, EP-05
- **Depende de:** I1-01, I1-02, I0-12 (fixtures JSON de REST)

**Como** administrador del ISP **quiero** traer los pools de mi MikroTik como prefijos de clientes
**para** no teclearlos y no equivocarme.

**Contexto:** [`../traffic-model.md`](../traffic-model.md) §4.1 (cómo se llenan los `client_prefix`),
[`../vendors/mikrotik.md`](../vendors/mikrotik.md) §4 (REST por el túnel, usuario de solo lectura con
política mínima, TLS con huella fijada en el primer contacto). Lee `/ip pool`, `/ipv6 pool` y las
direcciones de interfaces de cara al cliente; **no escribe nada**. Credenciales del usuario de API
generadas por I1-02, guardadas cifradas.
**Archivos:** `mod:devices` (adaptador RouterOS de lectura y propuestas de prefijos).

**Criterios de aceptación**
1. **Dado** los fixtures REST de 7.12 y de la última long-term, **cuando** se ejecuta la importación,
   **entonces** devuelve una propuesta con cada pool/red, rol sugerido (pools → *Clientes*, redes de
   gestión → *Infraestructura*) y diferencias con los prefijos existentes.
2. **Dado** la propuesta, **cuando** el admin confirma una selección, **entonces** solo esas entradas
   se crean (origen `imported`) y se publica `client_prefix.*`.
3. **Dado** que el router no responde o la huella TLS cambió, **cuando** se importa, **entonces**
   error claro sin aplicar nada; cambio de huella → requiere confirmación explícita y queda auditado.
4. **Dado** cualquier llamada al router, **cuando** se revisa el adaptador, **entonces** solo usa
   métodos de lectura (test que falla ante cualquier `PUT`/`PATCH`/`POST`/`DELETE` hacia RouterOS).

**Hecho cuando:** `make test-devices SUITE=routeros-import` (fixtures) en verde; prueba en CHR en I1-25.

### I1-29 · Modo descubrimiento: propuestas de prefijos para nodos sin prefijos
- **Agente:** FLOW · **Área:** data · **Talla:** S · **Épica:** EP-06 · **Depende de:** I1-04

**Como** administrador del ISP **quiero** que Horus me proponga qué rangos son de clientes cuando no
los he declarado **para** empezar a ver clientes cuanto antes.

**Contexto:** [`../traffic-model.md`](../traffic-model.md) §4.1 punto 3: el ingester registra en
`flows.unattributed_1h` las IPs privadas/CGNAT y las públicas del ASN del ISP vistas del lado
`customer_edge`; se proponen prefijos agregados (p. ej. `/24`) para confirmar. Hasta confirmar, el
tráfico cuenta para el nodo, no para clientes.
**Archivos:** `mod:ingester`, `mod:analytics` (endpoint de propuestas).

**Criterios de aceptación**
1. **Dado** un nodo sin prefijos y el escenario `normal` (clientes en `10.20.0.0/24`), **cuando** pasa
   una hora simulada, **entonces** la propuesta incluye `10.20.0.0/24` con nº de IPs y bytes.
2. **Dado** la propuesta aceptada como *Clientes*, **cuando** llegan flujos nuevos, **entonces** se
   descubren clientes; los flujos anteriores no se reatribuyen (se indica en la UI).
3. **Dado** IPs públicas que no son del ASN del ISP, **cuando** se analizan, **entonces** no se proponen.

**Hecho cuando:** `make test-ingester SUITE=discovery-mode` en verde.

### I1-30 · Detectores de beaconing y salida sostenida (*should*)
- **Agente:** SEC · **Área:** data, security · **Talla:** M · **Épica:** EP-14 · **Depende de:** I1-11
- **Prioridad:** *should* — sale de I1 si pone en riesgo el gate G1; pasa a I2 sin bloquear nada.

**Como** analista de seguridad del ISP **quiero** detectar bots que llaman periódicamente a su C2 o
que sacan tráfico de forma sostenida **para** encontrar los que no escanean ni están en listas.

**Contexto:** filas **Beaconing** y **Tráfico saliente sostenido** de
[`../traffic-model.md`](../traffic-model.md) §8 (ventanas de horas sobre `flows_raw` y
`client_security_1h`; falsos positivos conocidos: actualizaciones, VPN, copias de seguridad);
`kind`: `beaconing`, `open_proxy_abuse`/`cryptomining` según el patrón.
**Archivos:** `mod:detection`.

**Criterios de aceptación**
1. **Dado** un escenario de beacon (mismo remoto, intervalo regular, bytes pequeños, ≥ 6 h),
   **cuando** se evalúa, **entonces** hay un hallazgo `beaconing` con intervalo y coeficiente de variación.
2. **Dado** un cliente con copias de seguridad nocturnas a un ASN de nube conocido, **cuando** se
   evalúa, **entonces** no hay hallazgo de salida sostenida.

**Hecho cuando:** `make test-detection SUITE=beaconing,sustained` en verde (el simulador añade los
escenarios `beacon` y `sustained_out`).

### I1-31 · UI Consola de plataforma mínima
- **Agente:** UI · **Área:** frontend · **Talla:** S · **Épica:** EP-23, EP-19
- **Depende de:** I0-15, C5

**Como** superadministrador **quiero** ver el estado de la plataforma, el almacenamiento y los rangos
de túneles **para** saber si la instalación está sana y si tengo copia remota.

**Contexto:** [`../frontend.md`](../frontend.md) §3.3–§3.4 (ISP, WireGuard hubs y rangos en lectura,
Almacenamiento con aviso "Sin copia remota configurada", Estado del sistema);
[ADR-0019](../adr/0019-almacenamiento-local-y-destino-remoto.md). Destinos remotos: I3.
**Archivos:** `apps/frontend/app/pages/platform/`.

**Criterios de aceptación**
1. **Dado** una instalación sin destino remoto, **cuando** abro Almacenamiento o Estado del sistema,
   **entonces** veo el aviso permanente "Sin copia remota configurada" con su consecuencia.
2. **Dado** la página WireGuard, **cuando** la abro, **entonces** veo cada hub con su endpoint, rango
   y ocupación (p. ej. "37 de 65 534 direcciones").
3. **Dado** un usuario que no es superadmin, **cuando** abre `/platform/*`, **entonces** "No encontrado".

**Hecho cuando:** `pnpm -C apps/frontend e2e --grep @platform` en verde.

## Plataforma y aceptación

### I1-22 · Instalador de un servidor
- **Agente:** PLAT · **Área:** infra, security · **Talla:** M · **Épica:** EP-22, EP-01
- **Depende de:** I0-02

**Como** persona responsable **quiero** instalar Horus en mi servidor siguiendo una guía corta
**para** probar con mi router real.

**Contexto:** perfil mínimo de [ADR-0025](../adr/0025-binario-modular-con-roles.md); TLS y
exposición según [`../security.md`](../security.md) (supuesto I1: red privada o VPN, P-26); puerto
UDP del hub WireGuard; almacenamiento local (D2).
**Archivos:** `deployments/compose/compose.prod.yaml`, `scripts/install.sh`, `docs/` (guía: PR a INT).

**Criterios de aceptación**
1. **Dado** una VM limpia con Docker, **cuando** ejecuto `scripts/install.sh`, **entonces** pregunta
   solo dominio/IP, rango de túneles y contraseña del superadmin, genera secretos y arranca todo.
2. **Dado** una instalación, **cuando** ejecuto `scripts/install.sh --check`, **entonces** comprueba
   puertos (HTTPS, UDP de WireGuard), salud de cada rol y espacio en disco.
3. **Dado** una segunda ejecución, **cuando** corre, **entonces** es idempotente y no regenera secretos.

**Hecho cuando:** job de CI que instala en una VM/contenedor limpio y ejecuta `make accept-i1 TARGET=installed`.

### I1-23 · Backup local y retención verificados
- **Agente:** PLAT · **Área:** infra · **Talla:** S · **Épica:** EP-19 · **Depende de:** I0-13, I0-06

**Como** persona responsable **quiero** no perder la configuración si el servidor falla **para**
poder reinstalar sin rehacer el alta de nodos.

**Contexto:** [`../disaster-recovery.md`](../disaster-recovery.md), [`../storage.md`](../storage.md)
(local primero; destino remoto SFTP en I3).
**Archivos:** `scripts/backup/`, `deployments/compose/`.

**Criterios de aceptación**
1. **Dado** el backup diario de PostgreSQL y de la configuración, **cuando** se restaura en una base
   vacía, **entonces** ISP, nodos, routers, peers, clientes y hallazgos coinciden (prueba en CI).
2. **Dado** el TTL del crudo de ClickHouse, **cuando** se comprueba en la instalación, **entonces**
   coincide con `storage.md`.
3. **Dado** disco por encima del 85 %, **cuando** se mide, **entonces** hay métrica y aviso en la UI
   de estado del sistema.

**Hecho cuando:** `make test-backup` en verde.

### I1-24 · `make accept-i1` con simulador y fixtures
- **Agente:** INT · **Área:** infra · **Talla:** M · **Épica:** EP-T4 · **Depende de:** I0-19

**Como** persona responsable **quiero** un comando que demuestre el incremento entero **para**
aceptarlo sin leer código.

**Contexto:** criterios de alto nivel de I1 en [`../roadmap.md`](../roadmap.md) §3.
**Archivos:** `tests/acceptance/i1/`.

**Criterios de aceptación**
1. **Dado** `make up && make accept-i1`, **cuando** corre, **entonces** registra un ISP, nodo y router
   simulado, completa el onboarding (clave pública simulada), reproduce los seis escenarios y
   comprueba clientes, tops, hallazgos, estado del exportador, aislamiento con un segundo ISP, WS y
   kiosco (e2e).
2. **Dado** los fixtures de CHR, **cuando** se reproducen, **entonces** se obtienen los mismos
   clientes y flujos que el laboratorio registró.
3. **Dado** un fallo, **cuando** termina, **entonces** el informe indica el criterio y la historia.

**Hecho cuando:** job nocturno `accept-i1` en verde dos noches seguidas.

### I1-25 · Lista de validación técnica en laboratorio CHR
- **Agente:** INT + FLOW · **Área:** data, infra · **Talla:** M · **Épica:** EP-T4, EP-26
- **Depende de:** I1-02, I1-04, I0-11

**Como** persona responsable **quiero** que el primer entregable funcione con RouterOS de verdad
**antes** de tocar mi router **para** no descubrir problemas en producción.

**Contexto:** **lista de validación de [`../vendors/mikrotik.md`](../vendors/mikrotik.md) §8.3**
(criterios de aceptación técnica del primer entregable) sobre el laboratorio de I0-11 con RouterOS
**7.12** y la **última long-term**.
**Archivos:** `tests/lab/`, informe adjunto al PR de cierre de I1.

**Criterios de aceptación** (uno por punto de §8.3)
1. **Dado** un CHR limpio, **cuando** se pega el script de I1-02, **entonces** no hay errores de sintaxis.
2. **Dado** el túnel, **cuando** se prueba, **entonces** el handshake ocurre en < 30 s y ICMP, SNMPv3 y
   REST/API-SSL responden por el túnel y **no** desde fuera.
3. **Dado** IPFIX del CHR, **cuando** llega, **entonces** se decodifica IPv4 e IPv6 con tiempos ± 2 s.
4. **Dado** NAT en el CHR, **cuando** un cliente navega, **entonces** la IP de cliente registrada es
   la privada en subida y bajada.
5. **Dado** 1 h de tráfico de laboratorio, **cuando** se comparan bytes de flujos con `ifHCOutOctets`
   de la LAN, **entonces** la diferencia es < 5 %, con y sin FastTrack.
6. **Dado** los perfiles beacon, escaneo y SMTP del laboratorio, **cuando** se generan, **entonces**
   aparecen como flujos esperados y los perfiles escaneo y SMTP producen sus hallazgos.
7. **Dado** un cliente PPPoE que reconecta, **cuando** cambia el ifIndex, **entonces** Horus sigue
   atribuyendo por IP sin crear clientes duplicados.
8. **Dado** el usuario de Horus en el router, **cuando** intenta escribir por REST, **entonces**
   recibe error y no puede ver secretos.
9. **Dado** el final de la validación, **cuando** se cierra, **entonces** los fixtures nuevos están
   guardados y los puntos "a verificar" resueltos en `vendors/mikrotik.md`.

**Hecho cuando:** `make lab-validate ROS=7.12` y `make lab-validate ROS=<long-term>` en una máquina con
KVM; informe adjunto. Si un punto falla, se abre bug con `needs:persona` y no se pasa al gate G1.

### I1-26 · Prueba de carga y pruebas de fallo de I1
- **Agente:** PLAT · **Área:** infra, data · **Talla:** M · **Épica:** EP-20, EP-21
- **Depende de:** I1-04, I1-21

**Como** persona responsable **quiero** saber cuánto aguanta un servidor y que nada se pierda si algo
se reinicia **para** dimensionar el primer despliegue.

**Contexto:** volúmenes de [`../vendors/mikrotik.md`](../vendors/mikrotik.md) §2.5; modos de fallo de
[`../architecture.md`](../architecture.md) §10.
**Archivos:** `tests/load/`, `tests/chaos/`.

**Criterios de aceptación**
1. **Dado** el simulador a 5 000 flujos/s (nodo mediano) durante 1 h, **cuando** termina, **entonces**
   no hay pérdida en el colector, el lag del ingester no crece y el p95 de la API de tráfico se mantiene
   < 500 ms; se publica el máximo sostenible del servidor de referencia.
2. **Dado** un reinicio de ClickHouse, de NATS y de `horus-app` durante la ingesta, **cuando** vuelven,
   **entonces** no se pierden flujos más allá del búfer documentado y el kiosco se recupera solo.
3. **Dado** el colector caído 1 min, **cuando** vuelve, **entonces** el hueco aparece como hueco
   (no ceros) y el exportador pasó por *Silencioso*.

**Hecho cuando:** `make load-i1` y `make chaos-i1` en el job nocturno, con informe publicado.

### I1-27 · Prueba con el MikroTik real de la persona
- **Agente:** PERSONA (guía y soporte: INT) · **Talla:** S · **Épica:** EP-26
- **Depende de:** todas las anteriores; I1-25 en verde

**Como** product owner **quiero** ver mi propia red en Horus **para** decidir si el primer entregable
es útil.

**Contexto:** guía paso a paso preparada por INT (instalación I1-22, alta, script, verificación);
recomendación de [`../vendors/mikrotik.md`](../vendors/mikrotik.md) §2.5: medir CPU por núcleo del
router antes y después de activar Traffic Flow; revisar offload por hardware (§2.7).

**Criterios de aceptación** (verificación de la persona)
1. **Dado** su router con RouterOS v7 ≥ 7.12, **cuando** pega el script y registra la clave pública,
   **entonces** en ≤ 2 min ve el router *Exportando* y clientes descubiertos.
2. **Dado** varios días de uso, **cuando** revisa los hallazgos, **entonces** los reconoce y resuelve
   o los marca como falso positivo; INT publica la tasa de falsos positivos por tipo de hallazgo.
3. **Dado** la CPU del router, **cuando** compara antes/después, **entonces** el aumento es aceptable
   para la persona (si no, se registra como riesgo para I2).
4. **Dado** la TV del NOC, **cuando** la deja 24 h en modo kiosco, **entonces** sigue mostrando datos
   frescos sin intervención.
5. **Dado** todo lo anterior, **cuando** decide, **entonces** declara I1 aceptado o abre issues con lo
   que falta.

**Hecho cuando:** la persona comenta "I1 aceptado" en el issue de seguimiento del incremento.
