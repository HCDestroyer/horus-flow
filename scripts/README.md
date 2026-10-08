# scripts/ — utilidades del repositorio

- **Propósito:** scripts de desarrollo, CI (`scripts/ci/`) y recuperación (`scripts/dr/`).
- **Dueño:** PLAT — [`docs/backlog/team.md`](../docs/backlog/team.md) §2.
- **Documentación:** [`docs/conventions.md`](../docs/conventions.md) §8,
  [`docs/disaster-recovery.md`](../docs/disaster-recovery.md).

| Script | Qué hace |
| --- | --- |
| `check-codeowners.sh` | Falla si una carpeta de primer nivel o un módulo de `services/` no tiene regla en `.github/CODEOWNERS` (o si su bloque no indica el agente dueño). |
| `dev-secrets.sh` | Genera los secretos de desarrollo del compose en `deployments/compose/secrets/` (aleatorios, nunca sobrescribe ni imprime valores). I0-02. |
| `compose-preflight.sh` | Antes de `make up`: crea `.env` desde `.env.example`, genera secretos y falla nombrando la variable obligatoria o el secreto que falte. I0-02. |
| `wait-healthy.sh` | Espera a que todos los contenedores del compose estén `healthy` (job `compose-smoke`). I0-02. |
| `github-settings.sh` | Lo ejecuta la persona: aplica con `gh` ruleset de `main`, auto-merge, etiquetas y variables ([`.github/README.md`](../.github/README.md)). I0-03. |
| `ci/affected.py` | Módulos Go y jobs afectados por un cambio (diff + `go list -deps`). I0-03. |
| `ci/scope_guard.py` | Check `scope-guard`: el agente de la rama solo toca sus rutas según CODEOWNERS. I0-03. |
| `ci/dod.py` | Check `dod`: Definición de Terminado mínima verificada por máquina. I0-03. |
| `ci/review_gates.py` | Checks `agent-review` y `persona-gate`. I0-03. |
| `ci/observability-smoke.sh` | `make observability-smoke`: levanta el perfil `observability`, comprueba que Prometheus raspa la pila, que Grafana tiene datasources sanos y el dashboard con API e Ingesta, y que Loki recibe logs. `OBS_SMOKE_APP=1` incluye los `horus-*`. I0-18. |
