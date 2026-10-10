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
- **ClickHouse caído 5 min a 20 000 flujos/s: 0 pérdida en la cadena NATS → ingester → ClickHouse** (6 475 865 filas = 6 599 246 enviados − los 12 034 datagramas que el collector descartó por cola llena al volver ClickHouse), backlog máximo de 11 117 lotes (≈ 5,6 M de flujos, 1,03 GB sin comprimir) drenado en 2 min 22 s, `/readyz` del ingester con `tlm_flows_buffer` degradado al pasar del 70 % y kiosco de vuelta en 9 s. El escenario sale KO (−1,9 %) por esa pérdida **en el collector**, no en el búfer: un solo router se decodifica en un solo trabajador y, con la CPU saturada por el drenaje en 4 vCPU compartidas, su cola se llenó (ver «Pendientes»).
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
| 40 000 | 90 s | 3 509 361 | 3 307 827 | 201 534 (5,7 %) | 3 307 827 | 531 / 6,6 | 7 s | 841 ms | KO: cola del collector llena (19 166 datagramas; 0 en el socket UDP), lag creciendo 5 s/min, p95 |
| 60 000 | 90 s | 5 223 877 | — | — | — | 4 763 / ≈ 40 | — | 1 405 ms | KO (interrumpida: disco libre < 200 MB en la máquina compartida) |

**Máximo sostenible: 10 000 flujos/s** con todos los criterios; **20 000 flujos/s de ingesta sin pérdida ni lag**. A 40 000 flujos/s la cadena después del collector sigue sin perder nada (ClickHouse = collector), pero el lag crece (unas 33 000 filas/s es lo que dan ClickHouse e ingester en 4 vCPU compartidas) y el collector descarta por cola llena.

CPU a 40 000 flujos/s (de 400 %, sin CPU libre en la máquina): ClickHouse 227 %, horus-app 54 % (ya con el INSERT columnar; antes 63 % a 10 000/s), collector 27 %. La rampa de 40 000 y 60 000 se ejecutó aparte (`ISP10K_RATES=40000,60000`, 03:29–03:34 UTC) con la imagen que ya incluye el INSERT columnar y los códecs nuevos.

Ejecuciones descartadas (para que no se repitan): con el inventario sin prefijos (fallo de `flowinv`, abajo) el 100 % de las filas eran `unknown` y no se calculaban los agregados por cliente; con el búfer UDP de 4 MiB y el simulador enviando cada segundo en ráfaga, 10 000/s perdía el 1,4–3 % en el socket del collector (`RcvbufErrors`) sin ningún problema en la ingesta.

## ClickHouse caído 5 min a 20 000 flujos/s

`make load-isp10k` (02:56–03:13 UTC, misma máquina y contención), búfer TLM_FLOWS de 1,5 GiB:

| Caída | Vuelta a healthy | Enviados | ClickHouse | Perdidos | Descartes del collector | Lag máx. | TLM_FLOWS máx. | Drenaje | Kiosco | Resultado |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 5 min | 6 s | 6 599 246 | 6 475 865 | 123 381 (6,2 s) | 12 034 datagramas (cola llena) | 11 117 lotes | 1 610 MB (= max_bytes; 215 MB en disco a los 2,5 min) | 2 min 22 s | 9 s | KO por el collector; 0 en la cadena |

