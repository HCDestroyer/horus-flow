# Horus Flow — Convenciones de ingeniería

> Estado: **propuesta ronda 2** (aplica [`po-decisions.md`](po-decisions.md) D3, D6, D7, D9 y los ADR 0017–0025) ·
> Responsable: Agente C. Desarrollo por **agentes de IA + 1 persona** (D7, [ADR-0023](adr/0023-entrega-por-incrementos-y-equipo-ia.md));
> entrega por **incrementos** en lugar de sprints (D9); **binario modular** ([ADR-0025](adr/0025-binario-modular-con-roles.md)).
> Fuente: [`vision.md`](vision.md) §1, §2, §10–13. Relacionados: [`architecture.md`](architecture.md)
> y [`services.md`](services.md) (Agente 1), [`api.md`](api.md) y [`events.md`](events.md)
> (Agente 3), [`database.md`](database.md) (Agente 2), [`security.md`](security.md),
> [`observability.md`](observability.md), [`backlog/`](backlog/) (Agente 5).

Este documento fija **cómo se escribe, prueba, integra y publica** el código. Las convenciones
de API REST, errores y eventos son del Agente 3; las de esquema de BD, del Agente 2.

## 0. Resumen de decisiones

| Tema | Decisión |
|------|----------|
| Módulo Go | **Un solo módulo** `github.com/hcdestroyer/horus-flow` en la raíz (con `go.work` solo si un servicio necesita otro ciclo de dependencias) |
| Binario | **Un binario `horus`** (`services/cmd/horus`) que compone los módulos según `HORUS_ROLES`; **una imagen** ([ADR-0025](adr/0025-binario-modular-con-roles.md)) |
| Layout de módulo | `services/<módulo>/{api,internal/{domain,app,adapters}}`; cableado manual en `services/cmd/horus` |
| DI | Constructores explícitos, sin `wire`/`fx`/`dig` |
| Config | Variables de entorno 12-factor con prefijo `HORUS_`, `_FILE` para secretos, `caarlos0/env` |
| Frontend | Nuxt 4 en modo **SPA** (`ssr: false`), estático tras el reverse proxy; gateway como BFF |
| Cliente API | Generado del OpenAPI con `openapi-typescript` + `openapi-fetch` |
| Lint/format | golangci-lint v2 (+ gofumpt, goimports), ESLint (`@nuxt/eslint`) + Prettier, `vue-tsc`, buf, hadolint, actionlint, squawk |
| Tests | `go test -race`; testcontainers-go; contrato OpenAPI/buf/JSON Schema; Playwright; k6 |
| Git | **Trunk-based**, una rama corta por agente y tarea (`agent/<agente>/<tarea>`), PR revisado por **otro agente** + CI obligatorio + persona sólo en áreas sensibles (CODEOWNERS); merge queue; squash; Conventional Commits (§6) |
| Definición de Terminado | **Verificada por máquina** (job `dod`), no por casillas marcadas a mano (§11) |
| Versionado | **Versión única de producto** SemVer para el monorepo (release-please); un `0.<incremento>.z` por incremento |
| CI | GitHub Actions; detección de **módulos** afectados por grafo de dependencias Go + filtros de rutas; tests de arquitectura y de aislamiento de tenant siempre |
| Imágenes | **Una** imagen `horus` multi-stage → `distroless/static:nonroot` (+ imagen del frontend); tags `vX.Y.Z` y `sha-<7>`; despliegue por digest |

## 1. Estructura del monorepo

```
horus-flow/
├── go.mod / go.sum                # módulo único Go
├── apps/frontend/                 # Nuxt 4
├── services/
│   ├── cmd/horus/                 # main único: lee HORUS_ROLES y compone los módulos
│   └── <módulo>/                  # auth, devices, wireguard, snmp, flows (collector+ingester), traffic,
│                                  # detection, alerts, analytics, reporting, jobs, gateway, wgagent
├── packages/
│   ├── protobuf/                  # .proto + buf.yaml (contratos internos y payloads de eventos)
│   ├── events/                    # catalog/<dominio>.yaml, streams/, examples/ (events.md)
│   ├── schemas/openapi/           # una spec por módulo + gateway-routes.yaml + bundle
│   └── go/                        # librerías de plataforma: observability, authz (TenantScope), config,
│                                  # natsx (sobre + Horus-Tenant), crypto/envelope, httpx, grpcx, testkit,
│                                  # archtest (tests de arquitectura), tenanttest (batería de aislamiento)
├── infrastructure/ deployments/ scripts/ (scripts/dr/, scripts/ci/) docs/
└── .github/ (workflows, CODEOWNERS, pull_request_template.md)
```

Reglas (verificadas por tests de arquitectura, §5): `packages/go/*` **no** contiene lógica de dominio; un módulo sólo
importa el paquete **`api/`** (contrato) de otro, nunca su `internal/`; `domain` no importa adaptadores.

**¿Por qué un solo módulo Go y un solo binario?** Un solo `go.sum`, refactors atómicos, detección de afectados con
`go list -deps`, una imagen que operar (D7). Separar un rol en otro contenedor es cambiar `HORUS_ROLES`, no código.

