# Sprint 1 — Plataforma base

- **Objetivo del sprint:** *aplicación funcionando de punta a punta aunque todavía no monitoree
  routers* (`vision.md` §9): un usuario semilla inicia sesión en Nuxt, ve el layout con
  navegación y menú de usuario y un dashboard base, todo levantado con `docker compose up`, con
  CI verde y una traza visible desde el navegador hasta PostgreSQL.
- **Épicas:** EP-01, EP-02, EP-03, EP-T1, EP-T3, EP-T4, EP-T5.
- **Flujos:** Plataforma (P), Backend core (B), Frontend (F), Datos (D). Ver [`team.md`](team.md).
- **Capacidad:** S1 se planifica al 70 % de la velocidad de referencia → ~15–18 pts por flujo.

| ID | Historia | Flujo | Área | Pts | Prio | Depende de |
| --- | --- | --- | --- | --- | --- | --- |
| S01-01 | Docker Compose de infraestructura (PostgreSQL, Redis, NATS JetStream, MinIO) | P | infra | 5 | Must | — |
| S01-02 | Variables de entorno y secretos | P | infra, security | 3 | Must | S01-01 |
| S01-03 | CI: lint, tests y builds | P | infra | 5 | Must | — |
| S01-04 | Stack de observabilidad en compose | P | infra | 5 | Must | S01-01 |
| S01-05 | Reverse proxy con TLS local | P | infra, security | 2 | Should | S01-01 |
| S01-06 | Plantilla de servicio Go | B | backend | 3 | Must | S01-03 |
| S01-07 | api-gateway: enrutado, errores, CORS, rate limit | B | backend | 5 | Must | S01-06 |
| S01-08 | auth: login con Argon2id y emisión de tokens | B | backend, security | 5 | Must | S01-06, S01-10 |
| S01-09 | auth: refresh, logout y `GET /api/v1/me` | B | backend, security | 3 | Must | S01-08 |
| S01-10 | Migraciones PostgreSQL y usuario semilla | D | data | 3 | Must | S01-01 |
| S01-11 | devices: esqueleto con health y `GET /routers` vacío | B | backend | 2 | Must | S01-06 |
| S01-12 | WebSocket base en api-gateway | B | backend | 3 | Should | S01-07, S01-08 |
| S01-13 | Proyecto Nuxt 4 con Nuxt UI, tema e i18n | F | frontend | 3 | Must | S01-03 |
| S01-14 | Pantalla de login | F | frontend | 3 | Must | S01-13 |
| S01-15 | Layout: sidebar, toolbar y navegación | F | frontend | 5 | Must | S01-13 |
| S01-16 | Menú de usuario | F | frontend | 2 | Must | S01-15, S01-09 |
| S01-17 | Dashboard base | F | frontend | 3 | Must | S01-15 |
| S01-18 | Cliente API, sesión y manejo global de errores | F | frontend | 3 | Must | S01-13 |
| S01-19 | Mocks desde OpenAPI para desarrollo frontend | F | frontend | 2 | Should | S01-13 |
| S01-20 | Simulador SNMP mínimo (preparación S3/S5) | D | data | 3 | Could | S01-01 |
| S01-21 | Prueba e2e de humo (login → dashboard) | F | frontend, infra | 2 | Must | S01-14, S01-17, S01-08 |

Total: 70 pts (P 20, B 21, F 23, D 6). El flujo Datos dedica el resto de su capacidad a apoyar
migraciones y a terminar los documentos de S0 que queden en revisión.

---

## Infraestructura y DevOps

### S01-01 · Docker Compose de infraestructura
- **Épica:** EP-01 · **Área:** infra · **Pts:** 5 · **Flujo:** Plataforma

**Como** desarrollador **quiero** levantar toda la infraestructura con un comando **para** trabajar
en un entorno idéntico al de mis compañeros.

1. **Dado** un clon limpio con Docker instalado, **cuando** ejecuto `docker compose up -d`
   (o `make up`), **entonces** PostgreSQL, Redis, NATS con JetStream habilitado y MinIO quedan
   `healthy` en < 2 min.
2. **Dado** el compose, **cuando** reviso la configuración, **entonces** cada servicio tiene
   `healthcheck`, volumen persistente nombrado, versión de imagen fijada (no `latest`) y límites
   de memoria.