- **Dónde se pierde:** 12 034 datagramas × ~10,3 registros (isp10k: 10,6 registros por datagrama de media) ≈ 123 400 registros: toda la pérdida es la cola del collector durante el drenaje, que en 4 vCPU compite por CPU con ClickHouse y el ingester. Es una inferencia: el driver de caos no registraba entonces lo recibido por el collector. Ahora separa «enviados → collector → ClickHouse» y los datagramas perdidos en el socket (`collector_records`, `udp_rcvbuf_errors`, `pipeline_lost_records`), pero no se pudo repetir por falta de disco.
- **Búfer:** el backlog sin confirmar llegó a 5,6 M de flujos (1,03 GB lógicos). TLM_FLOWS alcanzó su `max_bytes` porque conserva los lotes ya confirmados de la rampa, que son los más antiguos y se descartan primero. `/readyz` avisó (`tlm_flows_buffer: TLM_FLOWS buffer at 72% of max_bytes …, 6958 batches pending`) a los 3 min 35 s de caída.
- **Sin duplicados:** las filas en ClickHouse cuadran con lo recibido por el collector. Ni los reintentos de grupos durante la caída (mismo token) ni las reentregas de JetStream (mensajes de un grupo aún pendiente) duplicaron nada.
- **Memoria:** drenando con el INSERT fila a fila, horus-app llegó a 894 MB de 1 GiB (GOMEMLIMIT 900 MiB). El INSERT columnar reduce a menos de la mitad la memoria que hace falta para preparar cada grupo.
- **Repetición (03:38–03:47 UTC):** con el INSERT columnar, los códecs nuevos y la cola del collector a 32 768, el escalón de 10 000/s volvió a dar OK (p95 197 ms) y durante la caída el collector no descartó nada; la ejecución se cortó a mitad del drenaje porque el disco libre de la máquina bajó de 200 MB (el vigilante de disco paró la pila), así que no cuenta.
- **Endurecido después de estas ejecuciones** (sin repetir el caos por falta de disco): durable del ingester con `MaxDeliver` ilimitado (con ClickHouse caído horas, un lote prefetched podía agotar sus 5 entregas y perderse tras reiniciar el ingester), InProgress de los grupos en su propia goroutine y reentregas descartadas por `Nats-Msg-Id` antes de decodificar.

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

## Compresión de ClickHouse (tarea del product owner)

Medido en un ClickHouse 26.8 de banco con **2 M de filas reales del escenario isp10k** (volcado de `flows_raw` de la rampa: 10 000 clientes, 95 % de filas atribuidas), `OPTIMIZE FINAL` y bytes por columna de `system.parts_columns`.

**Columnas caras de `flows_raw` antes (B/fila):**

- `batch_id` 4,8: un UUID aleatorio por lote, con LZ4.
- `flow_start` 2,9; `ts` y `received_at` 2,4: con DoubleDelta, que el orden por cliente penaliza (las segundas diferencias saltan en cada cliente).
- `remote_ip` 2,9, con LZ4.
- `client_port` 2,0: el puerto efímero es incompresible.
- `bytes` 1,9.
- `service_id`, `remote_org_id`, `remote_prefix` y `category_id`, ~1,4–1,6 cada una: UUID e IPv6 con LZ4.
- `remote_asn` 1,1 y `remote_port` 0,8.

Las IPs ya eran `IPv6` nativas, `remote_country` ya era `LowCardinality` y las demás cadenas son `Enum8`. Poner `LowCardinality` en otras columnas cambiaría tipos del contrato C3, así que no se toca.

| Variante de `flows_raw` | B/fila | INSERT de 2 M filas | OPTIMIZE |
| --- | ---: | ---: | ---: |
| I0-13 (LZ4 por defecto, DoubleDelta en tiempos) | 32,1 | 3,4 s | 2,0 s |
| ZSTD(1) en lugar de LZ4, Delta en `flow_start` | 21,1 | 2,7 s | 2,2 s |
| ZSTD(3) en todo | 20,4 | 3,1 s | 2,1 s |
| **ZSTD(3) y tiempos sin DoubleDelta (migración `20261010130000`)** | **17,6** | 2,8 s | 2,1 s |
| Lo mismo con ZSTD(9) (techo) | 16,1 | 4,5 s | 5,9 s |

Con la migración aplicada, el banco de escritura (grupos de 50 000, 2 vCPU) pasa de 45 000 a **49 000 filas/s**, la CPU de los INSERT de 26,4 a 24,3 s y la de los merges de 49 a 45 s por millón de filas: no encarece la ingesta. En la rampa de 40 000/s, `flows_raw` ocupó 17,3 B/fila en disco con los códecs nuevos.

