# packages/go — librerías de plataforma Go

- **Propósito:** librerías comunes a todos los módulos del binario `horus`. **Sin lógica de
  dominio** (lo verifica `archtest`, regla `platform-no-services`).
- **Dueño:** CORE (revisa PLAT). Rutas sensibles (persona obligatoria): `authz`, `crypto`, `natsx`,
  `tenanttest`, `archtest`.
- **Documentación:** [`docs/conventions.md`](../../docs/conventions.md) §1–2 y §5,
  [`docs/observability.md`](../../docs/observability.md) §1–3 y §7,
  [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md).

## Paquetes (I0-04)

| Paquete | API principal |
| --- | --- |
| `config` | `Load[T](environ) (T, error)`, `Parse`, `ParseWith`, `Resolve` (variables `HORUS_*_FILE` con prioridad), interfaz `Validator`, struct `Common` con las variables comunes (`HORUS_ENV`, `HORUS_ROLES`, `HORUS_PROCESS`, `HORUS_LOG_LEVEL`, `HORUS_LOG_FORMAT`, `HORUS_ADMIN_ADDR`, `HORUS_HTTP_ADDR`, `HORUS_PPROF_ENABLED`, `HORUS_START_TIMEOUT`, `HORUS_SHUTDOWN_DELAY`, `HORUS_SHUTDOWN_TIMEOUT`) |
| `observability` | `NewLogger(w, LogConfig)` (slog JSON/texto; `ts` RFC 3339 ms UTC; siempre `service`, `role`, `trace_id`, `span_id`, `request_id`, `tenant_id`), `ForRole`, `ContextHandler`, `WithRole`/`WithTenant`/`WithRequestID`/`WithTrace` y sus getters, `Secret` (redactado en logs/JSON/fmt), `NewRegistry(BuildInfo)` (Go + proceso + `horus_build_info`), `MetricsHandler`, `LatencyBuckets`, `ValidateLabels` (lista negra de labels) |
| `health` | `NewRegistry(Options)`, `Registry.Role(name)` → `Role.AddCheck(Check{Name, Critical, Timeout, Probe})`, `Role.SetState`, `Registry.SetDraining`, `Report`, `LiveHandler` (`/healthz`), `ReadyHandler` (`/readyz`, `?role=`), `DialProbe`. Chequeos con timeout 1 s y caché 5 s; crítico que falla → rol `unavailable` (503); degradable → rol `degraded` (proceso sigue en 200) |
| `lifecycle` | `New(Options{StartTimeout, DrainDelay, ShutdownTimeout, OnDrain})`, `Manager.Append(Hook{Name, Start, Run, Stop})`, `Manager.Run(ctx)`: arranque ordenado, `Run` concurrente, apagado en orden inverso acotado, `ErrShutdownTimeout` |
| `httpx` | `NewServer(name, addr, h, logger, opts)` con `Start`/`Run`/`Stop`/`Hook` (apagado que drena peticiones), `RequestID`, `Recover`, `NewMetrics` (RED: `http_server_requests_total`, `http_server_request_duration_seconds`, `http_server_requests_in_flight`), `NewMux` + `ForService(role).Handle` (rol en contexto y métricas por plantilla de ruta), `AdminHandler` (`/healthz`, `/readyz`, `/metrics`, pprof opcional) |
| `module` | Contrato módulo ↔ binario: `Deps{Role, Logger, Health, Metrics, Routes, Common, Environ}`, `Module` (`Run`), `Starter`, `Stopper`, `Factory`, `Func`, `Idle()` |
| `testkit` | `Logger(t)` + `LogBuffer` (`Records`, `Find`), `Environ(kv...)`, `Eventually` |
| `archtest` | `Check(root, modulePath) ([]Violation, error)`, `FindRepoRoot`. Reglas `module-internal`, `module-api-only`, `platform-no-services`, `domain-no-adapters`; `TestRepositoryArchitecture` corre contra el repo en cada `go test ./...` |

## Pendiente (historias posteriores)

`authz` (TenantScope, I0-07/I0-08), `natsx` (sobre + `Horus-Tenant`, outbox relay, DLQ),
`crypto/envelope`, `grpcx`, `tenanttest`, trazas OpenTelemetry en `observability` (I0-18).
