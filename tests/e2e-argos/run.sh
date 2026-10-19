#!/usr/bin/env bash
# Prueba de extremo a extremo Horus Flow ↔ ArgosISP con los DOS sistemas corriendo de verdad
# (contrato docs/integrations/argosisp.md). Ver tests/e2e-argos/README.md.
#
#   tests/e2e-argos/run.sh            # up + test + down (lo que hace `make e2e-argos`)
#   tests/e2e-argos/run.sh up         # levanta todo con bases nuevas (idempotente)
#   tests/e2e-argos/run.sh test       # corre la prueba contra lo levantado
#   tests/e2e-argos/run.sh down       # para procesos y borra contenedores
#   tests/e2e-argos/run.sh ctl stop|start horus|argos   # lo usa la prueba (degradación)
#
# Horus corre como binario nativo (sin imagen Docker); ArgosISP con `node ace` desde su checkout
# (ARGOS_DIR). Solo PostgreSQL, ClickHouse, NATS y Valkey van en contenedores de imágenes públicas.
# Los secretos son de laboratorio y se generan en cada `up` dentro de E2E_WORK (nunca en el repo).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ARGOS_DIR="${ARGOS_DIR:-$(cd "$ROOT/.." && pwd)/argos-isp}"
WORK="${E2E_WORK:-$ROOT/.e2e-argos}"
PREFIX="${E2E_PREFIX:-e2e-argos}"
BIND=127.0.0.1
PG_PORT="${E2E_PG_PORT:-55432}"
CH_HTTP_PORT="${E2E_CH_HTTP_PORT:-58123}"
CH_NATIVE_PORT="${E2E_CH_NATIVE_PORT:-59000}"
NATS_PORT="${E2E_NATS_PORT:-54222}"
VALKEY_PORT="${E2E_VALKEY_PORT:-56379}"
HORUS_PORT="${E2E_HORUS_PORT:-18080}"
HORUS_ADMIN_PORT="${E2E_HORUS_ADMIN_PORT:-18081}"
HORUS_GRPC_PORT="${E2E_HORUS_GRPC_PORT:-19090}"
ARGOS_PORT="${E2E_ARGOS_PORT:-13333}"
AAA_PORT="${E2E_AAA_PORT:-13100}"
PG_IMAGE="${E2E_PG_IMAGE:-postgres:18.6-alpine}"
CH_IMAGE="${E2E_CH_IMAGE:-clickhouse/clickhouse-server:26.8.20.9}"
NATS_IMAGE="${E2E_NATS_IMAGE:-nats:2.11-alpine}"
VALKEY_IMAGE="${E2E_VALKEY_IMAGE:-valkey/valkey:8.1.10-alpine}"
# Contraseñas de laboratorio (contenedores locales en 127.0.0.1; no son secretos).
PG_SUPER_PASS=e2e-local-pg
CH_PASS=e2e-local-ch

log() { printf '\033[1;34m[e2e-argos]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[e2e-argos]\033[0m %s\n' "$*" >&2; exit 1; }

wait_until() { # wait_until <segundos> <descripción> <comando...>
    local secs=$1 what=$2; shift 2
    for _ in $(seq 1 $((secs * 2))); do
        if "$@" >/dev/null 2>&1; then return 0; fi
        sleep 0.5
    done
    die "tiempo agotado esperando $what"
}

psql_super() { docker exec -i "$PREFIX-pg" psql -v ON_ERROR_STOP=1 -X -q -o /dev/null -U postgres "$@" 2>&1 | grep -v NOTICE || true; }

# ------------------------------------------------------------------------------- contenedores
containers_up() {
    command -v docker >/dev/null || die "falta docker"
    mkdir -p "$WORK"
    cat >"$WORK/nats.conf" <<'EOF'
port: 4222
http_port: 8222
max_payload: 8MB
jetstream {
  store_dir: /data/jetstream
  max_memory_store: 128MB
  max_file_store: 32GB
}
EOF
    run_once() { # run_once <nombre> <args de docker run...>
        local name=$1; shift
        if [ "$(docker inspect -f '{{.State.Running}}' "$name" 2>/dev/null)" = true ]; then return; fi
        docker rm -f "$name" >/dev/null 2>&1 || true
        docker run -d --name "$name" "$@" >/dev/null
    }
    run_once "$PREFIX-pg" -e POSTGRES_PASSWORD="$PG_SUPER_PASS" -p "$BIND:$PG_PORT:5432" --shm-size 256m "$PG_IMAGE"
    run_once "$PREFIX-valkey" -p "$BIND:$VALKEY_PORT:6379" "$VALKEY_IMAGE"
    run_once "$PREFIX-nats" -v "$WORK/nats.conf:/etc/nats/nats.conf:ro" -p "$BIND:$NATS_PORT:4222" "$NATS_IMAGE" -c /etc/nats/nats.conf
    local nofile; nofile="$(ulimit -Hn)"; [ "$nofile" = unlimited ] && nofile=262144
    [ "$nofile" -gt 262144 ] && nofile=262144
    run_once "$PREFIX-ch" --ulimit "nofile=$nofile:$nofile" -e CLICKHOUSE_DB=horus -e CLICKHOUSE_USER=horus \
        -e CLICKHOUSE_PASSWORD="$CH_PASS" -e CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1 \
        -p "$BIND:$CH_HTTP_PORT:8123" -p "$BIND:$CH_NATIVE_PORT:9000" "$CH_IMAGE"
    wait_until 60 "PostgreSQL" docker exec "$PREFIX-pg" pg_isready -q -U postgres
    wait_until 60 "ClickHouse" curl -sf "http://$BIND:$CH_HTTP_PORT/ping"
    wait_until 30 "Valkey" docker exec "$PREFIX-valkey" valkey-cli ping
}