**Recompresión por antigüedad.** `TTL … RECOMPRESS CODEC(ZSTD(n))` cambia el códec por defecto de la parte y **solo afecta a las columnas sin códec explícito** (comprobado). En `flows_raw` ya no aporta: todas sus columnas grandes llevan códec, y ZSTD(9) solo ganaría un 9 % con 3× más CPU en merges. En los agregados, que usan LZ4 por defecto, sí: la migración `20261010140000` recomprime con ZSTD(6) a 1–3 días y mantiene el mismo TTL de borrado.

| Agregado | B/fila con LZ4 | ZSTD(6) | ZSTD(9) |
| --- | ---: | ---: | ---: |
| `customer_5m` / `_1h` / `_1d` | 19,6 / 19,9 / 20,2 | 11,7 / 11,8 / 11,9 | 11,5 / 11,5 / 11,6 |
| `client_security_1m` | 105 | 78,8 | 72,9 |
| `client_security_1h` | 241 | 210 | 209 |
| `client_port_1m` | 18,5 | 11,2 | 10,9 |
| `site_*` | 13 214 | 3 995 | — |

Coste: recomprimir unas 236 000 filas de un agregado con ZSTD(9) tarda ~1 s de CPU (~4 µs/fila; ZSTD(6), menos de la mitad). Para `customer_5m` de un ISP de 10 000 clientes son unos 25 s de CPU al día.

**Antes y después para un ISP de 10 000 clientes** (6 500 flujos/s de media = 562 M filas/día; agregados con el modelo de `docs/database.md` §8.1; detalle en [`docs/storage.md` §5.1](../../docs/storage.md)):

| | Antes | Después |
| --- | ---: | ---: |
| `flows_raw`, B/fila | 32,1 | **17,6** |
| GB/día (crudo + agregados) | 19,5 | **10,9** |
| Disco a 7 días | 137 GB | **76 GB** |
| Disco a 30 días | 145 GB | **81 GB** |
| Disco a 90 días | 165 GB | **94 GB** |

El 26,6 B/fila de I1 se midió con todas las filas `unknown` (sin servicio, organización ni prefijo); con filas atribuidas, el punto de partida real es 32,1.

**Conversaciones por hora.** La tabla (cliente × hora × ASN/prefijo remoto × protocolo × puerto de servicio × dirección) cambia el contrato C3, porque añade una tabla y una vista, así que queda como propuesta en [`docs/database.md` §6.6](../../docs/database.md). En la muestra salieron 413 000 conversaciones por 2 M de flujos en 3 min (20,3 B/fila). Estimado: ~7 GB para 30 días de un ISP de 10 000 clientes, frente a ~300 GB si se alargara el crudo a 30 días.

## Perfil recomendado y proyección

