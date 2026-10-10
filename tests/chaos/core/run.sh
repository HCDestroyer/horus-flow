#!/usr/bin/env bash
# run.sh — `make chaos-restart-core` (D23): matriz de reinicio brusco de los componentes no de flujos.
#
# Sobre una instalación REAL (scripts/install.sh, compose de producción, hub WireGuard) con routers
# simulados que se conectan por WireGuard de verdad (scripts/accept/sim-router.sh):
#
#   1. instala Horus en un raíz temporal (los pasos image, install e install-check de accept-i1, con
#      proyecto, interfaz, subred y puertos propios para no chocar con otra batería);
#   2. ejecuta el driver tests/chaos/core: alta de un ISP con dos routers simulados (túneles reales),
#      canal de alertas LibreNMS contra un receptor local, kiosco (cookie rotativa + WebSocket),
#      WebSocket de usuario, sesión del superadministrador y flujos del escenario `scan` en bucle;
#      después, en orden aleatorio y repetido (CHAOS_ROUNDS), `kill -9` del proceso principal y
#      `docker restart` de horus-app, horus-wg-agent, PostgreSQL, NATS y Valkey en un momento
#      aleatorio, y tras cada uno comprueba recuperación, túneles, sesión, WebSocket y kiosco;
#      después, una caída larga de horus-app (9 min, más que una ventana del motor) con flujos del
#      escenario dos_out durante la caída: al volver, su hallazgo debe abrirse (ventanas atrasadas);
#      al final, hallazgos sin pérdidas ni duplicados, alertas sin pérdidas ni duplicados (salvo la
#      deduplicación documentada), outbox sin eventos colgados, eventos de plataforma y un paquete
#      `horus diagnose` sin IPs de clientes;
#   3. `make accept-i1` contra la MISMA instalación ya castigada (TARGET=installed);
#   4. desinstala (salvo CHAOS_KEEP=1).
#
# Variables:
#   CHAOS_ROUNDS (2)               vueltas de la matriz (10 escenarios por vuelta)
#   CHAOS_SCENARIOS                subconjunto, p. ej. "kill:horus-app,restart:postgres"
#   CHAOS_SEED                     semilla del orden y de los momentos (por defecto, la hora)
#   CHAOS_SKIP_ACCEPT=1            no ejecuta accept-i1 al final
#   CHAOS_SKIP_OUTAGE=1            sin la caída larga de horus-app
#   CHAOS_KEEP=1                   conserva instalación y routers simulados
#   CHAOS_REUSE=1                  reutiliza la instalación de CHAOS_ROOT (no reinstala)
#   CHAOS_ROOT (/tmp/horus-chaos-core), CHAOS_PORT_OFFSET (22000), ACCEPT_IMAGE_MODE (auto)
#
# Resultados: bin/chaos-core/report.{md,json}, diagnose.tar.gz, logs del driver y de los pasos.
# Necesita root, Docker con compose v2, Go, wireguard-tools y el módulo wireguard o wireguard-go.
set -uo pipefail

repo_root="$(cd "$(dirname "$0")/../../.." && pwd)"
cd "$repo_root" || exit 1
GO="${GO:-go}"
MAKE="${MAKE:-make}"
export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"

out="$repo_root/bin/chaos-core"
mkdir -p "$out"
root="${CHAOS_ROOT:-/tmp/horus-chaos-core}"
offset="${CHAOS_PORT_OFFSET:-22000}"

# Instalación propia (accept-i1 con otros nombres): proyecto, interfaz del hub, subred, túneles y
# routers simulados distintos de los de accept-i1 por defecto.
export TARGET=local ACCEPT_LOCAL_ROOT="$root" ACCEPT_PORT_OFFSET="$offset" ACCEPT_PROJECT=horus-chaos-core
export ACCEPT_WG_INTERFACE=hfwgchaos ACCEPT_DOCKER_SUBNET=172.31.242.0/24 ACCEPT_TUNNEL_CIDR=10.242.0.0/16
export ACCEPT_OUT_DIR="$out/accept" ACCEPT_IMAGE="${ACCEPT_IMAGE:-horus:chaos-core}"
export SIM_ROUTER_NS_PREFIX=hfchaos- SIM_ROUTER_IF_PREFIX=hfch SIM_ROUTER_LINK_NET=169.254.78 SIM_ROUTER_DIR="$out/sim"
sim_router="$repo_root/scripts/accept/sim-router.sh"

[ "$(id -u)" = 0 ] || { echo "chaos-restart-core: necesita root (instalador, WireGuard, namespaces)"; exit 2; }

