# Equipo: agentes de IA en paralelo + 1 persona

Cómo se reparte el trabajo de los incrementos entre **agentes de IA** que trabajan en paralelo y
**una persona** (D7 de [`../po-decisions.md`](../po-decisions.md)). Sustituye al reparto en
"flujos humanos" del Sprint 0.

Relacionado: [`../roadmap.md`](../roadmap.md), [`README.md`](README.md) (formato de historia y
DoD), [`increment-0.md`](increment-0.md), [`increment-1.md`](increment-1.md),
[`../conventions.md`](../conventions.md) (Git, CI y flujo de trabajo con agentes — si algo de
este documento choca con `conventions.md`, prevalece `conventions.md`).

## 1. Principios

1. **La atención de la persona es el recurso escaso.** Todo lo que se pueda verificar por
   máquina lo verifica CI o un agente revisor. La persona aprueba contratos y seguridad, prueba
   con su MikroTik y decide; no revisa cada PR.
2. **Contrato antes que código.** Ningún agente implementa contra un contrato de otro que no esté
   congelado; mientras tanto trabaja contra mocks generados del contrato o contra el simulador.
3. **Propiedad por directorio.** Cada agente escribe solo en sus carpetas (CODEOWNERS). Tocar las
   de otro requiere PR de contrato o aprobación de su dueño.
4. **Integración continua en `main`.** PRs pequeños, merge varias veces al día, `main` siempre
   verde. La demo de un incremento sale de `main`.
5. **Ninguna decisión de arquitectura implícita.** Si falta una decisión, el agente aplica un
   supuesto explícito y reversible y lo registra (§6.4).

## 2. Agentes

Seis roles: **cinco agentes constructores en paralelo** más un **integrador**. El trabajo de
desarrollo se reparte **por módulo del binario `horus`**, no por proceso. Cada rol puede ser
una sesión de agente distinta; un agente puede llevar dos roles si la carga lo permite (p. ej.
PLAT + INT al final de I1), pero nunca revisa su propio PR.

| Agente | Misión | Propiedad | Módulos / roles de `horus` ([ADR-0025](../adr/0025-binario-modular-con-roles.md)) |
| --- | --- | --- | --- |
| **INT — Integrador** | Congela y versiona contratos; asigna historias; fusiona PRs; mantiene `make accept-iN`; prepara la demo y el siguiente `increment-N.md`; vigila alarmas (§5.3) | `packages/` (OpenAPI, Protobuf, esquemas de eventos, layout de dashboard, hallazgo), `tests/acceptance/`, `docs/` (salvo lo que la persona reserve) | — |
| **PLAT — Plataforma** | Repo, compose, CI, imagen única, observabilidad, laboratorio CHR, instalador, backup, pruebas de fallo y de carga | `infrastructure/`, `deployments/`, `scripts/`, `.github/`, `Makefile`, `cmd/horus` (arranque y registro de roles, revisa CORE) | — |
| **CORE — Backend core** | Borde HTTP/WS, identidad y tenants, inventario, registro de clientes, WireGuard, tokens de kiosco, persistencia de dashboards, script RouterOS | `mod:gateway`, `mod:auth`, `mod:devices`, `mod:wireguard`, `mod:wg-agent`, `mod:analytics/dashboards`, librerías comunes de Go (revisa PLAT) | gateway, auth (tenants, membresías, roles), devices (nodos, routers, realms, clientes), wireguard, wg-agent |
| **FLOW — Datos de tráfico** | Simulador, fixtures RouterOS, colector, ingester (enriquecimiento, descubrimiento de IPs), ClickHouse, catálogo, consultas de tráfico, estado de exportadores | `mod:collector`, `mod:ingester`, `mod:traffic`, `mod:analytics` (salvo `dashboards`), `infrastructure/clickhouse`, `tools/flowsim` | collector, ingester, traffic, analytics |
| **SEC — Seguridad y detección** | Feeds de reputación, detectores de botnet, hallazgos y su ciclo de vida; después scoring comercial y mitigación | `mod:detection` | detection (reputation, correlation, scoring) |
| **UI — Frontend** | Shell, selector de ISP, marco de widgets, vistas, dashboards, modo kiosco, e2e | `apps/frontend` | — |
| **PERSONA** | Aprobar, probar con su MikroTik real, decidir | — | — |

