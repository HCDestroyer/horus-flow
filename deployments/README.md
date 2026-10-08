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
