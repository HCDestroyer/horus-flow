# Landing comercial de Horus Flow

Página pública de venta de Horus Flow, de **Connection And Solutions Company, Sociedad Anónima
(C&S Company)**, Guatemala. Contacto: info@kns.gt. Es una app aparte del frontend del producto
(`apps/frontend`): no contiene ni lee datos de ningún ISP.

- **Stack:** Nuxt 4 (SSR con servidor Nitro) + Nuxt UI v4 + Tailwind 4, pnpm.
- **Idiomas:** español (`/`, principal, es-GT) e inglés (`/en`), con @nuxtjs/i18n.
- **Páginas:** portada (`/`, `/en`), compra (`/comprar`, `/en/buy`) y legales
  (`/legal/aviso-legal|privacidad|terminos`, `/en/legal/notice|privacy|terms`).
- **Servidor:** `POST /api/lead` (demo y contacto), `POST /api/purchase` (solicitud de compra),
  `/sitemap.xml` y `/robots.txt`.
- **Diseño:** Apple HIG / Liquid Glass (`.claude/skills/apple-hig`); revisión en
  [`docs/design-review.md`](docs/design-review.md) y capturas en [`docs/screenshots/`](docs/screenshots/).

## Desarrollo

```bash
cd apps/landing
pnpm install
MAIL_TRANSPORT=file pnpm dev        # http://localhost:3000, correos en .outbox/*.json
```

| Orden                      | Qué hace                                                                                                                                                                                 |
| -------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `pnpm lint`                | ESLint + Prettier                                                                                                                                                                        |
| `pnpm typecheck`           | `nuxt typecheck`                                                                                                                                                                         |
| `pnpm test`                | Vitest: validación, honeypot, tiempo mínimo, rate limit, IP real detrás de proxy, envío simulado, webhook, precios y traducciones                                                        |
| `pnpm build && pnpm start` | build de producción y servidor Node (`.output/server/index.mjs`)                                                                                                                         |
| `pnpm e2e`                 | Playwright: navegación, cambio de idioma, demo y compra con SMTP simulado, SEO, axe (claro/oscuro y HTML del servidor), objetivos táctiles, 320 px. `E2E_NO_BUILD=1` reutiliza `.output` |
| `pnpm screenshots`         | capturas a 1440, 768 y 390 px en claro y oscuro → `docs/screenshots/`                                                                                                                    |
| `pnpm images`              | regenera las capturas optimizadas (AVIF/WebP, varios anchos) y la imagen Open Graph desde `apps/frontend/docs/screenshots/tour`                                                          |

Playwright usa el Chromium de `PLAYWRIGHT_BROWSERS_PATH` (en el contenedor de desarrollo,
`/opt/pw-browsers`); no hace falta `playwright install` en local. En CI se instala.

## Variables

Todas son de tiempo de ejecución (no hace falta reconstruir). Ejemplo completo en
[`.env.example`](.env.example).

