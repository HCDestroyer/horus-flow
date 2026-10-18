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
  sobre los contratos en proceso `analytics.Consumption`, `detection.CustomerFindings`, `detection.SecurityStates`,
  `snmp.RouterStatus`, `analytics.ServingRouter` (router de los flujos más recientes del cliente; si no hay, el principal
  con `source = primary_router`) y `flows.LastFlow` (último flujo del exportador).
- **Multiempresa** (contrato 1.2.0): la identidad guarda la empresa del servicio (`external_company_id`,
  `company_name`, `company_nit`); un webhook de servicio con `empresa_id` la refresca.
- **Enlace de conexión** (contrato §1.4, H10; `internal/app/connection.go`, `internal/adapters/argos/peer.go`):
  `POST /integrations/argosisp/connection-links` (código de un uso, 15 min, solo SHA-256 en
  `devices.argosisp_connection_link`), canje público `POST /integrations/argosisp/conexion/canjear` (idempotente
  con la respuesta cifrada; cualquier otra petición → 410) y canje del enlace de Argos
  `POST /integrations/argosisp/connection-links/redeem`. Emite el `hf_pat_` con `auth.IntegrationTokens`, crea el
  canal webhook hacia Argos con `alerts.IntegrationWebhooks` y deja el conector y el canal **apagados**.
- **Autologin** (contrato §1.5, H11; `internal/app/sso.go`): `POST /integrations/argosisp/sso/tickets` (solo el
  API token del conector, `sso.issue`; ticket de `auth.SSOTickets`), `POST /integrations/argosisp/sso/resolve`
  (destino con el ACL del usuario) y "Ver en ArgosISP" `POST /integrations/argosisp/sso/open` (ticket pedido a Argos
  con el `atk_…`, URL validada). Provee `auth.SSOConnectors` (auth pregunta al canjear si el conector sigue activo).
- Provee `devices.CustomerDirectory` (cliente e ids de ArgosISP para el canal webhook de alerts).
- Variables: `HORUS_INTEGRATIONS_SWEEP_EVERY` (10 min), `HORUS_INTEGRATIONS_ARGOS_RPS` (10),
  `HORUS_INTEGRATIONS_ALLOW_PRIVATE` (`false`; solo laboratorios: http, privadas y loopback), `HORUS_PUBLIC_BASE_URL`
  (URL del webhook que se copia en Argos).

## ASN propios (Administración › ASN propios)

- `GET/POST /asn-rules`, `PATCH/DELETE /asn-rules/{id}` (If-Match) y `GET /asn-rules/catalog`: reglas ASN → nombre,
  categoría y servicio del catálogo de plataforma (`services/traffic/api/catalog`) en `devices.asn_rule` (RLS, una viva
  por ASN). Publica `horus.devices.asn_rule.created|updated|deleted` por el outbox; flows (ingester) las aplica al tráfico
  nuevo como `local_override` y analytics las usa en las etiquetas de los tops. Permisos `settings.read` / `settings.manage`.

## Transporte y nivel de protección (ADR-0031)

- `connection_mode`: `wireguard` | `ipsec` | `gre` | `direct` (MikroTik: los tres primeros; Huawei: los tres últimos).
- `protection_level`: `encrypted` | `unencrypted_private` | `unencrypted_internet` (migración: los directos existentes → `unencrypted_private`).
- `PUT /routers/{id}/transport` (`devices.credentials.write`, reauth ≤ 5 min): `tunnel_remote_address`, `tunnel_remote_id`, `path`;
  sin cifrado por Internet exige `confirm_unencrypted_internet` (`422 UNENCRYPTED_TRANSPORT_CONFIRMATION_REQUIRED`); auditado
  `devices.router.transport_changed`. La PSK IPsec es la credencial `ipsec_psk` (envelope, KEK de devices) que gestiona
  `api.TunnelSecrets` para `wireguard`. Detalle: [`docs/tunnels.md`](../../docs/tunnels.md).

## Alta de routers e importación del MikroTik (I1-01, I1-02, I1-28)

- `api/onboarding.go` (`devices.Onboarding` en `module.Services`): lo usa `wireguard` para leer routers, proyectar el túnel
  (`tunnel_address`, `wireguard_peer_id`, `onboarding_state`; publica `horus.devices.router.updated` con `tunnel_address` `/32`)
  y emitir las credenciales de solo lectura (SNMPv3 y usuario `horus` de la API) que el script muestra una vez; se guardan
  cifradas con envelope encryption (`HORUS_DEVICES_KEK_FILE`; efímera solo en dev) y AAD ligado a tenant, router y tipo.
