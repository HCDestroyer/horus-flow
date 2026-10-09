/**
 * PLANTILLA de widget (no se registra: las carpetas `_*` se ignoran). Para crear uno:
 *
 * 1. Copia esta carpeta a `app/widgets/<type>/`; `<type>` debe existir en
 *    `packages/schemas/dashboard/v0/widget-types.json` (catálogo C9, generado en
 *    `types/api/widget-catalog.ts` por `pnpm api:generate`).
 * 2. Ajusta el manifiesto: `type`, tamaños (los `default` deben estar en `sizes.allowed`
 *    del catálogo: lo comprueban `pnpm typecheck` y el build), `category`, `placeholder`
 *    y la variante mural (`wall.hide` / `wall.enlarge`).
 * 3. Añade `widgets.<type>.description` (empieza por verbo) y `widgets.<type>.empty` a
 *    `i18n/locales/es.json`.
 * 4. `Widget.vue` recibe `WidgetViewProps` (datos ya válidos: el host resuelve carga,
 *    error, vacío, permiso y frescura). Si los datos llegan en vivo, emite `live(at)`.
 * 5. Opcional: exporta `isEmpty(data)` si "vacío" no es la regla genérica.
 * 6. Añade sus datos a la API simulada (`mocks/widget-data.ts`) y un test.
 */
import { defineWidgetManifest } from '../define'

export default defineWidgetManifest({
  type: 'top_services',
  sizes: {
    normal: { default: { w: 4, h: 4 }, min: { w: 4, h: 4 } },
    wall: { default: { w: 4, h: 4 }, min: { w: 4, h: 4 } },
  },
  description: 'widgets.top_services.description',
  category: 'traffic',
  placeholder: 'bars',
  wall: { hide: ['up_bytes'], enlarge: [] },
})
