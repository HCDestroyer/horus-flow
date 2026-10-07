# 0018 — La IP es el cliente: descubrimiento automático de clientes desde los flujos

- Estado: Aceptada
- Fecha: 2026-10-07
- Decisores: product owner ([D1](../po-decisions.md)), Agente A (arquitectura, ronda 2)
- Sustituye: la recomendación de [Q5](../open-questions/architecture.md#q5) (clientes importados de
  CRM/facturación y sesiones IP↔cliente desde RADIUS) y la dependencia crítica P-04.

## Contexto

El Sprint 0 marcó como **crítica** la pregunta "¿de dónde sale el mapa IP ↔ cliente?" y proponía
un adaptador RADIUS/CRM con historial `customer_ip_assignment`. El PO decidió
([D1](../po-decisions.md)): **la IP es el cliente**. La IP sale de las conexiones que envía el
router; una IP = un cliente; por defecto es residencial y su tipo puede cambiar por el
comportamiento del tráfico o manualmente. No hay CRM, RADIUS ni facturación como fuente.

## Decisión

### 1. Identidad

- **Identidad natural del cliente = (`tenant_id`, `realm_id`, `ip`)**. El realm distingue espacios
  de direcciones repetidos (CGNAT/privadas de cada nodo, ver [ADR-0017](0017-multi-tenant-desde-v1.md)).
  IPv4 e IPv6 (en IPv6 el "cliente" es el prefijo delegado, `/64` por defecto, configurable por
  realm, para no crear un cliente por dirección temporal).
- En PostgreSQL el cliente tiene además un `id` UUIDv7 para la API y las referencias
  (`/api/v1/customers/{id}`), con **unicidad** `ux(tenant_id, realm_id, ip)`.
- **ClickHouse no necesita `customer_id`**: flujos y agregados se guardan por
  (`tenant_id`, `realm_id`, `client_ip`). La atribución es inmediata desde el primer flujo y no
  depende de que el registro del cliente exista ya en PostgreSQL. `analytics` resuelve
  `customer_id ↔ (realm, ip)` con una proyección/diccionario. Esto elimina la carrera "flujo antes
  que cliente" y la tabla temporal `customer_ip_assignment` deja de ser crítica.

### 2. Qué IP es un cliente

Solo las IPs dentro de los **prefijos de clientes del realm** (`ip_realm.client_prefixes`):

- Se sugieren automáticamente al registrar el router principal (pools CGNAT `100.64.0.0/10`,
  RFC 1918 vistos en interfaces con `flow_role = customer_edge`, direcciones IP de las interfaces
  hacia clientes descubiertas por SNMP/API de MikroTik) y el admin del tenant las confirma.
- El lado "cliente" de un flujo se determina por esos prefijos (y por la interfaz de entrada
  cuando el exportador la informa). Una IP de Internet **nunca** se convierte en cliente.
- Protección contra avalanchas (escaneos, IPs falsificadas): límite de clientes nuevos por realm
  y por minuto, y tope por realm igual al tamaño de sus prefijos; los excesos se cuentan
  (`horus_customers_discovery_throttled_total`) y generan una alerta de plataforma.

### 3. Quién crea el cliente al ver una IP nueva (recomendación)

```
flows-ingester ──(IP de cliente no vista en el realm)──► horus.flows.client.first_seen.<realm_id>
       ▲                                                    (lotes cada 10 s, deduplicado)
       │ conjunto de IPs conocidas por realm                          │
       │ (snapshot gRPC al arrancar + eventos)                        ▼
       └──────────── horus.devices.customer.discovered.<id> ◄── devices (módulo customers)
                                                                INSERT … ON CONFLICT DO NOTHING
                                                                + outbox
```

- **Dueño de la entidad cliente: `devices`** (módulo `customers`), igual que del resto del
  inventario de red del tenant. Es quien ya posee nodos, routers y realms, y aplica la regla de
  prefijos.
- **Detector: el rol `ingester` de `flows`**, que ya ve cada flujo enriquecido y mantiene en
  memoria el conjunto de IPs conocidas por realm. Ante una IP desconocida **no escribe en
  PostgreSQL ni llama a nadie** (P3: los colectores no esperan): la anota y publica en lote
  `horus.flows.client.first_seen` (telemetría; perder uno es inocuo porque el siguiente flujo
  vuelve a dispararlo tras el TTL de deduplicación).
