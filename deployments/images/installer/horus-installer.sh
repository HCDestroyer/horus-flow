#!/usr/bin/env bash
# horus-installer — punto de entrada de la imagen horus-installer: instalación de Horus Flow SOLO
# con Docker, sin ejecutar scripts en el anfitrión (docs/install-docker.md).
#
# No es un segundo instalador: ejecuta el mismo scripts/install.sh (con --runtime docker), horus-ctl
# y horus-backup de esta versión. Secretos, .env, compose y datos quedan en el ANFITRIÓN porque sus
# directorios se montan en la MISMA ruta (compose y horus-backup interpretan las rutas en el anfitrión):
#
#   docker run --rm -it --network host \
#     -v /var/run/docker.sock:/var/run/docker.sock \
#     -v /opt/horus:/opt/horus -v /etc/horus:/etc/horus -v /var/lib/horus:/var/lib/horus \
#     ghcr.io/hcdestroyer/horus-installer:X.Y.Z@sha256:… install --mode ip
#
# AVISO: el socket de Docker da al contenedor control total del anfitrión (equivale a root).
#
# Órdenes:
#   install [OPCIONES]         instala o vuelve a aplicar la configuración (idempotente; nunca regenera
#                              secretos). OPCIONES: las de install.sh (--mode, --domain, --admin-email…)
#   upgrade [OPCIONES]         backup previo obligatorio y después install con ESTA versión de la imagen
#   check                      comprobación completa (install.sh --check)
#   uninstall [--purge] [--yes]   quita contenedores, túnel y reglas; --purge borra datos y secretos
#   confirm-bundle             marca el paquete de secretos offline como guardado fuera del servidor
#   ctl ORDEN [ARGS]           horus-ctl: status, logs, backup, restore, remote-decrypt, diagnose…
#                              (upgrade/rollback: con la imagen horus-installer de la versión deseada)
#   backup [run|verify|status|…]   horus-backup
#   stack                      compose.stack.yaml (Portainer, TrueNAS…): install --yes (con backup previo
#                              si ya hay instalación)
#   host-agent                 (servicio horus-host) túnel WireGuard, cortafuegos y backups programados
#   host-health                healthcheck de horus-host
#   version | help
#
# Variables HORUS_OPT_<NOMBRE>=valor → --<nombre> valor de install.sh (HORUS_OPT_MODE=ip,
# HORUS_OPT_ADMIN_EMAIL=noc@isp.net, HORUS_OPT_ETC_DIR=/mnt/tank/horus/etc…); en las opciones sin
# valor, 1/true/yes/on (HORUS_OPT_ACME_STAGING=1). Vacías = no se pasan.
set -euo pipefail

release="${HORUS_RELEASE_DIR:-/usr/share/horus/release}"
install_sh="$release/scripts/install.sh"
ready=/run/horus-host.ready

say() { printf '==> %s\n' "$*"; }
log() { printf '%s horus-host: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
die() { printf 'horus-installer: %s\n' "$*" >&2; exit 1; }
usage() { sed -n '2,34p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

# --- Opciones desde el entorno (stack de Portainer/TrueNAS) ----------------------------------------
env_opts=()
etc_dir="${HORUS_OPT_ETC_DIR:-/etc/horus}"
for v in $(compgen -v | grep -E '^HORUS_OPT_[A-Z0-9_]+$' || true); do
  val="${!v}"
  unset "$v"
  [ -n "$val" ] || continue
  name="${v#HORUS_OPT_}"; name="${name,,}"; flag="--${name//_/-}"
  case "$flag" in
    --acme-staging | --no-update-check | --behind-proxy | --force | --skip-firewall | --skip-tunnel | --skip-backup | --yes)
      case "${val,,}" in 1 | true | yes | on | si | sí) env_opts+=("$flag") ;; esac ;;
    *) env_opts+=("$flag" "$val") ;;
  esac
done
etc_opt=(--etc-dir "$etc_dir")

# Directorio de la instalación (de install.conf; lo escribe install.sh).
install_dir() {
  local d=""
  [ -f "$etc_dir/install.conf" ] && d="$(sed -n 's/^install-dir=//p' "$etc_dir/install.conf" | tail -1)"
  printf '%s' "${d:-/opt/horus}"
}
need_install() { [ -f "$(install_dir)/.env" ] || die "no hay instalación en $(install_dir) (¿montaste $etc_dir y $(install_dir) con la misma ruta?)"; }
need_socket() { docker info >/dev/null 2>&1 || die "no llego al daemon de Docker: monta -v /var/run/docker.sock:/var/run/docker.sock"; }

# --- horus-host ------------------------------------------------------------------------------------
env_get() { # env_get <VARIABLE> <defecto> del .env de la instalación
  local v; v="$(sed -n "s/^$1=//p" "$env_file" | tail -1)"; printf '%s' "${v:-$2}"
}

