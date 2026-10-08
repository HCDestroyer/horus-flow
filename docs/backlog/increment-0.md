# Incremento 0 — Cimientos

- **Objetivo:** que cinco agentes de IA trabajen en paralelo sin bloquearse, sobre un esqueleto
  que ya ejecuta de punta a punta, con contratos congelados, datos de flujo simulados realistas y
  un laboratorio MikroTik CHR que responde las dudas técnicas antes de construir I1.
- **Demostración de la persona:** `make up`, login con el superadmin semilla, alta de ISP "Demo",
  nodo "Centro" y su MikroTik (IP de túnel asignada, prefijos de clientes); dashboard vacío con
  selector de ISP; `make sim` genera flujos RouterOS que `make sim-verify` decodifica (el colector
  llega en I1); `make lab-up` levanta el CHR de laboratorio.
- **Gate G0:** la persona aprueba los contratos C1–C10 ([`team.md`](team.md) §3) en un único PR.
- **Salida:** `make up && make accept-i0` en verde en un clon limpio.
- Plan y alcance: [`../roadmap.md`](../roadmap.md) §3 (I0). Reglas de historia: [`README.md`](README.md).

| ID | Historia | Agente | Área | Talla | Depende de | Ola |
| --- | --- | --- | --- | --- | --- | --- |
| I0-01 | Esqueleto del monorepo y CODEOWNERS por agente | PLAT | infra | S | — | 0 |
| I0-02 | Compose de desarrollo | PLAT | infra | M | I0-01 | 0 |
| I0-03 | CI, protección de `main` y auto-merge | PLAT | infra, security | M | I0-01 | 0 |
| I0-04 | Binario `horus` con roles y librerías comunes | CORE | backend | M | I0-01 | 0 |
| I0-05 | Contratos v0 (C1–C10) y gate G0 | INT | backend, data, frontend | M | — | 0 |
| I0-06 | Modelo multi-tenant en PostgreSQL y superadmin semilla | CORE | backend, data, security | M | I0-02, I0-05 (C2) | 1 |
| I0-07 | Autenticación mínima definitiva | CORE | backend, security | M | I0-04, I0-06 | 1 |
| I0-08 | Aislamiento por ISP y suite de matriz de tenants | CORE | backend, security | M | I0-07 | 1 |
| I0-09 | Inventario mínimo: ISP, nodo, router, prefijos de clientes | CORE | backend | M | I0-08 | 1 |
| I0-10 | Simulador de flujos IPFIX/NetFlow v9 con escenarios | FLOW | data | M | I0-01 | 0 |
| I0-11 | Laboratorio MikroTik CHR reproducible | PLAT | infra | M | I0-02 | 1 |
| I0-12 | Verificar los puntos "a verificar" y grabar fixtures | FLOW + PERSONA | data | M | I0-11 | 1 |
| I0-13 | Esquema ClickHouse v0 y migraciones | FLOW | data | M | I0-02, I0-05 (C3) | 1 |
| I0-14 | Proyecto Nuxt, login y layout | UI | frontend | M | I0-01 | 0 |
| I0-15 | Selector de ISP, cliente API generado y mocks | UI | frontend | M | I0-14, I0-05 (C5) | 1 |
| I0-16 | Marco de widgets v0 | UI | frontend | M | I0-15, I0-05 (C9) | 1 |
| I0-17 | Cargadores de datasets ASN y feeds de reputación | SEC | data, security | M | I0-01 | 0 |
| I0-18 | Observabilidad mínima | PLAT | infra | S | I0-04 | 1 |
| I0-19 | `make accept-i0` y e2e de humo | INT | infra, frontend | S | I0-09, I0-15 | 1 |

Historias convertidas del antiguo `sprint-01.md`: S01-01 → I0-02, S01-03 → I0-03, S01-06 → I0-04,
S01-08/09 → I0-07, S01-10 → I0-06, S01-13…18 → I0-14/I0-15, S01-21 → I0-19, S01-04 → I0-18
(recortada), S01-20 (simulador SNMP) → sustituida por I0-10 (simulador de flujos).

---

### I0-01 · Esqueleto del monorepo y CODEOWNERS por agente
- **Agente:** PLAT · **Área:** infra · **Talla:** S · **Épica:** EP-01 · **Depende de:** —

**Como** agente de IA **quiero** un repositorio con la estructura y la propiedad de carpetas
definidas **para** saber dónde escribir sin pisar a otros agentes.

**Contexto:** estructura en [`../conventions.md`](../conventions.md) §1; propiedad por agente en
[`team.md`](team.md) §2; si [`../architecture.md`](../architecture.md) consolida módulos en menos
procesos, se usa su mapa (C1).
**Archivos:** raíz (`go.mod`, `Makefile`, `.editorconfig`, `.gitignore`), `.github/CODEOWNERS`,
`services/cmd/horus/`, una carpeta por módulo `mod:<módulo>` con su README ([`team.md`](team.md) §2),
`apps/frontend/`, `packages/*/README.md`, `infrastructure/`, `deployments/`, `scripts/`,
`tools/flowsim/`, `tests/acceptance/`.

