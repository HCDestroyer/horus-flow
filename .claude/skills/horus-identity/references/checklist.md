# Checklist antes de entregar

**Marca y color**
- [ ] Solo tokens semánticos; ningún color crudo en componentes.
- [ ] `primary` solo en acciones primarias, selección, foco y "en vivo".
- [ ] Una sola acción primaria con halo por vista.
- [ ] Todo estado lleva icono + texto + color, según `estados.md`.
- [ ] Ningún color de estado usado como serie de gráfico.

**Materiales**
- [ ] `.app-aura` montado en el layout.
- [ ] Vidrio solo en la capa funcional; contenido en `.surface-card`; nada de vidrio sobre vidrio.
- [ ] Oscuro sin negro puro (`#0b1014`).

**Tipografía y datos**
- [ ] Fuente del sistema; monoespaciada para IPs/claves/IDs.
- [ ] Cifras tabulares en tablas, KPIs y ejes.
- [ ] Unidades y fechas con el formato de `marca-y-escritura.md`.

**Accesibilidad**
- [ ] Contraste ≥ 4,5:1 (texto), ≥ 3:1 (componentes y foco), ≥ 7:1 en valores clave de mural,
      en claro y en oscuro.
- [ ] Foco visible (`outline 2px primary`), navegación por teclado completa.
- [ ] Botones solo-icono con `aria-label`; gráficos con título, frase resumen y tabla alternativa.
- [ ] Probado con `prefers-reduced-motion`, `prefers-reduced-transparency` y `prefers-contrast: more`.
- [ ] axe sin violaciones.

**Movimiento**
- [ ] Animaciones interrumpibles, sin rebote salvo gestos con impulso.
- [ ] Números ruedan solo cuando el dato cambia (0,7 s).
- [ ] Halos solo en oscuro.
