# Incremento 1 — Primer entregable: NOC de un nodo MikroTik

- **Objetivo:** que un ISP vea, en una pantalla de monitoreo, **qué clientes (IPs) de un nodo
  MikroTik consumen qué y cuáles muestran señales compatibles con botnet**, con evidencia
  suficiente para actuar (D1, D5, D8, D10).
- **Demostración de la persona:** genera el script de onboarding desde la ficha de su router
  (RouterOS v7 ≥ 7.12), lo pega, registra la clave pública del router; en ≤ 2 min el router está
  *Exportando* y aparecen clientes por IP, tops de tráfico/servicios/categorías/ASN y hallazgos de
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
| I1-01 | Hub WireGuard mínimo: IPAM de túnel y alta del peer | CORE | backend, security | M | I0-09, I0-02 | 2 |
| I1-02 | Script de onboarding RouterOS y script inverso | CORE | backend, security | M | I1-01, I0-12 | 2 |
| I1-03 | Colector IPFIX / NetFlow v9 | FLOW | data | M | I0-10, I0-12, I1-01 (C2) | 2 |
| I1-04 | Ingesta a ClickHouse con lado cliente | FLOW | data | M | I1-03, I0-13 | 2 |
| I1-05 | Descubrimiento de IPs de clientes en el ingester | FLOW | data | S | I1-04 | 2 |
| I1-06 | Registro de clientes: API, tipo manual y alias | CORE | backend, security | M | I1-05 (C4), I0-09 | 2 |
| I1-07 | Enriquecimiento ASN/organización y catálogo semilla | FLOW | data | M | I1-04, I0-17 | 2 |
| I1-08 | API de tráfico: tops y series por ISP, nodo y cliente | FLOW | data, backend | M | I1-07 | 2 |
| I1-09 | Estado del exportador | FLOW | data | S | I1-03 | 2 |
| I1-10 | Motor de detección y detector de contacto con C2 | SEC | data, security | M | I1-04, I0-17 | 2 |
| I1-11 | Detectores de escaneo, SMTP masivo y ataque saliente | SEC | data, security | M | I1-10 | 2 |
| I1-12 | Hallazgos: ciclo de vida, API, eventos y falsos positivos | SEC | backend, security | M | I1-10 (C8) | 2 |
| I1-13 | Tiempo real: WebSocket con ámbito de ISP | CORE | backend, security | M | I0-08 (C6) | 2 |
| I1-14 | Pantallas de kiosco: registro, emparejamiento y token | CORE | backend, security | M | I0-08 (C7) | 2 |
| I1-15 | Persistencia de dashboards y plantillas | CORE | backend | S | I0-05 (C9) | 2 |
| I1-16 | UI Clientes: lista y detalle por IP | UI | frontend | M | I0-16, C5 | 2 |
| I1-17 | UI Tráfico: tops y series | UI | frontend | S | I0-16, C5 | 2 |
| I1-18 | UI Seguridad: hallazgos y detalle con evidencia | UI | frontend | M | I0-16, C5, C8 | 2 |
| I1-19 | UI Router: ficha, onboarding y estado del exportador | UI | frontend | S | I0-15, C5 | 2 |
| I1-20 | Widgets de I1 y plantillas "NOC del ISP" y "Seguridad" | UI | frontend | M | I0-16, I1-15 | 3 |
| I1-21 | Modo kiosco en la UI | UI | frontend | M | I1-20, I1-14, I1-13 | 3 |
| I1-22 | Instalador de un servidor | PLAT | infra, security | M | I0-02 | 2 |
| I1-23 | Backup local y retención verificados | PLAT | infra | S | I0-13, I0-06 | 2 |
| I1-24 | `make accept-i1` con simulador y fixtures | INT | infra | M | I0-19 | 2–3 |
| I1-25 | Lista de validación técnica en laboratorio CHR | INT + FLOW | data, infra | M | I1-02, I1-04, I0-11 | 3 |
| I1-26 | Prueba de carga y pruebas de fallo de I1 | PLAT | infra, data | M | I1-04, I1-21 | 3 |
| I1-27 | Prueba con el MikroTik real de la persona | PERSONA (guía: INT) | — | S | todas | G1 |

Camino crítico: I1-01 → I1-03 → I1-04 → I1-05/I1-10 → I1-11/I1-12 → I1-20 → I1-21 → I1-25 → I1-27.