# Tras crear wg0 de nuevo (reinicio del anfitrión, horus-host recreado con wireguard-go), el colector
# (publicado en la IP del hub) y wg-agent (clave, puerto y peers) se reinician para engancharse.
restart_dependents() {
  local svc id
  for svc in horus-collector horus-wg-agent; do
    for id in $(docker ps -aq --filter "label=com.docker.compose.project=$project" --filter "label=com.docker.compose.service=$svc"); do
      if docker restart -t 10 "$id" >/dev/null 2>&1; then log "$svc reiniciado ($wg_if nueva)"; else log "no se pudo reiniciar $svc"; fi
    done
  done
}

start_cron() {
  cron_pid=""
  [ -f "$crontab" ] || { log "sin $crontab: no hay backups programados"; return 0; }
  supercronic -passthrough-logs "$crontab" &
  cron_pid=$!
}

host_agent() {
  local install_dir first=1 created stop=""
  install_dir="${HORUS_INSTALL_DIR:-/opt/horus}"
  env_file="$install_dir/.env"
  [ -f "$env_file" ] || die "no hay $env_file (HORUS_INSTALL_DIR)"
  wg_if="$(env_get HORUS_WG_INTERFACE wg0)"
  project="$(env_get COMPOSE_PROJECT_NAME horus)"
  crontab="$install_dir/config/horus-host.crontab"
  local tunnel="$install_dir/bin/horus-tunnel"
  rm -f "$ready"
  trap 'stop=1' TERM INT
  start_cron
  log "vigilando $wg_if (proyecto $project)"
  while [ -z "$stop" ]; do
    if [ ! -x "$tunnel" ]; then
      [ "$first" = 0 ] || log "sin $tunnel (--skip-tunnel): solo backups programados"
      : >"$ready"; first=0
    elif [ "$first" = 1 ] || ! ip link show "$wg_if" >/dev/null 2>&1; then
      created=0; ip link show "$wg_if" >/dev/null 2>&1 || created=1
      if "$tunnel" up; then
        log "túnel $wg_if y cortafuegos aplicados"
        [ "$created" = 0 ] || restart_dependents
        : >"$ready"; first=0
      else
        rm -f "$ready"; log "horus-tunnel up falló; se reintenta en 10 s"
      fi
    fi
    if [ -n "${cron_pid:-}" ] && ! kill -0 "$cron_pid" 2>/dev/null; then log "supercronic terminó: se reinicia"; start_cron; fi
    sleep 10 &
    wait $! || true
  done
  log "parada (wg0 del kernel y las reglas se conservan; con wireguard-go, wg0 desaparece con el contenedor)"
  [ -z "${cron_pid:-}" ] || kill "$cron_pid" 2>/dev/null || true
}

host_health() {
  local install_dir; install_dir="${HORUS_INSTALL_DIR:-/opt/horus}"
  env_file="$install_dir/.env"
  [ -f "$ready" ] || exit 1
  if [ -x "$install_dir/bin/horus-tunnel" ]; then ip link show "$(env_get HORUS_WG_INTERFACE wg0)" >/dev/null 2>&1 || exit 1; fi
  if [ -f "$install_dir/config/horus-host.crontab" ]; then pgrep -x supercronic >/dev/null || exit 1; fi
}

# --- Órdenes ---------------------------------------------------------------------------------------
cmd="${1:-help}"; [ "$#" -eq 0 ] || shift
case "$cmd" in
  install)
    need_socket
    exec bash "$install_sh" --runtime docker "${env_opts[@]}" "$@" ;;
  stack | upgrade)
    need_socket
    # Volver a desplegar el stack o actualizar sobre una instalación existente: backup previo.
    if [ -x "$(install_dir)/bin/horus-backup" ] && [ -f "$(install_dir)/.env" ]; then
      say "Backup previo (horus-backup run all)"
      "$(install_dir)/bin/horus-backup" run all || die "el backup previo falló: no se aplica nada (horus-installer backup status)"
    fi
    yes=(); [ "$cmd" = upgrade ] || yes=(--yes)
    exec bash "$install_sh" --runtime docker "${yes[@]}" "${env_opts[@]}" "$@" ;;
  check) need_socket; exec bash "$install_sh" --check "${etc_opt[@]}" "$@" ;;
  uninstall) need_socket; exec bash "$install_sh" --uninstall "${etc_opt[@]}" "$@" ;;
  confirm-bundle) exec bash "$install_sh" --confirm-bundle "${etc_opt[@]}" ;;
  ctl)
    need_socket; need_install
    case "${1:-help}" in
      upgrade | rollback | auto-update)
        die "con la instalación solo Docker se actualiza ejecutando la imagen horus-installer de la versión deseada: horus-installer upgrade (docs/install-docker.md)" ;;
    esac
    ctl="$(install_dir)/release/scripts/horus-ctl.sh"; [ -f "$ctl" ] || ctl="$release/scripts/horus-ctl.sh"
    HORUS_INSTALL_DIR="$(install_dir)" exec bash "$ctl" "$@" ;;
  backup) need_socket; need_install; exec "$(install_dir)/bin/horus-backup" "$@" ;;
  host-agent) host_agent ;;
  host-health) host_health ;;
  version) tr -d ' \r\n' <"$release/VERSION"; echo ;;
  help | -h | --help) usage 0 ;;
  sh | bash) exec "$cmd" "$@" ;;
  *) echo "horus-installer: orden desconocida: $cmd" >&2; usage 2 ;;
esac
