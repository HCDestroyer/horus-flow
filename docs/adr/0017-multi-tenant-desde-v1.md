# 0017 — Multi-tenant desde v1 (tenant = ISP)

- Estado: Aceptada
- Fecha: 2026-10-07
- Decisores: product owner ([D6](../po-decisions.md)), Agente A (arquitectura, ronda 2)
- Sustituye: la recomendación de [Q1](../open-questions/architecture.md#q1) y el principio P9 de
  [architecture.md](../architecture.md) ("una organización en v1"). Ajusta
  [ADR-0007](0007-postgresql-fuente-de-verdad.md) (`organization_id` → `tenant_id`).

## Contexto

El PO confirmó ([D6](../po-decisions.md)) que el tráfico viene del **router principal de cada nodo
de cada ISP** y que habrá **varios ISP con varios routers** en la misma instalación. El diseño del
Sprint 0 era single-tenant con una columna preparada (`organization_id` / `tenant_id` constante) y
sin aislamiento real. Ya no basta: un operador de un ISP nunca debe ver clientes, IPs, tráfico,
hallazgos ni routers de otro ISP, y un error de filtrado (un `WHERE` olvidado, muy probable en
código escrito por agentes de IA, [D7](../po-decisions.md)) es un incidente de privacidad grave.

Además, `organization` ya es una entidad del catálogo de tráfico (Meta, Google…), por eso el
identificador de tenant se llama siempre **`tenant_id`** (convención del proyecto).

## Decisión

### 1. Modelo y jerarquía

```
plataforma (instalación de Horus)
└── tenant  = ISP                         tenant_id
    └── nodo (sitio/POP del ISP)          site con kind = 'node'
        └── router principal              router (exportador de flujos, peer WireGuard)
            └── realm de direcciones      ip_realm (espacio donde una IP es única)
                └── IP de cliente         customer  (identidad = tenant, realm, IP; ver ADR-0018)
```

- Un nodo tiene normalmente **un** router principal; el modelo admite varios (redundancia) y
  routers secundarios solo monitorizados por SNMP.
- Cada router principal define por defecto **un realm privado** (sus pools CGNAT/RFC 1918) y el
  tenant tiene **un realm público** (sus IPs públicas). Así, la misma IP privada en dos nodos o en
  dos ISP son clientes distintos.
- Datos **de plataforma** (sin `tenant_id`, compartidos): catálogo de clasificación
  (prefijos/ASN/organizaciones/servicios/categorías), feeds de reputación, fabricantes y modelos,
  plantillas de roles del sistema, configuración de destinos remotos de backup
  ([ADR-0019](0019-almacenamiento-local-y-destino-remoto.md)).
- Datos **de tenant** (con `tenant_id NOT NULL`): todo lo demás — nodos, routers, credenciales,
  peers WireGuard, clientes, flujos, métricas, hallazgos, scores, alertas, dashboards, reportes,
  auditoría.

### 2. Identidad y usuarios

- **El usuario es una identidad de plataforma** (email/usuario único global), no pertenece a un
  tenant. Su acceso a cada ISP se da por **membresía** `tenant_membership(user_id, tenant_id,
  roles)`. Un usuario puede tener acceso a uno o varios ISP con roles distintos en cada uno.
- **Usuarios de plataforma** (superadmin, soporte del operador de Horus): tienen roles de ámbito
  `platform` con permisos `platform.*` (`platform.tenants.manage`, `platform.users.manage`,
  `platform.storage.manage`, `platform.catalog.manage`…). No ven datos de un tenant por defecto:
  para operar dentro de un ISP **entran explícitamente** en él (token de ese tenant, ver §3), y la
  auditoría marca `via_platform = true`. El acceso de plataforma a datos de clientes de un tenant
  es una acción auditada, no una vista global.
- **Administrador de tenant** (rol `tenant_admin`): gestiona los usuarios y roles *de su ISP*
  (invita, asigna roles, revoca membresías) sin ver otros tenants.
- Roles: plantillas de sistema globales (`tenant_admin`, `noc`, `analyst`, `security_analyst`,
  `viewer`) + roles personalizados por tenant (`role.tenant_id` no nulo). ACL por recurso dentro
  del tenant como en [security.md](../security.md).

### 3. Contexto de tenant en la API

- **La sesión es del usuario; el access token es de un tenant.** El refresh token/sesión no lleva
  tenant. El frontend obtiene un access token por tenant (`POST /api/v1/auth/token` con
  `tenant_id`), que contiene `tid` (tenant activo) y los permisos **de ese tenant**. Cada pestaña o
  pantalla de kiosco guarda en memoria el token de su tenant, así un usuario puede tener dos ISP
  abiertos a la vez sin conflicto.
- **El tenant nunca se toma de un parámetro del cliente** (path, query, header o body) para
  autorizar: los servicios usan exclusivamente `tid` del token validado. Un `tenant_id` en un body
  distinto de `tid` → `403`.
- Las rutas REST **no cambian** (`/api/v1/routers`, `/api/v1/customers`…): el ámbito es implícito
  por el token. Las rutas de plataforma viven bajo `/api/v1/platform/*` y exigen un token de
  ámbito `platform` (sin `tid`).
- **Vistas multi-ISP**: fuera de v1, salvo un **resumen de plataforma** (`/api/v1/platform/overview`:
  estado por tenant — routers caídos, alertas abiertas, salud de ingesta — sin datos de clientes)
  para usuarios de plataforma. Ver [Q18](../open-questions/architecture.md#q18).
- El frontend incluye el tenant en la ruta (`/t/{tenant_slug}/...`) para que los enlaces y las
  pantallas de kiosco sean estables; al cambiar de tenant pide un token nuevo. Detalle en
  [frontend.md](../frontend.md) y [api.md](../api.md).

### 4. PostgreSQL: `tenant_id` + Row-Level Security (sí)

- `tenant_id uuid NOT NULL` en **todas** las tablas de datos de tenant (no solo en las raíz), y
  como primera columna de los índices únicos y de búsqueda (`ux(tenant_id, …)`).
- **RLS activado** (`ENABLE` + `FORCE ROW LEVEL SECURITY`) en todas las tablas con `tenant_id`, con
  política `tenant_id = current_setting('horus.tenant_id')::uuid`. Cada transacción de una
  petición hace `SET LOCAL horus.tenant_id = <tid>` desde el middleware de acceso a datos; si no se
  fija, la consulta no devuelve filas (fallo cerrado).
- El rol de base de datos de la aplicación **no** tiene `BYPASSRLS`. Los procesos que trabajan
  sobre todos los tenants (relay del outbox, jobs de scoring, snapshot de objetivos SNMP) usan un
  **rol de servicio distinto** con `BYPASSRLS`, limitado a esas lecturas y nunca expuesto a rutas
  HTTP.
- Además del RLS, el repositorio exige `tenant_id` explícito en cada consulta (doble barrera). Un
  test de arquitectura en CI falla si una tabla de tenant no tiene política RLS.
- Por qué RLS sí: el coste (un `SET LOCAL` por transacción y políticas declarativas) es bajo y
  convierte un bug de filtrado en "cero filas" en lugar de una fuga entre ISP. Con desarrollo por
  agentes de IA, la defensa en profundidad automática vale más que la revisión manual.

### 5. ClickHouse

- `tenant_id` es la **primera columna del `ORDER BY`** de toda tabla de tenant (flujos, métricas,
  agregados, hallazgos masivos, scores), seguida de la dimensión dominante (`realm_id`/`router_id`,
  tiempo). La poda por tenant es casi gratuita.
- **Particionado por día, no por tenant** (evita explosión de particiones con muchos ISP).
- **Row policies** con setting personalizado: los usuarios ClickHouse de lectura (`analytics`,
  `detection`, `alerts`) tienen `ROW POLICY … USING tenant_id = getSetting('SQL_horus_tenant')`;
  cada consulta de una petición fija ese setting desde `tid`. Los jobs multi-tenant usan otro
  usuario sin la política. Además, el constructor de consultas inyecta `tenant_id = ?` siempre.
- Retención: uniforme por tabla en v1; retención por tenant queda como pregunta
  ([Q21](../open-questions/architecture.md#q21)).
- Cuotas: límites de consulta (`max_execution_time`, `max_memory_usage`) por usuario y, en
  `analytics`, límite de concurrencia por tenant para que un ISP no degrade al resto.

### 6. NATS: sin token de tenant en los subjects; `tenant_id` obligatorio en el sobre

- Subjects según la convención `horus.<dominio>.<entidad>.<evento>.<entity_id>`, **sin** token de
  tenant. Todo mensaje (dominio y telemetría) lleva `tenant_id` en el sobre y en la cabecera
  `Horus-Tenant`; un mensaje sin tenant es rechazado por los consumidores (salvo los de plataforma,
  p. ej. catálogo publicado, que llevan `tenant_id` nulo y tipo de plataforma).
- **Una sola cuenta NATS** para los procesos de Horus: los consumidores son multi-tenant (un
  ingester procesa flujos de todos los ISP) y ningún tenant tiene acceso directo a NATS. Las
  NATS Accounts por tenant solo se justificarían si terceros se conectaran al bus; no es el caso.
- Los lotes de flujos se forman **por exportador** (un lote = un router = un tenant).
- El api-gateway filtra el fan-out de WebSocket por `Horus-Tenant` = `tid` de la conexión, además
  del filtrado por permisos.
- Equidad: el collector aplica un límite de flujos/s por exportador y por tenant (configurable)
  antes de publicar, para que un ISP no llene el stream de telemetría compartido.
- Contrato detallado (cabeceras, validación) en [events.md](../events.md).

### 7. Valkey

Claves de datos de tenant con prefijo `t:<tenant_id>:` (cachés de analytics, estados efímeros de
UI). Rate limit por usuario y por IP (no por tenant) más un límite global por tenant en
`analytics`. Revocación de sesión por `sid` (la sesión es de usuario). Ver
[ADR-0020](0020-valkey-en-lugar-de-redis.md).

### 8. Red y colectores

- La **IPAM de túneles WireGuard es de plataforma**: cada router de cualquier tenant recibe una
  dirección de túnel única en toda la instalación. Así la IP origen de los flujos y del SNMP
  identifica sin ambigüedad al router y, por tanto, al tenant.
- El hub WireGuard **impide el tráfico entre peers** (nftables: `forward` entre peers denegado);
  solo los pollers y el collector de Horus son alcanzables desde los túneles.
- El collector **descarta** los flujos de exportadores no registrados (contados en
  `horus_flows_dropped_total{reason="unknown_exporter"}`) y avisa con
  `horus.flows.exporter.unregistered` para que un admin lo registre; nunca crea routers ni tenants
  automáticamente.

### 9. Observabilidad y auditoría

Métricas de negocio con etiqueta `tenant` **solo** en métricas de baja cardinalidad (flujos/s por
tenant, lag por tenant); nunca IPs de clientes como etiqueta. Logs con `tenant_id`. La auditoría
lleva `tenant_id` (nulo para acciones de plataforma) y la marca `via_platform`.

## Alternativas consideradas

- **Single-tenant + una instalación por ISP**: aislamiento físico perfecto, pero N despliegues
  (N ClickHouse, N NATS…) que una sola persona no puede operar; contradice D6.
- **Base de datos o esquema PostgreSQL por tenant**: aislamiento fuerte, pero migraciones × N,
  pools de conexiones × N y consultas de plataforma complejas. Se reserva para un tenant con
  requisitos regulatorios propios.
- **Solo `tenant_id` sin RLS**: más simple, pero un filtro olvidado filtra datos entre ISP; con
  código generado por agentes es un riesgo no aceptable.
- **Tenant en el path (`/api/v1/tenants/{id}/…`)**: explícito y fácil de leer en logs, pero
  duplica la validación (path vs token), cambia todas las rutas y no aporta seguridad frente al
  token por tenant.
- **Token con todos los tenants y header `Horus-Tenant` por petición**: un único token, pero
  permisos por tenant dentro del JWT (tamaño), y el tenant pasa a ser un dato que envía el cliente.
- **Token de tenant en los subjects NATS** (`horus.<tenant>.<dominio>…`): permite suscripciones
  por tenant, pero los consumidores son multi-tenant y multiplica los subjects sin beneficio
  real; si hiciera falta, se añade con *subject mapping* sin tocar productores.
- **Cuenta NATS por tenant**: aislamiento idiomático, pero cada servicio tendría que consumir de N
  cuentas; sin clientes externos en el bus no aporta.

## Consecuencias

- (+) Aislamiento entre ISP aplicado en tres capas (token → repositorio → RLS/row policy).
- (+) Un usuario y una sesión sirven para varios ISP; pantallas de kiosco por ISP en paralelo.
- (+) Una sola instalación para todos los ISP: coste operativo compatible con IA + 1 persona.
- (−) Toda consulta, caché, evento y job debe propagar el tenant; los tests deben cubrir el
  aislamiento (caso "usuario del tenant A pide recurso del tenant B → 404").
- (−) `SET LOCAL` obliga a usar transacciones explícitas también en lecturas (pgx con
  `BeginTx`), y los jobs multi-tenant necesitan un rol separado bien acotado.
- (−) Vecinos ruidosos: un ISP grande comparte ClickHouse, NATS y collector con los demás; se
  mitiga con límites por tenant y se vigila con métricas por tenant.
- Impacto en otros documentos: [database.md](../database.md) (usuario global + membresías,
  `tenant_id` en todas las tablas, RLS, row policies), [security.md](../security.md) (claims
  `tid`/ámbito `platform`, roles de plataforma vs tenant), [api.md](../api.md)
  (`/auth/token`, `/platform/*`), [events.md](../events.md) (validación de `Horus-Tenant`),
  [frontend.md](../frontend.md) (selector de tenant, `/t/{slug}`).