## 2. Módulo Go

### 2.1 Layout

```
services/devices/
├── api/                         # contrato público: interfaces generadas de Protobuf, tipos de eventos
├── internal/
│   ├── config/config.go         # struct Config del módulo con tags env, Validate()
│   ├── domain/                  # entidades, value objects, errores de dominio, reglas puras (sin I/O)
│   ├── app/                     # casos de uso (orquestan dominio + puertos); ports.go con las interfaces
│   └── adapters/                # http (Chi), grpc, postgres (con TenantScope + SET LOCAL), nats, clickhouse,
│                                # vendor/mikrotik …
├── migrations/                  # SQL versionado con nombre por marca de tiempo UTC (evita choques entre agentes)
└── README.md                    # propósito, env vars, métricas, eventos que publica/consume, roles
```

Dependencias permitidas: `adapters → app → domain`; `adapters` implementa interfaces de `app/ports.go`. Cada
módulo expone `func Register(ctx, deps) (Module, error)` y `services/cmd/horus` sólo arranca los módulos de sus roles.

### 2.2 Inyección de dependencias

Cableado manual y explícito en `main`:

```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()
    if err := run(ctx, os.Getenv); err != nil {
        slog.Error("service failed", "error", err)
        os.Exit(1)
    }
}

func run(ctx context.Context, getenv func(string) string) error {
    cfg, err := config.Load(getenv)          // falla rápido si falta algo
    // obs, db, nats := ... (cada uno con Close diferido)
    // repo := postgres.NewRouterRepository(db)
    // svc  := app.NewService(repo, publisher, clock)
    // srv  := grpc.NewServer(svc, authz.Verifier(...))
    // errgroup: servidor gRPC, servidor admin, consumidores; shutdown ordenado
}
```

- Constructores `NewX(deps...) *X` con dependencias explícitas; sin variables globales
  (salvo el logger por defecto configurado en `main`), sin `init()` con efectos.
- Interfaces **pequeñas y definidas por el consumidor** (en `app/ports.go`), no por el
  implementador.
- Reloj e IDs inyectables (`Clock`, `IDGenerator` UUIDv7) para tests deterministas.

### 2.3 Configuración (12-factor)

- Solo variables de entorno; sin archivos de config obligatorios.
- Nombres: `HORUS_<CLAVE>` en mayúsculas; las comunes son idénticas en todos los servicios:
  `HORUS_ENV` (`dev|staging|prod`), `HORUS_LOG_LEVEL`, `HORUS_LOG_FORMAT` (`json|text`),
  `HORUS_GRPC_ADDR`, `HORUS_HTTP_ADDR`, `HORUS_ADMIN_ADDR`, `HORUS_POSTGRES_DSN`,
  `HORUS_NATS_URL`, `HORUS_VALKEY_URL`, `HORUS_CLICKHOUSE_DSN`, **`HORUS_ROLES`** (lista de roles
  del proceso), **`HORUS_DATA_DIR`** (almacén local, [ADR-0019](adr/0019-almacenamiento-local-y-destino-remoto.md)), las de mTLS `HORUS_TLS_CA_FILE`, `HORUS_TLS_CERT_FILE`,
  `HORUS_TLS_KEY_FILE` (gRPC interno, [`security.md`](security.md) §5.3), más las `OTEL_*`
  estándar.
- Secretos: variante `_FILE` (p. ej. `HORUS_POSTGRES_PASSWORD_FILE=/run/secrets/devices_pg_password`)
  que tiene prioridad; nunca secretos en la línea de comandos ni en logs.
- Librería: `github.com/caarlos0/env/v11` (tags `env:"..." envDefault:"..." required`) +
  `Validate()` propio. Al arrancar se loguea la configuración efectiva **sin secretos**.

### 2.4 Errores

- Envolver con contexto: `fmt.Errorf("create router %s: %w", id, err)`; comparar con
  `errors.Is/As`; nunca comparar strings.
- Errores de dominio como valores centinela o tipos (`domain.ErrNotFound`,
  `*domain.ValidationError{Field, Reason}`).
- Mapeo a gRPC (`codes.NotFound`, `InvalidArgument`, `PermissionDenied`, `FailedPrecondition`,
  `Unavailable`, `Internal`) en `adapters/grpc`; el gateway mapea a HTTP + Problem Details
  (formato del Agente 3 en [`api.md`](api.md)). Mensajes internos nunca llegan al cliente.
- Un error se loguea **una sola vez** (en el borde que lo maneja). Prohibido `panic` para
  control de flujo; recuperación de pánicos en interceptores/handlers y por paquete UDP.

### 2.5 Contexto, concurrencia y apagado

- `context.Context` es el primer parámetro de toda función con E/S; nunca se guarda en structs.
- Toda llamada saliente tiene timeout (por defecto: BD 3 s, gRPC 5 s, SNMP 2 s; configurables).
- Goroutines siempre con dueño (`errgroup`) y cancelables; prohibidas las "fire and forget".
- Apagado: al `SIGTERM` → `/readyz` 503 → esperar 5 s → dejar de aceptar → drenar en ≤ 25 s
  (compatible con `terminationGracePeriodSeconds: 30` de Kubernetes) → cerrar NATS/BD.
