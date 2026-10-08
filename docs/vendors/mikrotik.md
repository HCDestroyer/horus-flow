# Integración MikroTik (RouterOS) — Horus Flow

> Estado: **borrador ronda 2** · Dueño: Agente E (Integración MikroTik) ·
> Base: [`po-decisions.md`](../po-decisions.md) D1, D5, D6, D10.
>
> Relacionados: [`traffic-model.md`](../traffic-model.md) (registro canónico de flujo, muestreo,
> atribución, `flow_role`), [`services.md`](../services.md) (`flows`, `snmp`, `wireguard`,
> `devices`), [Q3/Q9 de `open-questions/architecture.md`](../open-questions/architecture.md),
> [Q2/Q5/Q6 de `open-questions/data.md`](../open-questions/data.md). El **ADR-0022** (Agente A)
> registra la decisión de arquitectura sobre MikroTik y referencia este documento.
>
> **Sobre las fuentes**: los datos de RouterOS se contrastaron con la documentación oficial
> (help.mikrotik.com / manual.mikrotik.com) a través de buscadores; el acceso directo a esas
> páginas estaba bloqueado desde el entorno de trabajo, así que **todo lo marcado "a verificar"
> debe comprobarse en el laboratorio CHR (§8)** antes de darlo por cierto. Las fuentes están en §10.

---

## 0. Resumen para otros agentes

| Tema | Decisión / recomendación | Para quién |
| --- | --- | --- |
| Versión | **RouterOS v7** obligatorio para el camino completo (WireGuard + REST). Mínima propuesta **7.12**; recomendada la **última long-term v7**. v6 solo en "modo degradado" (§1.2). | A, D |
| Exportación de flujos | **IPFIX** (alternativa NetFlow v9), **sin muestreo**, `active-flow-timeout=1m`, `inactive-flow-timeout=15s`, `cache-entries` ≥ 256k en nodos grandes, destino = colector de Horus **por el túnel WG** con `src-address` = IP de túnel. | B, D |
| IP del cliente | Traffic Flow contabiliza en las cadenas input/forward/output: si el **NAT/CGNAT lo hace el mismo router**, las direcciones exportadas son las **privadas del cliente (pre-NAT)** en ambos sentidos. Comportamiento esperado, **a verificar en laboratorio**. | B |
| Identidad del exportador | La **IP de túnel WG** del router (única en todo Horus), no su IP pública. | B, A |
| Ceguera por hardware | Tráfico con **offload por hardware** (bridge HW, L3HW en CCR2116/2216, FastTrack HW) **no** aparece en los flujos. Hay que detectarlo y avisar (§2.7). | B, D |
| SNMP | **SNMPv3 authPriv (SHA1 + AES)** sobre el túnel; v2c solo en laboratorio. Sondeo 60 s (sistema/interfaces), 300 s (salud). No recorrer interfaces PPPoE dinámicas. | A, D |
| API | **REST** (`https://<ip-túnel>/rest`) para lecturas puntuales; **API binaria TLS (8729)** para tablas grandes y sondeo periódico. Usuario `horus-ro` con política mínima y `address=` restringida. **Horus no escribe en el router en v1** (Q9 opción i). | A, D |
| WireGuard | El router es **iniciador** hacia el hub de Horus (`persistent-keepalive=25s`), clave generada **en el router** (Q9 opción b), `allowed-address` = solo la red de servicios de Horus. | A, D |
| Onboarding | Un script RouterOS con placeholders (§7) que Horus genera por nodo. | D |
| Laboratorio | CHR en QEMU/KVM (licencia free: 1 Mbps de subida por interfaz, suficiente para pruebas funcionales) + capturas IPFIX grabadas como *fixtures* para CI (§8). | D, todos |

---

## 1. Versiones soportadas y equipos

### 1.1 RouterOS v7 (camino soportado)

| Capacidad que usa Horus | Disponible desde | Nota |
| --- | --- | --- |
| WireGuard nativo (`/interface wireguard`) | v7.1 | No existe en v6. |
| API REST (`/rest`, JSON sobre HTTPS `www-ssl`) | v7.1beta4 | Sobre HTTP (`www`) solo desde v7.9; Horus **no** lo usará sin TLS. |
| Política de grupo `rest-api` separada de `api` | v7.x (versión exacta **a verificar**) | En algunas versiones (reportado en 7.11) REST exigía la política `api`. Probar ambas. |
| Traffic Flow v5 / v9 / IPFIX | v6 y v7 | IPFIX con selección de campos en `/ip traffic-flow ipfix`. |
| Muestreo de paquetes en Traffic Flow | v7 | Horus recomienda **no** usarlo en el router de nodo (§2.6). |
| SNMP v1/v2c/v3 | v6 y v7 | v3 con SHA1/AES; SHA2 **a verificar** en la versión elegida. |

**Versión mínima propuesta: 7.12** (primera rama v7 ampliamente desplegada con WireGuard y REST
estables; **a verificar** contra las notas de versión). **Recomendada: la última versión del canal
*long-term* v7** publicada en mikrotik.com/download. Horus debe leer la versión por SNMP/API y
marcar el router como `unsupported_version` si está por debajo de la mínima (aviso, no bloqueo).

### 1.2 ¿Qué pasa con RouterOS v6?

La última rama mantenida es **6.49.x (long-term)**; las anteriores están fuera de soporte. MikroTik
sigue publicando correcciones de seguridad para 6.49, pero la fecha de fin de vida definitiva
**está por confirmar** en fuentes oficiales.

| Función | v6 | Consecuencia |
| --- | --- | --- |
| WireGuard | **No** | El túnel tendría que ser otro (SSTP, L2TP/IPsec, OpenVPN) → fuera de alcance v1. |
| REST | **No** | Solo API binaria (8728/8729) o SSH. |
| Traffic Flow v5/v9 | Sí | IPFIX en v6 **a verificar** (se recomienda v9 en v6). |
| SNMP v2c/v3 | Sí | Igual que v7. |

**Recomendación**: v6 **no soportado** en el primer entregable. Si el PO lo requiere, un "modo
degradado" posterior: el router v6 exporta NetFlow v9 a una IP pública de Horus filtrada por IP de
origen, SNMPv3 por Internet o por un túnel alternativo, sin API REST. La vía preferida es
**actualizar a v7** (los CCR1009/1036/1072, RB1100AHx4, RB4011 y similares la admiten).

### 1.3 Equipos típicos en el router principal de un nodo de ISP

