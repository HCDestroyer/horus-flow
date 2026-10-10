#!/usr/bin/env bash
# horus-ctl — gestión diaria de una instalación de Horus Flow (historia I1-22).
#
# Lo instala scripts/install.sh en <instalación>/bin/horus-ctl (enlace en /usr/local/sbin), así
# que basta con `sudo horus-ctl <orden>`:
#
#   horus-ctl status                 versión, canal, URL, salud de cada servicio, backups y aviso
#                                    de versión nueva
#   horus-ctl logs [SERVICIO…] [-f] [-n LÍNEAS]   registros (todos o de horus-app, traefik…)
#   horus-ctl check                  comprobación completa (install.sh --check)
#   horus-ctl check-update           consulta si hay versión nueva en el canal (stable | beta)
#   horus-ctl upgrade [--to X.Y.Z | --bundle FICHERO.tar.gz] [--yes] [--patch-only]
#                                    actualiza: backup previo OBLIGATORIO, descarga verificada
#                                    (firma cosign + SHA256SUMS) o paquete offline (SHA256SUMS),
#                                    migraciones al arrancar, healthcheck y VUELTA ATRÁS automática
#                                    si algo falla (si el esquema cambió, restaura el backup previo)
#   horus-ctl rollback [--yes]       vuelve a la versión anterior a la última actualización
#   horus-ctl backup [run|verify|status]           copias locales (horus-backup)
#   horus-ctl restore [--pg latest|ETIQUETA] [--ch latest|NOMBRE] [--yes]
#                                    restaura PostgreSQL (pgBackRest) y/o ClickHouse en el sitio
#   horus-ctl uninstall [--purge] [--yes]          desinstala (conserva datos salvo --purge)
#   horus-ctl auto-update            (timer horus-autoupdate) aplica solo parches X.Y.Z del canal
#   horus-ctl version | help
#
# Variables: HORUS_RELEASE_BASE_URL (descargas de releases; por defecto las de GitHub del repo),
# HORUS_REPO (hcdestroyer/horus-flow), HORUS_SKIP_SIGNATURE=1 (no verifica cosign: solo pruebas),
# HORUS_HEALTH_TIMEOUT (segundos de espera del healthcheck tras actualizar; 300).
# Códigos de salida: 0 bien, 1 fallo, 2 uso incorrecto, 3 actualizado tras volver atrás (fallo).

# Los .env/.conf se generan en el servidor; las comillas simples de ch_sql se expanden en el contenedor.
# shellcheck disable=SC1090,SC2016
set -euo pipefail
umask 022

self="$(readlink -f "$0")"
release_dir="$(cd "$(dirname "$self")/.." && pwd)"
install_dir="${HORUS_INSTALL_DIR:-$(dirname "$release_dir")}"
env_file="$install_dir/.env"
repo="${HORUS_REPO:-hcdestroyer/horus-flow}"

c_ok=$'\033[32m' c_warn=$'\033[33m' c_err=$'\033[31m' c_off=$'\033[0m'
[ -t 1 ] || { c_ok=""; c_warn=""; c_err=""; c_off=""; }
say() { printf '==> %s\n' "$*"; }
ok() { printf '  %sOK%s    %s\n' "$c_ok" "$c_off" "$*"; }
warn() { printf '  %sAVISO%s %s\n' "$c_warn" "$c_off" "$*"; }
die() { printf '%shorus-ctl: %s%s\n' "$c_err" "$*" "$c_off" >&2; exit 1; }
usage() { sed -n '2,32p' "$self" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

cmd="${1:-help}"; [ "$#" -eq 0 ] || shift
case "$cmd" in help | -h | --help) usage 0 ;; esac
[ "$(id -u)" = 0 ] || die "ejecútalo como root: sudo horus-ctl $cmd"
[ -f "$env_file" ] || die "no hay instalación en $install_dir (falta .env); instala con bootstrap-debian.sh o install.sh"

set -a; . "$env_file"; set +a
etc_dir="${HORUS_ETC_DIR:-$(dirname "$HORUS_SECRETS_DIR")}"
conf_file="$etc_dir/install.conf"
declare -A conf=()
if [ -f "$conf_file" ]; then
  while IFS='=' read -r k v; do [[ "$k" =~ ^[a-z0-9-]+$ ]] && conf[$k]="$v"; done <"$conf_file"