- **Medido aquí (4 vCPU compartidas, carga 9–15):** 10 000 flujos/s con todos los criterios y 20 000 flujos/s sin pérdida ni lag (con la API a 700 ms). ClickHouse más el ingester tocan techo hacia 33 000 filas/s, y el collector de un solo router se satura hacia 35 000 registros/s. En el banco, ClickHouse con 2 vCPU dedicadas escribe 49 000 filas/s con todas las vistas.
- **Proyección para el servidor recomendado (8 vCPU dedicadas, 32 GB, NVMe):** a 40 000 flujos/s ClickHouse usa ~2,3 núcleos y horus-app ~0,6. Con el doble de CPU y sin vecinos, un pico de 60 000/s pediría ~3,5 núcleos para ClickHouse, ~1 para horus-app y ~0,5 para el collector, y aún quedaría margen para las consultas (a 10 000/s la API respondía en 272 ms con la mitad de la CPU ocupada por otras pilas). Para pasar de ~35 000 registros/s por exportador hace falta además decodificar un mismo router en paralelo (pendiente).
- **Perfil para un ISP de 10 000 clientes** (media de 6 500/s, pico habitual de 15 000/s, pico alto de 60 000/s): **un servidor de 8 vCPU, 32 GB de RAM y 500 GB de NVMe**. Se ocupan unos 250 GB con 7 días de crudo: 76–94 GB de ClickHouse, 27 GB de NATS (`max_bytes` de 100 GB), márgenes y backups. Reparto: ClickHouse 12–16 GB; horus-app 2 GB (`GOMEMLIMIT` 1,8 GiB); collector 512 MiB con `HORUS_COLLECTOR_QUEUE_DATAGRAMS=32768`; `net.core.rmem_max=33554432`. Autonomía con ClickHouse caído: 23 h a la tasa media y 10 h en pico habitual.
- **Segundo host o réplica de ClickHouse** cuando se dé alguna de estas condiciones:
  - el pico sostenido pase de ~40 000 flujos/s (varios nodos grandes o más de ~30 000 clientes);
  - el p95 de la API supere 500 ms en hora pico;
  - se quieran 30 días de crudo (más de 300 GB adicionales);
  - los paneles deban seguir funcionando con ClickHouse caído.

  El primer paso es llevar ClickHouse a un host propio (16 vCPU, 64 GB). La réplica (ReplicatedMergeTree + Keeper) solo hace falta para la alta disponibilidad de las consultas: la ingesta ya no pierde datos con ClickHouse caído mientras dure la autonomía de TLM_FLOWS.

## Pendientes (isp10k)

- ~~**Collector: decodificar un mismo router en paralelo.**~~ Hecho en la ronda D23 (abajo). Antes: cada exportador iba a un único trabajador (hash por IP de origen). Un router de 10 000 clientes satura su cola hacia 35 000 registros/s en esta máquina, y esa fue la única pérdida del caos de 5 min. Mientras tanto, `HORUS_COLLECTOR_QUEUE_DATAGRAMS=32768` en nodos grandes (≈ 45 MB de cola por exportador).
- **Repetir la rampa y el caos sin contención y con disco** (≥ 10 GB libres; aquí hubo entre 0,05 y 4 GB): escalones de 3 min, 60 000/s completo y el caos con las métricas nuevas del collector. El sitio natural es el nocturno de CI (4 vCPU dedicadas, `make load-isp10k`).
- ~~**Consumidor `ingester-known-clients`**~~ (arreglado en la ronda D23): también era un durable con estado en memoria, el mismo patrón que el fallo de `flowinv`. Tras un reinicio se reenvían `first_seen` de clientes ya conocidos (acotado por el límite por minuto). Hay que revisarlo.
- **Recompresión de `flows_raw`:** solo se conseguiría con un códec por defecto de servidor (regla `<compression>` de ClickHouse por tamaño de parte), que es configuración de despliegue (PLAT).
- **Conversaciones por hora:** decidir en la próxima versión del contrato C3 ([`docs/database.md` §6.6](../../docs/database.md)).
- ~~**Spool a disco del collector**~~: implementado en la ronda D23 ([`docs/architecture.md` §10.15](../../docs/architecture.md)).

---

# Ronda D23 (FLOW): reinicios bruscos, spool a disco y decodificación multihilo

Decisión del product owner [D23](../../docs/po-decisions.md): tras cualquier reinicio todo debe funcionar al 100 % y el multihilo solo entra si funciona al 100 %. Diseño y tabla de estado en [`docs/architecture.md`](../../docs/architecture.md) §10.15 (spool) y §10.16 (estado tras un reinicio).

**Resumen**

