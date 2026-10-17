# `mod:auth` — services/auth

- **Propósito:** Tenants (ISP), usuarios, membresías, roles, sesiones, tokens por tenant, auditoría.
- **Rol(es) de `horus`:** `auth`
- **Agente dueño:** CORE ([`docs/backlog/team.md`](../../docs/backlog/team.md) §2, [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Ruta sensible:** aprobación de la persona obligatoria ([`docs/conventions.md`](../../docs/conventions.md) §6.2).
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · Roles y fronteras: [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)
- **Layout y reglas:** [`docs/conventions.md`](../../docs/conventions.md) §2.1 — `api/` (contrato público), `internal/` (`config`, `domain`, `app`, `adapters`), `migrations/` (si tiene esquema PostgreSQL). Otros módulos solo importan `services/auth/api`.

Esqueleto creado en I0-01: todavía sin código. Este README se ampliará con variables de entorno,
métricas y eventos publicados/consumidos cuando el módulo tenga implementación.

## Autologin desde ArgosISP (ADR-0030; contrato `docs/integrations/argosisp.md` §1.5, H11)

- Tickets de un uso (60 s, solo SHA-256) en `auth.sso_ticket`, emitidos por devices con `api.SSOTickets` a
  petición del API token del conector (`sso.issue`). Canje público `POST /integrations/argosisp/sso/redeem`
  (`X-Requested-With` + `Origin`): con vinculación, sesión normal del usuario (`amr = [sso]`, 2FA de Horus con
  `/auth/mfa/verify`); sin ella, `link_token` (MAC, 10 min, un uso). Vinculaciones en `auth.sso_link`
  (`/me/sso-links`, exige autenticación posterior al canje y membresía activa; nunca por correo, nunca crea usuarios)
  y gestión por un administrador (`/integrations/argosisp/sso/links`). Fail-closed si `api.SSOConnectors` (devices)
  no está en el proceso.
- `api.IntegrationTokens`: el enlace de conexión (§1.4) emite y revoca el `hf_pat_` de la plantilla ArgosISP a
  nombre de quien generó o canjeó el enlace (si sigue teniendo todos sus permisos). `api.UserProfiles`: correo y
  nombre del usuario para "Ver en ArgosISP".