| Variable                                                          | Por defecto                            | Para qué                                                                                 |
| ----------------------------------------------------------------- | -------------------------------------- | ---------------------------------------------------------------------------------------- |
| `NUXT_PUBLIC_SITE_URL`                                            | `https://horusflow.kns.gt`             | URL pública canónica, sin barra final: canonical, hreflang, Open Graph, JSON-LD, sitemap |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_SECURE`, `SMTP_USER`, `SMTP_PASS` | — / 587 / `false` (465 → `true`)       | SMTP para avisar a ventas y confirmar al cliente                                         |
| `MAIL_FROM`                                                       | `Horus Flow <SALES_EMAIL>`             | remitente (debe pasar SPF/DKIM del dominio)                                              |
| `SALES_EMAIL`                                                     | `info@kns.gt`                          | destino de las solicitudes                                                               |
| `MAIL_CONFIRM_CUSTOMER`                                           | `true`                                 | correo de confirmación al cliente                                                        |
| `MAIL_TRANSPORT`                                                  | `smtp` si hay `SMTP_HOST`, si no `log` | `file` = SMTP simulado (`MAIL_OUTBOX_DIR`, por defecto `.outbox`); `log` = solo registra |
| `LEADS_WEBHOOK_URL`, `LEADS_WEBHOOK_SECRET`                       | —                                      | webhook opcional para un CRM: POST JSON con firma `X-Horus-Signature: sha256=<HMAC>`     |
| `TRUSTED_PROXIES`                                                 | —                                      | IPs/CIDR de los proxies de confianza; solo de ellos se acepta `X-Forwarded-For`          |
| `RATE_LIMIT_MAX`, `RATE_LIMIT_WINDOW_SECONDS`                     | 8 / 600                                | envíos por IP real y ventana                                                             |
| `FORM_MIN_FILL_SECONDS`                                           | 3                                      | tiempo mínimo entre mostrar y enviar un formulario                                       |
| `PAYMENT_PROVIDER`                                                | `manual`                               | proveedor de pago (ver abajo)                                                            |
| `ROBOTS_DISALLOW_ALL`                                             | —                                      | `1` en entornos de pruebas: `robots.txt` lo bloquea todo                                 |
| `PRICING_REQUIRE_CONFIRMED`                                       | —                                      | `1` hace fallar el build si los precios no están confirmados                             |

**Sin SMTP:** en desarrollo las solicitudes se registran (sin datos personales) y se responde OK.
En producción (`NODE_ENV=production`) sin SMTP ni webhook las rutas responden **503** con el
correo de ventas, para no perder solicitudes en silencio.

**Datos personales en logs:** solo la referencia, el plan, el país y el correo enmascarado
(`a***@dominio`); nunca nombres, teléfonos ni mensajes. La IP se usa en memoria para el rate
limit y en los logs va truncada.

**Antispam:** honeypot (`website`, invisible y fuera del tabulado: al bot se le responde OK sin
enviar nada), tiempo mínimo de llenado, y rate limit en memoria por IP real. Con varias
réplicas cada una cuenta por separado.

## Despliegue detrás de Nginx Proxy Manager

1. Construye y arranca (imagen Node distroless, usuario `nonroot`, raíz de solo lectura):

   ```bash
   cd apps/landing
   cp .env.example .env      # SMTP, NUXT_PUBLIC_SITE_URL=https://tu-dominio, TRUSTED_PROXIES…
   docker compose -f compose.example.yaml up -d --build
   ```

   El compose une el contenedor a la red de NPM (`PROXY_NETWORK`, por defecto `npm_default`)
   sin publicar puertos.

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

## Editar precios

Todo está en **un solo archivo**: [`app/config/pricing.ts`](app/config/pricing.ts) — planes,
importes en USD y GTQ por periodo (mensual/anual), lo que incluye cada plan, el servidor
recomendado, el texto del pago anual y lo que incluye toda licencia.

- Hoy `confirmed: false`: los importes son una **propuesta** (comentada en el archivo) y la página
  muestra **"Precio de lanzamiento: solicita cotización"**; el JSON-LD publica las ofertas sin
  importe y `pnpm build` avisa (`[pricing] WARN Precios SIN CONFIRMAR…`).
- Para publicar cifras: ajusta los importes y pon `confirmed: true`. Aparece el selector de
  moneda, las cifras por periodo, los importes en el resumen de compra, en los correos y en el
  JSON-LD. El test `tests/unit/pricing.test.ts` comprueba que no hay cifras mientras
  `confirmed` sea false: actualízalo al confirmar.

## Editar textos

- Textos de la página: [`i18n/locales/es.json`](i18n/locales/es.json) y
  [`en.json`](i18n/locales/en.json) (mismas claves; un test lo verifica). En vue-i18n `@`, `{`,
  `}` y `|` son especiales: el correo va como parámetro `{email}`.
- Datos del vendedor y rutas por idioma: [`app/config/site.ts`](app/config/site.ts).
- Textos legales (**borradores en español pendientes de revisión legal**, con marcas
  `[POR DEFINIR]`/`[POR CONFIRMAR]`): [`app/content/legal.ts`](app/content/legal.ts).
- Correos a ventas y al cliente: [`server/utils/emails.ts`](server/utils/emails.ts).
- Capturas: `pnpm images` (lista en `scripts/optimize-images.mjs` y `app/config/shots.ts`).

Todas las cifras de fiabilidad salen de `tests/load/REPORT.md` y de `README.md` (raíz) y se
presentan con la nota del servidor de pruebas. Si cambian allí, cámbialas aquí.

## Añadir una pasarela de pago

Hoy solo existe **"solicitud manual / transferencia"**
([`server/payments/manual.ts`](server/payments/manual.ts)): la compra genera una referencia
(`HF-P-AAAAMMDD-XXXXXX`), se avisa a ventas y el cliente paga por transferencia tras recibir la
cotización. La interfaz está en [`server/payments/types.ts`](server/payments/types.ts):

```ts
interface PaymentProvider {
  readonly id: string
  createPayment(
    order: PurchaseOrder,
  ): Promise<
    { provider: string; kind: 'manual' } | { provider: string; kind: 'redirect'; url: string }
  >
}
```

Para añadir Stripe, Recurrente (Guatemala) o PayPal:

1. Crea `server/payments/<proveedor>.ts` que implemente `PaymentProvider`. En `createPayment`
   crea la sesión de pago del proveedor (Stripe Checkout Session, Recurrente checkout, PayPal
   Order) con `order.amount`, `order.currency`, la referencia como `metadata`/`custom_id` y las
   URLs de vuelta (`NUXT_PUBLIC_SITE_URL` + `/comprar?ref=…`), y devuelve
   `{ kind: 'redirect', url }`. El formulario ya redirige cuando recibe `redirect`.
2. Regístralo en [`server/payments/index.ts`](server/payments/index.ts) y elige con
   `PAYMENT_PROVIDER=<id>`. Las claves van en variables de entorno (nunca en el repositorio).
3. Añade `server/api/payments/<proveedor>/webhook.post.ts` que **verifique la firma** del
   proveedor (Stripe-Signature, cabecera de Recurrente, verificación de PayPal), marque la
   referencia como pagada y avise a ventas. Sin webhook verificado no se da nada por pagado.
4. Solo tiene sentido con precios confirmados (`confirmed: true`): con `amount: null` el
   proveedor debe responder `manual`. Revisa los términos (§4) y la política de privacidad
   (destinatarios) antes de activarlo, y amplía `Permissions-Policy` (`payment=()`) y la CSP
   si el proveedor usa iframes o scripts propios.

## CI

Job `landing` en `.github/workflows/ci.yml`, solo si cambia `apps/landing/` (salida `landing` de
`scripts/ci/affected.py`): lint, typecheck, test, build, e2e + axe con Playwright.