- **Matriz de reinicio brusco (`make chaos-restart-flows`) en verde:** collector, horus-app, NATS y ClickHouse, con `kill -9` y `docker restart`, 3 veces cada uno en momentos aleatorios a 10 000 flujos/s constantes (escenario isp10k). **0 pérdida** salvo lo enviado por UDP con el collector caído (medido aparte: 2,2–2,7 s de flujos por escenario de 3 reinicios), **0 duplicados** en `flows_raw` (por clave de flujo y por clave + `batch_id`), agregados por cliente y por nodo **iguales** a los recalculados desde `flows_raw`, inventario intacto (100 % de filas atribuidas antes y después, 0 descartes de exportador desconocido, 0 `first_seen` repetidos de clientes conocidos, clientes y prefijos sin cambios) y el exportador en *exporting*.
- **Fallos que encontró la matriz, arreglados:** (1) un `kill -9` de horus-app a mitad de un INSERT duplicaba los agregados (ClickHouse había escrito las vistas sin confirmar `flows_raw` y el reintento con el mismo token volvía a empujarlas): ahora las vistas también se deduplican por el token; (2) un salto atrás de la secuencia del exportador (reinicio del router o secuencia restaurada) cegaba la medida de huecos hasta alcanzar la secuencia anterior.
- **Estado tras un reinicio:** el inventario ya no se pierde cuando DEVICES_EVENTS caduca (30 días): instantánea + stream. `ingester-known-clients` tenía el mismo patrón que el fallo de flowinv (reenviaba `first_seen`) y está arreglado. La actividad horaria, los `first_seen` pendientes, las plantillas IPFIX y la secuencia de cada dominio sobreviven a un reinicio.
- **Spool a disco del collector:** segmentos con CRC32C, `fsync` configurable, límite de tamaño que descarta lo más antiguo y lo cuenta, y reenvío en orden con el mismo `Nats-Msg-Id`. En la matriz, con NATS caído (`kill -9` y `docker restart`) se reenviaron 780 y 196 lotes desde el spool sin pérdida ni duplicados.
- **Decodificación multihilo de un mismo router** (`HORUS_COLLECTOR_DECODE_WORKERS`, por defecto una por CPU): equivalencia N=1/N=8 demostrada (mismo multiconjunto y mismo orden de registros por router, mismos huecos y lotes) y test dorado con 1 y 8 hilos. Banco de un solo router (isp10k): de 444 000 a 705 000 registros/s.
- **Caída de ClickHouse 5 min a 20 000/s con la imagen final: 0 pérdida en toda la cadena** (6 599 246 enviados = recibidos = filas), drenaje en 2 min 21 s y kiosco de vuelta en 13 s. Antes se perdía el 1,9 % en la cola del collector al drenar.
- **Máximo sostenible en esta máquina (4 vCPU compartidas):** sigue en **10 000 flujos/s con todos los criterios**, porque el p95 de la API pasa de 500 ms a 20 000/s. La **ingesta aguanta 20 000 flujos/s sin pérdida**. A 40 000/s la cadena después del collector ya no pierde nada (ClickHouse = collector, drenaje en 18 s) y la cola del collector descarta el 0,6 % (antes, el 5,7 %). A 60 000/s descarta el 2,6 %, ClickHouse sigue igual al collector y el backlog drena en 66 s. El límite ya no es un hilo por router: es la CPU compartida entre ClickHouse, el ingester y el collector.

## Máquina usada (D23)

| | |
| --- | --- |
| CPU | 4 vCPU Intel Xeon @ 2,80 GHz (máquina virtual) |
| Memoria | 16 GiB, sin swap |
| Sistema | Linux 6.18, Docker 29.8.2 |
| Versiones | ClickHouse 26.8.20.9, NATS 2.14.7, PostgreSQL 18.6, Valkey 8.1.10 |
| Límites | ClickHouse 2 GiB, horus-app 1 GiB, collector 512 MiB, NATS 512 MiB (1 GiB en load-isp10k desde esta ronda) |

**Contención:** la máquina la compartían otros dos agentes (la matriz de reinicios de CORE, pruebas con ClickHouse efímeros y el instalador de PLAT). La carga media fue de 7 a 10 sobre 4 vCPU durante la rampa y quedaban entre 6 y 14 GB de disco libre. Las cifras son un suelo.

## Matriz de reinicio brusco (10/10/2026, 07:02–07:54 UTC; collector repetido 07:56–08:11 tras el arreglo de la secuencia)

