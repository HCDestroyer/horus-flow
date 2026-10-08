# Horus Flow — Seguridad

> Estado: **propuesta ronda 2** (aplica [`po-decisions.md`](po-decisions.md) D1–D10, que prevalecen) · Responsable:
> Agente C (contratos, seguridad y operaciones). "Sprint N" = el incremento que entrega esa capacidad (D9).
> "Redis" = **Valkey** (D3). "MinIO/NAS" = almacenamiento local + destino remoto opcional (D2; [`storage.md`](storage.md)).
> Fuente: [`vision.md`](vision.md) §7. Documentos relacionados: [`architecture.md`](architecture.md),
> [`services.md`](services.md), [`database.md`](database.md), [`api.md`](api.md),
> [`events.md`](events.md), [`storage.md`](storage.md), [`observability.md`](observability.md),
> [`disaster-recovery.md`](disaster-recovery.md), [`conventions.md`](conventions.md).
> Preguntas abiertas: [`open-questions/security-ops.md`](open-questions/security-ops.md).

Horus Flow ve **toda la red de varios ISP** (D6: multi-tenant desde v1): credenciales de
administración de los routers MikroTik de cada nodo, claves WireGuard que dan acceso a sus redes
de gestión, y metadatos de tráfico de cada IP de cliente. Un compromiso de la plataforma equivale
a un compromiso de la red de **todos** los ISP alojados y a una filtración masiva de datos
personales; un fallo de aislamiento equivale a que un ISP vea la red y los clientes de su
competidor. Este documento fija el modelo de amenazas y los controles mínimos.

**Propósito declarado del tratamiento (D5):** seguridad de la red del ISP —detectar y mitigar
clientes infectados o que participan en botnets— y, de forma secundaria, operación de red y
detección de uso comercial de IPs residenciales. Uso exclusivamente empresarial (el ISP como
operador de su red). Este propósito acota qué se recoge, cuánto se guarda y quién lo ve (§13).

## 0. Resumen de decisiones

| # | Tema | Decisión v1 | Evolución |
|---|------|-------------|-----------|
| S1 | Proveedor de identidad | Servicio `auth` propio en Go; modelo de identidad compatible con OIDC (claims `sub`, `amr`, `acr`, `sid`). Endpoints de OP (OIDC Provider) con la librería certificada `zitadel/oidc` solo cuando haya un cliente externo (p. ej. SSO a Grafana). | Federación con IdP corporativo del ISP (Entra ID / Google Workspace) como *relying party*. Keycloak/Zitadel solo si aparecen requisitos multi-tenant/SAML. |
| S2 | Contraseñas | Argon2id `m=64 MiB, t=3, p=1`, sal 16 B, salida 32 B, formato PHC; *pepper* opcional vía HMAC. | Recalibrar en Sprint 15 (objetivo 150–400 ms por hash). |
| S3 | 2FA | TOTP (RFC 6238) obligatorio para roles privilegiados; 10 códigos de recuperación. | WebAuthn/passkeys (después de Sprint 2). |
| S4 | Tokens del navegador | **Access JWT corto (10 min, EdDSA, emitido por `auth`) en memoria de la SPA, enviado como `Authorization: Bearer`** + **refresh opaco rotativo con detección de reutilización** en cookie `__Secure-hf_rt` (`HttpOnly; Secure; SameSite=Strict; Path=/api/v1/auth`). Coincide con ADR-0012 y `api.md`. CSP estricta como defensa principal frente a XSS. | Reevaluar cookies HttpOnly para el access token si una auditoría lo pide. |
| S5 | Revocación e identidad interna | Cada access token lleva `sid`. El gateway valida firma (JWKS de `auth`) y revocación (`session_revoked:<sid>` en Valkey → fallback gRPC a `auth`, caché 30 s). **Fuente de verdad: `auth.sessions` en PostgreSQL.** El gateway reenvía el mismo JWT al REST del servicio (proxy HTTP, [ADR-0013](adr/0013-api-gateway-propio.md)) y por gRPC entre servicios, y **cada servicio lo revalida** (firma, `exp`, `aud`). gRPC interno con **mTLS** desde el Sprint 1. Los eventos NATS llevan `actor`, nunca tokens. | Certificados de corta vida automatizados (step-ca) y/o service mesh en Kubernetes. |
| S6 | Sesiones revocables | Tabla `sessions` (PG) + familias de refresh; revocar = marcar sesión + publicar `horus.auth.session.revoked` (gateway cierra WebSockets). Ventana máxima de aceptación tras revocar: 30 s si Valkey está caído; inmediata si no. | — |
| S7 | Autorización | RBAC + ACL por alcance **dentro de cada tenant** (`tenant`, `site`, `router_group`) mediante membresías; roles de plataforma aparte. Gateway: autenticación + pertenencia al tenant de la ruta + permiso grueso. Servicio dueño: permiso + filtrado por tenant y alcance (defensa en profundidad). | ABAC puntual si hace falta. |
| S8 | Auditoría | Tabla append-only en PostgreSQL con cadena de hashes; escritura vía outbox + NATS hacia `auth`; anclaje diario del hash en almacenamiento local de solo-anexado, replicado al destino remoto si existe (D2). | Exportación a SIEM. |
| S9 | Secretos de routers/WireGuard | *Envelope encryption* AES-256-GCM con AAD (DEK por secreto, KEK fuera de la BD); borrado = *crypto-shredding*; KEK en v1 = clave maestra en archivo (Docker secret); solo `devices`, `wireguard`, `auth` y `alerts` (canales) cargan su KEK. Columna `secret_ref` reservada para gestor externo. | OpenBao (Transit) como KMS en Sprint 14–16. SOPS+age para secretos de despliegue desde Sprint 1. |
| S10 | Cadena de suministro | govulncheck, osv-scanner, Trivy, gitleaks, Syft (SBOM), cosign keyless + attestations de GitHub, Renovate, Actions fijadas por SHA. | SLSA nivel 3. |
| S11 | Almacén clave-valor | **Valkey** (D3; fork BSD-3 de Redis, compatible con el protocolo); usuarios ACL por servicio, claves con prefijo de tenant, sin secretos persistentes (§3.7). | — |
| S12 | Aislamiento entre tenants (D6) | Tenant explícito en rutas, topics, subjects y gRPC; repositorios que exigen `TenantScope`; **RLS de PostgreSQL** y *row policies* de ClickHouse como segunda barrera; batería automática de pruebas "A no ve B" obligatoria en CI (§3.9). | Base de datos o despliegue dedicado por tenant si un ISP lo exige por contrato. |
| S13 | Pantallas NOC (D8) | **Kiosco = dispositivo registrado**: código de enrolamiento de un uso (10 min) → credencial de dispositivo HttpOnly rotativa → JWT de solo lectura limitado a dashboards asignados, CIDR opcional, sin datos personales por defecto; nunca tokens largos en la URL (§5.5). | Certificado de cliente en el dispositivo (mTLS) para videowalls gestionados. |
| S14 | Roles de plataforma | `platform_admin`/`platform_operator`/`platform_auditor` **sin** acceso implícito a datos de negocio de los ISP; acceso de soporte temporal, con motivo, notificado y auditado en ambos lados (§6.6). | — |
| S15 | Credenciales de MikroTik (D10) | API de RouterOS sólo con TLS (8729 / HTTPS) dentro del túnel WireGuard, usuario `horus-ro` de **solo lectura** con `address=` = red de servicios de Horus; SNMPv3 authPriv; alta con token de enrolamiento de un solo uso; v1 no escribe en routers. *Envelope encryption* con clave por tenant (§8.4). | Rotación automática programada. |
| S17 | Ronda 2 del PO (D11, D13–D16) | **D14** acceso por Internet: dominio por instalación (`HORUS_PUBLIC_BASE_URL`, `HORUS_ALLOWED_ORIGINS`), TLS ACME en Traefik, cookies host-only sin `Domain`, URLs absolutas solo desde la configuración (nunca `Host`), `Origin` validado en refresh/kiosco/WebSocket, 2FA obligatorio en `tenant_admin`, `security_analyst`, `network_engineer` y roles de plataforma desde el I1, rate limit de borde (api.md §1.11). **D13** canales email/Telegram por ISP: token de bot propio write-only cifrado como los demás secretos (KEK de `alerts`), mensajes sin IP de cliente salvo `include_personal_data` auditado (salen a terceros: proveedor SMTP, Telegram); LibreNMS previsto. **D11** acciones recomendadas: comandos RouterOS sugeridos, nunca ejecutados (no hay credenciales de escritura; ADR-0022); la IP solo aparece renderizada para quien tiene `customers.read`. **D15/D16** RouterOS ≥ 7.12 y túnel WireGuard iniciado por el router como único camino de gestión y flujos. | Dominio por ISP (lista de orígenes por tenant). |
| S16 | Destinos remotos de copias (D2) | Cifrado **en cliente** antes de salir del servidor (las copias son ilegibles para SFTP/Drive/MEGA/Dropbox); credenciales con mínimo privilegio (carpeta de app, usuario SFTP enjaulado), cifradas en PostgreSQL y materializadas sólo en memoria del proceso de copia (§8.5). | Destino *pull* (el NAS recoge) para que un servidor comprometido no pueda borrar copias. |

## 1. Activos y actores

### 1.1 Activos (por criticidad)

| Activo | Dónde vive | Criticidad | Impacto si se compromete |
|--------|-----------|------------|--------------------------|
| Credenciales de routers (SNMP v2c/v3, API REST RouterOS, SSH) | PostgreSQL (`devices`), cifradas con KEK por tenant | Crítica | Control total de la red del ISP (de **todos** los ISP si se compromete la plataforma) |
| Separación entre tenants | Código (repositorios, gateway), RLS, tabla de rutas | Crítica | Un ISP ve inventario, clientes y tráfico de otro (riesgo legal y comercial grave) |
| Claves privadas WireGuard (servidor y peers) | PostgreSQL (`wireguard`), cifradas; host WG | Crítica | Acceso a la red de gestión |
| KEK / clave maestra, clave de firma de JWT (`auth`), CA interna de mTLS | Archivo secreto / OpenBao | Crítica | Descifrado de todo lo anterior / suplantación |
| Metadatos de tráfico por IP de cliente (flujos) e IPs descubiertas | ClickHouse, PostgreSQL (`devices.customer`) | Alta (dato personal) | Filtración de hábitos de navegación de miles de clientes de varios ISP |
| Hallazgos de seguridad por cliente (botnets) | PostgreSQL/ClickHouse (`detection`) | Alta | Señalar a una persona como "infectada"; daño reputacional si se filtra |
| Credenciales de destinos remotos de copias (SFTP, Google Drive, MEGA, Dropbox) | PostgreSQL, cifradas | Alta | Lectura/borrado de las copias fuera del servidor |
| Credencial de dispositivo kiosco | Cookie HttpOnly en la pantalla; hash en PostgreSQL | Media | Ver dashboards del tenant asignado (solo lectura) |
| Usuarios, membresías, hashes, secretos TOTP, sesiones | PostgreSQL (`auth`), Valkey (caché) | Alta | Toma de cuentas |
| Registro de auditoría | PostgreSQL + anclas en almacenamiento local/remoto | Alta | Pérdida de trazabilidad / encubrimiento |
| Reglas de clasificación, reputación, scoring | PostgreSQL | Media | Detecciones falsas, ceguera |
| Backups | Disco local del servidor + destino remoto opcional | Crítica (contienen todo lo anterior, de todos los tenants) | Igual que los datos de origen |

