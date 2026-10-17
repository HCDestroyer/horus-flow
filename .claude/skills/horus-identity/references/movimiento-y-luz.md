# Movimiento y luz

## Luz (solo en oscuro, solo en señales)

| Señal | Implementación |
| --- | --- |
| Acción primaria (una por vista) | `.glow-brand` / sombra `0 0 18px var(--hx-glow-brand)`; hover 26 px |
| Dato en vivo | `.live-dot` + alternar `ring-a`/`ring-b` en cada dato nuevo (anillo 1,2 s) |
| Valor que cambió | alternar `ping-a`/`ping-b` (destello `text-shadow` 1,4 s) |
| Series de gráficos | halo 6–8 px (`shadowBlur` en ECharts) con el color de la serie |
| Crítico abierto | `.halo-critical` en la tarjeta, siempre con icono y texto |

En claro, `--hx-glow-brand` es transparente; con `prefers-contrast: more` todos los halos
desaparecen y el crítico pasa a borde de 2 px.

## Números vivos

Cada dígito rueda hasta su valor nuevo en **0,7 s**, curva `cubic-bezier(0.2, 0.7, 0.2, 1)`,
sin rebote, y solo cuando el dato cambia. El dígito real queda como texto (lector de pantalla,
copiar, pruebas) y la tira 0–9 es un pseudoelemento (`.roll-digit[data-d]`). Cifras tabulares.
Las series se dibujan al aparecer y avanzan con cada dato (morph 0,7 s).

## Paneles y transiciones

- Resortes **sin rebote** (~0,3 s, amortiguación 1,0) para paneles, menús y modales; rebote
  solo si el gesto traía impulso (arrastrar y soltar).
- Entran y salen **por el mismo lado**; los popovers nacen desde su disparador.
- Interrumpibles: nunca bloquear la entrada durante una transición.
- Feedback en `pointerdown`, no en el clic.

## Preferencias del sistema

- `prefers-reduced-motion`: sin aura en movimiento, sin latidos ni destellos; los números
  cambian sin rodar y los gráficos sin animación (regla global en los tokens).
- `prefers-reduced-transparency`: vidrio → sólido, sin aura.
- `prefers-contrast: more`: texto atenuado más oscuro/claro, bordes marcados, sin halos.

## Murales y TVs (NOC)

- Diseño a 1920×1080 que escala uniforme (`--u = min(100dvh/1080, 100dvw/1920)`).
- Halos y aura de baja luminancia (aura al 60 %, deriva de 90 s) para evitar quemado.
- Sin negro puro; nada interactivo; contraste ≥ 7:1 en valores clave.
- Para TVs sin `backdrop-filter`, versión "lite" con materiales sólidos.
