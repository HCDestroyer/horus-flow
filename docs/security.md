# Horus Flow — Seguridad

> Estado: borrador Sprint 0 · Responsable: Agente 4 (seguridad, observabilidad, plataforma).
> Fuente: [`vision.md`](vision.md) §7. Documentos relacionados: [`architecture.md`](architecture.md),
> [`services.md`](services.md), [`database.md`](database.md), [`api.md`](api.md),
> [`events.md`](events.md), [`storage.md`](storage.md), [`observability.md`](observability.md),
> [`disaster-recovery.md`](disaster-recovery.md), [`conventions.md`](conventions.md).
> Preguntas abiertas: [`open-questions/security-ops.md`](open-questions/security-ops.md).

Horus Flow ve **toda la red de un ISP**: credenciales de administración de los routers, claves
WireGuard que dan acceso a la red de gestión, y metadatos de tráfico de cada abonado. Un
compromiso de la plataforma equivale a un compromiso de la red del ISP y a una filtración masiva
de datos personales. Este documento fija el modelo de amenazas y los controles mínimos.

## 0. Resumen de decisiones

| # | Tema | Decisión v1 | Evolución |
|---|------|-------------|-----------|
| S1 | Proveedor de identidad | Servicio `auth` propio en Go; modelo de identidad compatible con OIDC (claims `sub`, `amr`, `acr`, `sid`). Endpoints de OP (OIDC Provider) con la librería certificada `zitadel/oidc` solo cuando haya un cliente externo (p. ej. SSO a Grafana). | Federación con IdP corporativo del ISP (Entra ID / Google Workspace) como *relying party*. Keycloak/Zitadel solo si aparecen requisitos multi-tenant/SAML. |
| S2 | Contraseñas | Argon2id `m=64 MiB, t=3, p=1`, sal 16 B, salida 32 B, formato PHC; *pepper* opcional vía HMAC. | Recalibrar en Sprint 15 (objetivo 150–400 ms por hash). |
| S3 | 2FA | TOTP (RFC 6238) obligatorio para roles privilegiados; 10 códigos de recuperación. | WebAuthn/passkeys (después de Sprint 2). |
| S4 | Sesión de navegador | **Sesión opaca** en cookie `__Host-hf_sid` (HttpOnly, Secure, SameSite=Lax), patrón BFF en el `api-gateway`. Fuente de verdad en PostgreSQL, caché en Redis. | Igual. |
| S5 | Identidad interna | El gateway intercambia la sesión por un **JWT interno** EdDSA (Ed25519) de 5 min que viaja por gRPC (`authorization`). Los eventos NATS llevan `actor`, nunca tokens. | mTLS entre servicios al pasar a multi-host/Kubernetes. |
| S6 | Refresh tokens | Solo para clientes no-navegador (CLI, integraciones): refresh rotativo con detección de reutilización por familia. | — |
| S7 | Autorización | RBAC + ACL por alcance (`global`, `site`, `router_group`). Gateway: autenticación + permiso grueso por ruta. Servicio dueño: permiso + filtrado por alcance (defensa en profundidad). | ABAC puntual si hace falta. |
| S8 | Auditoría | Tabla append-only en PostgreSQL con cadena de hashes; escritura vía outbox + NATS hacia `auth`; anclaje diario del hash en MinIO con Object Lock. | Exportación a SIEM. |
| S9 | Secretos de routers/WireGuard | *Envelope encryption* AES-256-GCM con AAD; KEK en v1 = clave maestra en archivo (Docker secret); solo `devices` y `wireguard` la cargan. | OpenBao (Transit) como KMS en Sprint 14–16. SOPS+age para secretos de despliegue desde Sprint 1. |
| S10 | Cadena de suministro | govulncheck, osv-scanner, Trivy, gitleaks, Syft (SBOM), cosign keyless + attestations de GitHub, Renovate, Actions fijadas por SHA. | SLSA nivel 3. |

## 1. Activos y actores

### 1.1 Activos (por criticidad)

| Activo | Dónde vive | Criticidad | Impacto si se compromete |
|--------|-----------|------------|--------------------------|
| Credenciales de routers (SNMP v2c/v3, SSH/API) | PostgreSQL (`devices`), cifradas | Crítica | Control total de la red del ISP |
| Claves privadas WireGuard (servidor y peers) | PostgreSQL (`wireguard`), cifradas; host WG | Crítica | Acceso a la red de gestión |
| KEK / clave maestra, clave de firma de JWT interno | Archivo secreto / OpenBao | Crítica | Descifrado de todo lo anterior / suplantación |
| Metadatos de tráfico de abonados (flujos) | ClickHouse, MinIO/NAS | Alta (dato personal) | Filtración de hábitos de navegación de miles de clientes |
| Usuarios, hashes, secretos TOTP, sesiones | PostgreSQL (`auth`), Redis | Alta | Toma de cuentas |
| Registro de auditoría | PostgreSQL + anclas en MinIO | Alta | Pérdida de trazabilidad / encubrimiento |
| Reglas de clasificación, reputación, scoring | PostgreSQL | Media | Detecciones falsas, ceguera |
| Backups | MinIO/NAS, offsite | Crítica (contienen todo lo anterior) | Igual que los datos de origen |

### 1.2 Actores de amenaza

1. **Atacante externo en Internet**: si la UI o los colectores quedan expuestos.
2. **Abonado del ISP / dispositivo comprometido en la red de clientes**: puede enviar UDP
   falsificado hacia los colectores si la red no está segmentada.
3. **Router comprometido**: envía flujos/traps maliciosos, explota parsers.
4. **Usuario interno malicioso o curioso** (operador NOC, analista): abuso de acceso a datos de
   clientes, exfiltración de credenciales.
5. **Cadena de suministro**: dependencia Go/npm o imagen base comprometida, Action de GitHub
   maliciosa.
6. **Operador de infraestructura con acceso al NAS/servidor**: acceso a backups o volúmenes.

## 2. Zonas de confianza

```
 Internet / red corporativa ISP
        │ 443 (HTTPS, WSS)            ← Zona EDGE: reverse proxy (Caddy), WAF básico, rate limit
 ┌──────▼───────┐
 │ reverse proxy│
 └──────┬───────┘
        │ red docker "edge"
 ┌──────▼───────┐   gRPC (red "app")   ┌──────────────────────────────────────┐
 │ api-gateway  ├─────────────────────►│ auth, devices, wireguard, analytics… │  Zona APP
 └──────────────┘                      └───────────┬──────────────────────────┘
                                                   │ red "data"
                       ┌───────────────────────────▼──────────────────────────┐
                       │ PostgreSQL · ClickHouse · Redis · NATS · MinIO        │  Zona DATA
                       └───────────────────────────▲──────────────────────────┘
                                                   │
 ┌─────────────────────────────────────────────────┴──────────────────────┐
 │ snmp (polling saliente) · flows (UDP 2055/4739/6343) · wireguard (UDP) │  Zona MGMT
 └───────────────────────────────▲────────────────────────────────────────┘
                                 │ VLAN/VRF de gestión o túneles WireGuard
                         Routers del ISP
```