fi
root_opt=()
case "$install_dir" in /opt/horus) ;; */opt/horus) root_opt=(--root "${install_dir%/opt/horus}") ;; esac
compose=(docker compose --project-directory "$install_dir" -f "$install_dir/compose.yaml" --env-file "$env_file")
state_dir="$install_dir/upgrade"
cache_dir="${install_dir%/opt/horus}/var/cache/horus"
current_version() { tr -d ' \r\n' <"$install_dir/release/VERSION" 2>/dev/null || echo "${HORUS_VERSION:-0.0.0-dev}"; }
token_file="$etc_dir/registry-token"
lock_file="/run/lock/horus-ctl.lock"; [ -d /run/lock ] || lock_file="/tmp/horus-ctl.lock"
take_lock() { exec 9>"$lock_file"; flock -n 9 || die "otra operación de horus-ctl está en curso"; }

# --- Versionado semántico -------------------------------------------------------------------------
semver_valid() { [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; }
# semver_cmp A B → imprime -1, 0 o 1 (precedencia de semver.org §11).
semver_cmp() {
  local a="$1" b="$2" ca cb pa="" pb="" i x y
  ca="${a%%-*}"; cb="${b%%-*}"
  [ "$ca" = "$a" ] || pa="${a#*-}"
  [ "$cb" = "$b" ] || pb="${b#*-}"
  local -a A B
  IFS=. read -r -a A <<<"$ca"; IFS=. read -r -a B <<<"$cb"
  for i in 0 1 2; do
    if [ "$((10#${A[i]}))" -gt "$((10#${B[i]}))" ]; then echo 1; return; fi
    if [ "$((10#${A[i]}))" -lt "$((10#${B[i]}))" ]; then echo -1; return; fi
  done
  if [ -z "$pa" ] && [ -z "$pb" ]; then echo 0; return; fi
  if [ -z "$pa" ]; then echo 1; return; fi
  if [ -z "$pb" ]; then echo -1; return; fi
  IFS=. read -r -a A <<<"$pa"; IFS=. read -r -a B <<<"$pb"
  for ((i = 0; i < ${#A[@]} || i < ${#B[@]}; i++)); do
    x="${A[i]:-}"; y="${B[i]:-}"
    if [ -z "$x" ]; then echo -1; return; fi
    if [ -z "$y" ]; then echo 1; return; fi
    if [[ "$x" =~ ^[0-9]+$ ]] && [[ "$y" =~ ^[0-9]+$ ]]; then
      if [ "$((10#$x))" -gt "$((10#$y))" ]; then echo 1; return; fi
      if [ "$((10#$x))" -lt "$((10#$y))" ]; then echo -1; return; fi
    elif [[ "$x" =~ ^[0-9]+$ ]]; then echo -1; return
    elif [[ "$y" =~ ^[0-9]+$ ]]; then echo 1; return
    elif [[ "$x" > "$y" ]]; then echo 1; return
    elif [[ "$x" < "$y" ]]; then echo -1; return; fi
  done
  echo 0
}
semver_gt() { [ "$(semver_cmp "$1" "$2")" = 1 ]; }
major_minor() { local c="${1%%-*}"; printf '%s' "${c%.*}"; }

# --- Fuente de versiones --------------------------------------------------------------------------
auth_header() { [ -s "$token_file" ] && printf 'Authorization: Bearer %s' "$(cat "$token_file")"; }
http_get() { # http_get URL [cabecera…]: cuerpo por stdout
  local url="$1" h; shift
  local args=(-fsSL --retry 2 -m 60)
  h="$(auth_header || true)"; [ -z "$h" ] || args+=(-H "$h")
  for x in "$@"; do args+=(-H "$x"); done
  curl "${args[@]}" "$url"
}

# releases_tsv: VERSIÓN<TAB>PRERELEASE<TAB>URL_NOTAS<TAB>FECHA (API de GitHub o latest.json).
releases_tsv() {
  local src="${HORUS_UPDATE_SOURCE:-https://api.github.com/repos/$repo/releases?per_page=50}" body
  command -v jq >/dev/null 2>&1 || die "falta jq (apt install jq)"
  body="$(http_get "$src" "Accept: application/vnd.github+json")" || return 1
  printf '%s' "$body" | jq -r '
    if type == "array" then .[] | select(.draft | not) | [(.tag_name | ltrimstr("v")), (.prerelease | tostring), .html_url, (.published_at // "")] | @tsv
    else (.channels // {}) | to_entries[] | [(.value.version | ltrimstr("v")), ((.key != "stable") | tostring), (.value.notes_url // ""), (.value.published_at // "")] | @tsv
    end'
}

# latest_in_channel CANAL [X.Y]: mayor versión publicada del canal (opcional: misma X.Y).
latest_in_channel() {
  local ch="$1" mm="${2:-}" best="" v pre _rest
  while IFS=$'\t' read -r v pre _rest; do
    semver_valid "$v" || continue
    [ "$ch" = beta ] || { [ "$pre" = false ] && [ "$v" = "${v%%-*}" ]; } || continue
    [ -z "$mm" ] || [ "$(major_minor "$v")" = "$mm" ] || continue
    if [ -z "$best" ] || semver_gt "$v" "$best"; then best="$v"; fi
  done < <(releases_tsv)
  printf '%s' "$best"
}

do_check_update() {
  local cur ch latest
  cur="$(current_version)"; ch="${HORUS_UPDATE_CHANNEL:-stable}"
  install -d -m 0755 "$state_dir"
  if ! latest="$(latest_in_channel "$ch")" || [ -z "$latest" ]; then
    printf 'checked_at=%s\nstatus=unchecked\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$state_dir/update-check"
    warn "no comprobado: sin acceso a la fuente de versiones (${HORUS_UPDATE_SOURCE:-GitHub})"
    return 0
  fi
  if semver_gt "$latest" "$cur"; then
    printf 'checked_at=%s\nstatus=available\nlatest=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$latest" >"$state_dir/update-check"
    echo "Hay una versión nueva: $latest (instalada $cur, canal $ch)."
    echo "Notas: https://github.com/$repo/releases/tag/v$latest"
    echo "Para actualizar: sudo horus-ctl upgrade --to $latest"
  else
    printf 'checked_at=%s\nstatus=up_to_date\nlatest=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$latest" >"$state_dir/update-check"
    echo "Horus Flow $cur está al día (canal $ch)."
  fi
}

# --- Salud ----------------------------------------------------------------------------------------
# health_ok: todos los servicios healthy (nats-init completado), API 401 y web 200 por Traefik.
health_report() {
  local svc st bad=0 code host
  while read -r svc st; do
    [ -n "$svc" ] || continue
    case "$st" in
      healthy) printf '  %-18s %s\n' "$svc" "sano" ;;
      exited*) if [ "$svc" = nats-init ]; then printf '  %-18s %s\n' "$svc" "completado"; else printf '  %-18s %s\n' "$svc" "PARADO ($st)"; bad=1; fi ;;
      *) printf '  %-18s %s\n' "$svc" "$st"; bad=1 ;;
    esac
  done < <("${compose[@]}" ps -a --format '{{.Service}} {{if .Health}}{{.Health}}{{else}}{{.State}}{{end}}' 2>/dev/null)
  host="${HORUS_PUBLIC_BASE_URL#https://}"
  code="$(curl -sk -o /dev/null -m 10 -w '%{http_code}' "https://127.0.0.1:$HORUS_HTTPS_PORT/api/v1/system/status" -H "Host: $host" || true)"
  [ "$code" = 401 ] || { printf '  %-18s HTTP %s (se esperaba 401)\n' "API" "${code:-sin respuesta}"; bad=1; }
  code="$(curl -sk -o /dev/null -m 10 -w '%{http_code}' "https://127.0.0.1:$HORUS_HTTPS_PORT/" -H "Host: $host" || true)"
  [ "$code" = 200 ] || { printf '  %-18s HTTP %s (se esperaba 200)\n' "web" "${code:-sin respuesta}"; bad=1; }
  return "$bad"
}
health_wait() { # health_wait SEGUNDOS
  local deadline=$((SECONDS + ${1:-300}))
  while [ "$SECONDS" -lt "$deadline" ]; do
    health_report >/dev/null 2>&1 && return 0
    sleep 5
  done
  health_report || true
  return 1
}

# --- Estado ---------------------------------------------------------------------------------------
do_status() {
  say "Horus Flow $(current_version) — canal ${HORUS_UPDATE_CHANNEL:-stable}"
  echo "  URL:      $HORUS_PUBLIC_BASE_URL (modo $HORUS_ACCESS_MODE)"
  echo "  Imágenes: ${conf[image-source]:-local}"
  echo "  Actualización automática de parches: ${conf[auto-update]:-off}${conf[update-window]:+ (ventana ${conf[update-window]})}"
  if [ -f "$state_dir/update-check" ]; then
    local st latest at
    st="$(sed -n 's/^status=//p' "$state_dir/update-check")"; latest="$(sed -n 's/^latest=//p' "$state_dir/update-check")"
    at="$(sed -n 's/^checked_at=//p' "$state_dir/update-check")"
    case "$st" in
      available) echo "  Versión nueva: $latest (comprobado $at) → sudo horus-ctl upgrade --to $latest" ;;
      up_to_date) echo "  Al día (comprobado $at)" ;;
      *) echo "  Versiones: no comprobado ($at)" ;;
    esac
  fi
  say "Servicios"
  if health_report; then ok "todo sano"; else warn "hay servicios con problemas: sudo horus-ctl logs SERVICIO"; fi
  say "Copias de seguridad"
  "$install_dir/bin/horus-backup" status 2>/dev/null | sed 's/^/  /' | head -20 || true
}

do_logs() {
  local follow=() n=200 svcs=()
  while [ "$#" -gt 0 ]; do
    case "$1" in -f | --follow) follow=(-f); shift ;; -n) n="$2"; shift 2 ;; *) svcs+=("$1"); shift ;; esac
  done
  exec "${compose[@]}" logs --tail "$n" "${follow[@]}" "${svcs[@]}"
}

