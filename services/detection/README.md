# `mod:detection` — services/detection

- **Propósito:** Reputación, correlación (detección de botnets), hallazgos y scoring.
- **Rol(es) de `horus`:** `detection`
- **Agente dueño:** SEC ([`docs/backlog/team.md`](../../docs/backlog/team.md) §2, [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · Roles y fronteras: [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)
- **Layout y reglas:** [`docs/conventions.md`](../../docs/conventions.md) §2.1 — `api/` (contrato público), `internal/` (`config`, `domain`, `app`, `adapters`), `migrations/` (si tiene esquema PostgreSQL). Otros módulos solo importan `services/detection/api`.

Esqueleto creado en I0-01: todavía sin código. Este README se ampliará con variables de entorno,
métricas y eventos publicados/consumidos cuando el módulo tenga implementación.

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
