# Túneles de routers: WireGuard, IPsec IKEv2 y GRE

Decisión: [ADR-0031](adr/0031-hub-de-tuneles-ipsec-gre.md). Este documento explica cómo llegan la
gestión (SNMP, API REST) y los flujos de cada router a Horus, qué transporte elegir y cómo operarlo.

> Horus **nunca escribe en los routers**: genera la configuración y la pega el operador. Las
> credenciales (PSK, SNMPv3) son de solo escritura, se guardan cifradas y la PSK se muestra una sola vez.

---

## 1. Resumen

| Transporte (`connection_mode`) | Cifrado | Fabricantes | Hub | Identidad del router |
| --- | --- | --- | --- | --- |
| `wireguard` | sí (Noise) | MikroTik | `wg0` (wg-agent) | clave pública + IP de túnel |
| `ipsec` (IKEv2, route-based) | sí (ESP) | MikroTik, Huawei VRP | `hxfrm<n>` (tunnel-agent + charon) | PSK propia + identidad IKE + IP de túnel |
| `gre` | **no** | MikroTik, Huawei VRP | `hgre<n>` (tunnel-agent) | IP pública fija + clave GRE + IP de túnel |
| `direct` | depende del camino | Huawei VRP, switches | — | dirección de gestión verificada por SNMPv3 |
| `via_router` (solo switches) | el del túnel del router | switches MikroTik y Huawei | túnel del router del sitio | dirección Horus verificada por SNMP (§10) |

- **Todos los transportes con túnel reciben una IP de túnel del mismo asignador** (el pool de
  `wireguard`). Esa /32 es la **identidad del exportador**: el colector, el inventario de flujos y el
  sondeo SNMP no cambian ([ADR-0018](adr/0018-la-ip-es-el-cliente.md), [ADR-0022](adr/0022-mikrotik-routeros-v7-primer-fabricante.md)).
- Cambiar de transporte revoca el peer anterior y crea uno nuevo **con la misma IP de túnel** (la
  reutilización solo se permite para el mismo router), así que el histórico de flujos no se parte.
- MikroTik con WireGuard **no cambia**: mismo script, mismo hub `wg0`, mismos eventos.

## 2. Qué transporte elegir

| Caso | Recomendado |
| --- | --- |
| MikroTik RouterOS ≥ 7.12 | **WireGuard** (sugerencia del asistente). IPsec si la política del ISP lo exige. |
| Huawei VRP (AR, NE40E/NetEngine 8000 con tarjeta de servicio) | **IPsec IKEv2** (sugerencia del asistente). |
| Huawei sin IPsec (NE40E sin tarjeta de servicio) | **GRE** por un camino privado (L3VPN, VRF de gestión, fibra propia); por Internet solo con confirmación. |
| Ya hay VPN o red de gestión hasta Horus | `direct` (Huawei) o GRE `private`. |

Ventajas y costes:

- **WireGuard:** el más simple; un puerto UDP; no existe en VRP.
- **IPsec IKEv2:** estándar (RFC 7296 / RFC 4303), nativo en VRP y RouterOS; identidad criptográfica
  por router; más piezas (IKE, NAT-T, MTU); en NE40E suele requerir tarjeta de servicio.
- **GRE:** casi universal y barato en CPU; **sin cifrado ni autenticación**: cualquiera en el camino
  puede leer SNMP y flujos o inyectar paquetes con la IP del router. La clave GRE no es un secreto, solo
  separa túneles.

## 3. Nivel de protección por router

Cada router tiene `protection_level`, derivado del transporte y del camino declarado:

| Nivel | Cuándo | Consola |
| --- | --- | --- |
| `encrypted` | WireGuard o IPsec | insignia verde «Cifrado» |
| `unencrypted_private` | GRE o directo por un camino privado declarado (`path=private`) | insignia ámbar «Sin cifrado · red privada» |
| `unencrypted_internet` | GRE o directo por Internet (`path=internet`) | insignia roja **persistente** «Sin cifrado por Internet» en la ficha del router, la lista y el NOC |

Los routers en modo directo anteriores a este cambio migran a `unencrypted_private` (el camino lo
aseguraba el operador).

Cambio de transporte — `PUT /api/v1/routers/{id}/transport` (permiso `devices.credentials.write`):