# --- Backups y restauración -----------------------------------------------------------------------
pgbr() { "${compose[@]}" exec -T -u postgres postgres pgbackrest --config=/run/secrets/pgbackrest_conf --stanza=horus "$@"; }
pg_latest_label() { pgbr info --output=json 2>/dev/null | jq -r '.[0].backup[-1].label // empty'; }
ch_latest_name() { find "$HORUS_STORE_DIR/backups/clickhouse" -mindepth 1 -maxdepth 1 -type d \( -name 'full_*' -o -name 'incr_*' \) -printf '%f\n' 2>/dev/null | sort -t_ -k2 | tail -1; }
ch_sql() {
  "${compose[@]}" exec -T clickhouse sh -c \
    'exec clickhouse-client --user "$CLICKHOUSE_USER" --password "$(cat /run/secrets/clickhouse_password)" --multiquery --format TSV'
}
# Huella del esquema (columnas) de PostgreSQL y ClickHouse: si cambia, la versión migró datos.
schema_fingerprint() {
  local pg ch
  pg="$("${compose[@]}" exec -T -u postgres postgres psql -XAtq -U "${HORUS_PG_USER:-horus}" -d "${HORUS_PG_DB:-horus}" -c \
    "SELECT md5(coalesce(string_agg(table_schema||'.'||table_name||'.'||column_name||':'||data_type, ',' ORDER BY table_schema, table_name, column_name), '')) FROM information_schema.columns WHERE table_schema NOT IN ('pg_catalog','information_schema')" 2>/dev/null || echo "?")"
  ch="$(echo "SELECT hex(MD5(arrayStringConcat(groupArray(concat(database,'.',table,'.',name,':',type)), ','))) FROM (SELECT database, table, name, type FROM system.columns WHERE database IN ('flows','dim') ORDER BY database, table, name)" | ch_sql 2>/dev/null || echo "?")"
  printf 'pg=%s ch=%s\n' "$pg" "$ch"
}