Reglas: el frontend solo alcanza EDGE; los servicios APP no se publican al host; DATA no tiene
puertos publicados (salvo acceso administrativo por VPN); MGMT solo acepta tráfico desde los
prefijos de gestión. Detalle de la topología: [`architecture.md`](architecture.md) (Agente 1).

## 3. Modelo de amenazas STRIDE

Leyenda: **S**poofing, **T**ampering, **R**epudiation, **I**nformation disclosure, **D**enial of
service, **E**levation of privilege. "Ctrl" = control previsto; "Sprint" = cuándo debe existir.

### 3.1 Frontend (Nuxt 4, SPA servida estática)

| STRIDE | Amenaza | Control | Sprint |
|--------|---------|---------|--------|
| S | Phishing / sitio clonado que captura credenciales | TOTP/WebAuthn; HSTS con preload; dominio propio | 2 |
| T | XSS inyecta script que actúa con la sesión del usuario | Vue escapa por defecto; prohibido `v-html` sin sanitizar (DOMPurify) — regla ESLint `vue/no-v-html` como error; CSP estricta `default-src 'self'; script-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'`; cookies HttpOnly (el JS nunca ve el token) | 1 |
| R | — | La auditoría vive en el backend | — |
| I | Datos sensibles en `localStorage`, caché del navegador o URLs | Nada sensible en storage del navegador; `Cache-Control: no-store` en `/api/*`; nunca IPs de abonados ni tokens en query strings | 1 |
| D | Consultas analíticas pesadas desde la UI | Límites de rango/tiempo y paginación en la API; rate limit por usuario | 9 |
| E | Ocultar botones como "control de acceso" | La UI solo **refleja** permisos (`/api/v1/me` devuelve permisos efectivos); la decisión siempre es del backend | 2 |

### 3.2 API Gateway (`api-gateway`) y reverse proxy

| STRIDE | Amenaza | Control | Sprint |
|--------|---------|---------|--------|
| S | Robo/fijación de sesión | Cookie `__Host-` + Secure + HttpOnly; rotación del ID de sesión en login y elevación (2FA); vinculación débil a User-Agent (alerta, no bloqueo) | 2 |
| S | CSRF | SameSite=Lax + verificación de `Origin`/`Sec-Fetch-Site` + cabecera obligatoria `X-CSRF-Token` (token sincronizador ligado a la sesión) en métodos no seguros; WebSocket: validación estricta de `Origin` en el *upgrade* | 2 |
| T | Manipulación de cabeceras de identidad (`X-User-Id`) por el cliente | El gateway **elimina** cualquier cabecera de identidad entrante; la identidad interna solo viaja como JWT firmado | 1 |
| R | Acciones sin trazabilidad | `request_id` + `trace_id` en cada petición; auditoría de toda mutación | 2 |
| I | Mensajes de error con trazas internas | Errores RFC 9457 genéricos (formato en [`api.md`](api.md)); detalles solo en logs | 1 |
| D | Fuerza bruta, inundación HTTP/WS | Rate limit en Redis (token bucket): login 5/min por cuenta + 20/min por IP; API 600/min por sesión; máx. 5 WS por sesión; tamaño máx. de cuerpo 1 MiB (excepto subidas explícitas); timeouts de lectura/escritura | 1–2 |
| E | Ruta sin mapear a permiso queda abierta | **Deny by default**: tabla ruta→permiso declarativa; test que falla si una ruta no tiene permiso o `public: true` explícito | 2 |

### 3.3 Servicio de autenticación (`auth`)

| STRIDE | Amenaza | Control | Sprint |
|--------|---------|---------|--------|
| S | Credential stuffing / fuerza bruta | Argon2id; lista de contraseñas filtradas (top 100k embebida, sin llamadas externas); retardo progresivo por cuenta (1, 2, 4… s hasta 15 min) en vez de bloqueo duro; TOTP | 2 |
| S | Enumeración de usuarios | Respuestas y tiempos idénticos para usuario inexistente (hash *dummy*) en login y recuperación | 2 |
| T | Modificar roles en BD | Solo `auth` tiene permisos de escritura sobre tablas de identidad; cambios de rol auditados y notificados al usuario | 2 |
| R | "Yo no hice ese cambio" | Auditoría con `actor`, `sid`, IP, UA, `amr` | 2 |
| I | Filtración de hashes/secretos TOTP | Secretos TOTP cifrados con envelope encryption; hashes Argon2id; backups cifrados | 2 |
| D | Agotamiento de memoria por Argon2id (64 MiB por hash) | Semáforo: máx. `N = RAM_asignada / 128 MiB` hashes concurrentes; rate limit previo al hash | 2 |
| E | Escalada al autoasignarse roles | Permiso `roles.assign` separado; nadie puede asignarse a sí mismo un rol con más permisos de los que tiene; cambios de rol `admin` requieren re-autenticación reciente (≤5 min) | 2 |

### 3.4 Servicios internos (`devices`, `wireguard`, `analytics`, `reporting`, …)

| STRIDE | Amenaza | Control | Sprint |
|--------|---------|---------|--------|
| S | Un servicio suplanta a otro / llamadas gRPC sin identidad | JWT interno verificado por interceptor gRPC común; credenciales separadas por servicio para PostgreSQL/NATS/ClickHouse/Redis | 1 |
| T | SQL injection | Solo consultas parametrizadas (`pgx`, `clickhouse-go` con parámetros); linter `gosec` G201/G202 | 1 |
| I | IDOR (acceder a router de otro sitio cambiando un UUID) | Filtrado por alcance ACL en el repositorio (cláusula `site_id = ANY($scopes)`), no solo en el handler; tests de autorización por endpoint | 3 |
| D | Consulta ClickHouse sin límites | `max_execution_time`, `max_memory_usage`, `max_rows_to_read` por usuario de ClickHouse (perfil `readonly_api`) | 9 |
| E | Servicio comprometido accede a datos de otros | Un usuario de BD por servicio con privilegios mínimos sobre su propio esquema (ver [`database.md`](database.md)) | 1 |

### 3.5 Colectores expuestos a la red de routers (`snmp`, `flows`)

