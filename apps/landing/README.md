# Landing comercial de Horus Flow

Página pública de venta de Horus Flow, de **Connection And Solutions Company, Sociedad Anónima
(C&S Company)**, Guatemala. Contacto: info@kns.gt. Es una app aparte del frontend del producto
(`apps/frontend`): no contiene ni lee datos de ningún ISP.

- **Stack:** Nuxt 4 (SSR con servidor Nitro) + Nuxt UI v4 + Tailwind 4, pnpm.
- **Idiomas:** español (`/`, principal, es-GT) e inglés (`/en`), con @nuxtjs/i18n.
- **Páginas:** portada (`/`, `/en`), compra (`/comprar`, `/en/buy`), legales
  (`/legal/aviso-legal|privacidad|terminos`, `/en/legal/notice|privacy|terms`) y el **panel de
  administración** en `/admin` (solo español, sin enlace público, `noindex`, fuera del sitemap).
- **Datos:** SQLite (`node:sqlite`, integrado en Node ≥ 22.13; sin compilar nada) en `DATA_DIR`, con migraciones versionadas
  ([`server/lib/migrations.ts`](server/lib/migrations.ts)): planes y precios, ajustes,
  configuración de pagos, solicitudes, administradores, sesiones y auditoría.
- **Servidor:** `GET /api/site` (catálogo, ajustes y métodos de pago públicos), `POST /api/lead`
  (demo y contacto), `POST /api/purchase` (solicitud de compra), `/api/checkout/**` (PayPal, link
  Neo, transferencia), `POST /api/payments/paypal/webhook`, `/api/admin/**` (panel),
  `/sitemap.xml` y `/robots.txt`.
- **Diseño:** Apple HIG / Liquid Glass (`.claude/skills/apple-hig`); revisión en
  [`docs/design-review.md`](docs/design-review.md) y capturas en [`docs/screenshots/`](docs/screenshots/).

## Desarrollo

```bash
cd apps/landing
pnpm install
MAIL_TRANSPORT=file DATA_DIR=.data pnpm dev   # http://localhost:3000, correos en .outbox/*.json
```