**Criterios de aceptación**
1. **Dado** un clon limpio, **cuando** ejecuto `make help`, **entonces** lista al menos `up`,
   `down`, `test`, `lint`, `sim`, `sim-verify`, `lab-up`, `accept-i0`.
2. **Dado** `.github/CODEOWNERS`, **cuando** se modifica un archivo de `mod:ingester`,
   **entonces** el dueño requerido es el rol FLOW (equipo o usuario de GitHub configurado).
3. **Dado** un archivo `.env` o `*.pcap` en la raíz, **cuando** hago `git status`, **entonces**
   aparece ignorado (los datos reales nunca entran al repo).

**Hecho cuando:** `make help` y `scripts/check-codeowners.sh` (verifica que toda carpeta de
primer nivel tiene dueño) pasan en CI.

### I0-02 · Compose de desarrollo
- **Agente:** PLAT · **Área:** infra · **Talla:** M · **Épica:** EP-01 · **Depende de:** I0-01

**Como** agente de IA **quiero** levantar todas las dependencias con un comando **para** probar
contra un entorno idéntico al de los demás agentes.

**Contexto:** perfil mínimo de [ADR-0025](../adr/0025-binario-modular-con-roles.md): PostgreSQL,
ClickHouse ([ADR-0021](../adr/0021-clickhouse-desde-el-primer-incremento.md)), NATS JetStream,
Valkey ([ADR-0020](../adr/0020-valkey-en-lugar-de-redis.md)) y Traefik, más los contenedores
propios `horus-app`, `horus-collector` y `horus-wg-agent` de la misma imagen. **Sin** MinIO
([ADR-0019](../adr/0019-almacenamiento-local-y-destino-remoto.md)). Versiones de imagen fijadas.
**Archivos:** `deployments/compose/compose.dev.yaml`, `deployments/compose/.env.example`,
`Makefile`.

**Criterios de aceptación**
1. **Dado** un clon limpio con Docker, **cuando** ejecuto `make up`, **entonces** todas las
   dependencias quedan `healthy` en < 2 min.
2. **Dado** el compose, **cuando** lo reviso, **entonces** cada servicio tiene `healthcheck`,
   volumen nombrado, imagen con versión fija (nunca `latest`) y límite de memoria.
3. **Dado** `make down && make up`, **cuando** vuelve a levantar, **entonces** los datos de
   PostgreSQL y ClickHouse persisten; `make reset` los borra.
4. **Dado** un puerto ocupado, **cuando** defino `HORUS_PG_PORT` (o equivalente) en `.env`,
   **entonces** arranca sin editar el YAML.
5. **Dado** que falta una variable obligatoria, **cuando** ejecuto `make up`, **entonces** falla
   nombrando la variable sin imprimir valores.

**Hecho cuando:** job de CI `compose-smoke` (`make up && scripts/wait-healthy.sh && make down`)
en verde.

### I0-03 · CI, protección de `main` y auto-merge
- **Agente:** PLAT · **Área:** infra, security · **Talla:** M · **Épica:** EP-01, EP-T4
- **Depende de:** I0-01

**Como** persona responsable del producto **quiero** que la CI verifique todo lo verificable
**para** no tener que revisar cada PR de los agentes (D7).

**Contexto:** pipeline y herramientas en [`../conventions.md`](../conventions.md) §4, §8; reglas
de merge en [`team.md`](team.md) §5; DoD en [`README.md`](README.md) §8.
**Archivos:** `.github/workflows/*.yaml`, `.github/pull_request_template.md`,
`.github/labeler.yml`, `scripts/ci/`.

**Criterios de aceptación**
1. **Dado** un PR, **cuando** se abre, **entonces** corre `lint → unit → integración → contratos →
   seguridad → build` solo para lo afectado (filtro por rutas y grafo Go).
2. **Dado** un PR que introduce un secreto (clave privada, token), **cuando** corre la CI,
   **entonces** gitleaks la falla.
3. **Dado** un PR con CI verde y una aprobación de agente revisor distinto del autor, **cuando** no
   tiene etiquetas `contract:*` ni `area:security`, **entonces** entra en la merge queue
   automáticamente.
4. **Dado** un PR con `contract:*` o `area:security`, **cuando** tiene CI verde y aprobación de
   agente, **entonces** **no** se fusiona hasta que la persona lo aprueba y recibe la etiqueta
   `needs:persona`.
5. **Dado** un PR que toca `mod:auth`, la librería de autorización, `mod:wireguard` o `.github/workflows/`, **cuando** se
   abre, **entonces** el etiquetador añade `area:security`.
6. **Dado** el pipeline completo sin caché, **cuando** se mide, **entonces** tarda < 15 min.

**Hecho cuando:** un PR de prueba de cada tipo (normal, con secreto, con `contract:openapi`)
produce el resultado esperado; captura del ruleset adjunta al PR.

### I0-04 · Binario `horus` con roles y librerías comunes
- **Agente:** CORE · **Área:** backend · **Talla:** M · **Épica:** EP-01, EP-T1
- **Depende de:** I0-01

