# `mod:devices` — services/devices

- **Propósito:** Inventario del tenant: nodos, routers, credenciales, realms y prefijos de clientes; submódulo customers (cliente = IP).
- **Rol(es) de `horus`:** `devices`
- **Agente dueño:** CORE ([`docs/backlog/team.md`](../../docs/backlog/team.md) §2, [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · Roles y fronteras: [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)
- **Layout y reglas:** [`docs/conventions.md`](../../docs/conventions.md) §2.1 — `api/` (contrato público), `internal/` (`config`, `domain`, `app`, `adapters`), `migrations/` (si tiene esquema PostgreSQL). Otros módulos solo importan `services/devices/api`.

Esqueleto creado en I0-01: todavía sin código. Este README se ampliará con variables de entorno,
métricas y eventos publicados/consumidos cuando el módulo tenga implementación.

## Integraciones de ecosistema (ADR-0030; contrato `docs/integrations/argosisp.md`)

- **Conector ArgosISP** (`internal/app/integrations.go`, `internal/adapters/argos`): URL base y token `atk_…`
  cifrado con la KEK de devices; resolución por lotes `POST /integraciones/horus/resolver` con respaldo
  `GET /ip/:ip` + `/clientes/:id` + `/servicios/:id` si Argos responde 404; ≤ 10 req/s por ISP. Identidad con
  vigencia en `devices.customer_external_identity`; alias `argosisp` que nunca pisa uno `manual`. Disparadores:
  cliente descubierto (cola en proceso), webhook firmado de Argos (`argosisp_webhook_inbox`, deduplicado), barrido
  de identidades de más de 6 h (lotes de 500) y "Actualizar desde ArgosISP". Argos caído: identidad `stale`,
  espera exponencial hasta 15 min; nunca bloquea la ingesta ni la detección.
- **Consulta 360** (`POST /integrations/ip-lookup`, por IP o por `external_service_id`), **consumo por lotes**
  (`POST /integrations/consumption`) y **estado de seguridad por lotes** (`POST /integrations/security-states`)
  sobre los contratos en proceso `analytics.Consumption`, `detection.CustomerFindings`, `detection.SecurityStates` y
  `snmp.RouterStatus`.
- **Multiempresa** (contrato 1.2.0): la identidad guarda la empresa del servicio (`external_company_id`,
  `company_name`, `company_nit`); un webhook de servicio con `empresa_id` la refresca.
- Provee `devices.CustomerDirectory` (cliente e ids de ArgosISP para el canal webhook de alerts).
- Variables: `HORUS_INTEGRATIONS_SWEEP_EVERY` (10 min), `HORUS_INTEGRATIONS_ARGOS_RPS` (10),
  `HORUS_INTEGRATIONS_ALLOW_PRIVATE` (`false`; solo laboratorios: http, privadas y loopback), `HORUS_PUBLIC_BASE_URL`
  (URL del webhook que se copia en Argos).

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
