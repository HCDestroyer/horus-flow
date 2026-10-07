# 0014 — Granularidad de microservicios en el MVP

- Estado: Sustituido por ADR-0025
- Fecha: 2026-10-07

## Contexto

`vision.md` dibuja 14 piezas (incluidas `network-service`, `security-service`, `reputation`,
`detection`, `reporting`) y a la vez pide no desarrollar todos los microservicios a la vez y no
definirlos todavía como definitivos. Cada desplegable cuesta CI, imagen, observabilidad, contratos
y un salto de red. El equipo es pequeño.

## Decisión

Separar servicios solo por **escala distinta, ciclo de vida distinto, privilegios distintos o
equipo distinto**. Aplicado:

1. **`network-service` no existe**: su contenido (IPAM de túneles, servidores/peers) va a
   `wireguard`; la topología de sitios/IPs de routers es de `devices`. La separación real es por
   privilegios: `wireguard` (control, sin privilegios) + `wireguard-agent` (mismo binario, modo
   agente, `CAP_NET_ADMIN`, red del host).
2. **`security`, `reputation` y `detection` son un solo servicio `detection`** con módulos
   internos `reputation`, `correlation`, `scoring`. "Security" es una categoría de hallazgos, no un
   servicio. `services/reputation/` se crea al separarlo.
3. **`reporting` es un rol worker de `analytics`** (mismo binario, proceso separado).
   `services/reporting/` se crea al separarlo.
4. **`flows` es un binario con dos roles** (`collector`, `ingester`) que escalan por separado.
5. **`snmp` incluye ICMP y el estado observado** de routers.
6. **`auth` incluye la auditoría** en v1.

MVP técnico (Sprints 1–5): `api-gateway`, `auth`, `devices`, `wireguard` (+agent), `snmp`. El
catálogo completo y criterios de separación están en [services.md](../services.md).

Los nombres de carpetas acordados (`api-gateway, auth, devices, wireguard, snmp, flows,
traffic-intelligence, reputation, detection, alerts, analytics, reporting`) no cambian; solo se
difiere la creación de `reputation/` y `reporting/`.

## Alternativas consideradas

- **Seguir el plan literal (14 servicios)**: máxima separación, pero 3 servicios para una sola
  cadena de correlación y un `network-service` sin dominio claro; más coste sin beneficio.
- **Monolito modular** (un binario Go con módulos): mínimo coste operativo, pero mezcla
  privilegios (WireGuard), perfiles de carga (flujos vs CRUD) y anula P1 (el plano de
  administración no depende del analítico). Se descarta como objetivo, aunque los servicios del
  plano de control podrían compartir proceso en desarrollo local si conviene.
- **Fusionar también `devices` + `wireguard`**: comparten "infraestructura de red", pero
  WireGuard tiene un agente privilegiado y un ciclo de cambios distinto; se mantienen separados.

## Consecuencias

- (+) 6 procesos en el MVP en lugar de 8–9; 13 al final de la fase 1.0 en lugar de 16+.
- (+) Fronteras de módulo internas preparadas para separar (paquetes `internal/<módulo>` sin
  imports cruzados salvo por interfaces).
- (−) Si un módulo crece rápido, la separación posterior implica migrar datos de esquema y
  publicar contratos que hoy son internos; se mitiga manteniendo tablas del módulo con prefijo
  propio.
- (−) El roadmap ([roadmap.md](../roadmap.md)) debe reflejar estas fusiones (Sprints 4, 8 y 12).