**Como** agente de IA **quiero** el binario `horus` con registro de roles, configuración, logs,
métricas, health checks por rol y apagado ordenado **para** que todos los módulos nazcan iguales
y se puedan repartir en procesos solo con `HORUS_ROLES`.

**Contexto:** [ADR-0025](../adr/0025-binario-modular-con-roles.md) (roles, readiness por rol,
eventos siempre por NATS con outbox), [`../conventions.md`](../conventions.md) (layout, DI, config
`HORUS_*`, errores), [`../observability.md`](../observability.md) (formato de log, métricas RED).
**Archivos:** `services/cmd/horus/`, librerías comunes de Go (config, observability, httpx, testkit) en la
ruta de `conventions.md`, `mod:_example` (módulo de ejemplo), `scripts/new-module.sh`, test de
arquitectura de importaciones.

**Criterios de aceptación**
1. **Dado** `scripts/new-module.sh demo`, **cuando** ejecuto `horus --roles=demo`, **entonces**
   expone `/healthz`, `/readyz` (con el estado de cada rol activo) y `/metrics` y escribe logs
   JSON con `trace_id`, `role` y `tenant_id` (vacío si no aplica).
2. **Dado** SIGTERM, **cuando** el servicio tiene peticiones en curso, **entonces** las termina
   dentro del plazo configurado y sale con código 0.
3. **Dado** que una dependencia de un rol no responde, **cuando** consulto `/readyz`,
   **entonces** ese rol aparece degradado con el nombre de la dependencia, los demás roles siguen
   listos y `/healthz` sigue en `200`.
4. **Dado** un módulo que importa el `internal/` de otro, **cuando** corre el test de arquitectura,
   **entonces** falla.

**Hecho cuando:** `go test -race ./...` y el test de arquitectura en verde.

### I0-05 · Contratos v0 (C1–C10) y gate G0
- **Agente:** INT · **Área:** backend, data, frontend · **Talla:** M · **Épica:** EP-00
- **Depende de:** documentos de ronda 2 · **Bloquea:** Ola 1

**Como** agente de IA **quiero** contratos versionados y aprobados **para** trabajar en paralelo
contra mocks sin rehacer trabajo.

**Contexto:** lista de contratos y dueños en [`team.md`](team.md) §3; fuentes: `architecture.md`,
`services.md`, `database.md`, `traffic-model.md`, `events.md`, `api.md`, `security.md`,
[`../frontend.md`](../frontend.md) §6, [`../vendors/mikrotik.md`](../vendors/mikrotik.md). El
alcance de los contratos es **solo el de I0–I1** (ver `increment-1.md`).
**Archivos:** `packages/schemas/openapi/v0/*.yaml`, `packages/events/v0/*.json`,
`packages/schemas/dashboard/v0/*.json`, `packages/schemas/finding/v0/*.json`,
`infrastructure/clickhouse/contract-v0.sql` (DDL de referencia), `.github/CODEOWNERS`.

**Criterios de aceptación**
1. **Dado** C5, **cuando** se valida con un linter de OpenAPI, **entonces** no hay errores y
   **todos** los endpoints con datos de ISP llevan el ISP en el ámbito (ruta o token) según
   [`../api.md`](../api.md).
2. **Dado** C4 y C8, **cuando** se valida cada ejemplo contra su JSON Schema, **entonces**
   todos pasan y cada evento lleva `tenant_id`.
3. **Dado** C9, **cuando** se validan los documentos de las plantillas "NOC del ISP" y "Seguridad"
   y la `config` de cada widget contra el `config_schema` de su tipo, **entonces** pasan.
4. **Dado** un contrato que contradice un documento de `docs/`, **cuando** INT lo detecta,
   **entonces** abre una pregunta en `docs/open-questions/` con `needs:persona` y el contrato
   sigue el documento (no inventa).
5. **Dado** el PR de congelación, **cuando** la persona lo aprueba, **entonces** se etiqueta
   `contracts-v0` y la Ola 1 se abre.

**Hecho cuando:** `make contracts-check` (lint OpenAPI, validación de esquemas y ejemplos, DDL
aplicable en ClickHouse vacío) en verde y PR aprobado por la persona.

### I0-06 · Modelo multi-tenant en PostgreSQL y superadmin semilla
- **Agente:** CORE · **Área:** backend, data, security · **Talla:** M · **Épica:** EP-23, EP-05
- **Depende de:** I0-02, I0-05 (C2)

**Como** superadministrador de plataforma **quiero** que la jerarquía ISP → nodo → router →
prefijos de clientes exista desde la primera migración **para** que añadir ISP no requiera
migraciones (D6).

**Contexto:** [`../database.md`](../database.md) (tenant, nodo, router, realm, cliente-IP,
usuarios, roles, membresías); [ADR-0017](../adr/0017-multi-tenant-desde-v1.md) (multi-tenant, RLS),
[ADR-0018](../adr/0018-la-ip-es-el-cliente.md); D1 y D6. Tenants y membresías son del módulo
`auth`; nodos, routers, realms y clientes de `devices` ([ADR-0025](../adr/0025-binario-modular-con-roles.md)).
**Archivos:** migraciones de `mod:auth` y `mod:devices`, `scripts/seed/`.

