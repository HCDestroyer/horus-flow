# Informe de carga y de fallo del incremento 1 (I1-26)

Historia I1-26 de [`docs/backlog/increment-1.md`](../../docs/backlog/increment-1.md). Volúmenes de referencia en [`docs/vendors/mikrotik.md`](../../docs/vendors/mikrotik.md) §2.5 (nodo mediano: 2 000–10 000 flujos nuevos/s en pico) y modos de fallo en [`docs/architecture.md`](../../docs/architecture.md) §10 (límites medidos en §10.14).

**Resumen**

- A **5 000 flujos/s durante 5 min**: ninguna pérdida (1 328 592 enviados = recibidos por el collector = filas en ClickHouse), lag máximo de 0 lotes y p95 de la API de tráfico de **121 ms**.
- **Máximo sostenible en la máquina de prueba: 7 500 flujos/s.** A 10 000 flujos/s el lag del ingester crece ~12 s/min y el collector empieza a descartar (cola llena). Cuello de botella: ClickHouse (≈ 170 % de CPU de 4 vCPU), que recibe un INSERT por cada lote de 500 registros.
- **Fallos:** ClickHouse, NATS y `horus-app` parados 30 s con ingesta → **0 flujos perdidos**, todo healthy en 6 s, backlog drenado en 12–20 s y kiosco (WebSocket + datos de widgets) de vuelta solo en 6 s. Collector parado 1 min → se pierde lo enviado durante la caída (~53 s de flujos; UDP sin reintento y sin spool a disco), el hueco es ausencia de filas (la serie de la API da `null`, nunca 0) y el exportador pasa por *Silencioso* y vuelve a *Exportando*.
- **Arreglados por el camino:** el WebSocket del gateway respondía **501** en el binario real y el exportador no pasaba por *Silencioso* (ni recuperaba su estado) tras reiniciar el collector.
- **Límites nuevos:** TLM_FLOWS ocupa **184 B por flujo** (supuesto: 60 B; la autonomía de §9.4 es un tercio); el búfer en memoria del collector ~150 B por flujo (256 MiB ≈ 6 min a 5 000 flujos/s); `flows_raw` **26,6 B por fila** comprimida (supuesto: 20 B).

## Cómo se ejecuta

    make load-i1                               # 5 000 flujos/s durante 5 min (criterio)
    make load-i1 DURATION=1h RAMP=1            # nocturno: 1 h y rampa hasta el máximo sostenible
    make load-i1 RATE=7500 DURATION=3m RAMP=1 RAMP_RATES=10000,12500
    make chaos-i1                              # ClickHouse, NATS y horus-app 30 s; collector 1 min
    make chaos-i1 CHAOS_SCENARIOS=collector CHAOS_COLLECTOR_DOWN=2m

Ambos levantan un compose propio (`tests/load/stack.sh`: proyecto `horus-load`, puertos +23000, volúmenes nuevos, perfil `app`, imagen `horus:load`) y lo destruyen al terminar (`LOAD_KEEP=1` lo conserva, `LOAD_REUSE=1` usa uno levantado). Resultados en `bin/load/results.{md,json}` y `bin/load/chaos.{md,json}`; en el nocturno (jobs `load-i1` y `chaos-i1`) van al resumen del job y como artefacto.

## Máquina usada

| | |
| --- | --- |
| CPU | 4 vCPU Intel Xeon @ 2,80 GHz (máquina virtual) |
| Memoria | 16 GiB, sin swap |
| Sistema | Linux 6.18, Docker 29.8.2 (cgroup v1, overlayfs) |
| Compose | `compose.dev.yaml` + `tests/load/compose.load.yaml` (ClickHouse 2 GiB, horus-app 1 GiB, collector 512 MiB, NATS 512 MiB) |
| Versiones | PostgreSQL 18.6, ClickHouse 26.8.20.9, NATS 2.14.7, Valkey 8.1.10 |

