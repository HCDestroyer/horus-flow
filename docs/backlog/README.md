# Backlog de Horus Flow

Cómo se organiza el backlog. El plan de sprints está en [`../roadmap.md`](../roadmap.md); las
épicas en [`epics.md`](epics.md); el reparto entre flujos paralelos en [`team.md`](team.md).

## 1. Estructura

```
docs/backlog/
├── README.md        ← este documento (reglas)
├── epics.md         ← todas las épicas del proyecto (EP-xx y transversales EP-Tx)
├── team.md          ← flujos de trabajo paralelos y contratos
├── sprint-00.md     ← historias detalladas por sprint
├── sprint-01.md
├── sprint-02.md
└── sprint-03.md     ← los sprints ≥ 4 se detallan en el planning del sprint anterior
```

Solo se detallan los próximos 3–4 sprints (horizonte de planificación). Los demás viven como
épicas con alcance, y se refinan en el *backlog refinement* de la segunda semana del sprint previo.

Cuando el proyecto adopte un gestor (GitHub Projects/Issues o GitLab Issues, según
`vision.md` §10), cada historia de estos archivos se convierte en un issue con el mismo ID; el
Markdown deja de ser la fuente de verdad para el estado, pero sigue siendo la de los criterios de
aceptación hasta que se migren.

## 2. Jerarquía e identificadores

| Nivel | ID | Ejemplo | Vive en |
| --- | --- | --- | --- |
| Épica | `EP-NN` (producto) / `EP-TN` (transversal) | `EP-05 Inventario` | `epics.md` |
| Historia | `SNN-MM` (sprint, número) | `S03-04` | `sprint-NN.md` |
| Tarea técnica / spike | `SNN-MM` con tipo `task` o `spike` | `S03-14` (spike WireGuard) | `sprint-NN.md` |
| Bug | `BUG-NNN` | | gestor de issues |

Una historia que no se termina **no se renumera**: pasa al siguiente sprint conservando su ID y
se anota "arrastrada desde SNN".

## 3. Formato de historia

```markdown
### S01-07 · Login con usuario y contraseña
- **Épica:** EP-03 · **Área:** frontend, backend · **Servicio:** auth, api-gateway
- **Tipo:** story · **Puntos:** 5 · **Prioridad:** Must
- **Depende de:** S01-03, S01-05

**Como** operador del NOC **quiero** iniciar sesión con mi usuario y contraseña
**para** acceder a la plataforma de forma segura.

**Criterios de aceptación**
1. **Dado** un usuario activo con contraseña válida, **cuando** envía el formulario,
   **entonces** accede al dashboard y recibe un access token de vida corta y un refresh token.
2. **Dado** …, **cuando** …, **entonces** …

**Notas técnicas:** enlaces a `api.md`, `security.md`, decisiones, fuera de alcance.
```

Reglas:

- El **rol** ("como…") es una persona real del producto: *administrador de plataforma*,
  *operador NOC*, *técnico de campo*, *analista de seguridad*, *gerente*, *ejecutivo comercial*,
  *ingeniero de plataforma* (para historias técnicas). No se usa "como usuario" genérico.
  Las personas se validan con el PO (pregunta P-03 en
  [`../open-questions/product.md`](../open-questions/product.md)).
- Las **tareas técnicas** sin usuario final (CI, compose) pueden escribirse como
  "Como ingeniero de plataforma quiero… para…" o como tarea con objetivo y criterios; ambos
  requieren criterios verificables.
- **Prioridad** MoSCoW dentro del sprint: *Must* (sin ella no hay objetivo de sprint), *Should*,
  *Could*. Lo *Could* es lo primero que sale si el sprint se desborda.

## 4. Criterios de aceptación (Given/When/Then)

- En español: **Dado / Cuando / Entonces** (equivalente a Given/When/Then), numerados.
- Cada criterio es verificable por un test automatizado o una demo concreta. "Rápido", "intuitivo"
  o "seguro" no son criterios; "responde en < 300 ms p95 con 100 routers" sí.
- Incluir siempre al menos **un caso negativo** (permiso denegado, entrada inválida, dependencia
  caída) además del camino feliz.
- Los criterios de frontend incluyen los **estados** de [`../frontend.md`](../frontend.md) §8
  (carga, vacío, error, degradado) cuando aplican.
- Los criterios de API referencian el endpoint y el código HTTP; el contrato completo vive en
  [`../api.md`](../api.md). Los de eventos, el subject `horus.<dominio>.<entidad>.<evento>` de
  [`../events.md`](../events.md).

## 5. Estimación

- **Story points**, escala Fibonacci modificada: 1, 2, 3, 5, 8, 13.
  - 1 = cambio trivial y conocido (½ día o menos).
  - 3 = historia típica bien entendida (1–2 días de una persona).
  - 8 = grande; aceptable pero se revisa si se puede partir.
  - **13 = no entra en un sprint tal cual: se divide antes del planning.**
- Los **spikes** se estiman en tiempo fijo (time-box) y se anotan con sus puntos equivalentes
  para no distorsionar la velocidad.
- Velocidad de referencia inicial (hasta medir 2 sprints): **~20–25 puntos por flujo y sprint**
  con 1–2 personas o agentes por flujo. S1 se planifica al 70 % de esa cifra por el arranque.
- Se estima en *planning poker* por el flujo responsable; si dos flujos participan, la historia se
  divide por flujo o se estima en conjunto.

## 6. Etiquetas

