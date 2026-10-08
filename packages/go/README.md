# packages/go — librerías de plataforma Go

- **Propósito:** `observability`, `authz` (TenantScope), `config`, `natsx` (sobre + `Horus-Tenant`),
  `crypto/envelope`, `httpx`, `grpcx`, `testkit`, `archtest` (tests de arquitectura) y
  `tenanttest` (batería de aislamiento). **Sin lógica de dominio.**
- **Dueño:** CORE (revisa PLAT). Rutas sensibles (persona obligatoria): `authz`, `crypto`, `natsx`,
  `tenanttest`, `archtest`.
- **Documentación:** [`docs/conventions.md`](../../docs/conventions.md) §1–2,
  [`docs/security.md`](../../docs/security.md).
- **Estado:** vacío; lo llena I0-04.
