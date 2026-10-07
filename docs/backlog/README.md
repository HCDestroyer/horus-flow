# Backlog de Horus Flow

Cómo se organiza el backlog tras las decisiones del PO ([`../po-decisions.md`](../po-decisions.md)):
**IA + 1 persona** (D7) y **incrementos en lugar de sprints** (D9). El plan está en
[`../roadmap.md`](../roadmap.md); las épicas en [`epics.md`](epics.md); el reparto entre agentes
en [`team.md`](team.md).

## 1. Estructura

```
docs/backlog/
├── README.md        ← este documento (reglas)
├── epics.md         ← épicas del proyecto y en qué incremento cae cada una
├── team.md          ← agentes, contratos congelados, ramas, PRs e integración
├── increment-0.md   ← historias de I0 (cimientos)
└── increment-1.md   ← historias de I1 (primer entregable)
```

Solo se detallan el incremento en curso y el siguiente. Los demás viven como épicas con alcance
en [`epics.md`](epics.md) y en [`../roadmap.md`](../roadmap.md) §3; el agente integrador detalla
`increment-N+1.md` cuando el incremento N entra en su última ola.

**Archivos retirados.** `sprint-00.md` a `sprint-03.md` se **eliminaron** en la ronda 2:
`sprint-00` describía el trabajo de documentación ya hecho (su resultado es `docs/`), y
`sprint-01..03` respondían a un plan de sprints que ya no existe. Las historias que siguen siendo
válidas se **convirtieron** a `increment-0.md` (CI, compose, plantilla Go, login, layout, e2e de
humo); el resto se retiró o quedó como alcance de I2/I3 en [`epics.md`](epics.md). Siguen
disponibles en el historial de Git. Trazabilidad completa en [`../roadmap.md`](../roadmap.md) §6.

Cuando el proyecto use GitHub Issues, cada historia se convierte en un issue con el mismo ID y
etiquetas; el Markdown sigue siendo la fuente de los criterios de aceptación.

## 2. Identificadores

| Nivel | ID | Ejemplo | Vive en |
| --- | --- | --- | --- |
| Épica | `EP-NN` (producto) / `EP-TN` (transversal) | `EP-10 Colección de flujos` | `epics.md` |
| Historia, tarea o spike | `IN-NN` (incremento, número) | `I1-07` | `increment-N.md` |
| Bug | `BUG-NNN` | | GitHub Issues |

Una historia que no se termina en su incremento **no se renumera**: pasa al siguiente con nota
"arrastrada desde IN".

## 3. Formato de historia (pensado para agentes de IA)

Cada historia debe poder ejecutarla un agente **sin preguntar**: contexto suficiente, archivos
implicados y una comprobación automática de terminado.

```markdown
### I1-07 · Detector de contacto con C2 conocido
- **Agente:** SEC · **Área:** data, security · **Tamaño:** M · **Épica:** EP-14
- **Depende de:** I0-17, I1-02 · **Contratos:** esquema de hallazgo v0, esquema CH v0

**Como** analista de seguridad del ISP **quiero** … **para** …

**Contexto:** qué hay que saber y dónde leerlo (docs y secciones concretas), supuestos.
**Archivos:** carpetas/archivos que crea o modifica (solo dentro de su propiedad, team.md §2).

**Criterios de aceptación**
1. **Dado** …, **cuando** …, **entonces** …
2. **Dado** … (caso negativo) …

**Hecho cuando:** `make test-detection && make accept-i1 SCENARIO=c2` en verde en CI.
**Fuera de alcance:** …
```

Reglas:

- **Rol** ("como…") es una persona real: *superadministrador de plataforma*, *administrador del
  ISP*, *operador NOC*, *analista de seguridad*, *gerente del ISP*, *pantalla NOC* (para el
  kiosco), *agente de IA* o *ingeniero de plataforma* (para historias técnicas).
- **Agente** es el responsable según [`team.md`](team.md): `INT` (integrador), `PLAT`, `CORE`,
  `FLOW`, `SEC`, `UI`; `PERSONA` para lo que solo puede hacer la persona.
- **Contexto** cita documentos con sección. Si el documento no responde algo, el agente aplica
  la regla de [`team.md`](team.md) §6.4 (supuesto explícito y reversible + pregunta registrada).
- **Archivos** limita el radio de acción: un agente no toca carpetas de otro sin PR de contrato.
- **Hecho cuando** es un comando o job de CI reproducible. Si algo solo se puede comprobar con
  el router real, la historia lo marca como **verificación de la persona** y además tiene una
  versión automática con el simulador.

## 4. Criterios de aceptación (Dado / Cuando / Entonces)

- Numerados, en español, cada uno verificable por un test automatizado. "Rápido", "intuitivo" o
  "seguro" no son criterios; "responde en < 300 ms p95 con 1 000 clientes" sí.
- Siempre **al menos un caso negativo** (permiso denegado, ISP ajeno, entrada inválida,
  dependencia caída).
- Todo criterio que lea o escriba datos de un ISP incluye el **caso de aislamiento**: otro ISP no
  ve ni modifica nada (respuesta `404`, nunca `403`, para no revelar existencia).
- Frontend: incluye los estados de [`../frontend.md`](../frontend.md) §9 (carga, vacío, error,
  degradado) y, si es un widget, su comportamiento en modo kiosco.
