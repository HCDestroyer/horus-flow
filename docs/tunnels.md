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
| `direct` | depende del camino | Huawei VRP | — | dirección de gestión verificada por SNMPv3 |

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
- **No validado aún**: VRP en equipo real o eNSP; instalación de la CHILD SA y el camino de datos
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
