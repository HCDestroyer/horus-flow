# `mod:traffic` — services/traffic

- **Propósito:** Catálogo de clasificación (prefijo→ASN→organización→servicio→categoría) y su snapshot.
- **Rol(es) de `horus`:** `traffic`
- **Agente dueño:** FLOW ([`docs/backlog/team.md`](../../docs/backlog/team.md) §2, [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · Roles y fronteras: [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)
- **Layout y reglas:** [`docs/conventions.md`](../../docs/conventions.md) §2.1 — `api/` (contrato público), `internal/` (`config`, `domain`, `app`, `adapters`), `migrations/` (si tiene esquema PostgreSQL). Otros módulos solo importan `services/traffic/api`.

Esqueleto creado en I0-01: todavía sin código. Este README se ampliará con variables de entorno,
métricas y eventos publicados/consumidos cuando el módulo tenga implementación.

## Dataset prefijo → ASN → organización (I0-17, aportado por SEC)

- `internal/config/datasets.yaml` (embebido; `-config` / `HORUS_ASN_DATASETS_CONFIG`): RIPE RIS
  (MRT), iptoasn.com (PDDL), estadísticas delegadas de los 5 RIR, PeeringDB y CAIDA AS2Org
  (declarada `no`), con licencia y uso comercial.
- `internal/adapters/asnsources`: parsers MRT TABLE_DUMP_V2 (origen por mayoría de pares),
  iptoasn TSV (rangos → CIDR), RIR delegated (cabecera y resúmenes verificados) y PeeringDB `/api/net`.
- `internal/app/asnbuild`: consolidación BGP > iptoasn > RIR, nombres y `network_type` de PeeringDB,
  diff contra el snapshot anterior (> 5 % ⇒ revisión, `-accept-large-diff`).
- `api/asn`: snapshot versionado y `Lookup(ip)` → prefijo, ASN, país, organización, tipo de red.
- CLI `go run ./services/traffic/cmd/horus-asn <sources|fetch|build|sync|lookup|status>`;
  snapshots en `$HORUS_DATA_DIR/catalog/asn/v<N>/`. Sin Internet:
  `-config tests/fixtures/datasets/datasets.yaml -fixtures tests/fixtures/datasets -allow-unverified`.
