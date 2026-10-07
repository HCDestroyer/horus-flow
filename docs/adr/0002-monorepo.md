# 0002 — Monorepo

- Estado: Aceptada (modificada por ADR-0025: un solo `go.mod` para el backend)
- Fecha: 2026-10-07

## Contexto

Horus Flow tiene un frontend Nuxt, ~10 servicios Go, contratos compartidos (Protobuf, esquemas de
eventos, OpenAPI) e infraestructura (Compose, configs de PostgreSQL/ClickHouse/NATS). Los
contratos cambian a la vez que productores y consumidores. El equipo es pequeño. `vision.md §11`
propone un monorepo.

## Decisión

Un **monorepo único** con la estructura de `vision.md §11` (`apps/`, `services/`, `packages/`,
`infrastructure/`, `docs/`, `scripts/`, `deployments/`).

- Go: **un `go.mod` por servicio** más módulos compartidos en `packages/` (p. ej.
  `packages/go/platform` para logging/OTel/config, `packages/protobuf` con código generado),
  enlazados con `go.work` para desarrollo local. Esto permite builds e imágenes independientes
  por servicio sin arrastrar dependencias ajenas.
- CI con filtros por ruta: solo se construye/testea lo afectado (y lo que depende de un
  `packages/` modificado).
- Un cambio de contrato (proto/evento) y sus adaptaciones van en el **mismo PR**.
- Versionado: imágenes etiquetadas por SHA de commit; releases del producto con SemVer global.

## Alternativas consideradas

- **Polyrepo (un repo por servicio)**: despliegue independiente "natural", pero cambios de
  contrato requieren PRs coordinados en N repos y publicar paquetes versionados de proto. Excesivo
  para el tamaño del equipo.
- **Monorepo con un solo `go.mod`**: más simple, pero todas las imágenes comparten árbol de
  dependencias y cualquier bump afecta a todo.
- **Herramientas de monorepo (Bazel, Nx, Pants)**: potentes pero con curva alta; se reevalúa si el
  tiempo de CI supera ~15 min.

## Consecuencias

- (+) Cambios atómicos de contrato; refactors transversales sencillos; una sola fuente de docs.
- (+) La independencia de despliegue se conserva (imagen por servicio).
- (−) CI debe ser inteligente con filtros por ruta; riesgo de acoplamiento accidental vía
  `packages/` (regla: `packages/` no contiene lógica de dominio).
- (−) Permisos de repositorio son globales (aceptable para un equipo).
