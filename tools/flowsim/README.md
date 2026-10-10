# tools/flowsim — simulador de flujos

- **Propósito:** generar flujos IPFIX/NetFlow v9 realistas de RouterOS 7 con escenarios
  (`scenarios/`, contrato C10) para probar ingesta y detección sin router real (`make sim`,
  `make sim-verify`).
- **Dueño:** FLOW — [`docs/backlog/team.md`](../../docs/backlog/team.md) §2.
- **Documentación:** [`docs/vendors/mikrotik.md`](../../docs/vendors/mikrotik.md),
  [`docs/traffic-model.md`](../../docs/traffic-model.md), historia I0-10 en
  [`docs/backlog/increment-0.md`](../../docs/backlog/increment-0.md).
- **Estado:** I0-10 implementada; I0-12: plantillas y NAT verificados con una captura real
  ([`docs/traffic-model.md` §4.4.3](../../docs/traffic-model.md)), anonimizador
  (`cmd/ipfix-anonymize`) y fixture real en [`tests/fixtures/mikrotik-real`](../../tests/fixtures/mikrotik-real).

## Uso rápido

```sh
make sim SCENARIO=normal SEED=1 RATE=2000              # IPFIX por UDP a 127.0.0.1:4739, 5 min en tiempo real
make sim SCENARIO=scan PROTO=v9 SIM_TARGET=10.0.0.5:2055
make sim SCENARIO=c2 SIM_OUT=bin/sim/c2.pcap           # a fichero (pcap o .hfsim, .gz comprime)
make sim SCENARIO=normal NAT=false IPV6=false DURATION=1m SPEED=0
make sim SCENARIO=normal SIM_PROFILE=legacy             # plantillas supuestas de I0-10 (256/257)
make sim SCENARIO=normal NAT_IPS=203.0.113.20,203.0.113.21 SIM_OUT=bin/sim/n.pcapng

make sim-verify                                        # prueba sin router: escenarios + fixtures + captura real
make sim-verify SIM_LISTEN=127.0.0.1:4739              # escucha y verifica contra bin/sim/<escenario>-<proto>.expected.json
make sim-verify SIM_IN=bin/sim/c2.pcap SCENARIO=c2     # verifica un fichero
```

Prueba de punta a punta en loopback (dos terminales):

```sh
go run ./tools/flowsim/cmd/sim-verify -listen 127.0.0.1:4739 -expected bin/sim/normal-ipfix.expected.json
make sim SCENARIO=normal RATE=2000 DURATION=1m
```

`flowsim -h` y `sim-verify -h` listan todas las opciones; `flowsim -list` los escenarios.

## Qué emula

- **Exportador RouterOS 7** (`internal/export`): IPFIX (v10) o NetFlow v9; plantillas reenviadas
  cada `template_refresh` paquetes (20) o cada `template_timeout` (1 min, también sin datos);
  secuencia v9 = paquetes, IPFIX = registros de datos; observation domain por exportador;
  datagramas ≤ 1392 bytes (MTU WireGuard 1420); v9 con FlowSets rellenados a 4 bytes.
  Plantillas (`flow.Templates`, `-profile`):
  - `routeros7` (**defecto en IPFIX**): las de la captura real, campo a campo — **258** IPv4 (37
    campos) y **259** IPv6 (34 campos), mismo orden y longitudes, incluidos IE 206
    (`isMulticast`), 224 (`ipTotalLength`, 8 B), 33 (`igmpType`), 178/179 (ICMPv6 tipo/código),
    TCP seq/ack/ventana, TTL (IE 192), MAC pre/post (56/80/81/57) y NAT (225-228). Sin traducción,
    los campos post-NAT repiten los previos (como el router).
  - `legacy`: las supuestas en I0-10 (256/257; TTL mín./máx., AS; IE 225-228 con `-nat-fields`).
  - NetFlow v9: siempre `legacy` (FIRST/LAST_SWITCHED, contadores de 4 bytes, sin MAC ni TTL);
    no hay aún captura real de v9.
- **Caché de Traffic Flow** (`internal/sim`): cada flujo se exporta cada `active_timeout` (1 min)
  desde su creación y al vencer el `inactive_timeout` (15 s) tras el último paquete; flujos
  unidireccionales con TCP flags acumulados (SYN en el primer registro, FIN en el último).
- **NAT en el router principal** (D12) tal como lo exporta RouterOS 7 (§4.4): **subida** con `src`
  privada y `postNATSourceIPv4Address` = IP pública del NAT; **bajada** con `dst` = IP pública del
  NAT y la privada del cliente solo en `postNATDestinationIPv4Address` (IE 226). Pool de 3 IPs
  públicas por exportador (`nat_ips`, `-nat-ips`; por defecto 203.0.113.10-12); cada cliente sale
  por la misma y el puerto se conserva casi siempre. Sin campos NAT (v9 o `legacy` sin
  `-nat-fields`) se mantiene el modelo idealizado de I0-10 (IP privada en ambos sentidos), que en
  un router real no sería atribuible en la bajada.
