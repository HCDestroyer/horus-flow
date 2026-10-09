# Captura IPFIX real de MikroTik (anonimizada) — I0-12

Fixture de un router **real**: sirve para comprobar que el colector, el ingester y `sim-verify`
decodifican y atribuyen lo que exporta RouterOS 7 de verdad, no solo lo que imita el simulador.

| Fichero | Contenido |
|---|---|
| `ipfix-nat-20s.pcapng` | 20 s (19,8 s) de IPFIX, **anonimizado**: 246 datagramas, 2 596 registros IPv4, 348 516 bytes |
| `ipfix-nat-20s.expected.json` | Lo que debe dar `sim-verify` (esquema `horus.flowsim.expected/v1`) |
| `.gitignore` | Excepción a `*.pcapng` del `.gitignore` raíz **solo para capturas anonimizadas** |

## Origen

- Router principal de un nodo del PO, **RouterOS 7**, con **NAT en el mismo router**;
  `/ip traffic-flow` con IPFIX, `active-flow-timeout=1m` y los campos NAT activados.
- Captura de 74 s tomada el **2026-10-09** (04:36:30–04:37:44 UTC) con el sniffer en la interfaz de
  salida hacia el colector (puerto UDP 4739). Análisis completo en
  [`docs/traffic-model.md` §4.4.3](../../../docs/traffic-model.md) y
  [`docs/vendors/mikrotik.md`](../../../docs/vendors/mikrotik.md).
- Aquí van los primeros 20 s (la captura completa anonimizada pesa ~1,3 MB, por encima del límite de
  ~600 KB para fixtures). El recorte empieza en un datagrama con plantillas, así que se decodifica
  desde el primer paquete.
- **La captura original no está en el repositorio ni debe estarlo**: contiene IPs y MAC reales de
  clientes.

## Qué se anonimizó y cómo

Con [`tools/flowsim/cmd/ipfix-anonymize`](../../../tools/flowsim/cmd/ipfix-anonymize):

```sh
go run ./tools/flowsim/cmd/ipfix-anonymize -in cap.pcapng -out ipfix-nat-20s.pcapng \
  -key-file <clave fuera del repo> -duration 20s
```

| Dato | Tratamiento |
|---|---|
| IPs privadas (RFC 1918, CGNAT) | → `10.20.0.0/16`: cada /24 original va a un /24 distinto elegido por HMAC-SHA256 (nunca uno presente en el original) y se conserva el último octeto. 369 IPs en 103 /24. |
| IPs públicas del NAT (`postNATSourceIPv4Address` de subidas con src privada) | → `192.0.2.10` y `192.0.2.11`, por frecuencia. La captura tiene **2** IPs públicas de NAT (más una dirección privada de un srcnat entre redes internas, que sigue la regla de las privadas). |
| Exportador (IP privada del router en el original) | → `10.255.3.17`, en la cabecera IP y en los registros. |
| Colector | Es una IP de la red de clientes (el portátil que capturaba): se trata como cualquier privada. Por eso `collector_ip` está vacío en `expected.json`. |
| Resto de IPv4 públicas | → HMAC a `192.0.2.0/24`, `198.51.100.0/24` y `203.0.113.0/24` (760 direcciones, sin las originales ni las del NAT). **No es inyectivo**: 1 074 IPs públicas comparten esas 760 direcciones (solo afecta a remotos, nunca a clientes). |
| IPv6 | → `2001:db8::/32` conservando la estructura de /64 (no hay IPv6 en esta ventana). |
| MAC (Ethernet y IE 56/57/80/81) | → MAC unicast administradas localmente por HMAC, consistentes. 133 MAC. |
| Conservado | Puertos, contadores, tiempos (`systemInitTimeMilliseconds`, `flowStart/EndSysUpTime`), plantillas 258/259, secuencias, dominio de observación, `0.0.0.0`, `224.0.0.1`, `255.255.255.255`. |
| pcapng | Bloques reescritos sin opciones del original (sistema, hardware, nombres de interfaz); interfaz `ipfix-export`. |

El mapeo es determinista para una clave HMAC y consistente en todo el fichero. La clave es
**aleatoria y no se ha guardado**: sin ella el mapeo no se puede reproducir ni invertir.

### Verificación de la anonimización

Se comparó el conjunto de IPs y MAC del original (cabeceras + todos los campos de dirección de los
registros) con el del fixture, con dos decodificadores independientes (`ipfix-anonymize -check`, Go,
y un script Python del análisis del PO):

| Comprobación | Resultado |
|---|---|
| IPs identificativas del original presentes como dirección en el fixture | **0** de 1 448 |
| MAC identificativas del original presentes | **0** de 133 |
| MAC originales como secuencia de 6 bytes en cualquier posición | 0 |
| IPv4 originales como secuencia de 4 bytes en cualquier posición | 1 coincidencia casual dentro de `octetDeltaCount` (contadores intactos por diseño) |
| Registros, subidas con NAT y bajadas con NAT en la captura completa (original vs anonimizada, Python) | 11 253 / 3 277 / 3 211 en ambas |

## Qué debe dar el verificador

`make sim-verify` (o `go run ./tools/flowsim/cmd/sim-verify -in ipfix-nat-20s.pcapng -expected
ipfix-nat-20s.expected.json`) atribuye con la regla de `docs/traffic-model.md` §4.4 y prefijo de
clientes `10.20.0.0/16`:

| Regla (`by_rule`) | Registros | Comentario |
|---|---|---|
| `upload_src` (src en prefijo) | 783 | 726 con `postNATSrc` pública (subida con NAT) |
| `download_post_nat_dst` (IE 226 en prefijo, dst = IP pública del NAT) | **728** | todas con dst ∈ `nat_ips` |
| `download_dst` (dst en prefijo, sin NAT) | 2 | |
| `internal` (src y dst/IE 226 en prefijos) | 1 072 | tráfico entre redes privadas del nodo |
| estado `tunnel` (exportador) / `unknown` | 8 / 3 | |

236 clientes IPv4; plantillas 258 (37 campos) y 259 (34 campos) idénticas a las del simulador.
En la captura completa de 74 s (no versionada) el mismo verificador da 3 211 bajadas por IE 226,
3 541 subidas (3 258 con NAT; las otras 19 de las 3 277 de §4.4.3 caen en `internal` o `tunnel`),
4 429 internas, 37 de túnel y 32 desconocidas.

**Pérdidas en la captura**: la secuencia IPFIX tiene 23 saltos en estos 20 s (3 247 registros que el
router envió y no están en la captura; 80 saltos y 11 985 registros en los 74 s). `expected.json` lo
fija (`sequence_gaps`, `lost_records`) para que el verificador lo compruebe en lugar de fallar.
Pendiente averiguar si se pierden en el sniffer o salen por otra interfaz.

La señal `scan_vertical` de `expected.json` es real: un host interno contra 285 puertos de otro host
interno en la misma ventana.

Para regenerar `expected.json` tras un cambio intencionado del verificador:
`sim-verify -in ipfix-nat-20s.pcapng -expected ipfix-nat-20s.expected.json -update` (conserva
prefijos, IPs y parámetros; reescribe lo observado). `TestRealNATAttribution`
(`tools/flowsim/internal/selftest`) fija los recuentos de la tabla con independencia del golden.