1. Exige **re-autenticación reciente** (≤ 5 min; `403 REAUTH_REQUIRED` si no).
2. Sin cifrado hay que declarar `path` (`private` o `internet`).
3. `unencrypted_internet` exige además `confirm_unencrypted_internet: true`; si falta →
   `422 UNENCRYPTED_TRANSPORT_CONFIRMATION_REQUIRED` con el riesgo explicado. La consola muestra un diálogo con
   el riesgo, una casilla que hay que marcar y luego pide la contraseña (re-autenticación).
4. Se audita como `devices.router.transport_changed` (de/a, nivel, camino, confirmación) y, sin cifrado
   por Internet, se registra además un aviso en el log.
5. Un fabricante que no admite el transporte → `422 CONNECTION_MODE_NOT_SUPPORTED`
   (MikroTik: `wireguard`, `ipsec`, `gre`; Huawei VRP: `ipsec`, `gre`, `direct`).

Datos del router por transporte:

- `ipsec`: `tunnel_remote_id` (identidad IKE: IPv4, FQDN, `usuario@dominio` o `keyid:ID`). Por
  defecto la IP pública (`tunnel_remote_address`). Con IP dinámica o tras NAT, usar FQDN/`keyid:`.
- `gre`: `tunnel_remote_address` obligatoria (IPv4 pública **fija**): el hub fija el origen del túnel
  y solo acepta protocolo 47 de esa IP.

## 4. Arquitectura

```
consola ── PUT /routers/{id}/transport ──► devices (protection_level, auditoría)
        ── POST /routers/{id}/tunnel-config ──► wireguard (IP de túnel, PSK, clave GRE, config generada)
                                                   │ estado deseado completo + versión (gRPC mTLS)
                                                   ▼
                                       horus-tunnel-agent (NET_ADMIN, red del host)
                                         ├─ netlink: hxfrm<n> (XFRM, if_id=n) y hgre<n> (GRE, clave n)
                                         │           rutas /32 de la IP de túnel (proto 105)
                                         ├─ ipset: horus-gre-peers, horus-tunnel-src
                                         └─ vici (socket 0770, grupo 1430) ──► horus-ipsec (charon)
                                                                                UDP 500/4500, ESP
        ◄── ReportTunnelStatus (IKE SA, rekey, último paquete GRE) ── tunnel-agent
wireguard: peer.handshake_stale/recovered ──► snmp/alerts: estado del router `tunnel_down`
```

### 4.1 tunnel-agent (rol hermano de wg-agent)

Se eligió un **rol nuevo** (`HORUS_ROLES=tunnel-agent`, proto `horus.tunnels.v1`) en vez de ampliar
`wireguard-agent`:

- wg-agent sigue intacto (MikroTik WireGuard sin cambios, sin riesgo de regresión).
- Privilegios separados: solo quien activa IPsec/GRE ejecuta un proceso con acceso a XFRM, GRE, ipset
  y vici. Sin `--tunnels` el contenedor no existe.
- Mismo patrón: el plano de control (`wireguard`) empuja el **estado deseado completo con versión**; el
  agente converge y es *fail-static* (si pierde el control, mantiene lo último aplicado).

Privilegios: imagen `horus`, `network_mode: host`, `cap_drop: ALL` + `NET_ADMIN`, sistema de archivos de
solo lectura, `group_add: 1430` (socket vici). Solo escucha gRPC mTLS en la dirección interna
(`:9096`); el certificado de cliente aceptado es el de `wireguard`.

### 4.2 IPsec IKEv2 (strongSwan por vici)

- **charon** corre en su propio contenedor `horus-ipsec` (Alpine, strongSwan 5.9), red del host, sin
  configuración ni secretos en disco: tunnel-agent carga conexiones y PSK por **vici**
  (`load-shared`, `load-conn`, `unload-conn`, `terminate`) y lee el estado (`list-sas`). Arranca como
  root solo para soltar privilegios: queda como usuario `ipsec` con `CAP_NET_ADMIN`.
- **Route-based con interfaces XFRM**: una interfaz `hxfrm<n>` por router con `if_id=n`; la conexión
  del router lleva `if_id_in = if_id_out = n`. Los selectores del CHILD son `0.0.0.0/0` y el
  anti-suplantación lo hacen el `if_id` más el conjunto `horus-tunnel-src` (cada interfaz solo puede
  traer la IP de túnel de su router).
