# 0003 — Go como lenguaje del backend

- Estado: Aceptada
- Fecha: 2026-10-07

## Contexto

El backend combina APIs CRUD, procesos de larga duración con miles de dispositivos (SNMP, ICMP),
recepción UDP de alto volumen (NetFlow/IPFIX/sFlow, hasta cientos de miles de registros/s),
gestión de WireGuard en el kernel y WebSockets. Se despliega en contenedores pequeños,
inicialmente en un par de hosts.

## Decisión

**Go** (versión estable más reciente, fijada en `go.mod` y en la imagen base) para todos los
servicios backend. Librerías base previstas (a confirmar en [conventions.md](../conventions.md)):
`gosnmp` (SNMP), `golang.zx2c4.com/wireguard/wgctrl` (WireGuard), `nats.go` (NATS/JetStream),
`pgx` (PostgreSQL), `clickhouse-go` v2 (ClickHouse, protocolo nativo), `go-chi/chi` (REST),
`google.golang.org/grpc`, OpenTelemetry Go SDK. Decodificación de flujos: evaluar
`netsampler/goflow2` como librería frente a implementación propia (spike en Sprint 6).

## Alternativas consideradas

- **Rust**: mejor rendimiento y sin GC para el collector de flujos, pero mayor curva y menor
  productividad en CRUD; ecosistema SNMP/WireGuard menos maduro. Se reconsidera **solo** para el
  decodificador de flujos si el profiling demuestra que el GC de Go es el cuello de botella.
- **Node.js/TypeScript**: comparte lenguaje con el frontend, pero peor para UDP de alto volumen,
  concurrencia CPU-bound y llamadas netlink.
- **Python**: buen ecosistema de red, pero rendimiento insuficiente para flujos y despliegue más
  pesado.
- **Java/Kotlin**: rendimiento adecuado, pero huella de memoria y arranque mayores.

## Consecuencias

- (+) Concurrencia barata (goroutines) para miles de sondeos simultáneos; binarios estáticos e
  imágenes `distroless` de ~20 MB; un solo lenguaje en todo el backend.
- (+) Librerías maduras para todos los protocolos del dominio.
- (−) GC: el collector de flujos debe diseñarse con pools de buffers y lotes para minimizar
  asignaciones.
- (−) Frontend y backend en lenguajes distintos: los contratos se comparten vía OpenAPI/Protobuf
  con generación de tipos, no compartiendo código.