**Criterios de aceptación**
1. **Dado** una base vacía, **cuando** aplico las migraciones, **entonces** existen las tablas del
   contrato C2 con `tenant_id NOT NULL` en todas las tablas de datos de ISP.
2. **Dado** una tabla con `tenant_id`, **cuando** corre el test de arquitectura, **entonces**
   comprueba que tiene RLS activado según `database.md`; una consulta con el rol de la aplicación
   sin contexto de tenant no devuelve filas.
3. **Dado** un nodo con `client_prefix` `10.20.0.0/24`, **cuando** intento añadir `10.20.0.128/25` al
   mismo realm, **entonces** se rechaza por solapamiento; en el realm privado de otro nodo se acepta.
4. **Dado** `make seed`, **cuando** termina, **entonces** existe un superadmin cuya contraseña
   inicial se lee de `HORUS_SEED_ADMIN_PASSWORD_FILE` y se exige cambiarla en el primer login.
5. **Dado** una migración, **cuando** corre squawk, **entonces** no hay errores; y la migración es
   reversible (`down` probado en CI).

**Hecho cuando:** `make test-migrations` (aplicar, revertir, reaplicar, squawk) en verde.

### I0-07 · Autenticación mínima definitiva
- **Agente:** CORE · **Área:** backend, security · **Talla:** M · **Épica:** EP-03
- **Depende de:** I0-04, I0-06

**Como** operador NOC **quiero** iniciar sesión con usuario y contraseña **para** acceder solo a
los ISP que me corresponden.

**Contexto:** [`../security.md`](../security.md) (Argon2id, tokens, almacenamiento en la SPA,
bloqueo progresivo, catálogo de permisos C7); [`../api.md`](../api.md) (formato de error).
Fuera: TOTP, sesiones con UI, recuperación por email (I2).
**Archivos:** `mod:auth`, `mod:gateway` (validación de token).

**Criterios de aceptación**
1. **Dado** un usuario activo, **cuando** hace login con credenciales válidas, **entonces** recibe
   un access token corto (claims con `sub`, ISPs accesibles o `superadmin`) y un refresh opaco en
   cookie `HttpOnly`.
2. **Dado** un refresh usado dos veces, **cuando** llega el segundo uso, **entonces** se revoca la
   familia de tokens (detección de reutilización) y responde `401`.
3. **Dado** `GET /me`, **cuando** lo llama un usuario con acceso a 2 ISP, **entonces** devuelve
   ambos ISP con su rol y permisos efectivos en cada uno.
4. **Dado** 5 intentos fallidos, **cuando** llega el sexto, **entonces** se aplica el bloqueo
   progresivo de `security.md` y se registra el evento.
5. **Dado** cualquier respuesta de error, **cuando** se inspecciona, **entonces** no revela si el
   usuario existe.

**Hecho cuando:** `make test-auth` (unit + integración con PostgreSQL de compose) en verde.

### I0-08 · Aislamiento por ISP y suite de matriz de tenants
- **Agente:** CORE · **Área:** backend, security · **Talla:** M · **Épica:** EP-23, EP-04
- **Depende de:** I0-07

**Como** administrador de un ISP **quiero** la garantía de que otro ISP nunca ve mis datos
**para** poder usar una instalación compartida (D6).

**Contexto:** D6, [ADR-0017](../adr/0017-multi-tenant-desde-v1.md); roles `superadmin`,
`isp_admin`, `isp_operator`, `isp_viewer` (nombres finales en C7); regla de `404` en lugar de
`403` para recursos de otro ISP ([`README.md`](README.md) §4).
**Archivos:** librería común de autorización, `mod:gateway`, `tests/tenancy/`.

**Criterios de aceptación**
1. **Dado** el middleware de tenant, **cuando** una petición llega sin ámbito de ISP a un endpoint
   de datos de ISP, **entonces** responde `400` y nunca consulta la base.
2. **Dado** un usuario solo del ISP A, **cuando** pide cualquier recurso del ISP B (por ID directo
   o listando), **entonces** recibe `404` o una lista vacía.
3. **Dado** la suite `tests/tenancy`, **cuando** se añade un endpoint nuevo al OpenAPI, **entonces**
   la suite lo descubre del contrato y falla si no tiene caso de aislamiento declarado.
4. **Dado** un superadmin, **cuando** accede a datos de un ISP, **entonces** se permite y queda
   registrado con actor, ISP y recurso.

**Hecho cuando:** `make test-tenancy` en verde y enganchado como check requerido en CI.

### I0-09 · Inventario mínimo: ISP, nodo, router y prefijos de clientes (`client_prefix`)
- **Agente:** CORE · **Área:** backend · **Talla:** M · **Épica:** EP-05, EP-23
- **Depende de:** I0-08

**Como** administrador del ISP **quiero** registrar mis nodos y el router principal de cada uno con
sus prefijos de clientes **para** que Horus sepa de dónde vienen los flujos y qué IPs son clientes.

