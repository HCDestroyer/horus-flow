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

### 1. Qué se usa de RouterOS v7 (versión mínima soportada: la que fije `vendors/mikrotik.md`, ≥ 7.x con WireGuard y REST)

| Interfaz de RouterOS | Uso en Horus | Módulo | Sentido |
| --- | --- | --- | --- |
| **WireGuard** (`/interface wireguard`) | Túnel de gestión y transporte router ↔ hub de Horus. **La clave privada se genera en el router**; Horus solo recibe la pública ([Q9](../open-questions/architecture.md#q9), opción b). | wireguard | Horus genera la configuración; el técnico la aplica. |
| **Traffic Flow** (NetFlow v9 / **IPFIX**) | Fuente de flujos. IPFIX preferido (IPv6, campos extendidos); v9 admitido; **v5 solo como último recurso** (sin IPv6). Exportación con `src-address` = IP del túnel WG para identificar al exportador ([ADR-0017](0017-multi-tenant-desde-v1.md) §8). Timeouts recomendados: activo 60 s, inactivo 15 s. | flows | Router → collector (UDP por el túnel). |
| **SNMP** v2c/v3 (MIB-II, IF-MIB, HOST-RESOURCES-MIB, MIKROTIK-MIB) | Estado y métricas: uptime, CPU, RAM, temperatura/voltaje, interfaces (estado, contadores 64 bits, errores). **SNMPv3 (authPriv) por defecto**; v2c solo si el ISP lo exige. | snmp | Horus → router (por el túnel). |
| **API REST** (`/rest`, HTTPS) | **Solo lectura** en v1: identidad, versión/firmware, modelo/serie, interfaces y sus IPs, pools IP (para **sugerir los prefijos de clientes del realm**, [ADR-0018](0018-la-ip-es-el-cliente.md)), configuración de Traffic Flow (verificar que está bien), peers WG del lado router (diagnóstico). Más adelante y opcional: sesiones PPP activas / leases DHCP como alias de cliente. | devices (descubrimiento) | Horus → router (por el túnel). |
| API binaria (8728/8729) | No se usa en v1; alternativa solo si un dato no está en REST. | — | — |
| ICMP | Reachability (no es específico del fabricante). | snmp | Horus → router. |

Reglas:

- **Horus no escribe configuración en el router en v1**: el aprovisionamiento es un **script
  RouterOS (`.rsc`) generado por Horus** que el técnico revisa y pega (WG, Traffic Flow, usuario
  SNMPv3, usuario de API de solo lectura, reglas de firewall que permitan a Horus solo desde el
  túnel). Escribir por API convertiría a Horus en gestor de configuración (otro dominio y otro
  nivel de riesgo); se reevaluará como incremento propio ([Q23](../open-questions/architecture.md#q23)).
- Credenciales en el router con **mínimo privilegio**: grupo de usuario con políticas de solo
  lectura (`read`, `rest-api`/`api`), acceso restringido a la IP del túnel de Horus. Detalle en
  [vendors/mikrotik.md](../vendors/mikrotik.md) y [security.md](../security.md).
- Particularidades conocidas a tratar en el adaptador (confirmar en `vendors/mikrotik.md`): punto
  del flujo respecto al NAT (pre/post-NAT, crítico para [Q6](../open-questions/architecture.md#q6)),
  marcas de tiempo relativas a `sysUptime` en v9, índices de interfaz de los flujos frente a
  `ifIndex` de SNMP, interfaces dinámicas (PPPoE) que **no** se sondean por SNMP, y soporte de
  muestreo de Traffic Flow.

### 2. Diseño de adaptadores: por capacidad, no por fabricante monolítico

Cada módulo define el **puerto** (interfaz Go) de la capacidad que consume y los adaptadores de
fabricante viven junto al módulo dueño:

| Capacidad (puerto) | Módulo dueño | Adaptador MikroTik | Fallback genérico |
| --- | --- | --- | --- |
| `SNMPProfile`: OIDs, normalización, detección por `sysObjectID` | snmp | prefijo `1.3.6.1.4.1.14988` | MIB-II + IF-MIB + HOST-RESOURCES |
| `FlowExporterQuirks`: plantillas, tiempos, dirección, mapeo de interfaces | flows | RouterOS Traffic Flow v9/IPFIX | IPFIX/v9/v5/sFlow estándar |
| `DeviceFacts`: identidad, interfaces, IPs, pools | devices | REST RouterOS v7 | SNMP (sysDescr, ifTable, ipAddrTable) |
| `ProvisioningTemplate`: script de alta (WG, flujos, SNMPv3, firewall) | wireguard (+devices) | plantilla `.rsc` versionada | instrucciones manuales genéricas |

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
- (−) El alta de un router requiere un paso manual (pegar el script) por router.
- (−) Las particularidades de Traffic Flow (NAT, tiempos) deben validarse con equipos reales antes
  de confiar en la atribución por IP.
- Impacto: [database.md](../database.md) (`vendor_key`, matriz de capacidades, `client_prefixes`
  del realm), [traffic-model.md](../traffic-model.md) (quirks de RouterOS),
  [security.md](../security.md) (credenciales de solo lectura, SNMPv3),
  [vendors/mikrotik.md](../vendors/mikrotik.md).