- **Propuestas** (solo AEAD o AES-256 con SHA-2 y grupos ≥ 2048 bits):
  - IKE: `aes256gcm16-prfsha384-ecp384`, `aes256-sha256-ecp384`, `aes256-sha256-modp2048`.
  - ESP: `aes256gcm16-ecp384`, `aes256-sha256-ecp384`, `aes256-sha256-modp2048` (PFS siempre).
- **DPD** 30 s (`dpd_action=clear`, el router reinicia), **rekey** IKE 4 h / ESP 1 h, **NAT-T** automático
  (UDP 4500, `encap` cuando hay NAT), `unique=replace`, `mobike=no`.
- **PSK por router**: 48 caracteres aleatorios, guardada como credencial `ipsec_psk` con *envelope
  encryption* (KEK de `devices`). Se muestra **una sola vez** en la configuración generada; después solo
  se puede regenerar (rotación = nueva configuración + revocación de la anterior). Solo se descifra para
  empujarla al agente por mTLS y vive en memoria de charon.
- Identidades: hub `HORUS_IPSEC_LOCAL_ID` (por defecto la IP del hub); router `tunnel_remote_id`.

### 4.3 GRE

- `ip_gre` del kernel: una interfaz `hgre<n>` por router, `local` = IP del hub, `remote` = IP pública
  del router (origen fijado), `ikey = okey = n` (clave por router).
- El cortafuegos acepta protocolo 47 **solo** desde el conjunto `horus-gre-peers` (IPs públicas
  registradas), que mantiene tunnel-agent.
- **Keepalives**: el estado se deriva del **último paquete recibido** por la interfaz (contadores);
  RouterOS se configura sin keepalive (`!keepalive`) porque el kernel Linux no responde a los
  keepalives GRE de Cisco/MikroTik y la interfaz quedaría caída. En VRP no se configura `keepalive`
  (desactivado por defecto). Sin paquetes en 180 s el túnel cuenta como caído.
- **MTU/MSS**: túnel con MTU `HORUS_TUNNEL_MTU` (1400 por defecto). GRE añade 24 bytes (28 con clave);
  IPsec ESP con NAT-T hasta ~73. En el router: MTU 1400 en la interfaz de túnel y, si hay PPPoE en el
  camino, `clamp-tcp-mss`. Los flujos son UDP: si se fragmentan, bajar la plantilla o el MTU.

## 5. Configuraciones generadas

`POST /api/v1/routers/{id}/tunnel-config` (re-autenticación reciente) devuelve la configuración del
transporte actual con los secretos **una vez** y marca el peer como configurado. Cada configuración
lleva bloque de deshacer.

