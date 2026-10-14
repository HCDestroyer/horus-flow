# `mod:snmp` — services/snmp

- **Propósito:** Sondeo SNMPv3/ICMP por el túnel y estado observado de los routers (EP-08).
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
   `state_changed`, `rebooted` y `oper_status_changed` llevan `router_name` (y `site_id`/`site_name`, `uplink`,
   `thresholds`) para que alerts nombre el router en sus mensajes.
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
9. **Umbrales por ISP** (`GET|PUT|DELETE /routers/thresholds`; lectura `snmp.read`, escritura `snmp.manage`,
   auditado `snmp.thresholds.updated|reset`): CPU, memoria y temperatura (aviso y crítico) guardados en PostgreSQL
   (esquema `snmp`, tabla `router_thresholds` con RLS por tenant). Sin fila rigen los `HORUS_SNMP_*` de la
   instalación. El sondeador los carga con el rol de plataforma al arrancar y cada minuto; un `PUT` se aplica en
   caliente en el proceso que lo atiende. Sin `HORUS_POSTGRES_DSN` todos usan los de la instalación y el `PUT`
   responde 503.

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
| `HORUS_POSTGRES_DSN`, `HORUS_POSTGRES_PASSWORD` | — | Umbrales por ISP (esquema `snmp`). Vacío: solo los de la instalación. |
| `HORUS_SNMP_MIGRATE` | `true` | Aplica las migraciones PostgreSQL de `snmp` al arrancar. |
| `HORUS_SNMP_DB_APP_ROLE` / `_PLATFORM_ROLE` | `snmp_app` / `snmp_platform` | Roles de RLS. |
| `HORUS_CLICKHOUSE_DSN`, `HORUS_CLICKHOUSE_PASSWORD` | — | Como el ingester. Sin DSN: sin histórico. |
| `HORUS_CLICKHOUSE_SNMP_PASSWORD` | — | Usuario `horus_snmp` (escritor); vacío = usuario del DSN. |
| `HORUS_SNMP_CH_MIGRATE` | `true` | Aplica `snmp.*` al arrancar. |
| `HORUS_SNMP_CH_QUEUE` | `64` | Ciclos pendientes de escritura antes de descartar. |
| `HORUS_SNMP_INSTANCE_ID` | hostname | `instance_id` del heartbeat. |
| `HORUS_FLOWS_INVENTORY_FILE`, `HORUS_JWT_PUBLIC_KEYS`, `HORUS_AUTH_ISSUER` | — | Como analytics. |

## Métricas

`horus_snmp_polls_total{vendor,result}`, `horus_snmp_poll_duration_seconds{vendor}`,
`horus_snmp_poll_lag_seconds`, `horus_snmp_targets`, `horus_snmp_targets_stale`,
`horus_snmp_poller_heartbeat_timestamp_seconds`, `horus_snmp_routers{status}`,
`horus_snmp_publish_errors_total{kind}`, `horus_snmp_clickhouse_dropped_total`,
`horus_snmp_clickhouse_insert_rows_total{table}`, `horus_clickhouse_up{service="snmp"}`.
Logs `snmp: …` sin usuarios, contraseñas ni comunidades.

## Pruebas

`go test ./services/snmp/...` (máquina de estados, umbrales, deltas, interfaces, parseo con transporte
falso, engine con dobles, HTTP). Integración: `go test -tags integration ./services/snmp/internal/adapters/clickhouse/`
(migración real contra ClickHouse con `chtest`, escritura y lectura por tenant).
