# `.github/` — CI, puertas de revisión y protección de `main`

Historia **I0-03** ([`docs/backlog/increment-0.md`](../docs/backlog/increment-0.md)). Base:
[`docs/conventions.md`](../docs/conventions.md) §6, §8 y §11,
[ADR-0028](../docs/adr/0028-flujo-de-trabajo-de-agentes-de-ia.md),
[`docs/backlog/team.md`](../docs/backlog/team.md) §5. Ruta **sensible**: la persona aprueba los
cambios de esta carpeta.

## 1. Qué hay aquí

| Archivo | Qué hace |
| --- | --- |
| `workflows/ci.yml` | `changes` → `lint` → `unit` (matriz por módulo) → `integration` → `contracts` → `security` → `build` (+ `frontend`, `compose-smoke`) → `dod` → `ci-ok`. Solo sobre lo afectado. |
| `workflows/pr-gates.yml` | `label` (etiquetador), `scope-guard`, `agent-review`, `persona-gate`. |
| `workflows/auto-merge.yml` | Activa el auto-merge en cada PR no borrador de una rama del repo. |
| `labeler.yml` | Etiquetas por ruta: `area:security`, `area:sensitive`, `contract:*` y las informativas. |
| `pull_request_template.md` | Plantilla de PR (sin casillas de DoD: las comprueba `dod`). |
| `agent-review.md` | Lista fija del agente revisor. |
| `CODEOWNERS` | Dueño por carpeta y rutas sensibles (I0-01). |

Scripts de CI en [`scripts/ci/`](../scripts/ci/): `affected.py` (módulos afectados),
`scope_guard.py`, `dod.py`, `review_gates.py`. Los de `pr-gates.yml` se ejecutan **desde la rama
base**, no desde el PR: un PR no puede relajar sus propias puertas.

## 2. Checks y cuándo corren

| Check | Workflow | Qué comprueba | Se salta cuando |
| --- | --- | --- | --- |
| `changes` | ci | `git diff` + `go list -deps` → unidades Go afectadas y flags (frontend, compose, docker, contratos). `go.mod`, `.github/`, `infrastructure/`, `packages/events|protobuf/`, `scripts/ci/`, `Dockerfile` → todo | — |
| `lint` | ci | `check-codeowners`, `go mod tidy -diff`, golangci-lint v2, actionlint, zizmor, hadolint, shellcheck, `docker compose config` | golangci-lint, si no hay Go afectado |
| `unit (<módulo>)` | ci | `go test -race -shuffle=on` con cobertura, una celda por módulo afectado (`services/<m>`, `packages/go/<lib>`, `tools/<t>`); `archtest` y `tenanttest` siempre | sin Go afectado |
| `integration` | ci | `go test -tags=integration` (testcontainers, Docker del runner) | sin Go afectado o sin tests `integration` todavía |
| `contracts` | ci | `make contracts-check` (I0-05, INT) | sin cambios de contrato o el objetivo aún no existe |
| `security` | ci | gitleaks (commits del cambio), govulncheck, trivy fs (vuln, secretos, misconfig; HIGH/CRITICAL) | govulncheck, sin Go |
| `build` | ci | `make build`, imagen única `horus` (buildx, caché GHA, sin push), smoke no-root, trivy image | sin cambios de Go/Dockerfile |
| `frontend` | ci | pnpm install, lint, typecheck, test, build en `apps/frontend` | hasta que exista `apps/frontend/package.json` (I0-14) |
| `landing-motion` | ci | pnpm install y `pnpm typecheck` del proyecto Remotion de `apps/landing-motion` (el render no va en CI: los vídeos se commitean) | sin cambios en `apps/landing-motion/` |
| `compose-smoke` | ci | `make up && scripts/wait-healthy.sh && make down` (I0-02) | sin cambios de compose/infra, salvo en `merge_group` |
| `dod` | ci | Resultado de los jobs anteriores, título Conventional Commits, TODO con tarea, README de módulo, nombre de migraciones, aviso de dependencia sin ADR | — |
| `ci-ok` | ci | Agregador: todo `success` o `skipped` | — |
| `scope-guard` | pr-gates | La rama `<agente>/<ID>-slug` solo toca rutas de su agente (bloques `# Agente:` de CODEOWNERS); comunes: `go.mod`, `go.sum`, `docs/open-questions/` | etiqueta `scope-extended`, rama sin prefijo de agente, `merge_group` |
| `agent-review` | pr-gates | Aprobación de un revisor de `AGENT_REVIEWER_LOGINS` distinto del autor; "cambios pedidos" bloquea | `merge_group` |
| `persona-gate` | pr-gates | Con `contract:*`, `area:security` o `area:sensitive`: falla y pone `needs:persona` hasta que apruebe alguien de `PERSONA_LOGINS` | `merge_group` |

`agent-review` se publica como check de Actions a partir de la revisión de GitHub del agente
revisor (evento `pull_request_review`). Cuando exista la GitHub App del revisor, basta con poner su
identidad en `AGENT_REVIEWER_LOGINS`; si en el futuro la App publica el check directamente, se
desactiva el job y se mantiene el nombre `agent-review` como check requerido.

## 3. Ajustes de GitHub (los aplica la persona)

Ningún agente puede cambiar reglas de protección. La persona ejecuta, con permisos de admin:

```bash
gh auth login                          # cuenta de la persona
scripts/github-settings.sh --dry-run   # revisa lo que va a hacer
scripts/github-settings.sh --set-default-branch
```

`--set-default-branch` pone `main` como rama por defecto (hoy no lo es). El script es idempotente
y aplica exactamente esto:

