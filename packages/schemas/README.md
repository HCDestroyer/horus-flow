# packages/schemas — esquemas de API y documentos (contratos v0)

- **Dueño:** INT (contrato; `openapi/v0/gateway-routes.yaml` y `permissions/` son rutas sensibles: persona).
- **Gate:** G0 — ver [`docs/contracts/G0.md`](../../docs/contracts/G0.md).

| Ruta | Contrato | Contenido |
| --- | --- | --- |
| `openapi/v0/common.yaml` | C5 | Problem (RFC 9457), ErrorCode, PageInfo, parámetros, cabeceras, esquemas de seguridad (`tenantBearer` con `tid`…) |
| `openapi/v0/<módulo>.yaml` | C5 | Una spec por módulo dueño: auth, gateway, platform, devices, wireguard, flows, analytics, detection, alerts. Cada operación declara `x-module`, `x-scope`, `x-permission`, `x-principals`, `x-reauth`, `x-increment` |
| `openapi/v0/horus-api.yaml` | C5 | Raíz **generada** (no editar) |
| `openapi/v0/gateway-routes.yaml` | C5/C7 | Tabla ruta → módulo → ámbito → permiso **generada** desde las extensiones `x-*` |
| `openapi/dist/horus-api.v0.yaml` | C5 | Bundle **generado** (tipos TS, mocks Prism) |
| `permissions/v0/permissions.yaml` | C7 | Permisos, roles de sistema, token y lista blanca del kiosco |
| `websocket/v0/` | C6 | Temas del I1 (`topics.yaml`) y mensajes del protocolo `horus.ws.v1` |
| `finding/v0/` | C8 | Hallazgo con acciones recomendadas (D11) + ejemplos |
| `dashboard/v0/` | C9 | Documento de dashboard, tipo de widget, catálogo `widget-types.json`, playlist, datos de widget, manifiesto de presentación, plantillas "NOC del ISP" y "Seguridad" |
| `flowsim/v0/` | C10 | Formato de escenario del simulador y `expected.json` |
| `datastore/v0/` | C2/C3 | DDL de referencia PostgreSQL (multi-tenant, RLS) y ClickHouse (flujos y agregados) |
| `tools/` | — | `contracts-check.mjs` (todo lo verificable) y `ddl-check.py` |

## Verificación (`make contracts-check`)

```sh
cd packages/schemas
npm ci                                   # @redocly/cli, ajv, yaml (versiones fijadas)
BUF_BIN=$(command -v buf) npm run check  # lint OpenAPI, ámbito de tenant, tabla del gateway, JSON Schemas,
                                         # widgets/plantillas, catálogo de eventos + golden files vs Protobuf, buf lint
pip install pglast==7.2 chdb==3.1.2 && npm run check:db   # además, DDL v0 aplicable (PG parser + ClickHouse embebido)
npm run generate                         # regenera raíz, bundle y gateway-routes tras editar una spec de módulo
```

`make contracts-check` (lo cablea PLAT/coordinador) debe ejecutar: `npm ci --prefix packages/schemas` y
`npm --prefix packages/schemas run check:db` con `buf` ≥ 1.47 en PATH.
