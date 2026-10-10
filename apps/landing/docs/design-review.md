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