do_backup() {
  local sub="${1:-run}"; [ "$#" -eq 0 ] || shift
  case "$sub" in
    run) exec "$install_dir/bin/horus-backup" run all ;;
    verify) exec "$install_dir/bin/horus-backup" verify all ;;
    status) exec "$install_dir/bin/horus-backup" status ;;
    *) die "backup: run | verify | status" ;;
  esac
}

app_services=(horus-app horus-collector horus-wg-agent horus-web)

restore_pg() { # restore_pg latest|ETIQUETA
  local set="$1" args=(--delta)
  [ "$set" = latest ] || args+=(--set="$set" --type=immediate --target-action=promote)
  say "PostgreSQL: restauración en el sitio (${set})"
  "${compose[@]}" stop postgres >/dev/null
  docker run --rm --network none --user 0:0 \
    -v "$HORUS_DATA_ROOT/postgres:/var/lib/postgresql" \
    -v "$HORUS_STORE_DIR/backups/postgres:/var/lib/pgbackrest" \
    -v "$HORUS_SECRETS_DIR/pgbackrest.conf:/etc/pgbackrest/pgbackrest.conf:ro" \
    --entrypoint gosu "$HORUS_POSTGRES_IMAGE" postgres \
    pgbackrest --config=/etc/pgbackrest/pgbackrest.conf --stanza=horus --log-level-console=warn "${args[@]}" restore \
    || die "pgbackrest restore falló"
  "${compose[@]}" up -d --wait --wait-timeout 300 postgres >/dev/null || die "PostgreSQL no arrancó tras restaurar"
  ok "PostgreSQL restaurado ($set)"
}

