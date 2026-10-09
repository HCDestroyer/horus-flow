#!/usr/bin/env bash
# horus-backup — backups locales verificados de una instalación de Horus Flow (historia I1-23).
#
# Lo instala scripts/install.sh en <instalación>/bin/horus-backup y lo programan los timers de
# systemd horus-backup-*.timer (docs/disaster-recovery.md §3.7). Todo es LOCAL (D2, ADR-0019):
# el destino remoto (rclone, SFTP…) llega en I3 y nunca bloquea estos pasos.
#
#   horus-backup run [all|pg|ch]        backup "auto": full el domingo o si no hay ninguno; si no,
#                                       diferencial (PostgreSQL) / incremental (ClickHouse)
#   horus-backup pg [auto|full|diff|incr]   pgBackRest a repo1 posix (HORUS_STORE_DIR/backups/postgres)
#   horus-backup ch [auto|full|incr]    BACKUP nativo de ClickHouse al disco `backups`
#                                       (HORUS_STORE_DIR/backups/clickhouse; sin flows_raw)
#   horus-backup verify [all|pg|ch]     restauración de prueba en una base VACÍA y efímera y
#                                       comparación con el origen (huella por tabla)
#   horus-backup pg-init                stanza-create + check (idempotente; lo llama el instalador)
#   horus-backup check-ttl              TTL de flows.flows_raw = HORUS_RAW_TTL_DAYS (7 d, storage.md §5)
#   horus-backup metrics                ocupación de discos y reescritura de metrics.prom
#   horus-backup status                 resumen legible (últimos backups, restauraciones, disco)
#   horus-backup ch-sql < consulta.sql  consulta ClickHouse como administrador (diagnóstico)
#
# Métricas (texto Prometheus en HORUS_DATA_ROOT/metrics/metrics.prom, servidas en
# 127.0.0.1:9109/metrics.prom por el servicio backup-metrics):
#   horus_backup_last_success_timestamp_seconds{component}       postgres | clickhouse
#   horus_backup_last_attempt_timestamp_seconds{component}
#   horus_backup_last_status{component}                          1 = OK, 0 = fallo
#   horus_backup_last_duration_seconds{component}
#   horus_backup_size_bytes{component}                           tamaño del repositorio local
#   horus_backup_failures_total{component}
#   horus_backup_restore_test_last_success_timestamp_seconds{component}
#   horus_backup_restore_test_last_status{component}
#   horus_backup_raw_ttl_ok                                       1 si el TTL del crudo coincide
#   horus_store_disk_used_ratio{volume}  /  horus_store_disk_free_bytes{volume}   store | data
#
# Alertas: cada fallo escribe en el log del sistema (logger -t horus-backup), deja la métrica a
# 0 y, si HORUS_BACKUP_ALERT_WEBHOOK está definida, hace POST {"text": "..."} (Slack, Mattermost,
# Telegram vía puente…). El disco por encima de HORUS_STORE_ALERT_RATIO (0.85) también avisa.
#
# Configuración: <instalación>/.env (lo genera el instalador; sin secretos) o HORUS_ENV_FILE.
# Sale con 0 si todo fue bien y 1 si algún paso falló.

# Las funciones de backup se llaman de forma indirecta (run_step); las comillas simples de
# ch_sql se expanden dentro del contenedor.
# shellcheck disable=SC2329,SC2016
set -euo pipefail
umask 027

script_dir="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
env_file="${HORUS_ENV_FILE:-$script_dir/../.env}"
[ -f "$env_file" ] || { echo "horus-backup: no encuentro $env_file (HORUS_ENV_FILE)" >&2; exit 2; }
env_file="$(readlink -f "$env_file")"
install_dir="$(dirname "$env_file")"
set -a
# shellcheck disable=SC1090
. "$env_file"
set +a

