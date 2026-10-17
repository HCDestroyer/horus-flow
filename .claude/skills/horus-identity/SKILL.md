---
name: horus-identity
description: Identidad visual "vidrio y luz" del ecosistema Horus (Horus Flow y cualquier otra app, panel, landing, pantalla NOC, correo o documento de la familia Horus). Úsala siempre que crees, rediseñes o revises la interfaz de un producto Horus, aunque solo se pida "una pantalla", "un dashboard", "el login", "los colores", "el tema oscuro", "un gráfico" o "que se vea como Horus Flow"; también al elegir colores de estado, iconos, tipografía, animaciones o al escribir textos de interfaz para Horus. Trae tokens CSS listos (agnósticos y para Nuxt UI), paleta de gráficos validada para daltonismo, logo y reglas de uso.
---

# Identidad visual Horus — "vidrio y luz"

Una sola identidad para todo el ecosistema Horus. La fuente de verdad es Horus Flow
(`docs/frontend.md` §13 y `apps/frontend/app/assets/css/main.css`); esta skill la empaqueta para
aplicarla igual en cualquier app nueva. Si una app necesita algo que aquí no está, se resuelve
con estas mismas reglas y se propone de vuelta a la skill — no se inventa una variante local.

## La idea en una frase

Marca **sobria con un único color** (teal / azul petróleo). La capa funcional (navegación,
menús, modales) es **vidrio** translúcido sobre un **aura** de color muy difuminada; el contenido
va en **tarjetas** casi sólidas; la **luz** (halos) se reserva para **señales**: dato en vivo,
valor que cambió, crítico abierto y la acción primaria — y solo en tema oscuro.

## Reglas que no se rompen

1. **Un color de marca para todo el ecosistema.** Cada producto se distingue por su nombre
   ("Horus Flow", "Horus <Producto>"), nunca por otro color primario.
2. **Un color, un significado; nunca solo color.** Todo estado = icono + texto + color.
3. **Nunca colores crudos** (`text-red-500`, `#f00`) en componentes: solo tokens semánticos
   (`primary`, `neutral`, `success`, `info`, `warning`, `error`).
4. **Nunca vidrio sobre vidrio.** Vidrio = capa funcional flotante; tarjeta = contenido.
5. **La luz es señal, no decoración.** Una acción primaria con halo por vista; nada de brillos
   decorativos. En tema claro y con `prefers-contrast: more` no hay halos.
6. **Los colores de estado nunca son series de gráficos.** Las series usan la paleta categórica.
7. **Accesibilidad WCAG 2.1 AA verificable:** texto ≥ 4,5:1, componentes y foco ≥ 3:1, valores
   clave en pantallas murales ≥ 7:1; foco siempre visible; respeta `prefers-reduced-motion`,
   `prefers-reduced-transparency` y `prefers-contrast`.
8. **Sin negro puro** en oscuro (base azul noche `#0b1014`): evita halos y quemado en TVs.

## Cómo aplicarla en una app

1. **Elige el adaptador**
   - Nuxt / Vue con Nuxt UI v4 → copia `assets/nuxt-ui/main.css` (como `app/assets/css/main.css`)
     y `assets/nuxt-ui/app.config.ts`. Es el mismo tema que Horus Flow.
   - Cualquier otra pila (React, Svelte, HTML, Tailwind sin Nuxt UI, plantillas de correo) →
     importa `assets/horus-tokens.css`: variables `--hx-*` para claro/oscuro, materiales, aura,
     halos, números vivos y las tres preferencias del sistema. Tema oscuro con la clase `.dark`
     en `<html>` o `[data-theme="dark"]`.
2. **Monta el fondo:** `<div class="app-aura" aria-hidden="true"></div>` como primer hijo del
   layout (en TVs/murales `app-aura app-aura-wall`). Sin aura el vidrio no tiene qué reflejar.
3. **Asigna materiales** (tabla abajo) a cada región. Revisa que ningún vidrio contenga otro.
4. **Estados:** usa exactamente el mapeo de `references/estados.md` (severidades, exportador,
   router, cliente). Si el concepto es nuevo, aplica la misma lógica: urgencia → color,
   identidad → icono, siempre con texto.
5. **Gráficos:** `assets/chart-palette.ts` (categórica de 5 ranuras, orden fijo, validada para
   daltonismo, tinta por tema y umbrales). Cada gráfico lleva título, frase con el hallazgo
   principal y tabla alternativa.