- Concurrencia acotada (worker pools, semáforos) en colectores; nunca una goroutine por
  paquete/router sin límite.

### 2.6 Tiempo, identificadores y contadores

- Todo en **UTC**. En Go, `time.Time` siempre normalizado con `.UTC()` antes de persistir o
  serializar; prohibido `time.Local`.
- JSON/API: RFC 3339 con milisegundos y `Z` (`2026-10-07T22:31:04.512Z`) — formato exacto lo fija
  el Agente 3 en [`api.md`](api.md).
- PostgreSQL: `timestamptz`.
- ClickHouse: **validado** el criterio del Agente 2 ([`database.md`](database.md)):
  `DateTime64(3, 'UTC')` en tablas crudas/eventos (orden y deduplicación finos) y
  `DateTime('UTC')` (resolución de segundos, 4 bytes) en columnas de *bucket* de tablas agregadas
  (1 min, 1 h, 1 día), porque un bucket siempre cae en segundo exacto: ahorra espacio y acelera
  `GROUP BY` sin pérdida de información. Regla para el código: al consultar o unir crudo con
  agregados, convertir explícitamente (`toStartOfHour(ts)` devuelve `DateTime`); en Go ambos se
  mapean a `time.Time` UTC.
- IDs: UUIDv7 generados en la aplicación (`uuid.NewV7`) salvo que [`database.md`](database.md)
  indique lo contrario.
- Contadores de bytes/paquetes: `uint64` en Go, `UInt64` en ClickHouse; nunca `float` para
  volúmenes.

### 2.7 Persistencia y librerías base

- PostgreSQL: `jackc/pgx/v5` (+ `sqlc` para generar código tipado, recomendado); sin ORM.
- ClickHouse: `ClickHouse/clickhouse-go/v2` con inserción por lotes.
- NATS: `nats-io/nats.go` (API `jetstream`) envuelto por `packages/go/natsx`.
- HTTP: `go-chi/chi/v5` (vision §2); gRPC: `google.golang.org/grpc` + `buf` para generación.
- Logs: `log/slog`. UUIDv7: `github.com/google/uuid` (`uuid.NewV7`).
- Caché/rate limit: `valkey-io/valkey-go` o `redis/go-redis/v9` (ambos compatibles con Valkey).
- mTLS gRPC: `credentials.NewTLS` con `tls.Config{MinVersion: tls.VersionTLS13}` y recarga de
  certificados en caliente (`GetCertificate`) desde `packages/go/grpcx`.
- Versión de Go: la última estable menor (fijada en `go.mod` con `toolchain`), actualizada por
  Renovate.

## 3. Frontend Nuxt 4

### 3.1 Modo de render

**SPA (`ssr: false`)** compilada a estáticos (`nuxi generate`) y servida por el reverse proxy.
Motivos: es una aplicación interna tras login (SEO irrelevante), el gateway ya actúa como BFF
con cookie HttpOnly ([`security.md`](security.md) §5), y elimina un servidor Node en producción
(menos superficie, menos operación). Revisable vía ADR si se necesita SSR.

### 3.2 Estructura (Nuxt 4, directorio `app/`)

```
apps/frontend/
├── app/
│   ├── pages/                  # rutas; mínima lógica, delegan en composables
│   ├── layouts/                # default (shell con menú), auth (login)
│   ├── components/
│   │   ├── ui/                 # wrappers propios sobre Nuxt UI
│   │   ├── charts/             # componentes ECharts (carga diferida)
│   │   └── <dominio>/          # devices/, wireguard/, traffic/ …
│   ├── composables/            # useDevices(), useRealtime(), usePermissions() …
│   ├── middleware/             # auth.global.ts, permission.ts
│   ├── plugins/                # api client, otel, realtime
│   └── utils/
├── shared/                     # tipos compartidos (incluye tipos generados del OpenAPI)
├── tests/ (unit con Vitest) · e2e/ (Playwright)
└── nuxt.config.ts
```

### 3.3 Capas

`pages → composables (estado de la vista, llamadas a API) → api client generado`. Los
componentes de presentación reciben props y emiten eventos; no llaman a la API.
Datos remotos con `useAsyncData`/`useFetch` envolviendo el cliente generado, o con un
composable propio con caché por clave; formularios con validación `zod` (los esquemas pueden
derivarse del OpenAPI).

### 3.4 ¿Cuándo usar Pinia?

Alineado con [`frontend.md`](frontend.md) §13: **sin Pinia** mientras el estado compartido se
limite a sesión (usuario, permisos efectivos, access token en memoria — nunca persistido, ver
[`security.md`](security.md) §5.1), estado del sistema y conexión WebSocket; se resuelve con
composables sobre `useState`. Pinia se introduce (con ADR breve) solo si aparece estado global
complejo con muchas mutaciones cruzadas entre páginas (p. ej. un editor multi-paso del catálogo
de clasificación) o si se necesitan sus devtools/plugins.