restore_ch() { # restore_ch latest|NOMBRE
  local name="$1" t
  [ "$name" != latest ] || name="$(ch_latest_name)"
  [ -n "$name" ] && [ -d "$HORUS_STORE_DIR/backups/clickhouse/$name" ] || die "no existe el backup de ClickHouse '$name'"
  say "ClickHouse: restauración en el sitio de $name (dim y flows; flows_raw no está en los backups)"
  { echo "DROP DATABASE IF EXISTS dim SYNC;"
    echo "SELECT name FROM system.tables WHERE database = 'flows' AND name != 'flows_raw' AND name NOT LIKE '.inner%' FORMAT TSV;"
  } | ch_sql >"$state_dir/ch-tables.tmp"
  while read -r t; do
    [ -n "$t" ] && echo "DROP TABLE IF EXISTS flows.\`$t\` SYNC;"
  done <"$state_dir/ch-tables.tmp" | ch_sql >/dev/null
  rm -f "$state_dir/ch-tables.tmp"
  echo "RESTORE DATABASE flows EXCEPT TABLES flows.flows_raw, DATABASE dim FROM Disk('backups', '$name')" | ch_sql >/dev/null \
    || die "RESTORE de ClickHouse falló"
  ok "ClickHouse restaurado ($name)"
}

do_restore() {
  local pg="" ch="" yes=0
  while [ "$#" -gt 0 ]; do
    case "$1" in --pg) pg="$2"; shift 2 ;; --ch) ch="$2"; shift 2 ;; --yes | -y) yes=1; shift ;; *) die "restore: opción desconocida $1" ;; esac
  done
  [ -n "$pg$ch" ] || { pg=latest; ch=latest; }
  if [ "$yes" = 0 ]; then
    local a=""
    read -r -p "Restaurar SUSTITUYE los datos actuales (PostgreSQL: ${pg:-no}, ClickHouse: ${ch:-no}). Escribe 'restaurar': " a || true
    [ "$a" = restaurar ] || die "cancelado"
  fi
  install -d -m 0755 "$state_dir"
  "${compose[@]}" stop "${app_services[@]}" >/dev/null 2>&1 || true
  [ -z "$pg" ] || restore_pg "$pg"
  [ -z "$ch" ] || restore_ch "$ch"
  "${compose[@]}" up -d --wait --wait-timeout 600 >/dev/null || die "los servicios no quedaron sanos tras restaurar (horus-ctl logs)"
  ok "restauración completada y servicios sanos"
}

# --- Descargas verificadas ------------------------------------------------------------------------
release_base() { printf '%s' "${HORUS_RELEASE_BASE_URL:-https://github.com/$repo/releases/download}"; }
# fetch_asset VERSIÓN NOMBRE DESTINO (repos privados: API con el token de lectura)
fetch_asset() {
  local ver="$1" name="$2" dst="$3" id
  if [ -s "$token_file" ] && [ -z "${HORUS_RELEASE_BASE_URL:-}" ]; then
    id="$(http_get "https://api.github.com/repos/$repo/releases/tags/v$ver" "Accept: application/vnd.github+json" \
      | jq -r --arg n "$name" '.assets[] | select(.name == $n) | .id')" || return 1
    [ -n "$id" ] || return 1
    http_get "https://api.github.com/repos/$repo/releases/assets/$id" "Accept: application/octet-stream" >"$dst"
  else
    http_get "$(release_base)/v$ver/$name" >"$dst"
  fi
}

cosign_bin() { command -v cosign 2>/dev/null || { [ -x "$install_dir/bin/cosign" ] && echo "$install_dir/bin/cosign"; } || true; }
# verify_sums_signature DIR: firma cosign keyless de SHA256SUMS (bundle sigstore).
verify_sums_signature() {
  local dir="$1" cosign
  if [ "${HORUS_SKIP_SIGNATURE:-0}" = 1 ]; then warn "HORUS_SKIP_SIGNATURE=1: no se verifica la firma de SHA256SUMS"; return 0; fi
  cosign="$(cosign_bin)"
  [ -f "$dir/SHA256SUMS.sigstore.json" ] || die "falta SHA256SUMS.sigstore.json (firma de la release)"
  [ -n "$cosign" ] || die "falta cosign para verificar la firma (bootstrap-debian.sh lo instala)"
  "$cosign" verify-blob --bundle "$dir/SHA256SUMS.sigstore.json" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-identity-regexp "^https://github.com/$repo/.github/workflows/release.yml@refs/tags/v" \
    "$dir/SHA256SUMS" >/dev/null 2>&1 || die "la firma de SHA256SUMS no es válida: no se actualiza"
  ok "firma cosign de SHA256SUMS verificada"
}

