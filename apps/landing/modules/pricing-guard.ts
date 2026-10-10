// Aviso de build: precios sin confirmar en app/config/pricing.ts.
// Mientras `confirmed` sea false la página muestra "solicita cotización" en lugar de cifras;
// este módulo lo recuerda en cada build de producción (no la bloquea: la landing es útil sin
// cifras). Para que falle, PRICING_REQUIRE_CONFIRMED=1.
import { defineNuxtModule, useLogger } from 'nuxt/kit'
import { pricing } from '../app/config/pricing'

export default defineNuxtModule({
  meta: { name: 'pricing-guard' },
  setup(_options, nuxt) {
    if (nuxt.options.dev || nuxt.options._prepare) return
    if (pricing.confirmed) return
    const logger = useLogger('pricing')
    const msg =
      'Precios SIN CONFIRMAR (app/config/pricing.ts → confirmed: false): la página mostrará ' +
      '"Precio de lanzamiento: solicita cotización" y no publicará importes.'
    if (process.env.PRICING_REQUIRE_CONFIRMED === '1') {
      throw new Error(msg)
    }
    logger.warn(msg)
  },
})
