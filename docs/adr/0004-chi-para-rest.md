# 0004 — Chi como router HTTP para REST

- Estado: Aceptada
- Fecha: 2026-10-07

## Contexto

Cada servicio expone REST público (proxied por el gateway, ver [ADR-0013](0013-api-gateway-propio.md))
y el gateway mismo es un servidor HTTP con middlewares (auth, rate limit, trazas). Se necesita un
router con middlewares componibles, rutas con parámetros y compatibilidad total con `net/http`.

## Decisión

Usar **`go-chi/chi` v5** en todos los servicios Go y en el api-gateway. Contrato **OpenAPI 3.1
como fuente** (diseño primero, en `packages/schemas/openapi/` o por servicio, según defina el
Agente 3 en [api.md](../api.md)); generación de tipos/servidor con `oapi-codegen` (modo
`chi-server` / `strict-server`) y validación de requests contra el esquema.

## Alternativas consideradas

- **`net/http` estándar (ServeMux con patrones de Go ≥ 1.22)**: suficiente para rutas, pero sin
  grupos de middlewares ni sub-routers tan cómodos; Chi añade poco y es 100 % compatible.
- **Gin / Echo / Fiber**: más "framework", contexto propio no estándar (Fiber ni siquiera usa
  `net/http`), lo que complica OpenTelemetry y middlewares estándar.
- **gRPC-gateway** (REST generado desde proto): descartado en [ADR-0013](0013-api-gateway-propio.md).

## Consecuencias

- (+) Handlers `http.Handler` estándar; cualquier middleware del ecosistema (otelhttp) funciona.
- (+) Generación desde OpenAPI mantiene doc y código sincronizados.
- (−) Menos "baterías incluidas" (binding/validación) que Gin: se cubre con oapi-codegen y un
  paquete compartido de errores (`application/problem+json`).