| Grupo | Valores | Uso |
| --- | --- | --- |
| `area:` | `frontend`, `backend`, `infra`, `data`, `security` | Obligatoria, una o más. `infra` incluye DevOps/CI/observabilidad; `data` incluye colectores, ClickHouse y pipelines; `security` se añade a todo lo que toque autenticación, autorización, secretos, criptografía o datos personales (dispara revisión de seguridad) |
| `service:` | `api-gateway`, `auth`, `devices`, `wireguard`, `snmp`, `flows`, `traffic-intelligence`, `reputation`, `detection`, `alerts`, `analytics`, `reporting`, `frontend` | Servicio(s) afectados |
| `type:` | `story`, `task`, `spike`, `bug`, `chore` | |
| `stream:` | `platform`, `backend`, `frontend`, `data` | Flujo responsable (ver [`team.md`](team.md)) |
| `contract:` | `openapi`, `event`, `schema`, `proto`, `ws` | La historia crea o cambia un contrato compartido → requiere aprobación del flujo consumidor |
| `priority:` | `must`, `should`, `could` | MoSCoW del sprint |

## 7. Definición de Listo (DoR)

Una historia entra al sprint si:

1. Tiene rol, objetivo y beneficio claros, y épica asignada.
2. Tiene criterios Dado/Cuando/Entonces con al menos un caso negativo.
3. Está estimada en ≤ 8 puntos.
4. Sus dependencias están terminadas o planificadas antes en el mismo sprint.
5. Los contratos que consume (OpenAPI, evento, esquema) están publicados al menos en borrador
   aprobado (ver [`team.md`](team.md) §3).
6. Si tiene UI, existe boceto o referencia al patrón de [`../frontend.md`](../frontend.md).

## 8. Definición de Terminado (DoD)

La DoD de `vision.md` §13 se aplica a **cada historia**. Esta tabla dice cómo se verifica cada
punto; el revisor del PR marca la lista (plantilla de PR, ver [`../conventions.md`](../conventions.md)).

| Punto de `vision.md` §13 | Cómo se verifica | N/A cuando |
| --- | --- | --- |
| Código | Merge a `main` vía PR según el Git workflow de [`../conventions.md`](../conventions.md) | — |
| Tests | Unitarios (`go test`, Vitest) en CI; integración contra compose para servicios; Playwright para flujos de UI de criterios de aceptación. Cobertura de líneas nueva ≥ 70 % en backend | Solo documentación |
| Manejo de errores | Errores con el formato estándar de [`../api.md`](../api.md); la UI muestra el estado de error de [`../frontend.md`](../frontend.md) §8 | — |
| Logs | Logs estructurados JSON con `trace_id`, sin secretos ni datos personales en claro ([`../observability.md`](../observability.md)) | — |
| Métricas | Métricas RED (rate, errors, duration) del endpoint/consumidor nuevo en Prometheus y panel en Grafana | Cambio solo de UI estática |
| Seguridad | Permiso `recurso.accion` aplicado en backend; `govulncheck`/`npm audit`/escaneo de imagen sin críticos; revisión de seguridad si tiene etiqueta `area:security` ([`../security.md`](../security.md)) | — |
| Documentación | README del servicio o sección de `docs/` actualizada; decisiones relevantes en ADR | — |
| API documentada | OpenAPI actualizado y validado en CI; eventos registrados en el catálogo de [`../events.md`](../events.md) | Sin API ni eventos |
| Migración DB | Migración versionada, reversible, probada en CI sobre base vacía y sobre la versión anterior | Sin cambios de esquema |
| Docker | Imagen construida en CI, servicio en `docker compose` con variables documentadas | Solo frontend de componente |
| CI/CD | Pipeline verde: lint → unit → integración → security check → build (`vision.md` §12) | — |
| Health check | `/healthz` (vida) y `/readyz` (dependencias) del servicio respondiendo; compose usa `healthcheck` | Sin servicio nuevo |
| Backup si aplica | Datos nuevos persistentes incluidos en la política de backup y en la prueba de restauración ([`../disaster-recovery.md`](../disaster-recovery.md)) | Sin datos persistentes nuevos |
| Revisión | ≥ 1 aprobación de otra persona/agente de un flujo distinto si cambia un contrato; demo en la review del sprint | — |

Además, para frontend: accesibilidad verificada (axe sin violaciones serias + navegación por
teclado del flujo), tema claro y oscuro revisados, textos en el sistema de i18n.

Para historias generadas por agentes de IA: el agente adjunta en el PR la salida de los tests y la
lista DoD marcada; un humano o el agente coordinador aprueba antes del merge.

## 9. Ceremonias (resumen de `vision.md` §12)

| Ceremonia | Cuándo | Entrada / salida |
| --- | --- | --- |
| Planning | Día 1 | Objetivo de sprint, historias comprometidas por flujo, riesgos y dependencias cruzadas |
| Daily | Diaria, ≤ 15 min (asíncrona para agentes: entrada en el canal del sprint) | ¿Qué hice? ¿Qué haré? ¿Qué me bloquea? |
| Sync de contratos | Martes de la semana 1 | Contratos del sprint siguiente en borrador ([`team.md`](team.md) §4) |
| Refinement | Miércoles de la semana 2 | Historias del sprint siguiente cumplen DoR |
| Review | Último día | Demo con funcionalidad real en el entorno compose (no diapositivas) |
| Retro | Último día | 1–3 acciones de mejora con dueño |