Todo lo demás (listas, detalles, filtros de una página) vive en composables o en el estado de
la ruta (query params). Regla: si un estado solo lo usa una página, no es global.

### 3.5 Cliente API generado

- Fuente: el OpenAPI que publica el Agente 3 (`packages/schemas/openapi.yaml`).
- `openapi-typescript` genera tipos en `shared/api/schema.d.ts`; `openapi-fetch` da un cliente
  tipado de ~6 KB sin runtime pesado. Script `pnpm api:generate`; CI falla si el archivo
  generado no coincide (`git diff --exit-code`).
- Plugin `api` añade `Authorization: Bearer <access token>` y `X-Request-Id` opcional; ante 401
  llama una sola vez a `POST /api/v1/auth/refresh` (con `credentials: 'include'` y
  `X-Requested-With: horus`, deduplicando llamadas concurrentes entre peticiones y pestañas con
  `BroadcastChannel`/Web Locks) y reintenta; si falla, redirige a login. 403 → mensaje;
  Problem Details → toast.

### 3.6 WebSocket

- Una **única conexión por pestaña** gestionada por `plugins/realtime` + `useRealtime()`,
  autenticada con ticket de un uso y renovación en banda del token ([`api.md`](api.md) §4).
- Protocolo de mensajes y temas: definido por el Agente 3 en [`api.md`](api.md)/[`events.md`](events.md).
- Reconexión con backoff exponencial + jitter (1 s → 30 s máx.), ping/pong cada 25 s.
- Tras reconectar: re-suscribir y **resincronizar por REST** (los eventos perdidos durante la
  desconexión no se reproducen); los mensajes llevan `event_id` para descartar duplicados.
- Al recibir cierre por sesión revocada (código definido por el Agente 3) → ir a login.
- Suscripciones ligadas al ciclo de vida del componente (`onScopeDispose`).

### 3.7 UI

Nuxt UI + Tailwind; ECharts con `vue-echarts` y *tree-shaking* (`echarts/core`), gráficos
cargados de forma diferida. Accesibilidad AA en componentes propios. i18n con `@nuxtjs/i18n`
(español por defecto) — confirmar con el product owner.

## 4. Linters y formateo

| Ámbito | Herramienta | Configuración |
|--------|-------------|---------------|
| Go formato | `gofumpt` + `goimports` (sección `formatters` de golangci-lint v2) | `local-prefixes: github.com/hcdestroyer/horus-flow` |
| Go lint | `golangci-lint` v2 | Linters: `errcheck`, `govet`, `staticcheck`, `unused`, `ineffassign`, `gosec`, `revive`, `errorlint`, `wrapcheck` (solo en límites de paquete), `bodyclose`, `noctx`, `sqlclosecheck`, `rowserrcheck`, `contextcheck`, `containedctx`, `gocritic`, `misspell`, `unparam`, `exhaustive`, `nilerr`, `depguard` (capas, §2.1), `forbidigo` (prohíbe `fmt.Print*`, `log.Print*`, `panic` fuera de main/tests), `sloglint` (claves `snake_case`, sin mezclar estilos), `promlinter` |
| Protobuf | `buf lint` (estilo `STANDARD`) + `buf breaking --against '.git#branch=main'` | `buf.yaml` en `packages/protobuf` |
| TS/Vue | ESLint flat config con `@nuxt/eslint` + `eslint-config-prettier`; reglas: `vue/no-v-html: error`, `no-console: warn` | — |
| Formato TS/Vue/JSON/YAML/MD | Prettier | `printWidth: 100`, comillas simples, sin `;` (decisión de estilo, cerrada) |
| Tipos | `vue-tsc --noEmit` / `nuxi typecheck` | `strict: true` |
| SQL migraciones | `squawk` (detecta migraciones peligrosas en PostgreSQL: locks, `NOT NULL` sin default…) | — |
| Dockerfiles | `hadolint` | — |
| Workflows | `actionlint` + `zizmor` (seguridad de Actions) | — |
| Commits | `commitlint` (Conventional Commits) en CI sobre el título del PR | — |
| Markdown | `markdownlint-cli2` (solo advertencia) | — |
| Local | `lefthook` (pre-commit: formato + gitleaks en archivos staged); opcional, CI es la fuente de verdad | — |

`.editorconfig` en la raíz: UTF-8, LF, indentación 2 espacios (Go: tabs).

## 5. Estrategia de tests

