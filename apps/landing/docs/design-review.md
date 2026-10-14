## Design review: landing comercial de Horus Flow

> Revisión con el formato de la skill `apple-hig` sobre las capturas de
> [`screenshots/`](screenshots/) (`inicio-*` y `comprar-*` a 1440, 768 y 390 px, claro y oscuro)
> y sobre el código. Archivos de la skill leídos: `SKILL.md`, `liquid-glass.md`, `materials.md`,
> `accessibility.md`, `color.md`, `typography.md`, `layout.md`, `branding.md`,
> `design-principles.md`, `buttons.md`, `entering-data.md`, `dark-mode.md` y
> `.claude/skills/apple-design/SKILL.md`. Es una web (Nuxt): aplican los principios y los
> fundamentos (accesibilidad, color, tipografía, layout, escritura), no las convenciones de
> plataforma de iOS/macOS.

### Summary

Una landing B2B para ISPs con un único gesto propio: **la red /24 dibujada como 256 puntos con
un solo cliente señalado como Infectado**. La tesis de la página — "entre todos tus clientes,
Horus te dice cuál y por qué" — está en ese gráfico, en el titular y en la ficha que lo
acompaña (IP, estado, confianza, razón). El resto es deliberadamente sobrio: tipografía del
sistema, superficies sólidas, un solo acento (lapislázuli) y vidrio solo en la capa funcional.
Tras corregir los hallazgos Critical y High de la primera pasada: **Good**.

### Elemento distintivo (y por qué)

- **Qué:** cuadrícula 16×16 de `10.20.1.0/24` (SVG accesible con `<title>`/`<desc>`); la red
  (.0) y el broadcast (.255) van huecos; un punto rojo con anillo y guía discontinua hasta una
  ficha sólida "10.20.1.47 · Infectado · confianza 94 % · Contacta un servidor de mando y control
  listado en Feodo Tracker". Se anima una sola vez (barrido + anillo, < 2 s) y nada con
  `prefers-reduced-motion`.
