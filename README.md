# Horus Flow

**Visibilidad de tráfico y detección de botnets para proveedores de Internet (ISP).**

Horus Flow recibe los flujos IPFIX/NetFlow del router principal de cada nodo, descubre solo a
los clientes por su IP y detecta los que participan en botnets: contacto con servidores de mando
y control, escaneos, DDoS, spam o beaconing. Cada hallazgo explica sus razones y su nivel de
confianza, y propone comandos RouterOS listos para copiar y deshacer. Horus **solo lee y avisa**:
nunca escribe en el router.

Es multi-ISP desde el diseño: varios proveedores, cada uno con sus nodos y routers, aislados entre
sí en datos, API, eventos y permisos.

| | |
| --- | --- |
| **Versión** | 1.0 (Incremento 1: NOC de un nodo MikroTik) |
| **Fabricante soportado** | MikroTik RouterOS 7 (≥ 7.12) |
| **Sistema base** | Debian 12 / 13 (amd64) |
| **Estado** | Aceptación automática en verde; pendiente de validar en un router real ([gate G1](docs/backlog/increment-1.md)) |

---

## Índice

- [Funciones](#funciones)
- [Cómo funciona](#cómo-funciona)
- [Requisitos](#requisitos)
- [Instalación](#instalación)
- [Conectar un MikroTik](#conectar-un-mikrotik)
- [Operación](#operación)
- [Fiabilidad y rendimiento medidos](#fiabilidad-y-rendimiento-medidos)
- [Seguridad y privacidad](#seguridad-y-privacidad)
- [Desarrollo](#desarrollo)
- [Documentación](#documentación)

## Funciones

- **Clientes descubiertos solos.** Cada IP vista en los flujos es un cliente (residencial por
  defecto; comercial por scoring o a mano). IPv4 e IPv6 por separado; IPv6 agrupado por prefijo
  delegado (/64 por defecto).
- **Atribución con NAT en el router.** Usa los campos post-NAT de IPFIX (IE 225–228) para saber de
  qué cliente es cada flujo de bajada; verificado con un MikroTik real.
- **Detección de botnets.** Nueve detectores y listas de reputación (abuse.ch Feodo/ThreatFox,
  Spamhaus DROP, Tor, RIPE RIS, RIR, PeeringDB, más listas propias del superadmin). Los clientes
  comprometidos se marcan como **Infectado**, siempre con razones y confianza.
- **Acciones recomendadas.** Comandos RouterOS para aislar, limitar o avisar, con su comando para
  deshacer. El operador decide y los aplica.
- **Dashboards modulares y modo kiosco** para las pantallas del NOC: widgets configurables, vista
  mural escalable y kiosco registrado como dispositivo (código QR de un solo uso, solo lectura).
- **Alertas** por correo, Telegram y LibreNMS (API), configuradas por ISP. Por defecto, correo y
  Telegram no llevan IPs de clientes.
- **Alta del router en minutos.** Túnel WireGuard iniciado por el router, script RouterOS generado
  por Horus, token de un solo uso e importación de prefijos de solo lectura.

## Cómo funciona

```
 MikroTik (router principal del nodo)
   │  WireGuard ── IPFIX (con campos NAT) · API de solo lectura
   ▼
 horus-collector ──► NATS JetStream ──► ingester ──► ClickHouse (flujos y agregados)
   │ spool a disco          │                              │
   │ si NATS no responde    ▼                              ▼
   │                   detection · alerts · devices   API de tráfico
   ▼                        │                              │
 estado del exportador      └────────► PostgreSQL ◄────────┘
                                       (inventario, hallazgos, usuarios; RLS por ISP)
                                            │
                                    gateway (REST + WebSocket) ◄── horus-web (Nuxt 4)
```

El backend es **un único binario Go** (`horus`) con roles seleccionables (`HORUS_ROLES`). El perfil
mínimo cabe en un servidor: `horus-app`, `horus-collector`, `horus-wg-agent` y `horus-web`, más
PostgreSQL 18, ClickHouse 26.8, NATS JetStream, Valkey y Traefik. Arquitectura completa en
[`docs/architecture.md`](docs/architecture.md).

## Requisitos

| Tamaño (`--size`) | Clientes del ISP | CPU | RAM | Disco |
| --- | --- | --- | --- | --- |
| Pequeño (`small`) | hasta ~300 | 4 núcleos | 8 GB | 100 GB SSD |
| Mediano (`medium`) | ~2 000 | 8 núcleos | 16 GB | 500 GB SSD + disco para copias |
| Grande (`large`) | ~10 000 | 8 núcleos | 32 GB | 500 GB NVMe + disco para copias |

- Debian 12 o 13 limpio (instalación mínima con SSH), acceso root.
- Puertos: HTTPS (o el de tu proxy) y el puerto UDP de WireGuard abierto hacia los routers.
- Router: MikroTik con RouterOS ≥ 7.12.

Dimensionado detallado y mediciones en [`tests/load/REPORT.md`](tests/load/REPORT.md).

## Instalación

Como root, en un Debian limpio:

```bash
apt-get install -y curl
curl -fsSLO https://github.com/hcdestroyer/horus-flow/releases/latest/download/bootstrap-debian.sh
bash bootstrap-debian.sh
```

El script instala Docker desde el repositorio oficial, WireGuard y la sincronización de hora,
aplica los ajustes del sistema (búfer UDP, etc.), descarga las imágenes firmadas y deja Horus
arrancando con el sistema. Se puede repetir sin romper nada. Sin Internet, usa el paquete offline
`horus-<versión>-linux-amd64.tar.gz`.

**Modos de acceso:**

| Modo | Certificado HTTPS |
| --- | --- |
| Solo IP (`--mode ip`) | autogenerado por Horus |
| Dominio o subdominio (`--mode domain` / `subdomain`) | Let's Encrypt, automático |
| Detrás de tu proxy (`--tls external`) | lo pone tu proxy (Nginx Proxy Manager, nginx…) |

Guía completa, paso a paso: [`docs/install-debian.md`](docs/install-debian.md).

> **Repositorio privado:** si las imágenes de GHCR son privadas, el servidor necesita un token de
> solo lectura (`read:packages`). Ver [`docs/install-debian.md`](docs/install-debian.md) §8.

## Conectar un MikroTik

1. En la UI, crea el ISP, el nodo y el router.
2. Genera el script de alta y pégalo en el terminal del router.
3. Registra la clave pública que muestra el router.
4. En menos de 2 minutos el router aparece como **Exportando** y empiezan a salir clientes.

Guía con ejemplos reales, medición de CPU antes y después, y cómo deshacerlo todo:
[`docs/guides/i1-mikrotik-real.md`](docs/guides/i1-mikrotik-real.md).

## Operación

Todo se gestiona con `horus-ctl` (como root):

| Orden | Qué hace |
| --- | --- |
| `horus-ctl status` | versión, URL, salud de cada servicio, copias y versión nueva disponible |
| `horus-ctl logs [servicio] [-f]` | registros |
| `horus-ctl upgrade` | actualiza con copia previa obligatoria, verificación de firma y vuelta atrás automática |
| `horus-ctl rollback` | vuelve a la versión anterior |
| `horus-ctl backup run \| verify \| status` | copias locales (PostgreSQL con pgBackRest y ClickHouse) |
| `horus-ctl restore` | restaura copias |
| `horus-ctl diagnose` | paquete de diagnóstico para investigar un fallo, sin datos de clientes |
| `horus-ctl uninstall [--purge]` | desinstala (conserva los datos salvo `--purge`) |

**Actualizaciones.** Horus avisa en la consola de plataforma cuando hay versión nueva. Canales
`stable` y `beta`. La actualización automática de parches es opcional y está desactivada por
defecto; las versiones mayores y menores siempre son manuales.

**Logs y rastro de fallos.** JSON estructurado en todos los roles, con `trace_id` propagado de la
petición HTTP a los eventos y consumidores, y un registro de eventos de plataforma (arranques,
caídas, migraciones, degradaciones) con retención de 90 días. Cómo investigar un fallo:
[`docs/observability.md`](docs/observability.md) §11.

## Fiabilidad y rendimiento medidos

Cifras de un servidor de pruebas de 4 vCPU compartido; los detalles y la metodología están en
[`tests/load/REPORT.md`](tests/load/REPORT.md) y [`docs/architecture.md`](docs/architecture.md) §10.

- **Reinicios bruscos** (`kill -9` y `restart`) de todos los componentes: 0 flujos perdidos, 0
  duplicados y agregados idénticos. La única pérdida es lo que llega por UDP mientras el colector
  está apagado, que se mide y se muestra.
- **ClickHouse caído 5 minutos a 20 000 flujos/s:** 0 pérdida; la cola se vacía en unos 2,5 min.
- **Plataforma:** alertas, hallazgos, sesiones, túneles y kiosco se recuperan solos en 7–15 s.
- **Capacidad:** 10 000 flujos/s sostenidos con todos los criterios en 4 vCPU compartidas.
  Proyección para un ISP de 10 000 clientes: servidor de 8 vCPU / 32 GB.
- **Disco:** unos 11 GB/día de flujos crudos para 10 000 clientes (7 días de crudo; agregados hasta
  25 meses por cliente y 5 años por nodo).

## Seguridad y privacidad

- Aislamiento entre ISPs en todas las capas: RLS en PostgreSQL, row policies en ClickHouse,
  cabecera de tenant en NATS y tokens con alcance por ISP.
- Horus nunca escribe en el router; su usuario en RouterOS es de solo lectura.
- Credenciales de integraciones cifradas y de solo escritura.
- 2FA (TOTP) para administradores; rate limit con la IP real del cliente, también detrás de un
  proxy (solo se confía en las cabeceras de los proxies declarados).
- Los logs de plataforma y el paquete de diagnóstico enmascaran las IPs de clientes.

Detalle en [`docs/security.md`](docs/security.md).

## Desarrollo

Requisitos: Go 1.26 (toolchain `go1.26.8`), GNU Make, Docker con compose v2, Node 22 y pnpm para
el frontend.

```bash
make help              # todos los objetivos
make up                # compose de desarrollo (perfil mínimo)
make build             # compila ./bin/horus
make test              # tests de Go
make lint              # golangci-lint + CODEOWNERS
make contracts-check   # contratos v0 (OpenAPI, eventos, Protobuf, DDL)
make accept-i1         # aceptación completa del incremento 1 (resumen OK/FAIL/SKIP)
```

Pruebas de carga y de fallo: `make load-i1`, `make load-isp10k`, `make chaos-i1`,
`make chaos-restart-flows` y `make chaos-restart-core`.

**Estructura del repositorio:**

```
apps/frontend/         UI Nuxt 4 + Nuxt UI
services/cmd/horus/    main único: compone los módulos según HORUS_ROLES
services/<módulo>/     gateway, auth, devices, wireguard, wgagent, snmp, collector, ingester,
                       traffic, analytics, detection, alerts, jobs
packages/              contratos (protobuf, events, schemas) y librerías Go comunes
infrastructure/        configuración de PostgreSQL, ClickHouse, NATS, Valkey, Traefik
deployments/           compose de desarrollo y producción, imágenes
scripts/               instalador, horus-ctl, backups, aceptación y CI
tools/flowsim/         simulador de flujos IPFIX/NetFlow v9 (incluye un ISP de 10 000 clientes)
tests/                 aceptación, carga, caos, instalación en Debian y fixtures
docs/                  diseño, ADRs, decisiones del PO, guías y backlog
```

Convenciones de código, flujo de trabajo y Definición de Terminado:
[`docs/conventions.md`](docs/conventions.md). Dueños de cada carpeta:
[`.github/CODEOWNERS`](.github/CODEOWNERS).

## Documentación

| Documento | Contenido |
| --- | --- |
| [`docs/install-debian.md`](docs/install-debian.md) | Instalación, actualizaciones, copias, proxy inverso |
| [`docs/guides/i1-mikrotik-real.md`](docs/guides/i1-mikrotik-real.md) | Conectar tu MikroTik paso a paso |
| [`docs/architecture.md`](docs/architecture.md) | Arquitectura, modos de fallo y límites medidos |
| [`docs/observability.md`](docs/observability.md) | Métricas, logs, trazas y cómo investigar un fallo |
| [`docs/security.md`](docs/security.md) | Aislamiento, autenticación, privacidad |
| [`docs/po-decisions.md`](docs/po-decisions.md) | Decisiones del product owner (D1–D23) |
| [`docs/README.md`](docs/README.md) | Índice completo del diseño |
