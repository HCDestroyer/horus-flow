# `mod:devices` — services/devices

- **Propósito:** Inventario del tenant: nodos, routers, credenciales, realms y prefijos de clientes; submódulo customers (cliente = IP).
- **Rol(es) de `horus`:** `devices`
- **Agente dueño:** CORE ([`docs/backlog/team.md`](../../docs/backlog/team.md) §2, [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · Roles y fronteras: [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)
- **Layout y reglas:** [`docs/conventions.md`](../../docs/conventions.md) §2.1 — `api/` (contrato público), `internal/` (`config`, `domain`, `app`, `adapters`), `migrations/` (si tiene esquema PostgreSQL). Otros módulos solo importan `services/devices/api`.

Esqueleto creado en I0-01: todavía sin código. Este README se ampliará con variables de entorno,
métricas y eventos publicados/consumidos cuando el módulo tenga implementación.

## ASN propios (Administración › ASN propios)

- `GET/POST /asn-rules`, `PATCH/DELETE /asn-rules/{id}` (If-Match) y `GET /asn-rules/catalog`: reglas ASN → nombre,
  categoría y servicio del catálogo de plataforma (`services/traffic/api/catalog`) en `devices.asn_rule` (RLS, una viva
  por ASN). Publica `horus.devices.asn_rule.created|updated|deleted` por el outbox; flows (ingester) las aplica al tráfico
  nuevo como `local_override` y analytics las usa en las etiquetas de los tops. Permisos `settings.read` / `settings.manage`.

## Alta de routers e importación del MikroTik (I1-01, I1-02, I1-28)

- `api/onboarding.go` (`devices.Onboarding` en `module.Services`): lo usa `wireguard` para leer routers, proyectar el túnel
  (`tunnel_address`, `wireguard_peer_id`, `onboarding_state`; publica `horus.devices.router.updated` con `tunnel_address` `/32`)
  y emitir las credenciales de solo lectura (SNMPv3 y usuario `horus` de la API) que el script muestra una vez; se guardan
  cifradas con envelope encryption (`HORUS_DEVICES_KEK_FILE`; efímera solo en dev) y AAD ligado a tenant, router y tipo.
- `POST /routers/{id}/prefix-import-preview`: lee por REST (solo GET; `internal/adapters/routeros` rechaza cualquier otro método)
  `/ip/pool`, `/ipv6/pool`, `/ppp/profile`, `/ipv6/dhcp-server` y direcciones de interfaz por el túnel, con huella TLS fijada en el
  primer contacto (auditado); huella distinta → `409 ROUTER_TLS_FINGERPRINT_CHANGED` salvo confirmación explícita
  (`accept_new_tls_fingerprint`, auditada); router caído → `502 ROUTER_UNREACHABLE`. Propone rol, modo y campos IPv6 de la
  enmienda E-IPv6-1 sin aplicar nada. `POST /sites/{id}/client-prefixes/batch` crea la selección todo o nada.
- Variables: `HORUS_DEVICES_KEK_FILE`, `HORUS_WG_TUNNEL_CIDRS` (nunca se proponen), `HORUS_DEVICES_ROUTEROS_BASE_URL`
  (`https://{ip}`), `HORUS_DEVICES_ROUTEROS_TIMEOUT` (20 s).
- Suite `routeros-import`: `go test ./services/devices/internal/adapters/routeros/` (fixtures REST 7.12 y 7.20 en `testdata/`,
  sintéticos con el formato de RouterOS hasta grabarlos en CHR en I1-25; `-update` regenera los golden).