**Notación `mod:<módulo>`.** Con el binario modular de
[ADR-0025](../adr/0025-binario-modular-con-roles.md) cada módulo vive en su carpeta con fronteras
estrictas; `mod:<módulo>` designa esa carpeta en la ruta que fije
[`../conventions.md`](../conventions.md) (estructura del repo). La **propiedad es por módulo**:
CODEOWNERS se genera a partir de esta tabla en el PR que congela C1. Ningún módulo importa el
`internal/` de otro, solo su paquete público de contrato (lo comprueban los tests de arquitectura
de [ADR-0023](../adr/0023-entrega-por-incrementos-y-equipo-ia.md)).

## 3. Contratos que se congelan antes de paralelizar

"Congelado" = PR mergeado en `packages/` o `docs/` con aprobación del productor, de cada
consumidor y **de la persona** (una sola revisión conjunta, gate **G0**). Después, todo cambio
incompatible requiere versión nueva (`v1`, `v2`…) y convivencia de ambas hasta migrar a los
consumidores.

| # | Contrato | Dónde | Productor → consumidores | Necesario para |
| --- | --- | --- | --- | --- |
| C1 | Módulos y roles del binario `horus`, perfiles de despliegue, estructura del repo, CODEOWNERS | ADR-0025, `services.md`, `conventions.md`, `.github/CODEOWNERS` | INT (de la arquitectura) → todos | Empezar Ola 1 |
| C2 | Modelo multi-tenant: ISP, nodo, router (exportador), prefijos de clientes (realm), cliente-IP, usuario ↔ ISP | `database.md` + migraciones v0 | CORE → FLOW, SEC, UI | I0-06 |
| C3 | Esquema ClickHouse de flujos crudos y agregados (incl. `tenant_id`, lado cliente, TTL) | `database.md`, `traffic-model.md`, `infrastructure/clickhouse` | FLOW → SEC, CORE | I0-13, I1-04, detectores |
| C4 | Sobre de eventos y subjects de I1 (exportador, cliente descubierto/cambio de tipo, hallazgo) | `events.md`, `packages/events` | INT → todos | I1 |
| C5 | OpenAPI v0: auth/`me`, ISP, nodos, routers, clientes, tráfico, hallazgos, dashboards, kiosco | `api.md`, `packages/schemas/openapi` | CORE/FLOW/SEC → UI | UI con mocks |
| C6 | Temas WebSocket con ámbito de ISP y su payload | `api.md` | CORE → UI | I1-13, widgets en vivo |
| C7 | Catálogo de permisos, roles y token de kiosco | `security.md` | CORE → todos | I0-07, I1-14 |
| C8 | Esquema de hallazgo (detector, severidad, confianza, evidencia, estado) | `events.md`, `packages/schemas` | SEC → CORE, UI | I1-10…I1-12, I1-18 |
| C9 | Manifiesto de widget y formato de layout de dashboard | [`../frontend.md`](../frontend.md) §6, `packages/schemas/dashboard` | UI → CORE (persistencia) | I0-16, I1-15, I1-20 |
| C10 | Escenarios del simulador y fixtures de RouterOS (plantillas NetFlow v9/IPFIX) | `vendors/mikrotik.md`, `tools/flowsim/scenarios` | FLOW → SEC, INT | Pruebas de detección y aceptación |

### Cómo se trabaja sin bloquear

- **UI** genera tipos TypeScript desde C5 y usa mocks (MSW o equivalente) con datos del simulador;
  al existir el backend real cambia de origen sin tocar componentes.
- **SEC** desarrolla detectores contra tablas ClickHouse cargadas desde los escenarios de C10, sin
  esperar al colector real.
- **FLOW** prueba la ingesta con el simulador desde el primer día y con capturas reales de
  RouterOS grabadas en el laboratorio CHR (I0-12) y, si la persona la aporta, con una captura
  anonimizada de su router.