En desarrollo, sin `DATA_KEY_FILE`, se genera una clave de cifrado en `DATA_DIR/dev-data.key`.
Para entrar al panel en local crea un administrador (ver [Panel](#panel-de-administración)) y
abre `http://localhost:3000/admin`: Chromium y Firefox aceptan la cookie `__Host-` (Secure) en
`localhost`.

| Orden                      | Qué hace                                                                                                                                                                                                                                                                                                                                 |
| -------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `pnpm lint`                | ESLint + Prettier                                                                                                                                                                                                                                                                                                                        |
| `pnpm typecheck`           | `nuxt typecheck`                                                                                                                                                                                                                                                                                                                         |
| `pnpm test`                | Vitest: formularios, antispam, rate limit, IP real, correo simulado, webhook; migraciones, edición de precios y caché; TOTP, bloqueo, CSRF, sesión y permisos de `/api/admin`; cálculo del importe; PayPal simulado (crear, capturar, webhook verificado y no verificado); cifrado de credenciales                                       |
| `pnpm build && pnpm start` | build de producción y servidor Node (`.output/server/index.mjs`)                                                                                                                                                                                                                                                                         |
| `pnpm e2e`                 | Playwright: navegación, idioma, demo y compra con SMTP simulado, SEO, axe (claro/oscuro, HTML del servidor y todas las páginas del panel), objetivos táctiles, 320 px; login con TOTP, configurar pagos, cambiar un precio y verlo en la web, compra con PayPal simulado, link Neo y transferencia. `E2E_NO_BUILD=1` reutiliza `.output` |
| `pnpm screenshots`         | capturas de la landing (1440/768/390, claro y oscuro), del panel y de la compra con los tres métodos → `docs/screenshots/`                                                                                                                                                                                                               |
| `pnpm images`              | regenera las capturas optimizadas (AVIF/WebP, varios anchos) y la imagen Open Graph desde `apps/frontend/docs/screenshots/tour`                                                                                                                                                                                                          |

Playwright usa el Chromium de `PLAYWRIGHT_BROWSERS_PATH` (en el contenedor de desarrollo,
`/opt/pw-browsers`); no hace falta `playwright install` en local. En CI se instala.

## Variables

Todas son de tiempo de ejecución (no hace falta reconstruir). Ejemplo completo en
[`.env.example`](.env.example).

| Variable                                         | Por defecto                            | Para qué                                                                                                       |
| ------------------------------------------------ | -------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| `NUXT_PUBLIC_SITE_URL`                           | `https://horusflow.kns.gt`             | URL pública canónica, sin barra final: canonical, hreflang, Open Graph, JSON-LD, sitemap, URL del webhook      |
| `DATA_DIR`                                       | `.data` (imagen: `/data`)              | carpeta de la base de datos `horus-landing.sqlite` (volumen persistente)                                       |
| `DATA_KEY_FILE`                                  | — (**obligatoria en producción**)      | archivo con la clave AES-256 (32 bytes en base64/hex) que cifra las credenciales de PayPal y los secretos TOTP |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD_FILE`             | —                                      | primer administrador, solo si la base de datos aún no tiene ninguno                                            |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_SECURE`          | — / 587 / `false` (465 → `true`)       | SMTP para avisos y confirmaciones (servidor de kns.gt por definir)                                             |
| `SMTP_USER`, `SMTP_PASS_FILE` (o `SMTP_PASS`)    | —                                      | credenciales SMTP; la contraseña, mejor como archivo                                                           |
| `MAIL_FROM`                                      | `Horus Flow <info@kns.gt>`             | remitente (debe pasar SPF/DKIM de kns.gt, ver [Correo](#correo-spf-dkim-y-dmarc-de-knsgt))                     |
| `SALES_EMAIL`                                    | `info@kns.gt`                          | destino de los avisos: solicitudes y pagos confirmados                                                         |
| `MAIL_CONFIRM_CUSTOMER`                          | `true`                                 | correo de confirmación al cliente                                                                              |
| `MAIL_TRANSPORT`                                 | `smtp` si hay `SMTP_HOST`, si no `log` | `file` = SMTP simulado (`MAIL_OUTBOX_DIR`, por defecto `.outbox`); `log` = solo registra                       |
| `LEADS_WEBHOOK_URL`, `LEADS_WEBHOOK_SECRET_FILE` | —                                      | webhook opcional para un CRM: POST JSON con firma `X-Horus-Signature: sha256=<HMAC>`                           |
| `TRUSTED_PROXIES`                                | —                                      | IPs/CIDR de los proxies de confianza; solo de ellos se acepta `X-Forwarded-For`                                |
| `RATE_LIMIT_MAX`, `RATE_LIMIT_WINDOW_SECONDS`    | 8 / 600                                | envíos por IP real y ventana                                                                                   |
| `FORM_MIN_FILL_SECONDS`                          | 3                                      | tiempo mínimo entre mostrar y enviar un formulario                                                             |
| `ROBOTS_DISALLOW_ALL`                            | —                                      | `1` en entornos de pruebas: `robots.txt` lo bloquea todo                                                       |
| `ADMIN_COOKIE_INSECURE`                          | —                                      | solo desarrollo por HTTP fuera de localhost: cookie sin `Secure` ni `__Host-` (ignorada en producción)         |
| `PAYPAL_API_BASE`                                | —                                      | solo pruebas: API de un PayPal simulado (ignorada en producción)                                               |

Los secretos (`DATA_KEY_FILE`, `ADMIN_PASSWORD_FILE`, `SMTP_PASS_FILE`) se pasan como archivos;
[`compose.example.yaml`](compose.example.yaml) los monta como _secrets_ de Docker. Las
credenciales de PayPal no van en variables: se escriben en el panel y se guardan cifradas.

**Sin SMTP:** las solicitudes se guardan en la base de datos y se ven en el panel; solo se
registra que no se envió el correo. Sin base de datos, SMTP ni webhook (no es el caso de la
imagen), en producción las rutas responderían **503** para no perder solicitudes en silencio.

**Datos personales en logs:** solo la referencia, el plan, el país y el correo enmascarado
(`a***@dominio`); nunca nombres, teléfonos ni mensajes. La IP se usa en memoria para el rate
limit y en los logs va truncada.

**Antispam:** honeypot (`website`, invisible y fuera del tabulado: al bot se le responde OK sin
enviar nada), tiempo mínimo de llenado, y rate limit en memoria por IP real. Con varias
réplicas cada una cuenta por separado.

## Instalación automática (Debian/Ubuntu)

`scripts/install-landing.sh` hace todo lo de la sección siguiente: instala Docker (repositorio
oficial) si falta, genera los secretos con los permisos correctos, construye la imagen, arranca la
landing detrás de Nginx Proxy Manager (o en un puerto), crea el primer administrador y muestra los
pasos que faltan en NPM.

```bash
git clone https://github.com/hcdestroyer/horus-flow.git && cd horus-flow
git checkout claude/horus-landing
sudo bash apps/landing/scripts/install-landing.sh            # pregunta dominio y correo
# o sin preguntas:
sudo bash apps/landing/scripts/install-landing.sh install --yes \
  --domain horusflow.kns.gt --admin-email info@kns.gt \
  --smtp-host smtp.kns.gt --smtp-user info@kns.gt --smtp-pass-file /root/smtp_pass --backup-cron
```

- **Modo** `--mode npm` (por defecto si detecta Nginx Proxy Manager): se une a su red de Docker
  sin publicar puertos y calcula `TRUSTED_PROXIES` con la subred de esa red. `--mode port --port
127.0.0.1:3000`: publica un puerto para otro proxy.
- **Contraseña del panel:** con `--admin-password-file` (≥ 12 caracteres) o, si no, la genera y la
  muestra **una sola vez** al terminar. En el primer acceso a `/admin` se registra el TOTP.
- **Todo queda en** `/opt/horus-landing` (`--install-dir`): `compose.yaml`, `.env` sin secretos,
  `secrets/` (propietario 65532, modo 0400), `backups/` y una copia del propio script.
  **Copia `secrets/data.key` fuera del servidor.**
- **Repositorio privado:** si no ejecutas el script desde una copia del repositorio, lo clona con
  `--repo`/`--branch` y `--token-file` (token de solo lectura; no se guarda en `.git/config`).

Mantenimiento (con `sudo bash /opt/horus-landing/install-landing.sh …`):

| Orden                                                                  | Qué hace                                                                                                             |
| ---------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `status`                                                               | estado, salud y última copia                                                                                         |
| `logs [-f]`                                                            | registros del contenedor                                                                                             |
| `update`                                                               | copia de la base de datos, nueva versión del código, reconstrucción y arranque (migraciones automáticas)             |
| `backup`                                                               | copia consistente en caliente de la base de datos a `backups/` (se guardan 14; `--backup-cron` la programa a diario) |
| `restore ARCHIVO`                                                      | comprueba la copia, guarda la actual y restaura; si falla, la landing sigue con la base anterior                     |
| `admin list` · `admin reset-totp --email c` · `admin create --email c` | administradores del panel                                                                                            |
| `install --smtp-host …`                                                | cambia opciones (SMTP, dominio…) conservando secretos y datos                                                        |
| `uninstall` · `uninstall --purge`                                      | quita el contenedor (conserva datos y secretos) · lo borra todo                                                      |

## Despliegue manual detrás de Nginx Proxy Manager

1. Construye y arranca (imagen Node distroless, usuario `nonroot`, raíz de solo lectura):

   ```bash
   cd apps/landing
   cp .env.example .env      # SMTP, TRUSTED_PROXIES…
   mkdir -p secrets && chmod 700 secrets
   openssl rand -base64 32 > secrets/data.key          # ¡guárdala también fuera del servidor!
   printf '%s' 'contraseña-larga-del-primer-admin' > secrets/admin_password
   : > secrets/smtp_pass                               # o la contraseña SMTP
   chown 65532:65532 secrets/* && chmod 400 secrets/*   # solo los lee el usuario del contenedor
   docker compose -f compose.example.yaml up -d --build
   ```

   El compose une el contenedor a la red de NPM (`PROXY_NETWORK`, por defecto `npm_default`)
   sin publicar puertos, monta el volumen de datos `landing-data` en `/data` y los secretos en
   `/run/secrets/`. Tras el primer arranque puedes borrar `secrets/admin_password` (el hash ya
   está en la base de datos).

2. En NPM, **Proxy Host** → dominio → _Forward_ `http://horus-landing:3000`; pestaña SSL: Let's
   Encrypt, _Force SSL_ y _HTTP/2_. Activa _Block Common Exploits_.
3. `TRUSTED_PROXIES` debe contener la IP o subred desde la que NPM llega al contenedor (la red
   de Docker, p. ej. `172.16.0.0/12`). NPM añade `X-Forwarded-For`; sin esa variable el rate
   limit vería a todos los clientes como la IP de NPM, y con una subred demasiado amplia
   (`0.0.0.0/0`) cualquiera podría falsificar su IP.
4. Animaciones (`/motion/*.mp4|webm`): Safari solo reproduce vídeo servido con peticiones de
   rango (`206 Partial Content`) y el servidor Nitro responde siempre `200`. En la pestaña
   _Advanced_ del Proxy Host añade `proxy_force_ranges on;` para que NPM sirva los rangos. Sin
   eso, en Safari se queda el póster estático (no se rompe nada). Los vídeos se generan en
   [`../landing-motion/`](../landing-motion/README.md).
5. Comprueba `https://tu-dominio/robots.txt` y `https://tu-dominio/sitemap.xml` (deben mostrar
   tu dominio) y envía una solicitud de demo de prueba.

La salud del contenedor se comprueba con `/robots.txt`. El HTML se sirve comprimido (brotli o
gzip) y los recursos estáticos precomprimidos; si NPM comprime también, no hay doble compresión.

## Panel de administración

`https://horusflow.kns.gt/admin` (no enlazado desde la web, `noindex`, `Disallow: /admin`, fuera
del sitemap). Secciones: **Solicitudes** (lista con filtros, detalle, cambio de estado con nota,
exportación CSV), **Precios y planes**, **Pagos**, **Ajustes** (contacto, soporte, banner,
administradores, mi cuenta) y **Auditoría**.

### Primer administrador

- **Con variables:** `ADMIN_EMAIL` + `ADMIN_PASSWORD_FILE` (mínimo 12 caracteres). Al arrancar,
  si la base de datos no tiene administradores, se crea ese. Si ya hay alguno, no hace nada.
- **Con la línea de órdenes** (en el contenedor o en local tras `pnpm build`):

  ```bash
  # la contraseña se lee de la entrada estándar (o --password-file archivo)
  docker exec -i horus-landing /nodejs/bin/node .output/server/index.mjs \
    admin create --email info@kns.gt --name "C&S Company" < secrets/admin_password
  docker exec horus-landing /nodejs/bin/node .output/server/index.mjs admin list
  docker exec horus-landing /nodejs/bin/node .output/server/index.mjs admin reset-totp --email info@kns.gt
  docker exec horus-landing /nodejs/bin/node .output/server/index.mjs admin disable --email ex@kns.gt
  ```

### Acceso

1. Correo y contraseña (scrypt N=2¹⁵, r=8, p=1; mínimo 12 caracteres).
2. **TOTP obligatorio.** La primera vez se da de alta escaneando el QR (o escribiendo la clave) con
   Google Authenticator, Microsoft Authenticator, 1Password…, y se entregan **10 códigos de
   recuperación** de un solo uso (se pueden regenerar en Ajustes). Después, cada acceso pide el
   código de 6 dígitos o un código de recuperación. Un código TOTP no se acepta dos veces.
3. Sesión en la cookie `__Host-hf_admin` (`HttpOnly`, `Secure`, `SameSite=Strict`, `Path=/`):
   10 min para el segundo paso, 30 min de inactividad, 8 h como máximo; el identificador se
   rota al subir de nivel y cada 10 min. Cambiar la contraseña, desactivar a alguien o
   restablecer su TOTP cierra sus sesiones.
4. **CSRF:** toda petición que cambia algo lleva `X-CSRF-Token` (token de la sesión; en el login,
   doble envío con la cookie `__Host-hf_admin_csrf`) y se comprueba `Origin`/`Sec-Fetch-Site`.
5. **Rate limit y bloqueo progresivo:** 10 intentos de login por IP cada 5 min; tras 5 fallos
   por correo (o 20 por IP) bloqueo de 1, 2, 4… minutos, hasta 1 h. También para el TOTP.
6. **Auditoría:** cada acceso, fallo y cambio queda con quién, cuándo, la IP truncada y el antes
   y el después (nunca secretos).

Toda ruta de `/api/admin/**` pasa por [`server/middleware/admin.ts`](server/middleware/admin.ts)
(sesión completa salvo login/CSRF/estado); un test recorre todas las rutas del directorio y
comprueba que ninguna responde sin sesión.

**Si se pierde el acceso** (teléfono y códigos): `admin reset-totp --email …` desde la línea de
órdenes y volver a dar de alta el TOTP.

## Editar precios

En **/admin › Precios y planes**, sin tocar código ni redesplegar: importes por moneda (USD, GTQ)
y periodo (mensual, anual) con hasta 2 decimales, nombre y descripción ES/EN, lo que incluye
cada plan y toda licencia, plan destacado, visible/oculto, orden, moneda y periodo por defecto y
"Mostrar importes" (si se desactiva, la web vuelve a "Precio de lanzamiento: solicita
cotización" y no se puede pagar).

- **Vista previa** con el mismo componente de la web antes de **Publicar**.
- Cada publicación es una **versión** con fecha, autor y nota; cualquiera se puede **ver** o
  **restaurar** (se publica como versión nueva).
- La web lee el catálogo de la base de datos con una caché de 30 s que se invalida al publicar:
  el cambio se ve en la siguiente carga de la página (≤ 30 s si hubiera varios procesos).
- El importe de una compra se fija en el servidor al crear la solicitud, desde la base de datos;
  el navegador nunca envía importes.
- [`app/config/pricing.ts`](app/config/pricing.ts) es solo la **semilla** de la primera
  arrancada con la base de datos vacía (precios aprobados: Pequeño 149/1 490 USD, Mediano
  399/3 990, Grande 990/9 900, Enterprise a medida, y sus equivalentes en GTQ). Los planes son
  fijos (`small`, `medium`, `large`, `enterprise`, ligados a los tamaños del instalador); el panel
  los edita y oculta, no crea planes nuevos.

## Editar textos

- Textos de la página: [`i18n/locales/es.json`](i18n/locales/es.json) y
  [`en.json`](i18n/locales/en.json) (mismas claves; un test lo verifica). En vue-i18n `@`, `{`,
  `}` y `|` son especiales: el correo va como parámetro `{email}`.
- Datos del vendedor y rutas por idioma: [`app/config/site.ts`](app/config/site.ts).
- Textos legales (**borradores en español pendientes de revisión legal**, con marcas
  `[POR CONFIRMAR]`: registro mercantil, NIT del titular, plazos de conservación, impuestos y
  devoluciones): [`app/content/legal.ts`](app/content/legal.ts). Domicilio del titular: 2da
  avenida, San Martín Jilotepeque, Chimaltenango, Guatemala.
- Contacto, texto de soporte (24/7), tiempo de respuesta (vacío hasta que se acuerde) y banner:
  en **/admin › Ajustes**.
- Correos (solicitud, instrucciones de pago, pago confirmado):
  [`server/utils/emails.ts`](server/utils/emails.ts).
- Capturas: `pnpm images` (lista en `scripts/optimize-images.mjs` y `app/config/shots.ts`).

Todas las cifras de fiabilidad salen de `tests/load/REPORT.md` y de `README.md` (raíz) y se
presentan con la nota del servidor de pruebas. Si cambian allí, cámbialas aquí.

## Pagos

Tras enviar la solicitud en `/comprar` (o `/en/buy`) el cliente recibe la referencia
(`HF-P-AAAAMMDD-XXXXXX`) y elige entre los métodos **activos y completos** (se configuran en
**/admin › Pagos**):

| Método               | Cómo funciona                                                                                                                                                                                                                                                                                                                                                                                         | Estado                                                     |
| -------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| **PayPal**           | Botón del JS SDK de PayPal (se carga solo en la compra y solo al elegirlo). El servidor crea la orden (Orders v2, `PayPal-Request-Id` = referencia: idempotente) por el **importe en USD** del plan y periodo guardado en la solicitud, y la captura al aprobarla; comprueba estado, moneda, importe y referencia. El webhook `PAYMENT.CAPTURE.COMPLETED` confirma también si el navegador no vuelve. | Pagada al confirmarse; correo al cliente y a `SALES_EMAIL` |
| **Link de pago Neo** | Botón al link configurado para ese plan y periodo, con la referencia visible y en un correo. Neo se trata como un link externo (sin API).                                                                                                                                                                                                                                                             | Pendiente de pago; el administrador la marca como pagada   |
| **Transferencia**    | Cuentas bancarias (banco, tipo ES/EN, número, titular, moneda) e instrucciones ES/EN, en pantalla y por correo, con la referencia.                                                                                                                                                                                                                                                                    | Pendiente de pago; el administrador la marca como pagada   |

PayPal **no admite GTQ**: si el cliente eligió quetzales, PayPal cobra el precio en USD del mismo
plan (se le avisa). Al marcar una compra como pagada en el panel se puede avisar por correo al
cliente y a ventas. Las credenciales de PayPal (client id y secret) son de **solo escritura**: se
cifran con AES-256-GCM con la clave de `DATA_KEY_FILE` y nunca se vuelven a mostrar.

### PayPal: sandbox, webhook y paso a real

1. En <https://developer.paypal.com> › _Apps & Credentials_ › **Sandbox**, crea una app (tipo
   _Merchant_) y copia **Client ID** y **Secret**.
2. En la app › **Webhooks** › _Add webhook_: URL `https://horusflow.kns.gt/api/payments/paypal/webhook`
   (el panel la muestra), evento **Payment capture completed**. Copia el **Webhook ID**.
3. En **/admin › Pagos › PayPal**: modo **Sandbox**, Client ID, Secret y Webhook ID; marca
   "Ofrecer PayPal" y guarda.
4. Prueba una compra en `/comprar` con una cuenta _Personal_ de sandbox
   (_Testing Tools › Sandbox Accounts_). La solicitud pasa a **Pagada** en el panel y llegan los
   correos. En _Webhooks Events_ de PayPal se ve la entrega (respuesta 200). Un webhook sin firma
   válida (verificada con `POST /v1/notifications/verify-webhook-signature`) responde 400 y no
   cambia nada.
5. Para cobrar de verdad: crea la app en **Live**, repite el webhook en Live, y en el panel cambia
   a modo **Live** con las credenciales y el Webhook ID de Live.

En los tests, `PAYPAL_API_BASE` apunta a un PayPal simulado
([`tests/support/fake-paypal.mjs`](tests/support/fake-paypal.mjs)) y el e2e sustituye el JS SDK
por uno simulado; en producción esa variable se ignora.

**CSP:** la de toda la web es `default-src 'self'` (sin terceros; `media-src 'self'` para los
vídeos de `/motion`); la de `/comprar` y `/en/buy` añade los dominios de PayPal
(`www.paypal.com`, `*.paypal.com`, `*.paypalobjects.com`) en `script-src`, `connect-src`,
`frame-src` e `img-src`, y `Permissions-Policy: payment=(self "https://www.paypal.com")`.

## Correo: SPF, DKIM y DMARC de kns.gt

El remitente es `info@kns.gt` (`MAIL_FROM`) y los avisos llegan a `info@kns.gt` (`SALES_EMAIL`).
El servidor SMTP aún no se conoce: cuando se sepa, pon `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER` y
`SMTP_PASS_FILE`, y publica en el DNS de **kns.gt**:

| Registro           | Nombre                         | Valor (ejemplo; ajústalo al proveedor)                                                                                                                      |
| ------------------ | ------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| SPF (TXT)          | `kns.gt`                       | `v=spf1 include:<dominio-spf-del-proveedor> -all` — un solo registro SPF; si ya existe, añade el `include:` al existente                                    |
| DKIM (TXT o CNAME) | `<selector>._domainkey.kns.gt` | la clave pública que genere el proveedor SMTP (p. ej. `v=DKIM1; k=rsa; p=MIIB…`)                                                                            |
| DMARC (TXT)        | `_dmarc.kns.gt`                | empieza con `v=DMARC1; p=none; rua=mailto:info@kns.gt; adkim=s; aspf=s` y, tras revisar los informes unas semanas, pasa a `p=quarantine` y luego `p=reject` |

Comprueba con una solicitud de prueba que la cabecera `Authentication-Results` del correo
recibido muestre `spf=pass`, `dkim=pass` y `dmarc=pass`.

## Copia de seguridad

Todo lo que no está en el código vive en el volumen de datos (`/data/horus-landing.sqlite`, en
modo WAL) y en la clave de `DATA_KEY_FILE`. Sin la clave, las credenciales de PayPal y los
secretos TOTP guardados no se pueden descifrar (habría que volver a escribirlas y restablecer el
TOTP con `admin reset-totp`): **guarda la clave aparte**, no junto a la copia.

```bash
# copia en caliente y consistente (API de copia de SQLite), sin parar la landing
docker run --rm -v horus-landing_landing-data:/data -v "$PWD":/backup alpine:3.20 \
  sh -c 'apk add --no-cache sqlite >/dev/null && sqlite3 /data/horus-landing.sqlite ".backup /backup/horus-landing-$(date +%F).sqlite"'

# restaurar: parar, sustituir el archivo (y borrar -wal/-shm), arrancar
docker compose -f compose.example.yaml stop landing
docker run --rm -v horus-landing_landing-data:/data -v "$PWD":/backup alpine:3.20 \
  sh -c 'rm -f /data/horus-landing.sqlite-wal /data/horus-landing.sqlite-shm && cp /backup/horus-landing-AAAA-MM-DD.sqlite /data/horus-landing.sqlite && chown 65532:65532 /data/horus-landing.sqlite'
docker compose -f compose.example.yaml start landing
```

Programa la copia (cron diario) y guarda varias. Las migraciones se aplican solas al arrancar
una versión nueva; haz una copia antes de actualizar.

## CI

Job `landing` en `.github/workflows/ci.yml`, solo si cambia `apps/landing/` (salida `landing` de
`scripts/ci/affected.py`): lint, typecheck, test, build, e2e + axe con Playwright.