- API: referencia endpoint y código HTTP; el contrato completo vive en [`../api.md`](../api.md)
  y en `packages/schemas`. Eventos: subject de [`../events.md`](../events.md).

## 5. Tamaño

Sin story points ni velocidad (D9). Tallas relativas:

| Talla | Significado | Regla |
| --- | --- | --- |
| **S** | Cambio acotado, un área, < ~300 líneas | Un PR |
| **M** | Una funcionalidad completa en un área | 1–3 PRs pequeños |
| **L** | Cruza dos áreas o tiene incertidumbre técnica | **Se divide** antes de asignarse, salvo spikes con límite explícito |

Los **spikes** llevan un límite de alcance ("como máximo un prototipo y una nota en `docs/`") y
terminan en una decisión escrita.

## 6. Etiquetas (GitHub)

| Grupo | Valores | Uso |
| --- | --- | --- |
| `inc:` | `i0`, `i1`, `i2`… | Incremento |
| `agent:` | `int`, `plat`, `core`, `flow`, `sec`, `ui`, `persona` | Responsable |
| `area:` | `frontend`, `backend`, `infra`, `data`, `security` | `security` se añade a todo lo que toque autenticación, autorización, tenants, secretos o datos de clientes: exige aprobación de la persona |
| `contract:` | `openapi`, `event`, `schema`, `clickhouse`, `widget`, `ws` | Cambia un contrato congelado: exige versión nueva y aprobación del consumidor y de la persona |
| `type:` | `story`, `task`, `spike`, `bug`, `chore` | |
| `needs:` | `persona`, `hardware` | Bloqueada esperando a la persona o al router real |

## 7. Definición de Listo (DoR)

Una historia se asigna a un agente si:

1. Tiene rol, objetivo, épica, agente y talla ≤ M (o es un spike con límite).
2. Tiene contexto con enlaces, archivos implicados y comando de "hecho cuando".
3. Tiene criterios Dado/Cuando/Entonces con un caso negativo y, si aplica, de aislamiento.
4. Sus dependencias están mergeadas en `main` **o** consume solo contratos congelados (puede
   trabajar contra mocks/simulador).
5. Si tiene UI, referencia el patrón o widget de [`../frontend.md`](../frontend.md).

## 8. Definición de Terminado (DoD)

Con IA + 1 persona (D7) la DoD tiene que ser **verificable por máquina**. La comprueba CI y el
agente revisor; la persona solo interviene en lo marcado.

| Punto | Cómo se verifica (automático) | N/A cuando |
| --- | --- | --- |
| Código en `main` | PR squash-merge con CI verde, según [`team.md`](team.md) §5 y [`../conventions.md`](../conventions.md) | — |
| Tests | Unitarios (`go test -race`, Vitest) + integración contra compose; Playwright para criterios de UI; el comando "hecho cuando" pasa en CI | Solo docs |
| Aislamiento por ISP | Suite de matriz de tenants ampliada con los endpoints/temas nuevos | Sin datos de ISP |
| Errores | Formato de [`../api.md`](../api.md); la UI muestra los estados de [`../frontend.md`](../frontend.md) §9 | — |
| Logs y métricas | Logs JSON con `trace_id` y `tenant_id`, sin IPs de clientes en claro en logs de nivel info; métricas RED del endpoint/consumidor nuevo | UI estática |
| Seguridad | Permiso aplicado en backend (test 403/404); `govulncheck`/`npm audit`/Trivy sin críticos; gitleaks limpio | — |
| Contratos | OpenAPI/esquemas validados en CI; pruebas de contrato productor/consumidor | Sin contratos |
| Migraciones | Versionadas, probadas sobre base vacía y sobre la versión anterior; squawk sin errores | Sin esquema |
| Docker / health | Imagen construida; `healthz`/`readyz`; servicio en compose | Sin proceso nuevo |
| Backup | Datos persistentes nuevos incluidos en backup y en la prueba de restauración | Sin datos nuevos |
| Documentación | `docs/` o README del módulo actualizado en el mismo PR | — |
| Revisión | Aprobación de un agente revisor **distinto** del autor; además **la persona** si el PR lleva `area:security` o `contract:*` | — |

Frontend además: axe sin violaciones serias, navegación por teclado del flujo, tema claro y
oscuro, textos en i18n y captura de pantalla adjunta al PR (también en modo kiosco si es un
widget).

El agente autor adjunta en el PR: salida de tests, la lista DoD marcada y, para UI, capturas.

## 9. Ritmo de trabajo (sustituye a las ceremonias Scrum)

| Antes (Scrum) | Ahora (IA + 1 persona) |
| --- | --- |
| Planning | El integrador asigna historias "listas" a cada agente al abrir la ola (team.md §4) |
| Daily | Estado en el tablero de GitHub + comentario automático del integrador con bloqueos (`needs:persona`) |
| Sync de contratos | PR con `contract:*` aprobado por consumidores y persona |
| Refinement | El integrador redacta `increment-N+1.md`; la persona lo lee junto con la demo |
| Review | **Demo del incremento**: `make accept-iN` + prueba con el MikroTik real de la persona |
| Retro | Nota breve del integrador en el PR de cierre del incremento: qué bloqueó y qué se cambia |