---

## Conexión del router

### I1-01 · Hub WireGuard mínimo: IPAM de túnel y alta del peer
- **Agente:** CORE · **Área:** backend, security · **Talla:** M · **Épica:** EP-07, EP-26
- **Depende de:** I0-09, I0-02

**Como** administrador del ISP **quiero** que mi router se conecte a Horus por un túnel cifrado
**para** que flujos y gestión no viajen en claro por Internet y Horus sepa qué router envía cada flujo.

**Contexto:** diseño en [`../vendors/mikrotik.md`](../vendors/mikrotik.md) §5.1–5.2: el router es
iniciador con `persistent-keepalive=25s`; la clave privada se genera **en el router**; una `/32` por
router de un rango configurable del despliegue (nunca `100.64.0.0/10`), única en todo Horus; la IP de
túnel es la **identidad del exportador**; `allowed-address` del router = solo la red de servicios de
Horus. Registro de la clave pública por **opción A** (el técnico la copia a la UI); el endpoint de
*enrolment* (opción B) queda para I3. Módulos `wireguard` y `wg-agent`
([ADR-0025](../adr/0025-binario-modular-con-roles.md)); secretos según [`../security.md`](../security.md).
**Archivos:** `mod:wireguard`, `mod:wg-agent`.

**Criterios de aceptación**
1. **Dado** el evento de alta de router (I0-09), **cuando** lo consume `wireguard`, **entonces**
   asigna una IP de túnel libre y única globalmente; dos routers nunca reciben la misma IP, aunque
   sean de ISP distintos.
2. **Dado** una clave pública válida (base64, 44 caracteres) registrada por un `isp_admin`,
   **cuando** se guarda, **entonces** `wg-agent` añade el peer al hub con `allowed-ips` = su `/32` en
   < 10 s y el router pasa a "Esperando handshake".
3. **Dado** una clave pública ya usada por otro router, **cuando** se registra, **entonces** `409`.
4. **Dado** un handshake, **cuando** `wg-agent` lo observa, **entonces** se publica el estado y el
   router muestra "Túnel activo · último handshake hace N s".
5. **Dado** que se reinicia `horus-wg-agent`, **cuando** vuelve, **entonces** reconstruye todos los
   peers desde el estado deseado sin intervención (*fail-static*: los peers existentes no se borran
   mientras el control no responde).
6. **Dado** un usuario de otro ISP, **cuando** intenta registrar la clave de este router, **entonces** `404`.

**Hecho cuando:** `make test-wireguard` (integración con un namespace de red y `wg` en CI) y
`make test-tenancy` en verde.
**Fuera de alcance:** rotación, revocación desde la UI, preshared key, segundo hub (I3).

### I1-02 · Script de onboarding RouterOS y script inverso
- **Agente:** CORE · **Área:** backend, security · **Talla:** M · **Épica:** EP-26
- **Depende de:** I1-01, I0-12 (script validado en CHR)

**Como** técnico del ISP **quiero** un script listo para pegar en mi MikroTik **para** conectarlo a
Horus sin configurar nada a mano y poder deshacerlo.

**Contexto:** script, placeholders y notas operativas en
[`../vendors/mikrotik.md`](../vendors/mikrotik.md) §7 (todo con `comment="horus"`; IPFIX sin muestreo
por el túnel; usuario de solo lectura; SNMPv3; firewall solo desde el túnel; no tocar parámetros
globales de Traffic Flow sin avisar). RouterOS v7 ≥ 7.12. Contraseñas de SNMP/API generadas por
router: se muestran **una sola vez** y se guardan cifradas ([`../security.md`](../security.md)).
**Archivos:** `mod:wireguard` (plantillas `.rsc` versionadas por versión de RouterOS), endpoints de C5.

**Criterios de aceptación**
1. **Dado** un router con IP de túnel asignada, **cuando** pido el script, **entonces** recibo el
   `.rsc` de §7 con todos los placeholders resueltos y ninguno sin sustituir (test que busca `<…>`).
2. **Dado** el script generado, **cuando** se compara con el fixture validado en CHR (I0-12) para
   7.12 y la última long-term, **entonces** solo difieren los valores de los placeholders.
3. **Dado** el script, **cuando** lo pide por segunda vez, **entonces** las contraseñas no se
   vuelven a mostrar: se ofrece "Regenerar credenciales" que crea otras y lo registra.
