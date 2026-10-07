# 0005 — gRPC + Protobuf para comunicación interna síncrona

- Estado: Aceptada
- Fecha: 2026-10-07

## Contexto

Algunos servicios necesitan respuestas inmediatas de otros (p. ej. `snmp` pide objetivos de sondeo
con credenciales a `devices`; `wireguard` aplica estado en `wireguard-agent`; el gateway verifica
sesiones en `auth`). Además, los payloads de eventos de alto volumen (lotes de flujos, métricas)
necesitan una serialización compacta.

## Decisión

- **gRPC con Protobuf** para llamadas síncronas servicio↔servicio, en puerto `9090`, con mTLS
  cuando se active ([security.md](../security.md)), deadlines obligatorios (por defecto 2 s) y
  reintentos solo en métodos idempotentes.
- Definiciones en `packages/protobuf/` gestionadas con **Buf** (`buf lint`, `buf breaking` en CI
  contra `main`). Paquetes versionados `horus.<servicio>.v1`.
- Protobuf también es el formato de los **eventos de telemetría de alto volumen**
  (`flows`, `snmp.metrics`). El formato de eventos de dominio (Protobuf o JSON) lo decide el
  Agente 3 en [events.md](../events.md).
- gRPC **no** se expone al frontend.
- Regla de uso: gRPC solo si el llamador necesita la respuesta para continuar; para notificar
  hechos se usa NATS ([ADR-0006](0006-nats-jetstream-bus-de-eventos.md)). Máximo 2 saltos
  síncronos detrás del gateway.

## Alternativas consideradas

- **REST/JSON interno**: más simple de depurar, pero sin contratos tipados fuertes, sin
  streaming y con más coste de serialización.
- **Connect-RPC**: compatible con gRPC y más amigable con HTTP/1.1; opción válida, se puede
  adoptar sin romper clientes si se necesita llamar desde navegador. No necesario hoy.
- **Todo por NATS request/reply**: reduce dependencias, pero mezcla semánticas (comando vs
  evento) y pierde tooling de gRPC (deadlines, interceptores, reflexión).

## Consecuencias

- (+) Contratos tipados, compatibilidad verificada en CI (`buf breaking`), buen rendimiento.
- (+) Interceptores comunes para OTel, auth de servicio y métricas.
- (−) Depuración menos directa (usar `grpcurl` con reflexión en dev).
- (−) Acoplamiento temporal: cada llamada gRPC es un punto de fallo; por eso se limitan y se
  acompañan de cachés locales (ver [architecture.md §5.1](../architecture.md#51-regla-de-elección)).
