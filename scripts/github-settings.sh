#!/usr/bin/env bash
# github-settings.sh — aplica con `gh` la protección de `main`, el auto-merge y las etiquetas
# que exige la CI de Horus Flow (historia I0-03). Documentación: .github/README.md.
#
# Lo ejecuta la PERSONA (necesita permisos de administración del repositorio); los agentes no
# cambian reglas de protección (docs/conventions.md §6.1). Es idempotente: crea o actualiza.
#
# Qué hace:
#   1. Ajustes del repositorio: solo squash merge (título = título del PR), auto-merge activado,
#      borrar ramas al fusionar, botón "Update branch".
#   2. Etiquetas: area:*, contract:*, needs:persona, scope-extended, no-automerge, ...
#   3. Variables de Actions: PERSONA_LOGINS, AGENT_REVIEWER_LOGINS, MERGE_QUEUE.
#   4. Ruleset "main" sobre refs/heads/main: PR obligatorio, checks requeridos (ci-ok, dod,
#      scope-guard, agent-review, persona-gate), historial lineal, sin force-push ni borrado,
#      conversaciones resueltas, commits firmados y merge queue (si el plan lo permite).
#
# Uso: scripts/github-settings.sh [--repo dueño/nombre] [--dry-run] [--set-default-branch]
# Variables (opcionales):
#   PERSONA_LOGINS=hcdestroyer            quién es "la persona" (persona-gate)
#   AGENT_REVIEWER_LOGINS=bot1[bot],...   identidades del agente revisor (agent-review)
#   MERGE_QUEUE=auto|true|false           auto = intenta activarla y, si GitHub la rechaza
#                                         (repos de usuario), sigue sin ella (por defecto auto)
#   REQUIRE_CODEOWNERS=false|true         exigir revisión de CODEOWNERS (ver .github/README.md)
#   REQUIRE_SIGNED_COMMITS=true|false     exigir commits firmados en main (por defecto true)

set -euo pipefail

repo=""
dry_run=0
set_default=0
while [ $# -gt 0 ]; do
  case "$1" in
    --repo) repo="$2"; shift 2 ;;
    --dry-run) dry_run=1; shift ;;
    --set-default-branch) set_default=1; shift ;;
    -h | --help) sed -n '2,29p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "uso: $0 [--repo dueño/nombre] [--dry-run] [--set-default-branch]" >&2; exit 2 ;;
  esac
done

command -v gh >/dev/null || { echo "falta gh (https://cli.github.com)" >&2; exit 1; }
command -v jq >/dev/null || { echo "falta jq" >&2; exit 1; }
[ -n "$repo" ] || repo="$(gh repo view --json nameWithOwner -q .nameWithOwner)"

owner="${repo%%/*}"
PERSONA_LOGINS="${PERSONA_LOGINS:-$owner}"
AGENT_REVIEWER_LOGINS="${AGENT_REVIEWER_LOGINS:-}"
MERGE_QUEUE="${MERGE_QUEUE:-auto}"
REQUIRE_CODEOWNERS="${REQUIRE_CODEOWNERS:-false}"
REQUIRE_SIGNED_COMMITS="${REQUIRE_SIGNED_COMMITS:-true}"
GITHUB_ACTIONS_APP_ID=15368 # integration_id de GitHub Actions (origen de los checks)

api() { # api MÉTODO RUTA [json]
  if [ "$dry_run" -eq 1 ] && [ "$1" != GET ]; then
    echo "[dry-run] gh api -X $1 $2 ${3:+<<< $3}" >&2
    return 0
  fi
  if [ $# -ge 3 ]; then
    gh api -X "$1" "$2" -H "X-GitHub-Api-Version: 2022-11-28" --input - <<<"$3"
  else
    gh api -X "$1" "$2" -H "X-GitHub-Api-Version: 2022-11-28"
  fi
}

echo "== Repositorio $repo"

# --- 1. Ajustes del repositorio ----------------------------------------------------------------
api PATCH "/repos/$repo" "$(jq -n '{
  allow_squash_merge: true, allow_merge_commit: false, allow_rebase_merge: false,
  squash_merge_commit_title: "PR_TITLE", squash_merge_commit_message: "PR_BODY",
  allow_auto_merge: true, delete_branch_on_merge: true, allow_update_branch: true
}')" >/dev/null
echo "ok  ajustes: solo squash, auto-merge, borrar ramas al fusionar"

if [ "$set_default" -eq 1 ]; then
  api PATCH "/repos/$repo" '{"default_branch":"main"}' >/dev/null
  echo "ok  rama por defecto: main"
fi

