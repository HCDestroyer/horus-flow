#!/usr/bin/env bash
# accept-i1.sh — batería de aceptación del incremento 1 (`make accept-i1`, historia I1-24).
#
# Demuestra el primer entregable (docs/roadmap.md §3, I1) de punta a punta sobre una instalación
# REAL hecha con scripts/install.sh (compose de producción: TLS, NATS con contraseña, row
# policies, hub WireGuard…), con routers simulados que se conectan por WireGuard de verdad:
#
#   go-build → lint → sim-verify → flows-golden → image → install → install-check → e2e
#   (platform, inventory, reputation, onboarding, tunnel, flows, exporters, customers,
#   real-flows, traffic, findings, websocket, kiosk, isolation, silent) → frontend-build →
#   frontend-e2e → lab-chr
#
# Cada paso escribe su log en bin/accept-i1/<paso>.log. El resumen da OK/FAIL/SKIP por paso con
# las historias y el criterio que comprueba; al fallar, el criterio, la historia y el motivo.
# Sale con 1 si algún paso falla.
#
# Destino (TARGET):
#   local      (defecto) instala Horus con scripts/install.sh en un raíz temporal (no toca /opt,
#              /etc ni /var del host; sí crea una interfaz WireGuard, namespaces de red de los
#              routers simulados y reglas iptables etiquetadas, que se quitan al final) con
#              puertos desplazados (ACCEPT_PORT_OFFSET=21000: HTTPS en :21443) y la imagen
#              construida desde este árbol. Se desinstala al terminar salvo ACCEPT_KEEP=1.
#   installed  usa la instalación existente del servidor (ACCEPT_INSTALL_ROOT, por defecto /:
#              /opt/horus y /etc/horus). Crea ISP de prueba "accept-i1-*" (se suspenden al final),
#              añade los C2 de prueba (IPs de documentación) al snapshot de reputación y conecta
#              los routers simulados al hub. Necesita las credenciales del superadministrador:
#              en una instalación recién hecha basta la contraseña semilla
#              (ACCEPT_ADMIN_PASSWORD_FILE; la batería cambia la contraseña y activa TOTP y lo
#              guarda en bin/accept-i1/admin-creds.json); si ya tiene TOTP, también
#              ACCEPT_ADMIN_TOTP_SECRET_FILE.
#
# Variables:
#   ACCEPT_STEPS=a,b / ACCEPT_SKIP=a,b   pasos a ejecutar / omitir
#   ACCEPT_E2E_STAGES=a,b                solo esas etapas del e2e
#   ACCEPT_KEEP=1                        no desinstala (local) ni borra los routers simulados
#   ACCEPT_PORT_OFFSET (21000), ACCEPT_IMAGE (horus:accept-i1), ACCEPT_IMAGE_MODE (auto)
#   ACCEPT_INSTALL_ROOT, ACCEPT_ADMIN_PASSWORD_FILE, ACCEPT_ADMIN_TOTP_SECRET_FILE, ACCEPT_BASE_URL
#   ACCEPT_PROJECT (horus-accept-i1), ACCEPT_WG_INTERFACE (hfwgacci1), ACCEPT_DOCKER_SUBNET (172.31.241.0/24),
#   ACCEPT_TUNNEL_CIDR (10.241.0.0/16), ACCEPT_OUT_DIR (bin/accept-i1): otra instalación local en paralelo
#   (make chaos-restart-core los cambia para no chocar con esta batería)
#   GOLANGCI_LINT, GO, PLAYWRIGHT_BROWSERS_PATH
#
# Necesita root (instalador, WireGuard, namespaces de red), Docker con compose v2, Go,
# wireguard-tools y el módulo wireguard o wireguard-go.

# Los pasos corren en un subshell con su ACCEPT_NOTE_FILE (como en accept-i0).
# shellcheck disable=SC2030,SC2031
set -uo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$repo_root" || exit 1