**Contexto:** D1, D6, D10; endpoints de C5. La IP de túnel (identidad del exportador) la asigna
el módulo `wireguard` en I1 (I1-01); aquí el router queda "Pendiente de configurar".
**Archivos:** `mod:devices` (tenants: `mod:auth`).

**Criterios de aceptación**
1. **Dado** un superadmin, **cuando** crea un ISP, **entonces** se crea con slug único y puede
   asignarle un `isp_admin`; **dado** un `isp_admin`, **cuando** intenta crear un ISP, **entonces** `403`.
2. **Dado** un `isp_admin`, **cuando** registra un router en un nodo, **entonces** queda en estado
   "Pendiente de configurar" y se publica el evento de alta (C4) que consumirá `wireguard`.
3. **Dado** un nodo, **cuando** se declaran sus prefijos (`client_prefix`, IPv4 e IPv6) con rol
   *customers*, *infrastructure* o *excluded* y asignación estática/dinámica
   ([`../traffic-model.md`](../traffic-model.md) §4.1), **entonces** se validan sin solapes en el realm
   (`CLIENT_PREFIX_OVERLAP`) y se publica `client_prefix.*` (C4). Sin prefijos, el nodo queda en
   "modo descubrimiento" (I1-29).
4. **Dado** un nodo, **cuando** intento registrar un segundo router principal, **entonces** se
   rechaza (un router principal por nodo, D6) con error explicativo.
5. **Dado** RouterOS < 7.12 declarado, **cuando** registro el router, **entonces** se acepta con
   aviso "versión no soportada en el primer entregable".

**Hecho cuando:** `make test-devices` y `make test-tenancy` en verde; ejemplos de C5 validados
contra respuestas reales.

### I0-10 · Simulador de flujos IPFIX/NetFlow v9 con escenarios
- **Agente:** FLOW · **Área:** data · **Talla:** M · **Épica:** EP-T4, EP-10
- **Depende de:** I0-01

**Como** agente de IA **quiero** un generador determinista de flujos con el formato de RouterOS
**para** desarrollar y probar ingesta, descubrimiento y detección sin hardware.

**Contexto:** campos y plantillas de RouterOS en [`../vendors/mikrotik.md`](../vendors/mikrotik.md)
§2.3; parámetros (`active-flow-timeout=1m`, `inactive=15s`); volumen §2.5; señales de §6.
**Archivos:** `tools/flowsim/`, `tools/flowsim/scenarios/*.yaml`.

**Criterios de aceptación**
1. **Dado** `make sim SCENARIO=normal SEED=1 RATE=2000`, **cuando** corre 5 min, **entonces**
   envía IPFIX (y con `PROTO=v9`, NetFlow v9) con plantillas reenviadas periódicamente,
   secuencia correcta y desde una IP de origen configurable (la IP de túnel del router simulado).
2. **Dado** los escenarios `normal`, `scan`, `c2`, `spam`, `dos_out`, `commercial`, **cuando** se
   generan, **entonces** cada uno produce un archivo `expected.json` con los clientes y los
   hallazgos esperados (vacío de hallazgos en `normal`).
3. **Dado** la misma semilla, **cuando** genero dos veces, **entonces** la salida es idéntica byte
   a byte (salvo timestamps relativos al arranque).
4. **Dado** `NAT=true`, **cuando** se genera, **entonces** las IPs de cliente son privadas en
   ambos sentidos (comportamiento esperado de §2.4; se ajusta si I0-12 demuestra otra cosa).
5. **Dado** IPv6 activado, **cuando** se genera, **entonces** incluye clientes con prefijo
   delegado.

**Hecho cuando:** `make test-flowsim` (decodifica la salida con una librería independiente y la
compara con `expected.json`) en verde.

### I0-11 · Laboratorio MikroTik CHR reproducible
- **Agente:** PLAT · **Área:** infra · **Talla:** M · **Épica:** EP-T4, EP-26
- **Depende de:** I0-02

**Como** agente de IA **quiero** un MikroTik virtual con clientes simulados **para** comprobar el
comportamiento real de RouterOS sin depender del router de la persona.

**Contexto:** opciones y topología en [`../vendors/mikrotik.md`](../vendors/mikrotik.md) §8.1–8.2:
CHR en QEMU/KVM (licencia free, 1 Mbit/s de subida por interfaz: solo pruebas funcionales),
clientes como *network namespaces* o contenedores, `internet-sim` con servidores HTTP/DNS/"C2" de
prueba, NAT masquerade en `ether1`, segundo CHR con servidor PPPoE. Versiones: **7.12** y última
long-term. Requiere `/dev/kvm`; la CI **no** lo usa (usa fixtures de I0-12).
**Archivos:** `infrastructure/lab/chr/`, `scripts/lab/`, `Makefile` (`lab-up`, `lab-down`,
`lab-traffic`).

**Criterios de aceptación**
1. **Dado** una máquina con KVM, **cuando** ejecuto `make lab-up ROS=7.12`, **entonces** arranca
   el CHR con la topología de §8.2 y es accesible por consola en < 3 min.