# prepare_ghcr VERSIÓN → imprime el directorio del árbol de esa versión (instalador sin imágenes).
prepare_ghcr() {
  local ver="$1" dir tgz
  dir="$cache_dir/releases/$ver"
  tgz="horus-$ver-installer.tar.gz"
  install -d -m 0700 "$cache_dir/releases"
  rm -rf "$dir.dl"; install -d -m 0700 "$dir.dl"
  fetch_asset "$ver" SHA256SUMS "$dir.dl/SHA256SUMS" || die "no se pudo descargar SHA256SUMS de v$ver"
  fetch_asset "$ver" SHA256SUMS.sigstore.json "$dir.dl/SHA256SUMS.sigstore.json" || true
  fetch_asset "$ver" "$tgz" "$dir.dl/$tgz" || die "no se pudo descargar $tgz"
  verify_sums_signature "$dir.dl" >&2
  (cd "$dir.dl" && grep " [*]\{0,1\}$tgz\$" SHA256SUMS | sha256sum -c --quiet -) || die "SHA256 de $tgz no coincide"
  ok "SHA256 de $tgz verificado" >&2
  rm -rf "$dir"; install -d -m 0755 "$dir"
  tar -xzf "$dir.dl/$tgz" -C "$dir" --strip-components=1
  rm -rf "$dir.dl"
  printf '%s' "$dir"
}

# prepare_bundle FICHERO → directorio extraído y verificado (outer + inner SHA256SUMS).
prepare_bundle() {
  local f; f="$(readlink -f "$1")"
  [ -f "$f" ] || [ -d "$f" ] || die "no existe $1"
  if [ -d "$f" ]; then printf '%s' "$f"; return 0; fi
  local sums dir
  sums="$(dirname "$f")/SHA256SUMS"
  if [ -f "$sums" ]; then
    [ ! -f "$(dirname "$f")/SHA256SUMS.sigstore.json" ] || [ -z "$(cosign_bin)" ] || verify_sums_signature "$(dirname "$f")" >&2
    (cd "$(dirname "$f")" && grep " [*]\{0,1\}$(basename "$f")\$" SHA256SUMS | sha256sum -c --quiet -) \
      || die "SHA256 de $(basename "$f") no coincide con SHA256SUMS"
    ok "SHA256 de $(basename "$f") verificado" >&2
  else
    warn "sin SHA256SUMS junto a $(basename "$f"): se verifica solo su contenido" >&2
  fi
  dir="$cache_dir/bundles/$(basename "$f" .tar.gz)"
  install -d -m 0700 "$cache_dir/bundles"
  rm -rf "$dir"; install -d -m 0755 "$dir"
  tar -xzf "$f" -C "$dir" --strip-components=1 || die "no se pudo extraer $f"
  : >"$dir/.extracted"
  (cd "$dir" && sha256sum -c --quiet SHA256SUMS) || die "el contenido de $(basename "$f") no coincide con su SHA256SUMS"
  : >"$dir/.verified"
  printf '%s' "$dir"
}

# --- Actualización y vuelta atrás -----------------------------------------------------------------
snapshot() {
  local s="$state_dir/rollback"
  rm -rf "$s.new"; install -d -m 0700 "$s.new"
  cp -a "$env_file" "$install_dir/compose.yaml" "$install_dir/config" "$install_dir/release" "$s.new/"
  cp -a "$conf_file" "$s.new/install.conf"
  rm -rf "$s"; mv "$s.new" "$s"
}

restore_snapshot() {
  local s="$state_dir/rollback"
  [ -d "$s/release" ] || die "no hay instantánea de la versión anterior en $s"
  cp -a "$s/.env" "$env_file"; cp -a "$s/compose.yaml" "$install_dir/compose.yaml"
  rm -rf "$install_dir/config" "$install_dir/release"
  cp -a "$s/config" "$install_dir/config"; cp -a "$s/release" "$install_dir/release"
  cp -a "$s/install.conf" "$conf_file"
  set -a; . "$env_file"; set +a
}

