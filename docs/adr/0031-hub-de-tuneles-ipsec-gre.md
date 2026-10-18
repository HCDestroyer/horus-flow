# 0031 — Hub de túneles multitransporte: IPsec IKEv2 y GRE junto a WireGuard

- Estado: Aceptada (PO, 18/10/2026)
- Fecha: 2026-10-18
- Decisores: product owner, arquitectura
- Complementa: [ADR-0022](0022-mikrotik-routeros-v7-primer-fabricante.md) (WireGuard sigue siendo el
  camino de MikroTik) y [ADR-0025](0025-binario-modular-con-roles.md) (rol nuevo en el mismo binario).
- Cierra: [Q22](../open-questions/architecture.md#q22) y la decisión abierta de
  [`vendors/huawei.md`](../vendors/huawei.md) §5.4.

## Contexto

La gestión y los flujos de los routers viajan por un túnel WireGuard iniciado por el router; la IP de
túnel es la identidad del exportador. Huawei VRP no tiene WireGuard y hoy solo admite el modo directo,
cuya seguridad depende del camino del operador. Algunos ISP además exigen IPsec por política, o tienen
equipos sin cifrado por hardware.

## Decisión

1. **Tres transportes con túnel**: `wireguard`, `ipsec` (IKEv2 route-based) y `gre`, más `direct` para
   Huawei. Huawei → IPsec IKEv2 como camino estándar y GRE como alternativa sin cifrado.
2. **Una sola identidad**: todos los túneles reciben su /32 del mismo asignador de `wireguard`; el
   colector, el inventario y el sondeo no cambian. Cambiar de transporte conserva la IP del router.
3. **Rol hermano `tunnel-agent`** (no ampliar wg-agent): agente privilegiado (`NET_ADMIN`, red del
   host) que recibe el estado deseado completo por gRPC mTLS (`horus.tunnels.v1`) y crea interfaces
   XFRM (`if_id` por router) y GRE (clave por router y origen fijado), rutas /32 y conjuntos ipset.
   wg-agent queda intacto y los privilegios nuevos solo existen si se activa `--tunnels`.
4. **strongSwan (charon) por vici** en un contenedor propio sin configuración ni secretos en disco;
   propuestas solo AES-256-GCM o AES-256/SHA-2 con ECP-384/MODP-2048, PFS, DPD, rekey y NAT-T.
5. **PSK por router** (no certificados en esta entrega): aleatoria, cifrada con la KEK de `devices`,
   mostrada una vez. PKI queda como evolución.
6. **Nivel de protección por router** (`encrypted`, `unencrypted_private`, `unencrypted_internet`).
   Sin cifrado por Internet exige confirmación explícita y re-autenticación, se audita y muestra una
   insignia persistente.
7. **Cortafuegos por interfaz**: flujos y SNMP solo desde `wg0`, `hxfrm+`, `hgre+` o direcciones
   directas verificadas; IKE/ESP/GRE solo se abren cuando el transporte está activo.

## Alternativas descartadas

- **GRE sobre IPsec** (transporte): dos capas que depurar y más cabecera; Horus no necesita multicast.
- **Relé WireGuard en el sitio**: un equipo más por sitio y la IP de origen deja de ser la del router.
- **Ampliar wg-agent**: mezcla privilegios y arriesga el camino MikroTik ya validado.
- **Libreswan** u otro IKE: strongSwan tiene vici (API estable), XFRM `if_id` y es el más extendido.

## Consecuencias

- Un segundo demonio que operar (charon) y dos contenedores nuevos opcionales.
- Requiere `xfrm_interface`, `esp4`, `ip_gre` e `ipset` en el host.
- La configuración VRP queda pendiente de validación en equipo real; RouterOS se validó en CHR.
- Detalle operativo: [`tunnels.md`](../tunnels.md).