**Repositorio** (Settings → General → Pull Requests)

- Solo **squash merge**; título del commit = título del PR, cuerpo = descripción del PR.
- **Allow auto-merge**: sí. **Automatically delete head branches**: sí. **Always suggest updating
  pull request branches**: sí.

**Ruleset `main`** (Settings → Rules → Rulesets), objetivo `refs/heads/main`, activo, sin bypass:

| Regla | Valor |
| --- | --- |
| Restrict deletions | sí |
| Block force pushes | sí |
| Require linear history | sí |
| Require a pull request before merging | 0 aprobaciones humanas obligatorias (la revisión es `agent-review` + `persona-gate`); descartar aprobaciones al hacer push; conversaciones resueltas; método permitido: squash; revisión de CODEOWNERS: **no** de momento (ver abajo) |
| Require status checks to pass | `ci-ok`, `dod`, `scope-guard`, `agent-review`, `persona-gate` (origen: GitHub Actions). "Require branches to be up to date": **sí si no hay merge queue**, no con merge queue |
| Require signed commits | sí (los squash de GitHub van firmados; la App de los agentes firma) |
| Require merge queue | squash, `ALLGREEN`, hasta 5 entradas, espera 1 min, timeout de checks 60 min — **solo si GitHub lo permite** |

**Variables de Actions** (Settings → Secrets and variables → Actions → Variables)

| Variable | Valor | Uso |
| --- | --- | --- |
| `PERSONA_LOGINS` | `hcdestroyer` (por defecto, el dueño) | Quién desbloquea `persona-gate` |
| `AGENT_REVIEWER_LOGINS` | identidad(es) del revisor, p. ej. `horus-reviewer[bot]` | Quién cuenta para `agent-review`. Vacía = transitorio: cualquier revisor distinto del autor |
| `MERGE_QUEUE` | `true`/`false` (lo fija el script) | `auto-merge.yml` omite el método si hay cola |

**Secreto opcional** `AUTOMERGE_TOKEN`: token de una GitHub App (o PAT fino) con *Contents* y
*Pull requests* de escritura. Sin él, `auto-merge.yml` usa `GITHUB_TOKEN` y GitHub **no dispara**
los workflows de `push` a `main` del merge resultante (limitación documentada de GitHub). Hoy solo
`ci.yml` escucha `push` a `main`; cuando llegue `release.yml` (release-please) el secreto pasa a
ser necesario.

### Limitaciones conocidas y decisiones

- **Merge queue:** GitHub solo la ofrece en repositorios de **organización** (y en privados, con
  Enterprise). El repositorio es de un usuario, así que el script prueba a activarla y, si GitHub
  la rechaza, deja el ruleset sin cola con "branches up to date" obligatorio: cada PR se prueba
  rebasado sobre `main` antes de fusionar (mismo objetivo, menos paralelismo). Los workflows ya
  escuchan `merge_group`, así que mover el repo a una organización solo requiere volver a ejecutar
  el script.
- **Revisión de CODEOWNERS desactivada:** hoy `CODEOWNERS` asigna `@hcdestroyer` a **todas** las
  rutas (no hay equipos por agente), así que exigirla obligaría a la persona a aprobar cada PR y
  rompería D7. Las rutas sensibles quedan cubiertas por `persona-gate` vía `area:security`,
  `area:sensitive` y `contract:*` (ver `labeler.yml`, que replica los bloques `[SENSIBLE]`).
  Cuando existan los equipos `@hcdestroyer/agent-*` y las rutas no sensibles apunten a ellos:
  `REQUIRE_CODEOWNERS=true scripts/github-settings.sh`.
- **Identidades:** autor y revisor deben ser cuentas distintas (GitHub no deja aprobar el propio
  PR). Mientras todos los agentes usen la cuenta de la persona, `agent-review` solo se puede
  satisfacer con otra cuenta; crear la GitHub App de los agentes y la del revisor es un paso
  manual pendiente de la persona.
- **PRs desde forks:** el token es de solo lectura; `persona-gate` no puede poner `needs:persona`
  (lo avisa) y `auto-merge.yml` no actúa. No se esperan forks.

## 4. Cómo se cumple cada criterio de I0-03

| Criterio | Dónde |
| --- | --- |
| 1. lint → unit → integración → contratos → seguridad → build sobre lo afectado | `ci.yml` + `scripts/ci/affected.py` |
| 2. gitleaks falla con un secreto | `ci.yml` job `security` (gitleaks sobre los commits del PR, más trivy secret) |
| 3. CI verde + aprobación de agente, sin `contract:*`/`area:security` → cola automática | `auto-merge.yml` + checks requeridos del ruleset |
| 4. Con `contract:*` o `area:security` → espera a la persona y recibe `needs:persona` | `pr-gates.yml` job `persona-gate` (check requerido) |
| 5. `mod:auth`, autorización, `mod:wireguard`, `.github/workflows/` → `area:security` | `labeler.yml` |
| 6. Pipeline sin caché < 15 min | jobs en paralelo tras `changes`, `timeout-minutes` ≤ 20 por job; medir en el primer PR |

**Hecho cuando** (lo hace la persona tras aplicar los ajustes): abrir tres PRs de prueba —
normal (p. ej. un cambio en `services/cmd/horus/README.md` desde `plat/...`), con un secreto
falso (debe fallar `security`) y uno que toque `packages/schemas/openapi/` (debe recibir
`contract:openapi` y `needs:persona` y no fusionarse sin su aprobación) — y adjuntar la captura del
ruleset al PR de I0-03.
