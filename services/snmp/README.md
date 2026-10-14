# `mod:snmp` — services/snmp

- **Propósito:** Sondeo SNMPv3/ICMP por el túnel y estado observado de los routers (EP-08); vigilancia de los servicios de infraestructura del ISP (IPTV, HTTP, DNS…).
- **Rol(es) de `horus`:** `snmp`
- **Agente dueño:** FLOW (decisión D21 en docs/po-decisions.md) ([`docs/backlog/team.md`](../../docs/backlog/team.md) §2, [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · Roles y fronteras: [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)
- **Layout y reglas:** [`docs/conventions.md`](../../docs/conventions.md) §2.1 — `api/` (contrato público), `internal/` (`config`, `domain`, `app`, `adapters`), `migrations/clickhouse/` (esquema `snmp.*`). Otros módulos solo importan `services/snmp/api`.

## Qué hace (I2)

1. **Objetivos:** routers de todos los ISP con `tunnel_address` y `onboarding_state ∈ {tunnel_up, exporting}`
   (`devices.Onboarding.ListRouters` del proceso; se refresca cada minuto y al llegar `horus.devices.router.*`).
   Nombres de router y nodo del snapshot de inventario (`flowinv`). La credencial SNMPv3 se revela con
   `devices.SNMPCredentialReader` (kind `snmp_v3`, descifrada con la KEK de devices), se memoriza y se
   recarga tras un fallo de autenticación o un cambio del router. Sin credencial el router queda
   `degraded/no_snmp_credentials` (solo ICMP).
2. **SNMPv3** (`gosnmp`, authPriv con lo que diga la credencial: sha1|sha256 + aes128) por la IP del túnel,
   udp/161, un poller por router, `HORUS_SNMP_CONCURRENCY` routers a la vez. Cada ciclo: `sysDescr`
   (modelo), `sysUpTime` (reinicios), `hrProcessorLoad` (media y núcleo más cargado), fila RAM de
   `hrStorageTable`, contadores HC de las interfaces sondeadas. Cada `HORUS_SNMP_HEALTH_INTERVAL`:
   MIKROTIK-MIB (temperatura de placa y CPU, voltaje, versión RouterOS, serie, tabla de sensores
   `mtxrGaugeTable`). Los OIDs ausentes dan `null`, nunca error. Descubrimiento de interfaces al arrancar
   y cada hora (walk GETBULK de `ifName/ifAlias/ifType/ifHighSpeed/ifAdminStatus/ifOperStatus`); se
   sondean ethernet/sfp/wlan, `l2vlan`, `bridge` y `ieee8023adLag`, nunca `<pppoe-…>` ni PPP/túneles;
   `interface_id` = UUIDv5(router_id, ifName). Tasas por delta; `counter_discontinuity` (primer sondeo,
   uptime menor, contador que retrocede) ⇒ tasas `null`. Uplink: física/VLAN/bonding con más `tx_bps`
   medio; gana la marcada `upstream` en el inventario o cuyo alias contiene UPLINK/WAN/TRANSIT/UPSTREAM.
3. **ICMP** cada 30 s (3 sondas): socket `udp4` sin privilegios (`net.ipv4.ping_group_range`, lo fija el
   compose), si no raw (`CAP_NET_RAW`), si tampoco la señal queda `unknown` (se avisa una vez al arrancar).
   La señal pasa a `fail` tras 3 ciclos sin respuesta.
4. **Estado observado** (`internal/domain`): correlación de ICMP, SNMP, handshake WG (consumidor
   `snmp-wireguard` de `WIREGUARD_EVENTS`) y exportador (KV `flow_exporter_state`) con **histéresis de 2
   observaciones** (architecture.md §10.4). Umbrales: CPU y memoria sostenidos 3 ciclos, temperatura 1.
   **Estado efectivo** (`stale` > 3 intervalos o sondeador sin latido; `unknown` nunca observado) se
   calcula al servir `List`/`Live`.
5. **Persistencia:** NATS KV `snmp_router_state` (clave `router_id`, JSON con estado, muestra, contadores e
   interfaces) para conservar `since` entre reinicios. Sin NATS, solo memoria.
6. **Eventos** (`SNMP_EVENTS`, JSON, PubAck): `router.state_changed`, `router.rebooted`,
   `router.interfaces_discovered`, `interface.oper_status_changed`, `poller.heartbeat` (15 s, plataforma).
   **Telemetría** (`TLM_SNMP`, JSON con el sobre en cabeceras): `horus.telemetry.snmp.device.<router_id>` y
   `…interface.<router_id>`.
7. **ClickHouse** (`migrations/clickhouse`, tabla de control `snmp.goose_db_version`, usuario `horus_snmp`):
   `snmp.device_metrics`, `snmp.interface_metrics` (30 d) y agregados `device_5m/1h`, `interface_5m/1h`
   (90 d / 13 meses, MV en cascada, política por tenant `p_tenant`). Escritor asíncrono con cola acotada:
   si ClickHouse cae, se descartan lotes (`horus_snmp_clickhouse_dropped_total`) y el sondeo sigue.
8. **HTTP:** `GET /routers/status` (problemas primero; `site_id`, `status`), `GET /routers/{id}/metrics/live`,
   `GET /routers/{id}/interfaces`, `POST /routers/{id}/poll` (`Idempotency-Key` obligatoria → 428;
   `devices.update`; 1 cada 10 s por router → 429; auditado como `snmp.router.poll_requested`).
   Kiosco permitido en las lecturas. En `module.Services`: `api.ServiceRouterStatus` (`RouterStatusReader`
   + `InterfaceLister`) para analytics y el gateway.

## Servicios de infraestructura (I2)

Inventario y vigilancia de los **servidores propios del ISP** (IPTV, HTTP, DNS, RADIUS…; nunca clientes). Vive en este
rol porque es quien ya sondea por el túnel (socket ICMP compartido) y quien publica `SNMP_EVENTS`; es el primer uso de
PostgreSQL del rol (el estado de los routers sigue en NATS KV).

1. **Inventario** (PG, esquema `snmp`, `migrations/postgres`): `infra_service` (nombre único por ISP, `site_id`
   opcional, `target` IPv4/IPv6, `kind` libre, `checks` jsonb, `interval_seconds` ≥ 30, `failures_before_down` (3),
   `recoveries_before_up` (2), `enabled`, `probe_via_router_id`) e `infra_check_state` (estado por chequeo, latencia,
   contadores, últimos 30 resultados). RLS forzada `p_tenant`; roles `snmp_app` (API) y `snmp_platform` (carga del
   planificador y relay del outbox). Máximo 500 servicios por ISP.
2. **API** (`snmp.yaml`, tag `infra-services`): `GET|POST /infra-services`, `GET|PATCH|DELETE /infra-services/{id}`
   (`If-Match`; el detalle trae `recent`) y `GET /infra-services/{id}/router-snippet`. Lectura `snmp.read`, escritura
   `snmp.manage`; alcance por nodo (los servicios sin nodo exigen alcance completo). Auditado `snmp.infra_service.*`.
   Sin `HORUS_POSTGRES_DSN` la API responde 503.
3. **Chequeos activos** (`adapters/probe`, desde el rol): `icmp` (3 ecos con el socket del sondeador; IPv6 y sin socket
   → `unknown`), `tcp` (conexión al puerto) y `http` (GET con plazo; código en `expected_status_min..max`, 200–399 por
   defecto; no se lee el cuerpo, no se siguen redirecciones, nunca por el proxy del entorno; `tls_verify` por chequeo).
   Plazo `HORUS_SNMP_INFRA_TIMEOUT` acotado a la mitad del intervalo.
4. **Chequeos pasivos** (ClickHouse, `adapters/clickhouse/flows.go`): una consulta por ISP y ciclo a `flows.flows_raw`
   (tráfico por minuto de cada objetivo, como `client_ip` o `remote_ip`; la ventana termina 2 min antes de ahora por la
   latencia de exportación). `no_traffic`: menos de `min_bytes` en `window_minutes` → `down`, **solo si el objetivo tuvo
   tráfico antes** (`had_traffic`; si no, `unknown/no_traffic_baseline`). `ddos_inbound`: pps/bps entrantes medios de 5
   min por encima del umbral → `degraded/ddos_inbound` (entrante = `download` con el objetivo en `client_ip`, `upload`
   con el objetivo en `remote_ip`). Usuario `horus_snmp` con `horus_tenant_reader` (migración
   `20261014120000_snmp_flows_reader.sql`) y `SQL_horus_tenant` en cada consulta. **Límite:** el ingester no guarda la
   IP de un servidor de un prefijo `infrastructure` cuando habla con Internet (solo el extremo remoto), así que esos
   flujos no cuentan; sí los de clientes ↔ servidor y los de servidores sin prefijo.
5. **Planificador** (`app/infra_scheduler.go`): recarga el inventario cada minuto y tras cada alta/edición/baja; cada
   servicio cada `interval_seconds` + jitter (≤ 10 %), primer turno repartido en el intervalo; `HORUS_SNMP_INFRA_CONCURRENCY`
   servicios a la vez. **Histéresis** por chequeo: `failures_before_down` observaciones malas iguales seguidas →
   `down`/`degraded`; `recoveries_before_up` buenas → `up` (desde `unknown` basta una); `unknown` solo tras
   max(failures_before_down, 3) observaciones sin datos. Estado del servicio = el peor conocido (down > degraded > up;
   `unknown` si ninguno sabe). Si no se puede guardar, no se adopta el estado (el evento se reintenta en el ciclo
   siguiente). Un único rol `snmp` activo por instalación (como el sondeador de routers).
6. **Evento** `horus.snmp.service.state_changed.<service_id>` (catálogo `packages/events/catalog/snmp.yaml`, mensaje
   `horus.events.snmp.v1.ServiceStateChanged`) por el **outbox** `snmp.outbox` en la misma transacción que el estado:
   `{tenant_id, service_id, site_id|null, name, check_type, target, previous_state, state, reason, observed_at}`, con el
   chequeo que causó el cambio. Lo consume alerts (notificaciones).
7. **Servidores privados** (RFC 1918, ULA, CGNAT): el hub WireGuard solo enruta la /32 de túnel de cada router (wg-agent
   rechaza subredes y el hub es compartido entre ISP con redes privadas solapadas), así que Horus **no** amplía
   AllowedIPs. En su lugar, el servicio fija `probe_via_router_id` y cada chequeo TCP/HTTP un `forward_port`: Horus sondea
   `IP de túnel del router:forward_port` y el router lo reenvía. `router-snippet` genera las reglas que el **usuario**
   pega (dst-nat + masquerade + accept en forward, limitadas a `HORUS_WG_SERVICES_CIDR`, `comment="horus-infra <id>"`).
   El ping no se puede reenviar (`unknown/not_forwardable`). Horus nunca escribe en routers.

## Variables de entorno

| Variable | Defecto | Qué hace |
| --- | --- | --- |
| `HORUS_SNMP_ENABLED` | `true` | `false`: el rol sirve la API con `meta.poller.enabled=false` y no sondea. |
| `HORUS_SNMP_INTERVAL` | `60s` | Sistema e interfaces. |
| `HORUS_SNMP_HEALTH_INTERVAL` | `300s` | Temperatura, voltaje, sensores, versión. |
| `HORUS_SNMP_ICMP_INTERVAL` | `30s` | Ping por el túnel. |
| `HORUS_SNMP_DISCOVERY_INTERVAL` | `1h` | Redescubrimiento de interfaces. |
| `HORUS_SNMP_TIMEOUT` / `HORUS_SNMP_RETRIES` | `5s` / `1` | Por petición SNMP. |
| `HORUS_SNMP_CONCURRENCY` | `32` | Routers sondeados a la vez. |
| `HORUS_SNMP_CPU_WARNING` / `_CRITICAL` | `80` / `95` | % sostenido 3 ciclos. |
| `HORUS_SNMP_MEMORY_WARNING` / `_CRITICAL` | `85` / `95` | % sostenido 3 ciclos. |
| `HORUS_SNMP_TEMPERATURE_WARNING` / `_CRITICAL` | `70` / `85` | °C, 1 ciclo. |
| `HORUS_CLICKHOUSE_DSN`, `HORUS_CLICKHOUSE_PASSWORD` | — | Como el ingester. Sin DSN: sin histórico. |
| `HORUS_CLICKHOUSE_SNMP_PASSWORD` | — | Usuario `horus_snmp` (escritor); vacío = usuario del DSN. |
| `HORUS_SNMP_CH_MIGRATE` | `true` | Aplica `snmp.*` al arrancar. |
| `HORUS_SNMP_CH_QUEUE` | `64` | Ciclos pendientes de escritura antes de descartar. |
| `HORUS_SNMP_INSTANCE_ID` | hostname | `instance_id` del heartbeat. |
| `HORUS_FLOWS_INVENTORY_FILE`, `HORUS_JWT_PUBLIC_KEYS`, `HORUS_AUTH_ISSUER` | — | Como analytics. |
| `HORUS_POSTGRES_DSN`, `HORUS_POSTGRES_PASSWORD` | — | Servicios de infraestructura (esquema `snmp`). Sin DSN: API 503. |
| `HORUS_SNMP_DB_APP_ROLE` / `HORUS_SNMP_DB_PLATFORM_ROLE` | `snmp_app` / `snmp_platform` | Roles de PostgreSQL. |
| `HORUS_SNMP_PG_MIGRATE` | `true` | Aplica el esquema `snmp` de PostgreSQL al arrancar. |
| `HORUS_SNMP_INFRA_CONCURRENCY` | `32` | Servicios de infraestructura sondeados a la vez. |
| `HORUS_SNMP_INFRA_TIMEOUT` | `5s` | Plazo de cada chequeo activo (≤ la mitad del intervalo). |
| `HORUS_WG_SERVICES_CIDR` | `10.255.0.0/24` | Red de servicios de Horus (origen de los sondeos en el script de router). |
| `HORUS_PUBLIC_BASE_URL` | — | Base de `Location` en el alta. |

## Métricas

`horus_snmp_polls_total{vendor,result}`, `horus_snmp_poll_duration_seconds{vendor}`,
`horus_snmp_poll_lag_seconds`, `horus_snmp_targets`, `horus_snmp_targets_stale`,
`horus_snmp_poller_heartbeat_timestamp_seconds`, `horus_snmp_routers{status}`,
`horus_snmp_publish_errors_total{kind}`, `horus_snmp_clickhouse_dropped_total`,
`horus_snmp_clickhouse_insert_rows_total{table}`, `horus_clickhouse_up{service="snmp"}`,
`horus_snmp_infra_checks_total{type,result}`, `horus_snmp_infra_services{state}`.
Logs `snmp: …` sin usuarios, contraseñas ni comunidades.

## Pruebas

`go test ./services/snmp/...` (máquina de estados, umbrales, deltas, interfaces, parseo con transporte
falso, engine con dobles, HTTP). Integración: `go test -tags integration ./services/snmp/internal/adapters/clickhouse/`
(migración real contra ClickHouse con `chtest`, escritura y lectura por tenant);
`go test -tags integration ./services/snmp/internal/adapters/postgres/` (almacén de servicios: RLS, outbox) y
`go test -tags integration ./tests/tenancy/ -run TestInfraServicesAPI` (API y aislamiento entre ISP).
