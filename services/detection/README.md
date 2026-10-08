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
  con `--allow-unverified` hasta decisión del PO, P-14), frecuencia, formato, categoría, confianza y TTL.
- `internal/adapters/feeds`: parsers de abuse.ch Feodo Tracker y ThreatFox, Spamhaus DROP (texto y
  NDJSON) y listas netset (Tor, FireHOL). Rechazan archivos vacíos, truncados o páginas de error.
- `internal/app/feedsync`: descarga → validación → versionado (`packages/go/datasets`) conservando
  la última versión válida; compila el snapshot desde las versiones vigentes.
- `api/reputation`: snapshot versionado (contenedor `HSNP`, zstd + SHA-256) y búsqueda por IP en
  memoria (fuente, categoría, confianza, fecha; < 1 µs de mediana) para el ingester.
- CLI `go run ./services/detection/cmd/horus-feeds <sources|fetch|build|sync|lookup|status>`;
  datasets en `$HORUS_DATA_DIR/datasets/<fuente>/`, snapshots en `$HORUS_DATA_DIR/catalog/reputation/v<N>/`.
  Sin Internet: `-config tests/fixtures/feeds/feeds.yaml -fixtures tests/fixtures/feeds -allow-unverified`.
- Métricas (`status`, formato Prometheus): `horus_dataset_age_seconds`, `horus_dataset_entries`,
  `horus_dataset_consecutive_failures`, `horus_dataset_last_success_timestamp_seconds`.
