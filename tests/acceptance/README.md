# tests/acceptance — pruebas de aceptación por incremento

- **Propósito:** baterías `make accept-iN` y e2e de humo que demuestran cada incremento de punta
  a punta, sin revisar código.
- **Dueño:** INT — [`docs/backlog/team.md`](../../docs/backlog/team.md) §2.
- **Historias:** I0-19 en [`docs/backlog/increment-0.md`](../../docs/backlog/increment-0.md) e I1-24
  en [`docs/backlog/increment-1.md`](../../docs/backlog/increment-1.md); demostraciones de I0 e I1 en
  [`docs/roadmap.md`](../../docs/roadmap.md) §3.

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
| `lint` | I0-01, I0-03 | `make lint` (golangci-lint + CODEOWNERS) y golangci-lint con `--build-tags acceptance` sobre este directorio; caché propia en `bin/accept-i0/golangci-cache` |
| `contracts` | I0-05 | `make contracts-check` (OpenAPI, eventos, buf, esquemas, DDL) |
| `sim-verify` | I0-10, I0-12 | `make sim-verify`: los seis escenarios sin router y los fixtures grabados |
| `image` | I0-04 | imagen única `horus:accept` (ver abajo), usuario 65532, `--version` |
| `compose-up` | I0-02 | compose de aceptación con el perfil `app` y la imagen local, `up --wait` |
| `healthy` | I0-02, I0-04 | todos los contenedores `healthy`, `/readyz` de horus-app en 200 con todos sus roles, y `/api/v1/system/status` vía Traefik responde 401 problem+json sin token |
| `migrations` | I0-06, I0-09, I0-13 | versión goose de `auth` y `devices`, RLS en toda tabla con `tenant_id` (salvo outbox), `make migrate-ch` idempotente y tablas `flows.*`/`dim.*` presentes |
| `e2e-api` | I0-06…I0-09 | [`i0/smoke_test.go`](i0/smoke_test.go) contra el backend real (abajo) |
| `go-integration` | I0-06…I0-09, I0-13 | `go test -tags=integration -race ./...`: testcontainers (migraciones up/down/up, RLS, matriz "A no ve B" de toda operación `scope: tenant`) y esquema ClickHouse contra el ClickHouse del compose de aceptación (`HORUS_CH_TEST_DSN`; va después de `e2e-api` porque borra `flows`/`dim`) |
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

## `make accept-i1`

```bash
sudo make accept-i1                                   # TARGET=local: instalación de prueba (~20 min)
sudo make accept-i1 ACCEPT_KEEP=1                     # deja la instalación y los routers simulados
sudo ACCEPT_STEPS=go-build,image,install,install-check,e2e make accept-i1
sudo ACCEPT_E2E_STAGES=platform,inventory,onboarding,tunnel ACCEPT_STEPS=go-build,install,e2e make accept-i1
sudo TARGET=installed ACCEPT_ADMIN_PASSWORD_FILE=/root/pw make accept-i1   # contra /opt/horus
```

Orquestador: [`scripts/accept/accept-i1.sh`](../../scripts/accept/accept-i1.sh), mismo formato
que `accept-i0` (logs en `bin/accept-i1/<paso>.log`, resumen **OK / FAIL / SKIP** con historias;
sale con 1 si algo falla). El paso `e2e` se desglosa en etapas, cada una con su **criterio** y sus
historias: si una falla, el resumen imprime `CRITERIO: … — motivo`. Necesita **root**.

**Contra qué corre.** No usa el compose de desarrollo: la demostración de I1 es la de un servidor
instalado, así que el destino es una instalación real hecha con
[`scripts/install.sh`](../../scripts/install.sh) (compose de producción: Traefik con TLS, NATS con
contraseña, usuarios de ClickHouse por rol y row policies, hub WireGuard, filtro DOCKER-USER de
IPFIX):

- `TARGET=local` (defecto): `install.sh --root /tmp/horus-accept-i1 --mode ip` con la imagen de
  este árbol (`scripts/accept/image.sh`; en sandboxes con proxy, desde el binario local),
  proyecto `horus-accept-i1`, HTTPS en `:21443` (`ACCEPT_PORT_OFFSET`), WireGuard en `:51841`,
  túneles `10.241.0.0/16`, interfaz `hfwgacci1`. Se desinstala con `--purge` al terminar salvo
  `ACCEPT_KEEP=1`. No toca `/opt`, `/etc` ni `/var` del host.
- `TARGET=installed`: la instalación del servidor (`ACCEPT_INSTALL_ROOT`, por defecto `/`). Lee
  su `.env` (URL pública, TLS, almacén, secretos). Crea ISP `accept-i1-*`, añade dos C2 de prueba
  (IPs de documentación `203.0.113.66/.77`, fuente `flowsim-test-feed`) al snapshot de reputación
  conservando el resto, y conecta routers simulados al hub. Credenciales: en una instalación
  recién hecha basta la contraseña semilla (`ACCEPT_ADMIN_PASSWORD_FILE`; la batería cambia la
  contraseña y activa TOTP y lo guarda en `bin/accept-i1/admin-creds.json`, 0600); si el
  superadmin ya tiene TOTP, también `ACCEPT_ADMIN_TOTP_SECRET_FILE`.