Condiciones que empeoran las cifras: el simulador corre en la misma máquina (compite por CPU y, si se retrasa, envía ráfagas) y otra pila completa de Horus (aceptación de I1) se ejecutaba a la vez; con carga media 7 sobre 4 vCPU una ejecución a 5 000 flujos/s perdió el 1,4 % por desbordamiento del búfer UDP del collector. Son cifras de un servidor pequeño y compartido; el nocturno las repite en `ubuntu-24.04` (4 vCPU, 16 GiB) durante 1 h.

## Qué se mide

- **Pérdida en el collector:** registros enviados (expected.json del simulador) frente a `horus_collector_records_total`, `horus_collector_dropped_total{reason}`, pérdidas por secuencia y `RcvbufErrors` UDP del contenedor.
- **Lag del ingester:** pendientes + sin confirmar del durable `flows-ingester` en TLM_FLOWS cada 5 s (lotes y segundos de flujo), pendiente en la segunda mitad y tiempo de drenaje.
- **Extremo a extremo:** filas nuevas del ISP de prueba en `flows.flows_raw`.
- **API de tráfico:** 2 clientes en bucle (cada 0,5 s) sobre `timeseries` (1 h, 24 h), `top` (clientes 15 min, servicios 1 h) y `attribution`; p95 de las respuestas 200.

Una tasa se sostiene si no hay pérdida, ClickHouse tiene todas las filas, el backlog no supera 30 s de flujo ni crece más de 2 s/min, se drena en < 90 s y el p95 es < 500 ms sin errores. Simulador: `tools/flowsim`, escenario `normal` (250 hogares en 10.20.0.0/24, plantillas reales de RouterOS 7), sin NAT ni IPv6, en tiempo real, enviado a la IP del contenedor del collector (el proxy UDP de Docker descarta con carga); el origen que ve el collector (puerta de enlace de la red del compose) se registra como IP de túnel en el inventario base.

## Resultados de carga (10/10/2026, UTC)

| Hora | Flujos/s | Duración | Enviados | Pérdida | ClickHouse | Lag máx. (lotes / s) | Crecimiento | Drenaje | p95 API | Resultado |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 01:28 | 5 000 | 5 min | 1 328 592 | 0 | 1 328 592 | 0 / 0,0 | 0 | 13 s | 121 ms | OK |
| 01:36 | 7 500 | 3 min | 1 226 463 | 0 | 1 226 463 | 31 / 2,1 | 0,7 s/min | 15 s | 339 ms | OK |
| 01:39 | 10 000 | 3 min | 1 632 313 | 29 777 (1,8 %) | 1 602 536 | 553 / 27,6 | 12,4 s/min | 52 s | 454 ms | KO: cola del collector llena, lag creciente |
| 01:04 | 5 000 | 5 min | 1 328 592 | 19 030 (1,4 %) | 1 309 562 | 5 / 0,5 | 0 | 10 s | 122 ms | KO con contención: 1 754 `RcvbufErrors` |
| 01:10 | 7 500 | 2 min | 834 922 | 0 | 834 922 | 6 / 0,4 | 0 | 11 s | 268 ms | OK |
| 01:12 | 10 000 | 2 min | 1 113 614 | 0 | 1 113 614 | 346 / 17,3 | 11,4 s/min | 37 s | 372 ms | KO: lag creciente |

**Máximo sostenible: 7 500 flujos/s.**

- El cuello es ClickHouse: a 10 000 flujos/s ≈ 170 % de CPU, `horus-app` ≈ 27 %, collector ≈ 9 %. Un INSERT por lote (≤ 500 registros) × 11 vistas materializadas ≈ 20 partes/s.
- Con lotes de 2 000 (`HORUS_COLLECTOR_BATCH_MAX_RECORDS=2000`) el lag queda a 0 a 10 000 y 15 000 flujos/s, pero aquí el collector perdió el 1,1 % y el 4,6 % por búfer UDP (p95 703 ms a 15 000): la palanca es agrupar INSERT en el ingester o `async_insert`.
- Búfer UDP: el collector pide 8 MiB, el kernel lo limita a `net.core.rmem_max` (4 MiB): ≈ 3 s de margen a 5 000 flujos/s con la CPU saturada.
- Tamaños: 184 B por flujo en TLM_FLOWS (≈ 92 KB por lote de 500); `flows_raw` 26,6 B/fila comprimida (213 B sin comprimir).