: "${HORUS_STORE_DIR:?falta HORUS_STORE_DIR en $env_file}"
: "${HORUS_DATA_ROOT:?falta HORUS_DATA_ROOT en $env_file}"
: "${HORUS_SECRETS_DIR:?falta HORUS_SECRETS_DIR en $env_file}"
PG_USER="${HORUS_PG_USER:-horus}"
PG_DB="${HORUS_PG_DB:-horus}"
CH_KEEP_FULL="${HORUS_BACKUP_CH_KEEP_FULL:-3}"
RAW_TTL_DAYS="${HORUS_RAW_TTL_DAYS:-7}"
ALERT_RATIO="${HORUS_STORE_ALERT_RATIO:-0.85}"
CH_IMAGE="${HORUS_CLICKHOUSE_IMAGE:-clickhouse/clickhouse-server:26.8.20.9@sha256:9b61e3c635c04ad5bb521eb4f6e61ce7585b5580814e51c25bb9e8292ce43364}"
PG_IMAGE="${HORUS_POSTGRES_IMAGE:-horus-postgres:18.6-pgbackrest2.59.3}"
CH_BACKUP_DIR="$HORUS_STORE_DIR/backups/clickhouse"
PG_REPO_DIR="$HORUS_STORE_DIR/backups/postgres"
metrics_dir="$HORUS_DATA_ROOT/metrics"
state_dir="$HORUS_STORE_DIR/backups/state"
compose=(docker compose --project-directory "$install_dir" -f "$install_dir/compose.yaml" --env-file "$env_file")

mkdir -p "$metrics_dir" "$state_dir" "$CH_BACKUP_DIR/meta"
chmod 0755 "$metrics_dir"

