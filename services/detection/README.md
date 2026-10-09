# `mod:detection` — services/detection

- **Propósito:** Reputación, correlación (detección de botnets), hallazgos y scoring.
- **Rol(es) de `horus`:** `detection`
- **Agente dueño:** SEC ([`docs/backlog/team.md`](../../docs/backlog/team.md) §2, [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · Roles y fronteras: [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)
- **Layout y reglas:** [`docs/conventions.md`](../../docs/conventions.md) §2.1 — `api/` (contrato público), `internal/` (`config`, `domain`, `app`, `adapters`), `migrations/` (si tiene esquema PostgreSQL). Otros módulos solo importan `services/detection/api`.

## Motor de detección y hallazgos (I1-10, I1-11, I1-12, I1-30)

Horus **solo avisa** (D11, ADR-0024 §4): cada hallazgo trae acciones recomendadas con comandos
RouterOS como plantilla y su comando para deshacer, con `execution: manual`. Nunca escribe en el router.

### Piezas

| Paquete | Qué hace |
| --- | --- |
| `internal/engine` | Motor por tenant: detectores parametrizables por ISP, ventanas y cadencias, cliente/router, muestreo, allowlist, deduplicación de candidatos |
| `internal/adapters/clickhouse` | Lector `horus_detection` (rol lector por tenant): toda consulta lleva `tenant_id = ?` **y** `SQL_horus_tenant` (row policy p_tenant) |
| `internal/app` | Capa de hallazgos (deduplicación, silencio, reincidencia, auto-expiración, estado de seguridad, outbox) y API del contrato |
| `internal/actions` | Acciones recomendadas por kind (C8 `RecommendedAction`); `/ipv6 firewall` para clientes IPv6 (D22) |
| `internal/adapters/postgres`, `migrations/` | Esquema `detection` con RLS fail-closed |
| `itest/`, `tests/detection` | Integración: ciclo de vida en PostgreSQL; escenarios del simulador por collector + ingester reales a ClickHouse con el módulo real (`detection.Evaluate`) |

### Detectores y umbrales por defecto (`domain.DefaultParams`, ajustables por ISP en `detection.detector_config`)

| Detector (`detector_config.detector`) | kind | Regla por defecto (traffic-model.md §8) | Severidad |
| --- | --- | --- | --- |
| `c2_contact` | `botnet_c2_communication` | indicador `botnet_cc` (confianza ≥ 50) marcado en ingesta (`reputation_hit`, 2 h) y verificado en `flows_raw`; barrido retroactivo 7 d cada 6 h o con snapshot nuevo | high con respuesta, medium solo SYN |
| `c2_contact` | `cryptomining` / `reputation_hit` | pool de minería o distribución de malware **con respuesta** | medium / low |
| `scanning` | `outbound_scanning` | > 70 % SYN sin ACK y ≥ 100 destinos en 5 min (horizontal); ≥ 50 puertos TCP de un destino con ≥ 50 % de flujos ≤ 3 paquetes (vertical) | high / medium |
| `watched_ports` | `outbound_scanning` | ≥ 20 destinos a puertos de `dim.watch_port` en 5 min (25 excluido: lo cubre SMTP) | medium |
| `fanout` | `outbound_fanout` | > 500 IPs y > 200 /24 en 5 min con flujos **iniciados** por el cliente (local ≥ 1024 o remoto < 1024; ×2 para comerciales); solo si no es escaneo TCP | medium |
| `smtp` | `spam_smtp_outbound` | ≥ 20 servidores SMTP distintos (25/tcp) en 1 h; 200 para comerciales | high |
| `ddos` | `ddos_participation` | > 2 000 pps a ≤ 3 destinos durante ≥ 2 min seguidos (reparto por minutos como el simulador); o UDP de amplificación (53, 123, 1900, 11211, 19) > 1 000 pps a ≥ 20 reflectores | high |
| `beaconing` | `beaconing` | ≥ 10 conexiones al mismo destino en 24 h, ≥ 6 h, intervalo 30 s–1 h con CV < 0,2 y < 4 KiB de media | medium |
| `sustained_upload` | `open_proxy_abuse` | subida > 80 % y > 5 Mbit/s durante ≥ 30 min seguidos; exento si ≥ 80 % va a una nube conocida (AWS, Google, Microsoft, Apple, Dropbox, Backblaze, Wasabi) entre las 22 y las 7 h locales del ISP | medium |

Confianza en bandas (`low` < 0,45 ≤ `medium` < 0,75 ≤ `high`); con muestreo declarado (> 1) baja una
banda y se añade la razón `sampling_declared`. Severidad y confianza se muestran por separado.

**Tráfico `internal`** (cliente → cliente del mismo nodo): no entra en los detectores de I1 (solo
`direction = upload`). La captura real del PO mostró un escaneo vertical interno de 285 puertos: es
típico de equipos de gestión/monitorización del ISP. La "propagación interna" de §8 queda para un
detector propio con allowlist de hosts de gestión (I2). Lo verifica `TestRealCaptureInternalScanIsIgnored`.

### Ciclo de vida (I1-12)

- Deduplicación por (tenant, cliente, kind, objetivo principal): una nueva ocurrencia (última vez
  posterior) actualiza contador, última vez, razones y evidencia y emite `finding.updated`; la misma
  ventana evaluada otra vez no cambia nada.
- `acknowledge` (open → acknowledged), `resolve`, `mark-false-positive` (comentario obligatorio,
  silencio de `silence_days`, 30 por defecto) con `If-Match`; ambas alimentan `verdict_feedback`.
- Si un patrón resuelto vuelve se abre otro hallazgo con `previous_finding_id` ("reincidente"); un
  falso positivo no se reabre durante su silencio; sin ocurrencias en 7 días → `auto_expired`.
- Estado de seguridad del cliente (D5/D18, `detection.customer_security`): `infected` ("Infectado")
  con un hallazgo activo de severidad ≥ alta y confianza alta, o con señales de dos kinds; `suspected`
  con cualquier otro activo; `mitigated` 7 días tras resolverlos; si no, `clean`. Guarda razones y
  confianza y publica `horus.detection.customer.security_state_changed`.
- Eventos `horus.detection.finding.{opened,updated,resolved}` por outbox con el documento C8 sin datos
  personales (`customer: null`, `rendered_*: null`); la evidencia se audita (`security.evidence.read`)
  en proceso con auth o por `horus.detection.audit.recorded`.
- Cliente: `dim.customer` (proyectado por devices); si aún no está, `customer_id` = UUIDv5(tenant,
  "realm|ip"), el mismo que usa analytics.

### Variables de entorno

`HORUS_POSTGRES_DSN`, `HORUS_DETECTION_MIGRATE` (true), `HORUS_DETECTION_DB_APP_ROLE`/`_PLATFORM_ROLE`,
`HORUS_DETECTION_CURSOR_KEY`, `HORUS_JWT_PUBLIC_KEYS` (si auth no es local), `HORUS_CLICKHOUSE_DSN`,
`HORUS_DETECTION_CH_USER` (`horus_detection`), `HORUS_CLICKHOUSE_DETECTION_PASSWORD`,
`HORUS_DETECTION_CH_TIMEOUT` (30s), `HORUS_REPUTATION_SNAPSHOT_DIR`, `HORUS_DETECTION_ENGINE` (true),
`HORUS_DETECTION_INTERVAL` (1m), `HORUS_DETECTION_LAG` (2m), `HORUS_DETECTION_TENANTS` (lista fija; el
resto se registra en `detection.tenant_registry` al usar la API).

### Pruebas

```sh
go test ./services/detection/...                                              # unitarias
go test -tags=integration ./services/detection/itest/...                       # ciclo de vida (PostgreSQL)
HORUS_CH_NOFILE=16384 go test -tags=integration ./tests/detection/...          # escenarios del simulador + captura real
go test -tags=integration ./tests/tenancy/...                                  # aislamiento entre ISP
```

## Feeds de reputación (I0-17)

- `internal/config/feeds.yaml` (embebido; `-config` / `HORUS_FEEDS_CONFIG` lo sustituye): cada
  fuente declara URL, licencia, `commercial_use` (`yes` se descarga, `no` nunca, `unverified` solo
  con `--allow-unverified` hasta decisión del PO, P-14; las fuentes del catálogo están aprobadas
  para uso comercial por D20 salvo FireHOL), frecuencia, formato, categoría, confianza y TTL.
- `internal/adapters/feeds`: parsers de abuse.ch Feodo Tracker y ThreatFox, Spamhaus DROP (texto y
  NDJSON) y listas netset (Tor, FireHOL). Rechazan archivos vacíos, truncados o páginas de error.
- `internal/app/feedsync`: descarga → validación → versionado (`packages/go/datasets`) conservando
  la última versión válida; compila el snapshot desde las versiones vigentes.
- `api/reputation`: snapshot versionado (contenedor `HSNP`, zstd + SHA-256) y búsqueda por IP en
  memoria (fuente, categoría, confianza, fecha; < 1 µs de mediana) para el ingester.
- CLI `go run ./services/detection/cmd/horus-feeds <sources|fetch|build|sync|lookup|status>`;
  datasets en `$HORUS_DATA_DIR/datasets/<fuente>/`, snapshots en `$HORUS_DATA_DIR/catalog/reputation/v<N>/`.
  Sin Internet: `-config tests/fixtures/feeds/feeds.yaml -fixtures tests/fixtures/feeds`.
- Métricas (`status`, formato Prometheus): `horus_dataset_age_seconds`, `horus_dataset_entries`,
  `horus_dataset_consecutive_failures`, `horus_dataset_last_success_timestamp_seconds`.

## Listas de reputación personalizadas (D20)

El superadmin puede dar de alta listas propias que se descargan y entran en el snapshot junto a
las del catálogo. Contrato en `api/customfeeds`:

- `SourceSpec`: id, nombre, URL, formato (`csv` con `csv: {column, delimiter, comment, header}`),
  categoría, confianza, frecuencia, TTL, `max_bytes`, `min_entries`, `max_entries`,
  `on_dangerous` (`reject` por defecto | `warn`), licencia opcional y auditoría (`created_by`,
  `updated_at`). Serializa a YAML y JSON (duraciones como `"6h"`).
- `SourceProvider`: `File` (YAML con `version: 1` + `sources:` o una sola fuente; campos
  desconocidos = error), `Dir` (todos los `*.yaml`/`*.yml`), `ProviderFunc` (p. ej. consulta a
  PostgreSQL), `Static`, `Multi`. `Resolve` valida, convierte (`ToSource`: kind `reputation`,
  `origin: custom`, `commercial_use: yes` por autorización del superadmin) y combina con el
  catálogo; una lista inválida o con id repetido se informa y no se carga, sin bloquear el resto.
- `Validate(spec, DefaultPolicy())` devuelve `*ValidationError` con errores por campo (para la API
  de plataforma): id `custom-[a-z0-9][a-z0-9_-]{1,55}` (espacio de nombres separado del catálogo);
  URL solo `https`, sin credenciales ni fragmento, host público (no IP privada/reservada,
  `localhost`, nombres sin punto ni `.local`, `.internal`, `.lan`, `.home.arpa`, `.corp`,
  `.intranet`), ≤ 2048 caracteres; formato con parser; opciones `csv` válidas y solo con `csv`;
  categoría de `reputation.Category`; confianza 1–100; frecuencia 1 h–7 días; TTL 0 o entre la
  frecuencia y 90 días; `max_bytes` 1 KiB–256 MiB (32 MiB por defecto); `max_entries` hasta 2 M
  (200 000 por defecto); `min_entries` ≤ `max_entries`.
- Formatos genéricos (`internal/adapters/feeds`): `ip-list` (IP, CIDR, rango `a-b`, `ip:puerto`
  o `[ipv6]:puerto` por línea; comentarios `#`, `;`, `//`; BOM y CRLF tolerados) y `csv` (columna
  por nombre o posición, separador `, ; | \t`, comentario configurable). Los formatos del catálogo
  también se pueden usar.
- Protección ante listas peligrosas (`ListPolicy`, tras interpretar el archivo): la ruta por
  defecto y una cobertura IPv4 mayor que un /8 rechazan siempre la lista; los rangos privados o
  reservados (RFC 1918, CGNAT, loopback, enlace local, multicast, 240/4, ULA, NAT64…), los prefijos
  más amplios que /12 (IPv4) o /32 (IPv6) y los prefijos protegidos (`ParseWithPolicy`, p. ej. los
  del ISP) la rechazan con `on_dangerous: reject` o se descartan con aviso con `warn` (si son más
  del 5 % se rechaza igualmente). Una lista rechazada nunca reemplaza la versión vigente. Las
  fuentes del catálogo mantienen el criterio de I0-17 (mínimo /8 y /16; lo peligroso cuenta como
  inválido).
- Descarga (`datasets.EgressGuard` en `HTTPFetcher`, solo listas personalizadas): `https` con
  destino público y distinto de la propia instalación (direcciones de las interfaces locales),
  comprobado en la URL inicial resolviendo DNS (todas las IP), en cada redirección (máx. 5, nunca a
  `http`) y, en conexión directa, en la dirección real del socket (DNS rebinding). Detrás de un
  proxy HTTP(S) el socket lo abre el proxy y queda la comprobación por DNS.
- CLI: `horus-feeds <orden> -custom <archivo|directorio>` (o `HORUS_FEEDS_CUSTOM`); `sources`
  termina con 1 si alguna lista personalizada se rechaza. Ejemplos en `tests/fixtures/feeds/custom/`.

### Integración en el binario `horus` (pendiente)

`horus-feeds` y `horus-asn` siguen como binarios propios: `services/cmd/horus` solo acepta
`--roles`/`--version` (no tiene despachador de subcomandos, y añadirlo toca `app.go`, fuera del
registro de roles), y la sincronización periódica dentro del rol `detection` (o `traffic` para
ASN) necesita piezas que aún no existen: planificador por frecuencia de fuente, publicación del
snapshot en NATS Object Store y el `SourceProvider` sobre la tabla de PostgreSQL de la API de
plataforma. Cuando estén, el rol `detection` hará `customfeeds.Resolve` + `feedsync.Service`
en su `Run`, y las CLI pasarán a ser subcomandos (`horus feeds …`, `horus asn …`).

Evento pendiente: `horus.detection.reputation.source_refreshed` (contrato de INT) se publicará desde
el resultado de cada fuente (`datasets.Result`): `updated`/`unchanged` → `ok`; `failed` con
`errors.Is(err, feeds.ErrDangerous)` o `feeds.ErrTooLarge` → `rejected`; resto de `failed` → `failed`;
`origin` de `Source.Origin`, `consecutive_failures` del estado del almacén.