## Resultados de las pruebas de fallo (01:16–01:26 UTC, 5 000 flujos/s)

| Escenario | Caída | Healthy | Enviados | ClickHouse | Perdidos | Lag máx. | Búfer | Drenaje | Kiosco | Evento | Resultado |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | --- | ---: | ---: | ---: | --- |
| ClickHouse | 30 s | 6 s | 428 804 | 428 804 | 0 | 358 lotes | TLM_FLOWS | 20 s | 6 s | 1 ms | OK |
| NATS | 30 s | 6 s | 428 600 | 428 600 | 0 | 256 lotes | collector 23,5 MB | 12 s | 6 s | 2 ms | OK |
| horus-app | 30 s | 6 s | 428 228 | 428 228 | 0 | 271 lotes | TLM_FLOWS | 14 s | 6 s | 3 ms | OK |
| collector | 1 min | 6 s | 735 315 | 472 544 | 262 771 (52,6 s) | 0 | ninguno | 14 s | — | — | OK |

- Búfer documentado: TLM_FLOWS acotado a 2 GiB en la prueba (≈ 39 min a 5 000 flujos/s) y el búfer en memoria del collector, 256 MiB (≈ 6 min). Sin spool a disco.
- Kiosco enrolado con la playlist NOC (cookie → JWT → ticket → WebSocket en `security` + sondas de `/kiosk/config` y datos de widgets), reconexión cada 2 s como el frontend: vuelve en 6 s y recibe un evento nuevo en ms.
- Collector caído: 0 filas recibidas en el interior de la caída; la serie de la API no tiene ceros (`null`); eventos `silent` → `lossy` + `recovered` → `exporting` (prueba con `HORUS_COLLECTOR_SILENT_AFTER=30s`).

## Fallos encontrados y arreglados

1. WebSocket 501 en el binario real: `statusWriter` de `packages/go/httpx` sin `Hijack`; ahora `Hijack` y `Flush` (test `TestMetricsMiddlewareAllowsWebSocket`).
2. Estado del exportador tras reiniciar el collector: siembra `last_flow_at` desde el KV y registra `silent` → `recovered` (test `TestExporterStateAfterCollectorRestart`).

## Diferencias con producción y pendientes

- Exportador en inventario base; streams creados por los roles (`HORUS_NATS_ENSURE_STREAMS`), donde `natsx.EnsureStreams` deja TLM_FLOWS sin `max_bytes` y el driver lo fija a 2 GiB; claves de auth fijas para sobrevivir al reinicio de `horus-app`.
- FLOW: agrupar INSERT / `async_insert` para superar 7 500 flujos/s en 4 vCPU; spool a disco del collector; dimensionar TLM_FLOWS con 184 B/flujo (con 50 GB, M aguanta 15–30 min en pico).
- PLAT: `net.core.rmem_max` (32 MiB) en el instalador y búfer UDP del collector acorde.
- La API de tráfico tiene granularidad mínima de 5 min: un hueco de 1 min se ve como tramo más bajo; el hueco exacto está en `flows_raw` y en el estado del exportador.
- La hora a 5 000 flujos/s solo corre en el nocturno; localmente se ejecutaron 5 min.

---

# Ronda isp10k (FLOW): un ISP de 10 000 clientes

Objetivo del product owner: aguantar un ISP de 10 000 clientes sin depender de que ClickHouse esté siempre disponible. Referencias: [`docs/vendors/mikrotik.md`](../../docs/vendors/mikrotik.md) §2.5 (nodo grande: 15 000–60 000 registros/s en pico; el router real medido dio ~0,65 flujos/s por cliente, ~6 500/s de media para 10 000 clientes) y [`docs/architecture.md`](../../docs/architecture.md) §9.4, §10.1, §10.14 y §10.15.

**Resumen**