| STRIDE | Amenaza | Control | Sprint |
|--------|---------|---------|--------|
| S | UDP con IP de origen falsificada inyecta flujos falsos | Colector solo escucha en la interfaz/VLAN de gestión o dentro de WireGuard; **allowlist de exportadores** derivada del inventario (IP de origen + `observation_domain_id`/`source_id`); paquetes de origen desconocido se descartan y se cuentan; filtro nftables en el host; uRPF/BCP38 en el borde del ISP (recomendación al ISP) | 6 |
| S | Respuestas SNMP falsificadas | SNMPv3 authPriv (auth SHA-256 o superior si el equipo lo soporta, mínimo SHA-1; priv AES-128) como objetivo; v2c solo a través de WireGuard/VLAN de gestión | 5 |
| T | Paquetes malformados explotan el parser (NetFlow v9/IPFIX con plantillas) | Parsers con *fuzzing* nativo de Go en CI; límites duros: datagrama ≤ 9000 B, máx. 256 plantillas y 64 KiB de caché por exportador, longitud de campos validada; recuperación de pánicos por paquete | 6 |
| R | — | Métricas por exportador (sin IP de abonado) | 6 |
| I | Communities v2c en texto claro en la red | Migrar a v3; v2c solo dentro de túnel; community distinta por router, nunca `public` | 5 |
| D | Inundación UDP agota CPU / llena NATS | Token bucket por exportador (p. ej. 50k registros/s, configurable); descarte temprano con métrica `horus_flows_packets_dropped_total{reason}`; buffer acotado; JetStream con límites de bytes por stream | 6 |
| E | RCE en el colector da acceso a la red de gestión | Contenedor sin root, rootfs de solo lectura, `cap_drop: ALL` (solo `NET_BIND_SERVICE` si el puerto < 1024, p. ej. trap 162), sin acceso a la red DATA salvo NATS | 5–6 |

### 3.6 WireGuard (`wireguard`)

| STRIDE | Amenaza | Control | Sprint |
|--------|---------|---------|--------|
| S | Peer robado se conecta como un router legítimo | Rotación de claves; revocación inmediata; alerta si un peer hace handshake desde una IP de origen nueva; AllowedIPs `/32` estrictos por peer | 4 |
| T | Modificación de AllowedIPs para alcanzar otras redes | Solo `wireguard.write`; validación de solapamientos; auditoría con diff | 4 |
| R | — | Auditoría de alta/baja/rotación de peers | 4 |
| I | Fuga de la clave privada del servidor | Clave privada cifrada en reposo; en el host solo en memoria del kernel vía `wgctrl` (netlink), sin archivo `wg0.conf` persistente en claro; claves privadas de peers: ver §8.4 | 4 |
| D | Inundación del puerto UDP de WireGuard | WireGuard es silencioso ante paquetes no autenticados (cookies anti-DoS integradas); rate limit nftables | 4 |
| E | Contenedor con `NET_ADMIN` usado para pivotar | `wireguard` es el **único** servicio con `NET_ADMIN`; sin otras capacidades; no expone API pública (solo gRPC interno); el frontend jamás ejecuta comandos (vision §Sprint 4) | 4 |

### 3.7 Bases de datos y bus (PostgreSQL, ClickHouse, Redis, NATS)

| STRIDE | Amenaza | Control | Sprint |
|--------|---------|---------|--------|
| S | Conexión sin autenticación | Contraseñas/llaves por servicio (scram-sha-256 en PG; usuarios ClickHouse; `ACL` de Redis 6+; usuarios NATS con nkeys) | 1 |
| T | Publicación de eventos falsos en NATS | Permisos de *subject* por usuario NATS: cada servicio solo publica en `horus.<su-dominio>.>` y se suscribe a lo que consume | 1 |
| R | Borrado de auditoría | Ver §7 | 2 |
| I | Lectura de volúmenes/backups | Cifrado de disco en el host (LUKS) recomendado; backups cifrados (pgBackRest `repo-cipher-type=aes-256-cbc`, clickhouse-backup + SSE en MinIO) | 1 / 13 |
| D | Una consulta o consumidor lento tumba el bus | Límites por stream/consumer (`max_bytes`, `max_ack_pending`); perfiles de recursos en ClickHouse; `maxmemory` + política en Redis | 1–6 |
| E | Usuario de app con `SUPERUSER` | Prohibido; migraciones con un rol `*_migrator` distinto del rol de ejecución | 1 |

### 3.8 MinIO / NAS

| STRIDE | Amenaza | Control | Sprint |
|--------|---------|---------|--------|
| S | Acceso con credenciales raíz de MinIO | Root solo para bootstrap; usuarios y políticas IAM por servicio (`reporting` → `reports/*`, backups → `backups/*`) | 1 |
| T | Ransomware cifra/borra backups | Versionado + **Object Lock (modo compliance)** en el bucket de backups; copia offsite con credenciales distintas (3-2-1) | 13 |
| I | Reportes exportados con datos personales accesibles | URLs prefirmadas de 15 min; buckets privados; SSE-S3/KMS | 12 |
| D | NAS no responde → servicios bloqueados | Escrituras a MinIO asíncronas con reintento; ver [`disaster-recovery.md`](disaster-recovery.md) | 13–14 |
| E | Acceso administrativo al NAS | NAS fuera de la red de usuarios; 2FA en su panel; cuenta de servicio dedicada | 1 |

## 4. Autenticación

### 4.1 ¿Auth propio o Keycloak / Zitadel / Authentik?

| Criterio | Auth propio (Go) | Keycloak | Zitadel | Authentik |
|----------|------------------|----------|---------|-----------|
| Lenguaje / huella | Go, ~30 MB RAM | Java/Quarkus, 0,5–1 GB RAM | Go, ~200 MB, requiere PostgreSQL | Python + worker, ~500 MB |
| Integración con RBAC+ACL por sitio | Nativa, mismo modelo de datos | Mapear grupos/roles a claims; ACL por sitio sigue en Horus | Igual que Keycloak | Igual |
| UX de login integrada en Nuxt | Total | Temas Freemarker separados | Login UI propio o API | Flujos propios |
| Esfuerzo inicial | Medio-alto (Sprint 2 completo) | Medio (operar y configurar) | Medio | Medio |
| Riesgo de seguridad | Implementar bien primitivas (mitigado con librerías probadas y alcance acotado) | Bajo en el núcleo; superficie grande | Bajo | Medio |
| SSO/SAML/federación | Hay que construirla | Excelente | Muy buena | Muy buena |
| Operación (actualizaciones, backups) | Parte del producto | Componente extra crítico | Componente extra | Componente extra |

**Recomendación (S1):** servicio `auth` propio, porque (a) el producto es para **una** organización
y pocos usuarios (decenas), (b) el valor está en la autorización por sitio/grupo, que ningún IdP
resuelve, (c) evita operar un componente Java crítico en un despliegue docker compose de un solo
nodo, y (d) el plan del Sprint 2 ya incluye login, 2FA, sesiones y recuperación en la UI propia.

Para no cerrar la puerta a OAuth2/OIDC ("como base" según vision §7):

- El modelo interno usa los nombres de OIDC: `sub` (UUIDv7 del usuario), `sid`, `amr`
  (`pwd`, `otp`, `hwk`), `acr`, `auth_time`.
- **Como *relying party*** (cuando el ISP lo pida): login con el IdP corporativo
  (Entra ID / Google Workspace / Keycloak existente) mediante Authorization Code + PKCE con
  `coreos/go-oidc`; el usuario federado se vincula a un usuario local que conserva roles/ACL.