| Nivel | Qué | Herramientas | Dónde corre | Objetivo |
|-------|-----|--------------|-------------|----------|
| Unitarios Go | `domain` y `app` con fakes de puertos; tablas de casos | `go test -race -shuffle=on`, `testing`, `go-cmp` (sin frameworks de mocks pesados; fakes a mano o `mockery` solo si hace falta) | Cada PR | Cobertura ≥ 70 % en `domain`/`app` (no global) |
| Fuzzing | Parsers de NetFlow/IPFIX/sFlow, SNMP, entradas de config WG | Fuzzing nativo de Go (`go test -fuzz`) | PR: 30 s por objetivo; nocturno: 10 min | Sin pánicos |
| Unitarios frontend | Composables, utilidades, componentes | Vitest + `@nuxt/test-utils` + Vue Test Utils | Cada PR | Lógica crítica cubierta |
| Integración | Repositorios contra PostgreSQL (con RLS activo)/ClickHouse reales, consumidores NATS, Valkey; **replay de fixtures MikroTik** (pcap IPFIX/v9, snmpwalk, JSON REST) | `testcontainers-go` (postgres, clickhouse, nats, valkey), build tag `integration` | PR (sólo módulos afectados) | Todos los repositorios y consumidores |
| **Arquitectura** | Importaciones entre módulos sólo vía `api/`; `domain` sin adaptadores; toda tabla con `tenant_id` tiene política RLS; ninguna consulta ClickHouse sin `tenant_id`; todo repositorio exige `TenantScope`; toda operación OpenAPI declara `scope`; todo tipo de evento declara `tenant_scope` | `packages/go/archtest` (go/packages + análisis de SQL de migraciones) | **Cada PR, siempre** (no sólo afectados) | 0 violaciones |
| **Aislamiento de tenant** | Batería generada desde el OpenAPI, los topics WS y el catálogo de eventos ([`security.md`](security.md) §3.9) | `packages/go/tenanttest` con fixture de dos tenants | **Cada PR, siempre** | 100 % de operaciones `scope: tenant` |
| Contrato API | Respuestas del gateway validadas contra el OpenAPI; cliente generado sin diff | `kin-openapi` (validación de request/response en tests de integración del gateway), `openapi-typescript` | PR | 100 % de endpoints |
| Contrato gRPC | Compatibilidad de `.proto` | `buf breaking` | PR | Sin cambios incompatibles sin ADR |
| Contrato eventos | Eventos publicados validan contra su JSON Schema / proto (Agente 3); tests de productor y de consumidor usan los mismos ejemplos (*golden files*) | `santhosh-tekuri/jsonschema` o validación proto | PR | Todos los eventos |
| E2E | Flujos de usuario reales contra el stack compose (login + 2FA, CRUD de routers, WireGuard, permisos) | Playwright (Chromium; Firefox nocturno) | `main` y nocturno; en PR con etiqueta `e2e` | Recorridos críticos del sprint |
| Carga | API/WebSocket (incluidas pantallas kiosco); ingesta de flujos con varios tenants; polling SNMP a 10/100/500/1.000 routers | **k6** (HTTP/WS), generador de NetFlow/IPFIX en Go (`scripts/`), `snmpsim` | Manual / programado en staging | Umbrales de SLO de [`observability.md`](observability.md) |
| Caos | Ver [`disaster-recovery.md`](disaster-recovery.md) §6 | Toxiproxy, nftables, `fallocate` | Nocturno en staging | — |
| Seguridad | Autorización por endpoint (403 y alcance), escaneos | Tests de integración + herramientas de [`security.md`](security.md) §12 | PR | — |

Reglas: los tests no dependen del orden ni de la hora real (reloj inyectado); prohibido
`time.Sleep` para sincronizar (usar `require.Eventually` o canales); datos de prueba
sintéticos, nunca datos reales de abonados.

## 6. Flujo de trabajo: agentes de IA + 1 persona (D7)

Base: [ADR-0023](adr/0023-entrega-por-incrementos-y-equipo-ia.md). Principio: **todo lo que se pueda verificar por
máquina bloquea el merge; la persona revisa sólo donde su juicio aporta** (producto, decisiones, áreas sensibles).

### 6.1 Roles

| Rol | Quién | Hace | No hace |
|-----|-------|------|---------|
| **Agente autor** | Una sesión de agente por tarea | Implementa una tarea del backlog en su rama; abre el PR con la plantilla; corrige lo que digan CI y el revisor | Fusionar su propio PR; tocar rutas fuera del alcance declarado de la tarea; cambiar CI, `CODEOWNERS`, reglas de protección o secretos |
| **Agente revisor** | Otra sesión (otro contexto, instrucciones de revisión) | Revisa el diff contra la DoD, contratos, seguridad y aislamiento; publica el check `agent-review` (aprobado / cambios pedidos) con hallazgos en línea | Escribir código en la rama del autor (sólo sugerencias) |
| **Agente de operación** | Sesión con rol `platform_operator` | Ejecuta runbooks en `--dry-run`, prepara restauraciones, lee métricas | Acciones destructivas sin aprobación de la persona |
| **Persona** (PO / revisora) | La única persona del equipo | Acepta cada incremento (demo), aprueba ADRs, revisa PRs en áreas sensibles (§6.2), aprueba despliegues a producción y acciones destructivas de DR, hace una auditoría por muestreo semanal de PRs fusionados | Revisar línea a línea lo que ya verifica CI |

### 6.2 Qué revisa la persona (CODEOWNERS)

Su aprobación es **obligatoria** sólo en:

- `services/auth/**`, `packages/go/authz/**`, `packages/go/crypto/**`, `packages/go/natsx/**` (sobre y `Horus-Tenant`),
  `packages/go/tenanttest/**`, `packages/go/archtest/**` (quien cambia las reglas no puede ser quien las incumple).
