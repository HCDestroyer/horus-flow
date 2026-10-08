# services/_example — módulo de ejemplo y plantilla

- **Propósito:** módulo mínimo que muestra cómo nace un módulo de Horus
  ([`docs/conventions.md`](../../docs/conventions.md) §2,
  [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)). Es también la plantilla que copia
  [`scripts/new-module.sh`](../../scripts/new-module.sh) (`example` → nombre del módulo).
- **Rol:** `example`. **No** entra en `HORUS_ROLES=all`; se activa con `--roles=example`.
- **Dueño:** CORE.

## Estructura

```
module.go                      # Register(ctx, module.Deps): cableado manual
internal/config/               # Config (HORUS_EXAMPLE_*) + Validate
internal/domain/               # reglas puras, errores de dominio
internal/app/                  # casos de uso
internal/adapters/httpapi/     # rutas REST montadas en la API del proceso
```

## Variables de entorno

| Variable | Defecto | Uso |
| --- | --- | --- |
| `HORUS_EXAMPLE_GREETING` | `hola` | Saludo de la ruta `hello` |
| `HORUS_EXAMPLE_DEPENDENCY_ADDR` | — | `host:puerto` de una dependencia opcional; si no responde, el rol aparece `degraded` en `/readyz` con el chequeo `dependency` |

## Rutas

- `GET /api/v1/example/hello/{name}` → `{"message":"hola, <name>"}` (400 si el nombre está vacío o
  supera 64 caracteres).

## Métricas, eventos

Solo las RED HTTP comunes (`http_server_*{service="example"}`). No publica ni consume eventos.

## Tests

La carpeta empieza por `_`, así que los patrones `./...` no la recorren (tampoco
`./services/_example/...`). Aun así queda cubierta en cada `go test ./...`:

- `services/cmd/horus` la importa (compila y pasa `go vet`) y la prueba de extremo a extremo
  (`TestProcessHealthMetricsAndLogs`: rutas, `/readyz` degradado, métricas, logs);
- `TestNewModuleScript` genera un módulo con `scripts/new-module.sh` y ejecuta sus tests, que son
  copia de los de esta plantilla.

Para ejecutarlos directamente hay que nombrar los paquetes:
`go test ./services/_example/internal/app ./services/_example/internal/adapters/httpapi`.
