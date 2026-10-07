# 0022 — MikroTik RouterOS v7 como primer fabricante y diseño de adaptadores por capacidad

- Estado: Aceptada
- Fecha: 2026-10-07
- Decisores: product owner ([D10](../po-decisions.md)), Agente A (arquitectura, ronda 2)
- Detalle técnico del fabricante: [vendors/mikrotik.md](../vendors/mikrotik.md) (Agente E). Si hay
  diferencia en un dato concreto de RouterOS (comandos, OIDs, versiones mínimas), manda ese
  documento.

## Contexto

El plan original apuntaba a MikroTik, Cisco, Huawei y Juniper a la vez. El PO fijó
([D10](../po-decisions.md)) **MikroTik RouterOS v7** como primer fabricante: es el router
principal de los nodos de los ISP objetivo ([D6](../po-decisions.md)) y aporta en un solo equipo
todo lo que Horus necesita: SNMP, exportación de flujos (Traffic Flow), WireGuard nativo y una API
REST. Hay que decidir qué se usa de cada interfaz y cómo se aísla el código específico del
fabricante para añadir otros después sin tocar el dominio.

## Decisión

### 1. Qué se usa de RouterOS v7 (versión mínima soportada: **7.12**; recomendada la última *long-term* v7; RouterOS v6 fuera del primer entregable)

