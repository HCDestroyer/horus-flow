# 0028 — Flujo de trabajo de agentes de IA con verificación automática

- Estado: Aceptada
- Fecha: 2026-10-08
- Decisores: coordinador, a partir de la propuesta del Agente C (ronda 2) y [D7](../po-decisions.md)
- Detalle: [`conventions.md`](../conventions.md), [`backlog/team.md`](../backlog/team.md)

## Contexto

El equipo es IA + 1 persona ([D7](../po-decisions.md)). La persona no puede revisar cada cambio;
la calidad tiene que comprobarla una máquina y la revisión de código debe hacerla un agente
distinto del autor. Varios agentes trabajan a la vez en el mismo monorepo.

## Decisión

- Una rama por agente y tarea (`<agente>/<ID>-slug`); merge queue sobre `main`.
- Un PR solo se integra con CI verde **y** el check `agent-review` publicado por un agente
  revisor distinto del autor.
- `scope-guard`: cada agente solo puede tocar los módulos que posee; un PR que sale de su ámbito
  falla.
- La Definición de Terminado la comprueba un job `dod` (tests, lint, migraciones, OpenAPI,
  health checks, métricas, documentación).
- La persona revisa solo lo sensible (`CODEOWNERS`: contratos, seguridad, migraciones), en lote,
  una vez al día, y valida con su router real en cada hito.

## Alternativas consideradas

- **Revisión humana de todo:** no escala con una persona. Descartado.
- **Auto-merge solo con CI:** sin una segunda opinión, los errores de diseño pasan. Descartado.

## Consecuencias

- Antes de paralelizar hay que montar la CI, el `scope-guard` y el `dod` (Incremento 0).
- Los archivos compartidos se parten por dueño para evitar conflictos; las migraciones usan
  marca de tiempo.
