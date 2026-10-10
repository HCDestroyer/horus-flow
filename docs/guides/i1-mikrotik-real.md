# Guía: probar Horus Flow con tu MikroTik real (gate G1, historia I1-27)

> Para quien opera el ISP, sin conocimientos de programación. Tiempo: 1–2 h para instalar y
> conectar; después, unos días de uso para revisar los hallazgos.
> Guía preparada por INT. Historia: I1-27 en [`../backlog/increment-1.md`](../backlog/increment-1.md).

**Lo más importante antes de empezar:** Horus **nunca escribe en tu router**. Todo lo que cambia
en el MikroTik lo pegas tú, a mano, desde un script que puedes leer antes. Para deshacerlo todo
hay otro script (paso 12). Horus solo **lee**: recibe los flujos de tráfico (IPFIX) y, con un
usuario de solo lectura, puede leer los pools para proponerte los prefijos de clientes.

En los ejemplos usamos los datos de tu red:

| Dato | Valor en tu router |
| --- | --- |
| Red (LAN) de los clientes | `10.20.6.0/24` |
| Interfaz de esa LAN | `sfp-sfpplus2` |
| NAT | en el **router principal** (el mismo que va a exportar los flujos) |
| RouterOS | v7, **7.12 o superior** |

Cambia `horus.tu-isp.net`, IPs y nombres por los tuyos cuando aparezcan.

---

## Índice