- **CORE** valida respuestas y eventos contra C4/C5 en CI (pruebas de contrato productor); los
  consumidores tienen pruebas con los ejemplos del esquema.

## 4. Orden de arranque (olas)

Las olas no tienen duración fija: una ola termina cuando sus historias están en `main` y su
comprobación pasa.

| Ola | INT | PLAT | CORE | FLOW | SEC | UI | PERSONA |
| --- | --- | --- | --- | --- | --- | --- | --- |
| **0 · Arranque** (sin dependencias de contratos) | Redacta C1–C10 a partir de `docs/` (I0-05) | Esqueleto, compose, CI (I0-01…03) | Binario `horus` con roles (I0-04) | Simulador de flujos (I0-10) | Cargadores de datasets y feeds (I0-17) | Proyecto Nuxt, login, layout (I0-14) | Responde P-19, P-23…P-26; prepara su MikroTik y una máquina con KVM si puede |
| **Gate G0** | Abre el PR de congelación | | | | | | **Aprueba contratos** (una revisión) |
| **1 · Cimientos** | `make accept-i0` (I0-19) | Laboratorio CHR (I0-11), observabilidad (I0-18) | Modelo tenant, auth, aislamiento, inventario (I0-06…09) | Verificación "a verificar" y fixtures (I0-12), esquema ClickHouse (I0-13) | Motor de detección sobre fixtures (inicio de I1-10) | Selector de ISP, cliente API, marco de widgets (I0-15, I0-16) | Opcional: captura de su router (`make capture`, I0-12) |
| **Cierre I0** | Demo I0 | | | | | | Comprueba `make up` y el alta de su router |
| **2 · Núcleo I1** | Esqueleto de `make accept-i1` (I1-24) | Instalador, backup (I1-22, I1-23) | WireGuard mínimo, script, clientes, WS, kiosco, dashboards (I1-01, I1-02, I1-06, I1-13…15) | Colector, ingesta, descubrimiento, enriquecimiento, tráfico, exportador (I1-03…05, I1-07…09) | Detectores y hallazgos (I1-10…12) | Vistas contra mocks (I1-16…19) | — |
| **3 · Integración I1** | Aceptación completa (I1-24) y laboratorio (I1-25, con FLOW) | Carga y fallos (I1-26) | Correcciones | Rendimiento; apoyo a I1-25 | Ajuste de umbrales con escenarios y laboratorio | Widgets, plantillas y kiosco contra backend real (I1-20, I1-21) | — |
| **Gate G1** | Guía de prueba | | | | | | **Prueba con su MikroTik real** (I1-27) y acepta/rechaza hallazgos |

Camino crítico de I1: **I0-05 (contratos) → I0-12 (hechos de RouterOS) → I0-06 (modelo tenant) →
I1-01 (túnel e identidad del exportador) → I1-03/I1-04 (colector e ingesta) → I1-05/I1-10
(descubrimiento y detección) → I1-20/I1-21 (NOC y kiosco) → I1-25 (laboratorio §8.3) → I1-27
(router real)**. El integrador prioriza siempre las historias de este camino.

## 5. Coordinación: ramas, PRs e integración

### 5.1 Ramas y worktrees

- Un **worktree por agente** y una **rama por historia**: `<agente>/<ID>-<slug>`
  (p. ej. `flow/I1-04-ingesta-clickhouse`). Si [`../conventions.md`](../conventions.md) fija otro
  formato para agentes, prevalece.
- Ramas de vida corta: se abre PR en cuanto hay un primer corte que compila y tiene tests;
  trabajo incompleto detrás de *feature flag* `HORUS_FEATURE_*`.
- Conventional Commits; el título del PR lleva el ID (`feat(ingester): ingesta a ClickHouse [I1-04]`).

### 5.2 Pull requests y revisión

