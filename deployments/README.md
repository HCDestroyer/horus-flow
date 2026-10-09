# deployments/ — despliegue

- **Propósito:** compose de desarrollo y producción (`compose/`), perfiles mínimo y estándar,
  instalador de un servidor.
- **Dueño:** PLAT. Ruta sensible ([`docs/conventions.md`](../docs/conventions.md) §6.2).
- **Documentación:** [ADR-0011](../docs/adr/0011-docker-compose-antes-que-kubernetes.md),
  [ADR-0025](../docs/adr/0025-binario-modular-con-roles.md) §2, [`docs/conventions.md`](../docs/conventions.md) §9–10.

## Compose de desarrollo (I0-02)

`compose/compose.dev.yaml`: PostgreSQL 18.6, ClickHouse 26.8 (LTS), NATS 2.14 con JetStream,
Valkey 8.1 y Traefik 3.7. Sin MinIO ni Redis. Cada servicio tiene imagen de versión fija,
healthcheck, volumen nombrado y límite de memoria; los puertos se publican solo en `127.0.0.1`.

```bash
make up      # crea compose/.env y compose/secrets/ si faltan, levanta y espera a "healthy"
make down    # para los contenedores; conserva los volúmenes (datos)
make reset   # para y BORRA los volúmenes (PostgreSQL, ClickHouse, NATS, Valkey)
```

| Servicio | Host (por defecto) | Variable para cambiar el puerto | Credenciales |
| --- | --- | --- | --- |
| PostgreSQL | `127.0.0.1:5432`, BD/usuario `horus` | `HORUS_PG_PORT` | `compose/secrets/postgres_password.txt` |
| ClickHouse | HTTP `:8123`, nativo `:9000`, BD/usuario `horus` | `HORUS_CH_HTTP_PORT`, `HORUS_CH_NATIVE_PORT` | `compose/secrets/clickhouse_password.txt` |
| NATS | `:4222`, monitorización `:8222` | `HORUS_NATS_PORT`, `HORUS_NATS_MONITOR_PORT` | sin autenticación (solo desarrollo) |
| Valkey | `:6379`, usuario `default` | `HORUS_VALKEY_PORT` | `compose/secrets/valkey_password.txt` |
| Traefik | HTTP `:8000` (`/api`, `/ws` → `horus-app:8080`), ping/dashboard `:8082` | `HORUS_HTTP_PORT`, `HORUS_TRAEFIK_ADMIN_PORT` | — |

- **Variables:** `compose/.env` (copiado de [`compose/.env.example`](compose/.env.example), ignorado
  por Git). Las obligatorias (`HORUS_PG_DB`, `HORUS_PG_USER`, `HORUS_CH_DB`, `HORUS_CH_USER`) se
  declaran con `${VAR:?}`: si faltan, `make up` falla nombrándolas sin imprimir valores
  ([`scripts/compose-preflight.sh`](../scripts/compose-preflight.sh)).
- **Secretos:** [`scripts/dev-secrets.sh`](../scripts/dev-secrets.sh) genera valores aleatorios en
  `compose/secrets/` (ignorado por Git); llegan a los contenedores como archivos en `/run/secrets/`
  y al binario por las variables `*_FILE`. Para cambiarlos: `make reset` y borra `compose/secrets/`.
- **Contenedores propios** (`horus-app`, `horus-collector`, `horus-wg-agent`, misma imagen,
  [`Dockerfile`](../Dockerfile)): perfil `app`, **preparado pero inactivo** hasta que el binario de
  I0-04 implemente los roles y el subcomando `healthcheck`. Entonces: `make up COMPOSE_PROFILES=app`
  (construye `horus:dev`).
- **ClickHouse y `nofile`:** si `make up` falla con `error setting rlimit`, el host o el daemon de
  Docker tienen un límite de descriptores menor que 262144: baja `HORUS_CH_NOFILE` en `.env`.
- **CI:** el job `compose-smoke` ejecuta `make up && scripts/wait-healthy.sh && make down`.

## Producción en un servidor (I1-22) y backups locales (I1-23)

