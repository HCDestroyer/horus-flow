# Instalar Horus Flow en Debian

Guía para poner Horus Flow en marcha en un servidor **Debian 12 (bookworm) o Debian 13 (trixie)**
recién instalado. No hace falta saber programar: son unos pocos comandos que se copian y se pegan.
El instalador prepara el sistema, descarga una versión verificada de Horus y la arranca. Nada se
compila en el servidor y no hace falta git ni Go.

> Historia I1-22. Detalles técnicos: [`scripts/install.sh --help`](../scripts/install.sh),
> [`scripts/bootstrap-debian.sh`](../scripts/bootstrap-debian.sh),
> [`deployments/README.md`](../deployments/README.md) y [`disaster-recovery.md`](disaster-recovery.md).

## Índice

1. [Qué servidor necesitas](#1-qué-servidor-necesitas)
2. [Preparar Debian](#2-preparar-debian)
3. [El comando de instalación](#3-el-comando-de-instalación)
4. [Cómo se accede: IP, dominio, subdominio o proxy propio](#4-cómo-se-accede-ip-dominio-subdominio-o-proxy-propio)
5. [Abrir puertos](#5-abrir-puertos)
6. [Primer acceso del superadministrador (con TOTP)](#6-primer-acceso-del-superadministrador-con-totp)
7. [Uso diario: horus-ctl](#7-uso-diario-horus-ctl)
8. [Actualizaciones](#8-actualizaciones)
9. [Copias de seguridad y restauración](#9-copias-de-seguridad-y-restauración)
10. [Desinstalar](#10-desinstalar)
11. [Detrás de un proxy inverso (Nginx Proxy Manager / nginx)](#11-detrás-de-un-proxy-inverso-nginx-proxy-manager--nginx)
12. [Servidor sin Internet (paquete offline)](#12-servidor-sin-internet-paquete-offline)
13. [Problemas frecuentes](#13-problemas-frecuentes)
14. [Qué está probado y qué no](#14-qué-está-probado-y-qué-no)

---

## 1. Qué servidor necesitas

Un servidor (físico o virtual) para Horus solo. Cifras orientativas para un ISP con routers
MikroTik que exportan IPFIX:

| Tamaño (`--size`) | Clientes del ISP | CPU | RAM | Disco |
| --- | --- | --- | --- | --- |
| Pequeño (`small`) | hasta unos 300 | 4 núcleos | 8 GB | 100 GB SSD |
| Mediano (`medium`) | unos 2 000 | 8 núcleos | 16 GB | 500 GB SSD + un segundo disco para copias |
| Grande (`large`) | unos 10 000 | 8 núcleos | 32 GB | 500 GB **NVMe** + un segundo disco para copias |

- El instalador elige el tamaño según la RAM del servidor; puedes forzarlo con `--size small`,
  `--size medium` o `--size large`. Ajusta la memoria de ClickHouse (4, 8 o 14 GB), la cola del
  colector de flujos y el disco que se reserva para el búfer de flujos (NATS).
- Para más de 10 000 clientes, o para ver de dónde salen estas cifras, mira las mediciones de
  [`tests/load/REPORT.md`](../tests/load/REPORT.md).
- **Mínimo absoluto:** 2 núcleos, 4 GB de RAM y 20 GB libres (solo para pruebas).
- **Mejor un segundo disco para las copias** (`--store-dir /mnt/copias/horus`): si se rompe el disco
  principal, las copias siguen ahí.
- amd64 (Intel/AMD) o arm64 (por ejemplo, servidores Ampere).

## 2. Preparar Debian

1. Descarga la imagen **netinst** de Debian 12 o 13 desde <https://www.debian.org/distrib/>.
2. Instálala con las opciones normales. En la pantalla **"Selección de programas"** deja marcado
   solo:
   - [x] **servidor SSH**
   - [x] **utilidades estándar del sistema**
   - [ ] entorno de escritorio (desmárcalo: no hace falta interfaz gráfica)
3. Pon al servidor una **IP fija** (en la instalación o en tu router/DHCP).
4. Entra por SSH como **root** (o con tu usuario y luego `su -`).

> Si instalaste con un usuario normal y `sudo`, puedes anteponer `sudo` a los comandos en lugar de
> entrar como root.

## 3. El comando de instalación

Como root, copia y pega **esta línea** (instala `curl` si no está, descarga el preparador y lo ejecuta):

```bash
apt-get install -y curl && curl -fsSLO https://github.com/hcdestroyer/horus-flow/releases/latest/download/bootstrap-debian.sh && bash bootstrap-debian.sh
```

El instalador te hará **tres preguntas**: cómo se accede (apartado 4), el rango de IPs de los
túneles de los routers (deja el que propone, `10.255.0.0/16`, salvo que ya lo uses) y la contraseña
del superadministrador (si la dejas vacía, genera una y te la muestra **una vez**).

Qué hace, por orden (puedes repetir el comando cuando quieras: no rompe nada ni cambia contraseñas):

1. Comprueba que es Debian 12 o 13 y que eres root.
2. Instala Docker **desde el repositorio oficial de Docker** (con su clave verificada), WireGuard,
   `age`, `jq`, el cortafuegos (`iptables`) y la **hora automática** (NTP: los flujos llevan hora).
3. Ajusta el kernel para que no se pierdan flujos en ráfagas (búfer UDP de hasta 32 MiB para el
   colector) y permite el reenvío del túnel (`/etc/sysctl.d/90-horus.conf`).
4. Comprueba que el kernel tiene WireGuard.
5. Descarga la versión de Horus, **comprueba su firma y sus sumas SHA-256** y la instala.
6. Arranca todo y espera a que cada servicio esté sano. Al final verás un resumen con la URL, la
   contraseña (si se generó) y el **paquete de secretos** (apartado 9).

Opciones útiles (se añaden al final de `bash bootstrap-debian.sh`):

| Opción | Para qué |
| --- | --- |
| `--mode ip` / `--mode domain --domain horus.miisp.net --acme-email noc@miisp.net` | responder sin preguntas |
| `--yes --admin-email noc@miisp.net --admin-password-file /root/clave.txt` | instalación desatendida |
| `--store-dir /mnt/copias/horus` | copias en otro disco |
| `--size medium` | tamaño del servidor (apartado 1) |
| `--channel beta` | recibir versiones beta (por defecto `stable`) |
| `--auto-update on --update-window "Sun *-*-* 03:30:00"` | parches automáticos (apartado 8) |
| `--unattended-upgrades` | actualizaciones de seguridad automáticas de Debian |
| `--version 1.2.3` | instalar una versión concreta |

## 4. Cómo se accede: IP, dominio, subdominio o proxy propio

Horus tiene **cuatro modos** (decisión D19 de [`po-decisions.md`](po-decisions.md)). Elige uno:

| Modo | Cuándo | Certificado HTTPS | Opciones |
| --- | --- | --- | --- |
| **Solo IP** | no tienes dominio | autogenerado por Horus; el navegador avisará y tendrás que aceptarlo | `--mode ip` |
| **Dominio** | tienes `horus.miisp.net` apuntando a este servidor | Let's Encrypt, automático | `--mode domain --domain horus.miisp.net --acme-email tu@correo` |
| **Subdominio** | igual, con un subdominio de otro dominio | Let's Encrypt, automático | `--mode subdomain --domain horus.cliente.example --acme-email …` |
| **Detrás de tu proxy** | ya tienes Nginx Proxy Manager, nginx, Caddy… | lo pone tu proxy | `--tls external …` (apartado 11) |

- **Solo IP:** al terminar, el instalador muestra la **huella SHA-256** del certificado. La primera
  vez que entres, el navegador dirá que la conexión "no es privada": abre los detalles del
  certificado y comprueba que la huella coincide **antes** de aceptar.
- **Dominio o subdominio:** crea antes en tu DNS un registro **A** con la IP pública del servidor y
  abre el puerto 80 (Let's Encrypt lo usa para comprobar que el dominio es tuyo).

## 5. Abrir puertos

En el router o cortafuegos que hay delante del servidor, redirige a la IP del servidor:

| Puerto | Protocolo | Para qué | ¿Obligatorio? |
| --- | --- | --- | --- |
| **443** | TCP | la web y la API (HTTPS) | sí (salvo modo proxy: lo abre tu proxy) |
| **80** | TCP | redirige a HTTPS y certificados de Let's Encrypt | en modos dominio/subdominio |
| **51820** | **UDP** | túnel WireGuard de los routers | **sí, siempre**, directo al servidor |

- Los flujos (IPFIX/NetFlow, UDP 4739 y 2055) **no** se abren a Internet: llegan **dentro** del túnel.
- El instalador ya filtra el resto en el propio servidor.

## 6. Primer acceso del superadministrador (con TOTP)

1. Abre la URL que mostró el instalador (por ejemplo `https://203.0.113.10`).
2. Entra con el correo del superadministrador y la contraseña inicial.
3. Horus te pedirá **cambiar la contraseña** (mínimo 12 caracteres).
4. Después te mostrará un **código QR**: escanéalo con una app de autenticación (Google
   Authenticator, Microsoft Authenticator, FreeOTP, Aegis…) y escribe el código de 6 cifras.
5. A partir de ahora, cada inicio de sesión pide contraseña **y** el código de la app. Guarda los
   códigos de recuperación si te los ofrece.

## 7. Uso diario: horus-ctl

Todo se gestiona con una sola orden, `horus-ctl` (como root o con `sudo`):

| Orden | Qué hace |
| --- | --- |
| `horus-ctl status` | versión, URL, salud de cada servicio, copias y si hay versión nueva |
| `horus-ctl logs` · `horus-ctl logs horus-app -f` | ver registros (todos, o de un servicio en directo) |
| `horus-ctl check` | comprobación completa (puertos, certificados, disco, copias) |
| `horus-ctl check-update` | ¿hay versión nueva? |
| `horus-ctl upgrade` | actualizar (apartado 8) |
| `horus-ctl backup run` · `backup verify` · `backup status` | copias (apartado 9) |
| `horus-ctl restore` | restaurar copias |
| `horus-ctl uninstall` | desinstalar (apartado 10) |

Horus arranca solo al encender el servidor (servicio `horus.service` de systemd).

## 8. Actualizaciones

### Cómo te enteras

- La **consola de plataforma** (solo el superadministrador) muestra *"Hay una versión nueva X.Y.Z"*
  con las notas del cambio y el comando para actualizar. Horus lo comprueba cada 6 horas.
- `horus-ctl status` y `horus-ctl check-update` dicen lo mismo en el servidor.
- Si el servidor no tiene salida a Internet, aparece como **"no comprobado"** (no es un error).

### Canales

- **stable** (por defecto): solo versiones probadas, `X.Y.Z`.
- **beta**: también `X.Y.Z-beta.N` y `-rc.N`, para probar antes. Se elige al instalar con
  `--channel beta` (vuelve a ejecutar el instalador para cambiarlo).
- Las versiones siguen **versionado semántico**: `Z` = parche (arreglos), `Y` = funciones nuevas
  compatibles, `X` = cambios grandes.

### Actualizar a mano (recomendado)

```bash
horus-ctl upgrade              # a la última del canal
horus-ctl upgrade --to 1.3.0   # a una concreta
```

Qué hace, y por qué es seguro:

1. **Copia de seguridad completa obligatoria** (PostgreSQL y ClickHouse). Si falla, **no actualiza**.
2. Descarga la versión nueva y **verifica la firma** (cosign, firmada por el sistema de publicación
   de GitHub) y las sumas **SHA-256**. Si algo no cuadra, no instala nada.
3. Guarda una "foto" de la configuración actual.
4. Aplica la versión nueva; al arrancar, Horus actualiza las bases de datos (las **migraciones solo
   avanzan**, nunca deshacen).
5. Comprueba que todos los servicios están sanos, que la web responde y que la API responde.
6. **Si algo falla, vuelve atrás solo**: repone la versión anterior y, si la base de datos ya se había
   migrado, **restaura la copia hecha en el paso 1**. Verás `VUELTA ATRÁS` y el comando termina con
   código 3. Los datos de los minutos entre la copia y el fallo pueden perderse en ese caso.

Vuelta atrás manual después de una actualización que "funcionó" pero no te convence:

```bash
horus-ctl rollback
```

Si la versión nueva ya migró la base de datos, `rollback` restaura la copia previa a la
actualización (se pierde lo registrado desde entonces). Si hasta eso fallara, se puede restaurar a
mano cualquier copia con `horus-ctl restore` (apartado 9) o seguir
[`disaster-recovery.md`](disaster-recovery.md) (RB-06, RB-09).

### Actualización automática (opcional, desactivada por defecto)

Pensada para empresas con ventana de mantenimiento. Si la activas, **solo aplica parches**
(`1.2.3 → 1.2.4`), nunca `1.3.0` ni `2.0.0` (esas siempre a mano), en la ventana que elijas, con el
mismo backup previo, verificación, comprobación de salud y vuelta atrás automática.

```bash
bash /opt/horus/bin/install.sh --yes --auto-update on --update-window "Sun *-*-* 03:30:00"
# desactivar:
bash /opt/horus/bin/install.sh --yes --auto-update off
```

La ventana usa el formato de calendario de systemd (`Sun *-*-* 03:30:00` = domingos a las 03:30;
`*-*-* 04:00:00` = cada día a las 04:00). Se registra en el log del sistema (`journalctl -t horus-ctl`).

### Si el repositorio o las imágenes son privados

Si el repositorio de GitHub o los paquetes de GHCR no son públicos, el servidor necesita un **token de
solo lectura**:

1. En GitHub: *Settings → Developer settings → Personal access tokens*, crea un token con permiso
   **`read:packages`** (y **`repo`** o, en uno de grano fino, *Contents: Read* del repositorio).
2. Guárdalo en el servidor en un archivo, por ejemplo `/root/token.txt`.
3. Instala o reinstala con `--registry-token-file /root/token.txt`. El instalador lo copia a
   `/etc/horus/registry-token` con permisos **0600** (solo root) y `horus-ctl upgrade` lo usa.
4. Borra `/root/token.txt`.

Alternativas: publicar los paquetes de GHCR como **públicos** (en GitHub, *Packages → Package
settings → Change visibility*) o usar el **paquete offline** (apartado 12), que no necesita token.

## 9. Copias de seguridad y restauración

- Horus hace **copias locales cada noche** (02:15 UTC), las **verifica restaurándolas** en una base
  vacía cada domingo, guarda 3 completas y mantiene el registro continuo de cambios de PostgreSQL
  (se puede volver a casi cualquier minuto de los últimos 14 días).
- Están en `/var/lib/horus/store/backups` (o en tu `--store-dir`). **Mejor en otro disco.**
- `horus-ctl backup status` muestra cuándo fue la última y si la prueba de restauración salió bien.
- `horus-ctl backup run` hace una ahora.

**Paquete de secretos (¡muy importante!).** Al instalar se genera
`/root/horus-secrets-<servidor>-<fecha>.tar.gz.enc` y una **frase de descifrado** que se muestra una
sola vez. Sin él, las copias no se pueden leer en otro servidor. Cópialo **fuera** del servidor (dos
sitios: gestor de contraseñas y un USB), guarda la frase aparte, bórralo del servidor y ejecuta:

```bash
bash /opt/horus/bin/install.sh --confirm-bundle
```

**Restaurar** (sustituye los datos actuales; pide confirmación):

```bash
horus-ctl restore                         # la última copia de PostgreSQL y de ClickHouse
horus-ctl restore --pg 20261010-021500F   # una copia concreta de PostgreSQL (ver horus-ctl backup status)
```

Recuperar en un servidor nuevo tras perder el antiguo: [`disaster-recovery.md`](disaster-recovery.md) RB-09.

## 10. Desinstalar

```bash
horus-ctl uninstall           # quita Horus, CONSERVA datos, copias y secretos
horus-ctl uninstall --purge   # borra TODO: datos, copias y secretos (pide escribir "borrar")
```

Tras `uninstall` sin `--purge`, volver a ejecutar el comando de instalación recupera Horus con
los mismos datos y contraseñas. Docker y los paquetes de Debian se quedan instalados.

## 11. Detrás de un proxy inverso (Nginx Proxy Manager / nginx)

Si ya tienes un **proxy inverso** que gestiona tus certificados (Nginx Proxy Manager, nginx, Caddy,
HAProxy…), Horus puede ir detrás: el proxy pone el HTTPS y Horus solo habla HTTP con él.

```bash
bash bootstrap-debian.sh --tls external --public-url https://horus.miisp.net \
     --trusted-proxies 192.168.1.20 --http-bind 192.168.1.10:8080
```

| Opción | Qué poner |
| --- | --- |
| `--public-url` | la dirección que la gente escribe en el navegador (siempre `https://`) |
| `--trusted-proxies` | la **IP de tu proxy** (o su red, p. ej. `192.168.1.0/24`). **Obligatoria**: solo de ahí se aceptan las cabeceras `X-Forwarded-*` |
| `--http-bind` | dónde escucha Horus en HTTP: `127.0.0.1:8080` si el proxy está **en el mismo servidor**; la **IP de la LAN** del servidor (`192.168.1.10:8080`) si está en otra máquina |
| `--wg-endpoint` | nombre o IP pública a la que los routers mandan el túnel WireGuard (si no es la de `--public-url`) |

Qué cambia en este modo:

- Horus **no** pide certificado a Let's Encrypt, **no** genera uno propio y **no** redirige a HTTPS
  ni envía HSTS (actívalo en tu proxy si quieres). Mantiene el resto de cabeceras de seguridad (CSP,
  X-Frame-Options…).
- El cortafuegos del servidor solo deja llegar al puerto de `--http-bind` desde `--trusted-proxies`.
- Horus usa la **IP real** de cada visitante (para el límite de intentos de login, la auditoría y los
  registros), la que el proxy pone en `X-Forwarded-For`. Si alguien falsifica esa cabecera, se ignora.
- Si llega una petición que no pasó por tu proxy, o con `X-Forwarded-Proto: http`, la consola de
  plataforma muestra un **aviso**.

> **WireGuard NO pasa por el proxy.** El túnel de los routers es **UDP 51820** y debe llegar
> **directo** al servidor de Horus (redirección de puerto en tu router). Un proxy HTTP no puede
> llevarlo. En Nginx Proxy Manager se puede usar un **Stream** UDP (abajo), pero lo más sencillo es
> la redirección directa.

### Nginx Proxy Manager, paso a paso

En *Hosts → Proxy Hosts → Add Proxy Host*:

```text
┌ Details ───────────────────────────────────────────────┐
│ Domain Names:          horus.miisp.net                 │
│ Scheme:                http                            │
│ Forward Hostname / IP: 192.168.1.10   (el servidor)    │
│ Forward Port:          8080           (--http-bind)    │
│ [x] Block Common Exploits                              │
│ [x] Websockets Support      ← IMPRESCINDIBLE           │
└────────────────────────────────────────────────────────┘
┌ SSL ───────────────────────────────────────────────────┐
│ SSL Certificate:  Request a new SSL Certificate        │
│                   (Let's Encrypt)                      │
│ [x] Force SSL                                          │
│ [x] HTTP/2 Support                                     │
│ [ ] HSTS Enabled   (opcional; actívalo cuando funcione)│
│ Email Address for Let's Encrypt: noc@miisp.net         │
│ [x] I Agree to the Let's Encrypt Terms of Service      │
└────────────────────────────────────────────────────────┘
```

- **Websockets Support** es necesario para el tiempo real y los kioscos (pantallas del NOC).
- En *Advanced → Custom Nginx Configuration* añade, para que los kioscos no se desconecten:

  ```nginx
  proxy_read_timeout 3600s;
  proxy_send_timeout 3600s;
  ```

- `--trusted-proxies` = la IP con la que NPM llega al servidor de Horus (la IP de la máquina de NPM).
- Si quieres pasar también el túnel por NPM: *Hosts → Streams → Add Stream*, *Incoming Port*
  `51820`, *Forward Host* la IP del servidor de Horus, *Forward Port* `51820`, marca **UDP** (y
  desmarca TCP). Recuerda abrir el UDP 51820 hacia la máquina de NPM.

### nginx, ejemplo completo

Con nginx en el mismo servidor (`--http-bind 127.0.0.1:8080 --trusted-proxies 127.0.0.1`), por
ejemplo en `/etc/nginx/sites-available/horus` (es el mismo archivo que usa la prueba automática,
[`tests/install/debian/nginx-proxy.conf`](../tests/install/debian/nginx-proxy.conf)):

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 80;
    server_name horus.miisp.net;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    http2 on;
    server_name horus.miisp.net;

    ssl_certificate     /etc/letsencrypt/live/horus.miisp.net/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/horus.miisp.net/privkey.pem;
    add_header Strict-Transport-Security "max-age=31536000" always;
    client_max_body_size 2m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host              $host;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host  $host;
        proxy_set_header Upgrade           $http_upgrade;     # WebSocket
        proxy_set_header Connection        $connection_upgrade;
        proxy_read_timeout  3600s;                            # kioscos
        proxy_send_timeout  3600s;
        proxy_buffering     off;
    }
}
```

Importante: usa `$proxy_add_x_forwarded_for` (añade la IP real al final) y nunca pases tal cual un
`X-Forwarded-For` que mande el visitante. Si nginx está en otra máquina, cambia `127.0.0.1:8080`
por `IP_DEL_SERVIDOR:8080` y usa `--trusted-proxies IP_DE_NGINX`.

## 12. Servidor sin Internet (paquete offline)

Para servidores que no pueden descargar de GitHub/GHCR. En un ordenador con Internet descarga de la
página de la versión (<https://github.com/hcdestroyer/horus-flow/releases>):

- `horus-X.Y.Z-linux-amd64.tar.gz` (o `-arm64`): todo Horus, incluidas todas las imágenes;
- `SHA256SUMS`.

Cópialos al servidor (USB, `scp`…) en la misma carpeta y:

```bash
tar -xzf horus-X.Y.Z-linux-amd64.tar.gz horus-X.Y.Z/scripts/bootstrap-debian.sh
bash horus-X.Y.Z/scripts/bootstrap-debian.sh --bundle horus-X.Y.Z-linux-amd64.tar.gz
```

El servidor sigue necesitando los paquetes de Debian y de Docker (un espejo local de Debian sirve;
`DOCKER_APT_URL` apunta a un espejo del repositorio de Docker). Para **actualizar** sin Internet:

```bash
horus-ctl upgrade --bundle /ruta/horus-X.Y.Z-linux-amd64.tar.gz
```

Antes de aplicar se comprueba el SHA-256 con `SHA256SUMS` (y la firma si `cosign` está instalado y
copiaste también `SHA256SUMS.sigstore.json`). La consola mostrará "no comprobado" en las versiones
nuevas; es normal sin Internet (`--no-update-check` lo silencia).

## 13. Problemas frecuentes

| Síntoma | Qué hacer |
| --- | --- |
| `sistema no soportado` | Solo Debian 12 o 13. En Ubuntu u otros, instala Docker a mano y usa `scripts/install.sh`. |
| `apt-get update falló` | Sin Internet o sin acceso a los espejos de Debian/Docker. Revisa DNS y proxy (`/etc/apt/apt.conf.d/`). |
| `requisito(s) no se cumplen` | Poca RAM/CPU/disco o un puerto ocupado (80, 443, 51820). Libéralo o añade `--force` bajo tu responsabilidad. |
| El navegador avisa del certificado | Normal en modo IP: comprueba la huella (`horus-ctl check`) y acepta. |
| Let's Encrypt no emite el certificado | El dominio debe apuntar a este servidor y el **puerto 80** estar abierto desde Internet. |
| Un servicio no está "sano" | `horus-ctl status` y `horus-ctl logs NOMBRE`. Suele ser falta de RAM o disco lleno. |
| Disco lleno (≥ 85 %) | `horus-ctl check` lo marca. Amplía el disco o mueve las copias a otro (`--store-dir`). |
| El router no conecta el túnel | El **UDP 51820** debe llegar directo al servidor (no por el proxy). `horus-ctl check` muestra si WireGuard escucha. |
| `sin módulo wireguard` | Debian lo trae en el kernel. Si es un contenedor o VPS raro, se usa `wireguard-go` (más CPU). |
| Detrás de proxy: login no funciona o la página se queda cargando | Activa **Websockets Support**, usa `Scheme: http` y comprueba `--trusted-proxies` (la IP con la que llega el proxy). |
| La consola avisa de `proxy_untrusted_source` | Alguien entra sin pasar por el proxy: revisa el cortafuegos y `--http-bind`. |
| `upgrade` terminó con VUELTA ATRÁS | La versión anterior quedó funcionando. Revisa el registro que indica (`/opt/horus/upgrade/upgrade-*.log`) y avisa al proveedor. |
| Olvidé la contraseña del superadmin | Si hay otro superadmin, que la restablezca. Si no, consulta [`disaster-recovery.md`](disaster-recovery.md). |
| Hora incorrecta | `timedatectl` debe decir `System clock synchronized: yes`. |

## 14. Qué está probado y qué no

Probado automáticamente (`make test-install-debian`, contenedor Debian 12 y 13 con systemd y
Docker dentro): instalación con el paquete offline, servicios sanos, web y API por HTTPS con el
certificado autogenerado, segunda ejecución sin cambios, arranque tras reiniciar el contenedor,
actualización A→B, actualización a una versión rota con vuelta atrás automática, desinstalación
conservando datos, reinstalación y `--purge`, y el modo detrás de un nginx con TLS. En CI se hace
además con `apt` real contra los espejos de Debian y Docker.

**No probado de verdad** (tenlo en cuenta en la primera instalación):

- la emisión real de un certificado de **Let's Encrypt** (necesita un dominio público);
- un **reinicio real** del servidor (se simula reiniciando el contenedor con systemd);
- la descarga real desde **GHCR** y la verificación de la **firma cosign** (requieren una release
  publicada por el workflow `release.yml`);
- el **arm64** en hardware real (las imágenes y paquetes se publican, pero la prueba es amd64);
- Nginx Proxy Manager en sí (se prueba con nginx con la misma configuración que genera NPM).