4. **Dado** el script inverso, **cuando** se genera, **entonces** elimina todo lo que lleva
   `comment="horus"` y el target de Traffic Flow de Horus, sin tocar otros targets.
5. **Dado** un `isp_viewer`, **cuando** pide el script, **entonces** `403`; **dado** otro ISP, `404`.
6. **Dado** una versión de RouterOS < 7.12 declarada, **cuando** pide el script, **entonces** `422`
   con mensaje "RouterOS v7 ≥ 7.12 requerido".

**Hecho cuando:** `make test-onboarding-script` en verde; el script de I1-25 se pega en CHR sin errores.

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

### I1-04 · Ingesta a ClickHouse con lado cliente
- **Agente:** FLOW · **Área:** data · **Talla:** M · **Épica:** EP-10, EP-06
- **Depende de:** I1-03, I0-13

**Como** analista del ISP **quiero** que cada flujo quede guardado con su ISP, nodo, router, IP de
cliente e IP remota **para** consultar consumo por cliente.

**Contexto:** [`../traffic-model.md`](../traffic-model.md) (dirección, lado cliente, atribución),
[`../database.md`](../database.md) (tablas C3), [`../vendors/mikrotik.md`](../vendors/mikrotik.md) §2.4
(con NAT en el mismo router se ve la IP privada del cliente; PPPoE con ifIndex cambiante: atribuir
por IP, nunca por interfaz dinámica). La IP de cliente es la que cae en los **prefijos de clientes
del realm** del router (I0-09); si ninguna cae, el flujo se guarda como "No atribuido".
**Archivos:** `mod:ingester`.

**Criterios de aceptación**
1. **Dado** un flujo de subida (`src` en prefijo de cliente) y otro de bajada (`dst` en prefijo),
   **cuando** se ingieren, **entonces** ambos quedan con la misma `client_ip`, dirección correcta e
   `tenant_id`/nodo/router del exportador.
2. **Dado** un flujo sin ninguna IP en los prefijos del realm, **cuando** se ingiere, **entonces**
   queda como no atribuido y suma en la métrica de cobertura del router.
3. **Dado** lotes del simulador a 5 000 flujos/s, **cuando** se ingieren 10 min, **entonces** no hay
   lag creciente del consumidor y los inserts son por lotes.
4. **Dado** un lote reentregado (al menos una vez), **cuando** se procesa de nuevo, **entonces** no
   duplica filas (idempotencia según `events.md`/`database.md`).
5. **Dado** que ClickHouse no responde, **cuando** dura menos que la retención del stream,
   **entonces** al volver se ingiere todo sin pérdida.

**Hecho cuando:** `make test-ingester` (integración con ClickHouse y NATS efímeros + replay de
escenarios) en verde.

### I1-05 · Descubrimiento de IPs de clientes en el ingester
- **Agente:** FLOW · **Área:** data · **Talla:** S · **Épica:** EP-06 · **Depende de:** I1-04

**Como** operador NOC **quiero** que cada IP de cliente vista aparezca sola como cliente **para** no
tener que dar de alta clientes (D1).

**Contexto:** [ADR-0018](../adr/0018-la-ip-es-el-cliente.md); identidad `(ISP, realm, IP)`; el
ingester emite IPs vistas agregadas por ventana (no una por flujo) con el evento de C4; el registro
lo mantiene `devices` (I1-06). IPv6: el cliente es el prefijo delegado si así lo fija `database.md`
(pregunta abierta en `vendors/mikrotik.md` §9.2).
**Archivos:** `mod:ingester`.

**Criterios de aceptación**
1. **Dado** el escenario `normal` con 300 clientes, **cuando** pasa una ventana, **entonces** se
   publican eventos que cubren exactamente esas 300 IPs con `first_seen`/`last_seen` de la ventana.
2. **Dado** 1 000 flujos de la misma IP en la ventana, **cuando** se emite, **entonces** sale un solo
   registro para esa IP.
3. **Dado** una IP fuera de los prefijos, **cuando** se ve, **entonces** no se emite como cliente.

**Hecho cuando:** `make test-ingester SUITE=discovery` en verde.

### I1-06 · Registro de clientes: API, tipo manual y alias
- **Agente:** CORE · **Área:** backend, security · **Talla:** M · **Épica:** EP-06, EP-16
- **Depende de:** I1-05 (C4), I0-09