Requisitos: Debian 12/13 o Ubuntu 22.04/24.04 LTS, Docker Engine ≥ 24 con compose v2, ≥ 4 núcleos,
≥ 8 GiB de RAM, ≥ 100 GiB libres (mejor un **segundo disco** para `--store-dir`), puertos 80/443 TCP
y 51820 UDP libres, módulo WireGuard del kernel (o `wireguard-go`).

```bash
git clone https://github.com/hcdestroyer/horus-flow && cd horus-flow
sudo bash scripts/install.sh                     # pregunta: modo de acceso, rango de túneles, superadmin
# desatendido, solo IP:
sudo bash scripts/install.sh --yes --mode ip --admin-email noc@isp.net --admin-password-file /root/pw
# con dominio o subdominio (TLS automático de Let's Encrypt):
sudo bash scripts/install.sh --yes --mode domain --domain horus.isp.net --acme-email noc@isp.net \
     --admin-email noc@isp.net --admin-password-file /root/pw --store-dir /mnt/backups/horus
sudo /opt/horus/bin/install.sh --check           # puertos, salud de cada rol, disco, TLS, backups
sudo /opt/horus/bin/install.sh --uninstall       # conserva datos y secretos (--purge los borra)
```

| Qué | Dónde |
| --- | --- |
| Compose (copia de [`compose/compose.prod.yaml`](compose/compose.prod.yaml)), `.env` sin secretos, `config/`, `bin/` | `/opt/horus` |
| Secretos (0700, nunca se regeneran), TLS, respuestas del instalador (`install.conf`) | `/etc/horus/{secrets,tls}` |
| Datos de PostgreSQL, ClickHouse, NATS, Valkey, Traefik y métricas de backups | `/var/lib/horus/*` |
| Almacén y backups (`backups/postgres` pgBackRest, `backups/clickhouse`, `reports/`, `catalog/`…) | `--store-dir` (`/var/lib/horus/store`) |
| Paquete de secretos offline cifrado (guárdalo fuera y ejecuta `install.sh --confirm-bundle`) | `/root/horus-secrets-<host>-<fecha>.tar.gz.{enc,age}` |

- **Modos de acceso (D19):** `domain`/`subdomain` → router Traefik por `Host()`, reto HTTP-01 en
  :80, HSTS; `ip` (`ip_only`) → certificado autogenerado (EC P-256, SAN IP, 825 días) cuya huella
  SHA-256 muestran el instalador y `--check`; `wireguard` lo incluye en el script RouterOS.
- **Red:** solo Traefik (80/443) y el UDP de WireGuard del hub son públicos. `horus-wg-agent` va en
  la red del host con `NET_ADMIN` y gestiona `wg0` (creada por `bin/horus-tunnel` /
  `horus-tunnel.service`); el colector publica 4739/2055 UDP **solo en la IP del hub** y
  `DOCKER-USER`/`INPUT` descartan ese UDP si no entra por `wg0`. gRPC `wireguard` ↔ `wg-agent` con
  mTLS (CA interna generada por el instalador) por la puerta de enlace de la red del compose.
- **NATS:** usuario/contraseña; el servicio one-shot `nats-init` (`horus nats-provision`) aplica los
  streams del contrato C4 y el KV `flow_exporter_state` antes de los `horus-*`.
- **Backups (I1-23):** `horus-backup run all` a las 02:15 UTC (pgBackRest full el domingo / diff;
  WAL continuo con `archive_timeout=60`; ClickHouse `BACKUP` nativo full el domingo / incremental,
  sin `flows_raw`), `verify all` los domingos 06:00 (restauración en PostgreSQL y ClickHouse
  efímeros y vacíos, comparación tabla a tabla), `check-ttl` diario y métricas cada 5 min
  (`127.0.0.1:9109/metrics.prom`; reglas en `infrastructure/backup/prometheus-rules.yml`).
  Retención: 3 completos de PostgreSQL (≥ 14 días de PITR sin destino remoto) y 3 cadenas de
  ClickHouse. Alertas: log del sistema, métrica a 0 y `HORUS_BACKUP_ALERT_WEBHOOK` opcional.
  `make test-backup` lo prueba de punta a punta. El destino remoto (rclone/SFTP) llega en I3.