- **Como OIDC Provider**: solo cuando exista un cliente externo (p. ej. SSO para Grafana o una
  futura app móvil). Se implementa con `github.com/zitadel/oidc/v3` (librería certificada por la
  OpenID Foundation), exponiendo `/.well-known/openid-configuration`, `/authorize`, `/token`,
  `/jwks` con Authorization Code + PKCE únicamente (sin implicit ni password grant).
- Revisión de la decisión (ADR): si llega multi-tenant real, SAML o >500 usuarios, evaluar
  migrar a Zitadel (Go + PostgreSQL, encaja con el stack).

### 4.2 Usuario y contraseña

- **Hash:** Argon2id (`golang.org/x/crypto/argon2`), parámetros iniciales
  `m=65536 KiB (64 MiB), t=3, p=1`, sal aleatoria de 16 B, clave de 32 B. Supera el mínimo de
  OWASP (`m=19 MiB, t=2, p=1`) y se calibra para ~250 ms en el hardware objetivo.
  Almacenamiento en formato PHC: `$argon2id$v=19$m=65536,t=3,p=1$<sal>$<hash>`; si cambian los
  parámetros, se re-hashea en el siguiente login exitoso.
- **Pepper (opcional, recomendado):** `HMAC-SHA256(pepper, password)` antes de Argon2id; el
  pepper vive como secreto de despliegue fuera de la BD, con versión (`pepper_id`) para rotar.
- **Política (NIST SP 800-63B):** mínimo 12 caracteres, máximo 128, sin reglas de composición,
  rechazo contra lista de contraseñas filtradas embebida y contra el propio email/nombre,
  sin expiración periódica (solo ante sospecha de compromiso).
- **Comparaciones** en tiempo constante (`subtle.ConstantTimeCompare`).

### 4.3 TOTP (Sprint 2)

- RFC 6238, HMAC-SHA1, 6 dígitos, paso 30 s, ventana ±1 paso; se guarda el último paso usado
  para impedir reutilización del mismo código.
- Secreto de 20 B generado con `crypto/rand`, cifrado con envelope encryption (§8).
- Alta: QR + confirmación con un código válido antes de activar.
- 10 **códigos de recuperación** de un solo uso (10 caracteres base32), almacenados con Argon2id
  de parámetros reducidos o SHA-256 + sal (son de alta entropía); se muestran una sola vez.
- **Obligatorio** para roles con permisos `*.manage`, `wireguard.write`, `users.manage`,
  `roles.assign`, `devices.credentials.reveal` (configurable a "todos" — ver preguntas abiertas).

### 4.4 WebAuthn (posterior)

`go-webauthn/webauthn`; passkeys como segundo factor y luego como factor primario; varias llaves
por usuario; `amr=hwk`. Planificado para después del Sprint 2 (backlog del Agente 5).

### 4.5 Recuperación de cuenta

1. Usuario pide reset → respuesta idéntica exista o no la cuenta.
2. Se envía enlace con token de 32 B aleatorios; en BD solo se guarda `SHA-256(token)`;
   caducidad 30 min, un solo uso, invalida tokens anteriores.
3. El reset de contraseña **no** desactiva el 2FA: después de fijar la nueva contraseña se pide
   TOTP o código de recuperación.
4. Si el usuario perdió contraseña y 2FA: un administrador ejecuta "reset de 2FA" (requiere
   re-autenticación del admin, motivo obligatorio, auditado y notificado por email al usuario).
5. Todo reset revoca **todas** las sesiones del usuario.
6. **Bootstrap del primer admin:** comando `auth bootstrap-admin` dentro del contenedor que
   imprime un enlace de un solo uso (TTL 1 h); no hay credenciales por defecto.

## 5. Sesiones y tokens

### 5.1 Recomendación para la SPA Nuxt: sesión opaca + BFF en el gateway

Opciones evaluadas:

| | JWT de acceso corto + refresh rotativo en el navegador | Sesión opaca en cookie (BFF) |
|---|---|---|
| Revocación inmediata | No (hasta que expira el access token) salvo lista de revocación | Sí: borrar la sesión |
| Exposición a XSS | Alta si el token está en JS; media con cookie HttpOnly | Baja (cookie HttpOnly, nada en JS) |
| Complejidad en el cliente | Lógica de refresh, carreras entre pestañas | Ninguna |
| Coste por petición | Verificación local | Búsqueda en Redis (~0,2 ms) |
| Escalado | Sin estado | Redis compartido (ya está en el stack) |

**Decisión (S4/S5):** la SPA nunca maneja tokens. El `api-gateway` actúa como BFF:

```
Navegador ──cookie __Host-hf_sid──► api-gateway ──(Redis: sid→sesión; fallback PostgreSQL)
                                         │
                                         └─ mint JWT interno (EdDSA, 5 min, cacheado por sid)
                                                 │ gRPC metadata: authorization: Bearer <jwt>
                                                 ▼
                                         auth / devices / wireguard / …
```

- **Cookie:** `__Host-hf_sid=<256 bits base64url>; Path=/; Secure; HttpOnly; SameSite=Lax`.
  En Redis se guarda bajo `SHA-256(sid)` (un volcado de Redis no permite secuestrar sesiones).
  `SameSite=Lax` (no `Strict`) para que enlaces desde emails/alertas abran la app con sesión;
  la protección CSRF no depende solo de SameSite.
- **CSRF:** token sincronizador por sesión, entregado en `/api/v1/me` y enviado en
  `X-CSRF-Token` en `POST/PUT/PATCH/DELETE`; además validación de `Origin` contra la lista
  permitida. WebSocket: `Origin` obligatorio y coincidente.
- **Duración:** inactividad 2 h, absoluta 12 h (valores por defecto, configurables por rol).
  Las pantallas NOC "de pared" usarán un modo kiosco con rol de solo lectura y política propia
  (pregunta abierta).
- **Rotación:** nuevo `sid` en login, al completar 2FA, al cambiar contraseña/roles y cada
  30 min de uso (el anterior queda válido 30 s para peticiones en vuelo). Esta renovación
  deslizante cumple el requisito de "refresh" de vision §7 para el navegador.
- **Re-autenticación reciente** (`auth_time` ≤ 5 min) para: revelar credenciales, cambiar roles,
  generar/rotar claves WireGuard, desactivar 2FA, crear API tokens.

### 5.2 Sesiones revocables

- Fuente de verdad: tabla `sessions` en PostgreSQL (`id`, `user_id`, `created_at`,
  `last_seen_at`, `expires_at`, `ip`, `user_agent`, `amr`, `revoked_at`, `revoked_reason`);
  Redis es caché con TTL = inactividad. Esquema definitivo: [`database.md`](database.md).