| Familia | Ejemplos | Notas para Horus |
| --- | --- | --- |
| **CCR1000** (Tilera, heredado) | CCR1009, CCR1016, CCR1036, CCR1072 | Muchos núcleos lentos; el rendimiento por núcleo limita Traffic Flow y conntrack. Admiten v7 (**a verificar** modelo por modelo). |
| **CCR2000** (ARM64) | CCR2004-1G-12S+2XS, CCR2004-16G-2S+, CCR2116-12G-4S+, CCR2216-1G-12XS-2XQ | CCR2116/2216 tienen **L3HW offload** (chip de switch Marvell): el tráfico enrutado en hardware **no pasa por la CPU y no genera flujos**. |
| **RouterBOARD** | RB5009, RB4011, RB1100AHx4, hEX (nodos pequeños) | Suficiente para nodos de cientos de clientes; vigilar CPU y RAM de la caché de flujos. |
| **CHR** (x86/VM) | CHR en servidor x86 (habitual como concentrador PPPoE/BNG) | Sin offload de hardware: todo el tráfico pasa por CPU → flujos completos. Licencia por velocidad (p1/p10/unlimited). |

---

## 2. Traffic Flow (NetFlow v5/v9/IPFIX)

### 2.1 Cómo funciona en RouterOS

- Traffic Flow se engancha **al final de las cadenas `input`, `forward` y `output`**: solo
  contabiliza lo que llega a esas cadenas. El tráfico conmutado en un bridge con offload de
  hardware, o enrutado por L3HW, no llega y **no se exporta**.
- Mantiene una **caché** de flujos activos en RAM (`cache-entries`) y los exporta cuando expira un
  temporizador (activo o inactivo) o cuando la caché se llena.
- **Sin muestreo por defecto** (`packet-sampling=no`): cada paquete reenviado actualiza su flujo.
  Coherente con la recomendación de [`traffic-model.md` §4](../traffic-model.md).
- **FastTrack**: la página oficial de *Connection tracking* lista lo que FastTrack se salta; para
  Traffic Flow la restricción **se eliminó en v6.33** (los paquetes acelerados por software sí se
  contabilizan). **A verificar en laboratorio** comparando bytes de flujos con contadores de
  interfaz con FastTrack activo. FastTrack **por hardware** (en equipos que lo soportan) sí queda
  fuera, por pasar por el chip de switch.

### 2.2 Parámetros y valores recomendados

`/ip traffic-flow`:

| Parámetro | Defecto RouterOS | Recomendado Horus | Por qué |
| --- | --- | --- | --- |
| `enabled` | `no` | `yes` | |
| `interfaces` | `all` | `all` (v1) | Las interfaces PPPoE son dinámicas y no se pueden enumerar; Horus filtra por `flow_role` de la interfaz de entrada/salida. Hay reportes de que el filtro de interfaces no se respeta en algunos CCR (**a verificar**). |
| `cache-entries` | `4k` | `64k` (≤ 500 clientes), `256k` (≤ 5 000), `512k`+ (mayor) | Si la caché se llena, el router exporta antes de tiempo o descarta (**a verificar** cuál). Valores enumerados en la doc: 1k…512k (v7 puede admitir más, **a verificar**). Coste en RAM ≈ decenas de MB a 256k (**a verificar** con `/system resource`). |
| `active-flow-timeout` | `30m` | **`1m`** | Con 30 min, una descarga larga aparece de golpe media hora después: rompe los agregados de 5 min y la detección casi en tiempo real (D5). 1 min alinea con la ventana de pre-agregación de 60 s ([`traffic-model.md` §10](../traffic-model.md)). |
| `inactive-flow-timeout` | `15s` | `15s` | Más bajo multiplica registros y puede desbordar la caché (advertencia de la doc oficial). |
| `packet-sampling` | `no` | `no` | Ver §2.6. |
| `sampling-interval` / `sampling-space` | `0` / `0` | — | Solo si se activa muestreo. |

`/ip traffic-flow target`:

| Parámetro | Recomendado | Nota |
| --- | --- | --- |
| `dst-address` | IP del colector de Horus **dentro del túnel WG** (p. ej. `10.255.0.1`) | |
| `port` | `4739` (IPFIX) o `2055` (v9) | Puertos estándar que escucha `flows collector`. |
| `version` | **`ipfix`** (alternativa `9`; `5` solo para pruebas) | Opciones: `1`, `5`, `9`, `ipfix`. |
| `src-address` | **IP de túnel WG del router** | Hace que la IP de origen UDP (= `exporter_ip`) sea estable y única en todo Horus aunque el router tenga IP pública dinámica o detrás de NAT. |
| `v9-template-refresh` | `20` (defecto) | Nº de paquetes entre reenvíos de plantilla. |
| `v9-template-timeout` | `1m` | Reenvío de plantilla por tiempo; si el colector reinicia, decodifica en ≤ 1 min. Defecto **a verificar**. |

`/ip traffic-flow ipfix` (solo IPFIX; interruptores `yes/no` por campo): ver §2.3.

### 2.3 Campos que exporta cada versión

| Dato | v5 | v9 | IPFIX | Uso en Horus |
| --- | --- | --- | --- | --- |
| IPv4 origen/destino, puertos, protocolo | Sí | Sí | Sí | Núcleo del registro canónico. |
| **IPv6** origen/destino | **No** | Sí | Sí (incluye `ipv6-flow-label`) | Obligatorio para clientes con IPv6 → descarta v5. |
| Bytes / paquetes | Sí (32 bits) | Sí | Sí (`bytes`, `packets`) | |
| `in-interface` / `out-interface` (ifIndex) | 16 bits | Sí | Sí | Mapeo a `interface_id` y `flow_role`. |
| TCP flags (OR acumulado) | Sí | Sí | Sí (`tcp-flags`) | Escaneos, SYN sin respuesta (D5). |
| ToS / DSCP | Sí | Sí | Sí (`tos`) | Opcional. |
| Next-hop, máscaras, src/dst AS | Sí | Sí | Sí | Solo pista; el ASN canónico sale del catálogo. |
| Inicio/fin de flujo | `first`/`last` relativos a sysUptime | relativos a sysUptime | `first-forwarded`/`last-forwarded` + **`sys-init-time`** | Activar `sys-init-time` para que el colector calcule tiempos absolutos (IE 160). **NTP obligatorio** en el router. |
| **MAC origen/destino** | No | **a verificar** | Sí (`src-mac-address`, `dst-mac-address`) | MAC del **siguiente/anterior salto L2** (el CPE o el equipo intermedio), **no** de los dispositivos detrás del CPE. |
| **NAT** (`nat-src-address`, `nat-dst-address`, `nat-src-port`, `nat-dst-port`, `nat-events`) | No | No | Sí | Dirección traducida (post-NAT). Útil para correlacionar quejas de abuso que llegan con la IP pública; **sensible** (ver §9). Desactivado por defecto en Horus. |
| TTL (mín/máx) | No | **a verificar** | Sí (nombres exactos **a verificar** con `/ip traffic-flow ipfix print`) | Señal para estimar NAT/dispositivos detrás del CPE (§6). |
| Muestreo | campo de cabecera | Options template (**a verificar**) | **a verificar** | Si no viene, Horus usa `declared_sampling_rate` = 1. |