`make chaos-restart-flows` (CHAOS_RATE=10000, CHAOS_REPEAT=3, semillas 4711 y 815): proyecto `horus-chaos-flows`, escenario isp10k a tasa fija y `flowsim -sendlog` (cuándo se envió cada tick y cuántos registros terminan en cada segundo). Con eso se compara lo enviado con `flows_raw` **segundo a segundo de fin de flujo**. `kill -9` es un SIGKILL al proceso principal del contenedor desde el host; lo arranca la política de reinicio de Docker, como tras un fallo real. Resultados en `bin/load/chaos-restart.{md,json}`.

| Escenario | Reinicios (caída → de vuelta) | Enviados | flows_raw | Faltan | De ellos, enviados por UDP con el collector caído | Faltan fuera de esa ventana | Sobran | Duplicados (clave / clave+lote) | Agregados distintos (5m, 1h, 1d, nodo) | Atribuidas antes → después | first_seen repetidos | Exportador | Resultado |
| --- | --- | ---: | ---: | ---: | --- | ---: | ---: | ---: | --- | --- | ---: | --- | --- |
| collector / kill -9 | 0,9 s, 1,1 s, 1,3 s | 3 171 721 | 3 144 234 | 27 487 | 27 487 (2,7 s de flujos; el collector midió 17 827 con su secuencia) | 0 | 0 | 0 / 0 | 0, 0, 0, 0 | 1,000 → 1,000 | 0 | exporting | OK |
| collector / docker restart | 6,0 s, 6,1 s, 5,9 s | 3 168 905 | 3 146 463 | 22 442 | 22 442 (2,2 s; el collector midió 15 318) | 0 | 0 | 0 / 0 | 0, 0, 0, 0 | 1,000 → 1,000 | 0 | exporting | OK |
| horus-app / kill -9 | 7,0 s, 7,2 s, 6,6 s | 3 168 056 | 3 168 056 | 0 | — | 0 | 0 | 0 / 0 | 0, 0, 0, 0 | 1,000 → 1,000 | 0 | exporting | OK |
| horus-app / docker restart | 13,5 s, 13,2 s, 12,9 s | 3 181 569 | 3 181 569 | 0 | — | 0 | 0 | 0 / 0 | 0, 0, 0, 0 | 1,000 → 1,000 | 0 | exporting | OK |
| NATS / kill -9 | 16,0 s, 16,1 s, 17,1 s | 3 171 280 | 3 171 280 | 0 | — | 0 | 0 | 0 / 0 | 0, 0, 0, 0 | 1,000 → 1,000 | 0 | exporting | OK (780 lotes reenviados desde el spool) |
| NATS / docker restart | 6,6 s, 6,6 s, 6,5 s | 3 177 844 | 3 177 844 | 0 | — | 0 | 0 | 0 / 0 | 0, 0, 0, 0 | 1,000 → 1,000 | 0 | exporting | OK (196 lotes desde el spool) |
| ClickHouse / kill -9 | 7,1 s, 7,9 s, 7,2 s | 3 176 872 | 3 176 872 | 0 | — | 0 | 0 | 0 / 0 | 0, 0, 0, 0 | 1,000 → 1,000 | 0 | exporting | OK |
| ClickHouse / docker restart | 15,4 s, 11,8 s, 9,9 s | 3 178 838 | 3 178 838 | 0 | — | 0 | 0 | 0 / 0 | 0, 0, 0, 0 | 1,000 → 1,000 | 0 | exporting | OK |

