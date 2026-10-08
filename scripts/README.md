# scripts/ — utilidades del repositorio

- **Propósito:** scripts de desarrollo, CI (`scripts/ci/`) y recuperación (`scripts/dr/`).
- **Dueño:** PLAT — [`docs/backlog/team.md`](../docs/backlog/team.md) §2.
- **Documentación:** [`docs/conventions.md`](../docs/conventions.md) §8,
  [`docs/disaster-recovery.md`](../docs/disaster-recovery.md).

| Script | Qué hace |
| --- | --- |
| `check-codeowners.sh` | Falla si una carpeta de primer nivel o un módulo de `services/` no tiene regla en `.github/CODEOWNERS` (o si su bloque no indica el agente dueño). |