- Migraciones que crean/alteran políticas RLS, borran datos o cambian retención de datos personales.
- `packages/schemas/openapi/gateway-routes.yaml` (permisos y `scope` de rutas), `packages/events/catalog/**` cuando
  cambia `pii` o `tenant_scope`.
- Aprovisionamiento de routers (plantillas `.rsc`, enrolamiento), credenciales y destinos remotos.
- `.github/**` (workflows, `CODEOWNERS`), `deployments/**`, `infrastructure/**` de producción.
- `docs/adr/**` y `docs/po-decisions.md`.

Todo lo demás se fusiona con **CI verde + `agent-review` aprobado**, sin esperar a la persona. Para que la
aprobación de la persona sea distinguible, los agentes trabajan con una identidad de bot (GitHub App) y la persona con
su cuenta; las reglas de protección exigen el check `agent-review` (lo publica la app del revisor, no cuenta como
aprobación humana) y la revisión de CODEOWNERS en las rutas anteriores.

### 6.3 Ramas, PRs y cómo se evitan conflictos entre agentes

- **Trunk-based**: `main` siempre desplegable; **una rama por agente y tarea**:
  `agent/<agente>/<incremento>-<tarea-kebab>` (p. ej. `agent/b/i3-client-discovery`); la persona usa
  `human/<tarea>`. Vida ≤ 1–2 días; PRs pequeños (< 400 líneas cambiadas sin contar generado).
- **Una tarea declara su alcance** (rutas que puede tocar) en el issue; un check de CI (`scope-guard`) falla si el
  diff toca rutas fuera de ese alcance sin la etiqueta `scope-extended` puesta por la persona o por el agente
  coordinador.
- **Dueño único por módulo e incremento**: dos agentes no trabajan a la vez en el mismo `services/<módulo>/`; el
  backlog asigna módulo → agente. El reparto de documentos de esta ronda es el modelo.
- **Contratos primero**: un cambio de OpenAPI/Protobuf/catálogo de eventos/esquema de tabla va en un **PR de
  contrato** propio, que se fusiona antes que los PRs de implementación que lo consumen (el generador de código y los
  mocks salen de ahí).
- **Archivos compartidos sin conflictos**: registros partidos por dueño (`packages/events/catalog/<dominio>.yaml`,
  una spec OpenAPI por módulo, `gateway-routes.yaml` con un bloque por módulo y orden alfabético verificado),
  migraciones con **marca de tiempo UTC** en el nombre (no números secuenciales), `CHANGELOG.md` generado (nadie lo
  edita), código generado **nunca** se resuelve a mano: ante conflicto se regenera.
- **Merge queue** de GitHub obligatoria: cada PR se prueba rebasado sobre lo que entrará antes que él (detecta
  conflictos semánticos entre agentes); los workflows escuchan `merge_group`.
- **Squash merge**; título del PR en Conventional Commits (`tipo(alcance): descripción`, alcance = módulo,
  `frontend`, `protobuf`, `events`, `infra`, `docs`); `!` + `BREAKING CHANGE:` si rompe. Trailers de coautoría del
  agente según la configuración del repositorio. Referencia a la tarea: `Refs: HF-123`.
- **Protección de `main`**: PR obligatorio, checks `ci-ok` + `agent-review` + `dod` requeridos, CODEOWNERS en rutas
  sensibles, historial lineal, sin force-push, conversaciones resueltas, commits firmados (la app de los agentes firma).
- Hotfix: rama `agent/<agente>/hotfix-…` o `human/hotfix-…`, mismo flujo con prioridad en la cola.

### 6.4 Qué revisa el agente revisor (lista fija, versionada en `.github/agent-review.md`)

1. ¿El diff cumple la tarea y sólo la tarea? ¿Hay código muerto, TODO sin issue, secretos?
2. Contratos: ¿cambio compatible? ¿eventos con `tenant_id` y `Horus-Tenant`? ¿rutas con `scope` y permiso?
3. Aislamiento: ¿consultas con `TenantScope`? ¿claves de caché con `t:<tenant_id>:`? ¿datos de otro tenant
   alcanzables por algún camino que la batería no cubre (exportaciones, jobs, WS)?
4. Seguridad y privacidad: checklist de [`security.md`](security.md) §15; IPs de clientes fuera de logs/métricas.
5. Observabilidad: logs/métricas/alertas según [`observability.md`](observability.md) §10.
6. Pruebas: ¿prueban el comportamiento o sólo la implementación? ¿casos de error y de aislamiento?
7. Documentación: README del módulo, ADR si hay decisión, runbook si hay nuevo modo de fallo.

El revisor deja hallazgos en línea y un resumen; "cambios pedidos" bloquea. Si autor y revisor discrepan dos rondas,
se escala a la persona con ambos argumentos.

## 7. Versionado y releases

**Recomendación: una versión única de producto (SemVer) para todo el monorepo**, no por
servicio.

- Motivo: Horus se instala y actualiza como un todo (docker compose); las compatibilidades
  entre servicios se garantizan por contratos (OpenAPI, buf breaking, esquemas de eventos) y
  se prueban juntas en e2e. Versionar 12 servicios por separado añade matrices de
  compatibilidad sin beneficio hasta tener equipos/despliegues independientes (revisable
  con ADR al migrar a Kubernetes).