3. **Dado** que reinicio el stack, **cuando** vuelve a levantar, **entonces** los datos de
   PostgreSQL y MinIO persisten.
4. **Dado** que un puerto ya está ocupado, **cuando** arranco, **entonces** puedo cambiarlo por
   variable de entorno sin editar el compose.
5. **Dado** el primer arranque, **cuando** se inicializa MinIO, **entonces** se crean los buckets de
   [`../storage.md`](../storage.md) §4 con versionado y, en los que lo requieren (auditoría,
   backups), **object lock habilitado desde la creación** (no se puede activar después); un test
   verifica que un objeto bloqueado no se puede borrar.

**Notas:** archivos en `infrastructure/docker/` y `deployments/`. Buckets iniciales de MinIO según
[`../storage.md`](../storage.md). ClickHouse **no** entra en S1; llega en S5 si se aprueba C-03 del
roadmap, o en S6.

### S01-02 · Variables de entorno y secretos
- **Épica:** EP-01, EP-T2 · **Área:** infra, security · **Pts:** 3

**Como** ingeniero de plataforma **quiero** separar configuración de secretos **para** no filtrar
credenciales en el repositorio.

1. **Dado** el repositorio, **cuando** busco secretos, **entonces** solo hay `.env.example` con
   valores ficticios documentados; `.env` está en `.gitignore`.
2. **Dado** que un servicio arranca sin un secreto obligatorio, **cuando** inicia, **entonces**
   falla rápido con un mensaje que nombra la variable (sin imprimir su valor).
3. **Dado** el CI, **cuando** un PR introduce un secreto (patrón de clave privada, token),
   **entonces** el escaneo de secretos (p. ej. gitleaks) falla el pipeline.
4. **Dado** compose, **cuando** se inyectan secretos, **entonces** se usa el mecanismo de
   [`../security.md`](../security.md) (Docker secrets o archivo montado), no variables en claro en
   el YAML.

### S01-03 · CI: lint, tests y builds
- **Épica:** EP-01, EP-T4 · **Área:** infra · **Pts:** 5

**Como** equipo **queremos** que cada PR pase lint, tests y build **para** que `main` esté siempre
desplegable.

1. **Dado** un PR, **cuando** se abre o actualiza, **entonces** corre el pipeline
   `lint → unit test → integration test → security check → build` (`vision.md` §12) solo para los
   paquetes afectados (filtro por rutas del monorepo).
2. **Dado** Go, **cuando** corre el lint, **entonces** usa `golangci-lint` con la configuración de
   [`../conventions.md`](../conventions.md); **dado** el frontend, ESLint + `vue-tsc` + Prettier;
   **dado** una migración SQL nueva, `squawk` la analiza y falla ante operaciones que bloquean
   tablas ([`../database.md`](../database.md) D6).
3. **Dado** un fallo de test o lint, **cuando** termina el pipeline, **entonces** el merge queda
   bloqueado por *branch protection*.
4. **Dado** un pipeline completo sin caché, **cuando** se mide, **entonces** tarda < 15 min.
5. **Dado** un merge a `main`, **cuando** termina, **entonces** se publican imágenes con tag del SHA
   en el registro del repositorio.
6. **Dado** el security check, **cuando** corre, **entonces** ejecuta `govulncheck`,
   auditoría de dependencias npm y escaneo de imágenes (p. ej. Trivy) y falla con vulnerabilidades
   críticas.

**Notas:** GitHub Actions o GitLab CI según P-15; recomendación en
[`../open-questions/product.md`](../open-questions/product.md).

### S01-04 · Stack de observabilidad
- **Épica:** EP-T1 · **Área:** infra · **Pts:** 5

**Como** operador de la plataforma **quiero** métricas, logs y trazas desde el primer día **para**
saber dónde falla una petición (`vision.md` §6).

1. **Dado** `docker compose --profile observability up`, **cuando** levanta, **entonces** Prometheus,
   Grafana, Loki y OpenTelemetry Collector quedan `healthy` y Grafana tiene los datasources
   aprovisionados.
2. **Dado** un login desde el frontend, **cuando** abro Grafana, **entonces** veo una traza
   `frontend → api-gateway → auth → PostgreSQL` con el mismo `trace_id` que aparece en los logs.