- `devices` hace *upsert* idempotente por la clave natural y publica
  `horus.devices.customer.discovered` vía outbox; el ingester lo añade a su conjunto. `last_seen_at`
  no se actualiza por flujo: se calcula en ClickHouse (agregados) y `devices` lo refresca por lote
  horario desde un evento de resumen, para no convertir PostgreSQL en un contador caliente.
- Alternativas descartadas: que el ingester inserte en PostgreSQL (acopla el camino caliente a la
  BD transaccional y rompe la propiedad de datos); que lo haga `detection` (el cliente debe existir
  aunque no haya ningún hallazgo); un servicio `customers` separado (otro proceso sin escala ni
  privilegios distintos; contrario a [ADR-0025](0025-binario-modular-con-roles.md)).

### 4. Tipo de cliente

| Campo | Valores | Regla |
| --- | --- | --- |
| `kind` | `residential` (por defecto) · `commercial` · `unknown` | Tipo efectivo mostrado. |
| `kind_source` | `default` · `scoring` · `manual` | Quién fijó el tipo. |
| `kind_locked` | bool | `true` al fijarlo manualmente: el scoring ya no lo cambia (solo sugiere). |
| `commercial_use_suspected` | bool + score | Una IP residencial con uso comercial se marca, no se reclasifica sola si el umbral de confianza no se alcanza. |

- `detection` (módulo `scoring`) calcula scores y publica `horus.detection.customer.kind_suggested`
  (o `score.changed` con clase); `devices` aplica el cambio si `kind_locked = false` y la confianza
  supera el umbral del tenant, y registra el cambio con su razón (auditable).
- Alias, nombre, notas, etiquetas y grupos son **opcionales** y manuales; no se requieren para
  ninguna función.

### 5. Ciclo de vida

`active` mientras se vean flujos; `inactive` tras N días sin tráfico (configurable, 30 por
defecto); nunca se borra automáticamente mientras haya datos históricos que lo referencien; el
borrado sigue la retención de datos ([storage.md](../storage.md)).

### 6. Limitación aceptada

Con IPs dinámicas (PPPoE/DHCP), la misma IP puede pertenecer a distintos abonados en el tiempo y
un abonado puede cambiar de IP. Por decisión del PO, Horus modela el **punto de conexión
observado** (la IP), no al abonado contractual. Las métricas "por cliente" son por IP. Si en el
futuro se quiere correlacionar con el abonado, se añadirá un enriquecimiento **opcional** (sesiones
PPP/leases DHCP leídas de la API de MikroTik, o RADIUS) que añade un alias temporal, sin cambiar la
identidad ni ser dependencia de ninguna función.

## Alternativas consideradas

- **CRM/RADIUS como fuente de verdad** (propuesta del Sprint 0): más exacto con IPs dinámicas,
  pero crea una dependencia externa crítica que el PO descartó.
- **Cliente = abonado con historial de IPs**: requiere una fuente de sesiones; no existe.
- **`customer_id` en cada fila de flujo**: obliga a resolver el ID en el camino caliente y a que el
  cliente exista antes del flujo; la clave natural lo evita.
- **Crear clientes a mano**: contrario a D1 e inviable con miles de IPs.

## Consecuencias

- (+) Desaparece la dependencia crítica Q5/P-04: el valor por cliente existe desde el primer flujo.
- (+) Cero integración externa para empezar; funciona igual en todos los ISP.
- (+) Atribución determinista y reproducible (clave natural en ClickHouse).
- (−) Con IPs dinámicas, el histórico "de un cliente" mezcla abonados; se documenta en la UI.
- (−) La calidad depende de declarar bien los prefijos de clientes por realm; un prefijo mal
  declarado crea clientes falsos o ignora reales. Se mitiga con sugerencias automáticas y con la
  vista de "tráfico no atribuido".
- (−) CGNAT en el router principal: si los flujos se exportan post-NAT, la IP observada es la
  pública compartida y la decisión no funciona; el supuesto del PO (flujos del lado cliente) debe
  confirmarse por ISP ([Q6](../open-questions/architecture.md#q6)).
- Impacto: [database.md](../database.md) (tabla `customer` con clave natural, `kind_source`,
  `kind_locked`; `customer_ip_assignment` deja de ser crítica), [traffic-model.md](../traffic-model.md)
  (atribución por `realm_id` + `client_ip`), [events.md](../events.md)
  (`flows.client.first_seen`, `devices.customer.discovered`, `detection.customer.kind_suggested`),
  [api.md](../api.md) (`/customers` sin alta manual obligatoria; `PATCH` de tipo y alias).