- UI de "sesiones activas" (Sprint 2): el usuario y el admin (`sessions.manage`) pueden revocar.
- Al revocar: borrar de Redis, marcar en PostgreSQL y publicar
  `horus.auth.session.revoked` → el gateway cierra los WebSocket de ese `sid` en < 5 s y descarta
  el JWT interno cacheado. Los JWT internos ya emitidos expiran en ≤ 5 min; los servicios
  que hacen operaciones críticas (`wireguard`, revelado de credenciales) consultan
  `auth.CheckSession` vía gRPC (con caché de 10 s).
- Desactivar un usuario revoca todas sus sesiones y API tokens.

### 5.3 JWT interno (identidad entre servicios)

- Firmado por el gateway con Ed25519 (`kid` rotado cada 30 días, JWKS publicado por `auth`
  internamente; los servicios aceptan la clave actual y la anterior).
- Claims: `iss=horus-gateway`, `aud=horus-internal`, `sub`, `sid`, `org`, `amr`, `auth_time`,
  `exp` (≤ 5 min), `jti`, `perms` (permisos efectivos con alcance en forma compacta, p. ej.
  `{"devices.read":["site:018f…","group:018f…"],"users.manage":["*"]}`). Si el tamaño supera
  4 KiB, se incluye solo `roles_version` y el servicio resuelve permisos vía `auth` con caché.
- Nunca sale al navegador ni a los logs.
- Llamadas **de servicio a servicio sin usuario** (workers, jobs): JWT de servicio firmado por
  `auth` con `sub=svc:<nombre>` y permisos de servicio fijos; credencial de arranque del servicio
  vía Docker secret.

### 5.4 Clientes no-navegador (S6)

- **API tokens / cuentas de servicio** (integraciones del ISP, scripts): token opaco
  `hf_pat_<32 B base62>`, almacenado como SHA-256, con alcance de permisos ⊆ permisos del creador,
  caducidad obligatoria (máx. 1 año), último uso visible, revocable.
- **Refresh rotativo** (CLI / app futura vía OIDC): refresh opaco de un solo uso, TTL 30 días,
  organizado por `family_id`; cada uso emite un par nuevo e invalida el anterior; si se presenta
  un refresh ya usado (**reutilización**), se revoca toda la familia y la sesión asociada y se
  audita como incidente de seguridad.

## 6. Autorización: RBAC + ACL

### 6.1 Catálogo de permisos `recurso.accion`

Base de vision §7, ampliada (el catálogo canónico se mantiene en código, en `auth`, y se
expone en `GET /api/v1/permissions`):

| Recurso | Acciones |
|---------|----------|
| `users` | `read`, `manage` |
| `roles` | `read`, `manage`, `assign` |
| `sessions` | `read`, `manage` |
| `audit` | `read`, `export` |
| `sites` | `read`, `create`, `update`, `delete` |
| `devices` | `read`, `create`, `update`, `delete` |
| `devices.credentials` | `write`, `reveal` (revelar en claro; requiere 2FA + re-auth) |
| `wireguard` | `read`, `write`, `keys.rotate` |
| `snmp` | `read`, `manage` (perfiles, intervalos) |
| `flows` | `read` (exportadores), `manage` |
| `traffic` | `read` (agregados), `client.read` (detalle por abonado — dato personal) |
| `classification` | `read`, `manage` (reglas, categorías, servicios) |
| `security` | `read`, `manage` |
| `alerts` | `read`, `ack`, `manage` (reglas, canales) |
| `reports` | `read`, `export` |
| `settings` | `read`, `manage` |
| `api_tokens` | `manage` (propios) |

### 6.2 Roles predefinidos

Los roles de sistema no se pueden borrar; se pueden crear roles personalizados.

| Rol | Propósito | Permisos (resumen) | 2FA |
|-----|-----------|--------------------|-----|
| `admin` | Administración total de la plataforma | Todos | Obligatorio |
| `security_admin` | Seguridad de la plataforma y del tráfico | `security.*`, `audit.*`, `users.read`, `sessions.manage`, `alerts.*`, `traffic.read`, `traffic.client.read` | Obligatorio |
| `network_engineer` | Altas/bajas de equipos, WireGuard, SNMP, flows | `sites.*`, `devices.*`, `devices.credentials.write`, `wireguard.*`, `snmp.*`, `flows.*`, `alerts.read/ack`, `traffic.read` | Obligatorio |
| `noc_operator` | Monitoreo 24/7 y atención de alertas | `sites.read`, `devices.read`, `wireguard.read`, `snmp.read`, `flows.read`, `traffic.read`, `alerts.read/ack`, `reports.read` | Recomendado |
| `analyst` | Analítica de tráfico y reportes | `traffic.read`, `traffic.client.read`, `classification.read`, `reports.read/export`, `devices.read`, `sites.read` | Recomendado |
| `auditor` | Revisión de cumplimiento | `audit.read/export`, `users.read`, `roles.read`, `sessions.read` | Recomendado |
| `viewer` | Solo lectura sin datos personales | `*.read` excepto `traffic.client.read`, `audit.read`, `devices.credentials.*` | Opcional |

Nota: **nadie** tiene `devices.credentials.reveal` por defecto salvo `admin`; se concede de forma
explícita.

### 6.3 ACL por alcance

- Una **asignación** es `(usuario | grupo_de_usuarios, rol, alcance)`, con alcance
  `global` | `site:<uuid>` | `router_group:<uuid>`. Un usuario puede tener varias.
- Permisos efectivos = unión de asignaciones; el alcance de cada permiso es la unión de los
  alcances de los roles que lo otorgan. `router_group` se expande a sitios/routers en
  `devices`.
- Recursos sin sitio (usuarios, reglas de clasificación) solo admiten alcance `global`.
- Datos de tráfico por abonado heredan el alcance del router/sitio que los exportó.

### 6.4 Dónde se evalúa

1. **Gateway (grueso):** autenticación, sesión válida, 2FA cumplido si el rol lo exige, y
   "¿tiene el permiso X en *algún* alcance?" según la tabla declarativa ruta→permiso. Rechaza
   pronto (403) y reduce carga.
2. **Servicio dueño (fino, obligatorio):** interceptor gRPC común (`packages/go/authz`) que
   valida el JWT interno y expone `authz.Require(ctx, "devices.update", scope)`; el
   **repositorio** filtra por alcance (`WHERE site_id = ANY(@allowed_sites)` o "global"). Esto
   evita IDOR aunque el gateway tenga un error.
3. **Asíncrono (NATS):** los consumidores no reevalúan permisos de usuario; actúan como el
   servicio (`actor` queda registrado). La autorización se hizo al aceptar el comando original.
   Las acciones disparadas por eventos que modifican la red (p. ej. revocar peer por detección)
   requieren una política de servicio explícita y auditoría con `actor.type=system`.

### 6.5 Propagación de identidad