fresh_databases() {
    log "bases nuevas (horus, argosisp)"
    psql_super <<'SQL'
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname IN ('horus', 'argosisp') AND pid <> pg_backend_pid();
DROP DATABASE IF EXISTS horus;
DROP DATABASE IF EXISTS argosisp;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'horus') THEN CREATE ROLE horus LOGIN SUPERUSER PASSWORD 'e2e-local-horus'; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'argosisp') THEN CREATE ROLE argosisp LOGIN SUPERUSER PASSWORD 'argosisp'; END IF;
END $$;
CREATE DATABASE horus OWNER horus;
CREATE DATABASE argosisp OWNER argosisp;
ALTER DATABASE argosisp SET timezone TO 'UTC';
SQL
    docker exec "$PREFIX-valkey" valkey-cli FLUSHALL >/dev/null
    rm -rf "$WORK/horus-data"
    curl -sf -u "horus:$CH_PASS" "http://$BIND:$CH_HTTP_PORT/" --data-binary "DROP DATABASE IF EXISTS flows SYNC" >/dev/null || true
    # JetStream limpio: los durables "deliver new" y los streams de la corrida anterior sobran.
    docker restart "$PREFIX-nats" >/dev/null
}

# ------------------------------------------------------------------------------- procesos
pidfile() { echo "$WORK/$1.pid"; }

start_proc() { # start_proc <nombre> <dir> <archivo env> <comando...>
    local name=$1 dir=$2 envfile=$3; shift 3
    stop_proc "$name"
    (cd "$dir" && set -a && . "$envfile" && set +a && exec setsid "$@" >"$WORK/$name.log" 2>&1 </dev/null) &
    echo $! >"$(pidfile "$name")"
}

stop_proc() {
    local f; f="$(pidfile "$1")"
    [ -f "$f" ] || return 0
    local pid; pid="$(cat "$f")"
    if kill -0 "$pid" 2>/dev/null; then
        kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
        for _ in $(seq 1 60); do kill -0 "$pid" 2>/dev/null || break; sleep 0.5; done
        kill -KILL -- "-$pid" 2>/dev/null || true
    fi
    rm -f "$f"
}

