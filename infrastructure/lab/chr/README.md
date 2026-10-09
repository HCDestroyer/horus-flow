# Laboratorio MikroTik CHR (I0-11)

Un MikroTik virtual (CHR, RouterOS 7, licencia free) con clientes simulados detrás de un NAT en
el mismo router (D12), un túnel WireGuard hacia un "hub" que hace de Horus (D16) y exportación
IPFIX por ese túnel. Sirve para validar contra RouterOS real lo que
[`docs/vendors/mikrotik.md`](../../../docs/vendors/mikrotik.md) marca como "a verificar" (I0-12) y el
script de onboarding de la §7, sin depender del router de la persona.

> **Estado:** preparado y probado **por partes** en el entorno del agente, que no tiene
> `/dev/kvm` ni acceso a `download.mikrotik.com`. El primer `make lab-selftest` en verde tiene que
> correr en una máquina con KVM (la de la persona) y su salida se adjunta al PR (§ "Hecho cuando").
> Qué se ejecutó y qué no: ver [Estado de la validación](#estado-de-la-validación).

## Topología

```
 netns hlab-cli-a 10.20.0.11 ┐                                      ┌─ netns hlab-inet 198.18.0.10
 netns hlab-cli-b 10.20.0.12 ┼─ bridge hlab-lan ─ ether3-lan ─ CHR ─ ether2-wan ─ bridge hlab-wan ─┤   HTTP :80 (/beacon "C2", /blob)
 netns hlab-cli-c 10.20.0.13 ┘                    10.20.0.1   │  198.18.0.2 (NAT masquerade)     │   SMTP :25, "C2" :4444, sumidero :5201, :53/udp
                                                              │                                  └─ host 198.18.0.1 — endpoint del hub WG :51820
                                               ether1-mgmt (QEMU user-net, DHCP 10.0.2.15)
                                               REST 127.0.0.1:18080 · HTTPS :18443 · SSH :12222

 Túnel: wg-horus (CHR, 10.255.3.17) ⇄ hlab-wg (host, 10.255.0.1 = <HORUS_COLLECTOR_IP>)
        gestión (REST, API-SSL, SNMPv3, ICMP) y IPFIX → 10.255.0.1:4739/udp
```

- Diferencia con el dibujo de §8.2: la NIC 1 del CHR (`ether1`) se reserva a la **gestión del
  laboratorio** porque es la que recibe DHCP en un CHR limpio; la WAN con NAT es `ether2-wan` y la
  LAN `ether3-lan`. El NAT sigue estando en el mismo router que ve a los clientes (D12).
- `198.18.0.0/24` (RFC 2544) y `10.255.0.0/24` son configurables en [`lab.env`](lab.env) si chocan
  con tu red.
- Clientes como *network namespaces* (§8.1). El segundo CHR con PPPoE (§8.2) **no** está todavía.

## Requisitos (máquina de la persona)

- Linux x86-64 con **KVM**: `ls -l /dev/kvm` existe y tu usuario puede escribirlo (grupo `kvm`), o
  se ejecuta con `sudo`. Virtualización activada en la BIOS.
- `sudo` (bridges, taps, netns, WireGuard y QEMU necesitan root).
- Paquetes (Debian/Ubuntu):
  `sudo apt install iproute2 wireguard-tools qemu-system-x86 qemu-utils curl jq unzip python3 socat snmp`
  (`snmp` solo para la comprobación SNMPv3; `socat` para la consola). Sin módulo WireGuard en el
  kernel (WSL, contenedores) usa `wireguard-go`.
- Acceso a `https://download.mikrotik.com` (o descarga manual del zip, ver abajo).

## Uso

```bash
# 1. Checksum de la imagen: cópialo de https://mikrotik.com/download → Cloud Hosted Router →
#    "Raw disk image" (botón SHA256) y fíjalo en infrastructure/lab/chr/checksums.sha256:
#      <sha256>  chr-7.12.img.zip
#    (o pásalo una vez como CHR_SHA256=<sha256>). Sin checksum la descarga se niega.
make lab-up ROS=7.12              # red + hub WG + CHR limpio + base + onboarding (§7)
make lab-status                   # VM, túnel (wg show), netns
make lab-traffic PROFILE=scan     # beacon | scan | smtp | volume | dns  (CLIENT=a|b|c, DURATION=60)
make lab-console                  # consola serie del CHR (Ctrl-] para salir)
make lab-selftest ROS=7.12        # validación §8.3 en un CHR limpio → .lab/selftest-7.12.log
make lab-down                     # apaga la VM y borra la red (la caché de imágenes se queda)
```

Repite `lab-selftest` con la **última long-term** (`ROS=7.x.y` desde mikrotik.com/download) y
adjunta las dos salidas al PR. Cada `lab-up` arranca un CHR **limpio** (disco qcow2 de capa sobre la
imagen verificada, que nunca se modifica).

Si `download.mikrotik.com` no es accesible, descarga `chr-<versión>.img.zip` a mano en
`~/.cache/horus-lab/chr/` y vuelve a lanzar `make lab-up`: el checksum se verifica igual.

Sin KVM, `make lab-up` falla con un mensaje que sugiere los fixtures (`make sim-verify`, I0-12) o
`LAB_ACCEL=tcg` (emulación, arranque de varios minutos; súbelo con `LAB_BOOT_TIMEOUT=900`).

### Qué hace `make lab-up`

1. **Descarga verificada** (`scripts/lab/fetch-chr.sh`): solo RouterOS 7.x ≥ 7.12 (D15). La imagen
   va a `~/.cache/horus-lab/chr/`, nunca al repo. SHA-256 contra, por orden: la línea fijada en
   [`checksums.sha256`](checksums.sha256), el checksum que MikroTik publique junto a la imagen
   (la ruta `<versión>/SHA256SUMS` está **a verificar**) o `CHR_SHA256`. Si no coincide, se borra.
2. **Red**: bridges `hlab-wan`/`hlab-lan`, netns de clientes e `internet-sim`
   ([`internet-sim.py`](../../../scripts/lab/internet-sim.py)). Con Docker instalado añade reglas
   `FORWARD … -m comment horus-lab` para que `br_netfilter` no descarte el tráfico de los bridges;
   `lab-down` las quita.
3. **Hub WireGuard** `hlab-wg` en el host (`198.18.0.1:51820`, `10.255.0.1/24`), claves en `.lab/wg/`.
4. **CHR en QEMU** (KVM, 512 MB, 3 NIC virtio, consola serie en `.lab/console.sock`).
5. **Configuración** por la REST de gestión: fija una contraseña aleatoria de `admin`
   (`.lab/secrets.env`), sirve los `.rsc` desde `127.0.0.1` (el CHR los ve en `10.0.2.2`), los
   descarga con `/tool fetch` y los aplica con `/import verbose=yes`, como lo haría la persona al
   pegar el script. El import falla si la salida contiene errores (`.lab/import-*.log`).
   - [`base.rsc.tmpl`](base.rsc.tmpl): router del nodo antes del onboarding (IPs, NAT masquerade,
     firewall de entrada con drop final, para que `place-before=0` tenga dónde colocarse).
   - **Onboarding**: se extrae tal cual del bloque `routeros` de `vendors/mikrotik.md` §7
     ([`render-rsc.py`](../../../scripts/lab/render-rsc.py)) y se sustituyen sus placeholders: el
     laboratorio valida exactamente el script documentado.
6. Lee la clave pública de `wg-horus` y la añade como peer del hub. El router inicia el túnel.

### IPFIX hacia el host

El onboarding apunta Traffic Flow a `<HORUS_COLLECTOR_IP>:4739` = `10.255.0.1:4739/udp`, una IP
del host en la interfaz del hub. Para recibirlo:

- `python3 -I scripts/lab/ipfix-probe.py --listen 10.255.0.1:4739 --expect-src 10.20.0.0/24`
  (lo usa el selftest: plantilla, registros e IP privada pre-NAT);
- o el verificador del simulador: `make sim-verify SIM_LISTEN=10.255.0.1:4739 SIM_EXPECTED=…`;
- o el colector de Horus: el compose publica `horus-collector` solo en `HORUS_BIND_ADDR`
  (127.0.0.1). Para que reciba el IPFIX del laboratorio, levanta el compose con
  `HORUS_BIND_ADDR=10.255.0.1` (o `0.0.0.0`) y comprueba que la IP de origen que ve el colector
  sigue siendo `10.255.3.17` (con el *userland proxy* de Docker se pierde: punto a verificar en I0-12).

## `make lab-selftest` (§8.3)

Arranca un laboratorio limpio y comprueba: versión ≥ 7.12; base y onboarding sin errores (§8.3.1);
handshake WG < 30 s, ICMP, REST (www-ssl), API-SSL :8729 y SNMPv3 **por el túnel**, y REST con el
usuario `horus` **rechazada** fuera del túnel (§8.3.2); el usuario `horus` no puede escribir
(§8.3.8); llega IPFIX con plantilla y registros y, tras tráfico beacon + scan, aparecen IPs
privadas de los clientes pre-NAT (§8.3.3, §8.3.4, §8.3.6). Deja la salida en
`.lab/selftest-<ROS>.log`.

Fuera del selftest (manual o I0-12): IPv6, desviación de reloj ±2 s (§8.3.3), flujos vs
`ifHCOutOctets` en 1 h con y sin FastTrack (§8.3.5), PPPoE con segundo CHR (§8.3.7) y grabación de
fixtures (§8.3.9).

## Estado de la validación

| Pieza | Probado en el entorno del agente (sin KVM) | Pendiente en máquina con KVM |
| --- | --- | --- |
| Fallo claro sin `/dev/kvm` (criterio 3) | Sí: `make lab-up` sale con 2 y sugiere fixtures / `LAB_ACCEL=tcg` | — |
| Descarga + checksum (criterio 4) | Lógica probada con un zip de prueba: acepta con hash correcto, rechaza sin checksum, con hash erróneo y versiones < 7.12 o 6.x. `download.mikrotik.com` **bloqueado** (403 del proxy) | Primera descarga real y ruta del checksum publicado |
| Red, netns, internet-sim, NAT | Sí, con `LAB_ROUTER=netns` (router Linux de sustitución): los 5 perfiles de tráfico llegan y internet-sim ve la IP NAT `198.18.0.2` | Con el CHR real |
| QEMU | Arranca con la línea de órdenes del laboratorio en TCG (disco de prueba, sin RouterOS); taps en los bridges; `lab-down` limpia todo | Arranque real del CHR y REST por `ether1` |
| Hub WireGuard | Creado (con `wireguard-go`, sin módulo en el kernel) | Handshake con el CHR |
| Onboarding §7 | Extracción y sustitución de placeholders | Import en 7.12 y última LTS sin errores |
| `ipfix-probe.py` | Probado con `tools/flowsim` (IPFIX y v9) | Con Traffic Flow real |
| `lab-selftest` | — | Todo |

Supuestos a confirmar en la primera ejecución real (si fallan, el error lo dice y la consola
permite entrar a mano): el CHR limpio tiene `dhcp-client` en `ether1` y el servicio `www` activo;
`admin` sin contraseña acepta REST por HTTP; `POST /rest/execute` con `as-string` devuelve la
salida de `/import verbose=yes`.