2. **Dado** `make lab-traffic PROFILE=scan`, **cuando** corre, **entonces** un cliente del
   laboratorio genera el patrón (beacon, escaneo, SMTP o volumen según el perfil).
3. **Dado** una máquina sin KVM, **cuando** ejecuto `make lab-up`, **entonces** falla con un
   mensaje claro y sugiere usar los fixtures.
4. **Dado** que la imagen CHR se descarga, **cuando** se verifica, **entonces** se comprueba el
   checksum publicado por MikroTik y la imagen no se guarda en Git.

**Hecho cuando:** `scripts/lab/selftest.sh` en una máquina con KVM (la del agente o la de la
persona) termina en verde y su salida se adjunta al PR.

### I0-12 · Verificar los puntos "a verificar" y grabar fixtures
- **Agente:** FLOW (con PERSONA para el router real) · **Área:** data · **Talla:** M
- **Épica:** EP-T4, EP-10 · **Depende de:** I0-11 · **needs:** hardware (parte opcional)

**Como** agente de IA **quiero** confirmar en el laboratorio el comportamiento de RouterOS que
`vendors/mikrotik.md` marca como "a verificar" **para** congelar contratos sobre hechos y no sobre
supuestos.

**Contexto:** puntos marcados en [`../vendors/mikrotik.md`](../vendors/mikrotik.md): §0 y §2.4 (con
NAT en el mismo router, ¿los flujos llevan la IP privada del cliente en subida y bajada?), §2.3
(nombres de campos IPFIX por versión), §6 (campo TTL), §2.7 (tamaño máximo de datagrama IPFIX vs
MTU 1420 del túnel; ruta de lectura de L3HW), §2.6 (cómo se comunica la tasa de muestreo), §7
(sintaxis del script en 7.12 y en la última long-term). Lista de validación §8.3, puntos 1, 3, 4 y 9.
**Archivos:** `tools/flowsim/fixtures/routeros-<versión>/` (pcaps IPFIX y v9, IPv4 e IPv6,
salidas `print oid`, JSON de REST), `scripts/capture.sh` (`make capture`), PR a
`docs/vendors/mikrotik.md` con los resultados (revisa INT; la sección queda sin "a verificar").

**Criterios de aceptación**
1. **Dado** el CHR con NAT y un cliente `10.20.0.10`, **cuando** navega a `internet-sim`,
   **entonces** el pcap registrado muestra si `src` de subida y `dst` de bajada son `10.20.0.10`;
   el resultado (sí/no) se documenta y, si es "no", se abre `needs:persona` y se ajusta I0-10.
2. **Dado** las dos versiones de RouterOS, **cuando** se exporta IPFIX y NetFlow v9, **entonces**
   se guardan fixtures por versión y protocolo con un `README` que lista los campos presentes.
3. **Dado** el script de onboarding de §7 con placeholders resueltos, **cuando** se pega en un CHR
   limpio de cada versión, **entonces** no da errores de sintaxis (o se corrige el script en
   `vendors/mikrotik.md`).
4. **Dado** fixtures con IPs de laboratorio, **cuando** se revisan, **entonces** no contienen IPs ni
   datos de clientes reales. **Dado** una captura del router real de la persona (`make capture`,
   opcional), **cuando** se procesa, **entonces** se anonimiza (prefijos reescritos de forma
   consistente) antes de cualquier uso fuera de su máquina y nunca se sube sin anonimizar.

**Hecho cuando:** fixtures en el repo, `make test-fixtures` (decodifica todos los pcaps y verifica
los campos documentados) en verde, y PR de `vendors/mikrotik.md` mergeado.

### I0-13 · Esquema ClickHouse v0 y migraciones
- **Agente:** FLOW · **Área:** data · **Talla:** M · **Épica:** EP-10, EP-19
- **Depende de:** I0-02, I0-05 (C3)

**Como** agente de IA **quiero** el esquema de flujos y agregados con TTL desde la primera
migración **para** que el disco no se llene y los detectores tengan tablas estables.

**Contexto:** [`../database.md`](../database.md) (tablas de flujos, `ORDER BY` con `tenant_id`
primero, agregados), [`../traffic-model.md`](../traffic-model.md) (lado cliente, dirección,
atribución), [`../storage.md`](../storage.md) (retenciones, almacenamiento local por D2).
**Archivos:** `infrastructure/clickhouse/migrations/`, `mod:ingester` (solo tipos del esquema),
`scripts/clickhouse-migrate.sh`.

**Criterios de aceptación**
1. **Dado** ClickHouse vacío, **cuando** ejecuto `make ch-migrate`, **entonces** se crean las tablas
   de C3 con `tenant_id` como primera columna del `ORDER BY` y TTL en el crudo.
2. **Dado** un lote de prueba en el crudo, **cuando** se inserta, **entonces** las vistas
   materializadas pueblan los agregados (5 min y 1 h como mínimo) y los totales coinciden.
3. **Dado** una fila con fecha anterior al TTL, **cuando** se fuerza `OPTIMIZE … FINAL`, **entonces**
   desaparece.
