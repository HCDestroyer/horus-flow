# Capacidad de enlaces: método del pronóstico y del análisis

> Estado: **implementado** (rol `snmp`, I3). Código: `services/snmp/internal/domain/forecast.go` (pronóstico),
> `capstats.go` (pico diario, lleno vs averiado), `sitehealth.go` (salud del nodo), `trafficsplit.go`
> (local vs internacional), `netwatch.go` (sondas). API: [`api.md`](api.md) §2.5 bis; eventos: [`events.md`](events.md)
> §8.4; tablas: [`database.md`](database.md) §3 y §6.3; pantalla: [`frontend.md`](frontend.md) §8.6.

## 1. Qué responde

Para el NOC de un ISP: ¿qué enlace de subida se llena, pierde paquetes o tiene latencia; qué nodo está mal y por
qué; y **cuándo** habrá que ampliar un enlace o la salida internacional, con cuánto crecimiento?

## 2. Fuentes

| Señal | Fuente | Por qué |
| --- | --- | --- |
| Tráfico, utilización, errores, descartes | SNMP IF-MIB (contadores HC) que ya sondea el rol cada 60 s → `snmp.interface_5m` | Ya existe; medias de 5 min por interfaz. |
| Pérdida, latencia, jitter **por enlace** | Sondas ICMP de `/tool netwatch` **del propio router** hacia la puerta de enlace del proveedor de cada enlace, **leídas** cada minuto por la API REST de solo lectura (usuario `horus-ro`, solo GET, huella TLS ya fijada) | Es la única medida por enlace sin escribir en el router: la sonda sale por ese enlace. MIKROTIK-MIB solo publica el estado up/down de netwatch, no pérdida ni RTT. El script lo genera Horus y lo **pega el usuario**. |
| Referencia internacional del router | Sondas netwatch a 1.1.1.1 y 8.8.8.8 | Salida del router por su ruta por defecto (no un enlace concreto). |
| Respaldo sin sondas en el router | ICMP desde el host de Horus a destinos configurados | Etiquetado **«ruta Horus → destino»**: mide el camino Horus → destino, nunca un enlace del ISP. |
| Local vs internacional | `flows.site_1d.remote_asn` → `dim.asn` → `dim.organization.country`; prefijos: `flows.site_remote_net_1d` | Local si la IP remota está en un **prefijo** de la lista local del ISP (LAN de un IXP, cachés on-net; manda sobre el ASN), si el ASN está en la lista local o si su organización es del país del ISP. |

Horus **nunca escribe en routers**: todo lo que se configura en el router (sondas) lo pega el usuario.

## 3. Pico diario y diagnóstico

- **Capacidad** de un enlace = capacidad contratada configurada o, si no hay, velocidad del puerto (`ifHighSpeed`).
- **Capacidad con vigencia** (`snmp.uplink_capacity_history`): cada cambio de `contracted_bps` cierra la vigencia
  anterior y abre otra desde `capacity_valid_from` (por defecto, el momento del cambio; hasta 90 días atrás y nunca
  antes del cambio anterior). Cada intervalo de 5 min se compara con la capacidad **vigente entonces** (horas sobre
  umbral), cada día guarda la vigente al final del día (`uplink_daily.capacity_bps`) y el p95 de una ventana es el peor
  día contra su propia capacidad: un upgrade no rebaja la utilización pasada. Antes de la primera vigencia rige la
  primera (la capacidad ya existía antes de configurarla). Un cambio fechado en el pasado se corrige solo: el trabajo
  diario detecta los días guardados con otra capacidad y los recalcula (`ReplacingMergeTree`), solo esos días.
- **Pico diario** = percentil 95 (interpolación lineal) de las medias de 5 min del día **local** del ISP, por sentido;
  el pico del enlace es el mayor de los dos sentidos. Descarta ráfagas sueltas (≈ 72 min/día por encima).
- **Horas sobre umbral** = intervalos de 5 min con `max(rx, tx) / capacidad` ≥ 70 % (aviso) y ≥ 85 % (crítico).
- **Lleno vs averiado**: con ≥ 100 000 paquetes en la ventana se calculan `errores / paquetes` y
  `descartes / paquetes`.
  - p95 ≥ crítico o ≥ 1 h/día sobre el crítico → **lleno** (`full`); los descartes de cola son esperables.
  - Errores ≥ 0,01 % (CRC, alineación) → **averiado** (`faulty`), o **lleno y con errores** si además está lleno.
  - Descartes ≥ 0,1 % con el p95 bajo el aviso → **averiado** (ráfagas, política o negociación, no capacidad).
  - p95 ≥ aviso → `busy`; si no, `ok`.
- La **salida internacional** suma, intervalo a intervalo, los enlaces marcados `international_egress`; su p95 es el
  del conjunto (los picos de enlaces distintos no siempre coinciden).

El trabajo diario (por ISP, a partir de las 00:20 locales) escribe un día por sujeto en `snmp.uplink_daily`; la
primera vez rellena hasta 89 días (lo que guarda `interface_5m`). La vista calcula «hoy» al vuelo.

## 4. Pronóstico (Go puro, sin dependencias)

1. Serie: picos diarios de los últimos **8–26 semanas** (`ForecastWindowDays` = 182), sin días sin dato.
2. `y = ln(pico)`: el tráfico crece de forma multiplicativa; la pendiente es una tasa constante.
3. **Theil–Sen**: pendiente = mediana de las pendientes entre todos los pares de días; ordenada = mediana de
   `y − b·t`. Robusto a días atípicos (cortes, eventos) hasta ~29 % de la serie.
