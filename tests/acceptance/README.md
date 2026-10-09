# tests/acceptance — pruebas de aceptación por incremento

- **Propósito:** baterías `make accept-iN` y e2e de humo que demuestran cada incremento de punta
  a punta, sin revisar código.
- **Dueño:** INT — [`docs/backlog/team.md`](../../docs/backlog/team.md) §2.
- **Historia:** I0-19 en [`docs/backlog/increment-0.md`](../../docs/backlog/increment-0.md);
  demostración de I0 en la cabecera de ese archivo y en [`docs/roadmap.md`](../../docs/roadmap.md) §3.

## `make accept-i0`

```bash
make accept-i0                                   # batería completa (~10 min con cachés)
make accept-e2e ACCEPT_KEEP=1                    # solo imagen + compose + e2e de API; deja la pila
ACCEPT_STEPS=lint,contracts make accept-i0       # solo algunos pasos
ACCEPT_SKIP=go-integration,lab-chr make accept-i0
```

Orquestador: [`scripts/accept/accept-i0.sh`](../../scripts/accept/accept-i0.sh). Cada paso
escribe su salida en `bin/accept-i0/<paso>.log`; al final se imprime un resumen con
**OK / FAIL / SKIP** por paso, su duración, las historias que cubre y, si falla, la ruta del log
(y las últimas 25 líneas en pantalla). Sale con código 1 si algún paso falla. Un paso cuya
dependencia falló queda en SKIP con el motivo.

| Paso | Historias | Qué comprueba |
| --- | --- | --- |
| `go-build` | I0-04 | `make build` y `bin/horus --version` |
| `go-test` | I0-04…I0-17 | `make test` (`go test -race -shuffle=on ./...`) |
| `go-integration` | I0-06…I0-09, I0-13 | `go test -tags=integration -race ./...` (testcontainers: migraciones up/down/up, RLS, matriz "A no ve B" de toda operación `scope: tenant`) |
| `lint` | I0-01, I0-03 | `make lint` (golangci-lint + CODEOWNERS) |
| `contracts` | I0-05 | `make contracts-check` (OpenAPI, eventos, buf, esquemas, DDL) |
| `sim-verify` | I0-10, I0-12 | `make sim-verify`: los seis escenarios sin router y los fixtures grabados |
| `image` | I0-04 | imagen única `horus:accept` (ver abajo), usuario 65532, `--version` |
| `compose-up` | I0-02 | compose de aceptación con el perfil `app` y la imagen local, `up --wait` |
| `healthy` | I0-02, I0-04 | todos los contenedores `healthy`, `/readyz` de horus-app en 200 con todos sus roles, y `/api/v1/system/status` vía Traefik responde 401 problem+json sin token |
| `migrations` | I0-06, I0-09, I0-13 | versión goose de `auth` y `devices`, RLS en toda tabla con `tenant_id` (salvo outbox), `make migrate-ch` idempotente y tablas `flows.*`/`dim.*` presentes |
| `e2e-api` | I0-06…I0-09 | [`i0/smoke_test.go`](i0/smoke_test.go) contra el backend real (abajo) |
| `frontend-build` | I0-14…I0-16 | `pnpm install --frozen-lockfile && pnpm build:mocks` |
| `frontend-e2e` | I0-14…I0-16 | Playwright `--grep @i0` contra la SPA generada con mocks (login → selector de ISP → dashboard vacío, layout, tema, accesibilidad) |
| `lab-chr` | I0-11 | `make lab-selftest` si existe `/dev/kvm`; si no, **SKIP** con el motivo |

### E2E de humo contra el backend real (`e2e-api`)

`go test -tags acceptance ./tests/acceptance/i0/` habla con la API pública a través de Traefik
(`http://127.0.0.1:${HORUS_HTTP_PORT}`), es decir, el mismo camino que el navegador:

1. Sin token, `/system/status` y `/sites` → 401 `UNAUTHENTICATED`; login con contraseña errónea
   → 401 sin revelar el usuario.
2. Login del superadmin semilla → token de sesión; `/system/status` con `auth` e `inventory` en
   `ok`; el token de plataforma se niega (`PASSWORD_CHANGE_REQUIRED`) hasta cambiar la contraseña
   y después (`MFA_ENROLLMENT_REQUIRED`) hasta activar TOTP.
3. Enrolamiento TOTP (`/me/totp/enroll` + `/confirm`), nuevo login que exige el segundo factor
   (código erróneo → 401, correcto → tokens) y `/me` con `mfa_enabled`.
4. Token de plataforma y alta de dos ISP vía `POST /platform/tenants` (con `Idempotency-Key`).
5. `POST /auth/token` para el ISP A; alta de nodo, router principal y `client_prefix`; un
   prefijo solapado se rechaza con 409 `CLIENT_PREFIX_OVERLAP`.
6. Con el token del ISP B: nodo, router y prefijos de A → **404**; listados vacíos; router en el
   nodo de A → 422; `tenant_id` de A en el cuerpo → 403 `TENANT_MISMATCH`.

Necesita una base recién creada: cambia la contraseña del superadmin semilla y activa su TOTP.

### Compose de aceptación

La batería no usa la pila de `make up`: levanta un proyecto aparte (`ACCEPT_PROJECT`, por defecto
`horus-accept`) con **volúmenes nuevos** y los puertos publicados desplazados
(`ACCEPT_PORT_OFFSET=20000`: API en `:28000`, admin de horus-app en `:28081`, PostgreSQL en
`:25432`…), así no toca los datos ni choca con los puertos de desarrollo. Usa
`deployments/compose/.env` (lo crea `compose-preflight.sh` desde `.env.example`) y los secretos de
`deployments/compose/secrets/`, incluido `seed_admin_password.txt`. Al terminar hace
`down --volumes` salvo `ACCEPT_KEEP=1`; si algo falló, guarda `bin/accept-i0/compose-logs.txt`.

### Imagen: Dockerfile raíz o binario local

[`scripts/accept/image.sh`](../../scripts/accept/image.sh) (`make accept-image`) construye
`horus:accept` con el Dockerfile raíz. Si `docker build` falla —típicamente en entornos con un
proxy HTTPS que sustituye certificados, donde `go mod download` dentro del contenedor de build
falla con `x509: certificate signed by unknown authority` porque no conoce la CA del proxy—
compila el binario en el host con los mismos flags (`CGO_ENABLED=0 -trimpath -s -w`) y monta una
imagen equivalente con [`i0/Dockerfile.prebuilt`](i0/Dockerfile.prebuilt): misma base
`distroless/static:nonroot` fijada por digest (se lee del Dockerfile raíz), mismo `/horus`,
usuario 65532 y `HORUS_DATA_DIR`. El resumen lo indica en la nota del paso `image`
(`prebuilt: …`). `ACCEPT_IMAGE_MODE=dockerfile` desactiva el respaldo (CI);
`ACCEPT_IMAGE_MODE=prebuilt` lo fuerza.

### Requisitos

Go (toolchain de `go.mod`), Docker con Compose v2, `golangci-lint` v2 (`GOLANGCI_LINT`), `buf`
(`BUF_BIN` o en el `PATH`), Node ≥ 22.12 con pnpm y Chromium de Playwright
(`PLAYWRIGHT_BROWSERS_PATH`, versión fijada por `@playwright/test`). Para `lab-chr`: Linux, sudo y
`/dev/kvm`.

Si el daemon de Docker no admite el `nofile` de ClickHouse (262144) —"error setting rlimit" al
levantar— baja `HORUS_CH_NOFILE` en `deployments/compose/.env` (p. ej. 16384).