6. **Movimiento y luz:** sigue `references/movimiento-y-luz.md` (números que ruedan, punto en
   vivo, destello de cambio, resortes sin rebote).
7. **Logo:** `assets/logo-mark.svg` (ojo de Horus blanco sobre teal 700, radio 7/32). Reglas en
   `references/marca-y-escritura.md`.
8. **Antes de entregar,** recorre `references/checklist.md` y corre axe (o equivalente).

## Materiales

| Material | Clase | Dónde | Valor (oscuro / claro) |
| --- | --- | --- | --- |
| Vidrio | `.glass-material`, `.chrome-material` | Barra lateral, barra superior, banda de cabecera NOC | blur 28 px · saturate 150 % · relleno 62 % / 66 % blanco |
| Vidrio fuerte | `--hx-glass-strong` | Menús, popovers, modales, paneles laterales | relleno 86 % |
| Tarjeta | `.surface-card` | Widgets, tablas, gráficos, formularios | 86 % / blanco opaco · borde 1 px · radio 14 px |
| Sólido | automático | `prefers-reduced-transparency`, TVs sin `backdrop-filter` | fondo del tema |
| Aura | `.app-aura` | Fondo de toda la app | teal 20 % + índigo 16 % · blur 90 px · deriva 60 s (mural 90 s al 60 %) |

## Color

| Token | Claro | Oscuro | Uso |
| --- | --- | --- | --- |
| primary (teal) | `#0f766e` (700) | `#2dd4bf` (400, la "luz" de la marca) | Acción primaria, selección, foco, punto en vivo |
| secondary (índigo) | `#4f46e5` (600) | `#a5b4fc` (300) | Acento puntual, segundo tono del aura |
| success (verde) | `#166534` (800) | `#4ade80` (400) | Correcto, exportando, online |
| info (azul) | `#1d4ed8` (700) | `#60a5fa` (400) | Informativo, tipo comercial |
| warning (ámbar) | `#92400e` (800) | `#fbbf24` (400) | Degradado, severidad media |
| error (rojo) | `#b91c1c` (700) | `#f87171` (400) | Caído, silencioso, crítica/alta |
| neutral (zinc) | texto atenuado `#52525b` | `#a1a1aa` | Estructura, estados sin urgencia |
| fondo | `#ffffff` (estructura `#fafafa`) | `#0b1014` · muted `#10151b` · elevado `#1a2029` · borde `#232a33` | |

En claro se usan tonos 600–800 y en oscuro 300–400 porque los 500 no llegan a 4,5:1.

## Tipografía

- Interfaz: **fuente del sistema** (`system-ui, -apple-system, 'Segoe UI', Roboto, …`).
  Monoespaciada (`ui-monospace, 'SF Mono', Menlo, Consolas, …`) para IPs, claves, IDs y `trace_id`.
- **Cifras tabulares** en tablas, KPIs y ejes (nada cambia de ancho al actualizarse).
- Jerarquía por peso + tamaño; tamaños en `rem`; `h1` con tracking −0,015 em; KPIs −0,02 em.
- Cuerpo ≥ 14 px en tablas densas, 16 px en formularios.
- Mural (1920×1080 de referencia, escala uniforme): KPI 72 px semibold, título 28, etiqueta 22,
  frescura 20; nada por debajo de 20 px. Unidades al 50 % del valor, mismo color.

## Iconos

Un único set: **Lucide** (`i-lucide-*`). Botón solo-icono con `aria-label` y tooltip. Iconos
sin color salvo cuando expresan un estado.

## Referencias

- `references/estados.md` — mapeo completo estado → color + icono + texto.
- `references/movimiento-y-luz.md` — halos, números vivos, punto en vivo, resortes, murales.
- `references/marca-y-escritura.md` — logo, nombres de producto, voz y formato de datos.
- `references/checklist.md` — revisión antes de entregar una pantalla.
- `assets/horus-tokens.css` — tokens agnósticos (`--hx-*`) + clases.
- `assets/nuxt-ui/` — tema exacto de Horus Flow para Nuxt UI v4.
- `assets/chart-palette.ts` — paleta categórica, tinta de gráficos y umbrales.
- `assets/logo-mark.svg` — marca.