### 1.2 Actores de amenaza

1. **Atacante externo en Internet**: si la UI o los colectores quedan expuestos.
2. **Abonado del ISP / dispositivo comprometido en la red de clientes**: puede enviar UDP
   falsificado hacia los colectores si la red no está segmentada.
3. **Router comprometido**: envía flujos/traps maliciosos, explota parsers.
4. **Usuario interno malicioso o curioso** (operador NOC, analista): abuso de acceso a datos de
   clientes, exfiltración de credenciales.
4b. **Usuario de otro ISP (tenant vecino)** — amenaza principal nueva con D6: un usuario legítimo
   del tenant A que intenta (o recibe por error) datos del tenant B: IDs manipulados, cursores o
   topics ajenos, exportaciones, cachés compartidas, mensajes WebSocket mal enrutados.
4c. **Operador de la plataforma** (`platform_admin`, la persona que opera Horus, D7): acceso
   técnico a todos los datos; se limita por diseño (§6.6) y se audita.
4d. **Persona frente a una pantalla NOC** (visitas, cámaras, fotos): ve lo que muestra el kiosco.
5. **Cadena de suministro**: dependencia Go/npm o imagen base comprometida, Action de GitHub
   maliciosa.
6. **Operador de infraestructura con acceso al NAS/servidor o al proveedor de nube**: acceso a
   backups o volúmenes (mitigado por cifrado en cliente, §8.5).
7. **Agente de IA de desarrollo comprometido o equivocado** (D7): introduce un fallo de
   aislamiento o un secreto en el repo; mitigado por revisión por otro agente, CI obligatoria y
   revisión humana de áreas sensibles ([`conventions.md`](conventions.md) §6).

## 2. Zonas de confianza