inst_env() { sed -nE "s/^$1=(.*)$/\1/p" "$root/opt/horus/.env" 2>/dev/null | tail -1; }

cleanup() {
  rc=$?
  if [ "${CHAOS_KEEP:-0}" = 1 ]; then
    echo "chaos-restart-core: se conservan la instalación ($root) y los routers simulados (CHAOS_KEEP=1)"
  else
    bash "$sim_router" down-all >/dev/null 2>&1 || true
    if [ -f "$root/opt/horus/.env" ] && [ "${CHAOS_REUSE:-0}" != 1 ]; then
      bash scripts/install.sh --uninstall --purge --yes --root "$root" >"$out/uninstall.log" 2>&1 || true
      rm -rf "$root"
    fi
  fi
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

echo "==> compilando horus, flowsim y el driver"
"$GO" build -o "$out/flowsim" ./tools/flowsim/cmd/flowsim || exit 1
"$GO" build -tags acceptance -o "$out/horus-chaos-core" ./tests/chaos/core || exit 1

if [ "${CHAOS_REUSE:-0}" != 1 ]; then
  echo "==> instalación de prueba en $root (pasos image, install e install-check de accept-i1)"
  ACCEPT_STEPS=image,install,install-check ACCEPT_KEEP=1 bash scripts/accept/accept-i1.sh >"$out/install.log" 2>&1
  rc=$?
  tail -n 15 "$out/install.log"
  [ "$rc" = 0 ] || { echo "chaos-restart-core: la instalación falló (log: bin/chaos-core/install.log)"; exit 1; }
fi

base="$(inst_env HORUS_PUBLIC_BASE_URL)"
cert=""
[ "$(inst_env HORUS_TLS_MODE)" = self_signed ] && cert="$(inst_env HORUS_TLS_DIR)/public.crt"
echo "==> matriz de reinicio contra $base (proyecto $(inst_env COMPOSE_PROJECT_NAME))"
env ${cert:+SSL_CERT_FILE="$cert"} CHAOS_BASE_URL="$base" CHAOS_INSTALL_DIR="$root/opt/horus" \
  CHAOS_PROJECT="$(inst_env COMPOSE_PROJECT_NAME)" CHAOS_ADMIN_EMAIL="$(inst_env HORUS_SEED_ADMIN_EMAIL)" \
  CHAOS_ADMIN_PASSWORD_FILE="$root/admin-password" CHAOS_PG_USER="$(inst_env HORUS_PG_USER)" CHAOS_PG_DB="$(inst_env HORUS_PG_DB)" \
  CHAOS_STATE_DIR="$out" CHAOS_SIM_ROUTER="$sim_router" CHAOS_FLOWSIM="$out/flowsim" \
  CHAOS_ACCEPT_CREDS="$ACCEPT_OUT_DIR/admin-creds.json" CHAOS_RECEIVER_PORT="$((offset + 999))" \
  "$out/horus-chaos-core" 2>&1 | tee "$out/driver.log"
chaos_rc=${PIPESTATUS[0]}

accept_rc=0
if [ "${CHAOS_SKIP_ACCEPT:-0}" != 1 ]; then
  echo "==> accept-i1 contra la instalación después del caos (TARGET=installed)"
  TARGET=installed ACCEPT_INSTALL_ROOT="$root" ACCEPT_KEEP=1 SIM_ROUTER_NS_PREFIX=hfsim- SIM_ROUTER_IF_PREFIX=hfsim \
    SIM_ROUTER_LINK_NET=169.254.77 SIM_ROUTER_DIR="$ACCEPT_OUT_DIR/sim" GO="$GO" \
    GOLANGCI_LINT="${GOLANGCI_LINT:-golangci-lint}" bash scripts/accept/accept-i1.sh 2>&1 | tee "$out/accept-i1.log"
  accept_rc=${PIPESTATUS[0]}
  SIM_ROUTER_NS_PREFIX=hfsim- SIM_ROUTER_IF_PREFIX=hfsim bash "$sim_router" down-all >/dev/null 2>&1 || true
  if [ "$accept_rc" = 0 ]; then echo "accept-i1 después del caos: OK" >>"$out/report.md"; else echo "accept-i1 después del caos: KO" >>"$out/report.md"; fi
fi

echo
cat "$out/report.md" 2>/dev/null
if [ "$chaos_rc" != 0 ] || [ "$accept_rc" != 0 ]; then
  echo "chaos-restart-core: KO (driver $chaos_rc, accept-i1 $accept_rc)"
  exit 1
fi
echo "chaos-restart-core: OK"