| Canal | Mecanismo |
|-------|-----------|
| Navegador → gateway | Cookie de sesión + `X-CSRF-Token` |
| Gateway → servicio (gRPC) | Metadata `authorization: Bearer <JWT interno>` + `traceparent` + `x-request-id` |
| Servicio → servicio (gRPC) | Reenvía el JWT del usuario si actúa en su nombre (con `exp` corto); si no, JWT de servicio |
| Servicio → NATS | Campo `actor` del envelope (definido por el Agente 3 en [`events.md`](events.md)) |
| Integración → gateway | `Authorization: Bearer hf_pat_…` → el gateway lo cambia por JWT interno |

**Dependencia con el Agente 3 (envelope de eventos):** seguridad necesita en `actor`:
`type` (`user` | `service` | `system` | `api_token`), `id` (UUIDv7 o `svc:<nombre>`), `sid`
(opcional, solo usuario), `ip` (opcional, solo eventos de auditoría). **Nunca** tokens, ni
permisos completos, ni secretos en el envelope.

## 7. Auditoría

### 7.1 Qué se audita (mínimo)

- **Autenticación:** login OK/fallido (motivo genérico), logout, 2FA alta/baja/fallo, uso de
  código de recuperación, reset de contraseña solicitado/completado, bloqueo progresivo,
  reutilización de refresh token, creación/revocación de API tokens, revocación de sesiones.
- **Autorización/administración:** alta/baja/cambio de usuarios, roles, asignaciones y alcances
  (con diff antes/después), cambios en `settings`.
- **Inventario y red:** crear/editar/borrar sitios, routers, grupos; escritura y **revelado**
  de credenciales (sin el valor); WireGuard: alta/baja/rotación/revocación de peers y servidores,
  cambios de AllowedIPs.
- **Datos personales:** acceso a vistas de detalle por abonado (`traffic.client.read`),
  exportaciones de reportes (quién, qué filtro, cuántas filas), descargas de backups.
- **Seguridad:** cambios de reglas de detección/clasificación/alertas, ack/cierre de alertas
  de seguridad.
- **Plataforma:** arranque con configuración insegura (p. ej. TLS desactivado), rotación de
  claves (KEK, firma JWT), restauraciones de backup.

### 7.2 Formato del registro

`id` (UUIDv7), `occurred_at` (UTC), `actor` (`type`, `id`, `sid`), `ip`, `user_agent`,
`action` (p. ej. `devices.credentials.reveal`), `resource_type`, `resource_id`, `scope`
(sitio), `outcome` (`success` | `denied` | `error`), `reason`, `changes` (diff JSON sin
secretos), `request_id`, `trace_id`, `prev_hash`, `hash`. Esquema en [`database.md`](database.md)
(Agente 2).

### 7.3 Escritura e inmutabilidad

1. Cada servicio escribe el evento de auditoría en su **outbox** dentro de la misma transacción
   que el cambio de negocio (no se pierde si NATS cae) y lo publica en
   `horus.audit.record.created` (stream `AUDIT`, `R` = réplicas del clúster, retención 7 días).
2. `auth` es el **único escritor** de la tabla `audit_log`: consume el stream y añade cada
   registro calculando `hash = SHA-256(prev_hash ‖ JSON canónico del registro)`.
3. Inmutabilidad en PostgreSQL: el rol de `auth` solo tiene `INSERT, SELECT` sobre
   `audit_log`; un trigger rechaza `UPDATE/DELETE/TRUNCATE`; la purga por retención solo la
   hace un rol `audit_archiver` que primero exporta la partición a MinIO.
4. **Anclaje:** cada día se escribe el hash de cabeza de la cadena en un objeto del bucket
   `audit-anchors` con **Object Lock (compliance, 2 años)**, y el job de verificación recorre la
   cadena (alerta si se rompe).
5. Retención propuesta: 1 año en línea, 5 años archivado en MinIO/NAS (validar con legal).
6. La auditoría **no** es log: no va a Loki como fuente de verdad (puede ir una copia sin datos
   personales).

## 8. Gestión de secretos

### 8.1 Clasificación

| Tipo | Ejemplos | Dónde |
|------|----------|-------|
| Secretos de despliegue | Contraseñas de PG/ClickHouse/Redis/NATS/MinIO, pepper, clave de firma JWT, KEK | Docker secrets (archivos), cifrados en el repo de despliegue con SOPS+age |
| Secretos de dominio (datos) | Credenciales SNMP/SSH/API de routers, claves privadas WireGuard, secretos TOTP, credenciales de canales de notificación (SMTP, bot de Telegram) | PostgreSQL, cifrados con envelope encryption |
| Secretos de CI | Credenciales de registro, tokens | GitHub Actions secrets/environments + OIDC (sin credenciales de larga duración cuando sea posible) |

### 8.2 Envelope encryption (S9)

- Cada secreto de dominio se cifra con una **DEK** aleatoria (AES-256-GCM, nonce 96 bits).
  La DEK se cifra con la **KEK** activa.
- Se almacena: `ciphertext`, `nonce`, `wrapped_dek`, `kek_id`, `alg` (`A256GCM`), `created_at`.
- **AAD** = `"<tabla>|<columna>|<id_registro>|<org_id>"`: impide copiar un secreto cifrado a
  otra fila/router.
- Librería común `packages/go/crypto/envelope` con interfaz `KeyProvider` (`Wrap`, `Unwrap`)
  y dos implementaciones: `FileKeyProvider` (v1) y `OpenBaoTransitProvider` (v2).
- **Rotación de KEK:** nueva `kek_id` activa para escrituras; job de re-envolvimiento de DEKs
  (no requiere re-cifrar los datos); la KEK anterior se retira cuando ya no hay referencias.
- **Quién descifra:** solo `devices` (credenciales de routers) y `wireguard` (claves WG) y
  `auth` (TOTP), cada uno con **su propia KEK**. `snmp` obtiene credenciales en claro mediante
  la RPC interna `devices.CredentialService/Resolve`, autorizada solo para `svc:snmp`, sobre la
  red interna (TLS cuando aplique); las mantiene en memoria con TTL de 15 min y nunca las escribe
  en disco ni en logs.
- La API pública trata las credenciales como **campos de solo escritura**: nunca se devuelven;
  "revelar" es un endpoint aparte, con `devices.credentials.reveal`, 2FA, re-auth y auditoría.

### 8.3 Progresión recomendada

| Fase | Sprint | KEK / secretos de despliegue | Motivo |
|------|--------|------------------------------|--------|
| v1 | 1–13 | KEK = 32 B aleatorios en archivo montado como Docker secret (`/run/secrets/devices_kek`, permisos 0400, uid del servicio); secretos de despliegue en archivos `secrets/*.txt` (gitignored) en local y cifrados con **SOPS + age** en `deployments/` | Simple, sin componentes extra, compatible con compose y con Kubernetes Secrets después |
| v2 | 14–16 | **OpenBao** (fork MPL-2.0 de Vault; se prefiere a Vault por la licencia BSL de este) con el motor **Transit** como KMS: la KEK nunca sale de OpenBao; auto-unseal o unseal con Shamir 3/5 | KEK fuera del host de aplicación, auditoría de uso de claves, rotación gestionada |
| v3 | Según escala | Credenciales dinámicas de BD de OpenBao; integración con External Secrets Operator en Kubernetes | Solo si se migra a Kubernetes |