- **Repetición con la imagen final** (10/10/2026, 09:19–10:10 UTC, semilla 9917, tras mezclar el trabajo de CORE y añadir la pausa por ISP y los eventos de plataforma del spool): **los 8 escenarios en OK** con los mismos criterios. collector / kill -9: faltan 40 114 registros, todos dentro de la caída (el collector midió 44 381 con su secuencia); collector / docker restart: faltan 20 424 (el collector midió 14 035); el resto, 0 faltan y 0 sobran; 0 duplicados y 0 agregados distintos en todos; NATS reenvió 681 y 170 lotes desde el spool.
- **Lo recibido por el collector** se mide ahora (`horus_collector_received_records_total`, sumado entre reinicios). En los escenarios sin caída del collector coincide con lo enviado y con `flows_raw`. Si el collector se reinicia, el contador no ve el último segundo antes de un `kill -9`, así que esa pérdida se mide contra lo enviado.
- **Pérdida con el collector caído:** solo es UDP y solo dentro de la ventana caída → escuchando, más ≤ 1 s de lotes abiertos en memoria con `kill -9`. Ningún registro falta fuera de esa ventana. El collector mide además, con la secuencia guardada, lo que el router envió mientras estaba caído (`horus_collector_downtime_lost_records_total` y `data_gap` con `collector_down`).
- **Pendiente:** con `docker restart` faltan ≈ 0,24 s de flujos por reinicio más de los que mide la secuencia del collector (22 442 frente a 15 318 en 3 reinicios). O lo recibido justo antes del cierre ordenado no llega a `flows_raw`, o la secuencia guardada al parar va por delante. Cae dentro de la ventana de caída (cumple el criterio), pero hay que localizarlo.
- **Primera ejecución (06:03–06:59 UTC, antes de los arreglos):** `horus-app / kill -9` dejó 9 606 grupos de `customer_5m/1h/1d` y 31 de `site_5m` por encima de `flows_raw`, por agregados duplicados en un INSERT interrumpido. Además, la medida de huecos del collector se quedó ciega desde el segundo escenario, porque la secuencia del simulador se reinició. Los dos fallos están arreglados; la tabla de arriba es la ejecución con los arreglos.

## Rampa y caída de ClickHouse 5 min

`make load-isp10k` con escalones de **3 min** (10/10/2026). La rampa se para en el primer escalón que no cumple (20 000/s, por el p95 de la API), así que 40 000 y 60 000/s se ejecutaron aparte con la imagen final.

| Flujos/s | Duración | Enviados | Collector | Pérdida (collector) | ClickHouse | Lag máx. (lotes / s) | Drenaje | p95 API | Resultado |
| ---: | ---: | ---: | ---: | --- | ---: | ---: | ---: | ---: | --- |
| 10 000 | 3 min | 1 766 341 | 1 766 341 | 0 | 1 766 341 | 35 / 1,7 | 2 s | 239 ms | OK (pila nueva, 08:12 UTC) |
| 20 000 | 3 min | 3 407 688 | 3 407 688 | 0 | 3 407 688 | 79 / 2,0 | 5 s | 676 ms | KO: p95 de la API (ingesta OK) |
| 40 000 | 3 min | 6 697 318 | 6 655 956 | 41 362 (0,6 %; 4 648 datagramas por cola llena, 0 en el socket) | 6 655 956 | 923 / 11,5 | 18 s | 3 287 ms | KO: cola del collector, p95 |
| 60 000 | 3 min | 9 979 627 | 9 717 598 | 262 029 (2,6 %; 25 472 datagramas por cola llena, 0 en el socket) | 9 717 598 | 6 131 / 51 | 1 min 6 s | 1 356 ms | KO: cola del collector, lag, p95 |

