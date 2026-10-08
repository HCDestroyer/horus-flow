# services/cmd/horus — binario único `horus`

- **Propósito:** `main` único del backend. Lee `HORUS_ROLES` / `--roles` (lista separada por comas
  o `all`) y compone los módulos de `services/<módulo>/` correspondientes
  ([ADR-0025](../../../docs/adr/0025-binario-modular-con-roles.md),
  [`docs/services.md`](../../../docs/services.md) §1).
- **Dueño:** PLAT (arranque y registro de roles; revisa CORE) — [`docs/backlog/team.md`](../../../docs/backlog/team.md) §2.
- **Estado (I0-04):** registro de roles, configuración tipada, logs JSON, métricas, health checks
  por rol y apagado ordenado. Los roles son stubs que se registran y quedan listos.

```bash
make build                                   # ./bin/horus con la versión de git
HORUS_ROLES=collector ./bin/horus
go run ./services/cmd/horus --roles=auth,devices
go run ./services/cmd/horus --version        # versión y roles disponibles
```

## Archivos

| Archivo | Contenido |
| --- | --- |
| `main.go` | Señales (`SIGINT`/`SIGTERM`; una segunda señal termina de inmediato) y `os.Exit` |
| `app.go` | `run` (flags, configuración, logger) y `build` (cableado manual: admin, roles, API) |
| `roles.go` | Catálogo de roles en orden de arranque; `scripts/new-module.sh` añade aquí los nuevos |

## Roles

`auth`, `devices`, `wireguard`, `snmp`, `traffic`, `detection`, `alerts`, `analytics`,
`reporting`, `ingester`, `jobs`, `collector`, `wg-agent`, `gateway` (este es el orden de arranque;
se para en orden inverso). `example` ([`services/_example`](../../_example/README.md)) existe pero no
entra en `all`.

## Puertos y endpoints

| Variable | Defecto | Sirve |
| --- | --- | --- |
| `HORUS_ADMIN_ADDR` | `:8081` | `/healthz` (liveness, sin dependencias), `/readyz` (estado por rol, `?role=<rol>`), `/metrics`, `/debug/pprof/*` si `HORUS_PPROF_ENABLED=true`. Nunca se publica fuera |
| `HORUS_HTTP_ADDR` | `:8080` | API REST de los roles locales; solo se abre si algún rol registra rutas |

`/readyz` responde 200 si todos los roles están listos (un rol `degraded` cuenta como listo) y 503
si alguno está `starting`, `unavailable` o `stopping`, o el proceso está `draining`.

## Ciclo de vida

1. Carga `config.Common` (errores de configuración o rol desconocido → código 2).
2. Arranca el servidor de administración, después cada rol en orden (`Start` con
   `HORUS_START_TIMEOUT`) y por último la API.
3. Al recibir `SIGTERM`: `/readyz` → 503 (`draining`), espera `HORUS_SHUTDOWN_DELAY` (5 s), cancela
   los roles, la API deja de aceptar y drena las peticiones en curso, se paran los roles en orden
   inverso y por último el servidor de administración. Todo dentro de `HORUS_SHUTDOWN_TIMEOUT`
   (25 s). Apagado limpio → código 0; plazo agotado o error → código 1.

## Métricas propias

`horus_build_info{service,version,commit,go_version}`, colectores `go_*`/`process_*` y las RED HTTP
de [`packages/go/httpx`](../../../packages/go/README.md).