**Como** operador NOC **quiero** ver los clientes descubiertos y poder marcar una IP como comercial
o ponerle un alias **para** reflejar lo que sé de mi red.

**Contexto:** [ADR-0018](../adr/0018-la-ip-es-el-cliente.md); tipo por defecto `residential`; el tipo
**manual prevalece** sobre el automático que llegará en I2; cambio de tipo con motivo obligatorio y
registro de auditoría; acceso al detalle de cliente registrado ([`../security.md`](../security.md),
minimización). Endpoints de C5; permisos de C7 (p. ej. `subscribers.read`, `subscribers.manage`).
**Archivos:** `mod:devices`.

**Criterios de aceptación**
1. **Dado** eventos de IPs vistas, **cuando** se consumen, **entonces** se crea el cliente si no
   existe (tipo `residential`, origen `default`) o se actualiza `last_seen`, de forma idempotente.
2. **Dado** la lista de clientes, **cuando** filtro por nodo, tipo, "con hallazgos abiertos" o
   "activo en las últimas 24 h", **entonces** la paginación por cursor devuelve los correctos.
3. **Dado** un `isp_operator`, **cuando** cambia el tipo a `commercial` con motivo, **entonces** se
   guarda con origen `manual`, autor y fecha, se publica el evento de cambio de tipo y queda en el
   historial; sin motivo → `422`.
4. **Dado** un `isp_viewer`, **cuando** intenta cambiar el tipo, **entonces** `403`.
5. **Dado** un cliente de otro ISP, **cuando** se pide por ID, **entonces** `404`.
6. **Dado** cualquier endpoint, **cuando** se revisa la API, **entonces** la IP del cliente nunca
   va en la ruta ni en la query string (se usa el ID opaco del cliente; la búsqueda por IP va en el
   cuerpo de un `POST` de búsqueda).

**Hecho cuando:** `make test-devices SUITE=clients` y `make test-tenancy` en verde.

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

### I1-08 · API de tráfico: tops y series por ISP, nodo y cliente
- **Agente:** FLOW · **Área:** data, backend · **Talla:** M · **Épica:** EP-15
- **Depende de:** I1-07

**Como** operador NOC **quiero** consultar quién y qué consume más **para** detectar saturación y
consumo anómalo.

**Contexto:** módulo `analytics` ([ADR-0025](../adr/0025-binario-modular-con-roles.md)); consultas
sobre agregados (C3) eligiendo granularidad según el rango; `meta.partial` cuando falta cobertura
([`../api.md`](../api.md)); toda consulta filtra por `tenant_id` (test de arquitectura).
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

Los umbrales por defecto de esta sección son **conservadores**, configurables por ISP y se ajustan
con el laboratorio (I1-25) y con el router real (I1-27). Con `active-flow-timeout=1m` la resolución
temporal efectiva es de un minuto ([`../vendors/mikrotik.md`](../vendors/mikrotik.md) §2.2).
Lenguaje: "señales compatibles con…", nunca "infectado" salvo confirmación humana
([ADR-0024](../adr/0024-deteccion-de-botnets-como-objetivo-principal.md)).

### I1-10 · Motor de detección y detector de contacto con C2
- **Agente:** SEC · **Área:** data, security · **Talla:** M · **Épica:** EP-13, EP-14
- **Depende de:** I1-04, I0-17

**Como** analista de seguridad del ISP **quiero** saber qué clientes contactan con servidores de
mando y control conocidos **para** avisarles antes de que su IP acabe en listas negras (D5).

**Contexto:** [ADR-0024](../adr/0024-deteccion-de-botnets-como-objetivo-principal.md),
[`../traffic-model.md`](../traffic-model.md); feeds de I0-17 (con fuente y fecha de inclusión);
salida = hallazgos según C8. El motor evalúa ventanas sobre ClickHouse (o el consumo de lotes, según
C1) y es extensible: un detector = una función con nombre, parámetros por ISP, señales y confianza.
**Archivos:** `mod:detection`.

**Criterios de aceptación**
1. **Dado** el escenario `c2`, **cuando** se evalúa, **entonces** se genera un hallazgo por la IP
   esperada con detector `c2_contact`, la IP remota, el feed, la fecha de inclusión en el feed, nº de
   conexiones, primera/última vez y confianza (alta si ≥ 3 conexiones en 1 h o periodicidad; media si 1).
