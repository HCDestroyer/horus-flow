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
| `accept/accept-i0.sh` | `make accept-i0`: batería de aceptación del incremento 0 con resumen OK/FAIL/SKIP por paso ([`tests/acceptance/README.md`](../tests/acceptance/README.md)). I0-19. |
| `accept/accept-i1.sh` | `make accept-i1`: aceptación del incremento 1 sobre una instalación real (`TARGET=local` en un raíz temporal o `TARGET=installed`), con resumen por paso y etapa con su criterio ([`tests/acceptance/README.md`](../tests/acceptance/README.md)). I1-24. |
| `accept/sim-router.sh` | Routers simulados para `accept-i1`: un namespace de red con WireGuard configurado con el script de onboarding de Horus, desde el que se exportan los flujos. I1-24. |
| `accept/image.sh` | `make accept-image`: imagen `horus:accept` con el Dockerfile raíz o, si `docker build` no puede descargar módulos (CA de un proxy), desde el binario del host. I0-19. |
| `lab/lab.sh` | `make lab-up/lab-down/lab-status/lab-traffic/lab-console`: laboratorio MikroTik CHR en QEMU/KVM con clientes en netns, NAT, túnel WireGuard e IPFIX ([`infrastructure/lab/chr/README.md`](../infrastructure/lab/chr/README.md)). I0-11. |
| `lab/selftest.sh` | `make lab-selftest`: validación §8.3 de `vendors/mikrotik.md` en un CHR limpio; salida en `.lab/selftest-<ROS>.log`. I0-11. |
| `lab/fetch-chr.sh`, `lab/render-rsc.py`, `lab/internet-sim.py`, `lab/traffic.py`, `lab/ipfix-probe.py` | Descarga verificada de CHR, render del onboarding §7, servidores y tráfico de prueba, receptor IPFIX mínimo. I0-11. |
| `install.sh` | `make install`: instalador de un servidor (Debian/Ubuntu LTS + Docker). Modos de acceso `domain`/`subdomain` (Let's Encrypt) o `ip` (certificado autogenerado con huella), requisitos, secretos y paquete de secretos offline cifrado, hub WireGuard (`wg0`, kernel o wireguard-go) con el UDP de IPFIX/NetFlow filtrado a esa interfaz, compose de producción healthy, superadmin inicial, primer backup y timers. `--check`, `--uninstall [--purge]`, idempotente ([`deployments/README.md`](../deployments/README.md)). I1-22. |
| `backup/horus-backup.sh` | Se instala como `<instalación>/bin/horus-backup`: pgBackRest (repo1 posix, WAL continuo) y `BACKUP` nativo de ClickHouse al almacén local, retención, restauración de prueba en bases vacías con comparación de huellas, TTL del crudo, métricas `horus_backup_*`/`horus_store_*` y alertas. I1-23. |
| `backup/test-backup.sh` | `make test-backup`: instala en un raíz temporal, siembra datos por la API real, backups, `verify`, TTL, métricas y alertas, y desinstala. I1-23. |