**Recomendación**: IPFIX con todos los campos de la tabla activados **excepto** los NAT (activables
por ISP si el PO lo aprueba). El colector debe leer plantillas, no asumir un orden de campos, y
tolerar campos ausentes. El nombre exacto de cada interruptor de `/ip traffic-flow ipfix` varía
entre versiones: el script de onboarding (§7) solo toca los que existen en la versión mínima
(**a verificar** en CHR).

### 2.4 Qué interfaz elegir para ver la IP del cliente (antes del NAT)

En RouterOS el NAT de origen (`srcnat`) se aplica en *postrouting*, **después** de `forward`, y el
des-NAT de las respuestas ocurre en *prerouting*, **antes** de `forward`. Como Traffic Flow cuenta
en `forward`:

| Topología del nodo | IP de cliente observada | Atribución (D1) |
| --- | --- | --- |
| **IP pública por cliente** (sin NAT) | Pública del cliente | Directa. |
| **NAT/CGNAT en el mismo router principal** | **Privada del cliente (pre-NAT) en subida y bajada** — esperado, **a verificar en laboratorio** | Directa en el *realm* del router ([`traffic-model.md` §4.2](../traffic-model.md)). La IP pública compartida solo aparece si se activan los campos `nat-*`. |
| **CGNAT en otro equipo, detrás del router principal** (hacia Internet) | Privada/CGNAT del cliente (el router está antes del NAT) | Directa. |
| **CGNAT en otro equipo, entre clientes y router principal** | IP pública compartida: **no identifica al cliente** | No atribuible sin logs de NAT del otro equipo. Hay que exportar desde el equipo de CGNAT o el BNG. Pregunta al PO (§9). |
| **NAT en el CPE del cliente** (residencial típico) | WAN del CPE | Una IP = un cliente (D1). Los dispositivos detrás no son visibles. |

Conclusión práctica: **no hace falta elegir una interfaz especial** si el NAT está en el propio
router; basta `interfaces=all` y que Horus marque como `customer_edge` las interfaces del lado
cliente (VLAN de acceso, interfaces PPPoE dinámicas, bridge LAN) y como `upstream` las de salida.

**Particularidades de ifIndex en RouterOS** (importante para el Agente B):

- Cada sesión **PPPoE** crea una interfaz dinámica `<pppoe-usuario>` con un ifIndex que **cambia
  en cada reconexión**. Horus no debe crear un `interface_id` por sesión: tratar todas las
  interfaces dinámicas PPPoE/L2TP como una interfaz lógica `customer_edge (dinámica)` y atribuir
  por IP. Propuesta: regla por nombre (`pppoe-*`, `<l2tp-*>`) obtenida por SNMP/API.
- VLANs y bridges tienen ifIndex propio; con offload de bridge activo, el tráfico intra-bridge no
  se ve (no es problema: es tráfico local entre clientes del mismo segmento).

### 2.5 Volumen y CPU

Estimaciones de orden de magnitud (**a validar en laboratorio y en un nodo real**):

| Nodo | Clientes | Flujos nuevos/s (pico) | Registros IPFIX/s con `active=1m` | Ancho de banda de exportación |
| --- | --- | --- | --- | --- |
| Pequeño | 300 | 300–1 500 | 400–2 000 | 0,3–1,5 Mbit/s |
| Mediano | 2 000 | 2 000–10 000 | 3 000–12 000 | 2–8 Mbit/s |
| Grande | 10 000 | 10 000–50 000 | 15 000–60 000 | 10–40 Mbit/s |

Supuestos: 1–5 flujos nuevos/s por cliente en pico (más con P2P o equipos infectados que
escanean), ~60–90 B por registro IPFIX v4 (más en v6). La exportación consume subida del nodo y
viaja por el túnel WG (+ ~60 B de sobrecarga por paquete).

Recomendaciones:

- Vigilar por SNMP la **CPU por núcleo** (`hrProcessorLoad`) además de la media: Traffic Flow y
  conntrack cargan núcleos concretos. Umbral de aviso propuesto: 70 % sostenido 5 min en cualquier
  núcleo tras activar Traffic Flow.
- Medir antes y después de activar Traffic Flow (CPU, `/system resource` free-memory) durante el
  onboarding; Horus debe mostrar esta comparación en el asistente.
- Un pico de flujos por un cliente infectado (escaneo masivo) **es justo lo que D5 quiere
  detectar**: no limitar por muestreo; si la CPU no aguanta, preferir mejorar el equipo.

### 2.6 Muestreo

El router de nodo debe exportar **sin muestreo**: con 1:N los flujos cortos (escaneos, DNS,
intentos de C2) desaparecen y la detección de botnets de D5 pierde sensibilidad
([`traffic-model.md` §4](../traffic-model.md)). Si un ISP lo activa, Horus debe conocer la tasa
(`sampling-interval`/`sampling-space`) para escalar bytes y bajar la confianza de las detecciones;
cómo se comunica la tasa en IPFIX de RouterOS está **a verificar**.

### 2.7 Limitaciones conocidas y cómo detectarlas

| Limitación | Efecto | Detección en Horus |
| --- | --- | --- |
| Offload por hardware (bridge HW, L3HW CCR2116/2216, FastTrack HW) | Tráfico invisible en flujos | Comparar bytes de flujos `customer_edge` vs `ifHCInOctets/ifHCOutOctets` del uplink por SNMP cada hora; si la cobertura < 80 % → alerta `flow_coverage_low`. Leer por API `/interface/ethernet/switch` (`l3-hw-offloading`) **a verificar** ruta. |
| Caché llena | Exportaciones prematuras o pérdida | Comparar cobertura como arriba; recomendar más `cache-entries`. |
| Pérdida UDP (Internet, túnel) | Huecos | Secuencia de IPFIX (`sequenceNumber` de cabecera) → métrica de pérdida por exportador. |
| Reloj del router desfasado | Tiempos erróneos | Comparar `exportTime` con hora de recepción; alerta si > 30 s. NTP en el onboarding. |
| Filtro de `interfaces` ignorado en algunos CCR (reporte de foro) | Más volumen | Horus filtra igualmente por `flow_role`. |
| Paquetes UDP grandes por el túnel (MTU WG 1420) | Fragmentación | **A verificar** tamaño máximo de datagrama IPFIX de RouterOS; ajustar MTU WG si hace falta. |

---

## 3. SNMP

### 3.1 v2c vs v3 en RouterOS

| | v2c | v3 |
| --- | --- | --- |
| Seguridad | Comunidad en claro | `security=authorized` (auth) o **`private` (auth + cifrado)** |
| Algoritmos | — | Auth: MD5, SHA1 (SHA2 **a verificar** en la versión mínima). Cifrado: DES, AES (AES-128). |
| Configuración | `/snmp community` con `name` = comunidad | `/snmp community` con `name` = **usuario v3**, `authentication-password`, `encryption-password` (mín. 8 caracteres) |
| Restricción | `addresses=` (origen permitido) | Igual |
| Uso en Horus | Solo laboratorio | **Producción: authPriv SHA1 + AES**, aun dentro del túnel (defensa en profundidad) |