# --- 2. Etiquetas ------------------------------------------------------------------------------
labels='[
  ["area:security","b60205","Auth, autorización, cifrado, WireGuard o workflows: aprueba la persona"],
  ["area:sensitive","d93f0b","Ruta [SENSIBLE] de CODEOWNERS: aprueba la persona"],
  ["contract:openapi","5319e7","Cambio de contrato OpenAPI (C5)"],
  ["contract:events","5319e7","Cambio de contrato de eventos (C4/C8)"],
  ["contract:protobuf","5319e7","Cambio de contrato Protobuf"],
  ["contract:schemas","5319e7","Cambio de esquemas de dashboard/hallazgo (C8/C9)"],
  ["contract:clickhouse","5319e7","Cambio del DDL de referencia de ClickHouse (C3)"],
  ["needs:persona","fbca04","Espera la revisión de la persona"],
  ["scope-extended","c2e0c6","Autoriza tocar rutas fuera del agente (persona o coordinador)"],
  ["no-automerge","000000","No activar auto-merge en este PR"],
  ["architecture","0e8a16","Decisión de arquitectura (exige ADR)"],
  ["new-failure-mode","e99695","Añade un modo de fallo: exige alertas y runbook"],
  ["e2e","1d76db","Ejecuta los e2e en el PR"],
  ["contracts-v0","5319e7","Congelación de contratos v0 (gate G0)"],
  ["area:infra","bfdadc","Infraestructura y CI"],
  ["area:backend","bfdadc","Backend Go"],
  ["area:data","bfdadc","Datos de tráfico"],
  ["area:frontend","bfdadc","Frontend"],
  ["area:docs","bfdadc","Documentación"]
]'
existing="$(api GET "/repos/$repo/labels?per_page=100" | jq -r '.[].name')"
while IFS=$'\t' read -r name color desc; do
  body="$(jq -n --arg n "$name" --arg c "$color" --arg d "$desc" '{name:$n,color:$c,description:$d}')"
  if printf '%s\n' "$existing" | grep -Fxq -- "$name"; then
    api PATCH "/repos/$repo/labels/$(jq -rn --arg n "$name" '$n|@uri')" "$body" >/dev/null
  else
    api POST "/repos/$repo/labels" "$body" >/dev/null
  fi
done < <(jq -r '.[] | @tsv' <<<"$labels")
echo "ok  etiquetas ($(jq length <<<"$labels"))"

# --- 3. Variables de Actions ---------------------------------------------------------------------
set_var() {
  local body
  body="$(jq -n --arg n "$1" --arg v "$2" '{name:$n,value:$v}')"
  if api GET "/repos/$repo/actions/variables/$1" >/dev/null 2>&1; then
    api PATCH "/repos/$repo/actions/variables/$1" "$body" >/dev/null
  else
    api POST "/repos/$repo/actions/variables" "$body" >/dev/null
  fi
}
set_var PERSONA_LOGINS "$PERSONA_LOGINS"
[ -n "$AGENT_REVIEWER_LOGINS" ] && set_var AGENT_REVIEWER_LOGINS "$AGENT_REVIEWER_LOGINS"
echo "ok  variables: PERSONA_LOGINS=$PERSONA_LOGINS AGENT_REVIEWER_LOGINS=${AGENT_REVIEWER_LOGINS:-<vacía: transitorio>}"

# --- 4. Ruleset de main ------------------------------------------------------------------------
ruleset() { # ruleset true|false  → JSON del ruleset con o sin merge queue
  jq -n \
    --argjson mq "$1" \
    --argjson codeowners "$REQUIRE_CODEOWNERS" \
    --argjson signed "$REQUIRE_SIGNED_COMMITS" \
    --argjson app "$GITHUB_ACTIONS_APP_ID" '
  {
    name: "main",
    target: "branch",
    enforcement: "active",
    conditions: { ref_name: { include: ["refs/heads/main"], exclude: [] } },
    bypass_actors: [],
    rules: (
      [
        { type: "deletion" },
        { type: "non_fast_forward" },
        { type: "required_linear_history" },
        { type: "pull_request", parameters: {
            required_approving_review_count: 0,
            dismiss_stale_reviews_on_push: true,
            require_code_owner_review: $codeowners,
            require_last_push_approval: false,
            required_review_thread_resolution: true,
            allowed_merge_methods: ["squash"] } },
        { type: "required_status_checks", parameters: {
            strict_required_status_checks_policy: ($mq | not),
            do_not_enforce_on_create: false,
            required_status_checks: [
              { context: "ci-ok",        integration_id: $app },
              { context: "dod",          integration_id: $app },
              { context: "scope-guard",  integration_id: $app },
              { context: "agent-review", integration_id: $app },
              { context: "persona-gate", integration_id: $app } ] } }
      ]
      + (if $signed then [{ type: "required_signatures" }] else [] end)
      + (if $mq then [{ type: "merge_queue", parameters: {
            merge_method: "SQUASH",
            grouping_strategy: "ALLGREEN",
            max_entries_to_build: 5,
            min_entries_to_merge: 1,
            max_entries_to_merge: 5,
            min_entries_to_merge_wait_minutes: 1,
            check_response_timeout_minutes: 60 } }] else [] end)
    )
  }'
}

rs_id="$(api GET "/repos/$repo/rulesets?per_page=100" | jq -r '.[] | select(.name == "main") | .id' | head -1)"
apply_ruleset() {
  if [ -n "$rs_id" ]; then
    api PUT "/repos/$repo/rulesets/$rs_id" "$1" >/dev/null
  else
    api POST "/repos/$repo/rulesets" "$1" >/dev/null
  fi
}

mq_on=false
case "$MERGE_QUEUE" in
  true) apply_ruleset "$(ruleset true)"; mq_on=true ;;
  false) apply_ruleset "$(ruleset false)" ;;
  auto)
    if apply_ruleset "$(ruleset true)" 2>/dev/null; then
      mq_on=true
    else
      echo "aviso: GitHub rechazó la merge queue (solo existe en repos de organización con el plan" \
        "adecuado); se aplica el ruleset sin ella y con 'branch up to date' obligatorio" >&2
      apply_ruleset "$(ruleset false)"
    fi
    ;;
  *) echo "MERGE_QUEUE debe ser auto, true o false" >&2; exit 2 ;;
esac
set_var MERGE_QUEUE "$mq_on"
echo "ok  ruleset 'main': checks ci-ok, dod, scope-guard, agent-review, persona-gate; merge queue=$mq_on;" \
  "codeowners=$REQUIRE_CODEOWNERS; commits firmados=$REQUIRE_SIGNED_COMMITS"

echo "Hecho. Haz una captura de Settings → Rules → Rulesets → main y adjúntala al PR de I0-03."