2. **Dado** el escenario `normal`, **cuando** se evalúa, **entonces** no hay ningún hallazgo.
3. **Dado** una IP remota que salió del feed hace más de la ventana de vigencia, **cuando** se
   evalúa, **entonces** no genera hallazgo nuevo.
4. **Dado** un detector desactivado para un ISP, **cuando** se evalúa, **entonces** no produce
   hallazgos para ese ISP y sí para los demás.

**Hecho cuando:** `make test-detection SUITE=c2` y `make accept-i1 SCENARIO=c2` en verde.

### I1-11 · Detectores de escaneo, SMTP masivo y ataque saliente
- **Agente:** SEC · **Área:** data, security · **Talla:** M · **Épica:** EP-14 · **Depende de:** I1-10

**Como** analista de seguridad del ISP **quiero** detectar comportamientos típicos de equipos
reclutados en botnets **para** encontrar clientes comprometidos aunque el C2 no esté en ninguna lista.

**Contexto:** señales de [`../vendors/mikrotik.md`](../vendors/mikrotik.md) §6 (fan-out, SYN sin
respuesta por `tcp-flags`, 25/tcp saliente, volumen) y de ADR-0024. Valores por defecto:

| Detector | Señal | Umbral por defecto (configurable por ISP) |
| --- | --- | --- |
| `outbound_scan` | Destinos distintos al mismo puerto | ≥ 100 destinos en 5 min, ≥ 70 % de flujos sin respuesta o solo SYN; severidad alta si el puerto es de propagación (23, 2323, 22, 445, 5555, 7547, 37215, 52869) |
| `smtp_burst` | 25/tcp saliente | ≥ 20 servidores de correo distintos en 15 min desde una IP de tipo residencial (los comerciales usan umbral aparte) |
| `outbound_dos` | Paquetes hacia pocos destinos | ≥ 10 000 pps hacia ≤ 3 destinos sostenido ≥ 2 min, o inundación UDP/SYN con proporción subida/bajada anómala |

**Archivos:** `mod:detection`.

**Criterios de aceptación**
1. **Dado** los escenarios `scan`, `spam` y `dos_out`, **cuando** se evalúan, **entonces** cada uno
   produce exactamente los hallazgos de su `expected.json` con ≥ 2 elementos de evidencia (p. ej.
   puerto, nº de destinos, muestra de destinos, pps, ventana).
2. **Dado** el escenario `normal` y el `commercial` (un servidor legítimo con muchas conexiones
   entrantes), **cuando** se evalúan, **entonces** no se genera ningún hallazgo de estos detectores.
3. **Dado** un flujo de un cliente marcado `commercial`, **cuando** se evalúa `smtp_burst`,
   **entonces** usa el umbral de comerciales.
4. **Dado** un muestreo declarado en el exportador, **cuando** se evalúa, **entonces** la confianza
   baja un nivel y la evidencia lo indica.

**Hecho cuando:** `make test-detection` y `make accept-i1 SCENARIO=scan,spam,dos_out,commercial,normal` en verde.

### I1-12 · Hallazgos: ciclo de vida, API, eventos y falsos positivos
- **Agente:** SEC · **Área:** backend, security · **Talla:** M · **Épica:** EP-14
- **Depende de:** I1-10 (C8)

**Como** analista de seguridad del ISP **quiero** gestionar cada hallazgo de principio a fin **para**
saber qué está pendiente y enseñar a Horus qué no es un problema.

**Contexto:** esquema C8; estados *Nuevo → En revisión → Confirmado / Falso positivo → Resuelto*;
deduplicación: un hallazgo abierto por (ISP, cliente, detector, objetivo principal) que se actualiza
con nuevas ocurrencias; "Falso positivo" silencia ese patrón para esa IP durante un tiempo
configurable (por defecto 30 días); eventos de alta y cambio (C4) para WebSocket y, en I3, alertas.
Permisos de C7 (p. ej. `security.read`, `security.manage`).
**Archivos:** `mod:detection`.

**Criterios de aceptación**
1. **Dado** ocurrencias repetidas del mismo patrón, **cuando** se detectan, **entonces** se actualiza
   el hallazgo abierto (contador, última vez, evidencia) en lugar de crear otro.
2. **Dado** un hallazgo marcado *Falso positivo* con motivo, **cuando** el patrón se repite en el
   periodo de silencio, **entonces** no se reabre; pasado el periodo, sí.