Además: desactivar o restringir la comunidad `public` por defecto, `write-access=no`, y fijar
`addresses=` a la red de servicios de Horus. El *engine ID* se genera automáticamente; si se clona
la configuración entre routers, **a verificar** que no se dupliquen engine IDs.

### 3.2 OIDs útiles

Estándar (MIB-II, IF-MIB, HOST-RESOURCES):

| Dato | OID | Nota |
| --- | --- | --- |
| `sysDescr`, `sysObjectID`, `sysUpTime`, `sysName` | `1.3.6.1.2.1.1.{1,2,3,5}.0` | `sysDescr` incluye "RouterOS <modelo>"; reinicios por `sysUpTime`. |
| `ifName`, `ifAlias`, `ifHighSpeed` | `1.3.6.1.2.1.31.1.1.1.{1,18,15}` | Descubrimiento de interfaces. |
| `ifHCInOctets`, `ifHCOutOctets` | `1.3.6.1.2.1.31.1.1.1.{6,10}` | **Contadores de 64 bits**: usar siempre (los de 32 bits dan la vuelta en segundos a 10 Gbit/s). |
| `ifHCInUcastPkts`, `ifHCOutUcastPkts` | `1.3.6.1.2.1.31.1.1.1.{7,11}` | |
| `ifOperStatus`, `ifAdminStatus` | `1.3.6.1.2.1.2.2.1.{8,7}` | |
| `ifInErrors`, `ifOutErrors`, `ifInDiscards`, `ifOutDiscards` | `1.3.6.1.2.1.2.2.1.{14,20,13,19}` | |
| `hrProcessorLoad` | `1.3.6.1.2.1.25.3.3.1.2` | **Una fila por núcleo**. |
| `hrStorageTable` (RAM, disco) | `1.3.6.1.2.1.25.2.3.1` | Memoria principal en la fila de tipo `hrStorageRam`. |

MIKROTIK-MIB (raíz `1.3.6.1.4.1.14988.1.1`):

| Dato | OID | Nota |
| --- | --- | --- |
| Rama de salud `mtxrHealth` | `…1.1.3` | |
| Temperatura de placa `mtxrHlTemperature` | `…1.1.3.10.0` | Décimas de °C. No existe en todos los modelos. |
| Temperatura de CPU `mtxrHlProcessorTemperature` | `…1.1.3.11.0` | Décimas de °C. |
| Voltaje | `…1.1.3.8.0` (reportado en foro) | Fuentes contradictorias: **a verificar**. |
| Tabla de sensores v7 (`mtxrGaugeTable`: nombre, valor, unidad) | `…1.1.3.100.1` | En v7 la salud se expone como tabla genérica de sensores (**a verificar**). Preferible a OIDs fijos. |
| Licencia `mtxrLicense` (nivel `mtxrLicLevel`) | `…1.1.4` (`…1.1.4.3.0`) | |
| Sistema (nº de serie, firmware, versión) `mtxrSystem` | `…1.1.7` | Subíndices **a verificar**. La versión de RouterOS también sale de `sysDescr` o de la API. |
| Colas simples `mtxrQueueSimpleTable` | `…1.1.2.1` | Nombre, destino, bytes/paquetes por cola. |
| Árbol de colas `mtxrQueueTreeTable` | `…1.1.2.2` | |
| Vecinos (MNDP/CDP/LLDP) `mtxrNeighbor` | `…1.1.11` | Topología opcional. |

**Regla para el adaptador MikroTik de `snmp`**: los OIDs exactos de cada modelo se obtienen en el
propio router con `print oid` (`/system health print oid`, `/system resource print oid`,
`/interface print oid`, `/queue simple print oid`). El laboratorio (§8) debe guardar esas salidas
como *fixtures*.

### 3.3 Intervalos y límites

| Grupo | Intervalo | Nota |
| --- | --- | --- |
| Reachability ICMP | 30 s | Por el túnel. |
| Sistema (uptime, CPU por núcleo, RAM) | 60 s | |
| Interfaces físicas, VLAN y uplinks | 60 s | Solo las marcadas en `devices`. |
| Salud (temperatura, voltaje, ventiladores, PSU) | 300 s | |
| Licencia, versión, inventario | 1 h / al reconectar | |
| Colas simples | **No en v1** (ver Q6) | Con miles de colas, un *walk* tarda y carga la CPU. |

Límites:

- **No recorrer las interfaces PPPoE dinámicas** con SNMP: en un concentrador con miles de sesiones
  `ifTable` tiene miles de filas que cambian continuamente; el *walk* es lento y el agente SNMP de
  RouterOS consume CPU. Descubrir con un *walk* de `ifName` (GETBULK, `max-repetitions` 25–50) una
  vez por hora y luego hacer GET solo de los ifIndex seleccionados.
- Timeouts recomendados: 3 s, 2 reintentos; un poller por router a la vez (sin paralelismo
  intra-router).

---

## 4. API: REST vs API binaria vs SSH

| | REST (`/rest`) | API binaria (8728 / **8729 TLS**) | SSH (22) |
| --- | --- | --- | --- |
| Versión | v7.1+ | v6 y v7 | v6 y v7 |
| Transporte | HTTPS (`www-ssl`, 443), Basic Auth en **cada** petición | Sesión TCP persistente, login una vez | Sesión interactiva / comandos |
| Formato | JSON; `.proplist` y `.query` en `POST …/print` | Palabras de protocolo propio; consultas `?`; comandos `listen`/`follow` para cambios | Texto CLI (frágil de parsear) |
| Límites | **Timeout de 60 s** por petición; coste de autenticación por llamada | Sin timeout fijo; adecuada para tablas grandes | — |
| Uso en Horus | Lecturas puntuales e inventario: versión, `/system/resource`, `/interface`, `/ip/address`, estado de `/ip/traffic-flow`, comprobación del onboarding | Sondeo periódico de tablas grandes: `/ppp/active`, `/ip/dhcp-server/lease`, `/ip/arp`; conteos de conexiones (§6) | Solo diagnóstico manual; nunca en el camino automático |

**Recomendación**: implementar el adaptador sobre la **API binaria con TLS (8729)** como camino
principal (sirve también para v6 si se soporta más adelante) y REST para lecturas simples y
pruebas. Ambas exigen certificado en el router (§4.3).

### 4.1 Qué lee Horus y para qué