3. **Dado** cada servicio Go, **cuando** Prometheus lo raspa, **entonces** expone métricas RED y de
   runtime en `/metrics`.
4. **Dado** un servicio caído, **cuando** pasa 1 min, **entonces** el dashboard "Plataforma" de
   Grafana lo muestra en rojo.

### S01-05 · Reverse proxy con TLS local
- **Épica:** EP-01 · **Área:** infra, security · **Pts:** 2

1. **Dado** el stack, **cuando** abro `https://horus.localhost`, **entonces** el proxy (Caddy o
   Traefik, según ADR) sirve el frontend y enruta `/api/` y `/api/v1/ws` al api-gateway con TLS.
2. **Dado** una respuesta, **cuando** inspecciono cabeceras, **entonces** incluye HSTS,
   `X-Content-Type-Options`, `Referrer-Policy` y la CSP inicial de [`../security.md`](../security.md).

## Backend

### S01-06 · Plantilla de servicio Go
- **Épica:** EP-01 · **Área:** backend · **Pts:** 3

**Como** desarrollador backend **quiero** una plantilla común **para** que cada servicio nazca
cumpliendo la DoD.

1. **Dado** la plantilla, **cuando** creo un servicio, **entonces** incluye Chi, configuración por
   entorno, logs JSON con `trace_id`, OpenTelemetry, `/metrics`, `/healthz`, `/readyz`, apagado
   ordenado (SIGTERM), Dockerfile multi-stage con imagen mínima y usuario no root.
2. **Dado** `/readyz`, **cuando** una dependencia obligatoria no responde, **entonces** devuelve
   503 con la lista de dependencias y su estado.

### S01-07 · api-gateway
- **Épica:** EP-01, EP-03 · **Área:** backend · **Pts:** 5 · **Servicio:** api-gateway

**Como** frontend **quiero** un único punto de entrada **para** no conocer la topología interna
(`vision.md` §1).

1. **Dado** una petición a `/api/v1/auth/*` o `/api/v1/routers*`, **cuando** llega al gateway,
   **entonces** se enruta a auth o devices respectivamente.
2. **Dado** una ruta protegida sin token o con token inválido/expirado, **cuando** llega,
   **entonces** responde 401 con el formato de error de [`../api.md`](../api.md) y no la reenvía.
3. **Dado** un servicio interno caído, **cuando** se le llama, **entonces** el gateway responde 503
   con código de error estable (p. ej. `service_unavailable`) y el nombre lógico del dominio, en
   < 2 s (timeout configurado).
4. **Dado** más de N peticiones de login por IP/minuto (N en `security.md`), **cuando** se excede,
   **entonces** responde 429 con `Retry-After` (rate limit en Redis).
5. **Dado** una petición, **cuando** se responde, **entonces** se propaga `traceparent` y se añade
   `X-Request-Id`.

### S01-08 · auth: login
- **Épica:** EP-03 · **Área:** backend, security · **Pts:** 5 · **Servicio:** auth

**Como** operador del NOC **quiero** iniciar sesión con usuario y contraseña **para** acceder a la
plataforma de forma segura.

1. **Dado** un usuario activo con contraseña correcta, **cuando** hace
   `POST /api/v1/auth/login`, **entonces** recibe 200 con un access token de vida corta y un
   refresh token en cookie `HttpOnly; Secure; SameSite=Strict` (mecanismo exacto en
   [`../security.md`](../security.md)), y se registra una sesión en PostgreSQL/Redis.
2. **Dado** contraseña incorrecta o usuario inexistente, **cuando** intenta, **entonces** recibe
   401 con el mismo mensaje y tiempo de respuesta similar en ambos casos (no enumeración).
3. **Dado** 5 fallos consecutivos (valor en `security.md`), **cuando** intenta de nuevo,
   **entonces** se aplica retraso progresivo / bloqueo temporal.
4. **Dado** las contraseñas en base de datos, **cuando** se inspeccionan, **entonces** son hashes
   Argon2id con los parámetros de `security.md`.
5. **Dado** un usuario desactivado, **cuando** intenta, **entonces** recibe 401 sin revelar el
   motivo.

### S01-09 · auth: refresh, logout y `/me`
- **Épica:** EP-03 · **Área:** backend, security · **Pts:** 3