3. **Dado** un hallazgo *Resuelto*, **cuando** vuelve a aparecer el patrón, **entonces** se abre uno
   nuevo enlazado al anterior ("reincidente").
4. **Dado** un `isp_viewer`, **cuando** intenta cambiar el estado, **entonces** `403`; otro ISP, `404`.
5. **Dado** la lista, **cuando** filtro por estado, severidad, detector, nodo o cliente, **entonces**
   la paginación devuelve los correctos y cada fila trae un resumen legible ("Escaneo del puerto 23
   a 1 240 destinos en 5 min").

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

### I1-14 · Pantallas de kiosco: registro, emparejamiento y token
- **Agente:** CORE · **Área:** backend, security · **Talla:** M · **Épica:** EP-25
- **Depende de:** I0-08 (C7)

**Como** administrador del ISP **quiero** dar de alta una TV del NOC sin dejar una sesión de
usuario abierta en ella **para** que esté siempre encendida sin riesgo.

**Contexto:** diseño en [`../frontend.md`](../frontend.md) §7.2 (emparejamiento por código);
[`../security.md`](../security.md) (token de kiosco: solo lectura, revocable, limitado a ISP y lista
de dashboards, sin acceso a detalle de cliente); pregunta Q19 de
[`../open-questions/security-ops.md`](../open-questions/security-ops.md).
**Archivos:** `mod:auth` (tokens de pantalla), `mod:gateway`.

**Criterios de aceptación**
1. **Dado** una TV que abre `/kiosk`, **cuando** no tiene token, **entonces** obtiene un código de
   emparejamiento de 8 caracteres válido 10 min.
2. **Dado** un `isp_admin` que introduce el código y elige ISP, lista de dashboards e intervalo de
   rotación, **cuando** confirma, **entonces** la TV recibe un token de pantalla de solo lectura y el
   alta queda auditada.
3. **Dado** un token de pantalla, **cuando** pide un endpoint de escritura, de detalle de cliente o de
   otro ISP, **entonces** `403`/`404`.
4. **Dado** que el admin revoca la pantalla, **cuando** la TV hace la siguiente petición, **entonces**
   recibe `401` y vuelve a la pantalla de emparejamiento en < 1 min.
5. **Dado** una pantalla, **cuando** el admin cambia su lista de dashboards, **entonces** la TV la
   aplica sin intervención (evento o sondeo de configuración).

**Hecho cuando:** `make test-auth SUITE=kiosk` y `make test-tenancy` en verde.

### I1-15 · Persistencia de dashboards y plantillas
- **Agente:** CORE · **Área:** backend · **Talla:** S · **Épica:** EP-24 · **Depende de:** I0-05 (C9)

**Como** operador NOC **quiero** que los dashboards existan en el servidor **para** que la misma
plantilla se vea igual en mi navegador y en la TV.

**Contexto:** formato de layout C9 y ámbitos de [`../frontend.md`](../frontend.md) §6.4; en I1 solo
plantillas del sistema (solo lectura) + lista de reproducción de kiosco; guardado por usuario/ISP es I2,
pero el modelo ya lo admite.
**Archivos:** `mod:analytics/dashboards`.

**Criterios de aceptación**
1. **Dado** el arranque, **cuando** se migra, **entonces** existen las plantillas "NOC del ISP" y
   "Seguridad" (versionadas) válidas contra C9.
2. **Dado** un layout con un tipo de widget desconocido, **cuando** se intenta guardar, **entonces** `422`.
3. **Dado** `GET` de un dashboard, **cuando** lo pide un usuario o token de otro ISP, **entonces** `404`
   (las plantillas del sistema son visibles para todos, sus datos no).

**Hecho cuando:** `make test-analytics SUITE=dashboards` en verde.

## Interfaz

Todas las historias de UI cumplen [`../frontend.md`](../frontend.md) §9 (estados), §12 (permisos),
§13–§14 (visual y accesibilidad) y adjuntan capturas claro/oscuro. Trabajan contra mocks de C5 hasta
que el backend está en `main`, y la historia no se cierra hasta pasar contra el backend real.

### I1-16 · UI Clientes: lista y detalle por IP
- **Agente:** UI · **Área:** frontend · **Talla:** M · **Épica:** EP-06, EP-16
- **Depende de:** I0-16, C5

**Como** operador NOC **quiero** ver los clientes de mi ISP por IP con su tipo y su consumo **para**
encontrar rápido a quién mirar.

**Contexto:** [`../frontend.md`](../frontend.md) §8.1 (lista, detalle, tipo residencial/comercial y
razones, cambio manual con motivo, privacidad: la IP nunca en la URL).
**Archivos:** `apps/frontend/app/pages/isp/[isp]/clients/`, `apps/frontend/app/components/clients/`.

**Criterios de aceptación**
1. **Dado** la lista, **cuando** cargo, **entonces** veo IP (monoespaciada), nodo, tipo con su origen
   (por defecto / manual), tráfico 24 h ↓↑, servicio principal, hallazgos abiertos y última vez visto.
2. **Dado** una búsqueda por IP, **cuando** la escribo, **entonces** se busca por `POST` y la URL no
   contiene la IP.
3. **Dado** el detalle, **cuando** cambio el tipo a Comercial, **entonces** se me pide el motivo, el
   cambio aparece en el historial y un toast confirma "Tipo actualizado".
4. **Dado** un `isp_viewer`, **cuando** abre el detalle, **entonces** no ve la acción de cambiar tipo.
5. **Dado** un ISP sin clientes descubiertos, **cuando** abro la lista, **entonces** el estado vacío
   explica "Aún no hemos visto clientes en los flujos" con enlace a la ficha del router.

**Hecho cuando:** `pnpm -C apps/frontend e2e --grep @clients` en verde (mocks y backend real).

### I1-17 · UI Tráfico: tops y series
- **Agente:** UI · **Área:** frontend · **Talla:** S · **Épica:** EP-15 · **Depende de:** I0-16, C5

**Como** ingeniero de red **quiero** ver tops de clientes, servicios, categorías y organizaciones
con un rango de tiempo **para** entender el consumo de mi red.

**Contexto:** [`../frontend.md`](../frontend.md) §8.3 y reglas de gráficos (top N + "Otros", huecos
no ceros, `meta.partial`); reutiliza los widgets de I1-20.
**Archivos:** `apps/frontend/app/pages/isp/[isp]/traffic/`.

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
**Archivos:** `apps/frontend/app/pages/isp/[isp]/security/`, `apps/frontend/app/components/findings/`.

**Criterios de aceptación**
1. **Dado** la lista, **cuando** cargo, **entonces** veo severidad (icono + texto + color),
   resumen legible, cliente, nodo, confianza, estado y última vez, ordenado por severidad y recencia.
2. **Dado** el detalle, **cuando** lo abro, **entonces** veo la narrativa ("Por qué lo marcamos"),
   ≥ 2 elementos de evidencia, la línea de tiempo de ocurrencias y las acciones de estado.
3. **Dado** "Marcar como falso positivo", **cuando** lo pulso, **entonces** pide motivo y explica
   cuánto tiempo se silencia.
4. **Dado** un hallazgo nuevo por WebSocket, **cuando** llega, **entonces** aparece arriba sin mover
   el scroll y se anuncia en la región `aria-live` de forma agregada.

**Hecho cuando:** `pnpm -C apps/frontend e2e --grep @security` en verde.

### I1-19 · UI Router: ficha, onboarding y estado del exportador
- **Agente:** UI · **Área:** frontend · **Talla:** S · **Épica:** EP-26, EP-05
- **Depende de:** I0-15, C5

**Como** administrador del ISP **quiero** conectar mi MikroTik desde su ficha siguiendo pasos claros
**para** empezar a ver datos sin leer documentación.

**Contexto:** [`../frontend.md`](../frontend.md) §8.4 (pasos: generar script → pegar → registrar
clave pública → verificación en vivo de handshake y flujos); secretos mostrados una sola vez.
**Archivos:** `apps/frontend/app/pages/isp/[isp]/nodes/`, `apps/frontend/app/components/onboarding/`.

**Criterios de aceptación**
1. **Dado** un router "Pendiente de configurar", **cuando** abro su ficha, **entonces** veo los tres
   pasos con el estado de cada uno y el botón "Generar script".
2. **Dado** el script, **cuando** se muestra, **entonces** hay "Copiar" y "Descargar .rsc" y un aviso
   de que las contraseñas no se volverán a mostrar.
3. **Dado** que llega el primer handshake y luego el primer flujo, **cuando** ocurren, **entonces**
   los pasos se marcan en vivo y la ficha muestra "Exportando · último flujo hace N s".
4. **Dado** un router *Silencioso*, **cuando** abro su ficha, **entonces** veo desde cuándo y qué
   revisar (túnel, Traffic Flow, firewall).

**Hecho cuando:** `pnpm -C apps/frontend e2e --grep @onboarding` en verde.

### I1-20 · Widgets de I1 y plantillas "NOC del ISP" y "Seguridad"
- **Agente:** UI · **Área:** frontend · **Talla:** M · **Épica:** EP-24 · **Depende de:** I0-16, I1-15

**Como** operador NOC **quiero** un dashboard listo para la pantalla del NOC **para** ver de un
vistazo el estado de tráfico y seguridad de mi ISP.

**Contexto:** catálogo de widgets marcados "I1" en [`../frontend.md`](../frontend.md) §6.2 y
plantillas de §6.5; cada widget funciona en escala normal y mural (§7.4).
**Archivos:** `apps/frontend/app/widgets/*`, plantillas en el seed de I1-15 (PR a CORE).

**Criterios de aceptación**
1. **Dado** la plantilla "NOC del ISP", **cuando** se abre con datos del simulador, **entonces**
   muestra los widgets de §6.5 con datos reales y su frescura.
2. **Dado** un widget cuya fuente falla, **cuando** se renderiza, **entonces** solo él muestra error.
3. **Dado** escala mural a 1920×1080, **cuando** se captura, **entonces** cumple los tamaños mínimos
   de §7.4 (verificado con un test que mide el tamaño de fuente calculado de valores y etiquetas).

**Hecho cuando:** `pnpm -C apps/frontend e2e --grep @dashboards` en verde con capturas normal y mural.

### I1-21 · Modo kiosco en la UI
- **Agente:** UI · **Área:** frontend · **Talla:** M · **Épica:** EP-25
- **Depende de:** I1-20, I1-14, I1-13

**Como** pantalla NOC **quiero** mostrar dashboards indefinidamente sin que nadie la toque **para**
que el equipo vea el estado de la red en todo momento.

**Contexto:** [`../frontend.md`](../frontend.md) §7 completo (emparejamiento, pantalla completa,
rotación, autorrefresco, legibilidad, tema oscuro, sin interacción, recuperación, actualización de
versión, protección de pantalla).
**Archivos:** `apps/frontend/app/pages/kiosk/`, `apps/frontend/app/layouts/kiosk.vue`,
`apps/frontend/app/composables/useKiosk.ts`.

**Criterios de aceptación**
1. **Dado** una TV emparejada, **cuando** arranca, **entonces** muestra el primer dashboard en tema
   oscuro, sin barra lateral ni controles, y rota según el intervalo configurado.
2. **Dado** un corte de red de 5 min, **cuando** ocurre, **entonces** mantiene los últimos datos con
   "Sin conexión desde HH:MM" bien visible y se recupera sola al volver, sin recargar a mano.
3. **Dado** un reinicio del servidor, **cuando** vuelve, **entonces** la TV reconecta y, si la versión
   del frontend cambió, se recarga sola en el siguiente cambio de dashboard.
4. **Dado** 24 h de ejecución simulada (Playwright con reloj acelerado), **cuando** termina,
   **entonces** la memoria del navegador no crece sin límite y no hay errores no capturados.
5. **Dado** `prefers-reduced-motion`, **cuando** rota, **entonces** el cambio es un fundido corto o
   instantáneo.

**Hecho cuando:** `pnpm -C apps/frontend e2e --grep @kiosk` (incluye la prueba de 24 h acelerada)
en verde.

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
2. **Dado** varios días de uso, **cuando** revisa los hallazgos, **entonces** marca cada uno como
   confirmado o falso positivo; INT publica la tasa de falsos positivos por detector.
3. **Dado** la CPU del router, **cuando** compara antes/después, **entonces** el aumento es aceptable
   para la persona (si no, se registra como riesgo para I2).
4. **Dado** la TV del NOC, **cuando** la deja 24 h en modo kiosco, **entonces** sigue mostrando datos
   frescos sin intervención.
5. **Dado** todo lo anterior, **cuando** decide, **entonces** declara I1 aceptado o abre issues con lo
   que falta.

**Hecho cuando:** la persona comenta "I1 aceptado" en el issue de seguimiento del incremento.