1. [Qué vas a conseguir](#1-qué-vas-a-conseguir)
2. [Antes de empezar: lista de comprobación](#2-antes-de-empezar-lista-de-comprobación)
3. [Instalar Horus en el servidor](#3-instalar-horus-en-el-servidor)
4. [Primer acceso: contraseña y código de verificación](#4-primer-acceso-contraseña-y-código-de-verificación)
5. [Crear tu ISP, el nodo y el router](#5-crear-tu-isp-el-nodo-y-el-router)
6. [Medir la CPU del router ANTES](#6-medir-la-cpu-del-router-antes)
7. [Generar el script y pegarlo en el router](#7-generar-el-script-y-pegarlo-en-el-router)
8. [Qué debes ver en 2 minutos](#8-qué-debes-ver-en-2-minutos)
9. [Prefijos de clientes: importar del MikroTik (solo lectura)](#9-prefijos-de-clientes-importar-del-mikrotik-solo-lectura)
10. [Medir la CPU DESPUÉS y revisar FastTrack/offload](#10-medir-la-cpu-después-y-revisar-fasttrackoffload)
11. [Usar Horus unos días: tráfico, hallazgos y falsos positivos](#11-usar-horus-unos-días-tráfico-hallazgos-y-falsos-positivos)
12. [La TV del NOC en modo kiosco](#12-la-tv-del-noc-en-modo-kiosco)
13. [Deshacer todo en el router](#13-deshacer-todo-en-el-router)
14. [Problemas frecuentes](#14-problemas-frecuentes)
15. [Decidir: ¿aceptas I1?](#15-decidir-aceptas-i1)

---

## 1. Qué vas a conseguir

Al terminar tendrás:

- Tu router conectado a Horus por un **túnel cifrado WireGuard** que abre el propio router.
- En la ficha del router, el estado **Exportando** (llegan flujos) y los **clientes descubiertos**:
  cada IP de `10.20.6.0/24` que use Internet aparece como un cliente (tipo *Residencial* por defecto).
- El top de clientes, servicios (YouTube, Netflix, WhatsApp…), categorías y organizaciones.
- **Hallazgos de seguridad** explicados ("señales compatibles con…", nunca "infectado"), que puedes
  reconocer, resolver o marcar como **falso positivo**.
- El dashboard **NOC del ISP** en la TV del NOC, en pantalla completa y sin iniciar sesión.

## 2. Antes de empezar: lista de comprobación

**Servidor para Horus** (una máquina o VM dedicada):

- [ ] Debian 12/13 o Ubuntu 22.04/24.04 LTS, recién instalado, con acceso `root` (o `sudo`).
- [ ] 4 núcleos o más, 8 GB de RAM o más, 100 GB libres (mejor un segundo disco para las copias).
- [ ] Docker Engine 24 o superior con el plugin `docker compose`
      (<https://docs.docker.com/engine/install/>).
- [ ] Puertos libres: **80 y 443 TCP** (la web) y **51820 UDP** (WireGuard).
- [ ] Que el router pueda llegar al servidor por **UDP 51820** (si hay un firewall en medio, ábrelo).
- [ ] `git`, `curl`, `openssl` instalados (`sudo apt install -y git curl openssl wireguard-tools`).

**Router MikroTik:**

- [ ] RouterOS **7.12 o superior**: en una terminal, `/system resource print` y mira `version`.
- [ ] Acceso de administrador por **Winbox** (New Terminal) o **SSH**.
- [ ] Salida a Internet hacia el servidor de Horus por UDP 51820.
- [ ] Una **copia de seguridad** antes de tocar nada:
      `/system backup save name=antes-de-horus` y `/export file=antes-de-horus`
      (descarga los dos archivos desde *Files*).
- [ ] Hora correcta (el script activa NTP; los tiempos de los flujos dependen de ella).

**Cómo vas a entrar a Horus** (elige uno):

| Modo | Cuándo usarlo | Qué necesitas |
| --- | --- | --- |
| `domain` | Tienes un dominio propio para Horus (`horus-isp.net`) | El nombre apunta (registro DNS A) a la IP pública del servidor y el puerto 80 es accesible desde Internet (para el certificado de Let's Encrypt) |
| `subdomain` | Usas un subdominio de tu web (`horus.tu-isp.net`) | Igual: registro DNS A hacia el servidor y puerto 80 accesible |
| `ip` | No tienes dominio o es una prueba | Nada más. Horus crea un certificado propio; el navegador mostrará un aviso que aceptas **después de comprobar la huella** que te muestra el instalador |

## 3. Instalar Horus en el servidor

Conéctate al servidor por SSH y ejecuta:

```bash
git clone https://github.com/hcdestroyer/horus-flow
cd horus-flow
sudo bash scripts/install.sh
```

El instalador **pregunta** y comprueba los requisitos. Responde:

1. **Modo de acceso**: `domain`, `subdomain` o `ip` (tabla del paso 2).
2. **Nombre** (solo `domain`/`subdomain`): p. ej. `horus.tu-isp.net`, y un **email** para avisos de
   Let's Encrypt.
3. **IP pública** (solo `ip`): la que usará el router para llegar al servidor.
4. **Rango de túneles**: deja el propuesto `10.255.0.0/16` salvo que ya uses esa red. **Nunca**
   `100.64.0.0/10` (es el CGNAT de los ISP; el instalador lo rechaza).
5. **Email del superadministrador**: el tuyo. La contraseña inicial la genera y te la muestra (o
   usa la tuya con `--admin-password-file`).

Si prefieres no responder preguntas, los tres modos en una línea:

```bash
# Dominio propio (o subdominio: --mode subdomain)
sudo bash scripts/install.sh --yes --mode domain --domain horus.tu-isp.net --acme-email noc@tu-isp.net \
     --admin-email noc@tu-isp.net --admin-password-file /root/clave-horus

# Solo IP (sin dominio)
sudo bash scripts/install.sh --yes --mode ip --public-ip 203.0.113.20 \
     --admin-email noc@tu-isp.net --admin-password-file /root/clave-horus
```

(`/root/clave-horus` es un archivo que creas tú con la contraseña inicial dentro.)

Al terminar el instalador muestra un resumen. **Guarda estas tres cosas:**

- La **URL** (p. ej. `https://horus.tu-isp.net` o `https://203.0.113.20`).
- En modo `ip`, la **huella SHA-256** del certificado (la compararás en el navegador).
- El **paquete de secretos** (`/root/horus-secrets-….tar.gz.enc`) y su **frase de descifrado**:
  cópialos **fuera del servidor** (dos sitios), bórralos del servidor y marca el paquete como
  guardado:

  ```bash
  sudo /opt/horus/bin/install.sh --confirm-bundle
  ```

Comprueba que todo está sano (debe terminar en `check: OK`):

```bash
sudo /opt/horus/bin/install.sh --check
```

> Si algo falla, vuelve a ejecutar `sudo bash scripts/install.sh`: es seguro repetirlo (no cambia
> contraseñas ni secretos). Si sigue fallando, guarda la salida y abre un issue.

## 4. Primer acceso: contraseña y código de verificación

1. Abre la URL en el navegador. En modo `ip` verás un aviso de certificado: abre los detalles del
   certificado, comprueba que la **huella SHA-256** es la que mostró el instalador y acéptalo.
2. Entra con el email del superadministrador y la contraseña inicial.
3. Horus te obliga a **cambiar la contraseña** y a activar el **código de verificación (TOTP)**:
   escanea el QR con una app de autenticación (Google Authenticator, Aegis, 1Password…), escribe
   el código de 6 dígitos y **guarda los códigos de recuperación** en un lugar seguro.
4. Vuelve a entrar con la contraseña nueva y el código de la app.

## 5. Crear tu ISP, el nodo y el router

1. **ISP:** en la consola de plataforma (*Plataforma › ISP*) crea tu ISP: nombre, país y zona
   horaria. Entra en él con el selector de ISP de arriba.
2. **Nodo:** en *Nodos*, crea el nodo donde está el router (p. ej. "Nodo Centro", código `CEN`).
3. **Router:** en el nodo, pulsa **Conectar router** y rellena nombre (p. ej. `rt-centro`), modelo
   y versión de RouterOS. El primer router de un nodo es el **principal**.
4. Horus asigna al router una **IP de túnel** (una `/32` del rango de túneles). La verás en la
   ficha del router, pestaña *Conexión*.

## 6. Medir la CPU del router ANTES

Traffic Flow (la exportación de flujos) consume CPU. Mide **antes** de activarlo para poder
comparar. En la terminal del router, en una hora de tráfico normal:

```routeros
/system resource print
/system resource cpu print
```

Apunta la carga **de cada núcleo** (columna `LOAD` de `cpu print`) y `free-memory`. Repite la
medida 3 veces con un minuto de diferencia y anota la media. Si quieres ver qué consume la CPU:

```routeros
/tool profile duration=30s
```

| Momento | Núcleo más cargado | Media de núcleos | free-memory |
| --- | --- | --- | --- |
| Antes | | | |
| Después (paso 10) | | | |

## 7. Generar el script y pegarlo en el router

1. En la ficha del router, pestaña **Conexión**, pulsa **Generar script** (elige RouterOS 7.12 o la
   rama long-term según tu versión).
2. El script **solo se muestra una vez**: contiene la contraseña del usuario de solo lectura y un
   **token de un solo uso válido 24 h**. Cópialo o descárgalo. Si lo pierdes, pulsa *Regenerar*.
3. **Léelo antes de pegarlo.** Hace esto, en este orden:
   - `0)` activa NTP;
   - `1)` crea la interfaz `wg-horus` (la **clave privada se genera en el router y nunca sale**),
     su IP de túnel `/32`, el peer hacia Horus con `persistent-keepalive=25s` y una ruta a la red
     de servicios de Horus por el túnel;
   - `2)` reglas de firewall **al principio de `input`** que permiten SNMP, API y ping **solo desde
     Horus y solo por el túnel**;
   - `3)` el grupo `horus-ro` y el usuario de **solo lectura** (sin `write`, `policy` ni
     `sensitive`);
   - `4)` un certificado para la API por HTTPS;
   - `5)` SNMPv3 de solo lectura;
   - `6)` **Traffic Flow** con IPFIX **sin muestreo**, `active-flow-timeout=1m`,
     `inactive-flow-timeout=15s`, destino = IP del colector de Horus por el túnel, puerto 4739, y
     los **campos NAT** activados (`nat-src-address`, `nat-dst-address`…). Con NAT en tu router
     principal son imprescindibles: sin ellos la bajada no se puede atribuir a `10.20.6.x`;
   - `7)` envía a Horus **solo la clave pública** del router con el token.
4. Abre **New Terminal** en Winbox (o SSH) y pega el script completo.

**Importante — `comment` en el destino de Traffic Flow.** En tu router, la orden
`/ip traffic-flow target add … comment="horus"` **falló** (RouterOS no acepta `comment` en ese
menú). Los scripts que genera Horus ya **no** lo llevan. Si tienes un script antiguo que sí lo
lleva, borra ` comment="horus"` de esa línea antes de pegarla. La línea correcta es así (con tus
valores):

```routeros
/ip traffic-flow target add dst-address=10.255.0.1 port=4739 version=ipfix \
    src-address=10.255.1.7 v9-template-refresh=20 v9-template-timeout=1m
```

5. Al final la terminal debe mostrar `HORUS_ROUTER_PUBKEY=…` y
   `HORUS: clave publica enviada; el tunel se activa en segundos.`

**Si ya exportas flujos a otro colector**, el script no cambia los parámetros globales de Traffic
Flow (muestra `HORUS AVISO: …`). Revisa tú que `active-flow-timeout` sea `1m`:
`/ip traffic-flow print`. Si es mayor, los datos llegarán tarde y la detección perderá precisión.

**Registrar la clave pública.** No tienes que copiar nada de vuelta: el propio script la envía con
el token. Si tu router no tiene salida HTTPS hacia Horus y el paso 7 da error, abre un issue con el
mensaje; como alternativa, la clave pública está en `HORUS_ROUTER_PUBKEY=…` o con
`/interface wireguard print` (columna `public-key`).

## 8. Qué debes ver en 2 minutos

En la ficha del router, pestaña *Conexión*, el asistente avanza solo:

1. **Clave del router recibida** — segundos después de pegar el script.
2. **Handshake del túnel** — "Túnel activo · último handshake hace N s".
3. **Primer flujo recibido** — el router pasa a **Exportando**.
4. **Primeros clientes descubiertos** — solo si el nodo ya tiene prefijos de clientes (paso 9).
   Mientras no los tenga verás "Llegan flujos, pero este nodo no tiene prefijos de clientes".

En el router puedes comprobar el túnel y la exportación:

```routeros
/interface wireguard peers print detail where comment="horus"
/ip traffic-flow print
/ip traffic-flow target print
```

En `peers`, `last-handshake` debe ser de hace pocos segundos y `rx`/`tx` deben crecer.

**Criterio de aceptación:** desde que pegas el script hasta ver el router **Exportando** y los
**clientes descubiertos**: **2 minutos o menos**. Apunta el tiempo real.

## 9. Prefijos de clientes: importar del MikroTik (solo lectura)

Horus necesita saber qué redes son de clientes para convertir cada IP en un cliente. Tu LAN de
clientes es `10.20.6.0/24` en `sfp-sfpplus2`.

**Opción A — importar (recomendada).** En la ficha del router, pestaña **Prefijos de clientes**,
pulsa **Importar del MikroTik**. Horus lee por el túnel, con el usuario de solo lectura, los pools
y las redes de interfaz del router (no cambia nada). Verás una lista:

- marca `10.20.6.0/24` (aparecerá como *Red de interfaz* de `sfp-sfpplus2` o como *Pool IPv4* si
  la usas en DHCP) con el rol **Clientes**;
- deja como **Infraestructura** las redes de enlaces, gestión o servidores propios;
- las IPs públicas del NAT de tu router, si aparecen, como **Infraestructura**;
- pulsa **Confirmar importación**.

**Opción B — a mano.** En la misma pestaña, **Añadir prefijo**: `10.20.6.0/24`, rol *Clientes*,
asignación *Dinámica (pool)* si es DHCP.

**Opción C — modo descubrimiento.** Si no declaras nada, Horus propone tras un rato las redes que
ve del lado de los clientes. Acepta la propuesta como *Clientes*.

Los flujos anteriores a declarar el prefijo no se reasignan: los clientes aparecen con el tráfico
nuevo (en 1–2 minutos).

## 10. Medir la CPU DESPUÉS y revisar FastTrack/offload

**CPU.** Con Traffic Flow ya activo y a la misma hora del día que la medida del paso 6, repite:

```routeros
/system resource print
/system resource cpu print
```

Anota la tabla del paso 6. Orientación: preocúpate si algún núcleo queda por encima del **70 %
sostenido** durante 5 minutos. Si el aumento no te parece aceptable, anótalo: se registra como
riesgo para I2 (no se arregla bajando el muestreo, porque se perderían justo los escaneos y
contactos cortos que buscamos).

**FastTrack y offload por hardware.** El tráfico acelerado por hardware **no pasa por Traffic
Flow** y Horus no lo ve. Revisa:

```routeros
# ¿Hay FastTrack? (por software, RouterOS v7 sí lo contabiliza en Traffic Flow)
/ip firewall filter print where action=fasttrack-connection
# ¿Offload L3 por hardware? (CCR2116/2216 y switches con L3HW)
/interface ethernet switch print
# ¿Bridge con offload por hardware en la LAN de clientes?
/interface bridge port print where interface=sfp-sfpplus2
```

- Si `/interface ethernet switch print` muestra `l3-hw-offloading=yes`, el tráfico enrutado por
  hardware no generará flujos: los clientes aparecerán con menos tráfico del real. Coméntalo antes
  de desactivarlo (afecta al rendimiento del router).
- En `bridge port`, `hw=yes` solo afecta al tráfico entre clientes del mismo bridge (no a Internet).
- Comprobación sencilla: compara el tráfico de un cliente en Horus con el que ves en el router
  (`/interface monitor-traffic sfp-sfpplus2`, o *Torch* para una IP concreta). Si Horus ve mucho
  menos, sospecha de offload.

## 11. Usar Horus unos días: tráfico, hallazgos y falsos positivos

- **Clientes:** lista de IPs con tipo, origen y última actividad. Si una IP es de un cliente
  comercial, ábrela y cambia el tipo a *Comercial* con un motivo.
- **Tráfico:** top de clientes, servicios, categorías y organizaciones.
- **Seguridad › Hallazgos:** cada hallazgo dice qué cliente, qué señal (contacto con un C2
  conocido, escaneo, SMTP saliente directo, participación en DDoS…), sus razones, la evidencia y la
  confianza.

Para cada hallazgo que revises:

1. Ábrelo y lee las razones y la evidencia.
2. Si lo reconoces y vas a actuar: **Reconocer**; cuando esté corregido (p. ej. el cliente cambió
   las contraseñas del equipo): **Resolver**.
3. Si **no** es una señal real (un servidor del cliente, una herramienta del propio ISP…): pulsa
   **Falso positivo**, escribe **por qué** (obligatorio) y confirma. Así Horus aprende qué es ruido
   en tu red y no vuelve a avisar de lo mismo durante un tiempo.

Apunta cuántos hallazgos de cada tipo eran reales y cuántos falsos positivos: INT publicará la tasa
por tipo (criterio 2 de I1-27).

## 12. La TV del NOC en modo kiosco

La TV **no inicia sesión**: es un dispositivo registrado, de solo lectura, que se puede revocar.

1. En Horus: *Administración › Pantallas NOC › Nuevo kiosco*. Nombre (p. ej. "TV sala NOC"),
   dashboards o lista de reproducción ("NOC del ISP" y "Seguridad"), y en **Red permitida** la red
   del NOC (recomendado). Deja **Mostrar datos de clientes** desactivado salvo que la TV no la vea
   nadie ajeno. Pulsa **Crear y generar código**.
2. Aparece un **código de 8 caracteres** (un solo uso, 10 minutos).
3. En la TV (navegador en pantalla completa, p. ej. Chromium en modo kiosco o el navegador de la
   Smart TV), abre `https://<tu-horus>/kiosk`, escribe el código y pulsa **Conectar**.
4. Pulsa una vez para pantalla completa. La TV rota entre dashboards, se refresca sola y, si se va
   la red, muestra "Sin conexión desde las HH:MM" con los últimos datos y se recupera sola.
5. Para dejarla de usar: *Pantallas NOC › Revocar*.

Criterio: déjala **24 h** sin tocarla y comprueba que sigue mostrando datos frescos.

## 13. Deshacer todo en el router

En la ficha del router, **Script de desinstalación**: cópialo y pégalo en la terminal del router.
Quita solo lo que creó Horus. Si prefieres hacerlo a mano, son estas órdenes (sustituye la IP del
colector y la IP de túnel por las tuyas, las ves en el script de alta):

```routeros
# 1) Destino de Traffic Flow de Horus (no lleva comment: se busca por destino, puerto y origen)
/ip traffic-flow target remove [find where dst-address=10.255.0.1 && port=4739 && src-address=10.255.1.7]
# Si no queda ningún otro destino, apaga Traffic Flow:
:if ([:len [/ip traffic-flow target find]] = 0) do={ /ip traffic-flow set enabled=no }

# 2) SNMPv3 y usuario de solo lectura
/snmp community remove [find where comment="horus"]
/user remove [find where comment="horus"]
/user group remove [find where comment="horus" && name="horus-ro"]

# 3) Certificados
/ip service set [find where certificate="horus-api"] certificate=none
/certificate remove [find where name="horus-api" || name~"^horus-ca"]

# 4) Firewall, ruta, dirección y túnel
/ip firewall filter remove [find where comment~"^horus"]
/ip route remove [find where comment="horus"]
/ip address remove [find where comment="horus"]
/interface wireguard peers remove [find where comment="horus"]
/interface wireguard remove [find where name="wg-horus"]
```

No se restauran los parámetros globales de Traffic Flow (`active-flow-timeout`, `cache-entries`)
que hubieras cambiado: compáralos con tu `/export` del paso 2. Comprueba que no queda nada:

```routeros
/export where comment~"horus"
/ip traffic-flow target print
```

Para quitar Horus del **servidor**: `sudo /opt/horus/bin/install.sh --uninstall` (conserva datos y
secretos); con `--purge` lo borra todo.

## 14. Problemas frecuentes

| Síntoma | Qué revisar |
| --- | --- |
| Al pegar el script, error en `/ip traffic-flow target add` | El script lleva ` comment="horus"` en esa línea (versión antigua): bórralo de la línea y pégala de nuevo. Después pega el resto del script desde esa línea. |
| Error `failure: already have such entry` | Ya pegaste esa parte antes. Ejecuta el script de desinstalación (paso 13) y vuelve a pegar un script **nuevo** (*Regenerar*: el token solo sirve una vez). |
| "Esperando la clave del router…" no avanza | El paso 7 del script no llegó a Horus: ¿el router resuelve el nombre de Horus y tiene salida HTTPS? En modo `ip`, ¿se importó el certificado (`/certificate print`)? Mira el error en la terminal. Si el token caducó (24 h) o se usó, *Regenerar*. |
| Clave recibida pero sin handshake | UDP 51820 bloqueado entre router y servidor; IP pública o nombre del endpoint incorrecto; hora del router muy desfasada. En el router: `/interface wireguard peers print detail`. En el servidor: `sudo wg show`. |
| Túnel activo pero el router no pasa a *Exportando* | `/ip traffic-flow print` → `enabled=yes`; `/ip traffic-flow target print` → destino = IP del colector y `src-address` = IP de túnel del router. Si el router tiene reglas `output` que bloquean, permite UDP 4739 hacia la red de servicios de Horus. |
| *Exportando* pero sin clientes | Falta declarar `10.20.6.0/24` como prefijo de **Clientes** (paso 9). |
| Solo aparecen subidas, no bajadas | Faltan los campos NAT de IPFIX: `/ip traffic-flow ipfix print` debe mostrar `nat-src-address=yes` y `nat-dst-address=yes`. |
| Estado *Reloj desfasado* | La hora del router no es correcta: `/system ntp client print`, `/system clock print`. |
| Estado *Con pérdidas* | Se pierden paquetes de flujos por el camino o la caché del router se llena: sube `cache-entries` (`/ip traffic-flow set cache-entries=256k`) y revisa la calidad del enlace. |
| El router pasa a *Silencioso* | No llegan flujos desde hace más de 2 minutos: túnel caído, Traffic Flow desactivado o servidor caído. Es el comportamiento esperado si apagas Traffic Flow. |
| Clientes con mucho menos tráfico del real | Offload por hardware (paso 10). |
| La TV muestra "Sin conexión" | Red de la TV fuera de la *Red permitida* del kiosco, o servidor caído. Se recupera sola al volver. |
| El navegador no acepta el certificado (modo `ip`) | Es normal: compara la huella con la de `sudo /opt/horus/bin/install.sh --check` y acéptalo. |

Si algo no está en la tabla: guarda la salida de `sudo /opt/horus/bin/install.sh --check`, una
captura de la ficha del router y lo que mostró la terminal del MikroTik, y abre un issue.

## 15. Decidir: ¿aceptas I1?

Repasa los criterios de I1-27 y anota tu resultado en el issue de seguimiento del incremento:

| # | Criterio | Tu resultado |
| --- | --- | --- |
| 1 | De pegar el script a ver el router *Exportando* y clientes descubiertos en **≤ 2 min** | |
| 2 | Tras varios días, reconoces los hallazgos y resuelves o marcas falsos positivos | |
| 3 | El aumento de CPU del router (pasos 6 y 10) es aceptable | |
| 4 | La TV en modo kiosco sigue con datos frescos tras **24 h** sin tocarla | |
| 5 | Decisión: "I1 aceptado" o issues con lo que falta | |

Para aceptar, comenta **"I1 aceptado"** en el issue de seguimiento.
