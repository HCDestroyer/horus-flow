# 0015 — Enriquecimiento de flujos en la ingesta con snapshot del catálogo

- Estado: Propuesta (recomendada)
- Fecha: 2026-10-07

## Contexto

`vision.md` propone `Router → Flow Collector → NATS → ClickHouse` y luego
`Flow → Traffic Intelligence → Categories`. La clasificación `IP → Prefix → ASN → Organization →
Service → Category` debe aplicarse a decenas o cientos de miles de flujos/s y ser actualizable sin
redeploy. Hay tres lugares posibles para clasificar: en un servicio intermedio, en la ingesta, o en
consulta (diccionarios de ClickHouse).

## Decisión

- `traffic-intelligence` es **dueño del catálogo** (PostgreSQL) y lo **compila** a un snapshot
  binario versionado (trie de prefijos IPv4/IPv6 → ASN/org/servicio/categoría) que publica en
  MinIO con checksum, anunciándolo con el evento `traffic.catalog.published`.
- El **rol `ingester` de `flows`** carga el snapshot en memoria (doble buffer, cambio atómico),
  enriquece cada flujo en el momento de la inserción y **guarda en ClickHouse la clasificación y
  la `catalog_version`** usadas. Igual con el mapa IP→cliente/router proyectado desde `devices`.
- Si no hay snapshot disponible (MinIO caído en el arranque), el ingester usa la última copia en
  disco local; si no tiene ninguna, inserta con clasificación `unknown` y `catalog_version = 0`.
- Reclasificación histórica (cuando cambia el catálogo): no se reescriben datos raw; los
  agregados afectados pueden recalcularse con un job explícito en `analytics`. Para consultas
  "con el catálogo actual" sobre raw, se ofrece además un **diccionario ClickHouse** (`ip_trie`)
  cargado desde el mismo snapshot.

## Alternativas consideradas

- **Servicio intermedio** (collector → NATS → traffic-intelligence → NATS → ingester): duplica el
  tráfico NATS (a 100 routers ~18 MB/s → 36 MB/s) y añade latencia y un punto de fallo en el
  camino caliente.
- **Solo en consulta (diccionarios ClickHouse)**: siempre con el catálogo más reciente y sin
  reescrituras, pero clasifica en cada consulta (más CPU) y no permite agregados por
  servicio/categoría en vistas materializadas, que son la base de la retención larga.
- **gRPC a traffic-intelligence por flujo o por lote**: acoplamiento síncrono en el camino
  caliente; descartado por P3.

## Consecuencias

- (+) Un solo salto NATS; clasificación en memoria a millones de búsquedas/s por núcleo.
- (+) Agregados por servicio/categoría posibles en vistas materializadas.
- (+) La clasificación histórica es reproducible (se sabe con qué versión se clasificó).
- (−) La clasificación queda "congelada" con la versión del momento; cambiar el catálogo no
  corrige el pasado sin re-agregar.
- (−) El ingester depende de un formato de snapshot compartido: el formato es un contrato
  versionado en `packages/` (propiedad de traffic-intelligence).