- **Escritura agrupada en el ingester:** un INSERT por cada 50 000 filas o 1 s (antes, uno por lote de 500), ack de JetStream solo tras el INSERT y token de deduplicación por grupo con su composición guardada en un KV (reintentos idempotentes tras una caída). En el banco de ClickHouse (2 vCPU) pasa de **5 500 a 38 000 filas/s** (×7) y, con las vistas de 1 h y 1 d en cascada, a **47 000 filas/s**. `async_insert` con deduplicación se queda en 10 700 filas/s y duplica agregados salvo con `deduplicate_blocks_in_dependent_materialized_views`: descartado.
- **Máximo sostenible en 4 vCPU compartidas: 10 000 flujos/s con todos los criterios** (I1: 7 500 con el escenario `normal` y, sin saberlo, sin agregados por cliente; ver «Fallos encontrados»). A **20 000 flujos/s la ingesta es perfecta** (0 pérdida, lag 2,5 s, drenaje 6 s) y lo que falla es el p95 de la API de tráfico (700 ms) con la máquina a carga 11–15 por otras pilas.
- **ClickHouse caído 5 min a 20 000 flujos/s:** @@CHAOS@@
- **TLM_FLOWS con compresión s2: 185 B por flujo sin comprimir y 48–63 B en disco** (antes 184 B en disco). `max_bytes` cuenta sin comprimir: 100 GB ocupan ≈ 27 GB y aguantan **23 h** de ClickHouse caído a la tasa media de un ISP de 10 000 clientes y **10 h** en pico habitual (15 000/s).
- **Búfer UDP:** el collector pide 32 MiB y avisa si el kernel se lo recorta. Con 4 MiB (rmem_max del host) y el simulador enviando en ráfagas se perdía el 1,4–3 % a 10 000/s; con 32 MiB y envío repartido, 0.

## Cómo se ejecuta

    make load-isp10k                                  # rampa 10 000→20 000→40 000→60 000 (3 min) + ClickHouse caído 5 min a 20 000/s
    make load-isp10k ISP10K_STEP=90s                  # escalones de 90 s (lo usado aquí, por el disco)
    make load-isp10k ISP10K_RATES=40000,60000 ISP10K_SKIP_CHAOS=1

Proyecto `horus-load-isp10k` (puertos +24000), imagen `horus:load-isp10k`, escenario `isp10k` del simulador a tasa fija (`-flat`) con los prefijos 10.64.0.0/18 y 2001:db8:1000::/40 (/64 por cliente) dados de alta por la API, búfer TLM_FLOWS de la prueba de 1,5 GiB y override [`compose.isp10k.yaml`](compose.isp10k.yaml): collector con 32 MiB de búfer UDP mediante `CAP_NET_ADMIN`, porque en la máquina compartida no se puede subir `net.core.rmem_max`. Mismos criterios que load-i1 (sin pérdida, ClickHouse con todas las filas, backlog ≤ 30 s y sin crecer más de 2 s/min, drenaje < 90 s, p95 de la API < 500 ms sin errores). Resultados en `bin/load/results-isp10k.{md,json}` y `bin/load/chaos.{md,json}`.

## Máquina usada (isp10k)

| | |
| --- | --- |
| CPU | 4 vCPU Intel Xeon @ 2,80 GHz (máquina virtual) |
| Memoria | 16 GiB, sin swap |
| Sistema | Linux 6.18, Docker 29.8.2 (cgroup v1, overlayfs) |
| Versiones | ClickHouse 26.8.20.9, NATS 2.14.7, PostgreSQL 18.6, Valkey 8.1.10 |
| Límites | ClickHouse 2 GiB, horus-app 1 GiB, collector 512 MiB, NATS 512 MiB (compose de desarrollo) |

**Contención:** la máquina la compartían la pila de aceptación de INT (al empezar), un contenedor de pruebas del instalador Debian (Docker dentro de Docker, con `docker save` y `gzip`) y otro ClickHouse ajeno: carga media 9–15 sobre 4 vCPU durante la rampa y **disco libre entre 0,05 y 4 GB** (una ejecución murió con PostgreSQL sin espacio). Por eso los escalones son de 90 s; las cifras son un suelo.

## Banco de escritura en ClickHouse

[`bench_integration_test.go`](../../services/ingester/internal/adapters/clickhouse/bench_integration_test.go) (`-tags chbench`): 1 M de filas sintéticas de 10 000 clientes en `flows_raw` con sus vistas, ClickHouse 26.8 limitado a 2 vCPU y sin otra carga en él.

