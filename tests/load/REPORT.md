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
