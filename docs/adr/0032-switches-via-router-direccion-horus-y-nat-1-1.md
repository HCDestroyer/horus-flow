# 0032 — Switches vía router del sitio: dirección Horus por equipo y NAT 1:1 en el router

- Estado: Aceptada (PO, 18/10/2026; diseño elegido por arquitectura)
- Fecha: 2026-10-18
- Decisores: product owner, arquitectura
- Complementa: [ADR-0031](0031-hub-de-tuneles-ipsec-gre.md) (hub multitransporte) y
  [ADR-0018](0018-la-ip-es-el-cliente.md) (la IP de origen identifica al exportador).
- Detalle operativo: [`tunnels.md`](../tunnels.md) §10.

## Contexto

Los switches casi siempre tienen una IP de gestión **privada** (192.168.88.2 es la de fábrica de
MikroTik). El modo `direct` solo funciona si Horus está en esa misma red; con Horus en un servidor
público no hay camino, y el sFlow del switch saldría a Internet sin cifrar. Además la unicidad de
`management_address` en toda la plataforma impedía que dos ISP (o dos sitios del mismo ISP) usaran la
misma IP privada. El router del sitio ya tiene un túnel con el hub (WireGuard, IPsec o GRE).

Restricciones duras:

- **WireGuard usa *cryptokey routing***: en una interfaz (`wg0`) un prefijo solo puede estar en las
  allowed-ips de **un** peer, y WireGuard elige el peer por la **dirección de destino** del paquete
  interior. Dos ISP con 192.168.88.0/24 no pueden convivir en el mismo `wg0`; tampoco dos routers del
  mismo ISP con la misma red de gestión.
- Las rutas de `hxfrm<n>`/`hgre<n>` están en la tabla principal del hub: el mismo choque.
- Los contenedores de aplicación (sondeo SNMP, colector) no tienen `NET_ADMIN` ni deben tenerlo.
- Horus nunca escribe en los equipos; el operador pega la configuración.

## Decisión

1. **Dirección Horus por equipo.** Cada switch vía router recibe una dirección de la **misma IPAM** que
   las IP de túnel (`HORUS_WG_TUNNEL_CIDRS`, única en la plataforma, cuarentena 24 h). Esa /32 es su
   identidad: destino del sondeo SNMP, origen esperado del sFlow, `via_address` en devices.
2. **NAT 1:1 en el router del sitio** (`netmap` en RouterOS, `nat static` en VRP) entre la dirección
   Horus y la IP privada, **solo** para el tráfico con la red de servicios del hub. La IP privada solo
   existe en esa regla del router de ese sitio: nunca llega al hub, a la IPAM ni al colector.
3. **El hub enruta la dirección Horus por el túnel de ese router**, como una /32 más del peer:
   allowed-ips del peer en wg-agent; ruta por `hxfrm<n>`/`hgre<n>` y ligadura prefijo↔interfaz en el
   ipset `horus-tunnel-src` en tunnel-agent. Lo aplican los agentes privilegiados con el estado deseado
   completo (fail-static); ningún contenedor de aplicación toca la red.
4. **Red de gestión opcional por router**: un bloque Horus alineado del mismo tamaño (/22–/30) y una
   sola regla NAT para toda la red; las redes de un router no se solapan dentro del ISP.
5. **Cortafuegos mínimo en el router**, generado y comentado: SNMP (UDP 161) desde la red de servicios
   hacia el switch, sus respuestas y sFlow (UDP 6343) del switch hacia el colector; el resto entre Horus
   y el switch se descarta. Bloques de deshacer.
6. **Prueba de control y estado**: el switch queda `snmp_verified` solo cuando responde con **su**
   credencial SNMP en su dirección Horus; solo entonces su sFlow se acepta (fuente secundaria, nunca
   `flows_raw`). Si el túnel del router cae, el switch queda `unreachable` / `via_router_tunnel_down`.
7. **Unicidad por ISP**: `management_address` es única por ISP (modo directo, entre verificados) y por
   (ISP, router, IP) vía router. Una dirección directa repetida entre ISP queda fuera del inventario de
   flujos (ambigua: nunca se adivina el tenant) y la verificación exige la credencial del propio equipo.

## Alternativas descartadas

- **Añadir la IP privada a las allowed-ips del peer** (lo que pedía la idea inicial): imposible con
  WireGuard en cuanto dos ISP, o dos routers, repiten la red; wg-agent ya lo rechazaba a propósito.
- **Espacios de red (netns) o VRF por ISP en el hub**: resuelve el choque entre ISP pero no entre
  routers del mismo ISP; exige una interfaz WireGuard (y un puerto) por ISP, cambiar el script de todos
  los routers y que el sondeador y el colector abran sockets dentro de cada VRF (`SO_BINDTODEVICE`,
  privilegios en contenedores de aplicación).
- **Policy routing con marcas por router**: WireGuard sigue eligiendo el peer por destino aunque la
  tabla cambie, y marcar por destino exige que la aplicación ponga `SO_MARK` (`NET_ADMIN`).
- **NAT 1:1 en el hub** (DNAT dirección Horus → IP privada antes del túnel): sirve para XFRM/GRE con
  tablas por túnel, pero no para WireGuard (tras el DNAT el destino vuelve a ser la IP privada y
  `wg0` no sabe a qué peer mandarla; el sFlow con origen privado tampoco pasaría el filtro de
  allowed-ips). Dos mecanismos distintos según el transporte, más estado en el hub.
- **Reenvío por puertos en la IP de túnel del router** (el patrón de los servicios de infraestructura):
  un puerto por switch y por protocolo, sin identidad propia del exportador (el sFlow llegaría con la
  IP de túnel del router y se confundiría con sus flujos).
- **Un pool aparte en 100.64.0.0/10** (p. ej. 100.127/16): los ISP usan CGNAT para sus clientes; el
  router tendría rutas en conflicto. La IPAM de túneles ya lo prohíbe.

## Consecuencias

- El operador pega dos configuraciones (switch y router del sitio). Las reglas del router son pocas,
  comentadas, idempotentes (cada bloque quita sus reglas antes de crearlas) y con bloque de deshacer.
- Funciona igual con WireGuard, IPsec y GRE; el switch hereda el nivel de protección del túnel del
  router en el tramo hub ↔ router (GRE = sin cifrar, con aviso).
- IPsec en RouterOS crea una política (y una CHILD SA) por dirección Horus o por bloque.
- Las direcciones Horus consumen la IPAM de túneles (un bloque por red declarada).
- Un router con switches vía él no se da de baja ni pasa a modo directo sin moverlos antes.
- VRP queda pendiente de validación en equipo real (como el resto de VRP).