- **IPv6** (§4.8): cliente = prefijo delegado (`ipv6_client_len` 48/56/60/64), sin NAT; RA del
  router a `ff02::1` y NS de los CPE en enlace local (infraestructura) y pings ICMPv6 (tipo
  128/129 en IE 178/179) de clientes con prefijo delegado.
- **Nodos**: IP de túnel WireGuard como `exporter_ip`, NAT en el router principal o IPs
  públicas, prefijos IPv6 delegados /64, acceso
  VLAN (MAC del CPE) o PPPoE (ifIndex dinámico por cliente, sin MAC), tráfico del propio router
  (exportación y SNMP por el túnel, WireGuard exterior, DNS y NTP).
- **Remotos**: prefijos reales de servicios conocidos (Google, Meta, Netflix, Cloudflare,
  Akamai, AWS, Microsoft, Apple) para el enriquecimiento; C2, sinkholes y objetivos en rangos
  de documentación (RFC 5737); clientes públicos en 198.18.0.0/15 (RFC 2544). Nunca IPs de
  clientes reales.
- **Determinismo**: misma semilla y mismo `start` ⇒ mismos bytes. En UDP el arranque es "ahora"
  (solo cambian los timestamps); en fichero se usa el `start` del escenario.

## Escenarios

| Escenario | Qué contiene | Señales / hallazgos esperados |
| --- | --- | --- |
| `normal` | 250 hogares (50 con IPv6) en 10.20.0.0/24 con NAT: 300 clientes | ninguno |
| `commercial` | 200 hogares + 6 empresas: HTTPS/SMTP entrantes, SaaS, vídeo, backups, CDN en 3 /24 | `inbound_service`; sin hallazgos |
| `c2` | bot con C2 activo (6667) y bot contra C2 caído (solo SYN) | `botnet_c2_communication` high / medium |
| `scan` | escaneo horizontal 23/2323 tipo Mirai y escaneo vertical 1-1024 | `outbound_scanning` high / medium |
| `fanout` | bot P2P UDP hacia cientos de /24 | `outbound_fanout` (provisional) |
| `spam` | spambot a decenas de MX por 25/tcp; empresa con correo legítimo por 587 | `spam_smtp_outbound` |
| `dos_out` | inundación UDP de 20 000 pps a un destino durante 3 min | `ddos_participation` |
| `beacon` | beacon cada 5 min ± 5 % durante 7 h | `beaconing` |
| `sustained_out` | subida sostenida de 12 Mbit/s durante 40 min | `open_proxy_abuse` |
| `isp10k` | Nodo grande de un ISP: 10 000 IPv4 privadas en 10.64.0.0/18 tras NAT (IE 225-228, 16 IPs públicas), 30 % de hogares y 50 % de empresas con /64 delegado, 400 empresas, 1 % de infectados (Mirai, C2, beacon caído, P2P, spam); pico 15 000 registros/s con perfil diario | señales informadas, no exigidas (`report_only`); carga de `make load-isp10k` |
| `out_of_prefix` | 2 nodos con el mismo 10.20.0.0/24; IPs fuera de prefijos, rango excluido, tránsito IPv6, tráfico interno, PPPoE | atribución (`unknown`, `excluded`, `transit`, `internal`, `infrastructure`, `tunnel`) |

Los `kind` de hallazgo siguen I1-10/I1-11/I1-30 y son provisionales hasta que se congele C8.

### Formato YAML

```yaml
name: scan
duration: 5m          # duración simulada
rate: 300             # registros/s del tráfico de fondo (RATE lo sustituye)
nat: true             # NAT en el router principal (D12)
ipv6: true
fixture: {duration: 2m, rate: 60}     # parámetros reducidos para fixtures de CI
export: {active_timeout: 1m, inactive_timeout: 15s, template_refresh: 20, template_timeout: 1m, max_datagram: 1392}
indicators: [{ip: 203.0.113.66, kind: botnet_cc, confidence: high}]   # feed de prueba
exporters:
  - name: node-a      # resto de campos con valores por defecto (sim.defaultExporter)
    nat_ips: [203.0.113.10, 203.0.113.11, 203.0.113.12]   # IPs públicas del NAT (defecto)
    populations:
      - name: households
        count: 200
        ipv6_share: 0.2           # fracción con prefijo delegado IPv6
        range: customers          # customers | excluded | unlisted | customers@<otro nodo>
        behaviors: [residential]
      - name: mirai
        count: 1
        behaviors:
          - residential: {}
          - scan: {mode: horizontal, ports: [23, 2323], rate: 10}
        expect:
          signals: [scan_horizontal, fanout, watch_ports]
          findings: [{kind: outbound_scanning, severity: high, signals: [scan_horizontal]}]
```

Campos de carga: `daily` (24 factores por hora UTC del instante simulado que multiplican la tasa de
fondo; `rate` es el pico; `-flat` lo ignora y da tasa fija), `report_only: true` (las señales
calculadas van a expected.json sin exigirse) y `fixture.skip_file: true` (sim-verify lo genera y
verifica con los parámetros de `fixture`, pero no se versiona la captura).

