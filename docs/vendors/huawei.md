# Integración Huawei (VRP) — Horus Flow

> Estado: **implementado contra documentación, sin equipo real** · Segundo fabricante (roadmap I6:
> «vendors come as adapters»). Relacionados: [`mikrotik.md`](mikrotik.md) (primer fabricante, base del
> diseño), [`traffic-model.md`](../traffic-model.md), [`services.md`](../services.md),
> [Q22 de `open-questions/architecture.md`](../open-questions/architecture.md#q22) (routers sin WireGuard).
>
> **Sobre las fuentes**: todo se construyó a partir de la documentación oficial de Huawei (support.huawei.com /
> info.support.huawei.com: guías de configuración, referencias de comandos y referencias de MIB de NE40E /
> NetEngine 8000 (VRP V8), AR (VRP V5) y CloudEngine), de los módulos MIB publicados por Huawei y de las RFC
> (§10). El acceso directo a support.huawei.com estaba bloqueado desde el entorno de trabajo, así que las
> páginas se consultaron a través del índice del buscador y los módulos MIB en texto (los mismos ficheros que
> Huawei distribuye) desde un espejo público. **No hay hardware Huawei**: todo lo marcado «pendiente de
> validación en equipo real» debe comprobarse con la lista del §9 antes de darlo por cierto. Los fixtures son
> **sintéticos** y lo dicen en su cabecera.

---

## 0. Resumen

| Tema | Decisión | Estado |
| --- | --- | --- |
| Estructura | `router.vendor` (`mikrotik` \| `huawei`) elige el adaptador SNMP (`snmpclient.ForVendor`), la fuente de sondas por enlace y el alta. Núcleo estándar común: IF-MIB (HC, errores, descartes), HOST-RESOURCES-MIB, ENTITY-MIB. MikroTik conserva exactamente su comportamiento (mismos tests y fixtures CHR). | Implementado |
| SNMP Huawei | CPU/memoria/temperatura por tarjeta con HUAWEI-ENTITY-EXTENT-MIB `hwEntityStateTable` (índice `entPhysicalIndex`), tarjetas por `entPhysicalClass = module(9)`, sistema = **MPU activa** (`hwEntityStandbyStatus`). Interfaces IF-MIB HC con nombres VRP (subinterfaces `GE0/1/0.100`, `Eth-Trunk`, `Vlanif`). | Implementado, fixtures sintéticos |
| Pérdida/latencia por enlace | Pruebas **NQA ICMP** que configura el operador; Horus las **lee** por SNMP: DISMAN-PING-MIB (RFC 4560) y, si no, NQA-MIB (`nqaResultsTable`). Mismo modelo de sonda que netwatch (`source = router_nqa`). | Implementado, pendiente de validación |
| Flujos | NetStream v9 (por defecto) o IPFIX con el decodificador existente (+ IE de muestreo 50 y 309/310; IE de empresa de Huawei ignorados). **Decodificador sFlow v5** nuevo (muestras con cabecera, tasa por muestra). | Implementado, fixtures sintéticos + fuzzing |
| Conexión | VRP **no tiene WireGuard**. **Decisión del PO (18/10/2026): IPsec IKEv2 route-based** a un hub strongSwan de Horus como camino estándar y **GRE** como alternativa sin cifrado (con nivel de protección y confirmación si va por Internet); el **modo directo** se mantiene. Detalle en [`tunnels.md`](../tunnels.md) y [ADR-0031](../adr/0031-hub-de-tuneles-ipsec-gre.md) (§5.4). | IPsec/GRE implementados; configuración VRP pendiente de validación en equipo real |
| Configuración del router | Horus **genera** la configuración VRP (SNMPv3 + vista, NetStream, NQA); **la pega el operador**. Horus nunca escribe en el router. | Implementado, pendiente de validación |

---

## 1. Equipos y versiones

| Familia | VRP | Uso típico en un ISP | Notas para Horus |
| --- | --- | --- | --- |
| **NE40E / NetEngine 8000** (X8A, X16A, M8, M14…) | **V8** (V800R0xx) | Router de borde / BNG | Chasis con MPU (control), LPU (línea) y SFU (conmutación): la salud se publica **por tarjeta**. NetStream distribuido por LPU (puede requerir muestreo por slot). IPsec requiere tarjeta de servicio en muchos modelos (§5.4). |
| **AR** (AR6000, AR600, AR2200/3200 heredados) | **V5** (V200R0xx / V300R0xx) | Router de sitio, CPE empresarial | Equipos de placa única o con SRU: la salud está en la placa principal. IPsec/IKEv2 nativos. |
| **CX / CloudEngine** | V8 | Agregación L2/L3 | Mismo modelo de MIB; fuera del alcance principal (no suelen ser el router principal del nodo). |

Horus lee la versión de `sysDescr` (`VRP (R) software, Version 8.180 (NE40E V800R011C10SPC100)` →
`V800R011C10SPC100`) y el modelo de la línea `HUAWEI NE40E-X8A` (o del producto entre paréntesis). No hay
versión mínima exigida: el campo `routeros_version` no aplica (`422` si se envía con `vendor=huawei`).

---

## 2. Arquitectura del adaptador

```
devices.router.vendor ──► snmpclient.ForVendor(vendor) ──► Adapter{ReadSystem, ReadResources, ReadHealth, Monitored}
                                                    │
                       núcleo estándar ◄────────────┘  Discover / ReadCounters (IF-MIB), readSystem (MIB-II),
                                                       ENTITY-MIB (entPhysicalTable)
devices.router.connection_mode ──► wireguard: túnel + API REST (netwatch, huella TLS, importación de prefijos)
                                   direct:    dirección de gestión + SNMP (NQA) + allow-list del colector
```

- `services/snmp/internal/adapters/snmpclient/standard.go`: núcleo común. `mikrotik.go`: MIKROTIK-MIB y
  HOST-RESOURCES-MIB (sin cambios). `huawei.go`: VRP. `ping.go`: DISMAN-PING-MIB / NQA-MIB. `vendor.go`:
  interfaz `Adapter` y selección.
- El sondeador (`services/snmp/internal/app`) toma el adaptador del objetivo; la etiqueta `vendor` de
  `horus_snmp_polls_total` ya distingue fabricantes.
- Sondas por enlace (`capacity_jobs.go`): MikroTik → netwatch por la API REST (devices); Huawei → NQA por
  SNMP (`Engine.ReadPings` + `NQASamples`).
- Alta: MikroTik → `POST /routers/{id}/provisioning-script` (wireguard). Huawei →
  `POST /routers/{id}/direct-onboarding` (devices). wireguard rechaza a los routers en modo directo
  (`422 ROUTER_VENDOR_NOT_SUPPORTED`) y no les reserva IP de túnel.

---

## 3. SNMP

### 3.1 MIB y OID

VRP implementa MIB-II, IF-MIB y ENTITY-MIB estándar; **HOST-RESOURCES-MIB no se usa** (no está
implementada de forma general en VRP: la CPU y la memoria están en la MIB privada).

| Dato | MIB / objeto | OID | Notas |
| --- | --- | --- | --- |
| Descripción, uptime | SNMPv2-MIB `sysDescr`, `sysUpTime` | 1.3.6.1.2.1.1.1.0 / .3.0 | Modelo y versión de VRP se extraen de `sysDescr` (CR/LF entre líneas). |
| Tarjetas | ENTITY-MIB `entPhysicalClass`, `entPhysicalName`, `entPhysicalSoftwareRev`, `entPhysicalSerialNum` | 1.3.6.1.2.1.47.1.1.1.1.{5,7,10,11} | `module(9)` = tarjeta, `chassis(3)` = chasis (serie). |
| Estado de reserva | HUAWEI-ENTITY-EXTENT-MIB `hwEntityStandbyStatus` | 1.3.6.1.4.1.2011.5.25.31.1.1.1.1.3 | `providingService(4)` = MPU activa; `hotStandby(2)`/`coldStandby(3)` = reserva. |
| CPU | `hwEntityCpuUsage` | …31.1.1.1.1.5 | Integer32 0..100 por entidad («uso global de la entidad, sin considerar el nº de CPU»). |
| Memoria | `hwEntityMemUsage`, `hwEntityMemSizeMega` (`hwEntityMemSize`) | …31.1.1.1.1.7 / .19 (.9) | % usado; tamaño en MiB (en bytes `.9`, Integer32: desborda > 2 GiB, por eso se prefiere `.19`). |
| Temperatura | `hwEntityTemperature` | …31.1.1.1.1.11 | °C por entidad. |
| Voltaje | `hwEntityVoltage` | …31.1.1.1.1.13 | mV. |
| Interfaces | IF-MIB `ifName`, `ifAlias`, `ifType`, `ifHighSpeed`, `ifAdminStatus`, `ifOperStatus` | 1.3.6.1.2.1.31.1.1.1.* / 2.2.1.* | Descubrimiento (walk). |
| Contadores | IF-MIB `ifHCIn/OutOctets`, `ifHCIn/OutUcastPkts`, `ifIn/OutErrors`, `ifIn/OutDiscards` | 1.3.6.1.2.1.31.1.1.1.{6,7,10,11}, 2.2.1.{13,14,19,20} | **GET** de los ifIndex sondeados (nunca walk de ifTable), igual que MikroTik. |

Todos los OID de HUAWEI-ENTITY-EXTENT-MIB se comprobaron contra el módulo MIB de Huawei
(`hwEntityExtentMIB`, 1.3.6.1.4.1.2011.5.25.31, LAST-UPDATED 202211240000Z).

### 3.2 Selección de entidades

Procedimiento documentado por Huawei («Querying the CPU Usage», «What Are the MIB OIDs of CPU Usage?»):
recorrer `entPhysicalClass` para obtener los índices de las tarjetas (clase 9) y pedir `hwEntityCpuUsage`
por esos índices; la CPU del sistema es la de la **MPU activa** (`hwEntityStandbyStatus`). Horus:

1. Tarjetas = entidades `module(9)` con CPU > 0 o memoria > 0 (puertos, fuentes y ventiladores devuelven 0).
   Si ninguna es `module`, cualquier entidad con CPU o memoria.
2. Tarjeta principal = la primera cuyo nombre la delata (`MPU`, `SRU`, `MCU`, `RPU`, `SRP`, `main board`) y
   que **no** esté de reserva; si no, la de más memoria que no esté de reserva.
3. `CPULoads` = principal primero y luego una carga por tarjeta: `cpu_percent` es la media y
   `cpu_max_core_percent` la tarjeta más cargada (en Huawei «núcleo» = tarjeta).
4. Memoria = la de la tarjeta principal (`MemSizeMega × MemUsage / 100`).
5. Temperatura y voltaje = los de la tarjeta principal; un sensor por entidad con temperatura (y voltaje).
6. Firmware = versión de `sysDescr` (si no, `entPhysicalSoftwareRev`); serie = la del chasis.

### 3.3 Interfaces sondeadas

| Se sondean | No se sondean |
| --- | --- |
| `ethernetCsmacd(6)` (GigabitEthernet, XGigabitEthernet, 25GE, 100GE…), `l2vlan(135)` y `l3ipvlan(136)` (subinterfaces y Vlanif), `ieee8023adLag(161)` (Eth-Trunk; también `propVirtual(53)` llamado `Eth-Trunk…` en VRP antiguos), tipos Ethernet obsoletos 62/69/117 | `NULL`, `InLoopBack`, `LoopBack`, `MEth` (gestión), `Virtual-*` (Virtual-Template/Ethernet/Access), `Tunnel`, `Dialer`, `Cellular`, `Aux`, `Console`, `NVE`, `Vbdif` y cualquier nombre con `:` (interfaces dinámicas por usuario). |

La elección automática del enlace de subida admite además `l3ipvlan(136)` (subinterfaces VRP); RouterOS no
publica ese tipo, así que no cambia nada para MikroTik.

### 3.4 SNMPv3

Credencial emitida por Horus para Huawei: usuario `horus-<8 hex del router>`, **authPriv SHA-256 + AES-128**
(`authentication-mode sha2-256`, `privacy-mode aes128`; RFC 7860 / RFC 3826), cifrada en reposo con la KEK
de devices y mostrada **una sola vez**. AES-256 no se usa: su derivación de clave difiere entre fabricantes
(Blumenthal vs. Reeder) y gosnmp necesita saber cuál. En VRP V5 antiguos sin `sha2-256` hay que poner la
credencial a mano (`PUT /routers/{id}/credentials/snmp` admite `sha1`).

---

## 4. Pérdida, latencia y jitter por enlace (NQA)

### 4.1 Modelo

Igual que netwatch en MikroTik: **el operador** configura pruebas ICMP en el router y Horus **solo lee** los
resultados. Convención (la genera `GET /routers/{id}/netwatch-snippet`, que con un Huawei devuelve
`language: vrp`):

| NQA | Valor | Significado en Horus |
| --- | --- | --- |
| admin-name | `horus` | Propietario (`pingCtlOwnerIndex` / `nqaAdminCtrlOwnerIndex`). Horus solo lee las de `horus`. |
| test-name | `gw-<interfaz>` (`/` y `:` → `_`; > 32 caracteres → `gw-h<8 hex SHA-1>`) | Puerta de enlace del proveedor por esa interfaz (`ref = gw`, emparejada con la interfaz descubierta por SNMP). |
| test-name | `intl-<ip>` | Referencia internacional (`ref = intl`). |
| test-type | `icmp` | Solo ICMP. |
| probe-count / interval / timeout / frequency | 10 / 1 s / 2 s / 30 s | `frequency` > (probe-count − 1) × interval + timeout, como exige VRP. |

### 4.2 Lectura (solo GET/walk)

1. **DISMAN-PING-MIB** (RFC 4560, documentada en las referencias de MIB de Huawei; «guarda solo el resultado
   de la última prueba»): walk por propietario `horus` (índice `5.104.111.114.117.115`) de
   `pingResultsTable` (1.3.6.1.2.1.80.1.3.1): `SentProbes(8)`, `ProbeResponses(7)`, `Min/Max/AverageRtt(4/5/6)`
   en ms, `RttSumOfSquares(9)`, `IpTargetAddress(3)`; destino de respaldo `pingCtlTargetAddress`.
2. Si DISMAN no publica ninguna prueba: **NQA-MIB** (1.3.6.1.4.1.2011.5.25.111). Lista de pruebas ICMP por
   `nqaAdminCtrlType(4) = icmp(1)` y GET de `nqaResultsTable` (…111.4.1.1) en índice de resultados 0 y salto
   0 (restricción de acceso que indica la MIB): `SentProbes(22)`, `ProbeResponses(21)`, `RttAvg(26)`,
   `CompletionTimeMin/Max(11/12)`, `LostPacketRatio(27)`, `Address(20)`.

### 4.3 Correspondencia con el modelo de sonda

| `ProbeSample` | DISMAN-PING-MIB | NQA-MIB |
| --- | --- | --- |
| `sent` / `received` | `pingResultsSentProbes` / `ProbeResponses` | `nqaResultsSentProbes` / `ProbeResponses` |
| `loss_pct` | (enviadas − respuestas) / enviadas | `nqaResultsLostPacketRatio` |
| `rtt_avg/min/max_ms` | `AverageRtt` / `MinRtt` / `MaxRtt` | `RttAvg` / `CompletionTimeMin` / `CompletionTimeMax` |
| `jitter_ms` | desviación típica = √(ΣRTT²/n − RTT̄²) | — (sin dato) |
| `up` | respuestas > 0 | respuestas > 0 |
| `source` | `router_nqa` | `router_nqa` |

### 4.4 Configuración NQA generada (ejemplo)

Solo comandos (sin comentarios: en la CLI de VRP cada línea pegada se ejecuta). **Pendiente de validación
en equipo real.**

```
system-view
nqa test-instance horus gw-GigabitEthernet1_0_0
 test-type icmp
 destination-address ipv4 {GATEWAY_1}
 source-interface GigabitEthernet1/0/0
 probe-count 10
 interval seconds 1
 timeout 2
 frequency 30
 start now
 quit
nqa test-instance horus intl-1.1.1.1
 test-type icmp
 destination-address ipv4 1.1.1.1
 probe-count 10
 interval seconds 1
 timeout 2
 frequency 30
 start now
 quit
return
```

Deshacer: `undo nqa test-instance horus <test-name>`. La vista SNMP del alta (§6.1) ya incluye
DISMAN-PING-MIB (1.3.6.1.2.1.80) y NQA-MIB (1.3.6.1.4.1.2011.5.25.111).

---

## 5. Conexión con Horus sin WireGuard (IPsec, GRE o directo)

### 5.1 Por qué no hay túnel

El camino MikroTik lleva gestión (SNMP, REST) y flujos por un túnel WireGuard iniciado por el router, y la
IP de túnel es la identidad del exportador ([ADR-0022](../adr/0022-mikrotik-routeros-v7-primer-fabricante.md)).
**VRP no implementa WireGuard.** El PO decidió (§5.4) **IPsec IKEv2** como camino estándar y **GRE** como
alternativa; el **modo directo** sigue disponible. Los tres comparten el modelo: la IP de túnel (IPsec, GRE)
o la dirección de gestión verificada (directo) es la identidad del exportador.

### 5.2 Modo directo (implementado)

- `connection_mode = direct` (Huawei siempre). Sin peer WireGuard ni IP del pool de túneles.
- **Dirección de gestión** (`management_address`): la IP de origen del router **tal y como la ve Horus**
  (tras cualquier NAT del camino). Es el destino del sondeo SNMP y la identidad del exportador.
- **Camino**: lo asegura el operador — VRF de gestión, VPN de sitio a sitio existente, o un relé
  MikroTik/Linux en el sitio. La consola lo advierte en el alta y en el asistente. **Nunca SNMP por Internet.**
- **Allow-list**: el colector solo acepta datagramas cuya IP de origen sea una dirección de gestión
  **verificada** (inventario de flujos); el resto se descarta como `unknown_exporter`. En el router, la ACL
  básica 2990 limita SNMP a la IP del sondeador de Horus.
- Los flujos hacia/desde la dirección de gestión **no** se filtran como «tráfico del túnel» (puede ser la IP
  del NAT de los clientes).

Variables de devices: `HORUS_DEVICES_DIRECT_COLLECTOR_ADDRESS` (IP del colector por ese camino),
`HORUS_DEVICES_DIRECT_COLLECTOR_PORT` (2055), `HORUS_DEVICES_DIRECT_POLLER_ADDRESS` (IP de origen del sondeo,
para la ACL), `HORUS_DEVICES_DIRECT_NTP_SERVER`. Vacías = marcadores en la configuración. Para recibir sFlow,
añadir `:6343` a `HORUS_COLLECTOR_LISTEN` y publicar el puerto (el decodificador distingue sFlow por su
cabecera, en cualquier puerto).

### 5.3 Prueba de control (snmp_verified)

Sin túnel, una IP de origen UDP no prueba nada: un ISP podría declarar la IP de otro y recibir sus flujos.
Por eso:

1. `pending_configuration`: el router tiene dirección de gestión, pero el colector **no** la acepta.
2. El sondeador obtiene respuesta **SNMPv3 authPriv con la credencial emitida para ESE router** en esa
   dirección → devices pasa el router a **`snmp_verified`** (`DirectVerifier`, evento `router.updated`).
3. Solo entonces el inventario de flujos acepta esa IP (`flowinv`: `Direct = true`), y con el primer flujo
   el router pasa a `exporting`.
4. La dirección verificada es **única en toda la plataforma** (índice parcial
   `ux_router__management_address`): otro router puede declararla, pero no verificarla
   (`MANAGEMENT_ADDRESS_IN_USE`). Una dirección directa nunca suplanta una IP de túnel.
5. Cambiar la dirección devuelve el router a `pending_configuration`.

Riesgo residual: la suplantación de IP de origen UDP por un tercero que esté en el camino. Por eso el camino
tiene que ser privado (VRF/VPN/relé); con WireGuard o IPsec la identidad sería criptográfica (§5.4).

### 5.4 Decisión: túnel para Huawei (IPsec IKEv2 + GRE)

| Opción | Cómo | A favor | En contra |
| --- | --- | --- | --- |
| **A. IPsec IKEv2 sitio a sitio a un hub strongSwan de Horus** (route-based) | Interfaz `Tunnel` con `tunnel-protocol ipsec` + perfil IKEv2 en VRP; hub strongSwan con VTI/XFRM y una /32 por router (como la IP de túnel de WireGuard). | Nativo en VRP (AR sin hardware extra); identidad criptográfica por router (certificado o PSK); mantiene el modelo «IP de túnel = identidad del exportador» sin cambiar colector ni inventario; estándar (RFC 7296/4301). | Más complejo de operar que WireGuard (PKI/PSK, NAT-T, MTU); en NE40E/NetEngine 8000 IPsec suele requerir **tarjeta de servicio**; un segundo hub que operar (strongSwan) junto a wg-agent. |
| **B. GRE sobre IPsec** | GRE entre router y hub, protegido por IPsec en modo transporte. | Admite multicast y protocolos de enrutamiento; útil donde solo hay IPsec basado en políticas. | Más cabeceras (MTU), dos capas que depurar; Horus no necesita multicast ni enrutamiento dinámico. |
| **C. Relé en el sitio (MikroTik/Linux con WireGuard)** | El Huawei exporta NetStream y responde SNMP hacia un equipo del sitio que sí tiene túnel WireGuard con Horus (SNAT/relé UDP). | Reutiliza el hub WireGuard y el modelo actual; sirve para cualquier fabricante; no exige tarjetas de servicio. | Un equipo más por sitio (punto de fallo); la IP de origen pasa a ser la del relé (hay que mapear relé → router, o un relé por router); NAT de SNMP. |
| **D. VRF de gestión / directo con ACL** (implementado) | Sin túnel: el camino privado lo pone el operador; allow-list por IP de origen + verificación SNMPv3. | Cero software nuevo; vale cuando el ISP ya tiene red de gestión o VPN con el datacenter de Horus. | Identidad por IP (suplantable en el camino); depende de la red del operador; no sirve si Horus está en Internet sin VPN. |

**Decisión del PO (18/10/2026, [ADR-0031](../adr/0031-hub-de-tuneles-ipsec-gre.md)): A + GRE.**

- **IPsec IKEv2 route-based** (`connection_mode = ipsec`) es el camino estándar y la sugerencia del asistente
  para Huawei: interfaz `Tunnel` con `tunnel-protocol ipsec` y perfil IKEv2 en VRP; hub strongSwan
  (charon por vici) con una interfaz XFRM por router (`if_id`), PSK propia por router (cifrada, mostrada
  una vez) y la IP de túnel en una `LoopBack` como origen de NetStream y SNMP. Suite IKE conservadora
  (AES-256, SHA2-256, grupo 14) que casa con la propuesta de respaldo del hub.
- **GRE** (`connection_mode = gre`) para equipos sin IPsec (NE40E sin tarjeta de servicio): clave por
  router, origen fijado a la IP pública registrada. **Sin cifrado**: nivel `unencrypted_private` o, por
  Internet, `unencrypted_internet` con confirmación, re-autenticación, auditoría e insignia persistente.
- **Directo** (D) se conserva; **relé** (C) y **GRE sobre IPsec** (B) quedan descartados.
- PKI (certificados por router) queda como evolución; esta entrega usa PSK.
- La configuración VRP generada (`POST /routers/{id}/tunnel-config`) sigue la documentación oficial de
  Huawei y está **pendiente de validación en equipo real o eNSP** (§9). En VRP V5 (AR) la interfaz se
  llama `Tunnel0/0/<n>` y algunos comandos cambian.

Operación, puertos y diagnóstico: [`tunnels.md`](../tunnels.md).

---

## 6. Configuración VRP generada (alta directa)

`POST /routers/{id}/direct-onboarding` (permiso `devices.credentials.write`, re-autenticación, auditado como
`devices.direct_onboarding.created`) devuelve `config` (solo comandos), `notes`, `placeholders` y `warnings`.
Sintaxis de **VRP V8** según la documentación oficial; **pendiente de validación en equipo real**. En VRP V5
(AR) algunos comandos cambian (p. ej. `snmp-agent usm-user v3 <u> <grupo> authentication-mode sha <clave>`
en una sola línea en versiones antiguas).

### 6.1 SNMPv3 de solo lectura con vista mínima

```
system-view
acl number 2990
 description horus-snmp-ro
 rule 5 permit source <IP del sondeador de Horus> 0
 rule 10 deny
 quit
snmp-agent
snmp-agent sys-info version v3
snmp-agent mib-view included horus-ro 1.3.6.1.2.1.1
snmp-agent mib-view included horus-ro 1.3.6.1.2.1.2
snmp-agent mib-view included horus-ro 1.3.6.1.2.1.31
snmp-agent mib-view included horus-ro 1.3.6.1.2.1.47
snmp-agent mib-view included horus-ro 1.3.6.1.2.1.80
snmp-agent mib-view included horus-ro 1.3.6.1.4.1.2011.5.25.31
snmp-agent mib-view included horus-ro 1.3.6.1.4.1.2011.5.25.111
snmp-agent group v3 horus-ro privacy read-view horus-ro acl 2990
snmp-agent usm-user v3 horus-xxxxxxxx group horus-ro
snmp-agent usm-user v3 horus-xxxxxxxx authentication-mode sha2-256
<contraseña de autenticación>
<contraseña de autenticación>
snmp-agent usm-user v3 horus-xxxxxxxx privacy-mode aes128
<contraseña de cifrado>
<contraseña de cifrado>
snmp-agent usm-user v3 horus-xxxxxxxx acl 2990
```

En VRP V8, tras `authentication-mode` y `privacy-mode` el equipo **pide la contraseña dos veces**: las dos
líneas siguientes son esas respuestas (por eso la configuración no lleva comentarios). Grupo con nivel
`privacy` (authPriv), vista solo de lectura (sin `write-view` ni `notify-view`).

### 6.2 NetStream (v9) hacia Horus

```
ntp-service unicast-server <NTP>
ip netstream export version 9
ip netstream export template timeout-rate 1
ip netstream export template option sampler
ip netstream export source <dirección de gestión>
ip netstream export host <colector de Horus> 2055
ip netstream timeout active 1
ip netstream timeout inactive 15
ip netstream sampler fix-packets 1000 inbound
ip netstream sampler fix-packets 1000 outbound
interface {INTERFAZ_NETSTREAM}
 ip netstream inbound
 ip netstream outbound
 quit
return
```

- **v9** por defecto (formato de flujos originales soportado por toda la gama; por defecto VRP exporta v5,
  que no lleva IPv6). IPFIX (`ip netstream export version ipfix`) también funciona con el decodificador.
- `export template option sampler`: plantilla de opciones con `samplerRandomInterval` (IE 50) → el colector
  conoce la tasa de muestreo y el ingester escala bytes y paquetes.
- Tiempos activo 1 min / inactivo 15 s, como en MikroTik (agregados de 1 min).
- Origen = dirección de gestión (debe coincidir con la que ve Horus).
- Equipos distribuidos (NE40E/NetEngine 8000): puede hacer falta el muestreo por slot
  (`slot <n>` → `ip netstream sampler to slot self`); está en `notes`.

### 6.3 NQA

Ver §4.4 (se genera aparte, con los enlaces de subida detectados por SNMP).

---

## 7. Flujos

### 7.1 NetStream v9: plantilla

Campos de la plantilla de flujos originales IPv4 documentada por Huawei para NetStream v9 y los que usa
Horus (el decodificador lee la plantilla; no asume orden ni presencia):

| IE (v9 / IANA) | Nombre | Uso |
| --- | --- | --- |
| 1, 2 | IN_BYTES, IN_PKTS | bytes/paquetes (×tasa de muestreo en el ingester) |
| 4, 5, 6 | PROTOCOL, TOS, TCP_FLAGS | protocolo, ToS, flags |
| 7, 11 | L4_SRC/DST_PORT | puertos |
| 8, 12, 9, 13 | IPV4_SRC/DST_ADDR, SRC/DST_MASK | direcciones (máscaras ignoradas) |
| 27, 28 | IPV6_SRC/DST_ADDR | IPv6 (plantilla propia) |
| 10, 14 | INPUT/OUTPUT_SNMP | ifIndex → interfaz del inventario |
| 15, 16, 17 | IPV4_NEXT_HOP, SRC_AS, DST_AS | pista (el ASN canónico sale del catálogo) |
| 21, 22 | LAST/FIRST_SWITCHED | relativos a sysUptime de la cabecera |
| 61 | DIRECTION | ingress/egress |
| 48 | FLOW_SAMPLER_ID | referencia al sampler (sin tasa) |
| 34, 50 | SAMPLING_INTERVAL, FLOW_SAMPLER_RANDOM_INTERVAL | tasa de muestreo (datos u opciones) |

Plantilla de opciones (`export template option sampler`): ámbito System + 48/49/50 → la tasa del exportador.
Campos propietarios desconocidos se saltan por su longitud.

### 7.2 IPFIX

Igual que v9 con tiempos absolutos (152/153) y PSAMP: `samplingSize`/`samplingPopulation` (IE 309/310,
RFC 5477) → tasa = población/tamaño, además de 305/306. Elementos de empresa (bit de empresa, PEN 2011 de
Huawei), de longitud fija o variable, se ignoran.

### 7.3 sFlow v5 (nuevo)

`services/collector/internal/decode/sflow.go`, según la especificación sFlow v5 (sflow.org):

- Cabecera: versión 5, dirección de agente (IPv4/IPv6), sub-agente (= dominio de observación), secuencia
  (saltos medidos por datagrama, como v9), uptime.
- `flow_sample` (1) y `flow_sample_expanded` (3): tasa de muestreo, ifIndex de entrada/salida (los formatos
  especiales —descartado, varias interfaces— quedan en 0).
- Registros: `sampled_header` (1) con cabecera Ethernet (802.1Q/802.1ad, MPLS hasta 8 etiquetas), IPv4 o IPv6
  cruda → IPs, protocolo, puertos, flags TCP, ICMP tipo/código, VLAN (fragmentos no iniciales sin puertos;
  cabeceras de extensión IPv6 básicas); `sampled_ipv4/ipv6` (3/4) si no hay cabecera; `extended_switch`
  (1001) → VLAN; `extended_router` (1002) → siguiente salto.
- Un flujo por muestra: `packets = 1`, `bytes = longitud de la trama`, `sampling_rate = tasa`,
  `flow_source = sflow_v5` (ya existía en el `Enum8` de ClickHouse). El ingester multiplica, igual que con
  NetFlow muestreado.
- `counter_sample` (2/4): no generan flujos (se cuentan como registros de opciones; las interfaces ya se
  leen por SNMP). Formatos de empresa distinta de 0: se ignoran.
- Límites: 1 024 muestras por datagrama, 64 registros por muestra. Test de fuzzing (`FuzzDecodeSFlow`).

---

## 8. Limitaciones conocidas

- **Sin validación en equipo real** (§9). Fixtures sintéticos: `services/snmp/internal/adapters/snmpclient/testdata/huawei-*-synthetic.snmpwalk`,
  `services/collector/internal/decode/testdata/{huawei-netstream-v9,huawei-ipfix,sflow-v5}-synthetic.hex`.
- Sin API REST: no hay importación de prefijos desde el router, ni huella TLS, ni lectura de pools; los
  prefijos se declaran a mano (o en modo descubrimiento).
- NetStream es **muestreado** en la mayoría de equipos: los recuentos son estimados y detection baja la
  confianza (`min_sampling_rate`), igual que con cualquier exportador muestreado.
- La CPU de Huawei es «una carga por tarjeta», no por núcleo.
- Si el equipo no expone las pruebas NQA en DISMAN-PING-MIB ni en NQA-MIB, la sonda queda sin datos
  (`probes_error`) y solo quedan las sondas de respaldo desde Horus.
- La configuración generada usa comandos IPv4 (`acl number`, `ip netstream`); con dirección de gestión IPv6
  hay que adaptarla (aviso en `warnings`).
- Credencial SHA-256: VRP V5 antiguos sin `sha2-256` necesitan la credencial puesta a mano (SHA-1).
- Contraseñas de 24 caracteres alfanuméricos: si la política de complejidad del equipo exige símbolos,
  regenerar o poner la credencial a mano.

---

## 9. Lista de validación con un equipo real (o eNSP)

Para cada ítem: capturar evidencia y, si difiere, ajustar el adaptador y **sustituir el fixture sintético
por la captura real** (el diseño solo necesita fixtures nuevos).

1. **SNMP**: `snmpwalk -v3 -l authPriv -a SHA-256 -x AES -On` de los OID del §3.1 con la credencial de
   Horus → grabar como `testdata/huawei-<modelo>-<vrp>.snmpwalk` y añadirlo a `huawei_test.go`. Comprobar:
   tarjetas `module(9)`, `hwEntityStandbyStatus` de las MPU, `hwEntityMemSizeMega`, unidades de temperatura,
   nombres de interfaces y `ifType` de subinterfaces y Eth-Trunk.
2. **Configuración del alta**: pegar §6.1 tal cual en V8 y en V5; confirmar prompts de contraseña,
   `sha2-256`, la ACL en el grupo y en el usuario, y que la vista basta (sin `noSuchObject` en lo que lee
   Horus). Confirmar que `snmp-agent sys-info version v3` no desactiva otras versiones en uso.
3. **NQA**: crear las pruebas del §4.4; comprobar si aparecen en `pingResultsTable` con propietario `horus`
   (o solo en NQA-MIB), si los resultados son de la última prueba o acumulados, unidades de RTT y
   `nqaResultsLostPacketRatio`; caracteres admitidos en test-name (`.`, `-`, `_`).
4. **NetStream**: capturar (`tcpdump -w`) 2 min de NetStream v9 e IPFIX con y sin `template option sampler`;
   añadir la captura como fixture con sus totales esperados; verificar tasa de muestreo, ifIndex y tiempos.
   En NE40E/NetEngine 8000, confirmar si hace falta el muestreo por slot.
5. **sFlow** (si el equipo lo soporta, p. ej. CloudEngine): capturar y añadir como fixture.
6. **Verificación**: el router pasa a `snmp_verified` en el primer ciclo con SNMP correcto y a `exporting`
   con el primer flujo; un datagrama desde otra IP se descarta (`unknown_exporter`).
7. **Comentarios**: confirmar que pegar la configuración sin comentarios funciona en la CLI (y si `#` es
   aceptable para documentarla en futuras versiones).
8. **Túnel** (para la decisión §5.4): IKEv2 route-based contra strongSwan en el modelo objetivo.

---

## 10. Fuentes

Documentación oficial de Huawei (consultada mediante el índice del buscador; acceso directo bloqueado
desde el entorno):

- CPU por entidad y MPU activa: «Querying the CPU Usage», CloudEngine 16800 V200R020C00 MIB Reference —
  <https://support.huawei.com/enterprise/en/doc/EDOC1100175575/818b7da7/querying-the-cpu-usage> ·
  «What Are the MIB OIDs of CPU Usage?», NetEngine AR600/AR6100/AR6200/AR6300 V300R019 —
  <https://support.huawei.com/enterprise/en/doc/EDOC1100112347/9ea82e56/what-are-the-mib-oids-of-cpu-usage> ·
  «MIB Example», S1700/S2720/S5700/S6700 V200R019C10 —
  <https://support.huawei.com/enterprise/en/doc/EDOC1100127036/e704d4b7/mib-example> ·
  «Hardware Management MIB», CloudEngine 8800/6800 V300R023C10 —
  <https://support.huawei.com/enterprise/en/doc/EDOC1100381909/7c434ebd/-hardware-management-mib>
- DISMAN-PING-MIB en VRP: <https://support.huawei.com/enterprise/en/doc/EDOC1000178181/f3df1ea/disman-ping-mib> ·
  <https://support.huawei.com/enterprise/en/doc/EDOC1100127036/f3df1ea/disman-ping-mib>
- NQA-MIB: S7700/S9700 V200R013C00 — <https://support.huawei.com/enterprise/en/doc/EDOC1100065754/ab9bf6f7/nqa-mib> ·
  AR500…AR2500 V200R009 (`nqaAdminParaTable`) — <https://support.huawei.com/enterprise/en/doc/EDOC1000177813/4fe55308/nqaadminparatable> ·
  S310 V600R022C10 — <https://support.huawei.com/enterprise/en/doc/EDOC1100306166/1ef85899/nqa-mib>
- Configuración NQA: NetEngine 8000 M14/M8/M4 V800R023C10SPC500 —
  <https://support.huawei.com/enterprise/en/doc/EDOC1100367115/16c01af2/nqa-configuration> · comandos NQA,
  CloudEngine V200R020C10 — <https://support.huawei.com/enterprise/en/doc/EDOC1100198444/e6f783f1/nqa-configuration-commands> ·
  «Configuring an NQA Test Instance», CX320 — <https://support.huawei.com/enterprise/en/doc/EDOC1000128406/5eea5a50/configuring-an-nqa-test-instance> ·
  «Configuring an ICMP Jitter Test» (VRP) — <https://info.support.huawei.com/hedex/api/pages/EDOC1100277644/AEM10221/03/resources/vrp/dc_vrp_nqa_cfg_0008.html>
- NetStream: `ip netstream export version` — <https://info.support.huawei.com/hedex/api/pages/EDOC1100277644/AEM10221/04/resources/command/yunshan/IP_NETSTREAM_EXPORT_VERSION.html> ·
  `export version` (agregados, IPFIX) — <https://info.support.huawei.com/hedex/api/pages/EDOC1100277644/AEM10221/04/resources/command/yunshan/EXPORT_VERSION.html> ·
  comandos NetStream, NetEngine AR600…AR6300 V300R021 — <https://support.huawei.com/enterprise/en/doc/EDOC1100203224/3a962da5/netstream-configuration-commands> ·
  ejemplos, CloudEngine 16800 V300R024C10 — <https://support.huawei.cn/enterprise/en/doc/EDOC1100463943/aa88faeb/netstream-configuration-examples> ·
  «Typical NetStream Configuration», S12700 — <https://support.huawei.com/enterprise/en/doc/EDOC1100212350/fec6590c/typical-netstream-configuration>
- SNMPv3: `snmp-agent usm-user` — <https://info.support.huawei.com/hedex/api/pages/EDOC1100278054/AEL11217/08/resources/dc/snmp-agent_usm-user.html> ·
  <https://support.huawei.com/enterprise/en/doc/EDOC1100096312/72d3e0f1/snmp-agent-usm-user> ·
  comandos SNMP, NetEngine AR1000V V300R021 — <https://support.huawei.cn/enterprise/en/doc/EDOC1100212579/dab469bd/snmp-configuration-commands> ·
  «SNMP Device Management Security» (guía de bastionado) — <https://support.huawei.com/enterprise/en/doc/EDOC1100038362/a9bfe738/snmp-device-management-security>

Módulos MIB de Huawei (texto de los ficheros que Huawei distribuye con cada versión; la descarga oficial
exige cuenta de soporte, se usó el espejo de LibreNMS): HUAWEI-ENTITY-EXTENT-MIB (LAST-UPDATED
202211240000Z) — <https://github.com/librenms/librenms/blob/master/mibs/huawei/HUAWEI-ENTITY-EXTENT-MIB> ·
NQA-MIB (LAST-UPDATED 202302061059Z) — <https://github.com/librenms/librenms/blob/master/mibs/huawei/NQA-MIB>.

RFC y especificaciones: RFC 2863 (IF-MIB), RFC 6933 (ENTITY-MIB v4), RFC 2790 (HOST-RESOURCES-MIB),
RFC 4560 (DISMAN-PING-MIB), RFC 2578 §7.7 (índices de tabla), RFC 3414 (USM), RFC 3826 (AES en USM),
RFC 7860 (HMAC-SHA-2 en USM), RFC 3954 (NetFlow v9), RFC 7011/7012 (IPFIX), RFC 5477 (PSAMP),
sFlow v5 — <https://sflow.org/sflow_version_5.txt>, RFC 7296 (IKEv2), RFC 4301 (IPsec).

## 11. Switches de la serie S

> Estado: **implementado contra documentación, fixtures sintéticos** (S5735, VRP V200R021). Modelo de datos,
> reglas de solo monitoreo y API comunes con MikroTik en [`mikrotik.md` §13](mikrotik.md#13-switches-de-monitoreo-crs-con-routeros-7-y-swos).

### 11.1 Alta (modo directo)

`device_type=switch`, `switch_platform=vrp` (fija `vendor=huawei`). `POST /routers/{id}/switch-onboarding`
genera una credencial SNMPv3 SHA-256 + AES-128 de solo lectura y la configuración (PENDIENTE DE VALIDACIÓN EN
EQUIPO REAL):

```
system-view
acl number 2991
 description horus-snmp-ro
 rule 5 permit source <HORUS_POLLER_IP> 0
 rule 10 deny
 quit
snmp-agent
snmp-agent sys-info version v3
snmp-agent mib-view included horus-ro 1.3.6.1.2.1.1      (system; ídem .2, .31, .47, LLDP 1.0.8802.1.1.2 y 1.3.6.1.4.1.2011.5.25.31)
snmp-agent group v3 horus-ro privacy read-view horus-ro acl 2991
snmp-agent usm-user v3 horus-<id> group horus-ro
snmp-agent usm-user v3 horus-<id> authentication-mode sha2-256   (VRP pide la contraseña dos veces)
snmp-agent usm-user v3 horus-<id> privacy-mode aes128
snmp-agent usm-user v3 horus-<id> acl 2991
return
```

La ACL usa el número 2991 (el alta directa de routers usa 2990) para no chocar si conviven. La vista añade
LLDP-MIB a la de los routers. Versiones antiguas sin `sha2-256`: usar `sha` (nota en la configuración).

### 11.2 CPU, memoria y temperatura

Mismo adaptador que los routers VRP (§3): HUAWEI-ENTITY-EXTENT-MIB `hwEntityStateTable` por
`entPhysicalIndex`, tarjetas `entPhysicalClass = module(9)`. En la serie S fija la única tarjeta con CPU es la
MPU (`MPU Board 0`, índice típico 67108873 en la «MIB Example» de S1700/S2720/S5700/S6700 V200R019C10); en un
*stack* la MPU de reserva se descarta por `hwEntityStandbyStatus`. El modelo sale de la primera línea de
`sysDescr` (`S5735-L48T4X-A1`).

### 11.3 Puertos, LLDP y flaps

IF-MIB con los nombres de VRP (`GigabitEthernet0/0/1`, `XGigabitEthernet0/0/1`, `Eth-Trunk1`; nunca `MEth`,
`Vlanif`, `NULL`, `InLoopBack`). LLDP-MIB estándar (`lldp enable` en la vista de sistema si no está activo); el
puerto local se traduce por `lldpLocPortId` (subtipo interfaceName) o, si no casa, por `lldpLocPortNum = ifIndex`.
Elección de puertos, uplinks, flaps y alertas como en MikroTik (`mikrotik.md` §13.3).

### 11.4 sFlow (opcional, fuente secundaria)

Con `sflow=true` (o `PATCH sflow_enabled`) la alta añade (guías de configuración sFlow de S2720/S5700/S6700
V200R019C10 y S1720/S2700/S5700/S6720 V200R011C10–V200R012):

```
system-view
sflow agent ip <dirección de gestión>
sflow collector 1 ip <HORUS_COLLECTOR_IP> udp-port <puerto> description horus
interface <PUERTO_SFLOW>
 sflow flow-sampling collector 1
 sflow flow-sampling rate 4096
 sflow flow-sampling inbound
 quit
return
```

- **Solo muestreo de entrada** en cada puerto: cada paquete se muestrea una vez, en el puerto por el que entra al
  switch (sin doble conteo dentro del switch). Sin *counter sampling*: los contadores los lee Horus por SNMP.
- Huawei recomienda **no más de 8 puertos** con muestreo; tasa entre 256 y 65535 (máximo del chip ENP), 4096 por
  defecto. En V200R011 y anteriores el comando es `sflow sampling …` en lugar de `sflow flow-sampling …`.
- El agente es la dirección de gestión verificada: el colector solo acepta sFlow de esa IP y lo marca como
  **exportador secundario** (nunca suma a clientes ni nodos; [`traffic-model.md` §13](../traffic-model.md#13-switches-sflow-como-fuente-secundaria)).
  El decodificador sFlow v5 es el de §7.3; el colector lo reconoce en cualquiera de sus puertos (6343 si se
  escucha, `HORUS_COLLECTOR_LISTEN`).

### 11.5 Lista de validación con un equipo real

1. Pegar la configuración de §11.1 en un S5735 (o eNSP con S5700): `display snmp-agent usm-user` y
   `snmpwalk -v3 -l authPriv -a SHA-256 -x AES` desde el sondeador.
2. Comparar `hwEntityStateTable` y `entPhysicalTable` con el fixture sintético; sustituirlo por la captura real.
3. `display lldp neighbor brief` frente a `lldpRemTable`; comprobar la traducción del puerto local.
4. sFlow: `display sflow configuration`, recibir muestras en el colector (`horus_collector_datagrams_total{version="sflow5"}`),
   verificar que solo crece `flows.switch_samples` y no `flows.flows_raw`.

### 11.6 Fuentes

- «Configuring sFlow» y «Configuring an sFlow Sampling Mode», S2720/S5700/S6700 V200R019C10 —
  <https://support.huawei.com/enterprise/en/doc/EDOC1100127059/a98f8c1a/configuring-sflow> ·
  <https://support.huawei.com/enterprise/en/doc/EDOC1100127059/4781ed36/configuring-an-sflow-sampling-mode>
- «Example for Configuring sFlow», S1720/S2700/S5700/S6720 V200R011C10 —
  <https://support.huawei.com/enterprise/en/doc/EDOC1000178174/d73ef0a7/example-for-configuring-sflow> ·
  V200R012 — <https://support.huawei.com/enterprise/en/doc/EDOC1100038345/4781ed36/configuring-an-sflow-sampling-mode>
- Licencias y límites de sFlow (S1720GW/GWR/X con licencia) —
  <https://support.huawei.com/enterprise/en/doc/EDOC1000141939/735d1ff0/licensing-requirements-and-limitations-for-sflow>
- «MIB Example» (HUAWEI-ENTITY-EXTENT-MIB en la serie S), S1700/S2720/S5700/S6700 V200R019C10 —
  <https://support.huawei.com/enterprise/en/doc/EDOC1100127036/e704d4b7/mib-example>
- `snmp-agent usm-user` — <https://support.huawei.com/enterprise/en/doc/EDOC1100096312/72d3e0f1/snmp-agent-usm-user>
- Acceso: igual que §10, support.huawei.com se consultó a través del índice del buscador (la página es una SPA
  que no se puede leer sin navegador desde el entorno de trabajo).