write_env() {
    mkdir -p "$WORK/horus-data"
    if [ ! -f "$WORK/horus-signing.pem" ]; then
        openssl genpkey -algorithm ed25519 -out "$WORK/horus-signing.pem" 2>/dev/null
        chmod 600 "$WORK/horus-signing.pem"
    fi
    [ -f "$WORK/keks" ] || { for m in AUTH DEVICES ALERTS DETECTION JOBS; do echo "HORUS_${m}_KEK=$(openssl rand -hex 32)"; done >"$WORK/keks"; chmod 600 "$WORK/keks"; }
    cat >"$WORK/horus.env" <<EOF
HORUS_ENV=dev
HORUS_LOG_LEVEL=info
HORUS_LOG_FORMAT=json
HORUS_ROLES=auth,devices,snmp,detection,alerts,analytics,reporting,gateway
HORUS_HTTP_ADDR=$BIND:$HORUS_PORT
HORUS_GRPC_ADDR=$BIND:$HORUS_GRPC_PORT
HORUS_ADMIN_ADDR=$BIND:$HORUS_ADMIN_PORT
HORUS_SHUTDOWN_DELAY=0s
HORUS_DATA_DIR=$WORK/horus-data
HORUS_POSTGRES_DSN=postgres://horus:e2e-local-horus@$BIND:$PG_PORT/horus?sslmode=disable
HORUS_CLICKHOUSE_DSN=clickhouse://horus:$CH_PASS@$BIND:$CH_NATIVE_PORT/horus
HORUS_CLICKHOUSE_INGESTER_PASSWORD=$CH_PASS-ingester
HORUS_CLICKHOUSE_ANALYTICS_PASSWORD=$CH_PASS-analytics
HORUS_CLICKHOUSE_DETECTION_PASSWORD=$CH_PASS-detection
HORUS_CLICKHOUSE_ALERTS_PASSWORD=$CH_PASS-alerts
HORUS_CLICKHOUSE_JOBS_PASSWORD=$CH_PASS-jobs
HORUS_CLICKHOUSE_SNMP_PASSWORD=$CH_PASS-snmp
HORUS_NATS_URL=nats://$BIND:$NATS_PORT
HORUS_NATS_ENSURE_STREAMS=true
HORUS_TLM_FLOWS_MAX_BYTES=1073741824
HORUS_VALKEY_URL=redis://$BIND:$VALKEY_PORT/1
HORUS_SEED_ADMIN_EMAIL=root@horus.test
HORUS_SEED_ADMIN_PASSWORD=semilla-inicial-123
HORUS_AUTH_ARGON2_MEMORY_KIB=1024
HORUS_AUTH_ARGON2_TIME=1
HORUS_AUTH_SIGNING_KEY_FILE=$WORK/horus-signing.pem
HORUS_PUBLIC_BASE_URL=http://$BIND:$HORUS_PORT
HORUS_ALLOWED_ORIGINS=http://$BIND:$HORUS_PORT
HORUS_INTEGRATIONS_ALLOW_PRIVATE=true
HORUS_ALERTS_WEBHOOK_ALLOW_PRIVATE=true
HORUS_INTEGRATIONS_SWEEP_EVERY=15s
EOF
    cat "$WORK/keks" >>"$WORK/horus.env"
    cat >"$WORK/argos.env" <<EOF
NODE_ENV=development
TZ=UTC
PORT=$ARGOS_PORT
HOST=$BIND
LOG_LEVEL=info
APP_KEY=e2e-clave-de-laboratorio-no-secreta-32
DB_HOST=$BIND
DB_PORT=$PG_PORT
DB_USER=argosisp
DB_PASSWORD=argosisp
DB_DATABASE=argosisp
REDIS_HOST=$BIND
REDIS_PORT=$VALKEY_PORT
REDIS_PASSWORD=
COLAS_PREFIJO=argose2e
EVENTOS_STREAM=argose2e:eventos
AAA_REDIS_PREFIJO=argose2e:aaa:user:
INTERNAL_TOKEN=token-interno-de-desarrollo
AAA_INTERNAL_TOKEN=token-interno-de-desarrollo
AAA_PPP_KEY=Y2xhdmUtZGUtZGVzYXJyb2xsby1hYWEtcHBwLTMyYnk=
API_PUBLIC_URL=http://$BIND:$ARGOS_PORT
API_INTERNAL_URL=http://$BIND:$ARGOS_PORT/api/v1/
PANEL_URL_PUBLICA=http://$BIND:13000
CORS_ORIGINS=http://$BIND:13000
HORUS_PERMITIR_HTTP=true
SMTP_HOST=$BIND
SMTP_PORT=1025
MAIL_FROM="ArgosIsp <no-responder@argosisp.local>"
PANEL_COOKIE_SEGURA=false
SEED_ADMIN_EMAIL=admin@argosisp.local
SEED_ADMIN_PASSWORD=admin12345
SEED_ADMIN_NOMBRE=Administrador
LOGIN_LIMITE_IP=0
LOGIN_LIMITE_USUARIO=0
AAA_SALUD_NODOS=
SESIONES_COLGADAS_CADA_MS=0
EOF
}

start_horus() {
    port_free "$HORUS_PORT" || die "el puerto $HORUS_PORT ya está en uso (¿otro Horus?)"
    start_proc horus "$ROOT" "$WORK/horus.env" "$WORK/bin/horus"
    wait_until 90 "Horus /readyz" curl -sf "http://$BIND:$HORUS_ADMIN_PORT/readyz"
}

port_free() { ! curl -s -o /dev/null --max-time 1 "http://$BIND:$1/"; }

start_argos_api() {
    port_free "$ARGOS_PORT" || die "el puerto $ARGOS_PORT ya está en uso (¿otra API de ArgosISP?)"
    start_proc argos-api "$ARGOS_DIR/apps/api" "$WORK/argos.env" node ace serve
    wait_until 120 "ArgosISP API" bash -c "curl -s -o /dev/null -w '%{http_code}' http://$BIND:$ARGOS_PORT/api/v1/auth/login | grep -q '^[1-5]'"
}