| Ruta RouterOS | Para qué en Horus | Frecuencia |
| --- | --- | --- |
| `/system/resource`, `/system/routerboard`, `/system/package` | Modelo, versión, arquitectura, CPU/RAM | 1 h |
| `/interface` (+ `/interface/vlan`, `/interface/pppoe-server/server`) | Interfaces y sugerencia de `flow_role` | 1 h y en onboarding |
| `/ip/address`, `/ipv6/address`, `/ip/pool`, `/ipv6/pool` | Rangos de clientes → *realm* y prefijos de clientes (atribución) | 1 h |
| `/ppp/active` | IP ↔ usuario PPPoE (alias opcional del cliente; **dato personal**) | 60 s (o `listen`) |
| `/ip/dhcp-server/lease` | IP ↔ MAC ↔ hostname (alias opcional; fabricante del CPE por OUI) | 60 s |
| `/ip/arp` | IP ↔ MAC en segmentos L2 directos | 5 min |
| `/ip/firewall/nat` | Saber si el router hace NAT/CGNAT (§2.4) | onboarding / 1 h |
| `/ip/traffic-flow`, `/ip/traffic-flow/target` | Verificar que la exportación está bien configurada (deriva de configuración) | 1 h |
| `/interface/wireguard/peers` | Estado del túnel visto desde el router | 5 min |
| `/ip/firewall/connection` | Conteo de conexiones (§6) | Ver §6, con cautela |
| `/queue/simple` | Plan contratado (max-limit) como pista residencial/comercial | 1 h, fuera de v1 |

D1 dice que **no** hace falta CRM: estas lecturas son **enriquecimiento opcional**. El cliente
sigue siendo la IP; usuario PPPoE, MAC y hostname se guardan como alias con retención limitada.

### 4.2 Usuario de solo lectura con política mínima

```routeros
/user group add name=horus-ro \
    policy=read,api,rest-api,!write,!policy,!sensitive,!local,!telnet,!ssh,!ftp,!reboot,!test,!winbox,!password,!web,!sniff,!romon \
    comment="horus"
/user add name=horus group=horus-ro password="<HORUS_API_PASSWORD>" \
    address=<HORUS_SERVICES_CIDR> comment="horus"
```

- `read` sin `sensitive`: Horus no ve contraseñas, claves WireGuard privadas ni secretos PPP.
- `address=` restringe el login a la red de servicios de Horus (por el túnel).
- Si la versión no tiene la política `rest-api`, quitarla (**a verificar** en 7.12).
- **Horus no escribe en el router en v1** (Q9 opción i). Una futura función "aplicar Traffic Flow
  automáticamente" requeriría un segundo usuario `horus-cfg` con `write`, deshabilitado por
  defecto, que el ISP active solo durante la operación. Queda fuera del primer entregable.

### 4.3 TLS

- El router necesita un certificado para `www-ssl` y `api-ssl`. Opción v1: certificado
  **autofirmado** generado en el router por el script (§7) y **fijado por huella** en Horus
  (TOFU: Horus registra el SHA-256 en el primer contacto por el túnel y alerta si cambia).
- Opción posterior: Horus actúa de CA interna y firma un CSR del router.
- Restringir servicios: `www-ssl` y `api-ssl` solo desde `<HORUS_SERVICES_CIDR>`; `api` (8728) y
  `www` (80) deshabilitados o restringidos; telnet/ftp deshabilitados (higiene general, decisión del
  ISP).

---

## 5. WireGuard en RouterOS v7

### 5.1 Diseño

```
 Router principal del nodo (iniciador)                 Hub WG de Horus (wireguard-agent)
 wg-horus  10.255.<n>.<m>/32  ──── UDP 51820 ────►     <HORUS_WG_ENDPOINT>:51820
   peer: hub (pubkey Horus)                            peer: router (pubkey del router)
   allowed-address = <HORUS_SERVICES_CIDR>             allowed-ips = 10.255.<n>.<m>/32
   persistent-keepalive = 25s
   ▲ SNMP 161/udp, API 8729, REST 443  ◄── poller/API de Horus
   └ IPFIX 4739/udp ──────────────────────────────►    flows collector
```

| Elemento | Valor | Por qué |
| --- | --- | --- |
| Interfaz | `wg-horus`, `listen-port` cualquiera, MTU 1420 | El router no necesita aceptar conexiones entrantes. |
| Dirección del router | `/32` de la IPAM de `wireguard` (p. ej. `10.255.0.0/16` configurable por despliegue) | Única en todo Horus (multi-tenant) → identidad del exportador y destino de sondeo. Elegir un rango que no colisione con las redes de los ISP; **no** usar 100.64.0.0/10 (CGNAT). |
| Peer (hub) | `public-key` del hub, `endpoint-address`/`endpoint-port` públicos de Horus | |
| `allowed-address` | **Solo** `<HORUS_SERVICES_CIDR>` (hub + colector + pollers) | El router no enruta nada más hacia Horus; Horus no ve redes del ISP. |
| `persistent-keepalive` | `25s` | El router suele estar detrás de NAT o con IP dinámica; mantiene el estado NAT y permite que Horus inicie SNMP/API. |
| `preshared-key` | Opcional (`auto` o generada por Horus) | Capa simétrica adicional. Si la genera Horus, viaja en el script: tratar como secreto. |
| Rutas | `/ip route` a `<HORUS_SERVICES_CIDR>` por `wg-horus` | **Nunca** ruta por defecto por el túnel. |
| Firewall | `input` acepta desde `wg-horus` solo 161/udp, 8729/tcp, 443/tcp, ICMP con origen `<HORUS_SERVICES_CIDR>` | |

Por qué el túnel sirve para **gestión y flujos**:

- **Gestión**: SNMP y API quedan sin exposición a Internet; funciona aunque el router esté tras NAT
  o con IP dinámica (el router inicia y el keepalive mantiene el camino de vuelta).
- **Flujos**: IPFIX/NetFlow es UDP sin cifrar ni autenticar; por el túnel viaja cifrado y
  autenticado por clave, y Horus **solo acepta flujos desde IPs de túnel conocidas** (evita
  inyección de flujos falsos y suplantación de exportador).
- **Coste**: el hub es punto único de fallo del plano de recolección (Q3); si cae, se pierden
  flujos (UDP sin búfer en el router). Mitigación futura: segundo hub y un segundo target de Traffic
  Flow.

### 5.2 Qué hace Horus automáticamente y qué es manual

| Paso | Automático (Horus) | Manual (técnico del ISP) |
| --- | --- | --- |
| Crear nodo/router en la UI | | Sí |
| Asignar IP de túnel, generar script con placeholders resueltos | Sí | |
| Pegar el script en el router (Terminal de WinBox/WebFig/SSH) | | Sí |
| Generar la clave privada del router | En el router (al crear la interfaz sin `private-key`) | |
| Registrar la clave pública del router en Horus | Opción A: el técnico copia la salida del script a la UI. Opción B (recomendada si se aprueba): el script hace `/tool fetch` a un endpoint de *enrolment* de Horus con un **token de un solo uso** y envía la clave pública por HTTPS | A: Sí / B: No |
| Alta del peer en el hub | Sí (`wireguard-agent`) | |
| Verificar handshake, ICMP, SNMP, API, recepción de IPFIX | Sí (checklist del asistente de onboarding) | |
| Proponer `flow_role` de interfaces | Sí (por nombre/tipo vía API) | Confirmar |
| Rotación de claves | Genera un script nuevo | Pegarlo |