- **Después del collector no se pierde nada a ninguna tasa** (ClickHouse = collector). Antes, a 40 000/s, ClickHouse se quedaba en 33 000 filas/s y el lag crecía; ahora, con escalones de 3 min, drena en 18 s.
- **Cola del collector:** a 40 000/s descarta el 0,6 % (antes, el 5,7 % en 90 s) y a 60 000/s el 2,6 %, con la CPU de la máquina saturada (carga 7–10 sobre 4 vCPU por ClickHouse, el ingester, el collector y las pilas de otros agentes). En el banco sin contención, un solo router se decodifica a 705 000 registros/s con 2 hilos: el límite aquí es la CPU compartida, no el reparto por router.
- **Con la imagen final sobre la misma pila** (tras 40 000 y 60 000/s, con más de 16 M de filas ya en ClickHouse), 10 000 y 20 000/s ingieren sin pérdida (lag 1,9 s y 3,7 s, drenaje 5 s y 8 s), pero el p95 de la API sube a 2,6 s y 3,7 s: las consultas recorren más datos con la CPU saturada.
- **NATS con 512 MiB moría por OOM a 40 000/s** con backlog en TLM_FLOWS (`kill` del cgroup, visto en `dmesg`). El compose de producción ya le da 1 GiB y la prueba usa ahora lo mismo. Con NATS caído, el collector pasó al spool y el ingester reintentó las confirmaciones sin perder nada, pero esa ejecución no cuenta.

**ClickHouse caído 5 min a 20 000/s (imagen final, 09:08–09:17 UTC):**

| Caída | Vuelta a healthy | Enviados | Collector | ClickHouse | Perdidos | Lag máx. | TLM_FLOWS máx. | Búfer collector máx. | Drenaje | Kiosco | Resultado |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 5 min | 6 s | 6 599 246 | 6 599 246 | 6 599 246 | 0 | 11 344 lotes | 1 610,6 MB (= max_bytes) | 23,8 MB | 2 min 21 s | 13 s | OK |

La ejecución anterior de esta ronda (08:19–08:27 UTC, imagen sin los últimos cambios) dio lo mismo: 0 perdidos y drenaje en 1 min 55 s.

## Equivalencia de la decodificación multihilo

- `TestDecodeWorkersEquivalence` (`services/collector/internal/app`): captura real MikroTik, tres exportadores intercalados con la captura repetida (plantillas reanunciadas, datos antes que su plantilla, secuencias que se reinician) y, con `HORUS_BENCH_PCAP`, 839 293 registros isp10k. Con 1, 2 y 8 hilos salen **los mismos registros en el mismo orden por router** (luego el mismo multiconjunto), los mismos lotes en número y tamaño, `batch_id` en orden de emisión, los mismos huecos (23 saltos y 3 247 registros perdidos en la captura real), recibidos y descartes.
- `make test-flows-golden` con 1 y 8 hilos, idéntico en ambos: 2 588 registros publicados, 23 saltos y 3 247 perdidos, subida/bajada/internos/desconocidos 783/730/1 072/3, 236 clientes con sus bytes y 236 `first_seen`.
- `FuzzPrepareEquivalence` (60 s, 4 M de ejecuciones): `Decoder.Prepare` más los conjuntos de datos decodificados en goroutines en orden inverso más `Finish` dan exactamente lo mismo que `Decode`, incluidos errores, retenidos que caducan y muestreo. `go test -race` en verde.
- Banco (`BenchmarkEngine`, un router isp10k, sin publicar): 443 686 registros/s con 1 hilo, 704 535 con 2 y 671 428 con 4. La etapa en serie (lotes y secuencia) es el siguiente límite, muy por encima de un router real.

## Pendientes (D23)

- **Directorio del spool en producción:** el compose monta `${HORUS_DATA_ROOT}/collector-spool` y el instalador (PLAT) debe crearlo con propietario 65532. Si falta o no se puede escribir, el collector sigue sin spool y `/readyz` marca `spool` degradado.
- **`docker restart` del collector:** ≈ 0,24 s de flujos por reinicio sin explicar del todo (ver arriba).
- **Lotes abiertos en memoria del collector:** un `kill -9` pierde ≤ 1 s de lo recibido, que se mide como parte de la caída. Evitarlo exige un diario de datagramas con lotes deterministas; no está hecho.
- **Cola del collector con la CPU saturada:** a partir de 40 000/s descarta en esta máquina. Hay que repetir la rampa en el servidor recomendado (8 vCPU dedicadas) en el nocturno.
- **Varios ingester sobre el mismo durable:** sigue el supuesto de un único proceso (§10.1).
