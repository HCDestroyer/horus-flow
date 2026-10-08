# packages/protobuf — contratos internos y payloads de eventos (C4, contrato v0)

- **Dueño:** INT (ruta sensible, cambios en PR `contract:protobuf`).
- **Documentación:** [ADR-0005](../../docs/adr/0005-grpc-protobuf-interno.md), [`docs/api.md`](../../docs/api.md) §5,
  [`docs/events.md`](../../docs/events.md) §5.3, [`docs/contracts/G0.md`](../../docs/contracts/G0.md).

| Paquete | Contenido |
| --- | --- |
| `horus.events.v1` | Sobre de eventos (`Envelope`, `Actor`) |
| `horus.common.v1` | `Reason`, `AuditRecord`, paginación |
| `horus.events.{auth,devices,wireguard,flows,traffic,detection,alerts,analytics}.v1` | Payloads del I0/I1 (estados como string, enum abierto) |
| `horus.devices.v1.InventoryService` | Snapshots para collector/ingester (exportadores, prefijos, claves de clientes) |
| `horus.auth.v1.SessionService` | `CheckSession` del gateway |
| `horus.wireguard.v1.{AgentService,ControlService}` | Control ↔ wg-agent (estado deseado idempotente) |

`buf lint` (STANDARD) y `buf breaking --against '.git#branch=main,subdir=packages/protobuf'` (FILE). `buf.gen.yaml`
describe la generación (Go en `gen/go`, TS de payloads en `gen/ts`); se ejecuta al cablear `make generate`.
Los snapshots binarios `.hsnp` de reputación y ASN (SEC) no son Protobuf: los eventos `*.snapshot_published` /
`catalog.published` solo los referencian por Object Store + `sha256`.
