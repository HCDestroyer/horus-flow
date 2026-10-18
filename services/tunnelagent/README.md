# `mod:tunnel-agent` — services/tunnelagent

- **Propósito:** Agente del hub IPsec IKEv2 y GRE ([ADR-0031](../../docs/adr/0031-hub-de-tuneles-ipsec-gre.md), [`docs/tunnels.md`](../../docs/tunnels.md)). Rol hermano de `wg-agent`: fail-static, `CAP_NET_ADMIN`, solo con `install.sh --tunnels`.
- **Rol(es) de `horus`:** `tunnel-agent`
- **Ruta sensible:** privilegios de red del host y PSK en tránsito (`area:security`).
- **Contrato:** `packages/protobuf/horus/tunnels/v1/agent.proto` (Go generado en `api/tunnelv1`).

## Funcionamiento

- `TunnelAgentService.ApplyTunnels` (lo llama `wireguard` por mTLS; solo acepta su certificado): estado **completo** + versión. Una versión
  menor que la aplicada (reintento tardío) no retrocede; la misma se vuelve a converger; valida cada túnel (índice, /32, IPs, identidad, PSK solo en IPsec, clave GRE solo en GRE).
- Por túnel `n` (`index`, estable por router):
  - **IPsec:** interfaz `hxfrm<n>` (XFRM, `if_id=n`, MTU del hub) y conexión `horus-<n>` en charon por **vici**
    (`load-shared` con la PSK, `load-conn` con `if_id_in/out=n`, propuestas fuertes, DPD 30 s, rekey 4 h/1 h, `unique=replace`).
  - **GRE:** interfaz `hgre<n>` (`local` = hub, `remote` = IP pública del router, `ikey=okey=n`).
  - Ruta /32 de la IP de túnel por la interfaz (protocolo 105) y entradas en los ipset `horus-gre-peers` (`hash:ip`) y
    `horus-tunnel-src` (`hash:net,iface`) que usa el cortafuegos de `horus-tunnel`/`horus-host`.
- Lo que sobra se retira (`unload-conn`, `terminate`, `unload-shared`, borrar interfaz, ruta e ipset). Si pierde a `wireguard`,
  mantiene lo último aplicado (fail-static).
- Cada `HORUS_TUNNELAGENT_REPORT_EVERY` (15 s) informa por `TunnelControlService.ReportTunnelStatus`: IKE SA establecida, último rekey,
  CHILD SA, último paquete GRE (contadores RX) y la versión aplicada.

## Adaptadores

| Driver | Uso |
| --- | --- |
| `kernel` (`internal/adapters/linux` + `vici`) | Producción: netlink (vishvananda/netlink), ipset y socket vici `/run/horus-ipsec/charon.vici`. |
| `memory` (`internal/adapters/memdev`) | Pruebas y el arnés de tenancy. |

## Variables

`HORUS_TUNNELAGENT_DRIVER` (`kernel`), `HORUS_TUNNELAGENT_VICI_SOCKET`, `HORUS_TUNNELAGENT_UNDERLAY_INTERFACE`,
`HORUS_TUNNELAGENT_GRE_SET`, `HORUS_TUNNELAGENT_SOURCE_SET`, `HORUS_TUNNELAGENT_GRPC_ADDR` (`:9096`),
`HORUS_TUNNELAGENT_REPORT_EVERY`, `HORUS_WIREGUARD_GRPC_ADDR_REMOTE` y las comunes de mTLS.

## Pruebas

- `go test ./services/tunnelagent/...` (dominio, app, gRPC, memdev).
- `go test -tags ipsec ./services/tunnelagent/internal/adapters/vici/` con un charon real (lo lanza `tests/tunnels/ipsec-e2e.sh`).