| Interfaz de RouterOS | Uso en Horus | Módulo | Sentido |
| --- | --- | --- | --- |
| **WireGuard** (`/interface wireguard`) | Túnel de gestión y transporte router ↔ hub de Horus. **El router es el iniciador** (`persistent-keepalive=25s`, funciona con IP dinámica o detrás de NAT); **la clave privada se genera en el router** y Horus solo recibe la pública ([Q9](../open-questions/architecture.md#q9), opción b); `allowed-address` del router = **solo la red de servicios de Horus**. | wireguard | Horus genera la configuración; el técnico la aplica. |
| **Traffic Flow** (**IPFIX**; NetFlow v9 como alternativa) | Fuente de flujos, **sin muestreo**, `active-flow-timeout=1m`, `inactive-flow-timeout=15s`. v5 solo como último recurso (sin IPv6). Exportación por el túnel con `src-address` = IP de túnel del router, que identifica al exportador y al tenant ([ADR-0017](0017-multi-tenant-desde-v1.md) §8). | flows | Router → collector (UDP por el túnel). |
| **SNMP** v2c/v3 (MIB-II, IF-MIB, HOST-RESOURCES-MIB, MIKROTIK-MIB) | Estado y métricas: uptime, CPU, RAM, temperatura/voltaje, interfaces (estado, contadores 64 bits, errores). **SNMPv3 (authPriv) por defecto**. Las interfaces PPPoE dinámicas no se recorren. | snmp | Horus → router (por el túnel). |
| **API binaria con TLS (8729)** | **Camino principal** del adaptador de lectura: tablas grandes y sondeo periódico (pools e IPs para **sugerir los prefijos de clientes del realm** [ADR-0018](0018-la-ip-es-el-cliente.md), NAT sí/no, verificación de Traffic Flow, peers WG del lado router; más adelante y opcional, `/ppp/active` y leases DHCP como alias de cliente). | devices | Horus → router (por el túnel). |
| **API REST** (`/rest`, HTTPS) | Lecturas puntuales (versión, recursos, comprobaciones del alta). | devices | Horus → router (por el túnel). |
| ICMP | Reachability (no es específico del fabricante). | snmp | Horus → router. |

Reglas:

- **Horus no escribe configuración en el router en v1**: el aprovisionamiento es un **script
  RouterOS (`.rsc`) generado por Horus** que el técnico revisa y pega (WG, Traffic Flow, usuario
  SNMPv3, usuario de API de solo lectura, reglas de firewall que permitan a Horus solo desde el
  túnel). Escribir por API convertiría a Horus en gestor de configuración (otro dominio y otro
  nivel de riesgo); se reevaluará como incremento propio ([Q23](../open-questions/architecture.md#q23)).
- Credenciales en el router con **mínimo privilegio**: usuario `horus-ro` en un grupo con
  políticas de solo lectura (`read`, `api`, `rest-api`), `address=` restringida a la red de
  servicios de Horus. Detalle en
  [vendors/mikrotik.md](../vendors/mikrotik.md) y [security.md](../security.md).
- Particularidades conocidas a tratar en el adaptador (confirmar en `vendors/mikrotik.md`): punto
  del flujo respecto al NAT (pre/post-NAT, crítico para [Q6](../open-questions/architecture.md#q6)),
  marcas de tiempo relativas a `sysUptime` en v9, índices de interfaz de los flujos frente a
  `ifIndex` de SNMP, interfaces dinámicas (PPPoE) que **no** se sondean por SNMP, y soporte de
  muestreo de Traffic Flow.
- **Ceguera por aceleración por hardware**: el tráfico con *offload* (bridge HW, L3HW en
  CCR2116/2216, FastTrack por hardware) **no aparece en los flujos**. Horus lo detecta comparando
  cada hora los bytes de flujos atribuidos con los contadores SNMP (`ifHCIn/OutOctets`) de las
  interfaces del router; si la cobertura baja de un umbral (80 % por defecto) emite la alerta
  `flow_coverage_low` y marca el periodo como incompleto en la tabla de cobertura
  ([architecture.md §10.11](../architecture.md#1011-cobertura-de-flujos-insuficiente)).
  El script de alta advierte de desactivar el *offload* en las interfaces hacia clientes cuando
  sea posible.

### 1.1 Alta de router: endpoint de *enrolment* con token de un solo uso (recomendado)

Decisión: el script `.rsc` generado incluye un `/tool fetch` (HTTPS POST) que envía la **clave
pública** del router a `POST /api/v1/enroll/wireguard` con un **token de un solo uso**. Copiar la
clave a mano en la UI queda como **alternativa** (routers sin salida HTTPS hacia Horus, o
política del ISP).

- El token: aleatorio de 256 bits, se guarda solo su hash, ligado a (tenant, router, peer
  previsto), caduca en 24 h, se invalida al primer uso y se puede revocar. El endpoint es la única
  ruta pública sin sesión además del login; tiene rate limit estricto, solo acepta una clave
  pública WireGuard válida (44 caracteres base64) y no devuelve secretos (la configuración del hub
  ya va en el script). Cada uso queda auditado.
- Tras el *enrolment* el peer queda `pending_handshake`; pasa a `active` con el primer handshake
  observado por `wg-agent`. Una clave pública ya registrada en otro peer se rechaza.
- Validación TLS: el script usa `check-certificate=yes`; la confianza en el certificado público de
  Horus en RouterOS se detalla en [vendors/mikrotik.md](../vendors/mikrotik.md). Si no es posible,
  se usa la alternativa manual, **nunca** `check-certificate=no`.
- Por qué: el alta pasa a "un solo pegado" por router (menos errores con muchos nodos y varios
  ISP) y la clave pública no es un secreto; el riesgo (un token robado registra una clave ajena)
  es acotado por la caducidad, el uso único y el aviso al admin del peer recién registrado.

### 1.2 Rango de direcciones de túnel

- **IPAM de plataforma, configurable por despliegue** (no hay rango cableado en el código). Valor
  propuesto en el instalador: `10.255.0.0/16`, con `10.255.0.0/24` como **red de servicios de
  Horus** (hub, collector, pollers) y una `/32` por router del resto.
- Validaciones antes de asignar y en cada alta: el rango **no** puede solaparse con
  `100.64.0.0/10` (CGNAT), con los prefijos de clientes declarados de ningún realm, ni con las
  direcciones y rutas leídas del router en el alta (`/ip/address`, `/ip/route`); si choca, el alta
  se bloquea con un error explicativo y el operador de plataforma puede añadir otro rango al pool.
- Se admiten varios rangos en el pool (crecimiento o un ISP que ya use `10.255/16`).


### 2. Diseño de adaptadores: por capacidad, no por fabricante monolítico

Cada módulo define el **puerto** (interfaz Go) de la capacidad que consume y los adaptadores de
fabricante viven junto al módulo dueño:

| Capacidad (puerto) | Módulo dueño | Adaptador MikroTik | Fallback genérico |
| --- | --- | --- | --- |
| `SNMPProfile`: OIDs, normalización, detección por `sysObjectID` | snmp | prefijo `1.3.6.1.4.1.14988` | MIB-II + IF-MIB + HOST-RESOURCES |
| `FlowExporterQuirks`: plantillas, tiempos, dirección, mapeo de interfaces | flows | RouterOS Traffic Flow v9/IPFIX | IPFIX/v9/v5/sFlow estándar |
| `DeviceFacts`: identidad, interfaces, IPs, pools, NAT | devices | API binaria TLS (8729) + REST | SNMP (sysDescr, ifTable, ipAddrTable) |
| `ProvisioningTemplate`: script de alta (WG, flujos, SNMPv3, usuario `horus-ro`, firewall, `/tool fetch` de enrolment) | wireguard (+devices) | plantilla `.rsc` versionada | instrucciones manuales genéricas |

- Registro de adaptadores por `vendor_key` (`mikrotik_routeros7`) y **matriz de capacidades** por
  modelo/firmware en `devices` (catálogo de plataforma). El dominio pregunta "¿este router tiene
  `DeviceFacts`?" en vez de "¿es MikroTik?".
- Ningún paquete de dominio importa un adaptador concreto; un test de arquitectura lo verifica.
- **Kit de pruebas de contrato por fabricante**: fixtures grabados de equipos reales (snmpwalk,
  capturas pcap de IPFIX/v9, respuestas JSON de REST) por versión de RouterOS. Añadir un fabricante
  = implementar los puertos + pasar el kit. Esto permite que agentes de IA añadan fabricantes sin
  acceso a hardware ([ADR-0023](0023-entrega-por-incrementos-y-equipo-ia.md)).
- Siguientes fabricantes (Cisco, Huawei, Juniper, otros) entran como incrementos propios cuando un
  ISP los necesite; mientras, funcionan con el fallback genérico (SNMP MIB-II + flujos estándar).

## Alternativas consideradas

- **Multifabricante desde el inicio**: reparte el esfuerzo en cuatro conjuntos de MIB y
  particularidades de flujos sin un cliente que los necesite.
- **Adaptador monolítico por fabricante** (una interfaz `Vendor` con todo): acopla módulos que
  evolucionan por separado (snmp, flows, devices) y obliga a implementar todo para añadir un
  fabricante parcial.
- **API REST como fuente de métricas** en lugar de SNMP: posible en MikroTik, pero no portable a
  otros fabricantes y más costosa por sondeo; SNMP sigue siendo el estándar.
- **Aprovisionar por API desde el día uno**: más cómodo, pero da a Horus credenciales de escritura
  sobre los routers de todos los ISP; riesgo excesivo para v1.

## Consecuencias

- (+) Un solo fabricante bien soportado en el primer entregable; validación con equipos reales.
- (+) Añadir fabricantes es implementar puertos acotados y pasar un kit de contrato.
- (+) Horus solo tiene credenciales de lectura en los routers.
- (−) El alta de un router requiere un paso manual (pegar el script) por router, y un endpoint
  público de *enrolment* que hay que proteger.
- (−) El tráfico acelerado por hardware es invisible; solo se puede detectar y avisar.
- (−) Las particularidades de Traffic Flow (NAT, tiempos) deben validarse con equipos reales antes
  de confiar en la atribución por IP.
- Impacto: [database.md](../database.md) (`vendor_key`, matriz de capacidades, `client_prefixes`
  del realm), [traffic-model.md](../traffic-model.md) (quirks de RouterOS),
  [security.md](../security.md) (credenciales de solo lectura, SNMPv3),
  [vendors/mikrotik.md](../vendors/mikrotik.md).