La opción B convierte el alta en "un solo pegado", pero exige un endpoint público de Horus (decisión
para el Agente A / ADR-0022). Con cualquiera de las dos, la **clave privada nunca sale del router**.

---

## 6. Datos útiles para las detecciones (D5 y residencial/comercial)

| Fuente MikroTik | Qué aporta | Viable a escala | Recomendación |
| --- | --- | --- | --- |
| **Flujos IPFIX** (§2) | Fan-out de destinos, escaneos (SYN sin respuesta por `tcp-flags`), SMTP saliente (25/tcp), periodicidad tipo *beacon*, volumen, P2P, puertos de C2 conocidos | **Sí** — es la fuente principal | Base de todas las detecciones de v1. |
| **TTL en IPFIX** | Distintos TTL iniciales (64 Linux/Android/iOS, 128 Windows) desde una misma IP ⇒ varios sistemas detrás del CPE; pista de uso comercial (muchos equipos) | **Sí** si el campo existe (**a verificar**) | Activar; heurística con confianza baja. |
| **MAC (IPFIX `src-mac-address`, DHCP leases, ARP)** | MAC del **CPE** (no de los dispositivos detrás); fabricante por OUI (router doméstico vs equipo empresarial) | Sí (barato) | Enriquecimiento; no sirve para contar dispositivos detrás de un CPE con NAT. |
| **Connection tracking** (`/ip/firewall/connection`) | Conexiones activas por IP de cliente en un instante | **Limitado**: en un nodo grande hay cientos de miles de entradas; volcarlas cada minuto carga la CPU y supera el timeout REST | Solo totales (`/ip/firewall/connection/tracking`: total de entradas) cada 5 min por SNMP/API; conteo por cliente bajo demanda (investigación de un caso) con la API binaria y `.proplist=src-address`. Los flujos dan la misma señal de forma continua. |
| **DNS cache** (`/ip/dns/cache`) | Dominios resueltos, solo si el router es el resolver de los clientes | **Bajo**: no guarda qué cliente preguntó; muchos ISP usan resolvers propios | No en v1. Mejor opción futura: logs dnstap de los resolvers del ISP ([`traffic-model.md` §7](../traffic-model.md)). |
| **Firewall address-lists** | (1) Leer listas que el ISP ya mantiene (bloqueos, morosos, "clientes empresa"); (2) en el futuro, que Horus publique una lista de IPs C2 y el router cuente/bloquee | (1) Sí, lectura barata. (2) Requiere escritura → fuera de v1 | (1) Lectura opcional como etiqueta. (2) Mitigación en una fase posterior, con aprobación del PO. |
| **Colas simples** (`/queue/simple`) | Plan contratado (velocidad), nombres que a veces incluyen "empresa" | Sí (lectura horaria) | Pista para el scoring residencial/comercial; fuera de v1. |
| **PPPoE activas / leases** | Alias legible y estabilidad de identidad cuando la IP cambia | Sí | Opcional (D1 no lo exige); dato personal. |
| **Logs (syslog)** de firewall | Eventos de reglas | Volumen alto, formato libre | No en v1. |

Conclusión: para D5 **los flujos sin muestrear son suficientes en v1**; la API aporta contexto
(NAT sí/no, rangos de clientes, alias) y SNMP aporta salud y la verificación de cobertura.

---

## 7. Script de onboarding (ejemplo)

Horus genera este script por router, sustituyendo los placeholders. Todo lo creado lleva
`comment="horus"` para poder auditarlo y desinstalarlo. Probado conceptualmente; **validar línea a
línea en CHR con la versión mínima** antes de entregarlo (sintaxis de `ipfix` y de certificados en
particular).

Placeholders:

| Placeholder | Ejemplo | Origen |
| --- | --- | --- |
| `<ROUTER_WG_IP>` | `10.255.3.17` | IPAM de `wireguard` |
| `<HORUS_WG_PUBKEY>` | `base64…` | Hub de Horus |
| `<HORUS_WG_ENDPOINT>` / `<HORUS_WG_PORT>` | `horus.example.net` / `51820` | Despliegue |
| `<HORUS_SERVICES_CIDR>` | `10.255.0.0/24` | Despliegue |
| `<HORUS_COLLECTOR_IP>` | `10.255.0.1` | Despliegue |
| `<SNMP_USER>`, `<SNMP_AUTH_PASS>`, `<SNMP_PRIV_PASS>` | `horus-…`, ≥ 8 caracteres | Generados por Horus por router |
| `<HORUS_API_PASSWORD>` | aleatoria | Generada por Horus por router |
| `<ROUTER_NAME>`, `<NTP_SERVER>` | `nodo-centro`, `pool.ntp.org` | UI |

