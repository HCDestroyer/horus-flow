# Instalar Horus Flow solo con Docker

Guía para instalar Horus Flow en un servidor que **ya tiene Docker** sin ejecutar ningún script en
el propio servidor: con un `docker run` o con un **stack** de Portainer, Proxmox o TrueNAS. Sirve
cuando el servidor lo gestiona otra persona, cuando todo se administra desde un panel de Docker o
cuando no quieres instalar paquetes en el sistema.

Por dentro es **el mismo instalador** que el de Debian ([`install-debian.md`](install-debian.md)):
la imagen `horus-installer` trae `install.sh`, `horus-ctl` y `horus-backup` de esa versión y los
ejecuta con `--runtime docker`. Mismas comprobaciones, mismos secretos (que nunca se regeneran),
mismo paquete de secretos offline, mismas copias de seguridad.

> Si puedes elegir, un **Debian dedicado con [`install-debian.md`](install-debian.md)** sigue siendo
> lo más sencillo de mantener. Esta guía es para cuando eso no es posible.

## Índice

1. [Qué cambia respecto a la instalación en Debian](#1-qué-cambia-respecto-a-la-instalación-en-debian)
2. [Antes de empezar](#2-antes-de-empezar)
3. [Instalar con docker run](#3-instalar-con-docker-run)
4. [Instalar con Portainer (stack)](#4-instalar-con-portainer-stack)
5. [Proxmox](#5-proxmox)
6. [TrueNAS SCALE](#6-truenas-scale)
7. [Puertos](#7-puertos)
8. [Uso diario](#8-uso-diario)
9. [Actualizar](#9-actualizar)
10. [Copias de seguridad y restauración](#10-copias-de-seguridad-y-restauración)
11. [Desinstalar](#11-desinstalar)
12. [Seguridad: lo que debes saber](#12-seguridad-lo-que-debes-saber)
13. [Problemas frecuentes](#13-problemas-frecuentes)
14. [Qué está probado y qué no](#14-qué-está-probado-y-qué-no)

---

## 1. Qué cambia respecto a la instalación en Debian

| En Debian (`install.sh`) | Solo con Docker (`horus-installer`) |
| --- | --- |
| systemd arranca el túnel y Horus al encender | Docker arranca los contenedores (`restart: unless-stopped`) |
| `horus-tunnel.service` crea el hub WireGuard `wg0` y las reglas de cortafuegos | el contenedor **horus-host** hace lo mismo (red del anfitrión, `NET_ADMIN`, `/dev/net/tun`) |
| timers de systemd para las copias | **horus-host** las programa (supercronic, mismos horarios en UTC) |
| `horus-ctl …` | `horus-installer ctl …` (un `docker run` de la imagen) |
| `horus-ctl upgrade` / `rollback` | ejecutar la imagen `horus-installer` de la versión deseada (apartado 9) |
| el instalador ajusta `net.core.rmem_max` | lo pones tú en el anfitrión (apartado 2) |
| paquete de secretos en `/root` | paquete de secretos en `/etc/horus/offline-bundle` |

Todo lo importante vive en el **anfitrión**, no dentro de los contenedores: `/opt/horus`
(compose, configuración, scripts), `/etc/horus` (secretos, certificados, respuestas del instalador)
y `/var/lib/horus` (datos y copias). Puedes borrar la imagen del instalador cuando quieras: la
próxima vez se usa la misma configuración.

## 2. Antes de empezar

**El servidor** (requisitos de [`install-debian.md`](install-debian.md) §1: 4 núcleos, 8 GB de RAM y
100 GB de disco como mínimo para un ISP pequeño) necesita:

- **Docker Engine 24 o superior.** El plugin compose del anfitrión no hace falta: el instalador
  trae el suyo.
- **WireGuard:** el módulo del kernel (`modprobe wireguard`; viene en Debian 12+, Ubuntu 20.04+,
  Proxmox 7+, TrueNAS SCALE) **o** el dispositivo `/dev/net/tun` (entonces horus-host usa
  wireguard-go, que gasta algo más de CPU). En ambos casos `/dev/net/tun` debe existir.
- **Búfer UDP del colector** (recomendado; sin él se pueden perder flujos en ráfagas). Es un ajuste
  del anfitrión que un contenedor no puede cambiar. Como root en el anfitrión (en Proxmox, en el
  propio Proxmox):

  ```bash
  printf 'net.core.rmem_max = 33554432\nnet.core.rmem_default = 1048576\n' > /etc/sysctl.d/90-horus.conf
  sysctl --system
  ```

- Los puertos **80/443 TCP** y **51820 UDP** libres (apartado 7).

**Comprueba la imagen.** Usa siempre la imagen **fijada por digest** que publica la release
(`ghcr.io/hcdestroyer/horus-installer:X.Y.Z@sha256:…`, en las notas de la release y en el
`compose.stack.yaml` adjunto), nunca `latest`. Si tienes [cosign](https://github.com/sigstore/cosign),
verifica la firma antes de ejecutarla:

```bash
cosign verify ghcr.io/hcdestroyer/horus-installer:X.Y.Z@sha256:… \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github.com/hcdestroyer/horus-flow/.github/workflows/release.yml@refs/tags/v'
```

El instalador verifica a su vez la firma de las imágenes de Horus que descarga (y la suya propia).

## 3. Instalar con docker run

Como root (o un usuario del grupo `docker`) en el servidor:

```bash
IMG=ghcr.io/hcdestroyer/horus-installer:X.Y.Z@sha256:…   # el de la release

docker run --rm -it --network host \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /opt/horus:/opt/horus -v /etc/horus:/etc/horus -v /var/lib/horus:/var/lib/horus \
  "$IMG" install
```

Preguntará el modo de acceso (solo IP, dominio o subdominio), el rango de túneles y el correo del
superadministrador, igual que en Debian. Sin preguntas:

```bash
docker run --rm -it --network host \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /opt/horus:/opt/horus -v /etc/horus:/etc/horus -v /var/lib/horus:/var/lib/horus \
  "$IMG" install --yes --mode ip --public-ip 203.0.113.10 --admin-email noc@miisp.net
```

Acepta las mismas opciones que `install.sh` (`--mode domain --domain horus.miisp.net --acme-email …`,
`--tls external …`, `--size`, `--tunnel-cidr`, `--store-dir`…; lista completa con `"$IMG" install --help`).

**Qué es cada parte** (no la cambies salvo que sepas por qué):

- `--network host`: el instalador comprueba puertos, detecta la IP pública y prueba HTTPS como si
  estuviera en el servidor.
- `-v /var/run/docker.sock:…`: crea y gestiona los contenedores de Horus. **Equivale a dar acceso
  root al servidor** (apartado 12).
- `-v /opt/horus:/opt/horus` y los demás: **misma ruta dentro y fuera**. Es obligatorio: compose y
  las copias interpretan esas rutas en el anfitrión. Si usas otras (`--install-dir`, `--etc-dir`,
  `--data-dir`, `--store-dir`), monta cada una igual (`-v /srv/horus:/srv/horus`). El instalador lo
  comprueba y se para si no coincide.

Para no repetir el `docker run`, define un atajo en tu `~/.bashrc` (lo usa el resto de esta guía):

```bash
horus-installer() {
  docker run --rm -it --network host \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v /opt/horus:/opt/horus -v /etc/horus:/etc/horus -v /var/lib/horus:/var/lib/horus \
    ghcr.io/hcdestroyer/horus-installer:X.Y.Z@sha256:… "$@"
}
```

Al terminar verás la URL, la **huella SHA-256** del certificado (modo solo IP), la contraseña
inicial del superadministrador (si la generó) y la ruta del **paquete de secretos offline** con su
frase de descifrado. Anota todo: la contraseña y la frase **no se vuelven a mostrar**. El primer
acceso es igual que en Debian ([`install-debian.md`](install-debian.md) §6: cambiar contraseña y
activar TOTP).

Comprueba la instalación cuando quieras:

```bash
horus-installer check
```

## 4. Instalar con Portainer (stack)

Cada release adjunta `compose.stack.yaml` (con la imagen ya fijada por digest) y
`stack.env.example`. En el repositorio están en `deployments/compose/`.

1. En Portainer: **Stacks › Add stack**. Nombre: `horus-installer` (**no** `horus`: ese nombre lo
   usan los servicios de Horus).
2. **Web editor:** pega el contenido de `compose.stack.yaml`.
3. **Environment variables › Load variables from .env file:** sube `stack.env.example` y cambia al
   menos `HORUS_ADMIN_EMAIL`, `HORUS_MODE` y `HORUS_PUBLIC_IP` (o `HORUS_DOMAIN` y
   `HORUS_ACME_EMAIL`). Ninguna variable es un secreto: los secretos se generan en el servidor.
   Recomendado: `HORUS_BUNDLE_RECIPIENT` con tu clave **pública** age (`age1…`), así el paquete de
   secretos se cifra para ti y no se imprime ninguna frase en los registros.
4. **Deploy the stack.** Se crea un único contenedor, `horus-init`, que instala todo y termina.
5. Abre **Containers › horus-installer-horus-init-1 › Logs**: ahí están la URL, la huella del
   certificado, la contraseña inicial y el paquete de secretos. Cuando lo hayas copiado todo,
   **elimina ese contenedor** (sus registros se borran con él).
6. Los servicios de Horus aparecen como el stack **horus** (Portainer lo marca como creado fuera de
   Portainer: es normal; gestiónalo con `horus-installer`, no lo edites desde Portainer).

Para cambiar algo (otra versión, otra variable): edita el stack y **Update the stack**. `horus-init`
vuelve a ejecutarse, hace una copia previa y aplica el cambio sin regenerar secretos.

## 5. Proxmox

**Recomendado: una VM** (Debian 12/13) con Docker. Dentro funciona exactamente como en un servidor
normal: puedes usar [`install-debian.md`](install-debian.md) o esta guía.

**En un contenedor LXC** también funciona, con estos ajustes (contenedor **con privilegios** o sin
ellos, Proxmox 8):

1. En el **anfitrión Proxmox** (no en el LXC):

   ```bash
   modprobe wireguard && echo wireguard > /etc/modules-load.d/wireguard.conf
   printf 'net.core.rmem_max = 33554432\nnet.core.rmem_default = 1048576\n' > /etc/sysctl.d/90-horus.conf
   sysctl --system
   ```

2. En `/etc/pve/lxc/<ID>.conf` (con el LXC parado):

   ```
   features: nesting=1,keyctl=1
   lxc.cgroup2.devices.allow: c 10:200 rwm
   lxc.mount.entry: /dev/net/tun dev/net/tun none bind,create=file
   ```

   `nesting` y `keyctl` son los que necesita Docker dentro de un LXC; las otras dos líneas pasan
   `/dev/net/tun`. Si Docker no arranca contenedores dentro del LXC, en último caso añade
   `lxc.apparmor.profile: unconfined` (reduce el aislamiento del LXC).

3. Arranca el LXC, instala Docker dentro y sigue el apartado 3 o 4.

Notas: el búfer UDP y el módulo WireGuard son **del kernel del anfitrión**, por eso se ponen en
Proxmox. En un LXC **sin privilegios** Docker puede rechazar `net.ipv4.ping_group_range`: el
instalador lo detecta y Horus sigue sin la latencia por ping (la salud de los routers se calcula con
SNMP y el túnel). Reenvía en el router los puertos del apartado 7 a la IP del LXC.

## 6. TrueNAS SCALE

TrueNAS SCALE 24.10 (Electric Eel) o posterior, con Docker:

1. Crea un **dataset** para Horus, por ejemplo `tanque/horus`, y dentro las carpetas `install`,
   `etc`, `data` y, mejor en otro pool, `store`. **No uses `/etc` ni `/opt`** del sistema: TrueNAS
   los reemplaza al actualizarse.
2. TrueNAS usa los puertos 80/443 para su propia web. Elige otros para Horus (`HORUS_HTTPS_PORT=8443`,
   `HORUS_HTTP_PORT=8080`) o mueve la web de TrueNAS (System › General › GUI).
3. **Apps › Discover Apps › ⋮ › Install via YAML**: pega `compose.stack.yaml` y define las variables
   (o copia `stack.env.example` como `.env`) con las rutas del dataset:

   ```
   HORUS_INSTALL_DIR=/mnt/tanque/horus/install
   HORUS_ETC_DIR=/mnt/tanque/horus/etc
   HORUS_DATA_DIR=/mnt/tanque/horus/data
   HORUS_STORE_DIR=/mnt/copias/horus-store
   HORUS_HTTPS_PORT=8443
   HORUS_HTTP_PORT=8080
   ```

4. Los registros de `horus-init` (en la app) muestran la URL, la huella, la contraseña inicial y el
   paquete de secretos, como en Portainer.

Con `docker run` en TrueNAS, monta esas mismas rutas (`-v /mnt/tanque/horus:/mnt/tanque/horus`) y
pasa `--install-dir`, `--etc-dir`, `--data-dir` y `--store-dir`; para las demás órdenes basta con
`-e HORUS_OPT_ETC_DIR=/mnt/tanque/horus/etc`.

## 7. Puertos

Los mismos que en Debian ([`install-debian.md`](install-debian.md) §5):

| Puerto | Protocolo | Para qué | ¿Obligatorio? |
| --- | --- | --- | --- |
| **443** (o `HORUS_HTTPS_PORT`) | TCP | web y API (HTTPS) | sí (salvo modo proxy) |
| **80** (o `HORUS_HTTP_PORT`) | TCP | redirección y Let's Encrypt | en modos dominio/subdominio |
| **51820** | **UDP** | túnel WireGuard de los routers | **sí, siempre** |
| **500, 4500** | **UDP** | IPsec IKEv2 (NAT-T) | solo con `--tunnels ipsec` |
| ESP (proto 50) / GRE (proto 47) | IP | IPsec sin NAT / GRE | solo con `--tunnels ipsec` / `--tunnels gre` |

Los flujos (IPFIX/NetFlow, UDP 4739 y 2055) **nunca** se abren: solo entran por el túnel.
Con `--tunnels` el contenedor `horus-host` aplica las mismas reglas que Debian (flujos solo por `wg0`,
`hxfrm+`, `hgre+` o `--direct-flow-sources`) y se añaden `horus-tunnel-agent` y `horus-ipsec`
([`tunnels.md`](tunnels.md) §7). El anfitrión necesita `ipset` y los módulos `xfrm_interface` e `ip_gre`.

## 8. Uso diario

Con el atajo `horus-installer` del apartado 3:

| Orden | Qué hace |
| --- | --- |
| `horus-installer ctl status` | versión, URL, salud de cada servicio, copias y si hay versión nueva |
| `horus-installer ctl logs` · `ctl logs horus-app -f` | registros |
| `horus-installer check` | comprobación completa (puertos, túnel, cortafuegos, certificado, disco, copias) |
| `horus-installer backup status` · `backup run` · `backup verify` | copias (apartado 10) |
| `horus-installer ctl restore` | restaurar copias |
| `horus-installer ctl diagnose` | paquete de diagnóstico para soporte (ver [`install-debian.md`](install-debian.md) §7) |
| `horus-installer uninstall` | desinstalar (apartado 11) |
| `docker logs horus-horus-host-1` | túnel, cortafuegos y copias programadas |

Horus arranca solo al encender el servidor: Docker levanta los contenedores y horus-host vuelve a
crear `wg0`, reaplica el cortafuegos y reinicia el colector y wg-agent cuando hace falta.

## 9. Actualizar

1. Consulta la versión nueva (`horus-installer ctl status` avisa) y sus notas.
2. Ejecuta la imagen **de la versión nueva** con `upgrade`:

   ```bash
   docker run --rm -it --network host \
     -v /var/run/docker.sock:/var/run/docker.sock \
     -v /opt/horus:/opt/horus -v /etc/horus:/etc/horus -v /var/lib/horus:/var/lib/horus \
     ghcr.io/hcdestroyer/horus-installer:NUEVA@sha256:… upgrade
   ```

   Hace una **copia previa obligatoria**, descarga y verifica las imágenes nuevas, aplica las
   migraciones al arrancar y espera a que todo esté sano. horus-host pasa también a la imagen nueva.
   Actualiza el atajo `horus-installer` con el digest nuevo.
3. Con Portainer o TrueNAS: cambia `HORUS_INSTALLER_IMAGE` (o el `compose.stack.yaml` de la release
   nueva) y vuelve a desplegar.

**Volver atrás:** ejecuta `install` con la imagen de la versión anterior. Si la versión nueva cambió
el esquema de la base de datos, restaura además la copia previa (`horus-installer ctl restore`).
La actualización automática de parches (`--auto-update on`) no existe en este modo.

## 10. Copias de seguridad y restauración

Igual que en Debian ([`install-debian.md`](install-debian.md) §9): copia local cada noche (02:15
UTC), prueba de restauración cada domingo (06:00 UTC), 3 copias completas y registro continuo de
PostgreSQL. Las programa horus-host. Están en `/var/lib/horus/store/backups` (o tu `--store-dir`):
**mejor en otro disco**.

**Paquete de secretos (¡muy importante!).** Queda en `/etc/horus/offline-bundle/` (en el
anfitrión). Sin él, las copias no se pueden leer en otro servidor. Cópialo **fuera** del servidor
(dos sitios), guarda la frase aparte, bórralo del servidor y confírmalo:

```bash
horus-installer confirm-bundle
```

**Restaurar** (sustituye los datos actuales; pide confirmación):

```bash
horus-installer ctl restore
horus-installer ctl restore --pg 20261010-021500F
```

Servidor nuevo tras perder el antiguo: [`disaster-recovery.md`](disaster-recovery.md) RB-09; copia
`secrets/` y `tls/` del paquete a `/etc/horus` antes de ejecutar `install`.

**Sin Internet:** carga primero las imágenes del paquete offline y apunta el instalador a él:

```bash
tar -xzOf horus-X.Y.Z-linux-amd64.tar.gz horus-X.Y.Z/images/horus-images.tar | docker load
docker run --rm -it --network host -v /var/run/docker.sock:/var/run/docker.sock \
  -v /opt/horus:/opt/horus -v /etc/horus:/etc/horus -v /var/lib/horus:/var/lib/horus \
  -v "$PWD:/paquete:ro" ghcr.io/hcdestroyer/horus-installer:X.Y.Z \
  install --image-source bundle:/paquete/horus-X.Y.Z-linux-amd64.tar.gz --no-update-check
```

Vuelve a pasar `--image-source bundle:…` (con el paquete montado) en cada ejecución de `install` o
`upgrade`; si no, el instalador intentará descargar de GHCR.

## 11. Desinstalar

```bash
horus-installer uninstall           # quita contenedores, túnel y reglas; CONSERVA datos, copias y secretos
horus-installer uninstall --purge   # borra TODO (pide escribir "borrar")
```

En un stack, elimina después el stack `horus-installer` desde Portainer. Si montaste directorios
completos (`/opt/horus`…), quedan vacíos en el anfitrión; bórralos con `rmdir` si quieres. Tras
`uninstall` sin `--purge`, volver a instalar recupera Horus con los mismos datos y contraseñas.

## 12. Seguridad: lo que debes saber

- **El socket de Docker es acceso root.** Quien puede ejecutar `horus-installer` (o cualquier
  contenedor con `/var/run/docker.sock`) controla el servidor entero. Lo mismo vale para el
  contenedor **horus-host**, que además usa la red del anfitrión, `NET_ADMIN` y `/dev/net/tun`. Es el
  mismo nivel de privilegio que el instalador de Debian (root) y sus servicios de systemd, pero
  conviene saberlo: no des acceso a Portainer ni al grupo `docker` a quien no daría root.
- **Imágenes fijadas y firmadas.** Usa siempre el digest de la release y verifica la firma
  (apartado 2). El instalador verifica la firma cosign de horus, horus-web, horus-postgres y de su
  propia imagen antes de usarlas.
- **IPFIX/NetFlow solo por el túnel.** El colector se publica solo en la IP del hub en `wg0`
  (`10.255.0.1` por defecto), nunca en `0.0.0.0`, y horus-host añade las mismas reglas que en Debian
  (cadenas `DOCKER-USER` e `INPUT`: UDP 4739/2055 que no venga de `wg0` se descarta), usando el
  mismo backend de iptables que Docker (nft o legacy).

  **Riesgo residual:** si el anfitrión usa un cortafuegos que no ve esas cadenas (Docker con el
  backend nativo de nftables, firewalld que las reescribe, un panel que vacía las reglas), las reglas
  no tienen efecto y solo queda la primera barrera: el puerto escucha únicamente en la IP del hub,
  que no es alcanzable desde Internet salvo para alguien en tu misma red local que enrute esa IP
  hacia el servidor. `horus-installer check` avisa si la regla `DOCKER-USER` falta.
- **wireguard-go:** sin el módulo del kernel, `wg0` existe solo mientras corre horus-host. Si se para,
  los routers pierden el túnel (no se expone nada) hasta que Docker lo reinicia.
- **Contraseña inicial y frase del paquete en los registros (stack).** En Portainer/TrueNAS se
  muestran en los registros de `horus-init`. Usa `HORUS_BUNDLE_RECIPIENT` y elimina el contenedor
  tras copiarlas; el primer acceso obliga a cambiar la contraseña y activar TOTP.

## 13. Problemas frecuentes

| Mensaje o síntoma | Qué hacer |
| --- | --- |
| `no está montado con la MISMA ruta del anfitrión` | monta cada directorio igual dentro y fuera (`-v /opt/horus:/opt/horus`) |
| `debe ejecutarse con --network host` | añade `--network host` al `docker run` (en un stack ya viene puesto) |
| `no llego al daemon de Docker` | falta `-v /var/run/docker.sock:/var/run/docker.sock` |
| `el anfitrión no tiene /dev/net/tun` | LXC de Proxmox: apartado 5; en otros: `modprobe tun` en el anfitrión |
| `módulo WireGuard no cargado` (aviso) | `modprobe wireguard` en el anfitrión, o déjalo: se usa wireguard-go |
| `net.core.rmem_max = 4194304 en el anfitrión` (aviso) | ajuste del apartado 2 en el anfitrión |
| horus-host `unhealthy` | `docker logs horus-horus-host-1`: suele ser `/dev/net/tun` o iptables del anfitrión |
| `puerto 443/tcp ocupado` | otro servicio (o la web de TrueNAS) usa el puerto: `--https-port 8443` |
| Los routers no conectan | ¿51820/UDP abierto y reenviado? `horus-installer check` muestra el estado de `wg0` |
| Portainer no deja editar el stack `horus` | es normal: se gestiona con `horus-installer` |

Más casos (dominio, proxy, Let's Encrypt): [`install-debian.md`](install-debian.md) §13.

## 14. Qué está probado y qué no

**Prueba automática cada noche** (job `install-docker` de `nightly.yml`, `make test-install-docker`): en un Ubuntu 24.04
limpio, instalación con `docker run` en modo solo IP, `check`, horus-host sano con `wg0`, reglas
`DOCKER-USER`/`INPUT` y copias programadas, colector publicado solo en la IP del hub, segunda
ejecución sin cambiar ningún secreto, huella del certificado servido, reinicio de horus-host
(`wg0` de nuevo y reenganche del colector), copia + restauración de prueba y `uninstall --purge`.

**Probado a mano:** el stack (`compose.stack.yaml` con `horus-init`) de punta a punta con
`docker compose`; wireguard-go en un anfitrión sin módulo del kernel.

**No probado todavía:** Portainer, Proxmox LXC y TrueNAS reales (las instrucciones siguen su
documentación), `ctl restore` en este modo, el modo dominio con Let's Encrypt y `--tls external`
(son el mismo código que en Debian), arm64, y Docker con el backend nativo de nftables.