# rollback_now RAZÓN: vuelve a la instantánea; si el esquema cambió (migraciones) o la versión
# anterior no arranca, restaura los backups previos a la actualización.
rollback_now() {
  local reason="$1" s="$state_dir/rollback" pg ch before after
  say "VUELTA ATRÁS: $reason"
  before="$(cat "$s/schema" 2>/dev/null || true)"
  after="$(schema_fingerprint 2>/dev/null || true)"
  restore_snapshot
  ok "configuración e imágenes de $(current_version) repuestas"
  pg="$(cat "$s/pg_label" 2>/dev/null || true)"; ch="$(cat "$s/ch_name" 2>/dev/null || true)"
  if [ -n "$before" ] && [ "$before" != "$after" ]; then
    warn "el esquema cambió durante la actualización: se restauran los backups previos (PostgreSQL ${pg:-?}, ClickHouse ${ch:-?})"
    "${compose[@]}" up -d --remove-orphans postgres clickhouse >/dev/null 2>&1 || true
    "${compose[@]}" up -d --wait --wait-timeout 300 postgres clickhouse >/dev/null 2>&1 || true
    do_restore ${pg:+--pg "$pg"} ${ch:+--ch "$ch"} --yes
  fi
  if "${compose[@]}" up -d --wait --wait-timeout 600 --remove-orphans >/dev/null 2>&1 && health_wait 180; then
    ok "vuelta atrás completada: Horus Flow $(current_version) sano"
    return 0
  fi
  if [ -n "$pg" ] && { [ -z "$before" ] || [ "$before" = "$after" ]; }; then
    warn "la versión anterior no arranca con los datos actuales: se restauran los backups previos"
    do_restore ${pg:+--pg "$pg"} ${ch:+--ch "$ch"} --yes && health_wait 180 && { ok "vuelta atrás completada con los datos del backup previo"; return 0; }
  fi
  die "la vuelta atrás no dejó los servicios sanos: revisa 'horus-ctl logs' y docs/install-debian.md (Problemas frecuentes)"
}

do_upgrade() {
  local to="" bundle="" yes=0 patch_only=0 force=0 cur tree src target
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --to) to="${2#v}"; shift 2 ;;
      --bundle) bundle="$2"; shift 2 ;;
      --yes | -y) yes=1; shift ;;
      --patch-only) patch_only=1; shift ;;
      --force) force=1; shift ;;
      *) die "upgrade: opción desconocida $1" ;;
    esac
  done
  take_lock
  cur="$(current_version)"
  install -d -m 0755 "$state_dir" "$cache_dir"
  if [ -n "$bundle" ]; then
    say "Paquete offline $bundle"
    tree="$(prepare_bundle "$bundle")"
    target="$(tr -d ' \r\n' <"$tree/VERSION")"
    src="bundle:$tree"
  else
    if [ -z "$to" ]; then
      to="$(latest_in_channel "${HORUS_UPDATE_CHANNEL:-stable}")" || die "no se pudo consultar la fuente de versiones (¿sin Internet? usa --bundle)"
      [ -n "$to" ] || die "no hay versiones publicadas en el canal ${HORUS_UPDATE_CHANNEL:-stable}"
    fi
    target="$to"
  fi
  semver_valid "$target" || die "versión inválida: $target"
  if ! semver_gt "$target" "$cur"; then
    [ "$force" = 1 ] || { ok "Horus Flow $cur ya está al día (objetivo $target)"; return 0; }
    warn "--force: se reinstala $target sobre $cur"
  fi
  if [ "$patch_only" = 1 ] && [ "$(major_minor "$target")" != "$(major_minor "$cur")" ]; then
    die "$target no es un parche de $cur: las versiones X.Y se actualizan a mano (horus-ctl upgrade --to $target)"
  fi
  say "Actualización $cur → $target"
  if [ "$yes" = 0 ]; then
    local a=""
    read -r -p "  Se hará un backup completo, se aplicará $target y, si falla, se volverá a $cur. ¿Continuar? [s/N] " a || true
    [[ "$a" =~ ^[sSyY] ]] || die "cancelado"
  fi
  if [ -z "$bundle" ]; then
    say "Descarga verificada de v$target"
    tree="$(prepare_ghcr "$target")"
    src=ghcr
  fi
  [ -x "$tree/scripts/install.sh" ] || die "$tree no trae scripts/install.sh"
  health_report >/dev/null 2>&1 || warn "la instalación actual no está sana antes de actualizar"

  say "Backup previo (obligatorio antes de migrar)"
  "$install_dir/bin/horus-backup" run all >"$state_dir/backup.log" 2>&1 || { tail -20 "$state_dir/backup.log" >&2; die "el backup previo falló: NO se actualiza"; }
  snapshot
  pg_latest_label >"$state_dir/rollback/pg_label" || true
  ch_latest_name >"$state_dir/rollback/ch_name" || true
  schema_fingerprint >"$state_dir/rollback/schema" || true
  printf '%s\n' "$cur" >"$state_dir/rollback/from-version"
  ok "backup correcto (PostgreSQL $(cat "$state_dir/rollback/pg_label"), ClickHouse $(cat "$state_dir/rollback/ch_name")); instantánea en $state_dir/rollback"

  say "Aplicando $target"
  local log
  log="$state_dir/upgrade-$target-$(date -u +%Y%m%dT%H%M%SZ).log"
  if ! HORUS_INSTALL_WAIT="${HORUS_INSTALL_WAIT:-600}" bash "$tree/scripts/install.sh" --yes --image-source "$src" \
    "${root_opt[@]}" --etc-dir "$etc_dir" >"$log" 2>&1; then
    tail -25 "$log" >&2
    rollback_now "la instalación de $target falló (registro: $log)"
    exit 3
  fi
  set -a; . "$env_file"; set +a
  if ! health_wait "${HORUS_HEALTH_TIMEOUT:-300}"; then
    rollback_now "$target no superó el healthcheck"
    exit 3
  fi
  ok "Horus Flow $target instalado y sano (registro: $log)"
  printf 'checked_at=%s\nstatus=up_to_date\nlatest=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$target" >"$state_dir/update-check"
  logger -t horus-ctl "actualizado $cur → $target" 2>/dev/null || true
}