4. **Dado** una consulta de agregados sin filtro de `tenant_id`, **cuando** se ejecuta con el
   usuario de lectura de la aplicación, **entonces** se rechaza o la suite de tenancy lo detecta
   (según el mecanismo que fije `database.md`).

**Hecho cuando:** `make test-clickhouse` (migrar, insertar fixtures, comprobar agregados y TTL) en verde.

### I0-14 · Proyecto Nuxt, login y layout
- **Agente:** UI · **Área:** frontend · **Talla:** M · **Épica:** EP-02, EP-T3
- **Depende de:** I0-01

**Como** operador NOC **quiero** entrar en Horus y ver una navegación clara **para** llegar a lo que
necesito sin pensar.

**Contexto:** [`../frontend.md`](../frontend.md) §4 (navegación), §5 (layout, tema), §13
(principios visuales), §14 (accesibilidad), §16 (arquitectura técnica); Nuxt UI con SPA
(`ssr: false`); i18n `es` por defecto; skills `.claude/skills/apple-design/SKILL.md` y
`.claude/skills/apple-hig/SKILL.md`.
**Archivos:** `apps/frontend/`.

**Criterios de aceptación**
1. **Dado** la pantalla de login, **cuando** envío credenciales válidas (mock), **entonces** llego
   al Resumen; con inválidas, error en contexto sin revelar si el usuario existe.
2. **Dado** el layout, **cuando** cambio entre tema Sistema/Claro/Oscuro, **entonces** todos los
   tokens cambian y axe no reporta violaciones serias en ninguno.
3. **Dado** 320 px de ancho, **cuando** abro el Resumen, **entonces** no hay scroll horizontal de
   página.
4. **Dado** un usuario sin un permiso, **cuando** se carga la navegación, **entonces** la sección se
   oculta (no se deshabilita).

**Hecho cuando:** `pnpm -C apps/frontend test && pnpm -C apps/frontend e2e --grep @i0` en verde
con capturas claro/oscuro adjuntas.

### I0-15 · Selector de ISP, cliente API generado y mocks
- **Agente:** UI · **Área:** frontend · **Talla:** M · **Épica:** EP-02, EP-23
- **Depende de:** I0-14, I0-05 (C5)

**Como** usuario con acceso a varios ISP **quiero** cambiar de ISP sin perder el contexto y saber
siempre en cuál estoy **para** no actuar sobre el ISP equivocado.

**Contexto:** [`../frontend.md`](../frontend.md) §3 (multi-tenant en la UI); cliente
`openapi-fetch` generado desde C5; mocks con datos derivados del simulador.
**Archivos:** `apps/frontend/app/composables/{useApi,useAuth,useTenant}.ts`,
`apps/frontend/app/components/tenant/`, `apps/frontend/types/api/`, `apps/frontend/mocks/`.

**Criterios de aceptación**
1. **Dado** un usuario con un solo ISP, **cuando** entra, **entonces** no ve selector, solo el
   nombre del ISP en la cabecera de la barra lateral.
2. **Dado** un usuario con 3 ISP, **cuando** cambia de ISP, **entonces** la URL cambia de prefijo
   (`/t/<slug>/…`), se conserva la sección actual si existe en el nuevo ISP y se recuerda como
   último ISP usado.
3. **Dado** una URL de un ISP sin acceso, **cuando** la abro, **entonces** veo "No encontrado"
   (nunca "sin permiso").
4. **Dado** `HORUS_UI_MOCKS=1`, **cuando** arranco el frontend, **entonces** funciona entero contra
   mocks generados del OpenAPI; sin la variable, contra el gateway.
5. **Dado** una respuesta `401`, **cuando** el refresh funciona, **entonces** la petición se repite
   una vez de forma transparente; si falla, vuelve al login conservando la ruta.

**Hecho cuando:** `pnpm -C apps/frontend test && pnpm -C apps/frontend e2e --grep @tenant` en verde.

### I0-16 · Marco de widgets v0
- **Agente:** UI · **Área:** frontend · **Talla:** M · **Épica:** EP-24
- **Depende de:** I0-15, I0-05 (C9)

**Como** operador NOC **quiero** dashboards compuestos por widgets **para** que la pantalla de
monitoreo muestre justo lo que necesito (D8).

**Contexto:** [`../frontend.md`](../frontend.md) §6 (contrato de widget en dos mitades: catálogo
`GET /widget-types` y datos resueltos en servidor según [`../api.md`](../api.md) §2.11, manifiesto de
presentación en el frontend; grilla, plantillas, estados por widget) y §7 (requisitos de kiosco que el marco debe permitir: tamaño de texto por
escala, sin hover obligatorio). En I0 la grilla es de **solo lectura**; el editor es de I2.
**Archivos:** `apps/frontend/app/widgets/` (registro + `WidgetHost`), `apps/frontend/app/components/dashboard/`,
`apps/frontend/app/widgets/_example/`.

**Criterios de aceptación**
1. **Dado** un manifiesto de presentación válido (C9) cuyo `type` existe en el catálogo mock de
   `widget-types`, **cuando** se registra, **entonces** el `WidgetHost` lo renderiza; un manifiesto
   inválido o de un tipo inexistente falla en el build, no en ejecución.
