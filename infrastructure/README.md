# infrastructure/ — configuración de infraestructura

- **Propósito:** configuración versionada de los componentes de infraestructura (PostgreSQL,
  ClickHouse, NATS, Valkey, Traefik, observabilidad).
- **Dueño:** PLAT. Las migraciones de ClickHouse (FLOW, I0-13) viven en
  `services/ingester/migrations/clickhouse/` porque el binario las embebe; esa ruta es
  sensible en producción ([`docs/conventions.md`](../docs/conventions.md) §6.2).
- **Documentación:** [`docs/architecture.md`](../docs/architecture.md),
  [`docs/database.md`](../docs/database.md), [`docs/observability.md`](../docs/observability.md).

| Ruta | Qué contiene | Historia |
| --- | --- | --- |
| `nats/nats.dev.conf` | NATS con JetStream para el compose de desarrollo (sin autenticación; monitorización en `:8222`) | I0-02 |
| `traefik/traefik.dev.yml`, `traefik/dynamic/` | Traefik de desarrollo: HTTP `:8000`, ping/dashboard `:8082`, proveedor de archivos (sin socket de Docker) | I0-02 |
| `observability/prometheus/`, `observability/loki/`, `observability/alloy/` | Prometheus (raspa `:8081/metrics` de `horus-*`, Traefik y la propia pila), Loki monolítico y Grafana Alloy (logs de los contenedores del proyecto compose con redacción de respaldo) del perfil `observability` | I0-18 |
| `observability/grafana/` | Datasources y dashboard "Horus · Platform Overview" (filas Servicios, API, Ingesta y Logs) provisionados como código | I0-18 |
| `nats/nats.prod.conf`, `nats/kv.yaml` | NATS de producción con usuario/contraseña (incluye `auth.conf` del instalador) y `max_file_store` dimensionado; buckets KV que provisiona `horus nats-provision` (`flow_exporter_state`) junto con los streams de `packages/events/streams/streams.yaml` | I1-22 |
| `traefik/prod/*.tmpl` | Rutas de producción: `horus.acme.yml.tmpl` (domain/subdomain, Let's Encrypt, HSTS) y `horus.ip.yml.tmpl` (ip_only, certificado autogenerado); cabeceras de seguridad y rate limit | I1-22 |
| `backup/` | Disco `backups` de ClickHouse, `httpd.conf` del servicio `backup-metrics` y reglas Prometheus de backups y disco | I1-23 |
| `lab/chr/` | Laboratorio MikroTik CHR: valores (`lab.env`), checksums fijados de la imagen, configuración base del router con NAT y documentación ([`lab/chr/README.md`](lab/chr/README.md)) | I0-11 |