- `POST /routers/{id}/prefix-import-preview`: lee por REST (solo GET; `internal/adapters/routeros` rechaza cualquier otro método)
  `/ip/pool`, `/ipv6/pool`, `/ppp/profile`, `/ipv6/dhcp-server` y direcciones de interfaz por el túnel, solo con la huella TLS
  confirmada: sin confirmar → `409 ROUTER_TLS_FINGERPRINT_UNCONFIRMED` (la captura queda pendiente, sin enviar la contraseña);
  distinta de la fijada → `409 ROUTER_TLS_FINGERPRINT_CHANGED` (queda pendiente); `accept_new_tls_fingerprint` está obsoleto y no
  fija nada; router caído → `502 ROUTER_UNREACHABLE`. Propone rol, modo y campos IPv6 de la
  enmienda E-IPv6-1 sin aplicar nada. `POST /sites/{id}/client-prefixes/batch` crea la selección todo o nada.
- Huella TLS del router (`internal/app/tlspin.go`, `internal/adapters/routeros/tlsprobe.go`): `GET /routers/{id}/tls-fingerprint`
  (`devices.read`), `POST …/capture` (`devices.update`; solo el saludo TLS, deja la huella **pendiente**) y `POST …/confirm`
  (`devices.credentials.write`, re-autenticación; debe coincidir con la pendiente, CAS en BD). Columnas `tls_fingerprint_pending*` y
  `tls_fingerprint_confirmed_*` de `devices.credential`. Auditoría: `devices.router.tls_fingerprint_observed|changed|confirmed|repinned`.
  Una huella distinta en segundo plano detiene las lecturas y avisa en la ficha; nunca se acepta sola.
- Sondas netwatch para el rol `snmp` (capacidad de enlaces): `devapi.RouterProbeReader` (`devices.RouterProbes`, en
  proceso) lee `/tool/netwatch` con el mismo usuario de solo lectura, solo GET y **solo con la huella TLS ya fijada** (en
  segundo plano nunca se acepta una huella nueva); sin túnel, credencial o huella → `ErrRouterAPIUnavailable`.
- Variables: `HORUS_DEVICES_KEK_FILE`, `HORUS_WG_TUNNEL_CIDRS` (nunca se proponen), `HORUS_DEVICES_ROUTEROS_BASE_URL`
  (`https://{ip}`), `HORUS_DEVICES_ROUTEROS_TIMEOUT` (20 s).
- **Huawei VRP en modo directo** ([`docs/vendors/huawei.md`](../../docs/vendors/huawei.md)): `vendor` `mikrotik|huawei`,
  `connection_mode` `wireguard|direct` (Huawei siempre `direct`) y `management_address` (IP de origen vista por Horus).
  `POST /routers/{id}/direct-onboarding` (`devices.credentials.write`, re-autenticación, auditado
  `devices.direct_onboarding.created`) fija la dirección, emite una credencial SNMPv3 nueva (SHA-256/AES-128, sin usuario
  de la API) y devuelve la configuración VRP que pega el operador (`internal/domain/vrp.go`, pendiente de validación en
  equipo real). `devices.DirectVerifier` (rol `snmp`): primera respuesta SNMPv3 authPriv en esa dirección →
  `onboarding_state = snmp_verified` (prueba de control; solo entonces el colector acepta sus flujos; dirección verificada
  única en la plataforma, `MANAGEMENT_ADDRESS_IN_USE`). La API REST de RouterOS (importación, huella TLS, credencial
  `routeros_api`) responde `422 ROUTER_VENDOR_NOT_SUPPORTED` con un Huawei. Variables: `HORUS_DEVICES_DIRECT_COLLECTOR_ADDRESS`,
  `HORUS_DEVICES_DIRECT_COLLECTOR_PORT` (2055), `HORUS_DEVICES_DIRECT_POLLER_ADDRESS`, `HORUS_DEVICES_DIRECT_NTP_SERVER`
  (vacías = marcadores en la configuración).
- Suite `routeros-import`: `go test ./services/devices/internal/adapters/routeros/` (fixtures REST 7.12 y 7.20 en `testdata/`,
  sintéticos con el formato de RouterOS hasta grabarlos en CHR en I1-25; `-update` regenera los golden).