| Fabricante | IPsec | GRE | Fuentes | Validación |
| --- | --- | --- | --- | --- |
| RouterOS 7 | `/ip ipsec profile, peer, proposal, identity, policy` + IP de túnel en loopback/bridge | `/interface gre` con `!keepalive` | [IPsec](https://help.mikrotik.com/docs/spaces/ROS/pages/11993097/IPsec), [GRE](https://help.mikrotik.com/docs/spaces/ROS/pages/24805521/GRE) | **CHR 7.12 y 7.23.8**: importación sin errores, IKE SA establecida contra charon cargado por vici, bloques de deshacer limpios (§8) |
| Huawei VRP | `ike proposal`, `ike peer` (v2), `ipsec profile`, `interface Tunnel` con `tunnel-protocol ipsec` | `interface Tunnel` con `tunnel-protocol gre` y `gre key` | Huawei NE40E/AR «IPsec Configuration», «GRE Configuration» (support.huawei.com) | **Pendiente de equipo real o eNSP**; plantillas fijadas por pruebas *golden* |
| Linux / strongSwan | `swanctl.conf` route-based con `if_id` e `ip link add … type xfrm` | `ip tunnel add … mode gre key` | [swanctl.conf](https://docs.strongswan.org/docs/latest/swanctl/swanctlConf.html), [route-based VPN](https://docs.strongswan.org/docs/latest/features/routeBasedVpn.html) | Contenedor strongSwan contra el hub real (`tests/tunnels/ipsec-e2e.sh`) |

El asistente de alta propone el transporte según el fabricante (Huawei → IPsec, MikroTik → WireGuard),
permite cambiarlo, pide IP pública / identidad, aplica el nivel de protección y muestra la
configuración con la PSK una sola vez.

## 6. Estado y alertas

- tunnel-agent informa cada 15 s (`HORUS_TUNNELAGENT_REPORT_EVERY`): IPsec → IKE SA establecida,
  último rekey, CHILD SA instalada; GRE → último paquete recibido.
- `wireguard` lo convierte en el mismo modelo de *handshake* que WireGuard: sin señal en 180 s →
  `peer.handshake_stale` → el router pasa a **`tunnel_down`** (alerta y NOC); al volver,
  `peer.handshake_recovered`. Los eventos llevan `transport`.
- La ficha del router muestra transporte, nivel de protección, estado del túnel, último rekey o último
  paquete; el NOC muestra la insignia de protección junto a la salud.

## 7. Cortafuegos y puertos

El instalador (`scripts/install.sh`, servicio `horus-tunnel`) y el contenedor `horus-host` (solo
Docker) aplican las mismas reglas:

- Cadena `HORUS-FLOWS` (UDP 4739, 2055, 6343): se acepta solo desde `wg0`, `hxfrm+`, `hgre+` y las
  direcciones de `--direct-flow-sources` (modo directo); el resto se descarta. SNMP solo sale/entra
  por los túneles.
- Cadena `HORUS-TUNNEL-SRC`: el origen de lo que entra por `hxfrm+`/`hgre+` debe ser la IP de túnel de
  ese router (`ipset horus-tunnel-src`, `hash:net,iface`).
- IKE (UDP 500/4500) y ESP (protocolo 50) se abren **solo** con `--tunnels ipsec`; GRE (protocolo 47)
  **solo** con `--tunnels gre` y solo desde `horus-gre-peers`.

| Puerto | Protocolo | Cuándo |
| --- | --- | --- |
| 51820 | UDP | siempre (WireGuard) |
| 500, 4500 | UDP | `--tunnels ipsec` |
| ESP | IP proto 50 | `--tunnels ipsec` (sin NAT en el camino) |
| GRE | IP proto 47 | `--tunnels gre` (solo desde IPs registradas) |

Instalación: `install.sh --tunnels ipsec,gre --tunnel-hub-address 203.0.113.10`. Variables:
`HORUS_TUNNEL_TRANSPORTS`, `HORUS_TUNNEL_HUB_ADDRESS`, `HORUS_IPSEC_LOCAL_ID`, `HORUS_TUNNEL_MTU`,
`HORUS_TUNNELAGENT_ADDR` (wireguard) y `HORUS_TUNNELAGENT_*` (agente). Necesita `ipset` en el host y los
módulos `xfrm_interface`, `esp4` e `ip_gre` del kernel (el instalador carga `xfrm_interface` e `ip_gre`;
`esp4` lo carga el kernel al instalar la primera SA).

## 8. Validación realizada

- **RouterOS CHR 7.12 y 7.23.8** (`scripts/lab/tunnels.sh`): la configuración IPsec generada se importa
  sin errores y establece la IKE SA con el hub configurado por vici (IKE AES_CBC_256/SHA2_256/ECP_384,
  ESP AES_GCM_16_256); los bloques de deshacer dejan el router limpio; la interfaz GRE se crea con
  keepalive nulo (`keepalive=0s,0` es rechazado por RouterOS: por eso `!keepalive`).
- **strongSwan ↔ hub** (`tests/tunnels/ipsec-e2e.sh`, contenedores): IKE establecida con la suite
  preferida (GCM/ECP384) y con la de respaldo (CBC/SHA256/MODP2048).
- **Switches vía router (§10), RouterOS CHR 7.23.8 con WireGuard** (`scripts/lab/via-router.sh`, TCG, 2026-10-18):
  con un "switch" en un netns de la LAN del CHR (IP privada 10.20.0.50) y la dirección Horus 10.255.3.40 en las
  allowed-ips del peer del hub, la configuración generada se importa sin errores; SNMP del hub a 10.255.3.40 llega
  al switch (que ve como origen 10.255.0.1) y vuelve con origen 10.255.3.40; otro puerto UDP se descarta; el sFlow
  del switch llega al colector con origen 10.255.3.40; reimportar no duplica reglas; el bloque «Deshacer» deja el
  router limpio y entonces la dirección Horus ya no alcanza al switch. Las variantes IPsec, GRE y con red declarada
  se importan y se deshacen sin errores en el mismo CHR (solo sintaxis: sin túnel IPsec/GRE real con el hub). Nota del
  CHR emulado: tras el import las reglas aparecen unos segundos como «I» (inválidas) antes de filtrar.
- **Camino de datos WireGuard en netns** (`TestViaRouterDataPath`, `-tags=wireguard`, wireguard-go): hub con el
  wg-agent real y dos "routers" de dos ISP con el **mismo** switch 192.168.88.2 detrás (NAT 1:1 con iptables /32):
  cada dirección Horus llega a su switch, el sFlow de cada uno llega con su dirección Horus, el router de un ISP no
  puede suplantar la dirección Horus del otro (WireGuard lo descarta) y un estado deseado con la misma IP privada en
  dos peers se rechaza entero.
- **No validado aún**: VRP en equipo real o eNSP (incluido el NAT estático y las ACL de §10 en la interfaz Tunnel);
  el camino de datos de §10 por IPsec/GRE en el hub (rutas y ipset de tunnel-agent sobre `xfrm_interface`/`ip_gre`);
  RouterOS 7.12 para §10 (solo 7.23.8); instalación de la CHILD SA y el camino de datos
  XFRM/GRE en un kernel con `xfrm_interface`/`ip_gre` (el entorno de pruebas no los tiene); reglas de
  cortafuegos ejecutadas en un host real.

## 9. Diagnóstico

| Síntoma | Revisar |
| --- | --- |
| IKE no establece | UDP 500/4500 llegan al hub; identidad del router = `tunnel_remote_id`; PSK actual (si se regeneró, reimportar); `docker logs horus-ipsec`. |
| `NO_PROPOSAL_CHOSEN` | El router no ofrece AES-256 + SHA-2 + grupo ≥ 14; actualizar firmware o propuesta. |
| IKE arriba, sin flujos | Origen NetStream/IPFIX = IP de túnel (loopback); ruta a la red de servicios por el túnel; `ipset list horus-tunnel-src`. |
| GRE sin tráfico | La IP pública real del router coincide con `tunnel_remote_address` (sin NAT); `ipset list horus-gre-peers`; clave GRE. |
| Fragmentación | MTU 1400 en el túnel, `clamp-tcp-mss`, plantilla de flujos más pequeña. |
| `tunnel_down` intermitente | DPD/rekey en logs de charon; CPU de la tarjeta de servicio (NE40E). |

## 10. Switches vía router del sitio

Decisión: [ADR-0032](adr/0032-switches-via-router-direccion-horus-y-nat-1-1.md). Un switch tiene casi
siempre una IP de gestión **privada**; con Horus en un servidor público no hay camino directo, y su
sFlow no debe salir a Internet sin cifrar. El switch se alcanza entonces **a través del túnel de un
router del sitio** (WireGuard, IPsec o GRE) del mismo ISP, sin túnel propio.

### 10.1 Cuándo usar cada modo

| Pregunta del asistente: «¿Horus está en la misma red de gestión que el switch?» | Modo |
| --- | --- |
| **No** (lo habitual: Horus en la nube o en otro sitio) — opción por defecto | `via_router` |
| **Sí** (Horus instalado dentro de la red de gestión, misma VLAN/VRF) | `direct` |

### 10.2 Cómo funciona

```
 hub Horus 10.255.0.1 ──wg0 / hxfrm<n> / hgre<n>──► router del sitio ──LAN de gestión──► switch 192.168.88.2
   SNMP a 10.255.1.40 (dirección Horus)            netmap 10.255.1.40 ⇄ 192.168.88.2     (ve SNMP desde 10.255.0.1)
   sFlow llega desde 10.255.1.40      ◄────────────  solo con la red de servicios          sFlow → 10.255.0.1:6343
```

- **Dirección Horus**: `wireguard` asigna al switch una /32 de la misma IPAM que las IP de túnel
  (`via_address`; única en la plataforma, cuarentena 24 h). Es su identidad: el sondeador SNMP la
  consulta y el colector identifica el sFlow por ella. La IP privada (`management_address`) **nunca**
  es identidad y nunca llega al hub: dos ISP con 192.168.88.2, o dos sitios del mismo ISP, no chocan.
- **Hub**: la dirección Horus viaja con el peer del router: wg-agent la añade a las allowed-ips del
  peer (WireGuard elige el peer por destino: sin solapes, ni entre ISP ni entre routers); tunnel-agent
  añade la ruta por `hxfrm<n>`/`hgre<n>` y la ligadura prefijo↔interfaz en `horus-tunnel-src`
  (antisuplantación). Nada cambia en los contenedores de aplicación.
- **Router del sitio**: NAT 1:1 (`netmap` en RouterOS, `nat static` en VRP) entre la dirección Horus y
  la IP privada, **solo** hacia la red de servicios del hub; cortafuegos mínimo: SNMP (UDP 161) desde
  Horus hacia el switch y sus respuestas, sFlow (UDP 6343) del switch hacia el colector; el resto entre
  Horus y el switch se descarta. Horus nunca escribe en el router: la configuración la pega el operador.
- **Switch**: su configuración de siempre (SNMPv3 de solo lectura; comunidad v2c en SwOS) con el
  sondeador = IP de servicios del hub y el colector sFlow = colector de Horus. El switch debe poder
  volver a la red de servicios por el router (su puerta de enlace de gestión o una ruta estática).
- **Red de gestión declarada** (opcional, `POST /routers/{id}/management-subnets`): /22–/30 privada;
  los switches con IP dentro comparten un bloque Horus alineado del mismo tamaño y una sola regla NAT.
  Las redes de un router no se solapan dentro del ISP.

### 10.3 Alta

`POST /api/v1/routers/{switch_id}/switch-onboarding` con `connection_mode=via_router`,
`via_router_id` y `management_address` (re-autenticación): devuelve la configuración del switch, el
camino (`path`: router, transporte, protección heredada, dirección Horus) y la del router del sitio
(`router_config`). El switch queda `snmp_verified` cuando responde con **su** credencial en su
dirección Horus; solo entonces su sFlow se acepta (fuente secundaria: nunca `flows_raw`, clientes ni
atribución).

Configuración del router generada (fijada por *golden files* en
`services/wireguard/internal/tunnelcfg/testdata/via-*`):

| Router | WireGuard | IPsec | GRE |
| --- | --- | --- | --- |
| RouterOS 7 | `netmap` con `in/out-interface=wg-horus` | `netmap` con `ipsec-policy=in,ipsec` + política IPsec para la dirección Horus (una CHILD SA por dirección o bloque) | `netmap` con `in/out-interface=horus-gre` |
| Huawei VRP | — | `nat static global … inside …` y `traffic-filter` (ACL 3991/3992) en `Tunnel10` | igual |

Cada bloque RouterOS quita primero sus reglas (`comment="horus-via <id del switch>"`, o
`horus-via-net <id de la red>`), así que pegarlo otra vez es seguro; al final va el bloque «Deshacer».

### 10.4 Estado y reglas

- Túnel del router caído → el switch queda **`unreachable`** con razón `via_router_tunnel_down` (no
  `offline`): el problema es el camino. alerts no avisa por el switch (el aviso es el del túnel).
- Sin ICMP: el router solo deja pasar SNMP; la señal ICMP del switch queda `unknown`.
- Un router con switches vía él no se da de baja ni pasa a modo directo (`409 ROUTER_HAS_VIA_SWITCHES`).
- La IP de gestión de un switch vía router solo cambia con su alta (el router necesita reglas nuevas).
- Protección: el switch hereda la del túnel del router en el tramo hub ↔ router (GRE = sin cifrar,
  con aviso en la configuración y en la ficha).

### 10.5 Validación

Ver §8 y la sección «Vía router» de [`vendors/mikrotik.md`](vendors/mikrotik.md) y
[`vendors/huawei.md`](vendors/huawei.md). `scripts/lab/via-router.sh` valida la configuración RouterOS
contra el CHR del laboratorio (WireGuard).