2. **Dado** un documento de dashboard (C9, `widgets[].position`), **cuando** se renderiza, **entonces** cada widget ocupa su
   posición en la grilla de 12 columnas y se adapta según §5.2 de `frontend.md`.
3. **Dado** un widget cuya fuente falla, **cuando** se renderiza, **entonces** solo ese widget
   muestra su estado de error con "Reintentar"; el resto del dashboard sigue funcionando.
4. **Dado** un widget, **cuando** se renderiza con `scale=wall`, **entonces** usa la escala
   tipográfica de pantalla mural y ninguna información depende de hover.
5. **Dado** un widget de ejemplo con datos en vivo (mock), **cuando** llega un dato, **entonces**
   muestra su frescura ("hace 3 s") según §10.2 de `frontend.md`.

**Hecho cuando:** `pnpm -C apps/frontend test --filter widgets` y la historia de Storybook/Histoire
(o página `/dev/widgets`) con capturas en escala normal y mural.

### I0-17 · Cargadores de datasets ASN y feeds de reputación
- **Agente:** SEC · **Área:** data, security · **Talla:** M · **Épica:** EP-11, EP-13
- **Depende de:** I0-01

**Como** analista de seguridad del ISP **quiero** que Horus tenga listas actualizadas de IP→ASN y
de C2/botnets conocidos **para** que la detección se apoye en fuentes trazables.

**Contexto:** fuentes y licencias en [`../traffic-model.md`](../traffic-model.md) y
[`../open-questions/data.md`](../open-questions/data.md) Q9; feeds abiertos (p. ej. listas de C2 de
abuse.ch, Spamhaus DROP/EDROP) **solo si su licencia permite uso comercial** (P-14); D5.
Almacenamiento local (D2).
**Archivos:** `mod:detection` (feeds), `mod:traffic` (dataset IP→ASN→organización: SEC aporta el
código en un PR que revisa FLOW, dueño del módulo), `config/feeds.yaml`.

**Criterios de aceptación**
1. **Dado** `config/feeds.yaml`, **cuando** cada fuente se declara, **entonces** incluye URL,
   licencia, uso comercial permitido (sí/no), frecuencia y formato; las de "no" no se descargan.
2. **Dado** una descarga, **cuando** termina, **entonces** se guarda versionada con checksum y
   fecha; si falla, se conserva la última versión válida y se emite una métrica de antigüedad.
3. **Dado** un archivo corrupto o vacío, **cuando** se procesa, **entonces** se rechaza sin
   reemplazar la versión vigente.
4. **Dado** una IP de prueba presente en un fixture de feed, **cuando** se consulta la librería de
   búsqueda, **entonces** devuelve la fuente, la categoría (C2, spam, escáner…) y la fecha en < 1 µs
   de mediana (estructura en memoria).

**Hecho cuando:** `make test-feeds` (con fixtures locales, sin Internet) en verde.

### I0-18 · Observabilidad mínima
- **Agente:** PLAT · **Área:** infra · **Talla:** S · **Épica:** EP-T1 · **Depende de:** I0-04

**Como** ingeniero de plataforma **quiero** métricas y logs de todos los procesos desde el primer
día **para** diagnosticar sin entrar en los contenedores.

**Contexto:** [`../observability.md`](../observability.md); perfil opcional para no cargar el
servidor de la persona.
**Archivos:** `deployments/compose/compose.observability.yaml`, `infrastructure/grafana/`.

**Criterios de aceptación**
1. **Dado** `make up PROFILE=observability`, **cuando** levanta, **entonces** Prometheus raspa
   todos los `/metrics` y Grafana tiene un panel "Ingesta" y otro "API" aprovisionados.
2. **Dado** el perfil desactivado, **cuando** arranca el sistema, **entonces** nada falla por su
   ausencia.

**Hecho cuando:** `scripts/ci/observability-smoke.sh` en verde.

### I0-19 · `make accept-i0` y e2e de humo
- **Agente:** INT · **Área:** infra, frontend · **Talla:** S · **Épica:** EP-T4
- **Depende de:** I0-09, I0-15

**Como** persona responsable **quiero** un comando que compruebe el incremento completo **para**
aceptar I0 sin revisar código.

**Contexto:** demostración de I0 en la cabecera de este archivo y en
[`../roadmap.md`](../roadmap.md) §3.
**Archivos:** `tests/acceptance/i0/`, `Makefile`.

**Criterios de aceptación**
1. **Dado** un clon limpio, **cuando** ejecuto `make up && make accept-i0`, **entonces** se
   comprueba: login del superadmin, alta de ISP/nodo/router con prefijos, aislamiento entre dos ISP
   de prueba, `make sim-verify` de los seis escenarios, esquema ClickHouse migrado y e2e de UI (login → selector →
   dashboard vacío).
2. **Dado** un paso que falla, **cuando** termina, **entonces** el informe dice qué paso, con qué
   salida y qué historia lo cubre.

**Hecho cuando:** job nocturno `accept-i0` en verde dos noches seguidas.
