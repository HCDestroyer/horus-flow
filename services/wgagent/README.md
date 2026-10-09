# `mod:wg-agent` — services/wgagent

- **Propósito:** Agente que aplica el estado WireGuard del hub (wgctrl: netlink o UAPI de wireguard-go), fail-static, `CAP_NET_ADMIN` (I1-01).
- **Rol(es) de `horus`:** `wg-agent`
- **Agente dueño:** CORE ([`docs/backlog/team.md`](../../docs/backlog/team.md) §2, [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Nombre de carpeta:** `wgagent` ([`docs/conventions.md`](../../docs/conventions.md) §1) para el módulo `mod:wg-agent` y el rol `wg-agent`.
- **Ruta sensible:** privilegios de red del host (`area:security`).
- **Contrato:** `packages/protobuf/horus/wireguard/v1/agent.proto` (Go generado en `api/agentv1`); interfaces en proceso en `api/`.

## Funcionamiento

- `ApplyDesiredState` (lo llama `wireguard`): estado **completo** + versión; valida claves públicas y que cada `allowed-ip` sea una
  sola dirección única; calcula el plan (añadir/actualizar/quitar) contra los peers observados y lo aplica. Idempotente; una versión
  menor que la aplicada se ignora; un estado inválido se rechaza entero sin tocar nada.
- Cada 15 s (`HORUS_WGAGENT_REPORT_EVERY`) envía `ReportStatus` (handshakes, endpoint, contadores, `interface_up`).
- **Fail-static:** si el control no responde no se toca ningún peer; tras reiniciar no borra nada hasta recibir un estado deseado
  completo (el control lo reenvía al ver `applied_version = 0`). Al parar no se limpia la interfaz.
- La interfaz y su dirección las crea el despliegue (el agente gestiona clave, puerto y peers):
  `ip link add wg0 type wireguard` (módulo de kernel) o `wireguard-go wg0` (espacio de usuario), y
  `ip addr add 10.255.0.1/16 dev wg0 && ip link set wg0 up`.

## Variables

`HORUS_WGAGENT_INTERFACE` (wg0), `HORUS_WG_PORT` (51820), `HORUS_WGAGENT_DRIVER` (`kernel` | `memory`, este solo en dev),
`HORUS_WGAGENT_PRIVATE_KEY_FILE` (clave privada del hub, obligatoria fuera de dev; nunca sale del agente), `HORUS_WG_HUB_ID`,
`HORUS_WGAGENT_GRPC_ADDR` (:9095), `HORUS_WIREGUARD_GRPC_ADDR_REMOTE` (ControlService de wireguard), `GRPC_TLS_MODE` (`mtls`) y
`HORUS_TLS_{CA,CERT,KEY}_FILE`. En el mismo proceso que `wireguard` se llaman por `module.Services` sin gRPC.

## Probar con WireGuard real sin módulo de kernel

El sandbox/CI puede no tener el módulo `wireguard`; `wireguard-go` crea la interfaz en espacio de usuario y wgctrl habla con ella por
su socket UAPI (`/var/run/wireguard/<if>.sock`). Requiere root o `CAP_NET_ADMIN` y `/dev/net/tun`:

```sh
sudo go test -tags=wireguard ./services/wgagent/internal/adapters/kernel/          # arranca wireguard-go -f hfwgtest0
HORUS_TEST_WG_KERNEL=1 sudo go test -tags=wireguard ./services/wgagent/internal/adapters/kernel/  # con módulo de kernel
```

Los tests unitarios (`go test ./services/wgagent/...`) usan la interfaz en memoria (`internal/adapters/memdev`).