1. **Dado** un refresh token válido, **cuando** llama a `POST /api/v1/auth/refresh`, **entonces**
   recibe un nuevo access token y un refresh token **rotado**; el anterior queda invalidado.
2. **Dado** un refresh token ya usado, **cuando** se reutiliza, **entonces** se revoca toda la
   familia de la sesión (detección de robo) y responde 401.
3. **Dado** una sesión activa, **cuando** llama a `POST /api/v1/auth/logout`, **entonces** la
   sesión se revoca y el refresh token deja de funcionar.
4. **Dado** un usuario autenticado, **cuando** llama a `GET /api/v1/me`, **entonces** recibe
   `id`, `username`, `display_name`, `email`, `locale`, `roles` y la lista de `permissions`
   efectivos (la UI la usa para navegación, [`../frontend.md`](../frontend.md) §9).

### S01-10 · Migraciones y usuario semilla
- **Épica:** EP-01, EP-03 · **Área:** data · **Pts:** 3 · **Flujo:** Datos

1. **Dado** una base vacía, **cuando** se ejecuta `auth migrate`, **entonces** se aplican con
   **goose** (SQL embebido, esquema `auth`, tabla `auth.goose_db_version`) las migraciones de
   usuarios, roles, permisos y sesiones de [`../database.md`](../database.md).
2. **Dado** el primer arranque, **cuando** no hay usuarios, **entonces** se crea un administrador
   con contraseña tomada de un secreto (nunca por defecto en código) y marcado para cambio en el
   primer login.
3. **Dado** una migración, **cuando** corre CI, **entonces** se aplica sobre base vacía y sobre la
   versión anterior (forward-only en producción; *down* solo en desarrollo, según `database.md`).

### S01-11 · devices: esqueleto
- **Épica:** EP-05 · **Área:** backend · **Pts:** 2 · **Servicio:** devices

1. **Dado** devices en compose, **cuando** consulto `/readyz`, **entonces** responde 200 con
   PostgreSQL disponible.
2. **Dado** un usuario con `devices.read`, **cuando** pide `GET /api/v1/routers`, **entonces**
   recibe una lista paginada vacía con el formato de [`../api.md`](../api.md); **dado** un usuario
   sin el permiso, 403.

### S01-12 · WebSocket base
- **Épica:** EP-T5 · **Área:** backend · **Pts:** 3 · **Servicio:** api-gateway

**Como** frontend **quiero** un canal en tiempo real autenticado **para** mostrar estados en vivo a
partir de S3.

1. **Dado** un access token válido, **cuando** el cliente obtiene un ticket de un solo uso
   (`POST /api/v1/realtime/tickets`, TTL 30 s) y abre `/api/v1/ws` con él y un `Origin` permitido
   ([`../api.md`](../api.md) §4, [`../security.md`](../security.md)), **entonces** la conexión se acepta y recibe un mensaje de bienvenida
   con versión del protocolo.
2. **Dado** un ticket inválido, caducado o ya usado, u `Origin` no permitido, **cuando** intenta conectar,
   **entonces** se rechaza con código de
   cierre documentado.
3. **Dado** una conexión abierta, **cuando** pasan 30 s sin tráfico, **entonces** hay ping/pong y
   las conexiones muertas se cierran.
4. **Dado** un evento de prueba publicado en NATS (`horus.system.heartbeat.emitted` o el que defina
   [`../events.md`](../events.md)), **cuando** el cliente está suscrito al tema, **entonces** lo
   recibe en < 1 s.

## Frontend

### S01-13 · Proyecto Nuxt 4
- **Épica:** EP-02, EP-T3 · **Área:** frontend · **Pts:** 3

1. **Dado** `apps/frontend`, **cuando** ejecuto `pnpm dev`, **entonces** arranca Nuxt 4 con Nuxt UI,
   Tailwind, TypeScript estricto, ESLint y Vitest.
2. **Dado** el tema, **cuando** reviso `app.config.ts` y el CSS, **entonces** están los colores
   semánticos y tokens de estado de [`../frontend.md`](../frontend.md) §10, en claro y oscuro.
3. **Dado** cualquier texto visible, **cuando** reviso el código, **entonces** sale de archivos de
   i18n (`es` por defecto; ver P-08), nunca literal en plantillas.