out_dir="${ACCEPT_OUT_DIR:-$repo_root/bin/accept-i1}"
mkdir -p "$out_dir/notes"
rm -rf "$out_dir"/*.log "$out_dir"/notes/* "$out_dir"/notes/.installed "$out_dir"/compose-logs.txt "$out_dir"/e2e-report.tsv 2>/dev/null || true

GO="${GO:-go}"
MAKE="${MAKE:-make}"
export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"
target="${TARGET:-local}"
case "$target" in local | installed) ;; *) echo "accept-i1: TARGET=$target desconocido (local|installed)" >&2; exit 2 ;; esac

offset="${ACCEPT_PORT_OFFSET:-21000}"
image="${ACCEPT_IMAGE:-horus:accept-i1}"
project="${ACCEPT_PROJECT:-horus-accept-i1}"
inst_root=""   # raíz de la instalación ("" = /)
if [ "$target" = local ]; then
  inst_root="${ACCEPT_LOCAL_ROOT:-/tmp/horus-accept-i1}"
else
  inst_root="${ACCEPT_INSTALL_ROOT:-}"
  inst_root="${inst_root%/}"
fi
sim_router="$repo_root/scripts/accept/sim-router.sh"
export SIM_ROUTER_DIR="$out_dir/sim"

# --- Registro de pasos (mismo formato que accept-i0) -------------------------------------------
declare -a S_ID S_STORY S_DESC S_RES S_TIME S_NOTE
failed=0

contains() { case ",$1," in *",$2,"*) return 0 ;; *) return 1 ;; esac; }

result_of() {
  local i
  for i in "${!S_ID[@]}"; do
    if [ "${S_ID[$i]}" = "$1" ]; then printf '%s' "${S_RES[$i]}"; return; fi
  done
}

record() { S_ID+=("$1"); S_STORY+=("$2"); S_DESC+=("$3"); S_RES+=("$4"); S_TIME+=("$5"); S_NOTE+=("$6"); }

# step <id> <historias> <descripción> <dependencias,coma> <función>
step() {
  local id="$1" story="$2" desc="$3" deps="$4" fn="$5" dep note_file="$out_dir/notes/$1" start rc note
  if [ -n "${ACCEPT_STEPS:-}" ] && ! contains "$ACCEPT_STEPS" "$id"; then
    record "$id" "$story" "$desc" SKIP 0 "no está en ACCEPT_STEPS"; return
  fi
  if [ -n "${ACCEPT_SKIP:-}" ] && contains "$ACCEPT_SKIP" "$id"; then
    record "$id" "$story" "$desc" SKIP 0 "ACCEPT_SKIP"; return
  fi
  for dep in ${deps//,/ }; do
    if [ "$(result_of "$dep")" = "FAIL" ]; then
      record "$id" "$story" "$desc" SKIP 0 "depende de '$dep', que falló"; return
    fi
  done
  printf '\n\033[1m==> [%s] %s (%s)\033[0m\n' "$id" "$desc" "$story"
  start="$(date +%s)"
  ( export ACCEPT_NOTE_FILE="$note_file"; "$fn" ) >"$out_dir/$id.log" 2>&1
  rc=$?
  note="$(cat "$note_file" 2>/dev/null || true)"
  local elapsed=$(($(date +%s) - start))
  if [ "$rc" -eq 0 ]; then
    record "$id" "$story" "$desc" OK "$elapsed" "$note"
    printf '    OK en %ss%s\n' "$elapsed" "${note:+ — $note}"
  elif [ "$rc" -eq 77 ]; then
    record "$id" "$story" "$desc" SKIP "$elapsed" "$note"
    printf '    SKIP — %s\n' "$note"
  else
    failed=1
    record "$id" "$story" "$desc" FAIL "$elapsed" "${note:-código $rc}"
    printf '    \033[31mFAIL\033[0m en %ss (código %s) — log: %s\n' "$elapsed" "$rc" "${out_dir#"$repo_root"/}/$id.log"
    tail -n 25 "$out_dir/$id.log" | sed 's/^/    | /'
  fi
}

skip() { printf '%s' "$1" >"$ACCEPT_NOTE_FILE"; exit 77; }
note() { printf '%s' "$1" >"$ACCEPT_NOTE_FILE"; }

# Valor de la instalación (.env generado por install.sh; sin secretos).
inst_env() { sed -nE "s/^$1=(.*)$/\1/p" "$inst_root/opt/horus/.env" 2>/dev/null | tail -1; }

# --- Pasos -------------------------------------------------------------------------------------
s_go_build() {
  $MAKE --no-print-directory build && ./bin/horus --version || return 1
  "$GO" build -o bin/flowsim ./tools/flowsim/cmd/flowsim || return 1
  "$GO" build -tags acceptance -o bin/pcapreplay ./tests/acceptance/i1/pcapreplay || return 1
  note "horus, flowsim y pcapreplay"
}

s_lint() {
  export GOLANGCI_LINT_CACHE="${GOLANGCI_LINT_CACHE:-$out_dir/golangci-cache}"
  local lint="${GOLANGCI_LINT:-golangci-lint}"
  command -v "$lint" >/dev/null 2>&1 || [ -x "$lint" ] || skip "sin golangci-lint (GOLANGCI_LINT)"
  "$lint" run --build-tags acceptance ./tests/acceptance/... || return 1
  local sc="koalaman/shellcheck:v0.11.0"
  if command -v shellcheck >/dev/null 2>&1; then
    shellcheck scripts/accept/accept-i1.sh scripts/accept/sim-router.sh || return 1
  elif docker image inspect "$sc" >/dev/null 2>&1; then
    docker run --rm -v "$repo_root/scripts/accept:/s:ro" "$sc" /s/accept-i1.sh /s/sim-router.sh || return 1
  fi
}

s_sim_verify() {
  # Los seis escenarios (y los demás) sin router y la captura real con su expected.json.
  $MAKE --no-print-directory sim-verify
}

s_flows_golden() {
  # Criterio 2 de I1-24: la captura real de MikroTik por collector + ingester contra ClickHouse
  # (testcontainers) da los mismos clientes y flujos que se registraron.
  $MAKE --no-print-directory test-flows-golden
}

s_image() {
  [ "$target" = local ] || skip "TARGET=installed: se usa la imagen de la instalación"
  ACCEPT_IMAGE="$image" GO="$GO" bash scripts/accept/image.sh
}

s_install() {
  if [ "$target" = installed ]; then
    [ -f "$inst_root/opt/horus/.env" ] || { echo "no hay instalación en ${inst_root:-/}opt/horus (TARGET=installed)"; return 1; }
    note "instalación existente: $(inst_env HORUS_PUBLIC_BASE_URL) (proyecto $(inst_env COMPOSE_PROJECT_NAME))"
    return 0
  fi
  [ "$(id -u)" = 0 ] || { echo "TARGET=local necesita root (instalador, WireGuard)"; return 1; }
  if [ -f "$inst_root/opt/horus/.env" ]; then
    bash scripts/install.sh --uninstall --purge --yes --root "$inst_root" >/dev/null 2>&1 || true
  fi
  rm -rf "$inst_root"
  mkdir -p "$inst_root"
  printf 'AcceptI1-%s' "$(openssl rand -hex 12)" >"$inst_root/admin-password"
  chmod 0600 "$inst_root/admin-password"
  rm -f "$out_dir/admin-creds.json"
  touch "$out_dir/notes/.installed" # la limpieza desinstala solo lo que instaló esta ejecución
  bash scripts/install.sh --yes --force --root "$inst_root" --mode ip --public-ip 127.0.0.1 \
    --admin-email admin@horus.test --admin-password-file "$inst_root/admin-password" \
    --tunnel-cidr "${ACCEPT_TUNNEL_CIDR:-10.241.0.0/16}" --image "$image" --project "$project" \
    --http-port $((offset + 80)) --https-port $((offset + 443)) --wg-port $((51820 + offset / 1000)) \
    --wg-interface "${ACCEPT_WG_INTERFACE:-hfwgacci1}" --docker-subnet "${ACCEPT_DOCKER_SUBNET:-172.31.241.0/24}" --tlm-max-bytes 1073741824 \
    --backup-metrics-port $((offset + 9109)) --skip-backup || return 1
  note "install.sh --mode ip en $inst_root, $(inst_env HORUS_PUBLIC_BASE_URL)"
}

s_install_check() {
  bash scripts/install.sh --check ${inst_root:+--root "$inst_root"}
}

s_e2e() {
  local base email pw totp cert=""
  base="${ACCEPT_BASE_URL:-$(inst_env HORUS_PUBLIC_BASE_URL)}"
  [ -n "$base" ] || { echo "no sé la URL pública de la instalación"; return 1; }
  email="${ACCEPT_ADMIN_EMAIL:-$(inst_env HORUS_SEED_ADMIN_EMAIL)}"
  pw="${ACCEPT_ADMIN_PASSWORD_FILE:-}"
  [ -n "$pw" ] || [ "$target" != local ] || pw="$inst_root/admin-password"
  if [ -z "$pw" ] && [ ! -f "$out_dir/admin-creds.json" ]; then
    echo "TARGET=installed necesita ACCEPT_ADMIN_PASSWORD_FILE (y ACCEPT_ADMIN_TOTP_SECRET_FILE si el superadmin ya tiene TOTP)"
    return 1
  fi
  totp="${ACCEPT_ADMIN_TOTP_SECRET_FILE:-}"
  [ "$(inst_env HORUS_TLS_MODE)" = self_signed ] && cert="$(inst_env HORUS_TLS_DIR)/public.crt"
  local proj; proj="$(inst_env COMPOSE_PROJECT_NAME)"
  echo "e2e contra $base (proyecto $proj)"
  env ${cert:+SSL_CERT_FILE="$cert"} ACCEPT_BASE_URL="$base" ACCEPT_ADMIN_EMAIL="$email" \
    ACCEPT_ADMIN_PASSWORD_FILE="$pw" ACCEPT_ADMIN_TOTP_SECRET_FILE="$totp" \
    ACCEPT_STATE_DIR="$out_dir" ACCEPT_REPORT="$out_dir/e2e-report.tsv" \
    ACCEPT_STORE_DIR="$(inst_env HORUS_STORE_DIR)" ACCEPT_SECRETS_DIR="$(inst_env HORUS_SECRETS_DIR)" \
    ACCEPT_CH_CONTAINER="$proj-clickhouse-1" ACCEPT_PG_CONTAINER="$proj-postgres-1" \
    ACCEPT_CH_USER="$(inst_env HORUS_CH_USER)" ACCEPT_PG_USER="$(inst_env HORUS_PG_USER)" ACCEPT_PG_DB="$(inst_env HORUS_PG_DB)" \
    ACCEPT_SIM_ROUTER="$sim_router" ACCEPT_FLOWSIM="$repo_root/bin/flowsim" ACCEPT_PCAPREPLAY="$repo_root/bin/pcapreplay" \
    ACCEPT_SUSPEND_TENANTS="$([ "$target" = installed ] && echo 1 || echo 0)" \
    "$GO" test -tags acceptance -count=1 -v -timeout 45m ./tests/acceptance/i1/
}

s_frontend_build() {
  command -v pnpm >/dev/null 2>&1 || skip "sin pnpm"
  (cd apps/frontend && pnpm install --frozen-lockfile && pnpm build:mocks)
}

s_frontend_e2e() {
  # Pantallas de I1 contra la API simulada: clientes, tráfico, seguridad, onboarding del router,
  # widgets, kiosco y consola de plataforma.
  local port="${ACCEPT_UI_PORT:-$((offset + 4173))}"
  (cd apps/frontend && E2E_NO_BUILD=1 PORT="$port" pnpm exec playwright test \
    --grep '@clients|@traffic|@security|@onboarding|@widgets|@kiosk|@platform' --reporter=list)
}

s_lab_chr() {
  if [ ! -e /dev/kvm ]; then
    skip "sin /dev/kvm: la lista §8.3 en CHR (make lab-selftest, I1-25) necesita KVM; ejecútalo en un host con virtualización"
  fi
  $MAKE --no-print-directory lab-selftest
}

# --- Al salir: logs y limpieza -----------------------------------------------------------------
cleanup() {
  local proj
  proj="$(inst_env COMPOSE_PROJECT_NAME)"
  if [ "$failed" = 1 ] && [ -n "$proj" ]; then
    docker compose -p "$proj" ps -a >"$out_dir/compose-logs.txt" 2>&1 || true
    docker compose -p "$proj" logs --no-color --tail 300 >>"$out_dir/compose-logs.txt" 2>&1 || true
  fi
  if [ "${ACCEPT_KEEP:-0}" = 1 ]; then
    echo "accept-i1: se conservan los routers simulados y la instalación de prueba (ACCEPT_KEEP=1; TARGET=$target, raíz ${inst_root:-/})"
    return
  fi
  bash "$sim_router" down-all >/dev/null 2>&1 || true
  if [ "$target" = local ] && [ -f "$out_dir/notes/.installed" ]; then
    bash scripts/install.sh --uninstall --purge --yes --root "$inst_root" >"$out_dir/uninstall.log" 2>&1 || true
    rm -rf "$inst_root"
  fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# --- Batería -----------------------------------------------------------------------------------
step go-build       "I0-04"             "compilar horus, flowsim y pcapreplay"                       ""             s_go_build
step lint           "I1-24"             "golangci-lint (etiqueta acceptance) y shellcheck"          ""             s_lint
step sim-verify     "I0-10,I0-12"       "simulador: seis escenarios y captura real sin router"       ""             s_sim_verify
step flows-golden   "I1-04,I1-24"       "test dorado de la captura real (collector + ingester + CH)" ""             s_flows_golden
step image          "I0-04"             "imagen única horus"                                         ""             s_image
step install        "I1-22"             "instalación con scripts/install.sh (TARGET=$target)"         "image"        s_install
step install-check  "I1-22"             "install.sh --check: salud de cada rol, puertos, TLS, WireGuard" "install"   s_install_check
step e2e            "I1-01..I1-21"      "demostración de I1 contra la instalación (ver etapas)"     "go-build,install" s_e2e
step frontend-build "I1-16..I1-21"      "build del frontend con la API simulada"                     ""             s_frontend_build
step frontend-e2e   "I1-16..I1-21"      "Playwright de las pantallas de I1 contra la API simulada"   "frontend-build" s_frontend_e2e
step lab-chr        "I1-25"             "lista de validación en MikroTik CHR"                         ""             s_lab_chr

# --- Resumen -----------------------------------------------------------------------------------
printf '\n\033[1mResumen de accept-i1\033[0m (%s, TARGET=%s)\n' "$(git describe --tags --always --dirty 2>/dev/null || echo dev)" "$target"
printf '%-17s %-5s %6s  %-19s %s\n' PASO RES. TIEMPO HISTORIAS NOTA
n_ok=0; n_fail=0; n_skip=0
line() { # line <id> <res> <tiempo> <historias> <nota>
  local color
  case "$2" in
    OK) color=32; n_ok=$((n_ok + 1)) ;;
    FAIL) color=31; n_fail=$((n_fail + 1)) ;;
    *) color=33; n_skip=$((n_skip + 1)) ;;
  esac
  printf '%-17s \033[%sm%-5s\033[0m %5ss  %-19s %s\n' "$1" "$color" "$2" "$3" "$4" "$5"
}
for i in "${!S_ID[@]}"; do
  note="${S_NOTE[$i]}"
  [ "${S_RES[$i]}" = FAIL ] && note="${note:+$note — }log: bin/accept-i1/${S_ID[$i]}.log"
  line "${S_ID[$i]}" "${S_RES[$i]}" "${S_TIME[$i]}" "${S_STORY[$i]}" "$note"
  # Las etapas del e2e (informe del test Go) van debajo, con su criterio.
  if [ "${S_ID[$i]}" = e2e ] && [ -s "$out_dir/e2e-report.tsv" ]; then
    while IFS=$'\t' read -r sid sres stime sstory scrit snote; do
      if [ "$sres" = FAIL ]; then
        line "  $sid" "$sres" "$stime" "$sstory" "CRITERIO: $scrit — $snote"
      else
        line "  $sid" "$sres" "$stime" "$sstory" "$scrit${snote:+ — $snote}"
      fi
    done <"$out_dir/e2e-report.tsv"
  fi
done
printf '\nOK %d · FAIL %d · SKIP %d\n' "$n_ok" "$n_fail" "$n_skip"
if [ "$failed" = 1 ]; then
  [ -f "$out_dir/compose-logs.txt" ] && echo "Logs de los contenedores: bin/accept-i1/compose-logs.txt"
  echo "accept-i1: KO"
  exit 1
fi
echo "accept-i1: OK"
