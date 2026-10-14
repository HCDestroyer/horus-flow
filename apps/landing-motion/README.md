# Animaciones de la landing (Remotion)

Proyecto [Remotion](https://www.remotion.dev) (React + TypeScript) con las animaciones neón de
la landing comercial (`apps/landing`). **La landing no carga React ni el Player de Remotion:**
las composiciones se renderizan a mano a archivos de vídeo, que se commitean en
[`apps/landing/public/motion/`](../landing/public/motion/) y se muestran con
`apps/landing/app/components/MotionVideo.vue` (`<video>` nativo, carga diferida, póster como
LCP y póster estático con `prefers-reduced-motion`).

## Composiciones

| Id | Duración | Lienzo | Dónde se usa | Qué cuenta |
| --- | --- | --- | --- | --- |
| `HeroNetwork` | 10 s (300 f, 30 fps) | 1920 × 1920 | Hero (`SubnetGrid.vue`) | La red `10.20.1.0/24` (256 puntos, `.0` y `.255` huecos) como malla; paquetes de luz bajan al router MikroTik, cruzan el túnel WireGuard y llegan a Horus. `10.20.1.47` contacta un C2, Horus lo detecta y la IP se enciende en rojo neón con un pulso; vuelta a la calma. |
| `HowItWorks` | 12 s (360 f) | 1920 × 640 | "Cómo funciona" (`FlowDiagram.vue`) | Router → túnel WireGuard (cifrado, candado) → Horus (collector → detección) → NOC/kiosco y alertas (email, Telegram, LibreNMS); cada etapa se ilumina en orden. |
| `Resilience` | 8 s (240 f) | 1920 × 640 | "Fiabilidad" (`ReliabilitySection.vue`) | ClickHouse se apaga, los flujos se acumulan en el búfer y se vacían al volver. La cifra ("0 flujos perdidos", de `tests/load/REPORT.md`) va en el HTML. |

Reglas de diseño (de `.claude/skills/apple-hig/` y `.claude/skills/apple-design/`):

- **Sin texto traducible dentro del vídeo** (solo nombres propios e IPs). El texto ES/EN va en
  HTML encima o debajo: accesible, indexable y traducido. El `<video>` lleva `aria-hidden` y el
  contenedor, la descripción equivalente.
- **El movimiento nunca es el único portador de significado** (motion.md › "Make motion
  optional"): la ficha "Infectado · confianza · razón" del hero y la lista de etapas de "Cómo
  funciona" dicen lo mismo en texto.
- **El rojo neón solo significa Infectado.** Un componente caído es gris, nunca rojo. El acento
  lapislázuli de la landing se vuelve neón azul-cian (`src/lib/theme.ts` parte de los tokens
  oscuros de `apps/landing/app/assets/css/main.css`).
- **Glow por capas** (`src/lib/Neon.tsx`): halo desenfocado + trazo nítido encima. La forma la
  lleva el trazo nítido, así que la compresión no la emborrona.
- **Loops perfectos:** todo depende de `frame` de forma periódica (periodos que dividen la
  duración) o vuelve a su estado inicial antes del último frame (`src/lib/motion.ts`).
- Pulsos lentos (≤ 2 por segundo), sin destellos (accessibility.md › "Be cautious with
  fast-moving and blinking animations").

## Uso

```sh
pnpm install
pnpm studio                 # Remotion Studio para editar las composiciones
pnpm render                 # renderiza todas las composiciones
pnpm render:one HeroNetwork # una (o varias) por id
pnpm frames                 # solo los PNG de frames clave (docs/frames/)
pnpm typecheck              # lo único que corre en CI (job landing-motion)
```

`scripts/render.mjs` usa el Chromium ya instalado en
`/opt/pw-browsers/chromium_headless_shell-1194/` (cámbialo con `REMOTION_BROWSER_EXECUTABLE`;
nunca se descarga otro) y `ffmpeg` del sistema. Por composición:

1. Intermedio H.264 CRF 12 con Remotion (en `out/`, se borra al terminar).
2. **WebM VP9** y **MP4 H.264** de respaldo, a 1920 y 960 px de ancho, 2 pasadas, sin audio,
   `+faststart`. El bitrate se calcula para el presupuesto: el grande apunta al 85 % del tope y
   el pequeño al 40 %. El script falla si un archivo se pasa del tope.
3. **Póster AVIF y WebP** del frame más representativo, en los dos anchos (es el LCP del hero).
4. PNG de 3–4 frames clave en [`docs/frames/`](docs/frames/) (a 960 px).

Presupuesto por formato y tamaño: hero ≤ 1,5 MB; el resto ≤ 1 MB.

Los vídeos no se renderizan en CI: tras cambiar una composición, ejecuta `pnpm render:one <id>`
y commitea los archivos de `apps/landing/public/motion/` y `docs/frames/`.

## Licencia de Remotion

Remotion **no es MIT ni de código abierto**: usa la "Remotion License". Texto leído de
`node_modules/remotion/LICENSE.md` (versión 4.0.525 instalada), citado literalmente:

> Depending on the type of your legal entity, you are granted permission to use Remotion for
> your project. Individuals and small companies are allowed to use Remotion to create videos for
> free (even commercial), while a company license is required for for-profit organizations of a
> certain size.

**Licencia gratuita.** Elegibilidad:

> You are eligible to use Remotion for free if you are:
>
> - an individual
> - a for-profit organization with up to 3 employees
> - a non-profit or not-for-profit organization
> - evaluating whether Remotion is a good fit, and are not yet using it in a commercial way

Uso permitido:

> Permission is hereby granted, free of charge, to any person eligible for the "Free License",
> to use the software non-commercially or commercially for the purpose of creating videos and
> images and to modify the software to their own liking, for the purpose of fulfilling their
> custom use case or to contribute bug fixes or improvements back to Remotion.

Uso no permitido:

> It is not allowed to copy or modify Remotion code for the purpose of selling, renting,
> licensing, relicensing, or sublicensing your own derivate of Remotion.

**Licencia de empresa:**

> You are required to obtain a Company License to use Remotion if you are not within the group
> of entities eligible for a Free License. This license will enable you to use Remotion for the
> allowed use cases specified in the Free License, and give you access to prioritized support
> (read the [Support Policy](https://www.remotion.dev/docs/support)).
>
> Visit [remotion.pro](https://www.remotion.pro/license) for pricing and to buy a license.

El texto también avisa: "In Remotion 5.0, the license will slightly change."

**Qué significa para Horus Flow (uso comercial por una empresa):**

- Si la empresa que produce los vídeos es una **organización con ánimo de lucro de más de 3
  empleados**, necesita una **Company License** de Remotion (remotion.pro) para usarlo, también
  para renderizar estas animaciones de marketing.
- Con **hasta 3 empleados** (o si quien renderiza es un particular), el uso comercial para crear
  vídeos es gratuito con la licencia gratuita.
- En ningún caso se puede vender, alquilar ni sublicenciar un derivado de Remotion; aquí solo
  se usa para producir vídeos, que es el caso de uso permitido.
- Remotion no se distribuye con el producto ni con la landing (solo los vídeos renderizados);
  figura en [`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md) como herramienta de
  producción. Antes de actualizar a Remotion 5 hay que releer la licencia.