do_rollback() {
  local yes=0; [ "${1:-}" = --yes ] && yes=1
  take_lock
  local from; from="$(cat "$state_dir/rollback/from-version" 2>/dev/null || true)"
  [ -n "$from" ] || die "no hay actualización que deshacer"
  if [ "$yes" = 0 ]; then
    local a=""; read -r -p "Volver de $(current_version) a $from? [s/N] " a || true
    [[ "$a" =~ ^[sSyY] ]] || die "cancelado"
  fi
  rollback_now "pedida a mano"
}

do_auto_update() {
  [ "${conf[auto-update]:-off}" = on ] || { echo "horus-ctl: actualización automática desactivada (install.sh --auto-update on)"; return 0; }
  local cur target
  cur="$(current_version)"
  semver_valid "$cur" || { echo "horus-ctl: versión instalada $cur sin semver: no se actualiza sola"; return 0; }
  if ! target="$(latest_in_channel "${HORUS_UPDATE_CHANNEL:-stable}" "$(major_minor "$cur")")"; then
    logger -t horus-ctl "auto-update: no comprobado (sin acceso a la fuente de versiones)" 2>/dev/null || true
    echo "horus-ctl: no comprobado (sin acceso a la fuente de versiones)"; return 0
  fi
  if [ -z "$target" ] || ! semver_gt "$target" "$cur"; then echo "horus-ctl: sin parches nuevos para $cur"; return 0; fi
  logger -t horus-ctl "auto-update: aplicando parche $cur → $target" 2>/dev/null || true
  do_upgrade --to "$target" --yes --patch-only
}

do_uninstall() {
  local args=(--uninstall) yes=0
  while [ "$#" -gt 0 ]; do
    case "$1" in --purge) args+=(--purge); shift ;; --yes | -y) args+=(--yes); yes=1; shift ;; *) die "uninstall: opción desconocida $1" ;; esac
  done
  if [ "$yes" = 0 ]; then
    local a=""; read -r -p "Desinstalar Horus Flow (los datos se conservan salvo --purge)? [s/N] " a || true
    [[ "$a" =~ ^[sSyY] ]] || die "cancelado"
  fi
  local tmp; tmp="$(mktemp -d)"
  cp -a "$install_dir/release/scripts/install.sh" "$tmp/install.sh"
  bash "$tmp/install.sh" "${args[@]}" "${root_opt[@]}" --etc-dir "$etc_dir"
  rm -rf "$tmp" "$state_dir"
}

case "$cmd" in
  status) do_status ;;
  logs) do_logs "$@" ;;
  check) exec bash "$install_dir/release/scripts/install.sh" --check "${root_opt[@]}" --etc-dir "$etc_dir" ;;
  check-update) do_check_update ;;
  upgrade) do_upgrade "$@" ;;
  rollback) do_rollback "$@" ;;
  backup) do_backup "$@" ;;
  restore) take_lock; do_restore "$@" ;;
  uninstall) do_uninstall "$@" ;;
  auto-update) do_auto_update ;;
  version) current_version; echo ;;
  *) echo "horus-ctl: orden desconocida: $cmd" >&2; usage 2 ;;
esac
