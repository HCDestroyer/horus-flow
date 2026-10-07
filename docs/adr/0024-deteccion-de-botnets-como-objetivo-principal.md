# 0024 — Propósito de seguridad: detección de clientes en botnets como objetivo de primer nivel

- Estado: Aceptada
- Fecha: 2026-10-07
- Decisores: product owner ([D5](../po-decisions.md)), Agente A (arquitectura, ronda 2)
- Modifica: la prioridad de `detection` en [services.md](../services.md) y
  [roadmap.md](../roadmap.md) (antes Sprint 8/10, tras analítica).

## Contexto

El PO declaró que el uso es **exclusivamente empresarial, para mitigar que los clientes entren en
botnets** ([D5](../po-decisions.md)). El propósito principal del producto pasa a ser la
**seguridad de red del ISP**: identificar IPs de clientes infectadas o participando en botnets
(bots de DDoS, spam, escaneo, proxies residenciales, mineros) para que el ISP actúe (avisar al
cliente, revisar el equipo). El uso comercial (consumo, residencial vs comercial) se mantiene pero
pasa a segundo plano. La privacidad sigue aplicando: minimización, retención limitada, acceso por
rol.

## Decisión

### 1. Prioridad y forma

- `detection` es un módulo **de primer nivel**: entra en el incremento inmediatamente posterior a
  la clasificación ([ADR-0023](0023-entrega-por-incrementos-y-equipo-ia.md)), antes que los
  reportes y el scoring comercial. `alerts` entra con él (los hallazgos sin notificación no
  sirven al NOC).
- Sale de `detection` un **hallazgo por cliente** (`finding`: tenant, realm, IP de cliente, tipo,
  severidad, confianza, evidencias, ventana temporal, estado `open/acknowledged/resolved/false_positive`).
  **Nunca** "IP en lista = infectado": un hallazgo requiere correlación de señales o una señal de
  alta confianza (p. ej. contacto repetido con un C2 confirmado).

### 2. Señales (solo con datos de flujo; sin inspección de paquetes)

| Señal | Indicador en flujos | Dónde se calcula |
| --- | --- | --- |
| Contacto con C2 / infraestructura maliciosa | destino en feed de reputación (C2 de botnets, listas de bloqueo) | **En la ingesta**: el ingester marca `reputation_hit` con un snapshot de reputación (como el catálogo, [ADR-0015](0015-enriquecimiento-de-flujos-en-ingesta.md)) |
| Escaneo saliente | muchos destinos distintos a puertos típicos (23/2323, 22, 445, 3389, 5555…) en ventana corta, baja tasa de respuesta | Consultas periódicas sobre agregados de 1–5 min |
| Participación en DDoS | ráfagas de pps/bps hacia un único destino, UDP a puertos de amplificación | Ventanas de 1 min |
| Envío de spam | conexiones salientes a TCP/25 de muchos destinos desde IP residencial | Agregados |
| *Beaconing* | conexiones periódicas de bajo volumen al mismo destino | Job horario |
| Proxy residencial / minería | patrones de conexiones entrantes anómalas, pools de minería conocidos (feed) | Agregados + reputación |

- Las reglas son **versionadas y explicables** (cada hallazgo guarda la versión de la regla y las
  evidencias), con umbrales por tenant.
- Requisitos que esto impone al resto:
  - El ingester carga **dos snapshots**: catálogo (traffic-intelligence) y **reputación**
    (detection, módulo `reputation`), ambos desde NATS Object Store
    ([ADR-0019](0019-almacenamiento-local-y-destino-remoto.md)). Se cumple así el criterio de
    [ADR-0014](0014-granularidad-de-microservicios-en-el-mvp.md) "flows necesita reputación en el
    camino caliente" **sin** separar `reputation` en un proceso: basta un snapshot.
  - ClickHouse necesita agregados de **1 minuto** por (tenant, realm, IP de cliente, puerto/proto
    destino) con retención corta (p. ej. 7 días) para escaneo/DDoS, además de los de 5 min/1 h/1 día
    ([database.md](../database.md)).
  - Flujos raw retenidos lo suficiente para investigar un hallazgo (≥ 7 días), con acceso
    restringido por permiso.

### 3. Tenancy y privacidad

- Feeds de reputación: **de plataforma** (compartidos). Hallazgos: **por tenant**.
- Correlación entre ISP (el mismo C2 contactado por clientes de varios ISP) solo como indicador
  agregado de plataforma (IP del C2, número de tenants afectados), **sin** exponer clientes de un
  tenant a otro. Fuera de v1.
- Permisos separados: `security.findings.read` (lista de hallazgos), `security.evidence.read`
  (flujos de evidencia de un cliente, auditado). Detalle en [security.md](../security.md).
- Licencias de feeds: solo fuentes con licencia compatible con uso comercial multi-ISP; a validar
  por fuente ([data.md](../open-questions/data.md)).

### 4. Fuera de alcance v1

Acciones automáticas sobre la red (bloquear, *walled garden*, limitar al cliente desde el router).
Horus detecta y notifica; el ISP actúa. Requeriría credenciales de escritura en los routers
([ADR-0022](0022-mikrotik-routeros-v7-primer-fabricante.md); [Q23](../open-questions/architecture.md#q23)).

## Alternativas consideradas

- **Mantener detection tras analytics y reportes**: retrasa el propósito declarado del producto.
- **IDS con inspección de paquetes (Suricata/Zeek)**: mucha más precisión, pero requiere copia de
  tráfico (port mirror) en cada nodo, hardware y un tratamiento de datos mucho más invasivo; fuera
  del modelo de flujos.
- **Separar `reputation` como servicio**: innecesario; el snapshot en el ingester resuelve el
  camino caliente.
- **Hallazgo = coincidencia con lista**: demasiados falsos positivos (CDN compartidas, IPs
  recicladas).

## Consecuencias

- (+) El producto entrega su propósito principal pronto y con explicación verificable.
- (+) Reutiliza el patrón de snapshot en la ingesta; sin nuevos procesos.
- (−) Más carga en ClickHouse (agregados de 1 min y consultas periódicas por tenant).
- (−) Calidad dependiente de feeds y umbrales; requiere ciclo de ajuste con datos reales y gestión
  de falsos positivos en la UI.
- Impacto: [database.md](../database.md) / [traffic-model.md](../traffic-model.md) (agregados de
  1 min, `reputation_hit`), [events.md](../events.md) (`detection.finding.*`,
  `detection.reputation.snapshot_published`), [security.md](../security.md) (permisos de
  evidencia, base legal por país), [roadmap.md](../roadmap.md), [frontend.md](../frontend.md)
  (vista de hallazgos y widget de kiosco).