| Tipo de PR | Revisa | Aprueba el merge |
| --- | --- | --- |
| Normal (dentro de su directorio) | Agente revisor asignado (§5.4) | Auto-merge con CI verde + 1 aprobación de agente |
| `contract:*` | Productor + todos los consumidores | **Persona** (en lote) |
| `area:security` (auth, tenants, permisos, secretos, datos de clientes, kiosco) | Agente revisor + CORE | **Persona** (en lote) |
| `docs/` de decisiones (ADR, roadmap) | INT | **Persona** |

La persona revisa los PRs que la necesitan **en lote, como máximo una vez al día**, desde una
consulta guardada de GitHub (`label:needs:persona is:open`). Un PR bloqueado por la persona no
bloquea al agente: sigue con la siguiente historia.

### 5.3 Integración (quién y cómo)

- **INT integra**: mantiene la *merge queue*, ordena los merges del camino crítico y, si `main`
  se pone rojo, revierte el PR culpable de inmediato y abre un issue al autor.
- Smoke e2e en cada merge; `make accept-iN` completo cada noche y antes de cada demo.
- **Alarmas** que INT vigila y publica en el issue de seguimiento del incremento: PR `contract:*`
  sin aprobación > 1 día; agente trabajando contra mocks cuando el backend real ya existe; `main`
  rojo > 1 h; historia del camino crítico sin PR tras abrirse la ola; `needs:persona` acumulados > 5.

### 5.4 Parejas de revisión

| Autor | Revisor por defecto | Por qué |
| --- | --- | --- |
| CORE | SEC | Seguridad de auth/tenants |
| FLOW | SEC | SEC consume sus tablas |
| SEC | FLOW | FLOW conoce el modelo de datos |
| UI | CORE | CORE produce la API que consume |
| PLAT | INT | Visión global de CI y despliegue |
| INT (contratos) | Todos los consumidores | Regla de contratos |

## 6. Reglas para los agentes

1. **Entrada:** la historia (ID), los contratos que consume y los documentos que cita.
   **Salida:** PR con DoD marcada ([`README.md`](README.md) §8), salida de tests y, en UI,
   capturas (incluido el modo kiosco si es un widget).
2. **Alcance:** solo escribe en sus directorios; para `packages/` o `docs/` abre PR de contrato.
3. **Pruebas primero en lo verificable:** cada criterio de aceptación tiene al menos un test que
   falla antes del cambio y pasa después.
4. **Dudas:** si falta una decisión, (a) aplica un supuesto explícito y reversible, (b) lo anota
   en la descripción del PR y en `docs/open-questions/` con la etiqueta `needs:persona`, (c) sigue.
   Nunca bloquea esperando respuesta.
5. **Datos reales:** ningún agente sube capturas, IPs o datos de clientes reales al repositorio;
   los fixtures reales se anonimizan (I0-12) y se guardan fuera de Git si no se pueden anonimizar.
6. **Nada de credenciales** del router o del servidor de la persona en el repo ni en logs.

## 7. Qué hace la persona

| Momento | Acción | Esfuerzo esperado |
| --- | --- | --- |
| Antes de Ola 0 | Responder las preguntas abiertas que bloquean I1 (P-19, P-23…P-26); indicar modelo y versión de RouterOS de su MikroTik y dónde hace NAT | Una sesión |
| Gate G0 | Revisar y aprobar los contratos congelados (C1–C10) en un único PR | Una revisión |
| Ola 1 | Aportar un servidor (o VM) de prueba, idealmente con KVM para el laboratorio CHR; opcional: captura anonimizada de su router con `make capture` (I0-12) | Una sesión |
| Continuo | Aprobar en lote los PRs `contract:*` y `area:security`; responder `needs:persona` | Minutos al día |
| Cierre I0 | Ejecutar `make up` y dar de alta su router | Una sesión |
| Gate G1 | Pegar el script RouterOS en su MikroTik; seguir la guía de prueba de I1 (I1-27); marcar cada hallazgo como confirmado o falso positivo; decidir si I1 está aceptado | Una sesión + uso durante varios días |
| Entre incrementos | Elegir el orden de I2/I3/I4 según lo que haya aprendido usando I1 | Una decisión |