```
 Internet / red corporativa ISP
        │ 443 (HTTPS, WSS)            ← Zona EDGE: reverse proxy (Traefik, ADR-0013), rate limit
 ┌──────▼───────┐
 │ reverse proxy│
 └──────┬───────┘
        │ red docker "edge"
 ┌──────▼───────┐ HTTP+mTLS (red "app")┌──────────────────────────────────────┐
 │ api-gateway  ├─────────────────────►│ auth, devices, wireguard, analytics… │  Zona APP
 └──────────────┘                      └───────────┬──────────────────────────┘
                                                   │ red "data"
                       ┌───────────────────────────▼──────────────────────────┐
                       │ PostgreSQL · ClickHouse · Valkey · NATS · disco local │  Zona DATA
                       └───────────────────────────▲──────────────────────────┘
                                                   │
 ┌─────────────────────────────────────────────────┴──────────────────────┐
 │ snmp (polling saliente) · flows (UDP 2055/4739/6343) · wireguard-agent │  Zona MGMT
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
| T | XSS inyecta script que actúa con la sesión del usuario | Vue escapa por defecto; prohibido `v-html` sin sanitizar (DOMPurify) — regla ESLint `vue/no-v-html` como error; CSP estricta `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; worker-src 'self' blob:; object-src 'none'; frame-ancestors 'none'; base-uri 'none'` (`'unsafe-inline'` solo en estilos, necesario para los estilos dinámicos de Nuxt UI/ECharts; ECharts renderiza en canvas y no necesita `unsafe-eval`; validar en Sprint 1 con el build real); refresh token en cookie HttpOnly (el JS no puede leerlo); access token de vida corta | 1 |
| R | — | La auditoría vive en el backend | — |
| I | Datos sensibles en `localStorage`, caché del navegador o URLs | Access token solo en memoria; nada sensible en storage del navegador; `Cache-Control: no-store` en `/api/*`; nunca IPs de clientes ni tokens en query strings (búsqueda por IP en el cuerpo, [`api.md`](api.md) §2.9) | 1 |
| D | Consultas analíticas pesadas desde la UI | Límites de rango/tiempo y paginación en la API; rate limit por usuario | 9 |
| E | Ocultar botones como "control de acceso" | La UI solo **refleja** permisos (`/api/v1/me` devuelve permisos efectivos); la decisión siempre es del backend | 2 |

### 3.2 API Gateway (`api-gateway`) y reverse proxy

| STRIDE | Amenaza | Control | Sprint |
|--------|---------|---------|--------|
| S | Robo/fijación de sesión o de tokens | Access token solo en memoria (TTL 10 min); refresh en cookie `__Secure-` HttpOnly SameSite=Strict; nueva sesión (`sid`) en login; refresh rotativo con detección de reutilización; vinculación débil a User-Agent (alerta, no bloqueo) | 2 |
| S | CSRF / cross-site WebSocket hijacking | La API usa `Authorization: Bearer` (no cookies); `/auth/refresh` y `/auth/logout`: SameSite=Strict + `X-Requested-With: horus` + validación de `Origin`; WebSocket con ticket de un uso y `Origin` validado | 2 |
| T | Manipulación de cabeceras de identidad (`X-User-Id`) por el cliente | El gateway **elimina** cualquier cabecera de identidad entrante; la identidad solo viaja como JWT firmado por `auth`, revalidado por cada servicio | 1 |
| R | Acciones sin trazabilidad | `request_id` + `trace_id` en cada petición; auditoría de toda mutación | 2 |
| I | Mensajes de error con trazas internas | Errores RFC 9457 genéricos (formato en [`api.md`](api.md)); detalles solo en logs | 1 |
| D | Fuerza bruta, inundación HTTP/WS | Rate limit en Valkey (token bucket, también por tenant): login 5/min por cuenta + 20/min por IP; API 600/min por sesión; máx. 5 WS por sesión; tamaño máx. de cuerpo 1 MiB (excepto subidas explícitas); timeouts de lectura/escritura | 1–2 |
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
| S | Un servicio suplanta a otro / llamadas gRPC sin identidad | **mTLS** (identidad de servicio en el certificado, autorización por método gRPC según el *caller*) + JWT de usuario verificado por interceptor gRPC común; credenciales separadas por servicio para PostgreSQL/NATS/ClickHouse/Valkey | 1 |
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
| R | — | Métricas por exportador (sin IP de cliente) | 6 |
| I | Communities v2c en texto claro en la red | Migrar a v3; v2c solo dentro de túnel; community distinta por router, nunca `public` | 5 |
| D | Inundación UDP agota CPU / llena NATS | Token bucket por exportador (p. ej. 50k registros/s, configurable); descarte temprano con métrica `horus_flows_packets_dropped_total{reason}`; buffer acotado; JetStream con límites de bytes por stream | 6 |
| E | RCE en el colector da acceso a la red de gestión | Contenedor sin root, rootfs de solo lectura, `cap_drop: ALL` (solo `NET_BIND_SERVICE` si el puerto < 1024, p. ej. trap 162), sin acceso a la red DATA salvo NATS | 5–6 |

### 3.6 WireGuard (`wireguard` + `wireguard-agent`)

Según [`architecture.md`](architecture.md), `wireguard` (plano de control, **sin privilegios**:
estado deseado, claves, IPAM) está separado de `wireguard-agent` (mismo binario en modo agente,
red del host y `CAP_NET_ADMIN`, aplica la config al kernel por netlink y lee handshakes). El
agente solo expone gRPC `WireGuardAgent.ApplyDesiredState` autenticado por mTLS y solo acepta
llamadas de `wireguard`.

| STRIDE | Amenaza | Control | Sprint |
|--------|---------|---------|--------|
| S | Peer robado se conecta como un router legítimo | Rotación de claves; revocación inmediata; alerta si un peer hace handshake desde una IP de origen nueva; AllowedIPs `/32` estrictos por peer | 4 |
| T | Modificación de AllowedIPs para alcanzar otras redes | Solo `wireguard.write`; validación de solapamientos; auditoría con diff | 4 |
| R | — | Auditoría de alta/baja/rotación de peers | 4 |
| I | Fuga de la clave privada del servidor | Clave privada cifrada en reposo; `wireguard-agent` la recibe por mTLS y la aplica al kernel vía `wgctrl` (netlink), sin archivo `wg0.conf` persistente en claro; claves privadas de peers: ver §8.4 | 4 |
| D | Inundación del puerto UDP de WireGuard | WireGuard es silencioso ante paquetes no autenticados (cookies anti-DoS integradas); rate limit nftables | 4 |
| E | Contenedor con `NET_ADMIN` usado para pivotar | `wireguard-agent` es el **único** contenedor con `NET_ADMIN` (sin otras capacidades, sin acceso a PostgreSQL ni a la KEK); `wireguard` corre sin privilegios; el agente no expone API pública (solo gRPC mTLS desde `wireguard`); el frontend jamás ejecuta comandos (vision §Sprint 4) | 4 |

### 3.7 Bases de datos y bus (PostgreSQL, ClickHouse, Valkey, NATS)

| STRIDE | Amenaza | Control | Sprint |
|--------|---------|---------|--------|
| S | Conexión sin autenticación | Contraseñas/llaves por servicio (scram-sha-256 en PG; usuarios ClickHouse; `ACL` de Valkey; usuarios NATS con nkeys) | 1 |
| T | Publicación de eventos falsos en NATS | Permisos de *subject* por usuario NATS: cada servicio solo publica en `horus.<su-dominio>.>` y se suscribe a lo que consume | 1 |
| R | Borrado de auditoría | Ver §7 | 2 |
| I | Lectura de volúmenes/backups | Cifrado de disco en el host (LUKS) recomendado; backups cifrados (pgBackRest `repo-cipher-type=aes-256-cbc`, clickhouse-backup cifrado en cliente, §8.5) | 1 / 13 |
| D | Una consulta o consumidor lento tumba el bus | Límites por stream/consumer (`max_bytes`, `max_ack_pending`); perfiles de recursos en ClickHouse; `maxmemory` + política en Valkey | 1–6 |
| E | Usuario de app con `SUPERUSER` | Prohibido; migraciones con un rol `*_migrator` distinto del rol de ejecución | 1 |

Valkey en concreto (D3): un usuario ACL por servicio (`ACL SETUSER gateway ~rl:* ~ws_ticket:* ~session_revoked:* ~idem:* ~wcache:* …`)
con comandos peligrosos denegados (`-@dangerous`, sin `FLUSHALL`, `CONFIG`, `KEYS`, `DEBUG`); claves de datos de
negocio con prefijo de tenant (`wcache:t:<tenant_id>:…`, `rl:t:<tenant_id>:…`) para que un error de clave no cruce
ISPs; nada secreto que deba sobrevivir (tickets y revocaciones caducan; sin persistencia obligatoria); sin puerto
publicado; TLS si cruza de host.

### 3.8 Almacenamiento local y destinos remotos (D2)

| STRIDE | Amenaza | Control | Sprint |
|--------|---------|---------|--------|
| S | Alguien suplanta el destino remoto (SFTP con *host key* cambiada) | `known_hosts` fijado al dar de alta el destino (TOFU con confirmación del `platform_admin`); TLS verificado para APIs de nube | backups |
| T | Ransomware o atacante con root en el servidor borra copias locales y remotas | Copias remotas **cifradas en cliente** y, cuando el destino lo permite, sin permiso de borrado (SFTP enjaulado con usuario que sólo escribe; versionado de Drive/Dropbox; preferido: modo *pull* en el que el NAS recoge por SFTP y el servidor no tiene credencial sobre el NAS); retención en destino gestionada por el destino | backups |
| I | El proveedor de nube o quien robe el NAS lee las copias | Cifrado en cliente (pgBackRest `repo-cipher-type`, `age`/rclone `crypt` para el resto) con clave distinta de la KEK y custodiada fuera del servidor ([`disaster-recovery.md`](disaster-recovery.md) §3.6) | backups |
| I | Reportes exportados con datos personales accesibles | Descarga **sólo** a través de la API (`/reports/{id}/download`), autorizada por `tid` y permiso, sin URLs prefirmadas; archivos bajo `reports/<tenant_id>/`; nunca en el destino remoto sin cifrar | 12 |
| D | Destino remoto lento o caído llena el disco local | Las copias locales tienen retención propia e independiente del envío; el envío reintenta con backoff y alerta; nunca bloquea a PostgreSQL ni a la ingesta | backups |
| E | Credencial de nube con más permisos de los necesarios | OAuth con alcance mínimo (Google Drive `drive.file`; Dropbox "App folder"); cuenta dedicada; MEGA con cuenta exclusiva para Horus (§8.5) | backups |

### 3.9 Aislamiento entre tenants (amenaza principal de D6)

Modelo: **base de datos y servicios compartidos, aislamiento lógico** (*pool model*). Cada fila de negocio lleva
`tenant_id`; cada token y cada mensaje también ([ADR-0017](adr/0017-multi-tenant-desde-v1.md)). La pregunta de diseño es "¿cuántas cosas tienen que fallar a la vez
para que A vea datos de B?"; el objetivo es **al menos dos**.

| Capa | Control primario | Segunda barrera |
|------|------------------|-----------------|
| REST | **Access token por tenant** (`tid`), emitido por `auth` sólo a miembros; el tenant nunca sale de path/query/header/body; un `tenant_id` en el body ≠ `tid` → `403` ([`api.md`](api.md) §0) | El módulo dueño vuelve a comprobar permiso fino con alcance dentro del `tid` |
| Repositorios PostgreSQL | Toda consulta de negocio recibe un `TenantScope` (tipo obligatorio en la firma; no existe `FindByID(id)` sin tenant); `WHERE tenant_id = $1` siempre | **RLS** (`ENABLE` + `FORCE`) con política `tenant_id = current_setting('horus.tenant_id')::uuid`, fijado con `SET LOCAL` desde `tid` al abrir la transacción (sin fijar → cero filas); el rol de la aplicación **no** tiene `BYPASSRLS`; los jobs multi-tenant (relay del outbox, scoring, snapshots) usan un rol separado, acotado y nunca expuesto a rutas HTTP ([ADR-0017](adr/0017-multi-tenant-desde-v1.md) §4, [`database.md`](database.md)) |
| ClickHouse | Consultas con `tenant_id` como primer filtro (primera columna del `ORDER BY`) | *Row policy* para el usuario de lectura de la API: `USING tenant_id = getSetting('SQL_horus_tenant')`, con el ajuste fijado por consulta; sin él, la consulta no devuelve filas |
| Caché (Valkey) y cachés en memoria | Claves de datos de tenant con prefijo `t:<tenant_id>:`; cachés en memoria con el tenant en la clave | Tests de propiedad: la misma clave lógica en dos tenants nunca colisiona |
| WebSocket | Conexión ligada al `tid` del token; el hub enruta por la cabecera `Horus-Tenant` | Mensajes sin cabecera o con cabecera ≠ sobre se descartan y cuentan (alerta si > 0) |
| NATS | `tenant_id` obligatorio en el sobre y cabecera `Horus-Tenant` obligatoria; una sola cuenta ([`events.md`](events.md) §2.4) | El consumidor valida ambos; ausencia o discrepancia ⇒ DLQ + alerta; el handler fija `SET LOCAL` (RLS) |
| Llamadas internas (gRPC o en proceso) | El `tid` del JWT viaja en el contexto; los métodos multi-tenant de servicio están declarados y acotados | Respuestas con `tenant_id` por elemento |
| Archivos (reportes, exportaciones, archivo) | Rutas `reports/<tenant_id>/`, `archive/<tenant_id>/`; descarga **sólo** por la API (sin URLs prefirmadas) | El handler de descarga revalida `tid` y permiso |
| Secretos | AAD de la *envelope encryption* incluye `tenant_id`; KEK por tenant (§8.2) | Un secreto copiado a otro tenant no descifra |
| Logs/métricas/Grafana | `tenant_id` en logs; Grafana **sólo** para la plataforma, nunca expuesto a los ISP | — |

STRIDE específico:

| STRIDE | Amenaza | Control | Sprint |
|--------|---------|---------|--------|
| S | Un usuario de A obtiene un token de B | `POST /auth/token` sólo emite tokens de tenants con membresía activa (o acceso de soporte vigente); API tokens y kioscos nacen ligados a un tenant | 2 |
| T | Un cuerpo de petición referencia `site_id`/`router_id` de otro tenant | Validación de referencias dentro del tenant (`422 NOT_FOUND`), FK compuestas `(tenant_id, id)` donde el Agente B lo permita | 3 |
| R | Un ISP niega haber visto/cambiado algo; o el soporte de plataforma actúa sin que el ISP lo sepa | Auditoría con `tenant_id`; acceso de soporte visible en la auditoría **del tenant** (§6.6) | 2 |
| I | IDOR entre tenants, cursor reutilizado, caché compartida, exportación con filtro mal construido, evento WS mal enrutado | Controles de la tabla anterior + batería automática "A no ve B" (abajo) | 1+ |
| I | Canal lateral: tiempos o mensajes de error revelan que un recurso existe en otro tenant | `404` idéntico para "no existe" y "es de otro tenant"; mismos tiempos (la consulta incluye siempre el tenant) | 2 |
| D | Vecino ruidoso: un ISP agota CPU, ClickHouse, el bus o el cupo de API | Cuotas por tenant (flujos/s, routers, clientes, req/min, conexiones WS); perfiles de ClickHouse con `max_execution_time`/`max_memory_usage`; descarte por cuota en el collector con evento | 6 |
| E | Un `tenant_admin` se concede permisos de plataforma o acceso a otro tenant | Las membresías sólo las crea un admin **de ese tenant** o la plataforma; los roles de plataforma sólo los asigna `platform_admin` con re-auth; nadie se asigna a sí mismo | 2 |

**Batería automática de aislamiento (obligatoria, bloquea el merge):** fixture con dos tenants (A y B) con datos
equivalentes; para **cada operación** del bundle OpenAPI con `scope: tenant` se ejecuta con el token de A: (1) contra
recursos de B por ID (`404` esperado), (2) con `tenant_id` de B en el cuerpo cuando el esquema lo admite
(`403 TENANT_MISMATCH`), (3) listados de A que no deben contener IDs de B; `POST /auth/token` con el tenant B para un
usuario sólo de A (`404`); para cada topic WebSocket, que una conexión de A no recibe eventos de B; para cada
consumidor NATS, que un mensaje sin `Horus-Tenant` o con cabecera ≠ sobre acaba en DLQ; test de arquitectura: toda
tabla con `tenant_id` tiene política RLS. El generador de casos lee la spec,
así que un endpoint nuevo queda cubierto sin escribir el test a mano ([`conventions.md`](conventions.md) §11).

### 3.10 Dashboards y kioscos (D8)

| STRIDE | Amenaza | Control | Sprint |
|--------|---------|---------|--------|
| S | Robo de la credencial de un kiosco (acceso físico al navegador de la pantalla) | Cookie HttpOnly rotativa con detección de reutilización; CIDR permitido; revocación inmediata; caducidad (180 días por defecto) | dashboards |
| S | Enrolamiento de una pantalla no autorizada | Código de 8 caracteres, un uso, 10 min, generado con re-auth por `kiosks.manage`; rate limit por IP y por código; el canje respeta el CIDR | dashboards |
| T | Un dashboard compartido amplía los permisos de quien lo mira | Los datos de widgets se calculan con los permisos y alcance **del espectador** ([`api.md`](api.md) §2.11); el autor no presta sus permisos | dashboards |
| T | Inyección de consultas a través de la `config` de un widget | `config` validada contra el JSON Schema del tipo; consultas parametrizadas construidas en servidor; no hay campo de consulta libre | dashboards |
| I | Visitas/cámaras ven IPs de clientes o hallazgos de seguridad en la pantalla | Kioscos sin datos personales por defecto (`show_personal_data=false`); widgets con `contains_personal_data` deshabilitados o enmascarados; activar requiere `kiosks.manage` + motivo auditado | dashboards |
| D | Muchas pantallas refrescando cada pocos segundos | Caché de datos de widget compartida por `(tenant, tipo, config, rango, alcance)`; mínimo `refresh_seconds` 10; rate limit por kiosco | dashboards |
| E | El JWT de kiosco alcanza rutas no previstas | Lista blanca `principals: [kiosk]` en la tabla del gateway; todo lo demás `403 KIOSK_FORBIDDEN`; test de CI que enumera rutas accesibles a kiosco | dashboards |

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

**Recomendación (S1), revisada con D6/D7:** se mantiene el servicio `auth` propio. Con varios ISP
el número de usuarios sigue siendo bajo (decenas a pocos cientos), el valor está en la
autorización por tenant/nodo/grupo, que ningún IdP resuelve, y una instalación de un solo servidor
operada por IA + 1 persona (D7) no debe cargar con un componente Java crítico más. El modelo
multi-tenant (usuario de plataforma + membresías) es pequeño y se implementa en `auth`. El
disparador para reevaluar Zitadel (multi-tenant nativo) es que algún ISP exija SSO/SAML con su
propio IdP o que se superen ~500 usuarios.

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
- Revisión de la decisión (ADR): si llega SAML/SSO por ISP o >500 usuarios, evaluar migrar a
  Zitadel (Go + PostgreSQL, multi-tenant nativo, encaja con el stack). La federación sería **por
  tenant** (cada ISP con su IdP), nunca un IdP que conceda acceso a varios ISP.

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

### 5.1 Recomendación para la SPA Nuxt: access JWT corto en memoria + refresh rotativo en cookie HttpOnly

Opciones evaluadas:

| | A. Access JWT en memoria JS + refresh en cookie HttpOnly | B. Access JWT + refresh, ambos en cookies HttpOnly | C. Sesión opaca en cookie (BFF puro) |
|---|---|---|---|
| Robo del token por XSS | El access token puede exfiltrarse y usarse hasta su `exp` (10 min) | No exfiltrable (el XSS solo actúa mientras la página está abierta) | No exfiltrable |
| CSRF | No aplica a la API (cabecera `Authorization`); solo `/auth/refresh` necesita defensa | Toda mutación necesita token CSRF + `Origin` | Toda mutación necesita token CSRF + `Origin` |
| Validación en gateway y servicios | Local (JWKS) | Local (JWKS) | Búsqueda en caché/PG por petición; necesita un token interno aparte para los servicios |
| Funciona con PostgreSQL caído ([`architecture.md`](architecture.md) §10.6) | Sí, hasta `exp` | Sí, hasta `exp` | Solo si la sesión está en caché |
| Clientes no-navegador | Mismo mecanismo | Necesita variante con cabecera | Necesita variante |
| Revocación | Por `sid` (caché de revocados) | Por `sid` | Inmediata |

**Decisión (S4): opción A**, que es además la que ya asumen [ADR-0012](adr/0012-nuxt4-nuxt-ui.md),
[`architecture.md`](architecture.md) y [`api.md`](api.md) §1.11. B ofrece algo más de protección
frente a la exfiltración del access token, pero obliga a CSRF en cada mutación; con TTL de
10 min, `sid` revocable y la CSP estricta de §3.1, el riesgo residual de A es aceptable. Se
reconsidera B si una auditoría de seguridad lo pide.

```
Navegador ──Authorization: Bearer <access JWT>──► Traefik ──► api-gateway
                                                    │ 1. verifica firma/exp/aud (JWKS de auth, caché)
                                                    │ 2. revocación: session_revoked:<sid> en Valkey
                                                    │    (si no responde: gRPC auth.CheckSession, caché 30 s)
                                                    │ 3. permiso grueso por ruta, rate limit
                                                    ▼ proxy HTTP al REST del servicio (mTLS), Authorization: Bearer <mismo JWT>
                                         auth / devices / wireguard / … (revalidan firma, exp, aud, permisos)
```

- **Access token:** JWT firmado por `auth` con **Ed25519 (EdDSA)**, TTL **10 min**. Claims:
  `iss` (URL de `auth`), `aud=horus-api`, `sub` (UUIDv7 del usuario de plataforma, o
  `kiosk:<id>`), `typ` (`user` | `kiosk` | `service`), `scope` (`session` | `tenant` |
  `platform`), `sid`, **`tid`** (tenant del token, sólo con `scope=tenant`; [ADR-0017](adr/0017-multi-tenant-desde-v1.md)
  §3), `via_platform` (true si es acceso de soporte), `amr`, `auth_time`, `iat`, `exp`, `jti`,
  `perms` (permisos efectivos **en ese tenant** —o de plataforma— con alcance, p. ej.
  `{"devices.read":["site:018f…"],"users.manage":["*"]}`). Si superara 4 KiB se sustituye por
  `perms_ver` y los módulos resuelven permisos vía `auth` con caché (60 s). La **sesión** es del
  usuario (sin tenant); cada pestaña pide su token de tenant con `POST /api/v1/auth/token`.
  Quitar una membresía surte efecto en ≤ 5 s aunque el token siga vivo: el gateway consulta
  `membership_revoked:<user>:<tenant>` en Valkey (como la revocación de sesiones).
  Rotación de clave de firma cada 90 días con `kid`; JWKS publica la actual y la anterior.
- **En el navegador:** el access token vive **solo en memoria** (variable del store de sesión,
  nunca `localStorage`/`sessionStorage`/cookies legibles). Al recargar la página, la SPA llama a
  `POST /api/v1/auth/refresh` para obtener uno nuevo.
- **Refresh token:** opaco de 256 bits en cookie
  `__Secure-hf_rt=<token>; Path=/api/v1/auth; Secure; HttpOnly; SameSite=Strict` (prefijo
  `__Secure-` porque `__Host-` exige `Path=/`). En BD solo `SHA-256(token)`; un solo uso;
  inactividad 12 h y **vida absoluta 7 días** (por defecto; configurable por rol). Organizado por
  familia = `sid`: cada uso emite access + refresh nuevos e invalida el anterior. Si se presenta
  un refresh **ya usado** (reutilización) se revoca la familia completa (la sesión), se notifica
  al usuario y se audita como incidente (`reason=refresh_token_reuse` en
  `horus.auth.session.revoked`, [`events.md`](events.md)). Tolerancia de 10 s para el refresh
  anterior (carreras entre pestañas): dentro de ese margen se devuelve el mismo par recién emitido.
- **CSRF:** la API no usa cookies de autenticación, así que no es vulnerable a CSRF. Para
  `/api/v1/auth/refresh` y `/logout` (que sí usan la cookie): `SameSite=Strict` + cabecera
  obligatoria `X-Requested-With: horus` ([`api.md`](api.md)) + validación de `Origin` contra la
  lista permitida.
- **WebSocket:** ticket de un solo uso (TTL 30 s) obtenido con el access token, `Origin`
  validado y renovación en banda con el token nuevo ([`api.md`](api.md) §4.2). La revocación
  cierra la conexión (código `4409`).
- **Re-autenticación reciente** (`auth_time` ≤ 5 min) para: revelar credenciales, cambiar roles,
  generar/rotar claves WireGuard, desactivar 2FA, crear API tokens.
- Las pantallas NOC "de pared" usan el **modo kiosco** (§5.5), no sesiones de usuario.

### 5.2 Sesiones revocables

- **Fuente de verdad:** tabla `sessions` de `auth` en PostgreSQL (`id`=`sid`, `user_id`,
  `created_at`, `last_seen_at`, `expires_at`, `ip`, `user_agent`, `amr`, `revoked_at`,
  `revoked_reason`) + `refresh_tokens`. Esquema definitivo: [`database.md`](database.md).
- **Valkey** solo como caché de revocación: `session_revoked:<sid>` con TTL = vida máxima
  del access token ([ADR-0009](adr/0009-redis.md)).
- UI de "sesiones activas" (Sprint 2): el usuario y el admin (`sessions.manage`) pueden revocar.
- Al revocar: marcar en PostgreSQL, escribir la clave de revocación, publicar
  `horus.auth.session.revoked` → el gateway cierra los WebSocket de ese `sid` en < 5 s.
  Ventana máxima en que un token revocado puede seguir aceptado: 0 s con Valkey sano; 30 s si
  está caído (fallback a `auth` con caché). Los servicios que hacen operaciones críticas
  (`wireguard`, revelado de credenciales, cambios de roles) consultan además
  `auth.CheckSession` por gRPC sin caché.
- Desactivar un usuario, cambiar su contraseña o resetear su 2FA revoca todas sus sesiones y
  API tokens. Cambiar sus roles fuerza un refresh (los permisos nuevos entran en ≤ 10 min, o
  inmediatamente con revocación si se le quitan permisos).

### 5.3 Identidad entre servicios

- **Transporte:** gRPC con **mTLS** desde el Sprint 1 ([ADR-0005](adr/0005-grpc-protobuf-interno.md)).
  CA interna: `step-ca` (Smallstep) en un contenedor; certificados de servicio con
  `SAN = <servicio>.horus.internal`, vida 30 días en v1 renovados por `step ca renew --daemon`
  (24 h al pasar a Kubernetes con cert-manager o mesh). En desarrollo, script que genera una CA
  local y los certificados.
- **Autorización de *caller*:** cada servicio declara qué servicios pueden invocar cada método
  gRPC (p. ej. solo `svc:snmp` puede llamar `devices.CredentialService/Resolve`; solo
  `svc:wireguard` puede llamar `WireGuardAgent.ApplyDesiredState`), evaluado con la identidad del
  certificado del cliente.
- **Llamadas en nombre de un usuario:** se reenvía el mismo access JWT; el servicio lo revalida.
- **Llamadas sin usuario** (workers, jobs, consumidores NATS): identidad = certificado mTLS del
  servicio; si se necesita un JWT (por uniformidad del interceptor), `auth` emite un JWT de
  servicio (`sub=svc:<nombre>`, permisos fijos, TTL 10 min) contra el certificado mTLS.
- El JWT nunca se escribe en logs, eventos ni trazas.

### 5.4 Clientes no-navegador: API tokens

- **API tokens / cuentas de servicio** (integraciones del ISP, scripts): token opaco
  `hf_pat_<32 B base62>`, almacenado como SHA-256, con alcance de permisos ⊆ permisos del
  creador, caducidad obligatoria (máx. 1 año), último uso visible, revocable. El gateway lo
  intercambia por un access JWT de corta vida (caché por token) antes de llamar a los servicios.
- Una futura app o CLI con login interactivo usará el mismo par access + refresh rotativo vía
  OIDC Authorization Code + PKCE (§4.1).

### 5.5 Modo kiosco para pantallas NOC (D8)

Resuelve Q19. Contrato en [`api.md`](api.md) §2.12; aquí el análisis.

**Opciones evaluadas:**

| Opción | Riesgo principal | Decisión |
|--------|------------------|----------|
| Usuario "pantalla" con contraseña y sesión larga | Credencial reutilizable desde cualquier sitio; 2FA imposible en una TV; sesión de 7 días caduca igual | No |
| Token de solo lectura de larga vida en la URL (`/noc?token=…`) | Acaba en historial, logs de proxies, capturas, `Referer`; quien lo copie tiene acceso indefinido desde cualquier red; no rotable sin tocar la pantalla | **No** |
| **Dispositivo registrado**: código de enrolamiento de un uso → credencial de dispositivo HttpOnly rotativa → JWT corto de solo lectura | Robo físico del navegador de la pantalla (acotado por CIDR, rotación con detección de reutilización y revocación) | **Sí** |
| Certificado de cliente mTLS en el dispositivo | El más fuerte; exige gestionar certificados en TVs/mini-PC | Evolución para videowalls gestionados |

**Controles:**

1. **Enrolamiento**: un `tenant_admin` (o rol con `kiosks.manage`, con re-auth) genera un
   código de 8 caracteres (alfabeto sin ambigüedades, ~40 bits), un solo uso, 10 min. El QR lleva
   el código en el **fragmento** de la URL (`#code=`), que el navegador no envía al servidor. 10
   intentos fallidos invalidan el código; rate limit por IP.
2. **Credencial de dispositivo**: 256 bits en cookie `__Secure-hf_kiosk` (`HttpOnly; Secure;
   SameSite=Strict; Path=/api/v1/kiosk`), rotativa en cada canje por JWT, con detección de
   reutilización (si se presenta una ya usada, se revoca el kiosco y se alerta: indica copia de la
   cookie). En BD sólo SHA-256. Caducidad absoluta configurable (180 días por defecto) y por
   inactividad (14 días sin uso).
3. **JWT de kiosco**: 10 min, `typ=kiosk`, `tid` = su tenant, sin permisos de escritura; el gateway
   sólo lo acepta en las rutas con `principals: [kiosk]` y sólo para los dashboards asignados.
4. **Red**: `allowed_cidrs` (recomendado: la red del NOC) aplicado en el canje del código, en el
   canje del JWT y en cada petición. Sin CIDR, la UI de administración lo marca como "riesgo".
5. **Datos personales**: por defecto el kiosco no recibe IPs de clientes ni detalle de hallazgos
   por cliente (sólo conteos y estados agregados); activarlo es explícito, con motivo auditado.
6. **Revocación**: inmediata (evento `horus.auth.kiosk.revoked` → cierre del WebSocket con `4409`
   en < 5 s); también al suspender el tenant.
7. **Auditoría**: enrolamiento, canjes (con IP), cambios de dashboards asignados, revocación.
   `last_seen_at`/`last_ip` visibles al administrador; alerta si un kiosco aparece desde una IP
   nueva.
8. **Interfaz**: la ruta `/kiosk` del frontend no tiene navegación, ni enlaces a otras páginas, ni
   formularios; CSP igual que el resto.

Riesgo residual aceptado: quien tenga acceso físico prolongado a la pantalla dentro del CIDR ve los
mismos dashboards que ya se ven en la pantalla.

## 6. Autorización: RBAC + ACL

### 6.1 Catálogo de permisos `recurso.accion`

Contrato v0 (C7): `packages/schemas/permissions/v0/permissions.yaml` (permisos, roles de sistema, token de kiosco y
equivalencias con los nombres del backlog: `isp_admin`→`tenant_admin`, `isp_operator`→`noc`, `isp_viewer`→`viewer`,
`superadmin`→`platform_admin`). El catálogo canónico vive en código (`auth`) y se expone en `GET /api/v1/permissions`. Hay dos familias: permisos
**de tenant** (se conceden dentro de una membresía y se evalúan en el tenant de la ruta) y permisos **de plataforma**
(prefijo `platform.`, sólo en roles de plataforma).

Permisos de tenant:

| Recurso | Acciones |
|---------|----------|
| `users` | `read`, `manage` (miembros del tenant e invitaciones) |
| `roles` | `read`, `manage`, `assign` |
| `audit` | `read`, `export` (auditoría del tenant) |
| `sites` | `read`, `create`, `update`, `delete` (nodos) |
| `devices` | `read`, `create`, `update`, `delete` |
| `devices.credentials` | `write`, `reveal` (revelar en claro; requiere 2FA + re-auth) |
| `wireguard` | `read`, `write`, `keys.rotate` |
| `snmp` | `read`, `manage` (perfiles, intervalos; también sondeo por API RouterOS) |
| `flows` | `read` (exportadores), `manage` |
| `customers` | `read` (lista y ficha de clientes = IPs, dato personal), `update` (alias/notas), `kind.write` (cambio manual de tipo, desbloqueo, reinicio), `export` |
| `traffic` | `read` (agregados por nodo/router/categoría), `customer.read` (tráfico de un cliente concreto — dato personal, auditado) |
| `traffic.catalog` | `read` (el catálogo es de plataforma; los tenants sólo lo leen) |
| `sites` (prefijos de clientes) | los prefijos de clientes del realm se gestionan con `sites.update` |
| `security.findings` | `read` (hallazgos, estado de seguridad por cliente, feeds, allowlist), `manage` (reconocer, resolver, falso positivo, allowlist del tenant) |
| `security.evidence` | `read` (flujos de evidencia de un cliente; auditado) — [ADR-0024](adr/0024-deteccion-de-botnets-como-objetivo-principal.md) §3 |
| `alerts` | `read`, `ack`, `manage` (reglas, canales de notificación email/Telegram del I1 — D13) |
| `reports` | `read`, `export` |
| `dashboards` | `read` (ver compartidos, crear privados), `manage` (dashboards y rotaciones compartidos con el tenant) |
| `kiosks` | `manage` |
| `settings` | `read`, `manage` (configuración del tenant: inactividad de clientes, reglas de tipo, retenciones dentro de los límites de plataforma) |
| `api_tokens` | `manage` (propios, ligados al tenant) |

Permisos de plataforma: `platform.tenants.read`, `platform.tenants.manage`,
`platform.users.read`, `platform.users.manage`, `platform.support_access`, `platform.audit.read`,
`platform.status.read`, `platform.storage.manage`, `platform.catalog.manage` (catálogo de clasificación de tráfico y
feeds de reputación, comunes a todos los ISP; [ADR-0017](adr/0017-multi-tenant-desde-v1.md) §2).

Cambios respecto al Sprint 0: `subscribers.*` → `customers.*` (D1: no hay ficha de abonado con nombre/dirección);
`traffic.client.read` → `traffic.customer.read`; `traffic.catalog.write/publish` pasan a plataforma; `sessions.*`
pasa a plataforma (las sesiones son de la persona, no del tenant). Equivalencias con [`frontend.md`](frontend.md):
`clients.read` → `customers.read`; `analytics.read` → `traffic.read`; `system.*` → `settings.*` (tenant) o
`platform.status.read`.

### 6.2 Roles predefinidos

Roles **de tenant** (plantillas de sistema que cada tenant recibe; no se borran ni editan; cada tenant puede crear
roles propios):

| Rol | Propósito | Permisos (resumen) | 2FA |
|-----|-----------|--------------------|-----|
| `tenant_admin` | Administración del ISP en Horus | Todos los de tenant (incl. `devices.credentials.reveal`, `kiosks.manage`) | Obligatorio |
| `security_analyst` | Botnets y seguridad de clientes (D5) | `security.findings.*`, `security.evidence.read`, `customers.read`, `traffic.read`, `traffic.customer.read`, `alerts.*`, `audit.read`, `dashboards.read` | Obligatorio |
| `network_engineer` | Nodos, routers, WireGuard, SNMP/API, flujos | `sites.*`, `devices.*`, `devices.credentials.write`, `wireguard.*`, `snmp.*`, `flows.*`, `alerts.read/ack`, `traffic.read`, `dashboards.*` | Obligatorio |
| `noc` | Monitoreo 24/7 | `sites.read`, `devices.read`, `wireguard.read`, `snmp.read`, `flows.read`, `traffic.read`, `security.findings.read`, `alerts.read/ack`, `reports.read`, `dashboards.read` | Recomendado |
| `analyst` | Analítica y clasificación de clientes | `traffic.read`, `traffic.customer.read`, `customers.read`, `customers.kind.write`, `reports.read/export`, `devices.read`, `sites.read`, `dashboards.*` | Recomendado |
| `auditor` | Revisión de cumplimiento del ISP | `audit.read/export`, `users.read`, `roles.read` | Recomendado |
| `viewer` | Solo lectura sin datos personales | `*.read` excepto `customers.read`, `traffic.customer.read`, `security.evidence.read`, `audit.read`, `devices.credentials.*` | Opcional |

Roles **de plataforma** (no son de ningún tenant):

| Rol | Propósito | Permisos | 2FA |
|-----|-----------|----------|-----|
| `platform_admin` | Operar Horus: altas/bajas de ISP, nodos, usuarios, copias, catálogo | Todos los `platform.*` salvo `platform.support_access` (se concede aparte, aunque sea a la misma persona, para que su uso sea explícito) | Obligatorio + llave de seguridad recomendada |
| `platform_operator` | Salud de la plataforma (y agentes de IA de operación, D7) | `platform.status.read`, `platform.tenants.read`, `platform.audit.read` | Obligatorio |
| `platform_auditor` | Revisión | `platform.audit.read`, `platform.tenants.read`, `platform.users.read` | Obligatorio |

Nota: **nadie** tiene `devices.credentials.reveal` por defecto salvo `tenant_admin`. Ningún rol de plataforma da
acceso a clientes, tráfico, hallazgos ni credenciales de un tenant (§6.6).

### 6.3 ACL por alcance

- Una **membresía** es `(usuario, tenant)` con una o varias **asignaciones** `(rol, alcance)`, alcance `tenant` |
  `site:<uuid>` | `router_group:<uuid>` (siempre dentro de ese tenant). Un usuario puede ser miembro de varios
  tenants con roles distintos en cada uno.
- Permisos efectivos **por tenant** = unión de las asignaciones de esa membresía. Nunca se suman permisos de dos
  tenants.
- Recursos del tenant sin nodo (miembros, roles, dashboards compartidos, kioscos) sólo admiten alcance `tenant`.
- Clientes y datos de tráfico heredan el alcance del nodo/router que los observó.

### 6.4 Dónde se evalúa

1. **Gateway (grueso):** autenticación, sesión válida (o kiosco vigente), 2FA cumplido si el rol
   lo exige, **pertenencia al tenant de la ruta**, y "¿tiene el permiso X en *algún* alcance de
   ese tenant?" según la tabla declarativa ruta→permiso. Rechaza pronto (404/403) y reduce carga.
2. **Servicio dueño (fino, obligatorio):** middleware HTTP e interceptor gRPC comunes (`packages/go/authz`) que
   valida mTLS + el access JWT (firma, `exp`, `aud`) y expone `authz.Require(ctx, "devices.update", scope)`; el
   **repositorio** filtra por tenant **y** alcance (`WHERE tenant_id = @tenant AND site_id =
   ANY(@allowed_sites)`), con RLS como segunda barrera (§3.9). Esto evita IDOR aunque el gateway
   tenga un error.
3. **Asíncrono (NATS):** los consumidores no reevalúan permisos de usuario; actúan como el
   servicio (`actor` queda registrado). La autorización se hizo al aceptar el comando original.
   Las acciones disparadas por eventos que modifican la red (p. ej. revocar peer por detección)
   requieren una política de servicio explícita y auditoría con `actor.type=system`.

### 6.5 Propagación de identidad

| Canal | Mecanismo |
|-------|-----------|
| Navegador → gateway | `Authorization: Bearer <access JWT>`; WebSocket con ticket de un uso |
| Gateway → servicio (proxy HTTP al REST del servicio, mTLS; [ADR-0013](adr/0013-api-gateway-propio.md)) | Header `Authorization: Bearer <access JWT>` + `traceparent` + `x-request-id` |
| Servicio → servicio (gRPC, mTLS) | Reenvía el JWT del usuario si actúa en su nombre; si no, identidad del certificado (y JWT de servicio si hace falta) |
| Servicio → NATS | Campo `actor` del envelope (definido por el Agente 3 en [`events.md`](events.md)) |
| Integración → gateway | `Authorization: Bearer hf_pat_…` → el gateway lo cambia por un access JWT de corta vida |

El sobre de eventos ([`events.md`](events.md) §5.1) ya recoge lo que pedía seguridad: `actor` con
`type` (`user`/`service`/`system`/`kiosk`), `id`, `sid` opcional, `via` (`session`/`api_token`),
`via_platform`, `platform_role` y `tenant_id` (más la cabecera `Horus-Tenant`); `ip`/`user_agent` **sólo** en
`*.audit.recorded`; sin nombres, tokens ni permisos.

### 6.6 Acceso de soporte de la plataforma a un tenant

La persona que opera Horus (D7) y los agentes de IA de operación necesitan a veces mirar datos de
un ISP para diagnosticar. Regla: **ningún rol de plataforma ve datos de negocio de un tenant por
defecto**.

- `POST /platform/tenants/{id}/support-access` (permiso `platform.support_access`, re-auth, 2FA):
  motivo obligatorio, duración ≤ 4 h, alcance de lectura (escritura sólo si el motivo lo exige y se
  marca explícitamente).
- El tenant puede fijar `support_access_policy`: `notify` (por defecto: se concede y se notifica al
  `tenant_admin`), `require_approval` (el `tenant_admin` aprueba) o `deny`.
- Durante el acceso, `POST /auth/token` emite un token del tenant con `via_platform = true`; todo
  queda en la auditoría del tenant **y** en la de plataforma.
- Los agentes de IA de operación usan `platform_operator` (sin datos de tenants); nunca se les
  concede acceso de soporte de forma automática.

## 7. Auditoría

### 7.1 Qué se audita (mínimo)

- **Autenticación:** login OK/fallido (motivo genérico), logout, 2FA alta/baja/fallo, uso de
  código de recuperación, reset de contraseña solicitado/completado, bloqueo progresivo,
  reutilización de refresh token, creación/revocación de API tokens, revocación de sesiones.
- **Autorización/administración:** alta/baja/cambio de usuarios, membresías, roles, asignaciones
  y alcances (con diff antes/después), cambios en `settings`; roles de plataforma.
- **Plataforma y tenants:** alta, suspensión, baja y purga de tenants; cambios de cuotas;
  **accesos de soporte** (inicio, fin, motivo); alta/edición/prueba de destinos remotos de copias
  (sin secretos).
- **Kioscos:** creación, códigos de enrolamiento emitidos y canjeados (IP), cambios de dashboards
  asignados o de la política de datos personales, revocación, reutilización de credencial.
- **Inventario y red:** crear/editar/borrar sitios, routers, grupos; escritura y **revelado**
  de credenciales (sin el valor); WireGuard: alta/baja/rotación/revocación de peers y servidores,
  cambios de AllowedIPs.
- **Datos personales:** acceso a la ficha y al tráfico de un cliente (`customers.read` en
  detalle, `traffic.customer.read`), búsquedas por IP (`customers/lookup`; se registra el hecho, no
  la IP buscada en claro sino su HMAC), exportaciones (quién, qué filtro, cuántas filas), cambios
  manuales de tipo de cliente (con motivo), purgas, descargas de backups.
- **Seguridad:** cambios de reglas de detección/clasificación/alertas, ack/cierre de alertas
  de seguridad.
- **Plataforma:** arranque con configuración insegura (p. ej. TLS desactivado), rotación de
  claves (KEK, firma JWT), restauraciones de backup.

### 7.2 Formato del registro

`id` (UUIDv7), `tenant_id` (`null` para acciones de plataforma), `occurred_at` (UTC), `actor`
(`type`, `id`, `sid`, `platform_role`), `via_platform`, `ip`, `user_agent`,
`action` (p. ej. `devices.credentials.reveal`), `resource_type`, `resource_id`, `scope`
(sitio), `outcome` (`success` | `denied` | `error`), `reason`, `changes` (diff JSON sin
secretos), `request_id`, `trace_id`, `prev_hash`, `hash`. Esquema en [`database.md`](database.md)
(Agente 2).

### 7.3 Escritura e inmutabilidad

1. Cada servicio escribe el evento de auditoría en su **outbox** dentro de la misma transacción
   que el cambio de negocio (no se pierde si NATS cae) y lo publica en
   `horus.<dominio>.audit.recorded` (convención de [`architecture.md`](architecture.md) §6.1;
   stream y retención definidos por el Agente 3 en [`events.md`](events.md), propuesta 7 días).
2. `auth` es el **único escritor** de la tabla `audit_log`: consume el stream y añade cada
   registro calculando `hash = SHA-256(prev_hash ‖ JSON canónico del registro)`.
3. Inmutabilidad en PostgreSQL: el rol de `auth` solo tiene `INSERT, SELECT` sobre
   `audit_log`; un trigger rechaza `UPDATE/DELETE/TRUNCATE`; la purga por retención solo la
   hace un rol `audit_archiver` que primero exporta la partición mensual a Parquet en el archivo
   de auditoría (`audit/` del almacenamiento local, de solo-anexado; Object Lock *compliance* sólo
   si el destino remoto es S3 compatible con esa función; [`storage.md`](storage.md)), luego
   `DETACH` + `DROP` ([`database.md`](database.md)).
4. **Anclaje:** cada día se escribe el hash de cabeza de la cadena en `audit/anchors/` y se envía
   al destino remoto si existe (fuera del alcance de un root del servidor), y el job de
   verificación recorre la cadena (alerta si se rompe).
5. Retención propuesta: **2 años en PostgreSQL** y **5 años** en el archivo de auditoría; el plazo
   legal real se valida (pregunta abierta Q4).
6. La auditoría **no** es log: no va a Loki como fuente de verdad (puede ir una copia sin datos
   personales).
7. **Visibilidad por tenant:** un ISP sólo ve registros con su `tenant_id` (incluidas las
   acciones de soporte de plataforma sobre él); la plataforma ve los suyos. La cadena de hashes es
   única para toda la instalación (un registro por fila, con `tenant_id`); la exportación de la
   auditoría de un tenant incluye las pruebas de inclusión (hash previo/siguiente) para que pueda
   verificarse sin ver registros de otros tenants.
8. Sin destino remoto (D2) la inmutabilidad es más débil: un root del servidor podría reescribir
   el archivo local (`chattr +a` sólo frena errores, no a root). La UI de plataforma lo indica.

## 8. Gestión de secretos

### 8.1 Clasificación

| Tipo | Ejemplos | Dónde |
|------|----------|-------|
| Secretos de despliegue | Contraseñas de PG/ClickHouse/Valkey/NATS, pepper, clave de firma JWT, claves de la CA interna, KEK | Docker secrets (archivos), cifrados en el repo de despliegue con SOPS+age |
| Secretos de dominio (datos) | Credenciales SNMP/API REST RouterOS/SSH de routers, claves privadas WireGuard, secretos TOTP, credenciales de canales de notificación (SMTP, bot de Telegram), credenciales de destinos remotos de copias, hash de credenciales de kiosco | PostgreSQL, cifrados con envelope encryption (los hashes, sólo hash) |
| Claves de cifrado de copias | Passphrase/clave de pgBackRest y de `age`/rclone `crypt` | Fuera del servidor (gestor de contraseñas + copia offline) y como Docker secret en el servidor; **distinta de la KEK** |
| Secretos de CI | Credenciales de registro, tokens | GitHub Actions secrets/environments + OIDC (sin credenciales de larga duración cuando sea posible) |

### 8.2 Envelope encryption (S9)

- Cada secreto de dominio se cifra con una **DEK** aleatoria (AES-256-GCM, nonce 96 bits).
  La DEK se cifra con la **KEK** activa.
- Se almacena (columnas de [`database.md`](database.md)): `secret_ciphertext`, `secret_nonce`,
  `dek_wrapped`, `kek_id` (el algoritmo va implícito en la versión de `kek_id`).
- **Alternativa `secret_ref`:** referencia a un secreto en un gestor externo (OpenBao KV) en
  lugar del material cifrado; la librería resuelve ambos caminos con la misma interfaz.
- **Borrado = crypto-shredding:** al eliminar una credencial o rotar una clave WG se
  sobrescriben con `NULL` `secret_ciphertext` y `dek_wrapped`; las copias en backups quedan
  inservibles sin esa DEK (la DEK solo existía envuelta en esa fila). Para borrados masivos
  (p. ej. baja de un sitio) basta retirar la DEK.
- **AAD** = `"<tabla>|<columna>|<id_registro>|<tenant_id>"`: impide copiar un secreto cifrado a
  otra fila/router **o a otro tenant**.
- **Jerarquía por tenant (D6):** KEK maestra (por servicio) → **TEK** (clave de tenant, 32 B
  aleatorios, guardada envuelta por la KEK en una tabla de claves) → DEK por secreto. Rotar o
  revocar a un tenant no toca a los demás. En la baja de un tenant se destruye su TEK: sus secretos
  quedan ilegibles en la base viva; en las copias siguen legibles mientras exista la KEK y la copia
  (hasta que caduque por retención, 35 días por defecto), lo que se documenta en el contrato de
  baja. Secretos de plataforma (destinos remotos) usan una TEK de plataforma.
- Librería común `packages/go/crypto/envelope` con interfaz `KeyProvider` (`Wrap`, `Unwrap`)
  y dos implementaciones: `FileKeyProvider` (v1) y `OpenBaoTransitProvider` (v2).
- **Rotación de KEK:** nueva `kek_id` activa para escrituras; job de re-envolvimiento de DEKs
  (no requiere re-cifrar los datos); la KEK anterior se retira cuando ya no hay referencias.
- **Quién descifra:** solo `devices` (credenciales de routers), `wireguard` (claves WG),
  `auth` (TOTP), `alerts` (secretos de canales de notificación) y `jobs` (destinos remotos),
  cada uno con **su propia KEK**. Con el binario modular ([ADR-0025](adr/0025-binario-modular-con-roles.md))
  las KEK siguen siendo distintas por módulo y cada módulo sólo carga la suya.
  `wireguard-agent` no tiene KEK: recibe el material en claro por mTLS desde `wireguard`. `snmp` obtiene credenciales en claro mediante
  la RPC interna `devices.CredentialService/Resolve`, autorizada solo para el certificado de `snmp`,
  sobre gRPC mTLS; las mantiene en memoria con TTL de 15 min y nunca las escribe
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

- **SNMP v2c:** community por router, cifrada; nunca `public`/`private`; vista de solo lectura;
  **sólo dentro del túnel WireGuard**.
- **SNMPv3 (MikroTik, D10):** usuario, protocolo y claves auth/priv cifrados; authPriv con SHA-256
  si la versión de RouterOS lo soporta (mínimo SHA1) y AES; en el router, `addresses=` limitado a la
  IP de Horus en el túnel ([`vendors/mikrotik.md`](vendors/mikrotik.md), Agente E).
- **API de RouterOS (D10, [ADR-0022](adr/0022-mikrotik-routeros-v7-primer-fabricante.md)):** API
  binaria **con TLS** (`api-ssl`, 8729; camino principal de lectura) y REST sobre HTTPS (`www-ssl`),
  ambas sólo dentro del túnel WireGuard; nunca `api` 8728 ni `www` en claro. Usuario dedicado
  `horus-ro` en un grupo de **solo lectura** (políticas `read`, `api`, `rest-api`; sin `write`,
  `policy`, `sensitive`, `password`, `ftp`, `winbox`, `web`, `ssh`), con `address=` = red de
  servicios de Horus. Certificado del router fijado en el primer contacto (TOFU con confirmación) o
  firmado por la CA del ISP; el cliente de Horus **no** desactiva la verificación TLS en silencio.
  Contraseña de ≥ 24 caracteres aleatoria generada por Horus y entregada dentro del script de alta
  `.rsc` (que por eso es `no-store`, se genera bajo re-auth y caduca con el token de enrolamiento).
- **SSH:** no se usa en v1.
- **Token de enrolamiento** (`POST /enroll/wireguard`): 256 bits, sólo su hash en BD, ligado a
  (tenant, router, peer previsto), TTL 24 h, un uso, revocable, rate limit estricto; el endpoint
  sólo acepta una clave pública y no devuelve secretos; una clave pública ya registrada se rechaza;
  el admin del tenant recibe aviso del peer recién registrado (un token robado sólo permite
  registrar una clave ajena antes que el router legítimo, lo que se detecta porque el router real
  no completa el handshake). El script usa `check-certificate=yes`, **nunca** `no`.
- **Escritura en routers:** v1 **no escribe** configuración en los MikroTik (ni mitigación de
  botnets por `address-list`, [ADR-0024](adr/0024-deteccion-de-botnets-como-objetivo-principal.md)
  §4). Si se aprueba más adelante, será un usuario **distinto** con permisos mínimos y cada acción
  con aprobación humana y auditoría; la credencial de lectura nunca gana permisos de escritura.
- **Hub WireGuard de plataforma:** su clave privada es el secreto más crítico de la instalación
  (todos los routers de todos los ISP confían en su clave pública); cifrada con la KEK de
  `wireguard`, incluida en la copia de secretos offline y con runbook de pérdida/rotación
  ([`disaster-recovery.md`](disaster-recovery.md) RB-11). El hub deniega el reenvío entre peers.
- **WireGuard** (alineado con [`database.md`](database.md) §2.3): clave privada del servidor y
  preshared keys cifradas; clave privada del peer router **no se guarda**: se genera solo si el
  router no puede generarla, se entrega **una vez** y se descarta (si se pierde → rotación).
  Preferido: el router genera su par y Horus solo guarda la pública. Las `.conf` renderizadas
  nunca se persisten.

### 8.5 Credenciales de destinos remotos de copias (D2)

Los destinos remotos son **de plataforma** (las copias contienen todos los tenants) y los gestiona
`platform_admin` ([`api.md`](api.md) §2.8; módulo `jobs`, [ADR-0019](adr/0019-almacenamiento-local-y-destino-remoto.md)). La herramienta candidata es **rclone** (SFTP, Google
Drive, MEGA, Dropbox, S3); MediaFire no tiene soporte estable en rclone y **no se ofrece**.

| Destino | Credencial | Mínimo privilegio | Notas |
|---------|-----------|-------------------|-------|
| **SFTP** (NAS u otro servidor; primero) | Llave ed25519 generada por Horus (la pública se instala en el destino) | Usuario dedicado, `ChrootDirectory`, sólo SFTP (`ForceCommand internal-sftp`), sin shell; idealmente sin permiso de borrado | `known_hosts` fijado al dar de alta |
| **SFTP en modo pull** (recomendado si hay NAS) | El NAS tiene una llave para **leer** el directorio de copias del servidor | El servidor no tiene ninguna credencial sobre el NAS | Un servidor comprometido no puede borrar las copias del NAS |
| **Google Drive** | Token OAuth (refresh token) | Alcance `drive.file` (sólo archivos creados por la app), cuenta de servicio o cuenta dedicada | El token se obtiene con el flujo de autorización de rclone desde la UI de plataforma |
| **Dropbox** | Token OAuth | App con acceso "App folder" | Versionado de Dropbox como protección extra |
| **MEGA** | Usuario + contraseña de una cuenta **exclusiva** | No hay alcances: la credencial da acceso a toda la cuenta | Por eso cuenta exclusiva y cifrado en cliente obligatorio |
| **S3 compatible** (no en v1) | Access key/secret | Política de sólo `PutObject`/`GetObject`; Object Lock si el proveedor lo ofrece | Se añade si aparece un disparador de [ADR-0019](adr/0019-almacenamiento-local-y-destino-remoto.md) §4; la opción más robusta frente a ransomware |

Controles comunes:

1. **Cifrado en cliente obligatorio** antes de que nada salga del servidor: pgBackRest cifra su
   repositorio; el resto (ClickHouse, configuración, anclas de auditoría) se cifra con rclone
   `crypt` o `age`. La clave de cifrado de copias es distinta de la KEK y se custodia **offline**
   fuera del servidor ([`disaster-recovery.md`](disaster-recovery.md) §3.6): **sin ella no hay
   recuperación** desde el destino remoto. El proveedor sólo ve blobs.
2. Credenciales guardadas con envelope encryption (TEK de plataforma); nunca en archivos de
   configuración de rclone en disco: el proceso de copia las recibe en memoria (variables de
   entorno `RCLONE_CONFIG_<REMOTO>_*` del proceso hijo, que muere al terminar) y no las escribe en
   logs (rclone con `--log-level INFO` y redacción).
3. Write-only en la API (`PUT .../credentials`), prueba de conectividad explícita, auditoría.
4. Si el destino remoto falla o no existe, el sistema **avisa y sigue**: las copias locales se
   hacen igual ([`disaster-recovery.md`](disaster-recovery.md) §3.0).

## 9. Seguridad de la red de gestión

1. **Segmentación:** colectores y `wireguard` en una red (VLAN/VRF de gestión) separada de la
   red de usuarios del ISP y de la red de clientes. Redes Docker separadas `edge`, `app`,
   `data`, `mgmt` (definición final en [`architecture.md`](architecture.md)).
2. **Exportación de flujos y SNMP:** preferentemente a través de la VLAN de gestión o del túnel
   WireGuard del router. Nunca por Internet en claro.
3. **Puertos de entrada** (solo desde prefijos de gestión, filtrado nftables en el host):
   NetFlow v5/v9 `UDP 2055`, IPFIX `UDP 4739`, sFlow `UDP 6343`, traps SNMP `UDP 162`
   (si se usan), WireGuard `UDP 51820`.
4. **Validación de origen:** el colector solo acepta exportadores registrados en el inventario
   (IP + ID de dominio de observación) **y lo asocia a su tenant**; los desconocidos se descartan
   y generan `horus.flows.exporter.unassigned` (tenant `platform`, con rate limit) para que el
   `platform_admin` los registre. Un exportador nunca puede "elegir" su tenant: el tenant sale del
   inventario, no del paquete. La IP de origen del exportador debe ser única en la plataforma
   (dirección del túnel WireGuard asignada por Horus).
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
  estático servido por Traefik/un servidor estático sin root. Imágenes referenciadas **por digest** en despliegues.
- `user: 65532:65532`; `read_only: true` + `tmpfs: /tmp` si hace falta;
  `cap_drop: [ALL]`; `security_opt: [no-new-privileges:true]`; perfil seccomp por defecto
  de Docker (nunca `unconfined`); sin `privileged`.
- Excepciones explícitas y documentadas: `wireguard-agent` (`cap_add: NET_ADMIN`,
  `network_mode: host`; el plano de control `wireguard` corre sin capacidades), colector de
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
| Navegador → reverse proxy | TLS 1.2+ (preferente 1.3), Traefik con ACME (Let's Encrypt) si hay dominio público, o certificado de la CA interna del ISP; HSTS `max-age=31536000; includeSubDomains` | Igual |
| Reverse proxy → gateway | HTTP en red Docker interna `edge` | mTLS |
| Gateway → servicios (HTTP) y servicio ↔ servicio (gRPC) | **mTLS desde el Sprint 1** ([ADR-0005](adr/0005-grpc-protobuf-interno.md)) con CA interna `step-ca`; certificados de 30 días renovados automáticamente (§5.3) | mTLS con cert-manager o service mesh (Linkerd); certificados de 24 h |
| Servicios → PostgreSQL / ClickHouse / NATS / Valkey | TLS **activado desde Sprint 1** para NATS y PostgreSQL (misma CA interna; coste bajo, evita deuda); ClickHouse/Valkey TLS cuando crucen host | TLS obligatorio en todo |
| Cualquier tramo que cruce hosts físicos, el NAS o la nube | SFTP (SSH) o HTTPS con verificación; además, copias cifradas en cliente (§8.5) | Igual |
| Horus → routers MikroTik (API REST, SSH, SNMP) | Dentro del túnel WireGuard; API sólo HTTPS con certificado fijado (§8.4) | Igual |

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

### 13.1 Propósito declarado y roles (D5, D6)

- **Propósito principal:** seguridad de la red del ISP — detectar clientes infectados o que
  participan en botnets (C2, DDoS, escaneos, spam) para avisar y mitigar. **Secundarios:**
  operación de red (capacidad, disponibilidad) y detección de uso comercial de IPs residenciales
  (D1). Uso exclusivamente empresarial.
- **Usos excluidos** (requieren ADR + revisión legal para cambiar): perfiles de navegación con
  fines comerciales o publicitarios, venta o cesión de datos, inspección de contenido (payloads,
  DNS, URLs, SNI), informes de "qué sitios visita" una IP fuera de una investigación de seguridad.
- **Roles:** cada ISP es **responsable** del tratamiento de los datos de sus clientes; quien opera
  la plataforma Horus actúa como **encargado** por cuenta de cada ISP (contrato de encargo/DPA por
  tenant). Si el operador de la plataforma es el mismo ISP, coincide.
- La base legal concreta (interés legítimo en la seguridad de la red, obligación de las normas de
  telecomunicaciones, contrato) depende del **país de cada tenant** (campo `country` del tenant;
  pregunta abierta Q1).

### 13.2 ¿Qué es dato personal?

- **IP del cliente** (D1: es su identidad en Horus). Horus no tiene nombres ni contratos, pero el
  ISP sí puede asociar la IP a una persona con sus propios sistemas (RADIUS/facturación) ⇒ es dato
  personal **seudonimizado**, no anónimo.
- **Flujos** (IP origen/destino, puertos, bytes, horarios): metadatos de comunicaciones; en
  algunas jurisdicciones protegidos por el **secreto de las comunicaciones**.
- **Hallazgos de seguridad por cliente** y su `security_state`: inferencias sensibles ("este
  cliente está infectado").
- **Tipo residencial/comercial por scoring**: perfilado automatizado con posible efecto
  contractual ⇒ transparencia, explicabilidad y revisión humana antes de actuar.
- **Alias/notas** que un operador escriba o que se importen del usuario PPPoE del MikroTik
  (pueden contener nombres): campo libre
  marcado como PII.
- Datos de usuarios de la plataforma (empleados de los ISP): email, IP de acceso, auditoría.
- **No** se recogen payloads, DNS, URLs ni SNI (sólo metadatos de flujo).

**IPs dinámicas:** con D1, si el ISP asigna IPs dinámicas (PPPoE/DHCP), una misma IP puede
corresponder a personas distintas a lo largo del tiempo. Consecuencias: el historial de tipo y los
hallazgos de una IP pueden mezclar a varias personas. Controles: los hallazgos y el scoring se
expresan **sobre la IP en una ventana de tiempo**, nunca como juicio sobre una persona; la UI lo
indica; la expiración por inactividad (90 días) corta historiales largos; el ISP decide con sus
propios registros a quién avisar.

### 13.3 Controles

1. **Minimización:** sólo los campos de flujo de la lista cerrada de
   [`traffic-model.md`](traffic-model.md) (Agente B). Sin nombres ni datos de contacto de clientes.
2. **Limitación del propósito por diseño:** los permisos separan agregados (`traffic.read`) de
   detalle por cliente (`traffic.customer.read`, `customers.read`, `security.findings.read`, `security.evidence.read`); el detalle por
   cliente es **auditado** en cada acceso.
3. **Retención acotada al propósito** (propuesta; la valida el PO por país, Q2):

   | Dato | Retención por defecto | Justificación |
   |------|-----------------------|---------------|
   | Flujos crudos | **7 días** (configurable 3–30 por tenant) | Investigación de un hallazgo reciente; lo antiguo se sirve con agregados |
   | Agregados por cliente (5 min / 1 h / 1 día) | 90 días / 13 meses / **25 meses** (máximo por cliente; [`storage.md`](storage.md)) | Líneas base de seguridad, scoring y comparativa interanual; ningún dato por cliente supera 25 meses por defecto (D5: minimización) |
   | Agregados por nodo/router (sin cliente) | 5 años | Capacidad de red; no son datos personales |
   | Hallazgos de seguridad | 25 meses | Reincidencia y ajuste de modelos |
   | Clientes (IP, alias) e historial de tipo | Hasta 25 meses sin actividad; luego purga | Ya no hay propósito |
   | Auditoría | 2 años en PostgreSQL + 5 años archivada (Q4) | Rendición de cuentas |

   Borrado efectivo con TTL de ClickHouse y purga programada en PostgreSQL; las copias caducan en
   su propio ciclo (35 días por defecto).
4. **Sin seudonimización por ID en ClickHouse:** por [ADR-0018](adr/0018-la-ip-es-el-cliente.md)
   los agregados se guardan por `(tenant, realm, client_ip)`, así que la IP es la clave también
   en el largo plazo; la protección es la **retención** (máx. 25 meses) y el acceso por permiso.
   Las exportaciones a terceros sustituyen la IP por `HMAC(clave_tenant, ip)`.
5. **Exportaciones:** registradas, con marca de agua (usuario, tenant, fecha), enlaces caducos.
6. **Logs, trazas y métricas sin IPs de clientes** ([`observability.md`](observability.md) §3.3).
7. **Pantallas NOC sin datos personales por defecto** (§5.5).
8. **Desarrollo y pruebas** con datos sintéticos (IPs de rangos de documentación RFC 5737/3849);
   prohibido copiar producción sin anonimizar. Los agentes de IA de desarrollo **nunca** reciben
   datos de producción (D7).
9. **Evaluación de impacto (DPIA/EIPD)** por país de tenant antes de activar la ingesta de flujos
   en producción.
10. **Scoring y hallazgos explicables** (razones con código, detalle y peso) y **decisión humana**
    antes de cualquier acción con efecto sobre el cliente (aviso, corte, cambio contractual).

### 13.4 Derechos y supresión

- La identificación del titular la hace el ISP (Horus no sabe quién es). Ante una solicitud de
  supresión, el ISP (o la plataforma por su cuenta) purga la IP: `customer.purged` → se borran los
  datos derivados por esa IP (mutación en ClickHouse por `(tenant, realm, client_ip)`); los flujos
  crudos caducan solos en ≤ 7 días.
- Proceso auditado.

### 13.5 Baja de un tenant (offboarding)

1. `horus.auth.tenant.offboarded`: se corta el acceso de sus usuarios y kioscos y se pausa la ingesta.
2. Exportación final a petición del ISP (inventario, clientes, agregados, auditoría del tenant),
   entregada cifrada.
3. Tras el plazo acordado (por defecto 30 días): purga de sus filas en PostgreSQL y ClickHouse
   (por `tenant_id`, primera columna del orden en ClickHouse), destrucción de su TEK
   (crypto-shredding de sus credenciales) y borrado de `archive/<tenant_id>/` y `reports/<tenant_id>/`.
   Los mensajes NATS no se pueden purgar por tenant (no hay token de tenant en el subject,
   [`events.md`](events.md) §2.4): caducan solos (dominio 30 días, telemetría ≤ 72 h).
4. Sus datos desaparecen de las copias cuando éstas caducan (35 días); el certificado de baja lo
   indica.
5. Se conserva sólo la auditoría exigible (registro de la baja y metadatos mínimos).

### 13.6 Pregunta abierta (legal)

La legislación aplicable depende del **país de cada ISP** y debe validarla un asesor legal:
protección de datos (p. ej. LFPDPPP en México, Ley 1581 en Colombia, Ley 29733 en Perú, LOPDP en
Ecuador, Ley 21.719 en Chile, LGPD en Brasil, RGPD en la UE/España), normas de telecomunicaciones
sobre **conservación obligatoria** de metadatos (mínimos *y* máximos) y secreto de las
comunicaciones. Con D5 el propósito de seguridad facilita la justificación, pero no la sustituye.
Ver [`open-questions/security-ops.md`](open-questions/security-ops.md) Q1–Q2.

## 14. Respuesta a incidentes (mínimo v1)

- Contacto de seguridad y responsable de guardia definidos (pregunta abierta).
- Playbooks breves: (a) cuenta comprometida → revocar sesiones/tokens, forzar reset y 2FA;
  (b) KEK o credenciales de routers comprometidas → rotar KEK, rotar credenciales en routers
  (lista exportable por sitio), rotar claves WG; (c) fuga de datos de tráfico → evaluar
  notificación según ley aplicable **de cada tenant afectado**; (d) **fallo de aislamiento entre
  tenants** (alerta `TenantMismatch` o reporte de un ISP) → desactivar la ruta/topic afectado,
  determinar con la auditoría qué datos de qué tenant vio quién, notificar a los ISP afectados,
  añadir el caso a la batería de aislamiento; (e) kiosco robado → revocar, revisar canjes y
  `last_ip`.
- `SECURITY.md` en la raíz del repo con canal de reporte (Sprint 1, Agente 5 lo planifica).

## 15. Checklist de seguridad para la Definición de Terminado

Cada historia que toque código marca (o justifica N/A):

- [ ] Endpoints nuevos registrados en la tabla ruta→permiso del gateway (test de "deny by
      default" pasa) y verificados también en el servicio con alcance ACL.
- [ ] Tests de autorización: al menos un caso 403 (sin permiso) y uno de alcance (otro sitio).
- [ ] **Aislamiento entre tenants**: la ruta/topic/consumidor nuevo está cubierto por la batería
      automática "A no ve B" (§3.9) y pasa; consultas con `TenantScope`; claves de caché con tenant.
- [ ] Rutas accesibles a kiosco declaradas explícitamente (`principals`) y sin datos personales
      salvo política.
- [ ] Entradas validadas (tipos, rangos, tamaños) en el borde; consultas parametrizadas.
- [ ] Ningún secreto, token, credencial, contraseña ni IP de cliente en logs, trazas, métricas,
      eventos (salvo los catalogados `pii`) ni mensajes de error (verificado por lint/test).
- [ ] Secretos de dominio cifrados con `envelope`; campos de credenciales de solo escritura.
- [ ] Acciones sensibles generan registro de auditoría (vía outbox) con diff sin secretos.
- [ ] Datos personales nuevos: campo justificado, retención definida, permiso adecuado.
- [ ] Dependencias nuevas justificadas; `govulncheck`, `osv-scanner`, `trivy` y `gitleaks` en
      verde.
- [ ] Contenedor cumple §10 (no root, sin capacidades extra salvo excepción documentada).
- [ ] Rate limit / límites de tamaño considerados para endpoints públicos o costosos.
- [ ] Parsers de entrada de red (flows, SNMP, traps) con test de fuzzing.
- [ ] Cambios en autenticación/autorización/aislamiento de tenants/criptografía: revisión por un
      agente revisor distinto del autor **y** aprobación de la persona responsable (CODEOWNERS,
      [`conventions.md`](conventions.md) §6.2).