**Backup de la KEK:** perder la KEK = perder todas las credenciales de routers y claves WG. Se
guarda en dos lugares fuera de línea (gestor de contraseñas corporativo + copia sellada impresa
o USB cifrado en caja fuerte), cifrada con age; procedimiento en
[`disaster-recovery.md`](disaster-recovery.md).

### 8.4 Casos concretos

- **SNMP v2c:** community por router, cifrada; nunca `public`/`private`; vista de solo lectura.
- **SNMPv3:** usuario, protocolo y claves auth/priv cifrados; preferir authPriv.
- **SSH/API de routers** (futuro, para adaptadores por fabricante): usuario con perfil de solo
  lectura salvo que una función lo requiera; preferir llaves SSH (ed25519) generadas por Horus a
  contraseñas; `known_hosts` fijado por router (TOFU con aprobación).
- **WireGuard:** clave privada del servidor cifrada; clave privada del peer generada en el
  servidor solo si el router no puede generarla, entregada **una vez** (descarga de config) y
  no almacenada después (o almacenada cifrada si el negocio exige re-descarga — pregunta
  abierta). Preferir que el router genere su par y Horus solo guarde la pública.

## 9. Seguridad de la red de gestión

1. **Segmentación:** colectores y `wireguard` en una red (VLAN/VRF de gestión) separada de la
   red de usuarios del ISP y de la red de abonados. Redes Docker separadas `edge`, `app`,
   `data`, `mgmt` (definición final en [`architecture.md`](architecture.md)).
2. **Exportación de flujos y SNMP:** preferentemente a través de la VLAN de gestión o del túnel
   WireGuard del router. Nunca por Internet en claro.
3. **Puertos de entrada** (solo desde prefijos de gestión, filtrado nftables en el host):
   NetFlow v5/v9 `UDP 2055`, IPFIX `UDP 4739`, sFlow `UDP 6343`, traps SNMP `UDP 162`
   (si se usan), WireGuard `UDP 51820`.
4. **Validación de origen:** el colector solo acepta exportadores registrados en el inventario
   (IP + ID de dominio de observación); los desconocidos se descartan y generan un evento
   `horus.flows.exporter.unknown` (con rate limit) para que el operador los registre.
   **Importante:** la IP de origen debe preservarse hasta el contenedor (red `host` para
   `flows` o NAT sin *userland proxy*); decisión de despliegue del Agente 1.
5. **Rate limiting** por exportador (token bucket) y global; límites de memoria de plantillas.
6. **SNMP saliente:** el colector consulta solo IPs del inventario; timeouts y reintentos
   acotados (p. ej. 2 s, 2 reintentos); concurrencia máxima por router (1–2) para no saturar CPU
   de equipos pequeños (MikroTik).
7. **En los routers** (recomendación al ISP, ver skill de redes): ACL que limite SNMP y la
   exportación a la IP del colector, SNMPv3, community/usuario dedicado, CoPP.

## 10. Hardening de contenedores

Aplicable a todos los servicios Go y al frontend (detalles de Dockerfile en
[`conventions.md`](conventions.md)):

- Imagen base `gcr.io/distroless/static-debian12:nonroot` (Go, `CGO_ENABLED=0`); frontend
  estático servido por Caddy sin root. Imágenes referenciadas **por digest** en despliegues.
- `user: 65532:65532`; `read_only: true` + `tmpfs: /tmp` si hace falta;
  `cap_drop: [ALL]`; `security_opt: [no-new-privileges:true]`; perfil seccomp por defecto
  de Docker (nunca `unconfined`); sin `privileged`.
- Excepciones explícitas y documentadas: `wireguard` (`cap_add: NET_ADMIN`), colector de
  traps si escucha en 162 (`NET_BIND_SERVICE`) — preferible escuchar en puerto alto y redirigir
  con nftables.
- Límites de recursos (`mem_limit`, `cpus`, `pids_limit`) por servicio.
- **Nunca** montar `/var/run/docker.sock` en servicios de la app. El agente de logs (Grafana
  Alloy) lee `/var/lib/docker/containers` en solo lectura; si necesita el socket, se usa un
  proxy de socket de solo lectura (`tecnativa/docker-socket-proxy` con solo `CONTAINERS=1`).
- Puertos de administración (`/metrics`, `/healthz`, `/readyz`, pprof) solo en la red interna.
- Host: actualizaciones automáticas de seguridad, SSH solo con llave, Docker *rootless* o
  `userns-remap` evaluado en Sprint 14, LUKS en discos de datos.
- Verificación: `trivy config` (misconfiguraciones de Dockerfile/compose) y `hadolint` en CI;
  Docker Bench for Security en el host antes de la release 1.0.

## 11. TLS

