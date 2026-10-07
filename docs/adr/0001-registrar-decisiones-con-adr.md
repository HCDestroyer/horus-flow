# 0001 — Registrar decisiones de arquitectura con ADR

- Estado: Aceptada
- Fecha: 2026-10-07
- Decisores: equipo Horus Flow (Sprint 0)

## Contexto

[vision.md](../vision.md) establece que "si algo contradice este documento, se registra como ADR"
y que el Sprint 0 entrega `docs/adr/`. El proyecto tiene 16 sprints, varios servicios y decisiones
que se cuestionarán (granularidad de servicios, gateway, almacenamiento). Sin registro, el porqué
de cada decisión se pierde y se re-discute.

## Decisión

Usar **ADR en formato MADR corto** en `docs/adr/NNNN-titulo-kebab.md`, en español, con las
secciones: Contexto, Decisión, Alternativas consideradas, Consecuencias (y cabecera con Estado,
Fecha, Decisores).

- Numeración secuencial de 4 dígitos; nunca se reutiliza un número.
- Estados: `Propuesta` → `Aceptada` | `Rechazada`; más tarde `Obsoleta` o `Reemplazada por NNNN`.
  Un ADR aceptado **no se edita** en su fondo: se crea uno nuevo que lo reemplaza.
- Un ADR se exige cuando la decisión: contradice `vision.md`; añade/quita/fusiona un servicio o
  un almacén de datos; cambia un contrato público (REST, eventos, tablas publicadas de
  ClickHouse); introduce una dependencia de infraestructura; o es costosa de revertir.
- El ADR va en el mismo PR que el cambio que motiva. El índice vive en [README.md](README.md).

## Alternativas consideradas

- **Wiki / Notion**: se desincroniza del código y no se revisa en PR.
- **Nygard clásico**: equivalente; MADR añade "alternativas" explícitas, útil en un proyecto que
  cuestiona su plan base.
- **Sin registro formal**: inaceptable dado el tamaño del plan.

## Consecuencias

- (+) Trazabilidad del porqué; incorporación más rápida de personas nuevas.
- (+) Revisión de decisiones en el mismo flujo de PR.
- (−) Pequeño coste por decisión; se mitiga con plantilla corta.