4. **Dado** Pinia, **cuando** reviso dependencias, **entonces** no se instala hasta que una historia
   lo justifique (`vision.md` §1); el estado de sesión vive en un composable.

### S01-14 · Pantalla de login
- **Épica:** EP-02, EP-03 · **Área:** frontend · **Pts:** 3 · **Depende de:** S01-13, contrato de S01-08

**Como** operador del NOC **quiero** una pantalla de inicio de sesión clara **para** entrar rápido
también desde el móvil.

1. **Dado** que no estoy autenticado, **cuando** abro cualquier ruta, **entonces** se me lleva a
   `/login?redirect=<ruta>` y tras autenticarme vuelvo a esa ruta.
2. **Dado** el formulario, **cuando** envío credenciales válidas, **entonces** el botón muestra
   estado de carga, no se puede enviar dos veces y entro al dashboard.
3. **Dado** credenciales inválidas, **cuando** envío, **entonces** se muestra un `UAlert` con
   "Usuario o contraseña incorrectos", el foco va al mensaje y la contraseña se vacía.
4. **Dado** 429, **cuando** envío, **entonces** el mensaje dice cuánto esperar.
5. **Dado** el api-gateway caído, **cuando** envío, **entonces** el mensaje dice "No se puede
   conectar con Horus Flow. Reintenta en unos segundos." con botón Reintentar.
6. **Dado** solo teclado, **cuando** navego, **entonces** el orden de foco es usuario → contraseña →
   mostrar contraseña → Iniciar sesión, con foco visible; los campos tienen `autocomplete` correcto.
7. **Dado** un ancho de 320 px, **cuando** veo la pantalla, **entonces** no hay scroll horizontal.

### S01-15 · Layout, sidebar, toolbar y navegación
- **Épica:** EP-02 · **Área:** frontend · **Pts:** 5

**Como** operador del NOC **quiero** una navegación estable y predecible **para** saber siempre
dónde estoy y adónde puedo ir.

1. **Dado** que estoy autenticado, **cuando** cargo la app, **entonces** veo `UDashboardGroup` con
   sidebar (logo, búsqueda, navegación, menú de usuario) y un panel con `UDashboardNavbar` que
   muestra el título de la sección.
2. **Dado** el mapa de navegación de [`../frontend.md`](../frontend.md) §3, **cuando** cargo la app,
   **entonces** solo aparecen los ítems cuyo permiso de lectura tengo y cuya sección existe en el
   sprint actual (en S1: Resumen, Inventario › Routers como placeholder, Administración si soy
   admin).
3. **Dado** un ancho ≥ 1024 px, **cuando** pulso colapsar, **entonces** el sidebar queda en iconos con
   tooltip y la preferencia se recuerda; **dado** < 1024 px, el sidebar es un panel deslizable
   que se abre con el botón de menú y se cierra con Esc o al navegar.
4. **Dado** la ruta actual, **cuando** miro el sidebar, **entonces** el ítem activo está marcado con
   algo más que color (peso + indicador) y `aria-current="page"`.
5. **Dado** el teclado, **cuando** cargo una página, **entonces** existe un enlace "Saltar al
   contenido" como primer elemento enfocable.
6. **Dado** una ruta inexistente o sin permiso, **cuando** navego, **entonces** veo una página 404 /
   403 con enlace al Resumen, dentro del layout.

### S01-16 · Menú de usuario
- **Épica:** EP-02 · **Área:** frontend · **Pts:** 2

1. **Dado** que estoy autenticado, **cuando** abro el menú de usuario (pie del sidebar), **entonces**
   veo nombre, rol principal, "Mi cuenta" (placeholder hasta S2), "Apariencia" (Sistema / Claro /
   Oscuro) e "Cerrar sesión".
2. **Dado** "Cerrar sesión", **cuando** lo pulso, **entonces** se llama a logout, se limpia el estado
   local, se cierra el WebSocket y vuelvo a `/login` con mensaje "Sesión cerrada".
3. **Dado** "Apariencia", **cuando** elijo una opción, **entonces** cambia sin recargar, sin destello
   y se recuerda; por defecto es "Sistema".

### S01-17 · Dashboard base
- **Épica:** EP-02 · **Área:** frontend · **Pts:** 3

