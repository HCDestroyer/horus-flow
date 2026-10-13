# services/assist — módulo `assist` (asistente de IA)

- **Propósito:** análisis con IA de hallazgos y routers con herramientas de solo lectura sobre datos
  enmascarados, y las mismas herramientas como servidor MCP. Diseño completo en
  [`docs/assist.md`](../../docs/assist.md).
- **Rol:** `assist` (`HORUS_ROLES=…,assist`). Debe correr en el mismo proceso que `gateway` y los módulos
  de datos (las herramientas leen la API del proceso como el usuario) y, para MCP, que `auth`.
- **Dueño:** SEC (ver `.github/CODEOWNERS`).

## Variables de entorno

Ver [`docs/assist.md` §3.1](../../docs/assist.md#31-configuración). Las comunes: `HORUS_POSTGRES_DSN`,
`HORUS_POSTGRES_PASSWORD_FILE`, `HORUS_ASSIST_MIGRATE` (`true`), `HORUS_ASSIST_DB_APP_ROLE`
(`assist_app`), `HORUS_ASSIST_DB_PLATFORM_ROLE` (`assist_platform`), `HORUS_JWT_PUBLIC_KEYS` si auth no es
local.

## Rutas

`GET /api/v1/assist/status`, `GET|PUT /api/v1/assist/settings`, `POST /api/v1/assist/settings/test`,
`POST /api/v1/assist/analyses`, `GET /api/v1/assist/audit`, `GET|POST /api/v1/assist/mcp-tokens`,
`DELETE /api/v1/assist/mcp-tokens/{token_id}`, `POST /api/v1/assist/mcp` (MCP, token `hmcp_…`).

## Datos

PostgreSQL `assist`: `settings` (proveedor por ISP, clave cifrada), `audit` (consultas, sin datos de
clientes), `mcp_token` (solo hash). RLS forzada en las tres; `assist_platform` solo busca tokens por hash.

## Métricas y eventos

Solo las RED HTTP comunes (`http_server_*{service="assist"}`). No publica ni consume eventos; los cambios
de configuración y de tokens MCP se registran en la auditoría del ISP (auth).

## Pruebas

`go test ./services/assist/...` y `go test -tags integration ./services/assist/internal/adapters/postgres/`
(Docker o `HORUS_TEST_POSTGRES_DSN`). Ningún test llama a la API real de Anthropic.