```routeros
# ===== Horus Flow · onboarding de nodo · RouterOS v7 (>= 7.12) =====
# 0) Identidad y hora (los timestamps de IPFIX dependen de NTP)
/system identity set name="<ROUTER_NAME>"
/system ntp client set enabled=yes servers=<NTP_SERVER>

# 1) Túnel WireGuard hacia Horus (la clave privada se genera aquí y no sale del router)
/interface wireguard add name=wg-horus mtu=1420 comment="horus"
/ip address add address=<ROUTER_WG_IP>/32 interface=wg-horus comment="horus"
/interface wireguard peers add interface=wg-horus name=horus-hub \
    public-key="<HORUS_WG_PUBKEY>" \
    endpoint-address=<HORUS_WG_ENDPOINT> endpoint-port=<HORUS_WG_PORT> \
    allowed-address=<HORUS_SERVICES_CIDR> persistent-keepalive=25s comment="horus"
/ip route add dst-address=<HORUS_SERVICES_CIDR> gateway=wg-horus comment="horus"

# 2) Firewall: permitir gestión solo desde Horus por el túnel (colocar antes de los drop existentes)
/ip firewall filter add chain=input in-interface=wg-horus src-address=<HORUS_SERVICES_CIDR> \
    protocol=udp dst-port=161 action=accept comment="horus snmp" place-before=0
/ip firewall filter add chain=input in-interface=wg-horus src-address=<HORUS_SERVICES_CIDR> \
    protocol=tcp dst-port=443,8729 action=accept comment="horus api" place-before=0
/ip firewall filter add chain=input in-interface=wg-horus src-address=<HORUS_SERVICES_CIDR> \
    protocol=icmp action=accept comment="horus icmp" place-before=0

# 3) Usuario de solo lectura para la API
/user group add name=horus-ro comment="horus" \
    policy=read,api,rest-api,!write,!policy,!sensitive,!local,!telnet,!ssh,!ftp,!reboot,!test,!winbox,!password,!web,!sniff,!romon
/user add name=horus group=horus-ro password="<HORUS_API_PASSWORD>" \
    address=<HORUS_SERVICES_CIDR> comment="horus"

# 4) Certificado autofirmado para REST (www-ssl) y API-SSL (Horus fija la huella en el primer contacto)
/certificate add name=horus-api common-name=<ROUTER_WG_IP> subject-alt-name=IP:<ROUTER_WG_IP> \
    key-usage=digital-signature,key-encipherment,tls-server days-valid=3650
/certificate sign horus-api
/ip service set www-ssl certificate=horus-api disabled=no
/ip service set api-ssl certificate=horus-api disabled=no
# Restringir orígenes: AÑADIR <HORUS_SERVICES_CIDR> a la lista 'address' existente de cada servicio
# (no sustituirla si el ISP ya gestiona por esos servicios). Ejemplo si no hay restricción previa:
# /ip service set www-ssl address=<HORUS_SERVICES_CIDR>
# /ip service set api-ssl address=<HORUS_SERVICES_CIDR>

# 5) SNMPv3 authPriv, solo lectura, solo desde Horus
/snmp community add name=<SNMP_USER> security=private \
    authentication-protocol=SHA1 authentication-password="<SNMP_AUTH_PASS>" \
    encryption-protocol=AES encryption-password="<SNMP_PRIV_PASS>" \
    addresses=<HORUS_SERVICES_CIDR> read-access=yes write-access=no
/snmp set enabled=yes contact="noc" location="<ROUTER_NAME>"
# Recomendado (decisión del ISP): deshabilitar la comunidad 'public' por defecto
# /snmp community set [find default=yes] disabled=yes

# 6) Traffic Flow (IPFIX, sin muestreo) hacia el colector de Horus por el túnel
/ip traffic-flow set enabled=yes interfaces=all cache-entries=256k \
    active-flow-timeout=1m inactive-flow-timeout=15s packet-sampling=no
/ip traffic-flow target add dst-address=<HORUS_COLLECTOR_IP> port=4739 version=ipfix \
    src-address=<ROUTER_WG_IP> v9-template-refresh=20 v9-template-timeout=1m
# Campos IPFIX (nombres a verificar con: /ip traffic-flow ipfix print)
/ip traffic-flow ipfix set sys-init-time=yes first-forwarded=yes last-forwarded=yes \
    src-mac-address=yes dst-mac-address=yes \
    nat-events=no nat-src-address=no nat-dst-address=no nat-src-port=no nat-dst-port=no

# 7) Salida para Horus: clave pública del router (opción A: copiarla en la UI)
:put ("HORUS_ROUTER_PUBKEY=" . [/interface wireguard get [find name=wg-horus] public-key])
# Opción B (si se aprueba el endpoint de enrolment):
# /tool fetch url="https://<HORUS_WG_ENDPOINT>/enroll/<ONE_TIME_TOKEN>" http-method=post \
#     http-data=[/interface wireguard get [find name=wg-horus] public-key] output=none
```

Notas operativas:

- Si el ISP ya tiene Traffic Flow apuntando a otro colector, **no** cambiar los parámetros globales
  sin avisar: `active-flow-timeout` y `cache-entries` son comunes a todos los targets. El asistente
  debe leer la configuración actual y mostrar el impacto.
- `cache-entries` en equipos con poca RAM (hEX, RB750): usar `64k`.
- `place-before=0` coloca las reglas al principio de `input`; el asistente debe advertir que el ISP
  revise el orden si usa listas de interfaces o reglas personalizadas.
- **Desinstalación**: Horus genera el script inverso (`remove [find comment~"horus"]` en cada menú,
  `/ip traffic-flow target remove` del target de Horus y restaurar los valores globales anteriores).

---

## 8. Laboratorio sin hardware

### 8.1 Opciones

| Opción | Cómo | Pros / contras |
| --- | --- | --- |
| **CHR en QEMU/KVM** (recomendada) | Imagen oficial CHR (`.img`/`.qcow2`/`.vmdk`/`.vdi`) desde mikrotik.com/download, versión mínima y última long-term | Igual que producción (sin offload HW). Licencia **free**: ilimitada en tiempo, **1 Mbit/s de subida por interfaz** → suficiente para pruebas funcionales, no de rendimiento. Prueba de 60 días de p1/p10/unlimited con cuenta de mikrotik.com. |
| CHR "en contenedor" | Imágenes tipo vrnetlab/containerlab (`vr-ros`) o la imagen publicada en Docker Hub como `mikrotik/chr` (verificar el publicador) | En realidad envuelven QEMU: requieren `--privileged` y `/dev/kvm`. Prácticas para docker compose y containerlab. |
| VirtualBox/VMware/Proxmox | Imagen CHR | Para la persona (PO) en su equipo. |
| *Fixtures* grabados | `pcap` de IPFIX/SNMP/REST capturados una vez desde CHR | **CI sin CHR**: los agentes prueban el decodificador y el adaptador con datos reales sin virtualización. |

### 8.2 Topología mínima del primer entregable

```
 [cliente-a] ─┐                          ┌─ [internet-sim] (servidores HTTP/DNS/"C2" de prueba)
 [cliente-b] ─┼─ ether2 (LAN/PPPoE) ─ CHR ─ ether1 (NAT masquerade)
 [cliente-c] ─┘    10.20.0.0/24          │
 (netns Linux)                           └─ wg-horus ──► stack Horus (docker compose):
                                                         wireguard-agent, flows, snmp, devices…
```

- Clientes como *network namespaces* o contenedores Linux que generan tráfico controlado: `curl`
  periódico (beacon), `iperf3` (volumen), `nmap`/`hping3` contra `internet-sim` (escaneo),
  conexiones a 25/tcp (spam simulado).
- CHR con NAT en `ether1` para verificar §2.4 (que la IP exportada es la privada).
- Un segundo CHR con PPPoE server para probar interfaces dinámicas e ifIndex cambiante.

### 8.3 Lista de validación (aceptación técnica)

1. Pegar el script de §7 en CHR limpio de la **versión mínima** y de la **última long-term**: sin
   errores de sintaxis.
2. Handshake WG < 30 s; ICMP, SNMPv3 (`snmpget` desde Horus) y REST/API-SSL responden por el túnel;
   desde fuera del túnel **no**.
3. El colector recibe IPFIX con plantilla, decodifica IPv4 e IPv6, con tiempos absolutos correctos
   (± 2 s con NTP).
4. Con NAT activo, `src_ip` de subida y `dst_ip` de bajada son **IPs privadas** del cliente.
5. Bytes de flujos vs `ifHCOutOctets` de la LAN en 1 h: diferencia < 5 % (sin offload) — con y
   sin FastTrack activo.