```sh
make sim SCENARIO=isp10k                                  # 10 min con el perfil diario de la hora actual
go run ./tools/flowsim/cmd/flowsim -scenario isp10k -flat -rate 40000 -duration 5m -target 10.0.0.5:4739
```

Comportamientos: `residential`, `commercial`, `c2`, `beacon`, `scan`, `fanout`, `smtp`, `ddos`,
`sustained`, `lateral` (parámetros en `internal/sim/behaviors.go`). Las claves desconocidas son
error. Si con los parámetros elegidos (p. ej. una duración demasiado corta) las señales calculadas
no coinciden con `expect`, `flowsim` falla (o avisa con `-allow-unmet`).

## expected.json

Lo escribe `flowsim` al terminar (`internal/expect`, esquema `horus.flowsim.expected/v1`): por
exportador, totales (datagramas, envíos de plantilla, registros v4/v6, bytes, paquetes), conteo
por estado de atribución, clientes (clave IPv4 o prefijo IPv6, población, tipo, registros y
bytes de subida/bajada), IPs fuera de prefijos; y a nivel global los indicadores del feed de
prueba, las señales de `docs/traffic-model.md` §8/§9 con sus datos (`internal/signals`) y los
hallazgos esperados con sus razones.

## Verificador

Atribuye con la regla de [`docs/traffic-model.md` §4.4](../../docs/traffic-model.md): (1) `src` en
`client_prefix` → subida; (2) si no, IE 226 presente, distinta de `dst` y en `client_prefix` →
bajada; (3) si no, `dst` en `client_prefix` → bajada; (4) si no, `unknown` (o `transit`,
`infrastructure`, `excluded`, `tunnel` e `internal` según §4.6; enlace local IPv6 =
infraestructura, §4.8.3). Cuenta los registros por regla (`by_rule`) y comprueba que las IPs
públicas del NAT son las de `nat_ips` y que las plantillas recibidas son las declaradas.

`sim-verify` decodifica con [goflow2](https://github.com/netsampler/goflow2) (librería
independiente del codificador) y comprueba: versión y dominio, secuencia sin huecos, plantilla
antes de los datos y refresco por paquetes/tiempo, coherencia de cada registro (tiempos,
duración ≤ active timeout, contadores, flags), totales exactos, atribución por estado con los
prefijos de expected.json (§4.4 y tabla §4.6), recuento por regla, plantillas, clientes y bytes exactos, IPs fuera de prefijos, IPs
privadas con NAT, prefijos IPv6 delegados, señales idénticas y hallazgos sustentados. Con
`-tolerance` admite pérdida UDP. Lee hfsim, pcap y pcapng (Ethernet/802.1Q, SLL, IP en bruto),
también comprimidos, así que sirve para las capturas del laboratorio CHR y de routers reales.
`-update` reescribe un expected.json con lo observado (para fijar el de una captura real; los
huecos de secuencia de una captura con pérdidas quedan en `sequence_gaps`/`lost_records`).

## Fixtures

`fixtures/sim/<escenario>/{ipfix,v9}.hfsim.gz` + `.expected.json`, semilla 1 y parámetros
`fixture` de cada escenario. Formato hfsim descrito en `internal/capture/capture.go` (se usa
en lugar de pcap porque `*.pcap` está en `.gitignore`). `make sim-verify` los verifica y
comprueba que regenerarlos da los mismos bytes. Para regenerarlos tras un cambio intencionado:

```sh
go run ./tools/flowsim/cmd/flowsim -fixtures-dir tools/flowsim/fixtures/sim
```

## Capturas reales: ipfix-anonymize

`cmd/ipfix-anonymize` (`internal/anonymize`) convierte una captura pcap/pcapng de IPFIX/v9 de un
router real en un fixture versionable: privadas → `10.20.0.0/16` conservando /24 y host, IPs
públicas del NAT → `192.0.2.10-12`, exportador → `10.255.3.17`, resto de públicas por HMAC a
RFC 5737, IPv6 a `2001:db8::/32` conservando /64, MAC a MAC locales por HMAC; puertos, contadores,
tiempos y plantillas intactos. Falla en cerrado (descarta tramas que no son flujo y datagramas sin
plantilla; rechaza plantillas redefinidas y direcciones de sustitución que ya existan en el
original) y termina comprobando que ninguna IP ni MAC original sobrevive:

```sh
go run ./tools/flowsim/cmd/ipfix-anonymize -in cap.pcapng -out fixture.pcapng -key-file clave.bin -duration 20s
go run ./tools/flowsim/cmd/ipfix-anonymize -check -in cap.pcapng -anon fixture.pcapng
```

La clave HMAC no se guarda en el repositorio (sin `-key-file` es aleatoria). **Nunca** se versiona
la captura original. Ejemplo y verificación en
[`tests/fixtures/mikrotik-real/README.md`](../../tests/fixtures/mikrotik-real/README.md).
