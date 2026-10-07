# 0011 — Docker Compose antes que Kubernetes

- Estado: Aceptada
- Fecha: 2026-10-07

## Contexto

El MVP (Sprints 1–5) tiene 6 procesos propios más PostgreSQL, Redis, NATS, MinIO y la pila de
observabilidad; hasta ~100 routers cabe en 2 hosts. Kubernetes añade coste operativo (control
plane, CNI, ingress, almacenamiento, upgrades) que un equipo pequeño no debe pagar antes de
necesitarlo, pero la arquitectura debe permitir migrar (`vision.md §5`).

## Decisión

- **Docker + Docker Compose** como entorno de desarrollo y de producción v1.
  - `infrastructure/docker/compose.yaml` base + overrides por entorno (`compose.dev.yaml`,
    `compose.prod.yaml`); perfiles por fase (`core`, `collectors`, `analytics`, `observability`).
  - `restart: unless-stopped`, healthchecks en todos los servicios, límites de CPU/memoria,
    volúmenes nombrados solo para almacenes de datos.
  - Imágenes inmutables por commit; despliegue = `pull` + `up -d` por servicio.
- Desde el Sprint 1 se cumplen las **reglas de portabilidad** de
  [architecture.md §8.2](../architecture.md#82-reglas-que-hacen-posible-la-migración-a-kubernetes)
  (12-factor, probes, SIGTERM, sin estado local, coordinación por NATS KV).
- Se migra a Kubernetes cuando se cumpla algún disparador de
  [architecture.md §8.4](../architecture.md#84-cuándo-migrar) (≈ > 300 routers, HA automática,
  > 3 hosts). Bases de datos se mantienen fuera del clúster al principio.

## Alternativas consideradas

- **Kubernetes desde el inicio (k3s)**: k3s reduce el coste, pero sigue añadiendo conceptos
  (ingress, PV, RBAC, Helm) y complica WireGuard (host network, NET_ADMIN) y UDP de flujos.
- **Docker Swarm**: multi-host simple, pero ecosistema en declive; no es un paso hacia K8s.
- **Nomad**: buena opción intermedia, pero añade otra herramienta que no es el destino final.
- **systemd + binarios**: mínimo coste, pero peor reproducibilidad y aislamiento.

## Consecuencias

- (+) Arranque rápido, entorno local idéntico a producción, depuración simple.
- (−) Sin autoescalado ni failover automático entre hosts; HA limitada a reinicio de contenedores.
- (−) Despliegues con breve corte por servicio (aceptable en v1; ventana de mantenimiento).
- (−) Riesgo de "deriva" que dificulte la migración si no se respetan las reglas de §8.2: se
  verifican en la revisión (Definición de terminado).