- **Por qué es de Horus y no de cualquiera:** en Horus _la IP es el cliente_ (D1) y el estado
  "Infectado" siempre va con razones y confianza (D18). La cuadrícula es literalmente lo que el
  producto ve (un prefijo de clientes) y lo que entrega (uno señalado, explicado). El ojo de
  Horus queda como marca pequeña en la barra (`branding.md › Best practices`: "Resist the
  temptation to display your logo throughout your app"), no como decoración del hero.
- **Plantillas evitadas** (`SKILL.md › Lens 3`): no hay crema + serif + terracota, ni negro +
  verde ácido, ni retícula de periódico; tampoco "número gigante + gradiente" en el hero. Las
  cifras de fiabilidad van en una **tabla prueba → resultado con sus condiciones**, porque el
  contexto de cada cifra pesa tanto como la cifra.

### Hallazgos corregidos

| #   | Severidad    | Hallazgo                                                                                                                                                                                                                                                                                                                                                                | Arreglo                                                                                                                                                                                  |
| --- | ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | **Critical** | Con la hidratación diferida del formulario, el `for` de las etiquetas (generado con `useId()` por `UFormField`) no coincidía con el `id` de los campos en el HTML del servidor: 9 controles sin nombre accesible (Lighthouse accesibilidad 92, reglas `label` y `button-name`). `accessibility.md › Vision`: "Describe your app's interface and content for VoiceOver." | Componente `LabeledField` con `<label for>` e `id` fijos (`demo-*`); nuevo test axe sobre el HTML del servidor sin los scripts de la app. Accesibilidad 100.                             |
| 2   | **High**     | Selectores de 40 px de alto y casillas de 18 px en móvil. `accessibility.md › Mobility`: "iOS, iPadOS 44x44 pt" por defecto.                                                                                                                                                                                                                                            | Campos con `min-h-12` (48 px); la etiqueta de la casilla forma parte del objetivo (≥ 44 px). Test e2e que mide todos los controles a 390 px en `/` y `/comprar`.                         |
| 3   | **High**     | `content-visibility: auto` en las secciones (para rendimiento) hacía que los enlaces de la barra a `#pricing` aterrizaran fuera de pantalla. `design-principles.md › Familiarity`: "Provide clear feedback… indicate when content changes."                                                                                                                             | Retirado; el rendimiento se consigue con hidratación diferida, CSS en línea y HTML comprimido. Test e2e de navegación por anclas.                                                        |
| 4   | **High**     | Rendimiento móvil 69 (TBT 630 ms, CSS de 200 KB, HTML sin comprimir).                                                                                                                                                                                                                                                                                                   | Secciones estáticas sin hidratar, interactivas al acercarse, sin `UApp`, solo los temas de Nuxt UI usados (CSS 76 KB), HTML brotli/gzip y hoja principal en línea → 90–95 en la portada. |
| 5   | Medium       | Titular del hero en 5 líneas a 1440 px; el bloque pesaba más que el gráfico. `typography.md › Conveying hierarchy`.                                                                                                                                                                                                                                                     | Escala 32/44/52 px con `text-balance` y tracking negativo (apple-design §15): 3–4 líneas.                                                                                                |
| 6   | Medium       | La guía del punto señalado terminaba en el vacío, separada de la ficha por el pie de figura.                                                                                                                                                                                                                                                                            | Pie de figura arriba; la guía baja hasta el borde de la ficha.                                                                                                                           |
| 7   | Medium       | Cifras partidas al final de línea ("~2 / 000 clientes") en móvil.                                                                                                                                                                                                                                                                                                       | Espacio de no separación en los miles (`2 000`, `10 000`).                                                                                                                               |

### Improvements (pendientes, ninguno Critical/High)

- **Medium — capturas pequeñas en móvil.** El mural del NOC a 358 px de ancho es ilegible; sirve
  de ambiente, pero no se puede ampliar. _Fix:_ enlace "Ver a tamaño completo" a la versión
  1920 px o un visor accesible.
- **Medium — compra larga en móvil.** El resumen y el botón quedan al final de tres bloques.
  `entering-data.md › Best practices` pide reducir la entrada; ya se usan selectores y el país
  se deduce del idioma del sistema. _Fix posible:_ barra inferior con importe + "Enviar" cuando
  el resumen no está a la vista.
- **Low — selector de moneda solo en la compra.** Con precios sin confirmar no tiene sentido en
  la portada; al confirmar (`confirmed: true`) aparece también allí.
- **Low — relleno de campos en oscuro** algo más oscuro que la tarjeta; legible (texto
  #e8ecf4 sobre #0d1220, 15,77:1) y coherente con los campos de sistema, pero podría igualarse.

### Craft notes

- **Color** (`color.md › Best practices`: "Avoid using the same color to mean different things").
  Lapislázuli = acción principal, enlaces, foco y plan recomendado; rojo = solo "Infectado"
  y errores de formulario. Contrastes calculados y anotados en `app/assets/css/main.css`: texto
  17,87:1 / 14,33:1, texto secundario 7,53:1 / 7,62:1, acento 9,02:1 / 8,06:1, rojo 6,57:1 /
  7,47:1, texto sobre botón 9,02:1 / 8,87:1 (claro / oscuro). Nada por debajo de 4,5:1.
- **Vidrio** (`liquid-glass.md › Review checklist`). Tres superficies, todas de la capa
  funcional: barra de navegación flotante, selector mensual/anual de precios y CTA flotante en
  móvil. Variante "regular" (blur 24 px, relleno 72 %, saturación 1,4). Nunca en tarjetas ni
  fondos; el menú móvil se abre como panel **sólido** debajo de la barra (no vidrio sobre
  vidrio); dentro del formulario de compra el selector es sólido porque vive en una superficie
  de contenido. Respaldo sólido con `prefers-reduced-transparency`, `prefers-contrast: more` y
  navegadores sin `backdrop-filter`.
- **Botones** (`buttons.md › Style`). Un único botón prominente por vista (Solicitar demo /
  plan recomendado / Enviar); el resto, contorno. Etiquetas que dicen lo que pasa ("Enviar
  solicitud de compra", "Solicitar cotización: Grande" para lectores de pantalla).
- **Tipografía.** Fuente del sistema (sin descargas), cuerpo a 17 px (`typography.md`: 17 pt por
  defecto), tracking negativo solo en titulares, cifras tabulares en tablas.
- **Apariencia.** Claro y oscuro según el sistema, sin selector propio (`dark-mode.md › Best
practices`: "Avoid offering an app-specific appearance setting").
- **Movimiento.** Transiciones de 150–200 ms y tres animaciones neón (vídeos renderizados con
  Remotion en `apps/landing-motion`): la cuadrícula del hero viva (paquetes hacia el router, el
  túnel y Horus; 10.20.1.47 se enciende en rojo), "cómo funciona" etapa a etapa y la prueba de
  fallo de fiabilidad. El neón es el **único elemento llamativo**; el resto sigue sobrio. El rojo
  neón solo significa Infectado (un componente caído es gris). `motion.md › "Make motion
optional"`: el texto equivalente va siempre en HTML (ficha, lista de etapas, pie con la cifra),
  el vídeo es `aria-hidden`, hay botón de pausa (`accessibility.md › "Let people control audio
and video playback"`) y con `prefers-reduced-motion` o ahorro de datos solo se ve el póster
  con "Reproducir animación". Capturas `screenshots/motion-*`.
- **Quitar un accesorio:** se probó y quitó el número gigante en fiabilidad; no queda otro
  elemento que sobre.

### What works

- El hero responde en tres segundos _qué_ (botnets), _a quién_ (ISP con MikroTik) y _qué no hace_
  ("Horus solo lee y avisa"), con dos CTA claros: Solicitar demo y Ver precios.
- Honestidad visible: "Datos simulados" en las capturas, la nota del servidor de pruebas bajo la
  tabla de fiabilidad y "Precio de lanzamiento: solicita cotización" mientras no haya precios
  aprobados.
- Formularios: etiquetas visibles, "Opcional" en lo opcional, selectores en vez de texto libre
  para rangos, validación en vivo con mensajes que dicen cómo corregir, foco al resultado y
  referencia copiable; "No pedimos datos de tarjeta".
- Accesibilidad verificada: axe sin violaciones en claro y oscuro (5 páginas) y en el HTML del
  servidor, enlace "Saltar al contenido", foco visible de 2 px, objetivos ≥ 44 px, sin
  desbordamiento a 320 px, Lighthouse accesibilidad 100.

### Platform notes

- Web responsive desde 320 px: barra con menú a partir de < 1024 px, CTA flotante solo < 640 px
  (donde la barra no muestra "Solicitar demo"), `safe-area-inset-bottom` respetado.
- `prefers-reduced-transparency` es solo de Chromium (`liquid-glass.md › Tauri and Electron`);
  en Safari/Firefox el vidrio se queda, con contraste suficiente gracias al relleno del 72 %.
- Rendimiento medido con Lighthouse 12 (móvil, simulado) en un servidor de pruebas compartido:
  portada 90–95, compra 89–90, legales 94; accesibilidad, buenas prácticas y SEO 100.

## Panel de administración y paso de pago (2026-10)

Capturas: `admin-*` (1440 claro/oscuro y 390) y `comprar-pago-{paypal,neo,transfer}-*`.

- **Herramienta de trabajo, no escaparate.** Densidad media: cuerpo a 15–17 px, filas de tabla de
  44 px (objetivo táctil), cabeceras de columna con sustantivos cortos (`lists-and-tables.md ›
Content`), cifras tabulares alineadas a la derecha. En móvil la tabla de solicitudes pasa a
  lista (referencia, estado, empresa, importe) en lugar de desplazarse en horizontal.
- **Vidrio solo en la capa flotante** (`liquid-glass.md › The two layers`): la barra lateral
  (escritorio) y la barra superior (móvil) flotan con la variante regular; la barra de
  publicación de Precios se vuelve vidrio solo cuando hay cambios sin publicar, para que destaque
  sin un color nuevo. Tablas, formularios y la vista previa van en superficies sólidas; el menú
  móvil se abre como panel sólido (no vidrio sobre vidrio).
- **Estados con texto y color** (`color.md`): Nueva (lapislázuli), Pendiente de pago (ámbar),
  Contactado (gris), Pagada (verde), Cancelada (anillo). El color nunca va solo.
- **Precios:** borrador → vista previa con el mismo componente de la web → publicar. Historial con
  "Ver" y "Restaurar"; las acciones irreversibles (restaurar, borrar credenciales, eliminar
  administrador) piden confirmación (`alerts.md`).
- **Acceso:** una sola columna con un paso por pantalla (contraseña → TOTP o alta por QR →
  códigos de recuperación), foco al título de cada paso y mensajes de error que dicen cómo
  seguir ("Espera 2 min antes de volver a intentarlo").
- **Pago en la web:** tres tarjetas de método (radio nativo con etiqueta asociada, ≥ 44 px); la
  referencia, el plan y el importe arriba, siempre visibles; PayPal avisa de que cobra en USD
  cuando el cliente eligió GTQ.
- **Accesibilidad verificada:** axe sin violaciones en login, solicitudes, detalle, precios,
  pagos, ajustes y auditoría, en claro y oscuro (`tests/e2e/admin.spec.ts`).
- **Pendiente (Low):** los diálogos de confirmación usan `window.confirm` (accesible y nativo,
  pero sin el estilo del sistema); se pueden pasar a una hoja modal propia.

## Páginas interiores y movimiento (2026-10)

Capturas: `producto-*`, `ia-*`, `monitoreo-*`, `pantallas-noc-*` (1440/768/390, claro y oscuro) y
`en-ai-1440-*`. Páginas nuevas: `/producto`, `/inteligencia-artificial`, `/monitoreo` y
`/pantallas-noc` (y `/en/product`, `/en/ai`, `/en/monitoring`, `/en/noc-screens`).

- **Navegación** (`design-principles.md › Wayfinding`, apple-design §16 "Direct, specific
  labels"): la barra nombra cada página (Producto, IA, Monitoreo, Pantallas NOC) y Precios sigue
  siendo un ancla de la portada. La página actual lleva `aria-current="page"` y se marca con peso
  y fondo, no solo con color. Cada página interior abre con una ruta visible (Inicio › Página).
  El menú móvil sigue siendo un panel sólido; nace de la barra y vuelve por el mismo camino
  (apple-design §7), y Escape lo cierra devolviendo el foco al botón.
- **Estados de router**: siete colores nuevos con contraste ≥ 3:1 como objeto gráfico (tabla en
  `app/assets/css/main.css`), siempre con su nombre y su razón en texto. "Crítico" reutiliza el
  rojo de "Infectado": en los dos casos significa "exige actuar".
- **IA** (`generative-ai.md`): la maqueta dice que es una ilustración con datos simulados, muestra
  el seudónimo en lugar de la IP, la insignia "Verificada" y que Horus no ejecuta nada.

### Movimiento

Principio: el movimiento explica (los flujos llegan a Horus, el análisis se escribe, un router
cambia de estado) y nunca es requisito para leer la página.

| Dónde                     | Qué                                                                                              | Cómo                                                                                   |
| ------------------------- | ------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------- |
| Toda la web               | Aparición al desplazarse (opacidad + 18 px), escalonada 70 ms                                    | Dos scripts en línea (`app/utils/reveal-script.ts`), sin hidratar; CSS en `motion.css` |
| Hero de portada y páginas | Líneas de flujo que convergen hacia la cuadrícula; los paquetes viajan dos veces y se apagan     | SVG + CSS (`HeroAmbient.vue`), < 5 s en total                                          |
| Portada                   | Cifras que cuentan hasta su valor                                                                | `motion-enhance.ts`; ancho fijo, no cuenta si ya estaba a la vista                     |
| IA (portada y página)     | Análisis que se escribe palabra a palabra, luego comando e insignia "Verificada"; "Ver de nuevo" | Solo opacidad: el texto ya ocupa su sitio (CLS 0); ~4 s                                |
| IA                        | Enmascarado: el dato real se atenúa y aparece el seudónimo                                       | CSS, una vez                                                                           |
| Monitoreo y producto      | Salud de routers con un guion de cambios de estado en bucle y resumen que se recalcula           | `motion-enhance.ts`; pulso del punto (transform); botón de pausa                       |
| Monitoreo                 | Histéresis: la gráfica se descubre de izquierda a derecha                                        | Cortina que se desplaza (transform)                                                    |
| Pantallas NOC             | TV que rota dashboards con barra de progreso en la lista de reproducción                         | Fundido de opacidad cada 6 s; botón de pausa                                           |
| Tarjetas enlace           | Elevación al pasar y respuesta al pulsar (escala 0,985 en 100 ms)                                | `.lift`; la sombra es un pseudo-elemento que solo cambia de opacidad                   |
| Entre páginas             | Fundido de 220 ms; la barra no participa                                                         | View Transitions (Nuxt), omitido con reduced motion                                    |

Reglas cumplidas:

- **Mejora progresiva:** sin JavaScript no se oculta nada (`html.rv` la pone un script del
  `<head>` y, si el observador no arranca en 3 s, se quita). Tests: `pages.spec.ts › sin
JavaScript`.
- **`prefers-reduced-motion: reduce`:** no hay clase `rv`, no corre ningún bucle, los botones de
  pausa no aparecen y todo está en su estado final (tests `pages.spec.ts › con
prefers-reduced-motion`). Si se activa con la página abierta, todo se muestra al instante.
- **Solo compositor:** `opacity`, `translate`, `scale` y `transform`. Animaciones con
  `backwards` (no queda estilo pegado: el hover responde al instante tras aparecer).
- **WCAG 2.2.2:** lo que dura más de 5 s (salud de routers, rotación del kiosco) tiene botón de
  pausa de 44 px y se detiene fuera de pantalla o con la pestaña oculta; lo demás es finito.
- **Sin dependencias nuevas:** unos pocos KB de JavaScript propio, sin librerías de animación.
- **Rendimiento:** las secciones de la portada siguen con `hydrate-never`; el titular (LCP) nunca
  se anima.