log() { printf '%s horus-backup: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
die() { log "ERROR: $*" >&2; exit 1; }

# --- Estado y métricas ---------------------------------------------------------------------------
state_set() { printf '%s\n' "$3" >"$state_dir/$1.$2"; }
state_get() { cat "$state_dir/$1.$2" 2>/dev/null || printf '%s' "${3:-}"; }

render_metrics() {
  local tmp="$metrics_dir/.metrics.prom.tmp" c
  {
    echo "# HELP horus_backup_last_success_timestamp_seconds Último backup local correcto (Unix)."
    echo "# TYPE horus_backup_last_success_timestamp_seconds gauge"
    for c in postgres clickhouse; do
      echo "horus_backup_last_success_timestamp_seconds{component=\"$c\"} $(state_get "$c" last_success 0)"
    done
    echo "# HELP horus_backup_last_attempt_timestamp_seconds Último intento de backup (Unix)."
    echo "# TYPE horus_backup_last_attempt_timestamp_seconds gauge"
    for c in postgres clickhouse; do
      echo "horus_backup_last_attempt_timestamp_seconds{component=\"$c\"} $(state_get "$c" last_attempt 0)"
    done
    echo "# HELP horus_backup_last_status Resultado del último backup (1 OK, 0 fallo)."
    echo "# TYPE horus_backup_last_status gauge"
    for c in postgres clickhouse; do
      echo "horus_backup_last_status{component=\"$c\"} $(state_get "$c" last_status 0)"
    done
    echo "# HELP horus_backup_last_duration_seconds Duración del último backup."
    echo "# TYPE horus_backup_last_duration_seconds gauge"
    for c in postgres clickhouse; do
      echo "horus_backup_last_duration_seconds{component=\"$c\"} $(state_get "$c" last_duration 0)"
    done
    echo "# HELP horus_backup_size_bytes Tamaño del repositorio local de backups."
    echo "# TYPE horus_backup_size_bytes gauge"
    for c in postgres clickhouse; do
      echo "horus_backup_size_bytes{component=\"$c\"} $(state_get "$c" size 0)"
    done
    echo "# HELP horus_backup_failures_total Backups fallidos desde la instalación."
    echo "# TYPE horus_backup_failures_total counter"
    for c in postgres clickhouse; do
      echo "horus_backup_failures_total{component=\"$c\"} $(state_get "$c" failures 0)"
    done
    echo "# HELP horus_backup_restore_test_last_success_timestamp_seconds Última restauración de prueba correcta (Unix)."
    echo "# TYPE horus_backup_restore_test_last_success_timestamp_seconds gauge"
    for c in postgres clickhouse; do
      echo "horus_backup_restore_test_last_success_timestamp_seconds{component=\"$c\"} $(state_get "$c" restore_success 0)"
    done
    echo "# HELP horus_backup_restore_test_last_status Resultado de la última restauración de prueba (1 OK, 0 fallo)."
    echo "# TYPE horus_backup_restore_test_last_status gauge"
    for c in postgres clickhouse; do
      echo "horus_backup_restore_test_last_status{component=\"$c\"} $(state_get "$c" restore_status 0)"
    done
    echo "# HELP horus_backup_raw_ttl_ok 1 si el TTL de flows.flows_raw coincide con storage.md."
    echo "# TYPE horus_backup_raw_ttl_ok gauge"
    echo "horus_backup_raw_ttl_ok $(state_get clickhouse raw_ttl_ok 0)"
    echo "# HELP horus_store_disk_used_ratio Ocupación del disco (0-1)."
    echo "# TYPE horus_store_disk_used_ratio gauge"
    echo "horus_store_disk_used_ratio{volume=\"store\"} $(state_get disk store_ratio 0)"
    echo "horus_store_disk_used_ratio{volume=\"data\"} $(state_get disk data_ratio 0)"
    echo "# HELP horus_store_disk_free_bytes Espacio libre del disco."
    echo "# TYPE horus_store_disk_free_bytes gauge"
    echo "horus_store_disk_free_bytes{volume=\"store\"} $(state_get disk store_free 0)"
    echo "horus_store_disk_free_bytes{volume=\"data\"} $(state_get disk data_free 0)"
  } >"$tmp"
  chmod 0644 "$tmp"
  mv -f "$tmp" "$metrics_dir/metrics.prom"
}

alert() {
  local msg host
  host="$(hostname)"
  msg="Horus ($host): $*"
  log "ALERTA: $*" >&2
  command -v logger >/dev/null 2>&1 && logger -t horus-backup -p user.err -- "$msg" || true
  printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >>"$state_dir/alerts.log"
  if [ -n "${HORUS_BACKUP_ALERT_WEBHOOK:-}" ]; then
    local body
    body="$(printf '%s' "$msg" | sed 's/\\/\\\\/g; s/"/\\"/g')"
    curl -fsS -m 10 -H 'Content-Type: application/json' -d "{\"text\":\"$body\"}" \
      "$HORUS_BACKUP_ALERT_WEBHOOK" >/dev/null || log "no se pudo enviar la alerta al webhook" >&2
  fi
}

# run_step <componente> <tipo backup|restore> <función> [args…]: ejecuta con set -e en un
# subshell, mide, actualiza el estado y alerta si falla (rc_total=1). Se llama siempre como
# orden simple: dentro de `|| …` o `if` bash desactiva set -e en todo el subshell.
run_step() {
  local comp="$1" kind="$2" fn="$3" start rc
  shift 3
  start="$(date +%s)"
  [ "$kind" = backup ] && state_set "$comp" last_attempt "$start"
  set +e
  ( set -e; "$fn" "$@" )
  rc=$?
  set -e
  local now; now="$(date +%s)"
  if [ "$kind" = backup ]; then
    state_set "$comp" last_duration "$((now - start))"
    if [ "$rc" = 0 ]; then
      state_set "$comp" last_status 1
      state_set "$comp" last_success "$now"
    else
      state_set "$comp" last_status 0
      state_set "$comp" failures "$(( $(state_get "$comp" failures 0) + 1 ))"
      alert "backup de $comp FALLIDO (código $rc)"
    fi
  else
    if [ "$rc" = 0 ]; then
      state_set "$comp" restore_status 1
      state_set "$comp" restore_success "$now"
    else
      state_set "$comp" restore_status 0
      alert "restauración de prueba de $comp FALLIDA (código $rc)"
    fi
  fi
  render_metrics
  [ "$rc" = 0 ] || rc_total=1
}

# run_plain <función>: como run_step pero sin estado ni métricas.
run_plain() {
  local rc
  set +e
  ( set -e; "$@" )
  rc=$?
  set -e
  [ "$rc" = 0 ] || rc_total=1
}

# --- PostgreSQL ----------------------------------------------------------------------------------
pg_exec() { "${compose[@]}" exec -T -u postgres postgres "$@"; }
pgbr() { pg_exec pgbackrest --config=/run/secrets/pgbackrest_conf --stanza=horus "$@"; }
psql_live() { pg_exec psql -X -v ON_ERROR_STOP=1 -U "$PG_USER" -d "$PG_DB" -Atq "$@"; }

# Huella de todas las tablas de usuario (hojas, también particiones): esquema.tabla, filas y
# md5 del contenido ordenado. Mismo SQL en el origen y en la base restaurada.
PG_FINGERPRINT_SQL="SELECT n.nspname || '.' || c.relname || ' ' ||
  (xpath('/row/c/text()', query_to_xml(format('SELECT count(*) AS c FROM %I.%I', n.nspname, c.relname), false, true, '')))[1]::text || ' ' ||
  coalesce((xpath('/row/m/text()', query_to_xml(format('SELECT md5(string_agg(t::text, %L ORDER BY t::text)) AS m FROM %I.%I t', '|', n.nspname, c.relname), false, true, '')))[1]::text, '-')
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind = 'r' AND n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg_toast%'
ORDER BY 1"

pg_init() {
  for _ in $(seq 1 60); do
    pg_exec pg_isready -q -U "$PG_USER" -d "$PG_DB" && break
    sleep 2
  done
  pgbr stanza-create
  pgbr check
}

pg_backup() {
  local type="${1:-auto}"
  if [ "$type" = auto ]; then
    type="diff"
    if [ "$(date -u +%u)" = 7 ] || ! pgbr info 2>/dev/null | grep -q 'full backup:'; then
      type=full
    fi
  fi
  log "PostgreSQL: pgbackrest backup --type=$type"
  pgbr --type="$type" backup
  pgbr info | sed 's/^/  /'
  state_set postgres size "$(du -sb "$PG_REPO_DIR" | cut -f1)"
}

pg_verify() {
  local name tmp seg archived=""
  name="horus_verify_$(date -u +%Y%m%dT%H%M%SZ)"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  log "PostgreSQL: huella del origen y punto de restauración $name"
  psql_live -c "BEGIN ISOLATION LEVEL REPEATABLE READ" -c "$PG_FINGERPRINT_SQL" \
    -c "SELECT pg_create_restore_point('$name')" -c "COMMIT" | sed -E '/^[0-9A-F]+\/[0-9A-F]+$/d' >"$tmp/source.txt"
  seg="$(psql_live -c "SELECT pg_walfile_name(pg_switch_wal())")"
  for _ in $(seq 1 120); do
    archived="$(psql_live -c "SELECT coalesce(last_archived_wal, '') FROM pg_stat_archiver")"
    [ -n "$archived" ] && [[ ! "$archived" < "$seg" ]] && break
    sleep 1
  done
  [[ ! "$archived" < "$seg" ]] || die "el WAL $seg no llegó al repositorio en 120 s (archive_command)"
  log "PostgreSQL: restauración en un contenedor efímero (base vacía) hasta $name"
  docker run --rm -i --name "horus-verify-pg-$$" --network none \
    -v "$PG_REPO_DIR:/var/lib/pgbackrest:ro" \
    -v "$HORUS_SECRETS_DIR/pgbackrest.conf:/etc/pgbackrest/pgbackrest.conf:ro" \
    -v /var/lib/pgrestore \
    -e "TARGET=$name" -e "PGUSER=$PG_USER" -e "PGDATABASE=$PG_DB" \
    --entrypoint /bin/sh "$PG_IMAGE" -s >"$tmp/restored.txt" <<EOF
set -e
install -d -o postgres -g postgres -m 0700 /var/lib/pgrestore/data
gosu postgres pgbackrest --config=/etc/pgbackrest/pgbackrest.conf --stanza=horus \
  --pg1-path=/var/lib/pgrestore/data --type=name --target="\$TARGET" --target-action=promote \
  --log-level-console=warn restore >&2
gosu postgres pg_ctl -D /var/lib/pgrestore/data -l /tmp/pg.log -w -t 600 \
  -o "-c archive_mode=off -c listen_addresses='' -c unix_socket_directories=/tmp -c port=5432" start >&2 \
  || { cat /tmp/pg.log >&2; exit 1; }
i=0
until [ "\$(gosu postgres psql -h /tmp -XAtq -c 'SELECT pg_is_in_recovery()')" = f ]; do
  i=\$((i+1)); [ \$i -lt 300 ] || { cat /tmp/pg.log >&2; exit 1; }; sleep 1
done
gosu postgres psql -h /tmp -X -v ON_ERROR_STOP=1 -Atq -c "$PG_FINGERPRINT_SQL"
gosu postgres pg_ctl -D /var/lib/pgrestore/data -m fast stop >&2
EOF
  local tables
  tables="$(wc -l <"$tmp/source.txt")"
  [ "$tables" -gt 0 ] || die "la huella del origen está vacía"
  if ! diff -u "$tmp/source.txt" "$tmp/restored.txt" >"$tmp/diff.txt"; then
    sed 's/^/  /' "$tmp/diff.txt" | head -40 >&2
    die "la base restaurada NO coincide con el origen en $name"
  fi
  log "PostgreSQL: restauración OK — $tables tablas idénticas (filas y md5) en $name"
  awk '{ printf "  %-45s %s filas\n", $1, $2 }' "$tmp/restored.txt" | grep -E 'auth\.tenant |devices\.(site|router|customer) |wireguard\.|detection\.(finding|.*finding)' || true
}

# --- ClickHouse ----------------------------------------------------------------------------------
ch_sql() {
  "${compose[@]}" exec -T clickhouse sh -c \
    'exec clickhouse-client --user "$CLICKHOUSE_USER" --password "$(cat /run/secrets/clickhouse_password)" --multiquery --format TSV'
}

# Tablas con datos propios que entran en el backup: flows (salvo flows_raw) y dim.
CH_TABLES_SQL="SELECT database, name FROM system.tables
  WHERE database IN ('flows', 'dim') AND engine LIKE '%MergeTree%' AND NOT (database = 'flows' AND name = 'flows_raw')
  AND name NOT LIKE '.inner%' ORDER BY database, name"

ch_counts() {
  local q="SELECT 'x' WHERE 0;"
  while IFS=$'\t' read -r db t; do
    [ -n "$db" ] || continue
    q+=" SELECT '$db.$t', count() FROM \`$db\`.\`$t\`;"
  done < <(printf '%s\n' "$CH_TABLES_SQL" | ch_sql)
  printf '%s\n' "$q" | ch_sql
}

ch_latest() { find "$CH_BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -name "${1}_*" -printf '%f\n' | sort | tail -1; }

ch_backup() {
  local type="${1:-auto}" ts name base="" settings="" have_dim
  have_dim="$(printf "SELECT count() FROM system.databases WHERE name IN ('flows','dim')\n" | ch_sql)"
  [ "$have_dim" = 2 ] || die "ClickHouse sin las bases flows/dim (¿migraciones del rol ingester?)"
  if [ "$type" = auto ]; then
    type="incr"
    if [ "$(date -u +%u)" = 7 ] || [ -z "$(ch_latest full)" ]; then type="full"; fi
  fi
  ts="$(date -u +%Y%m%dT%H%M%SZ)"
  name="${type}_$ts"
  if [ "$type" = incr ]; then
    base="$(ch_latest full)"
    local last_incr; last_incr="$(ch_latest incr)"
    # Encadenar sobre el último incremental de la misma cadena (base más reciente).
    if [ -n "$last_incr" ] && [ "$(cat "$CH_BACKUP_DIR/meta/$last_incr.base" 2>/dev/null)" = "$base" ] \
      && [[ "$last_incr" > "incr_${base#full_}" ]]; then
      base="$last_incr"
    fi
    [ -n "$base" ] || die "no hay backup completo de ClickHouse para el incremental"
    settings="SETTINGS base_backup = Disk('backups', '$base')"
  fi
  log "ClickHouse: BACKUP $name ${base:+(base $base)} — flows (sin flows_raw) y dim"
  ch_counts | sort >"$CH_BACKUP_DIR/meta/$name.before"
  printf "BACKUP DATABASE dim, DATABASE flows EXCEPT TABLES flows.flows_raw TO Disk('backups', '%s') %s\n" "$name" "$settings" | ch_sql >/dev/null
  ch_counts | sort >"$CH_BACKUP_DIR/meta/$name.after"
  if [ "$type" = incr ]; then
    local full="$base"
    [[ "$base" == incr_* ]] && full="$(cat "$CH_BACKUP_DIR/meta/$base.base")"
    printf '%s\n' "$full" >"$CH_BACKUP_DIR/meta/$name.base"
  fi
  [ -f "$CH_BACKUP_DIR/$name/.backup" ] || die "BACKUP terminó sin $name/.backup"
  ch_retention
  state_set clickhouse size "$(du -sb "$CH_BACKUP_DIR" | cut -f1)"
  log "ClickHouse: $name OK ($(du -sh "$CH_BACKUP_DIR/$name" | cut -f1))"
}

# Conserva los CH_KEEP_FULL completos más recientes y sus incrementales; borra el resto.
ch_retention() {
  local keep f b
  keep="$(find "$CH_BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -name 'full_*' -printf '%f\n' | sort | tail -n "$CH_KEEP_FULL")"
  while read -r f; do
    [ -n "$f" ] || continue
    grep -qx "$f" <<<"$keep" && continue
    log "ClickHouse: retención — borro $f y sus incrementales"
    while read -r b; do
      if [ -n "$b" ] && [ "$(cat "$CH_BACKUP_DIR/meta/$b.base" 2>/dev/null)" = "$f" ]; then
        rm -rf "${CH_BACKUP_DIR:?}/$b" "$CH_BACKUP_DIR/meta/$b".*
      fi
    done < <(find "$CH_BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -name 'incr_*' -printf '%f\n')
    rm -rf "${CH_BACKUP_DIR:?}/$f" "$CH_BACKUP_DIR/meta/$f".*
  done < <(find "$CH_BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -name 'full_*' -printf '%f\n' | sort)
}

ch_verify() {
  local name cname tmp
  name="$( (ch_latest full; ch_latest incr) | sort -t_ -k2 | tail -1)"
  [ -n "$name" ] || die "no hay backups de ClickHouse que verificar"
  cname="horus-verify-ch-$$"
  tmp="$(mktemp -d)"
  trap 'docker rm -f "$cname" >/dev/null 2>&1 || true; rm -rf "$tmp"' RETURN
  log "ClickHouse: restauración de $name en un ClickHouse efímero y vacío"
  docker run -d --name "$cname" --network none --memory 2g \
    --ulimit "nofile=${HORUS_CH_NOFILE:-262144}:${HORUS_CH_NOFILE:-262144}" \
    -v "$CH_BACKUP_DIR:/var/lib/horus/store/backups/clickhouse" \
    -v "${HORUS_CONFIG_DIR:-$install_dir/config}/backup/clickhouse-backups.xml:/etc/clickhouse-server/config.d/horus-backups.xml:ro" \
    -v /var/lib/clickhouse "$CH_IMAGE" >/dev/null
  for _ in $(seq 1 120); do
    docker exec "$cname" clickhouse-client -q 'SELECT 1' >/dev/null 2>&1 && break
    sleep 1
  done
  docker exec "$cname" clickhouse-client -q \
    "RESTORE DATABASE flows, DATABASE dim FROM Disk('backups', '$name')" >/dev/null
  local q="SELECT 'x' WHERE 0;"
  while read -r tbl _; do
    [ -n "$tbl" ] && q+=" SELECT '$tbl', count() FROM $tbl;"
  done <"$CH_BACKUP_DIR/meta/$name.before"
  printf '%s\n' "$q" | docker exec -i "$cname" clickhouse-client --multiquery --format TSV | sort >"$tmp/restored"
  # Cada tabla restaurada tiene entre las filas medidas antes y después del BACKUP (la ingesta
  # sigue mientras tanto) y ninguna tabla falta.
  join -j1 "$CH_BACKUP_DIR/meta/$name.before" "$CH_BACKUP_DIR/meta/$name.after" | join -j1 - "$tmp/restored" >"$tmp/joined"
  local expected got
  expected="$(wc -l <"$CH_BACKUP_DIR/meta/$name.before")"
  got="$(wc -l <"$tmp/joined")"
  [ "$expected" = "$got" ] || die "faltan tablas tras restaurar $name ($got de $expected)"
  if awk '$4 < $2 || $4 > $3 { bad = 1; printf "  %s: restauradas %s, esperado entre %s y %s\n", $1, $4, $2, $3 } END { exit bad }' "$tmp/joined" >&2; then
    log "ClickHouse: restauración OK — $got tablas de $name con las filas esperadas"
  else
    die "las filas restauradas de $name no coinciden"
  fi
}

# --- Comprobaciones ------------------------------------------------------------------------------
check_ttl() {
  local ttl
  # ClickHouse normaliza `INTERVAL 7 DAY` a `toIntervalDay(7)` en engine_full.
  ttl="$(ch_sql <<'SQL'
SELECT extract(engine_full, 'TTL .*?toIntervalDay\\((\\d+)\\)') FROM system.tables WHERE database = 'flows' AND name = 'flows_raw'
SQL
)"
  if [ "$ttl" = "$RAW_TTL_DAYS" ]; then
    state_set clickhouse raw_ttl_ok 1
    log "ClickHouse: TTL de flows.flows_raw = $ttl días (storage.md §5: $RAW_TTL_DAYS) OK"
  else
    state_set clickhouse raw_ttl_ok 0
    render_metrics
    alert "TTL de flows.flows_raw = '${ttl:-?}' días, esperado $RAW_TTL_DAYS (docs/storage.md §5)"
    return 1
  fi
  render_metrics
}

disk_metrics() {
  local vol path line avail used ratio
  for vol in store data; do
    path="$HORUS_STORE_DIR"; [ "$vol" = data ] && path="$HORUS_DATA_ROOT"
    line="$(df -P -B1 "$path" | awk 'NR == 2 { print $2, $3, $4 }')"
    read -r _ used avail <<<"$line"
    ratio="$(awk -v u="$used" -v a="$avail" 'BEGIN { printf "%.4f", ((u + a) > 0 ? u / (u + a) : 0) }')"
    state_set disk "${vol}_ratio" "$ratio"
    state_set disk "${vol}_free" "$avail"
    if awk -v r="$ratio" -v t="$ALERT_RATIO" 'BEGIN { exit !(r > t) }'; then
      alert "disco $vol ($path) al $(awk -v r="$ratio" 'BEGIN { printf "%d", r * 100 }') % (umbral $(awk -v t="$ALERT_RATIO" 'BEGIN { printf "%d", t * 100 }') %)"
    fi
  done
  render_metrics
}

status() {
  local c now; now="$(date +%s)"
  printf '%-11s %-8s %-22s %-10s %-8s %-22s\n' componente backup "último OK (UTC)" tamaño restore "última restauración OK"
  for c in postgres clickhouse; do
    local ok rs
    ok="$(state_get "$c" last_success 0)"; rs="$(state_get "$c" restore_success 0)"
    printf '%-11s %-8s %-22s %-10s %-8s %-22s\n' "$c" \
      "$([ "$(state_get "$c" last_status 0)" = 1 ] && echo OK || echo FALLO)" \
      "$([ "$ok" -gt 0 ] && date -u -d "@$ok" +%Y-%m-%dT%H:%MZ || echo nunca)" \
      "$(numfmt --to=iec "$(state_get "$c" size 0)" 2>/dev/null || state_get "$c" size 0)" \
      "$([ "$(state_get "$c" restore_status 0)" = 1 ] && echo OK || echo -)" \
      "$([ "$rs" -gt 0 ] && date -u -d "@$rs" +%Y-%m-%dT%H:%MZ || echo nunca)"
  done
  printf 'disco store: %s %%   disco datos: %s %%   TTL crudo OK: %s\n' \
    "$(awk -v r="$(state_get disk store_ratio 0)" 'BEGIN { printf "%.1f", r * 100 }')" \
    "$(awk -v r="$(state_get disk data_ratio 0)" 'BEGIN { printf "%.1f", r * 100 }')" \
    "$(state_get clickhouse raw_ttl_ok 0)"
  local age=$((now - $(state_get postgres last_success 0)))
  [ "$age" -lt 129600 ] || echo "AVISO: el último backup correcto de PostgreSQL tiene más de 36 h"
}

usage() { sed -n '2,40p' "$0" | sed -n '/^#   horus-backup/,/^# Métricas/p' | sed 's/^# \{0,1\}//'; exit 2; }

# Un solo horus-backup a la vez (timers solapados, ejecución manual).
exec 9>"$state_dir/lock"
if ! flock -w 3600 9; then die "otro horus-backup sigue en marcha"; fi

cmd="${1:-}"; shift || true
rc_total=0
case "$cmd" in
  run)
    what="${1:-all}"
    case "$what" in all | pg) run_step postgres backup pg_backup auto ;; esac
    case "$what" in all | ch) run_step clickhouse backup ch_backup auto ;; esac
    disk_metrics || true
    ;;
  pg) run_step postgres backup pg_backup "${1:-auto}" ;;
  ch) run_step clickhouse backup ch_backup "${1:-auto}" ;;
  verify)
    what="${1:-all}"
    case "$what" in all | pg) run_step postgres restore pg_verify ;; esac
    case "$what" in all | ch) run_step clickhouse restore ch_verify ;; esac
    ;;
  pg-init) run_plain pg_init ;;
  check-ttl) run_plain check_ttl ;;
  metrics) run_plain disk_metrics ;;
  status) status ;;
  ch-sql) ch_sql ;;
  *) usage ;;
esac
exit "$rc_total"
