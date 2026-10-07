# 0023 — Entrega por incrementos de valor y desarrollo por agentes de IA + 1 persona

- Estado: Aceptada
- Fecha: 2026-10-07
- Decisores: product owner ([D7](../po-decisions.md), [D9](../po-decisions.md)), Agente A
  (arquitectura, ronda 2)
- Modifica: el calendario de 16 sprints de [vision.md](../vision.md) y [roadmap.md](../roadmap.md);
  los supuestos de equipo de [backlog/team.md](../backlog/team.md).

## Contexto

El plan base organizaba el trabajo en 16 sprints fijos para un equipo humano. El PO aclaró que
"sprint" era solo un nombre ([D9](../po-decisions.md)): los agentes deciden cómo producir un
entregable en el menor tiempo posible. Y que el equipo es **agentes de IA + 1 persona** (product
owner/revisor, [D7](../po-decisions.md)). Esto cambia dos cosas: el **orden** (por valor, no por
capas técnicas) y el **control de calidad** (no hay revisores humanos para cada cambio).

## Decisión

### 1. Incrementos en lugar de sprints

- El trabajo se organiza en **incrementos (hitos)** ordenados por valor para el ISP. Cada
  incremento es **desplegable, demostrable con datos reales o grabados de MikroTik y reversible**,
  sin duración fija. El plan detallado y su orden canónico viven en [roadmap.md](../roadmap.md)
  (Agente D); este ADR fija las reglas.
- Se construye primero un **esqueleto caminante vertical** (de router a pantalla) y se engorda,
  en vez de terminar capas completas (todo el plano de administración antes de ver un flujo).
- Orden sugerido desde arquitectura (dependencias técnicas; `roadmap.md` manda):
  1. **Plataforma base**: despliegue de un servidor ([ADR-0025](0025-binario-modular-con-roles.md)),
     CI, tenants, login, usuarios de plataforma y de tenant.
  2. **Nodo conectado**: alta de nodo y router MikroTik, script WireGuard, estado ICMP/SNMP en
     vivo ([ADR-0022](0022-mikrotik-routeros-v7-primer-fabricante.md)).
  3. **Tráfico por IP**: flujos → NATS → ClickHouse ([ADR-0021](0021-clickhouse-desde-el-primer-incremento.md)),
     **clientes descubiertos automáticamente** ([ADR-0018](0018-la-ip-es-el-cliente.md)), consumo
     y top por IP. *Primer entregable útil.*
  4. **Clasificación + reputación**: catálogo IP→ASN→servicio→categoría y feeds de reputación en
     el ingester.
  5. **Detección de botnets + alertas** ([ADR-0024](0024-deteccion-de-botnets-como-objetivo-principal.md)):
     hallazgos por cliente, alertas con notificación.
  6. **Dashboards modulares y modo kiosco/NOC** ([D8](../po-decisions.md)).
  7. **Scoring residencial/comercial**, reportes, archivo y destino remoto SFTP
     ([ADR-0019](0019-almacenamiento-local-y-destino-remoto.md)).
  8. Fabricantes adicionales, destinos de nube, HA.
- Los incrementos 4–6 pueden solaparse si los contratos están cerrados (los agentes trabajan en
  paralelo por módulo).

### 2. Desarrollo por agentes con calidad verificable por máquina

Como no hay un equipo humano que revise cada cambio, **todo criterio de calidad que pueda
automatizarse se automatiza y bloquea el merge**:

| Puerta automática (CI) | Qué protege |
| --- | --- |
| Contratos primero: OpenAPI, Protobuf, esquemas de eventos y de tablas ClickHouse publicadas en el repo, con código generado y **tests de compatibilidad** (breaking change = fallo) | Que agentes en paralelo no rompan a otros módulos |
| **Tests de arquitectura**: reglas de importación entre módulos (solo paquetes públicos de contrato), dominio sin adaptadores concretos, toda tabla con `tenant_id` tiene RLS, ninguna consulta ClickHouse sin `tenant_id` | Fronteras de [ADR-0025](0025-binario-modular-con-roles.md) y aislamiento de [ADR-0017](0017-multi-tenant-desde-v1.md) |
| **Tests de aislamiento de tenant** generados por endpoint (usuario de A pide recurso de B → 404) | Fugas entre ISP |
| Integración con contenedores efímeros (PostgreSQL, ClickHouse, NATS, Valkey) y **replay de fixtures MikroTik** (pcap IPFIX/v9, snmpwalk, JSON REST) | Comportamiento real sin hardware |
| Migraciones reversibles comprobadas (up → down → up) | Despliegues reversibles |
| Seguridad: análisis estático, vulnerabilidades de dependencias, escaneo de imágenes y de secretos | Riesgos que nadie revisa a mano |
| Presupuestos de rendimiento del camino caliente (flujos/s del ingester, p95 REST) | Regresiones de escala |
| Smoke E2E del despliegue de un servidor + capturas de la UI | El incremento arranca y se ve |

- **Revisión humana (la persona)** solo donde aporta más: aceptación de cada incremento (demo),
  ADRs, y cambios en áreas sensibles marcadas por `CODEOWNERS` (auth, RLS/aislamiento,
  criptografía y secretos, aprovisionamiento de routers, retención de datos personales).
- Un agente revisor distinto del autor revisa cada PR contra la Definición de Terminado
  ([conventions.md](../conventions.md)) antes de la puerta humana, cuando aplique.
- Trabajo en paralelo por **módulo con dueño único por incremento**; los cambios de contrato van
  en un PR propio que se fusiona antes que sus consumidores.
- La documentación es la memoria del equipo: cada decisión que cumpla los criterios de
  [ADR-0001](0001-registrar-decisiones-con-adr.md) va en un ADR en el mismo PR.

### 3. Consecuencia arquitectónica

Con una sola persona operando, **la simplicidad operativa es un requisito**: menos procesos,
un solo servidor al inicio, una herramienta por problema. Por eso se decide además el
despliegue como **binario modular con roles** en [ADR-0025](0025-binario-modular-con-roles.md).

## Alternativas consideradas

- **Mantener 16 sprints fijos**: calendario artificial para agentes; retrasa el primer valor
  (flujos en el Sprint 6–7).
- **Kanban continuo sin hitos**: flexible, pero sin puntos de aceptación claros para la persona.
- **Revisión humana de todo PR**: la persona se convierte en el cuello de botella.

## Consecuencias

- (+) Primer entregable útil (tráfico por IP de un nodo MikroTik) en el tercer incremento.
- (+) Calidad sostenida sin equipo humano gracias a puertas automáticas que bloquean.
- (−) Gran inversión inicial en CI, fixtures y tests de arquitectura (incremento 1).
- (−) Las métricas de avance pasan de "sprint completado" a "incremento aceptado".
- Impacto: [roadmap.md](../roadmap.md) y [backlog/](../backlog/README.md) (reescritura en
  incrementos), [conventions.md](../conventions.md) (Definición de Terminado verificable, CI,
  `CODEOWNERS`), [backlog/team.md](../backlog/team.md).