6. Los escenarios de tráfico de prueba (beacon, escaneo, SMTP) aparecen como flujos esperados.
7. Interfaces PPPoE: tras reconectar un cliente, el ifIndex cambia y Horus sigue atribuyendo por IP.
8. Usuario `horus` no puede escribir (`/rest/…` con PUT/PATCH devuelve error) ni ver secretos.
9. Guardar como *fixtures*: pcaps de IPFIX (v9 e IPFIX, v4 y v6), salidas `print oid`, respuestas
   JSON de REST de las rutas de §4.1.

---

## 9. Riesgos y preguntas abiertas para el PO

### 9.1 Riesgos

| # | Riesgo | Impacto | Mitigación |
| --- | --- | --- | --- |
| R1 | **Offload por hardware** (CCR2116/2216 L3HW, bridges HW) deja tráfico fuera de los flujos | Subestimación del consumo y botnets invisibles | Verificación de cobertura flujos vs SNMP (§2.7); documentar que el ISP debe desactivar L3HW o aceptar cobertura parcial. |
| R2 | **CPU** del router principal al exportar sin muestreo en nodos grandes (sobre todo CCR1000 heredados) | Degradación del servicio del ISP | Medición antes/después en el onboarding; umbral de aviso; recomendación de equipo. |
| R3 | **CGNAT en un equipo distinto** entre clientes y el router principal | IP observada = pública compartida → D1 no se cumple | Detectar (rangos/NAT por API) y pedir exportación desde ese equipo o logs de NAT. |
| R4 | **Hub WG** como punto único de fallo; UDP sin búfer | Pérdida de flujos durante caídas | Segundo hub/segundo target (Q3); métrica de pérdida por secuencia. |
| R5 | **Diferencias entre versiones** de RouterOS (nombres de campos IPFIX, políticas `rest-api`, OIDs de salud) | Scripts o adaptador rotos | Matriz de versiones probada en CHR; fixtures por versión; aviso `unsupported_version`. |
| R6 | **Datos personales** vía API (usuario PPPoE, hostname, MAC) y campos NAT | Riesgo legal/privacidad (D5 sigue sujeto a minimización) | Desactivados por defecto; retención limitada; acceso por rol. |
| R7 | Cambiar parámetros **globales** de Traffic Flow afecta a otros colectores del ISP | Fricción con el ISP | El asistente muestra la configuración existente y el impacto. |
| R8 | IPv6 con direcciones temporales: "una IP = un cliente" no se cumple en v6 | Clientes inflados en IPv6 | Tratar como cliente el **prefijo delegado** (/56 o /64), no la dirección (decisión a confirmar). |
| R9 | Colisión del rango de túneles con redes internas del ISP | Rutas rotas en el router | Rango configurable por despliegue; comprobación con `/ip/route` antes del onboarding. |

### 9.2 Preguntas abiertas

1. ¿Qué **versiones de RouterOS** y **modelos** tienen hoy los routers principales de los ISP
   objetivo? ¿Hay v6 que no puedan actualizar? (decide si existe el "modo degradado").
2. ¿Dónde está el **NAT/CGNAT** en cada ISP: en el router principal o en otro equipo? (Supuesto
   abierto de `po-decisions.md`.)
3. ¿Los routers usan **L3HW offload** o FastTrack por hardware? ¿Aceptan desactivarlo?
4. ¿Se aprueba el **endpoint de enrolment** (alta en un solo pegado, opción B de §5.2) o se prefiere
   copiar la clave pública a mano?
5. ¿Horus puede leer **usuario PPPoE, hostname y MAC** como alias opcionales? ¿Y activar los campos
   **NAT** de IPFIX para atender quejas de abuso que llegan con la IP pública?
6. En **IPv6**, ¿el cliente es el prefijo delegado? (afecta a D1).
7. ¿Se contempla en el futuro que Horus **escriba** en el router (aplicar Traffic Flow, publicar
   address-lists de C2 para bloqueo)? Implica un usuario con escritura y otro nivel de riesgo.
8. ¿Hay ISP que ya exporten flujos a otro colector (conflicto de parámetros globales)?

---

## 10. Fuentes

Documentación oficial MikroTik (consultada mediante buscador; acceso directo bloqueado desde el
entorno, ver nota inicial):

- Traffic Flow: <https://help.mikrotik.com/docs/spaces/ROS/pages/21102653/Traffic+Flow> ·
  <https://manual.mikrotik.com/docs/diagnostics-monitoring-and-troubleshooting/traffic-flow/> ·
  referencia CLI IPFIX: <https://manual.mikrotik.com/docs/cli-reference/ip/traffic-flow/ipfix>
- Connection tracking (FastTrack): <https://help.mikrotik.com/docs/spaces/ROS/pages/130220087/Connection%2Btracking>
- SNMP (referencia CLI 7.24): <https://manual.mikrotik.com/docs/7.24/cli-reference/snmp>
- REST API: <https://help.mikrotik.com/docs/spaces/ROS/pages/47579162/REST%2BAPI> ·
  <https://manual.mikrotik.com/docs/developer-guides/rest-api>
- Usuarios y grupos: <https://help.mikrotik.com/docs/spaces/ROS/pages/8978504/User> ·
  <https://manual.mikrotik.com/docs/cli-reference/user/group>
- WireGuard: <https://help.mikrotik.com/docs/spaces/ROS/pages/69664792/WireGuard> ·
  <https://manual.mikrotik.com/docs/cli-reference/interface/wireguard/peers/>
- Licencias CHR: <https://manual.mikrotik.com/docs/getting-started/routeros-licensing/chr/chr-licensing/>

Fuentes secundarias (señaladas en el texto como reportes, no como hechos oficiales):

- MIKROTIK-MIB (árbol de OIDs): <https://mibs.observium.org/mib/MIKROTIK-MIB/> ·
  <https://mibs.observium.org/object/MIKROTIK-MIB/mtxrHealth>
- Temperatura en CCR2004 vía SNMP: <https://forum.mikrotik.com/t/ccr2004-cannot-find-the-temperature-oid/148604>
- Pérdida de NetFlow con offload: <https://forum.mikrotik.com/t/any-news-about-sflow-netflow-on-offloaded-switches/182987>
- Filtro de interfaces de Traffic Flow ignorado en CCR: <https://forum.mikrotik.com/t/possible-traffic-flow-bug/169224>
- Política `rest-api` vs `api`: <https://forum.mikrotik.com/t/rest-api-policy/169084>
- Ciclo de vida v6 (6.49 long-term): <https://endoflife.date/routeros> ·
  <https://forum.mikrotik.com/t/6-49-21-long-term-is-released/272802>
- CHR en contenedores para laboratorio: <https://forum.mikrotik.com/t/real-docker-images-for-chr-to-run-in-containerlalb/181934> ·
  <https://hub.docker.com/r/mikrotik/chr>