| Modo | Filas/s | CPU de los INSERT | CPU de merges | Partes nuevas | Agregados |
| --- | ---: | ---: | ---: | ---: | --- |
| Lote de 500 (I1, 4 en paralelo) | 5 508 | 15,3 s | 285 s | 9 385 | correctos |
| `async_insert` por lote con dedup (32 en paralelo) | 10 707 | 16,2 s | 170 s | 4 817 | correctos solo con `deduplicate_blocks_in_dependent_materialized_views=1` |
| **Grupo de 50 000 (2 en paralelo)** | **38 091** | 27,1 s | 49 s | 240 | correctos |
| Grupo de 50 000 + vistas en cascada | **46 960** | 24,8 s | 34 s | 240 | correctos |
| Lote de 500 + vistas en cascada | 6 287 | 16,4 s | 206 s | 9 290 | correctos |

Lo que cuesta es el número de partes y sus merges, no las filas: con grupos, la CPU de ClickHouse por millón de filas baja de ~300 s a ~60 s. `async_insert` agrupa en el servidor pero, con deduplicación por token, conserva un bloque por INSERT original y sin la deduplicación en las vistas duplica agregados (comprobado: 14 filas en el agregado para 8 en la tabla).

## Resultados de carga (10/10/2026, 03:01–03:05 UTC)

| Flujos/s | Duración | Enviados | Collector | Pérdida | ClickHouse | Lag máx. (lotes / s) | Drenaje | p95 API | Resultado |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 10 000 | 90 s | 931 836 | 931 836 | 0 | 931 836 | 30 / 1,5 | 2 s | 272 ms | OK |
| 20 000 | 90 s | 1 788 340 | 1 788 340 | 0 | 1 788 340 | 99 / 2,5 | 6 s | 700 ms | KO: p95 de la API (ingesta OK) |
@@RAMP@@

Ejecuciones descartadas (para que no se repitan): con el inventario sin prefijos (fallo de `flowinv`, abajo) el 100 % de las filas eran `unknown` y no se calculaban los agregados por cliente; con el búfer UDP de 4 MiB y el simulador enviando cada segundo en ráfaga, 10 000/s perdía el 1,4–3 % en el socket del collector (`RcvbufErrors`) sin ningún problema en la ingesta.

## ClickHouse caído 5 min a 20 000 flujos/s

@@CHAOSTABLE@@

## Tamaños medidos

| Medida | I1 | isp10k |
| --- | ---: | ---: |
| TLM_FLOWS, B por flujo sin comprimir (lo que cuenta `max_bytes`) | 184 | 185 |
| TLM_FLOWS, B por flujo en disco | 184 | **48–63** (s2; 780 MB lógicos = 215 MB en disco durante el caos) |
| Filas por INSERT en flows_raw | ≤ 500 | hasta 50 000 (≈ tasa × 1 s) |

## Fallos encontrados y arreglados

1. **El inventario de flujos perdía los routers y prefijos de la API al reiniciar** (`packages/go/flowinv`): la proyección de DEVICES_EVENTS vivía en memoria pero usaba un durable, y tras reiniciar horus-app o el collector solo llegaba lo no confirmado. El driver reinicia ambos tras dar de alta el ISP, así que **todas las filas eran `unknown`** (también en I1-26: aquellas cifras se midieron sin agregados por cliente ni señales de seguridad). En producción, cualquier reinicio dejaba sin atribuir los flujos de lo dado de alta por la API. Ahora relee el stream entero con un consumidor efímero (test de reinicio).
2. **El simulador enviaba cada segundo en una ráfaga** (todos los datagramas del tick con la misma marca de tiempo): ahora los reparte a lo largo de su segundo.
3. El ledger de grupos podía bloquear la ingesta si su KV se llenaba: tras 5 intentos inserta sin esa protección y lo cuenta.

@@COMPRESSION@@

## Perfil recomendado y proyección

@@PROFILE@@

## Pendientes (isp10k)

@@PENDING@@