**Como** operador del NOC **quiero** una página de resumen **para** tener un punto de partida, aunque
todavía no haya routers monitoreados.

1. **Dado** S1, **cuando** abro el Resumen, **entonces** veo tarjetas de "Routers" (total desde
   `GET /api/v1/routers`, 0), "Estado de la plataforma" (desde el estado de dependencias) y un
   estado vacío con la acción "Registrar el primer router" (deshabilitada con explicación hasta
   S3, o visible solo con `devices.create`).
2. **Dado** que una llamada falla, **cuando** se renderiza, **entonces** esa tarjeta muestra su
   estado de error con reintento y las demás siguen funcionando.
3. **Dado** la carga inicial, **cuando** los datos tardan, **entonces** se ven `USkeleton` con la
   forma final, no un spinner a pantalla completa.
4. **Dado** el WebSocket, **cuando** está conectado/desconectado, **entonces** el indicador de
   conexión de la toolbar lo refleja ([`../frontend.md`](../frontend.md) §7.3).

### S01-18 · Cliente API, sesión y errores globales
- **Épica:** EP-02 · **Área:** frontend · **Pts:** 3

1. **Dado** el access token, **cuando** se almacena, **entonces** vive solo en memoria (nunca en
   `localStorage`/`sessionStorage`); al recargar la página se recupera la sesión con
   `POST /api/v1/auth/refresh` usando la cookie `HttpOnly` ([`../security.md`](../security.md) S4).
2. **Dado** un 401 por access token expirado, **cuando** ocurre, **entonces** el cliente hace un
   único refresh (las peticiones concurrentes esperan) y reintenta; si el refresh falla, se va a
   `/login` conservando la ruta.
3. **Dado** un error con formato de [`../api.md`](../api.md), **cuando** llega, **entonces** se
   convierte en un tipo `ApiError` con `code`, `message` traducible y `trace_id` visible en el
   detalle del error (para soporte).
4. **Dado** cualquier llamada, **cuando** sale, **entonces** lleva `traceparent` (OTel web) para
   unir la traza con el backend.
5. **Dado** los tipos, **cuando** se compila, **entonces** los modelos se generan desde OpenAPI
   (p. ej. `openapi-typescript`), no a mano.

### S01-19 · Mocks desde OpenAPI
- **Épica:** EP-T4 · **Área:** frontend · **Pts:** 2

1. **Dado** el OpenAPI en borrador de un servicio, **cuando** ejecuto `pnpm dev:mock`, **entonces** el
   frontend funciona contra respuestas simuladas (Prism o MSW) sin backend.
2. **Dado** que el contrato cambia, **cuando** CI corre, **entonces** los tipos generados y los
   mocks se regeneran y la compilación falla si el frontend usa campos que ya no existen.

### S01-21 · Prueba e2e de humo
- **Épica:** EP-T4 · **Área:** frontend, infra · **Pts:** 2

1. **Dado** el stack en CI, **cuando** corre Playwright, **entonces** un usuario semilla inicia
   sesión, ve el Resumen, cambia a tema oscuro y cierra sesión.
2. **Dado** la misma prueba, **cuando** corre axe, **entonces** no hay violaciones *serious* ni
   *critical* en login y Resumen.

## Datos

### S01-20 · Simulador SNMP mínimo
- **Épica:** EP-08, EP-T4 · **Área:** data · **Pts:** 3 · **Prio:** Could

**Como** ingeniero de datos **quiero** agentes SNMP simulados **para** desarrollar el poller de S5 y
probar con decenas de routers sin hardware.

1. **Dado** `docker compose --profile sim up`, **cuando** arranca, **entonces** hay N agentes SNMP
   (snmpsim o similar) que responden MIB-II e IF-MIB con datos grabados de un MikroTik real.
2. **Dado** el simulador, **cuando** cambio N, **entonces** escala a 50 agentes en una máquina de
   desarrollo.

## Riesgos del sprint

| Riesgo | Mitigación |
| --- | --- |
| Contratos de auth no cerrados al inicio bloquean al frontend | S01-19: frontend trabaja contra mocks; contrato de login/refresh/me aprobado el día 2 |
| CI lento por monorepo | Filtros por ruta y caché de módulos Go/pnpm desde el inicio |
| Observabilidad se queda "para después" | Es Must; la demo de la review incluye la traza en Grafana |