start_argos_workers() {
    start_proc argos-relay "$ARGOS_DIR/apps/api" "$WORK/argos.env" node ace eventos:relay
    start_proc argos-horus "$ARGOS_DIR/apps/api" "$WORK/argos.env" node ace horus:worker
    start_proc argos-sesiones "$ARGOS_DIR/apps/api" "$WORK/argos.env" node ace sesiones:worker
    start_proc argos-aaa "$ARGOS_DIR/services/aaa" "$WORK/argos.env" env PORT="$AAA_PORT" HOST="$BIND" node src/index.ts
    wait_until 60 "aaa" curl -sf "http://$BIND:$AAA_PORT/health"
}

up() {
    [ -d "$ARGOS_DIR/apps/api" ] || die "ARGOS_DIR=$ARGOS_DIR no es un checkout de ArgosISP"
    stop_all
    containers_up
    fresh_databases
    write_env
    log "compilando horus y ch-migrate"
    (cd "$ROOT" && go build -o "$WORK/bin/horus" ./services/cmd/horus && go build -o "$WORK/bin/ch-migrate" ./services/ingester/cmd/ch-migrate)
    log "esquema ClickHouse"
    (set -a && . "$WORK/horus.env" && set +a && "$WORK/bin/ch-migrate" >"$WORK/ch-migrate.log" 2>&1) || { tail -20 "$WORK/ch-migrate.log"; die "ch-migrate falló"; }
    log "Horus"
    start_horus
    log "ArgosISP: dependencias, migraciones, seed"
    [ -d "$ARGOS_DIR/node_modules" ] || (cd "$ARGOS_DIR" && pnpm install --frozen-lockfile >"$WORK/pnpm.log" 2>&1)
    (cd "$ARGOS_DIR/apps/api" && set -a && . "$WORK/argos.env" && set +a &&
        node ace migration:run --force >"$WORK/argos-migrate.log" 2>&1 && node ace db:seed >>"$WORK/argos-migrate.log" 2>&1) ||
        { tail -30 "$WORK/argos-migrate.log"; die "migraciones de ArgosISP fallaron"; }
    log "ArgosISP: API, relay, horus:worker, sesiones:worker y aaa"
    start_argos_api
    start_argos_workers
    log "ArgosISP: datos de demostración por la API (pnpm demo:datos)"
    (cd "$ARGOS_DIR" && API_URL="http://$BIND:$ARGOS_PORT" node scripts/demo/datos.mjs >"$WORK/demo.log" 2>&1) || { cat "$WORK/demo.log"; die "demo:datos falló"; }
    log "listo"
}

stop_all() {
    for p in argos-aaa argos-sesiones argos-horus argos-relay argos-api horus; do stop_proc "$p"; done
}

down() {
    stop_all
    if [ "${E2E_KEEP:-0}" != 1 ]; then
        docker rm -f "$PREFIX-pg" "$PREFIX-ch" "$PREFIX-nats" "$PREFIX-valkey" >/dev/null 2>&1 || true
        rm -rf "$WORK/horus-data" "$WORK/bin"
    fi
}

run_test() {
    log "prueba (go test -tags e2e)"
    (cd "$ROOT" &&
        E2E_HORUS_URL="http://$BIND:$HORUS_PORT" E2E_ARGOS_URL="http://$BIND:$ARGOS_PORT" \
        E2E_HORUS_PG="postgres://horus:e2e-local-horus@$BIND:$PG_PORT/horus?sslmode=disable" \
        E2E_ARGOS_PG="postgres://argosisp:argosisp@$BIND:$PG_PORT/argosisp?sslmode=disable" \
        E2E_CH_URL="http://$BIND:$CH_HTTP_PORT" E2E_CH_PASSWORD="$CH_PASS" E2E_NATS_URL="nats://$BIND:$NATS_PORT" \
        E2E_AAA_URL="http://$BIND:$AAA_PORT" E2E_CTL="$ROOT/tests/e2e-argos/run.sh" E2E_SUMMARY_FILE="$WORK/summary.txt" \
        ARGOS_DIR="$ARGOS_DIR" E2E_WORK="$WORK" E2E_PREFIX="$PREFIX" \
        go test -tags e2e -count=1 -timeout 30m -v ${E2E_RUN:+-run "$E2E_RUN"} ./tests/e2e-argos/) | tee "$WORK/test.log" | grep -vE '^=== (PAUSE|CONT)' || true
    grep -q '^ok ' "$WORK/test.log"
}

ctl() {
    case "$1:$2" in
        stop:horus) stop_proc horus ;;
        start:horus) start_horus ;;
        stop:argos) stop_proc argos-api ;;
        start:argos) start_argos_api ;;
        *) die "ctl stop|start horus|argos" ;;
    esac
}

case "${1:-all}" in
    up) up ;;
    test) run_test ;;
    down) down ;;
    ctl) shift; ctl "$@" ;;
    all)
        up
        rc=0; run_test || rc=$?
        down
        exit $rc ;;
    *) die "uso: run.sh [all|up|test|down|ctl stop|start horus|argos]" ;;
esac
