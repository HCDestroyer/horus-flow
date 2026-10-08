# packages/ — contratos y librerías compartidas

| Carpeta | Contenido | Dueño |
| --- | --- | --- |
| [`protobuf/`](protobuf/README.md) | `.proto` + `buf.yaml` (contratos internos, payloads de eventos) | INT (contrato) |
| [`events/`](events/README.md) | Catálogo de eventos NATS, streams, ejemplos | INT (contrato) |
| [`schemas/`](schemas/README.md) | OpenAPI, hallazgo, dashboard/widgets | INT (contrato) |
| [`go/`](go/README.md) | Librerías de plataforma Go (sin lógica de dominio) | CORE |

Todo cambio de contrato va en un PR `contract:*` aprobado por productor, consumidores y la persona
([`docs/backlog/team.md`](../docs/backlog/team.md) §3).