| Tramo | v1 (compose, un nodo) | v2 (multi-host / Kubernetes) |
|-------|-----------------------|------------------------------|
| Navegador → reverse proxy | TLS 1.2+ (preferente 1.3), Caddy con ACME (Let's Encrypt) si hay dominio público, o certificado de la CA interna del ISP; HSTS `max-age=31536000; includeSubDomains` | Igual |
| Reverse proxy → gateway | HTTP en red Docker interna `edge` | mTLS |
| Gateway ↔ servicios (gRPC) | Texto plano en red Docker interna aislada + JWT firmado | **mTLS** con CA interna (`step-ca` de Smallstep) o service mesh (Linkerd); certificados de 24 h |
| Servicios → PostgreSQL / ClickHouse / NATS / Redis | TLS **activado desde Sprint 1** para NATS y PostgreSQL (coste bajo, evita deuda); ClickHouse/Redis TLS cuando crucen host | TLS obligatorio en todo |
| Cualquier tramo que cruce hosts físicos o el NAS | TLS obligatorio (MinIO con TLS) | TLS obligatorio |

Regla: el código siempre soporta TLS por configuración (`*_TLS_CA_FILE`, `*_TLS_CERT_FILE`,
`*_TLS_KEY_FILE`); desactivarlo genera un log `WARN` al arrancar y un registro de auditoría.

## 12. Cadena de suministro

| Control | Herramienta | Cuándo | Bloquea merge |
|---------|-------------|--------|---------------|
| Vulnerabilidades Go (alcanzables) | `govulncheck ./...` | PR + nocturno | Sí (si es alcanzable) |
| Vulnerabilidades en lockfiles (Go + pnpm) | `osv-scanner` | PR + nocturno | Sí (CVSS ≥ 7 con fix disponible) |
| Imágenes y filesystem | `trivy image` / `trivy fs` / `trivy config` | Build + nocturno sobre imágenes publicadas | Sí (CRITICAL/HIGH con fix) |
| Secretos en el código | `gitleaks` (pre-commit opcional + CI) + secret scanning/push protection de GitHub | PR | Sí |
| SAST | `gosec` (vía golangci-lint), CodeQL (Go y JS/TS) si el plan de GitHub lo permite, si no `semgrep` con reglas `p/golang`, `p/typescript` | PR | Sí (alta severidad) |
| SBOM | `syft` → SPDX JSON + CycloneDX, adjunto a cada imagen como attestation | Build en `main` y releases | — |
| Firma de imágenes | `cosign sign` keyless (OIDC de GitHub Actions, Sigstore) | Build en `main` y releases | — |
| Procedencia | `actions/attest-build-provenance` (SLSA build L2+) | Build | — |
| Verificación al desplegar | `cosign verify` con identidad del workflow esperada | Script de despliegue | Sí |
| Dependencias al día | **Renovate** (agrupa por ecosistema y por servicio en el monorepo; mejor que Dependabot para monorepos) con *minimum release age* de 3 días | Semanal | — |
| Integridad | `go mod verify`; `pnpm install --frozen-lockfile`; `GOFLAGS=-mod=readonly` | CI | Sí |
| GitHub Actions | Fijadas por SHA completo; `permissions:` mínimos por job (`contents: read` por defecto); sin `pull_request_target` con checkout de código del PR; OpenSSF Scorecard | Siempre | — |
| Licencias | `trivy` (licencias) o `go-licenses`; prohibidas AGPL/SSPL en dependencias enlazadas sin revisión | Nocturno | Advertencia |

Política de vulnerabilidades: CRITICAL ≤ 7 días, HIGH ≤ 30 días, MEDIUM en el siguiente sprint.

## 13. Privacidad de los datos de tráfico de clientes

### 13.1 ¿Qué es dato personal?

- **IP del abonado** (asignada vía PPPoE/DHCP) + registros del ISP que la asocian a un contrato
  → dato personal (identificable).
- **Flujos** (IP origen/destino, puertos, bytes, horarios): metadatos de comunicaciones que
  revelan hábitos (servicios usados, horarios, sitios visitados por ASN/servicio). Muy sensibles;
  en algunas jurisdicciones están protegidos por el **secreto de las comunicaciones**.
- **Scoring residencial/comercial** (Sprint 10): perfilado automatizado con posible efecto en
  el contrato del cliente → requiere transparencia y revisión humana.
- Datos de usuarios de la plataforma (empleados): email, IP de acceso, auditoría.
- **No** se recogen payloads, DNS ni URLs (vision Sprint 6: solo metadatos de flujo, no captura
  de paquetes). Cualquier ampliación requiere ADR + revisión legal.

### 13.2 Controles

1. **Minimización:** solo los campos de flujo necesarios (lista cerrada en
   [`traffic-model.md`](traffic-model.md), Agente 2).
2. **Seudonimización en el largo plazo:** los agregados > 30 días se asocian a `client_id`
   (UUID interno), no a IP; la correspondencia IP↔cliente↔tiempo vive en PostgreSQL con acceso
   restringido.
3. **Retención** (vision Sprint 13; validar con legal): flujo crudo 7–30 días, agregado
   6–12 meses, diario 2–5 años. Borrado efectivo con TTL de ClickHouse y lifecycle de MinIO,
   incluidos backups (que caducan en su propio ciclo).
4. **Acceso mínimo:** `traffic.read` muestra agregados por sitio/router/categoría;
   `traffic.client.read` (detalle por abonado) solo a roles que lo necesitan, con alcance por
   sitio y **auditado** cada acceso.
5. **Exportaciones:** registradas, con marca de agua (usuario, fecha) en PDF, enlaces caducos.
6. **Logs y trazas sin IPs de abonados** (ver [`observability.md`](observability.md)).
7. **Entornos de desarrollo/pruebas** con datos sintéticos o anonimizados, nunca copias de
   producción sin anonimizar.
8. **Evaluación de impacto (DPIA/EIPD)** antes de activar Sprints 6–10 en producción.
9. **Scoring explicable** (ya exigido por vision Sprint 10) y decisión final humana antes de
   cualquier acción comercial sobre el cliente.

### 13.3 Pregunta abierta (legal)

La legislación aplicable depende del **país del ISP** y debe validarla un asesor legal:
leyes de protección de datos (p. ej. LFPDPPP en México, Ley 1581 en Colombia, Ley 29733 en Perú,
LOPDP en Ecuador, Ley 21.719 en Chile, LGPD en Brasil, RGPD en la UE/España), normas de
telecomunicaciones sobre **conservación obligatoria** de metadatos (que pueden imponer retenciones
mínimas *y* máximas) y secreto de las comunicaciones. Ver
[`open-questions/security-ops.md`](open-questions/security-ops.md) Q1–Q2.

## 14. Respuesta a incidentes (mínimo v1)

- Contacto de seguridad y responsable de guardia definidos (pregunta abierta).
- Playbooks breves: (a) cuenta comprometida → revocar sesiones/tokens, forzar reset y 2FA;
  (b) KEK o credenciales de routers comprometidas → rotar KEK, rotar credenciales en routers
  (lista exportable por sitio), rotar claves WG; (c) fuga de datos de tráfico → evaluar
  notificación según ley aplicable.
- `SECURITY.md` en la raíz del repo con canal de reporte (Sprint 1, Agente 5 lo planifica).

## 15. Checklist de seguridad para la Definición de Terminado

Cada historia que toque código marca (o justifica N/A):

- [ ] Endpoints nuevos registrados en la tabla ruta→permiso del gateway (test de "deny by
      default" pasa) y verificados también en el servicio con alcance ACL.
- [ ] Tests de autorización: al menos un caso 403 (sin permiso) y uno de alcance (otro sitio).
- [ ] Entradas validadas (tipos, rangos, tamaños) en el borde; consultas parametrizadas.
- [ ] Ningún secreto, token, credencial, contraseña ni IP de abonado en logs, trazas, métricas,
      eventos ni mensajes de error (revisado en el PR).
- [ ] Secretos de dominio cifrados con `envelope`; campos de credenciales de solo escritura.
- [ ] Acciones sensibles generan registro de auditoría (vía outbox) con diff sin secretos.
- [ ] Datos personales nuevos: campo justificado, retención definida, permiso adecuado.
- [ ] Dependencias nuevas justificadas; `govulncheck`, `osv-scanner`, `trivy` y `gitleaks` en
      verde.
- [ ] Contenedor cumple §10 (no root, sin capacidades extra salvo excepción documentada).
- [ ] Rate limit / límites de tamaño considerados para endpoints públicos o costosos.
- [ ] Parsers de entrada de red (flows, SNMP, traps) con test de fuzzing.
- [ ] Cambios en autenticación/autorización/criptografía revisados por una segunda persona con
      el checklist de este documento.