4. **Estacionalidad semanal** (con ≥ 21 días): mediana de los residuos por día de la semana, centrada; se resta y se
   recalcula Theil–Sen. Para proyectar se suma el desfase del día **más cargado** (es el que cruza primero).
5. Intervalo de la pendiente: el de Sen (rangos de las pendientes por pares con z = 1,96). Ruido residual:
   σ = 1,4826 · MAD. Banda de la proyección = pendientes extremas ± 1,645 σ (≈ 90 %).
6. Crecimiento mensual = `e^(30,44·b) − 1`; pico a 6 meses = `e^(a + 182·b + s_max)`.
7. Fecha de cruce de θ·capacidad, con la capacidad **vigente hoy** (la serie son bits/s, no utilización: la historia
   de capacidad no la distorsiona) (θ = 80 % planificación, 90 % urgente, configurables):
   `t = (ln(θ·C) − a − s_max) / b`. Si el pico actual ya lo supera → «ya supera»; sin crecimiento o más allá de
   36 meses → no cruza.
8. **Nivel**: `urgent` si el urgente cruza dentro de la antelación (90 días por defecto); `planning` si lo hace el de
   planificación; `ok` en otro caso. Con < 14 días no hay tendencia; con < 28 días solo la tendencia («datos
   insuficientes para fecha estimada»); sin capacidad conocida, `no_capacity`.
9. **Confianza**: alta con ≥ 12 semanas y el intervalo de la pendiente por encima de 0; media con ≥ 8 semanas; baja
   en otro caso. Desde ~3 meses la estacionalidad semanal ya está bien estimada.

Recomendación (ejemplo): «Planificar upgrade de salida internacional: pico 780 Mb/s, en 6 meses ~1,0 Gb/s (+28 %),
cruza el 80 % hacia 03/2027.»

Validado con series sintéticas de crecimiento conocido (+3 %, +8 %, −2 % mensual) con fin de semana +15 %, ruido
±6 % y dos días atípicos: el crecimiento recuperado queda a ±0,8 puntos y dentro de su intervalo
(`TestForecastRecoversGrowthWithSeasonalityAndNoise`); la fecha de cruce de una serie exponencial exacta, a ±1,5 días.

## 5. Condiciones sostenidas y alertas

El monitor (cada ciclo del sondeador) evalúa por enlace la saturación (última muestra SNMP), la pérdida y la latencia
(sonda de su puerta de enlace) con **histéresis** (5 puntos de saturación; la mitad del aviso de pérdida; 10 % del aviso
de latencia) y **tiempo sostenido** (15 min para empeorar, la mitad —mínimo 5 min— para mejorar; un hueco de datos
> 5 min reinicia la racha). Solo un cambio de nivel emite `horus.snmp.uplink.condition_changed` (outbox, misma
transacción que `snmp.uplink_condition`): deduplicado en origen y con recuperación. El trabajo diario emite
`horus.snmp.capacity.forecast_changed` al entrar o salir de `planning`/`urgent`. `alerts` los publica en la categoría
`capacity`.

El reporte semanal (rol `reporting`) incluye la sección «Capacidad de enlaces» con estos mismos datos para su periodo
(p95, horas sobre 70 / 85 %, pérdida, latencia, pronóstico, recomendación y reparto local / internacional), por el
contrato estrecho `snmp.CapacityReport`.

## 6. Salud del nodo

Un estado por nodo (vocabulario de equipos) con razones legibles, la más grave primero: todos los enlaces caídos →
`offline`; saturación ≥ crítico, pérdida ≥ crítico, servicio caído o router caído/crítico → `critical`; enlace caído,
pérdida ≥ aviso, latencia ≥ crítico, errores de interfaz, router o servicio degradados → `degraded`; p95 ≥ aviso,
latencia ≥ aviso, router en aviso → `warning`. Ejemplo: «Degradado: enlace Proveedor A al 92 % en hora pico y 2,0 %
de pérdida».

## 7. Límites conocidos

- La lista local por prefijos se aplica sobre `flows.site_remote_net_1d` (red remota truncada a /24 en IPv4 y /48 en
  IPv6, 90 días): un prefijo más específico se cuenta por su /24 o /48 completo (la consola lo avisa) y los días
  anteriores a la migración (más allá de los 7 días que se rellenan desde `flows_raw`) solo se clasifican por ASN. La
  consulta compara `remote_net` con los extremos de cada prefijo (`arrayExists` sobre IPv6): cambiar la lista corrige
  también los días ya guardados. Se avisa (sin impedir guardar) si un prefijo se solapa con prefijos de clientes.
- `flows.site_1d` usa días UTC (el reparto local/internacional no se ajusta a la zona del ISP).
- Las estadísticas de netwatch son las de la última prueba del router (10 ecos cada 30 s), leídas cada minuto.
- Un único rol `snmp` activo por instalación (como el sondeador): el trabajo diario no tiene bloqueo distribuido;
  repetirlo es idempotente (ReplacingMergeTree y mismo nivel → sin eventos).
- Un cambio de capacidad a media jornada se aplica por intervalo de 5 min a las horas sobre umbral, pero el p95 del día
  se compara con la capacidad vigente al final del día.