- `0.y.z` hasta la Release 1.0; cada **incremento** aceptado por la persona produce un `0.<incremento>.0` (D9).
- **release-please** (modo simple, raíz) genera la PR de release, el `CHANGELOG.md` desde
  Conventional Commits y la etiqueta `vX.Y.Z`.
- Al etiquetar: se construyen la imagen `horus` y la del frontend con esa versión, se firman, se adjuntan SBOMs y
  se publica un `release-manifest.json` con sus digests; el compose de `deployments/` referencia ese manifiesto.
  Desplegar en producción requiere aprobación de la persona (environment protegido de GitHub).
- Versionado independiente para contratos: paquetes proto con versión en el paquete
  (`horus.devices.v1`), API REST `/api/v1`, eventos con versión de esquema (Agente 3).
- Migraciones de BD: siempre compatibles hacia atrás durante una versión menor
  (expand → migrate → contract) para permitir rollback del binario.

## 8. Pipeline CI (GitHub Actions)

Pipeline del plan (vision §12): `Code → Lint → Unit Test → Integration Test → Security Check → Build`.

### 8.1 Workflows

| Workflow | Disparador | Contenido |
|----------|------------|-----------|
| `ci.yml` | `pull_request`, `push` a `main`, `merge_group` | Pipeline completo sobre lo afectado |
| `release.yml` | `push` a `main` (release-please) y tags `v*` | Build de todas las imágenes, firma, SBOM, attestations, GitHub Release |
| `nightly.yml` | `schedule` diario | Todo el repo: tests completos, fuzz largo, e2e multi-navegador, `trivy image` sobre imágenes publicadas, `govulncheck`, licencias |
| `codeql.yml` | PR + semanal | Si el plan de GitHub lo permite (ver preguntas abiertas) |

### 8.2 Detección de módulos afectados

1. Job `changes` (`scripts/ci/affected.sh`): `git diff --name-only <base>...HEAD` + `go list -deps` por paquete
   de `services/<módulo>/...` → módulos afectados (cubre `packages/go/*` y código generado). Reglas fijas:
   `go.mod/go.sum`, `packages/events/`, `packages/protobuf/` → todos los módulos; `packages/schemas/` → gateway +
   frontend; `apps/frontend/**` → frontend; `.github/**` o `infrastructure/**` → todo.
2. Salida JSON → `strategy.matrix` para tests unitarios y de integración por módulo.
3. **Siempre**, afecte a lo que afecte: tests de arquitectura, batería de aislamiento de tenant, `scope-guard`,
   `dod`, build de la imagen única `horus` y smoke del perfil mínimo (3 contenedores) en `merge_group`.

### 8.3 Etapas y herramientas

| Etapa | Go | Frontend | Común | Bloquea |
|-------|----|----------|-------|---------|
| **Lint** | `golangci-lint run` (afectados), `go mod tidy -diff` | `eslint`, `prettier --check`, `nuxi typecheck` | `buf lint` + `buf breaking`, `hadolint`, `actionlint`/`zizmor`, `squawk`, commitlint del título de PR, verificación de código generado sin diff | Sí |
| **Unit Test** | `go test -race -shuffle=on -coverprofile` (afectados) + fuzz corto | `vitest run --coverage` | Cobertura publicada como comentario/artefacto | Sí |
| **Integration Test** | `go test -tags=integration` con testcontainers (Docker del runner) | — | Validación de contratos OpenAPI/eventos | Sí |
| **Security Check** | `govulncheck`, `gosec` (vía lint) | `pnpm audit --prod` (advertencia) | `osv-scanner`, `gitleaks`, `trivy fs` + `trivy config`, CodeQL/semgrep | Sí (umbrales de [`security.md`](security.md) §12) |
| **Build** | `docker buildx build` de la imagen **única** `horus` (caché `type=gha`); en PR sin push | `pnpm build` + imagen | `trivy image` sobre la imagen construida; en `main`: push a GHCR + `cosign sign` + `syft` SBOM + `attest-build-provenance` | Sí |
| E2E | — | Playwright contra el perfil mínimo (`docker compose up`) con capturas de pantalla adjuntas al PR si cambia la UI | — | En `merge_group` (smoke) y nocturno (completo) |
| **DoD** | Job `dod` (§11) | | | Sí |

Prácticas: Actions fijadas por SHA; `permissions: contents: read` por defecto (solo `release.yml`
con `packages: write`, `id-token: write`, `attestations: write`); `concurrency` para cancelar
ejecuciones obsoletas del mismo PR; caché de módulos Go y `pnpm store`; objetivo < 10 min
para un PR típico. Jobs agregadores `ci-ok` como único check requerido en la protección de rama
(evita problemas con matrices dinámicas).

## 9. Docker