**Routers simulados.** [`scripts/accept/sim-router.sh`](../../scripts/accept/sim-router.sh) crea
un namespace de red por router (`hfsim-<nodo>`) con una interfaz WireGuard configurada **con los
valores del script de onboarding que genera Horus** (IP de túnel, clave y endpoint del hub, red de
servicios, colector). La clave privada se genera allí y solo se registra la pública con
`POST /enroll/wireguard`. Los flujos (`flowsim` de cada escenario a ritmo real, y
[`i1/pcapreplay`](i1/pcapreplay/main.go) para la captura real) viajan por ese túnel hasta el
colector, con la IP de túnel como origen: el mismo camino que un MikroTik.

| Paso | Historias | Qué comprueba |
| --- | --- | --- |
| `go-build` | I0-04 | `make build`, `flowsim` y `pcapreplay` |
| `lint` | I1-24 | golangci-lint con `--build-tags acceptance` sobre `tests/acceptance` y shellcheck de los scripts |
| `sim-verify` | I0-10, I0-12 | los seis escenarios sin router y la captura real con su `expected.json` |
| `flows-golden` | I1-04, I1-24 | `make test-flows-golden`: la captura real por collector + ingester contra ClickHouse (testcontainers) |
| `image` | I0-04 | imagen `horus:accept-i1` (solo `TARGET=local`) |
| `install` | I1-22 | `install.sh` en el raíz temporal (o comprobación de la instalación existente) |
| `install-check` | I1-22 | `install.sh --check`: salud de cada rol, puertos, WireGuard, filtro IPFIX, TLS |
| `e2e` | I1-01…I1-21 | [`i1/`](i1/) contra la API pública por HTTPS, etapas abajo |
| `frontend-build`, `frontend-e2e` | I1-16…I1-21 | Playwright `@clients @traffic @security @onboarding @widgets @kiosk @platform` contra la API simulada |
| `lab-chr` | I1-25 | `make lab-selftest` si hay `/dev/kvm`; si no, **SKIP** |

Etapas del `e2e` (un ISP por escenario para que sus prefijos públicos no se solapen; el ISP
`demo` lleva el escenario `normal` y la captura real en otro nodo; el ISP `otro` queda vacío):

| Etapa | Historias | Criterio |
| --- | --- | --- |
| `platform` | I0-06, I1-31 | superadmin (cambio de contraseña y TOTP la primera vez), `/system/status`, alta de los ISP |
| `inventory` | I0-09, I1-01 | nodo, router principal y prefijos del `expected.json`; IP de túnel única asignada por wireguard |
| `reputation` | I1-10, I0-17 | feed de prueba con los C2 del escenario `c2` en el snapshot de reputación |
| `onboarding` | I1-02, I1-01 | script RouterOS 7.12 (`text/plain`, `no-store`, IPFIX con NAT, `active-flow-timeout=1m`, **sin `comment` en `/ip traffic-flow target add`**), enrolamiento 202 `pending_handshake`, token reutilizado → `ENROLLMENT_TOKEN_INVALID`, script inverso |
| `tunnel` | I1-01 | handshake WireGuard real de los 7 routers y peer `active` en < 60 s |
| `flows` | I1-03, I0-10, I0-12 | seis escenarios (semilla 1, `-fixture`, a ritmo real) y captura real por el túnel |
| `exporters` | I1-09 | `GET /flow-exporters`: *Exportando* (la captura real, de otro día, en `clock_skew`) |
| `customers` | I1-05, I1-06 | clientes descubiertos = claves del `expected.json` (236 en la captura real), tipo `residential` |
| `real-flows` | I1-04, I1-24 | filas de la captura real en `flows.flows_raw` por estado de atribución = `expected.json` |
| `traffic` | I1-07, I1-08 | tops por cliente, servicio, categoría, organización y ASN no vacíos |
| `findings` | I1-10…I1-12 | exactamente los hallazgos del `expected.json` (cliente, tipo, severidad); ninguno en `normal` ni `commercial`; razones, evidencia, confianza y nunca «infectado» |
| `websocket` | I1-13, I1-12 | reconocer un hallazgo emite el evento al ISP dueño y no al ISP `otro`; falso positivo |
| `kiosk` | I1-14, I1-15, I1-21 | alta, código de un uso, cookie de dispositivo → JWT, `/kiosk/config`, dashboard NOC y sus widgets, **`GET /flow-exporters` con JWT de kiosco**, `403 KIOSK_FORBIDDEN` fuera de la lista blanca, WebSocket |
| `isolation` | I0-08, I1-24 | ISP `otro`: 404 en recursos ajenos y listados/tops/resúmenes vacíos; row policies de ClickHouse (`horus_analytics` con `SQL_horus_tenant`); RLS en PostgreSQL |
| `silent` | I1-09 | el exportador pasa a *Silencioso* ≤ 2 min después del último flujo |
| `cleanup` | — | con `TARGET=installed`, suspende los ISP `accept-i1-*` creados |

Requisitos: los de `accept-i0` más root, `wireguard-tools` y el módulo `wireguard` o
`wireguard-go`, e `iproute2` con `ip netns`.