- **Multi-stage** para Go:
  ```dockerfile
  # (referencia de estilo; el Dockerfile real se escribe en Sprint 1)
  FROM golang:<versión>-bookworm AS build      # fijado por digest
  # go mod download con caché; CGO_ENABLED=0; go build -trimpath -ldflags "-s -w -X …/version.Version=$VERSION"
  FROM gcr.io/distroless/static-debian12:nonroot
  COPY --from=build /out/<servicio> /<servicio>
  USER 65532:65532
  ENTRYPOINT ["/<servicio>"]
  ```
  Un Dockerfile genérico parametrizado por `SERVICE` en `infrastructure/docker/go.Dockerfile`
  es preferible a 12 copias.
- Frontend: build con `node:<lts>-alpine` + pnpm → imagen final mínima no-root que sirve
  `/.output/public` (p. ej. `nginxinc/nginx-unprivileged` o un servidor estático en Go) detrás de
  Traefik, que añade cabeceras de seguridad (CSP, HSTS) ([ADR-0012](adr/0012-nuxt4-nuxt-ui.md),
  [ADR-0013](adr/0013-api-gateway-propio.md)).
- Sin shell ni gestor de paquetes en la imagen final; healthcheck de compose mediante el propio
  binario (`/<servicio> healthcheck` que llama a `/healthz`), ya que distroless no tiene `curl`.
- Etiquetas OCI (`org.opencontainers.image.source`, `.revision`, `.version`, `.created`).
- **Tags:** `ghcr.io/hcdestroyer/horus-flow/<servicio>:vX.Y.Z`, `:sha-<7>`, y `:main` solo
  para entornos de desarrollo. **Nunca** `latest` en despliegues; producción referencia por
  **digest** (`@sha256:…`) desde el manifiesto de release.
- `.dockerignore` estricto (sin `.git`, `node_modules`, `secrets/`, `.env`).
- Hardening en tiempo de ejecución: [`security.md`](security.md) §10.

## 10. Variables de entorno y secretos en local / compose

- `.env.example` versionado con **todas** las variables y valores de desarrollo no sensibles;
  `.env` en `.gitignore`.
- Secretos de desarrollo como archivos en `./secrets/*.txt` (en `.gitignore`), generados por
  `scripts/dev-secrets.sh` (valores aleatorios), montados con `secrets:` de compose en
  `/run/secrets/<nombre>` y consumidos con las variables `_FILE`.
- Mismo mecanismo en producción, con los archivos descifrados desde SOPS+age en el despliegue
  (y más adelante OpenBao, ver [`security.md`](security.md) §8.3).
- Nunca secretos en `environment:` del compose versionado, en `ARG`/`ENV` de Dockerfiles, ni en
  variables de GitHub Actions no marcadas como secretas.
- Perfiles de compose (`profiles:`) para arrancar solo lo necesario:
  `core` (gateway, auth, devices, datastores), `observability`, `collectors`, `analytics`.

## 11. Definición de Terminado (checklist verificable)

Una historia está terminada cuando **todas** las casillas aplicables están marcadas en el PR
(o justificadas como N/A). Mapeo a vision §13.

| # | Criterio (vision §13) | Verificación |
|---|-----------------------|--------------|
| 1 | **Código** | PR fusionado a `main` por squash con título Conventional Commit; sin `TODO` sin issue enlazado |
| 2 | **Tests** | Unitarios de la lógica nueva; integración si toca BD/NATS/externos; contrato si cambia API/proto/eventos; e2e si cambia un recorrido crítico; CI verde |
| 3 | **Manejo de errores** | Errores envueltos y mapeados (§2.4); casos de error con test; sin pánicos; timeouts en toda E/S |
| 4 | **Logs** | Logs JSON con campos obligatorios; nada de lo prohibido ([`observability.md`](observability.md) §3.3) |
| 5 | **Métricas** | RED automáticas + métricas de negocio necesarias con labels permitidos; panel/alerta actualizados si hay nuevo modo de fallo |
| 6 | **Seguridad** | Checklist de [`security.md`](security.md) §15 completo; escaneos en verde |
| 7 | **Documentación** | README del servicio actualizado (env vars, métricas, eventos); ADR si hubo decisión de arquitectura; runbook si hay nuevo modo de fallo |
| 8 | **API documentada** | OpenAPI actualizado (ejemplos y errores), cliente regenerado sin diff; eventos con esquema en `packages/events` |
| 9 | **Migración DB si aplica** | Migración versionada, reversible o *expand/contract*, pasa `squawk`, probada en integración |
| 10 | **Docker** | Imagen construye con el Dockerfile común, no root, pasa `trivy image`; compose actualizado |
| 11 | **CI/CD** | El servicio está en la detección de afectados y en el pipeline; `ci-ok` verde |
| 12 | **Health check** | `/healthz` y `/readyz` según [`observability.md`](observability.md) §7; `healthcheck` en compose |
| 13 | **Backup si aplica** | Datos nuevos cubiertos por la política de [`disaster-recovery.md`](disaster-recovery.md) (o justificado como reconstruible) |
| 14 | **Revisión** | ≥ 1 aprobación (2 en áreas sensibles por CODEOWNERS); demo en la review del sprint con funcionalidad real |

Plantilla de PR (`.github/pull_request_template.md`, la crea quien implemente el Sprint 1)
con estas 14 casillas.
